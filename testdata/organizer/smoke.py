#!/usr/bin/env python3
"""Disposable localserve integration test. Never connects to production."""
import base64
import hashlib
import http.cookiejar
import imaplib
import io
import json
import smtplib
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from email.message import EmailMessage
from email.utils import formatdate

BASE = "http://localhost:1080/webmail"
jar = http.cookiejar.CookieJar()
browser = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
csrf = ""


def request(path, value=None, headers=None, method=None, opener=browser):
    data = None if value is None else json.dumps(value).encode()
    hdrs = {"Content-Type": "application/json", "x-mox-csrf": csrf, **(headers or {})}
    req = urllib.request.Request(BASE + path, data=data, headers=hdrs, method=method)
    try:
        with opener.open(req, timeout=20) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as e:
        return e.code, json.load(e)


def api(name, *args):
    status, result = request("/api/" + name, {"params": args})
    assert status == 200 and not result.get("error"), (name, status, result)
    return result.get("result")


def organize(path, value=None):
    status, result = request("/organizer/" + path, value)
    assert status == 200 and not (isinstance(result, dict) and result.get("error")), (path, status, result)
    return result


prep = api("LoginPrep")
csrf = api("Login", prep, "mox@localhost", "moxmoxmox")
assert isinstance(csrf, str), csrf
stamp = str(time.time_ns())
sender = "receipt-" + stamp + "@localhost"
prefs = organize("preferences")
prefs.update(Enabled=True, MCPEnabled=True)
prefs["Tags"] += ["local-test"] if "local-test" not in prefs["Tags"] else []
prefs["Rules"] = [{"From": sender, "Subject": "", "Tag": "local-test", "Important": True}]
organize("preferences", prefs)
assert "AIKey" not in organize("preferences")

# Four messages cover incomplete text alternatives, local auto-tags, list
# headers, inline images, document previews, and developer/security categories.
doc = io.BytesIO()
with zipfile.ZipFile(doc, "w") as z:
    z.writestr("word/document.xml", '<w:document xmlns:w="urn:w"><w:body><w:p><w:r><w:t>Invoice 50.00</w:t></w:r></w:p></w:body></w:document>')
png = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
for i, subject in enumerate(["Rs 50.00 was paid on Amazon.in", "Your login request", "Weekly newsletter", "Deployment failed"]):
    msg = EmailMessage()
    msg["From"] = sender
    msg["To"] = "mox@localhost"
    msg["Subject"] = subject + " local-test-" + stamp
    msg["Date"] = formatdate(localtime=True)
    msg["Message-ID"] = f"<organizer-{stamp}-{i}@localhost>"
    msg["List-Unsubscribe"] = "<https://example.org/unsubscribe>, <mailto:leave@example.org>"
    msg["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
    msg.set_content("Thanks for your payment.")
    msg.add_alternative('<html><body><p>Payment successful</p><table><tr><td>Amount</td><td>₹50.00</td></tr><tr><td>Order</td><td>D01-123</td></tr></table><a href="https://example.org/statement">View statement</a><img alt="Remote logo" src="https://example.org/image.png"><img src="cid:logo"><script>parent.document.body.innerHTML="bad"</script></body></html>', subtype="html")
    msg.get_payload()[1].add_related(png, maintype="image", subtype="png", cid="<logo>")
    if i == 0:
        msg.add_attachment(doc.getvalue(), maintype="application", subtype="vnd.openxmlformats-officedocument.wordprocessingml.document", filename="invoice.docx")
    with smtplib.SMTP("127.0.0.1", 1025, timeout=10) as smtp:
        smtp.send_message(msg)


def search(limit=100, offset=0, extra=None):
    f = {"MailboxID": 0, "Subject": ["local-test-" + stamp], **(extra or {})}
    return organize("search", {"Query": {"Filter": f, "NotFilter": {}}, "Limit": limit, "Offset": offset})


for _ in range(30):
    result = search()
    if len(result["Messages"]) == 4:
        break
    time.sleep(0.1)
assert len(result["Messages"]) == 4, result
items = result["Messages"]
ids = [m["Message"]["ID"] for m in items]
receipt = next(m for m in items if "Rs 50" in m["Envelope"]["Subject"])
rid = receipt["Message"]["ID"]
assert {"finance", "important", "local-test"}.issubset(receipt["Message"]["Keywords"]), receipt
read = organize("message", {"ID": rid})
text = "\n".join(read["Parsed"]["Texts"])
assert "₹50.00" in text and "D01-123" in text and "<https://example.org/statement>" in text, text
assert len(read["Unsubscribe"]) == 2 and read["OneClick"]
assert "List-Unsubscribe" in read["Parsed"]["Headers"]
with browser.open(BASE + f"/msg/{rid}/html?images=none") as r:
    blocked = r.read().decode()
    assert "<img" not in blocked and "Image blocked" in blocked and "image.png" not in blocked and "parent.document" not in blocked
with browser.open(BASE + f"/msg/{rid}/htmlexternal") as r:
    enabled = r.read().decode()
    assert "<img" in enabled and "image.png" in enabled and "data:image/png" in enabled
assert search(2)["NextOffset"] == 2
assert len(search(2, 2)["Messages"]) == 2
assert len(search(extra={"Labels": ["finance"]})["Messages"]) == 1
assert search(extra={"Oldest": "2100-01-01T00:00:00Z"})["Messages"] == []
attachment = next(a for a in receipt["Attachments"] if a["Filename"] == "invoice.docx")
assert "Invoice 50.00" in organize("preview", {"ID": rid, "Path": attachment["Path"]})["Text"]
print("PASS receive, auto-tags, receipt conversion, hyperlinks, unsubscribe, filtered pagination, document preview")

# Collect mailbox IDs from the authenticated SSE start event and verify actual
# changes reach an existing connection, including changes performed through MCP.
sse_data = []
ready = threading.Event()
stop = threading.Event()
sse_token = api("Token")
params = {"ID": 1, "SSEID": 0, "ViewID": 1, "Cancel": False, "Query": {"Filter": {"MailboxName": "Inbox"}, "NotFilter": {}}, "Page": {"Count": 100}}
url = BASE + "/events?" + urllib.parse.urlencode({"singleUseToken": sse_token, "request": json.dumps(params)})


def events():
    with browser.open(url, timeout=15) as response:
        event = ""
        for raw in response:
            line = raw.decode().strip()
            if line.startswith("event:"):
                event = line[6:].strip()
            elif line.startswith("data:"):
                sse_data.append((event, json.loads(line[5:].strip())))
                if event == "start":
                    ready.set()
            if stop.is_set():
                break


thread = threading.Thread(target=events, daemon=True)
thread.start()
assert ready.wait(10), sse_data
start = next(data for event, data in sse_data if event == "start")
mbs = start["Mailboxes"]
inbox = next(m["ID"] for m in mbs if m["Name"] == "Inbox")
junk = next(m["ID"] for m in mbs if m["Junk"])
trash = next(m["ID"] for m in mbs if m["Trash"])
test_mailbox = "Organizer local test " + stamp
api("MailboxCreate", test_mailbox)
for action in ["star", "unstar", "read", "unread"]:
    organize("action", {"Action": action, "IDs": [rid]})
organize("action", {"Action": "tag", "IDs": ids, "Tags": ["batch-test"]})
assert len(search(extra={"Labels": ["batch-test"]})["Messages"]) == 4
organize("action", {"Action": "untag", "IDs": ids, "Tags": ["batch-test"]})
organize("action", {"Action": "junk", "IDs": [rid]})
junked = organize("message", {"ID": rid})["Item"]["Message"]
assert junked["Junk"] and junked["TrainedJunk"] is True
organize("action", {"Action": "keep", "IDs": [rid]})
kept = organize("message", {"ID": rid})["Item"]["Message"]
assert kept["MailboxID"] == inbox and kept["Notjunk"] and not kept["Junk"] and not kept["Seen"] and kept["TrainedJunk"] is False, kept
organize("action", {"Action": "move", "IDs": [rid], "MailboxID": trash})
organize("action", {"Action": "move", "IDs": [rid], "MailboxID": inbox})
print("PASS mailbox create, batch tags, star/read toggles, Keep/Junk feedback, moves preserving unread")

# OAuth DCR + PKCE, code replay, narrow permissions, refresh rotation, revocation.
status, client = request("/oauth/register", {"client_name": "Local smoke client", "redirect_uris": ["http://localhost:7777/callback"]})
assert status == 201, client
verifier = "x" * 64
challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
resource = BASE + "/mcp"
authorize = {"response_type": "code", "client_id": client["client_id"], "redirect_uri": "http://localhost:7777/callback", "code_challenge": challenge, "code_challenge_method": "S256", "resource": resource, "scope": "mail:read", "state": "local-smoke"}
auth_path = "/oauth/authorize?" + urllib.parse.urlencode(authorize)
status, details = request(auth_path, headers={"Accept": "application/json"})
assert status == 200 and details["RequestedScopes"] == ["mail:read"] and set(details["Scopes"]) == {"mail:read", "mail:move", "mail:write", "mail:send"}, details
with browser.open(BASE + auth_path) as page:
    shell = page.read().decode()
    assert '<base href="/webmail/">' in shell and '<div id="root">' in shell and 'type="checkbox"' not in shell, shell
status, invalid = request(auth_path, {"allow": True, "scopes": ["mail:admin"]})
assert status == 400 and invalid["error"] == "invalid_scope", invalid
status, consent = request(auth_path, {"allow": True, "scopes": ["mail:read", "mail:move"]})
assert status == 200, consent
redirect = urllib.parse.parse_qs(urllib.parse.urlsplit(consent["redirect"]).query)
assert redirect["state"] == ["local-smoke"]
code = redirect["code"][0]


def token(form):
    data = urllib.parse.urlencode(form).encode()
    req = urllib.request.Request(BASE + "/oauth/token", data=data, headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        with browser.open(req, timeout=15) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as e:
        return e.code, json.load(e)


form = {"grant_type": "authorization_code", "code": code, "code_verifier": verifier, "redirect_uri": authorize["redirect_uri"], "client_id": client["client_id"], "resource": resource}
assert token({**form, "code_verifier": "bad"})[0] == 400
status, tokens = token(form)
assert status == 200, tokens
assert token(form)[0] == 400
access = tokens["access_token"]


def rpc(method, params=None):
    status, result = request("/mcp", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params or {}}, {"Authorization": "Bearer " + access})
    assert status == 200 and "result" in result, result
    return result["result"]


def tool(name, args=None):
    return rpc("tools/call", {"name": name, "arguments": args or {}})


assert rpc("initialize", {"protocolVersion": "2025-11-25"})["protocolVersion"] == "2025-11-25"
assert len(rpc("tools/list")["tools"]) >= 9
assert not tool("read_mail", {"ID": rid})["isError"]
assert "₹50.00" in tool("mail_body", {"ID": rid, "Format": "html"})["content"][0]["text"]
assert "List-Unsubscribe" in tool("mail_body", {"ID": rid, "Format": "raw"})["content"][0]["text"]
img = next(a for a in receipt["Attachments"] if a["Part"]["MediaType"] == "IMAGE")
assert tool("attachment", {"ID": rid, "Path": img["Path"]})["content"][0]["type"] == "image"
assert tool("action", {"Action": "star", "IDs": [rid]})["isError"]
assert tool("send_mail", {"Message": {"From": "mox@localhost", "To": ["mox@localhost"], "Subject": "must not send", "TextBody": "blocked"}})["isError"]
assert not tool("action", {"Action": "move", "IDs": [rid], "MailboxID": junk})["isError"]
assert not tool("action", {"Action": "move", "IDs": [rid], "MailboxID": inbox})["isError"]
status, refreshed = token({"grant_type": "refresh_token", "refresh_token": tokens["refresh_token"], "client_id": client["client_id"], "resource": resource})
assert status == 200, refreshed
assert token({"grant_type": "refresh_token", "refresh_token": tokens["refresh_token"], "client_id": client["client_id"], "resource": resource})[0] == 400
grants = organize("grants")
organize("revoke", {"ID": grants[-1]["ID"]})
status, _ = request("/mcp", {"jsonrpc": "2.0", "id": 1, "method": "ping"}, {"Authorization": "Bearer " + access})
assert status == 401
print("PASS OAuth PKCE, replay prevention, MCP full bodies/images, permission boundaries, refresh rotation, revocation")

# An independently consented grant can perform write/send actions locally.
status, consent = request(auth_path, {"allow": True, "scopes": ["mail:read", "mail:move", "mail:write", "mail:send"]})
assert status == 200, consent
newcode = urllib.parse.parse_qs(urllib.parse.urlsplit(consent["redirect"]).query)["code"][0]
status, full = token({**form, "code": newcode})
assert status == 200, full
access = full["access_token"]
assert not tool("create_tag", {"Tag": "mcp-test"})["isError"]
assert not tool("action", {"Action": "tag", "IDs": ids, "Tags": ["mcp-test"]})["isError"]
assert not tool("action", {"Action": "star", "IDs": ids})["isError"]
assert len(search(extra={"Labels": ["mcp-test", "\\flagged"]})["Messages"]) == 4
assert not tool("send_mail", {"Message": {"From": "mox@localhost", "To": ["mox@localhost"], "Subject": "MCP sent local-test-" + stamp, "TextBody": "Local MCP send"}})["isError"]
print("PASS independently consented MCP batch write and send")

# Send through normal webmail submission, which localserve delivers only locally.
api("MessageSubmit", {"From": "mox@localhost", "To": ["recipient@localhost"], "Subject": "Organizer sent local-test-" + stamp, "TextBody": "Local smoke send"})
for _ in range(50):
    if len(search()["Messages"]) >= 5:
        break
    time.sleep(0.1)
assert len(search()["Messages"]) >= 5
api("MessageDelete", [ids[-1]])
assert len(search()["Messages"]) >= 4
imap = imaplib.IMAP4("127.0.0.1", 1143)
imap.login("mox@localhost", "moxmoxmox")
assert imap.delete(test_mailbox)[0] == "OK"
imap.logout()
assert any("ChangeMsgRemove" in json.dumps(data) for event, data in sse_data if event == "viewChanges"), sse_data
stop.set()
print("PASS local send/receive, delete, SSE change events")
print("ALL LOCAL SMOKE CHECKS PASSED")
