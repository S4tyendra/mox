# Mail organizer

Open **Mail → Settings → Inbox organizer** to enable auto-tags, create tags,
edit sender/subject/list-id rules (including file-to-folder), generate
hide-my-email aliases, or organize the existing Inbox. Categories run locally:
important, security, finance, orders, updates, newsletters, promotions. Tags are
IMAP keywords, so other mail clients see them too.

**Keep** moves mail to Inbox and marks it as non-junk. **Junk** moves mail to
Junk and marks it as junk. Both train the configured Mox Bayesian filter. HTML
tokenization now preserves word boundaries across paragraphs/table cells and
learns link hosts without URL query strings. Existing trained data is preserved;
new feedback trains the improved features. Mailing lists are not automatically
treated as spam.

Main folders: Inbox, Sent, Drafts, Junk, Trash. Archive and other existing folders
remain under More folders. Outbox is the outgoing delivery queue; it is not a
separate IMAP folder in this UI.

## Reading and search

- Text view uses a complete HTML-derived alternative when the sender's plain
  version is incomplete. Links remain clickable; original MIME/raw mail remains
  available through MCP.
- HTML without images requests `html?images=none`: the server removes image
  sources and inserts placeholders. The iframe adds its own CSP and strips
  sender scripts/active embeds. HTML + images is an explicit opt-in.
- Unsubscribe is detected from list headers or actual unsubscribe anchors. The
  button confirms the target before opening a link or posting the sender's
  one-click unsubscribe action. No unsubscribe is executed automatically.
- Attachment previews: PDF and common raster images; local text extraction for
  DOCX, XLSX, PPTX, ODF, text/CSV/JSON/XML. Other formats offer download. Preview
  input limit: 10 MB; Office XML decompression limit: 4 MB. No external preview
  service is used.
- Inbox tabs: All, Unread, Starred, Important. Page selection supports batch
  Keep/Junk, read, tags, AI and moves. Read all applies to the current mailbox.
- Filters exposes sender, recipient, subject, UTC date/time, mailbox, status,
  tag and attachment type. Search syntax also supports quoted phrases, exclusions
  and sizes, for example:

  ```text
  from:amazonpay.in after:2026-10-01 before:2026-10-03
  tag:finance has:pdf -is:read
  in:any subject:"payment successful" larger:500kb
  ```

  `in:any` includes every mailbox; `in:all` excludes Junk/Trash/Rejects.
  Search pagination uses the filtered results, not the mailbox's total count.

SSE keeps message additions, removals, flags, counts and mailbox keywords live.
Rows animate insertion/removal and respect reduced-motion preferences.

## AI

**Settings → AI & MCP** accepts an OpenAI-compatible Chat Completions API base
URL, model ID and optional API key. Use a public HTTPS base URL ending at the API
root, typically `/v1`. Redirects and private/reserved IP destinations are blocked.
Keys are stored in the private account database and are never returned to the
browser, SSE or MCP. Leave the key field blank to retain it.
Both legacy `max_tokens` providers and models requiring
`max_completion_tokens` are supported; only an explicit parameter rejection is retried.

Ask AI is available in the sidebar and message toolbar. It starts with up to 30
selected text messages, or the latest 15, including unread, starred and tag state.
MCP-backed tools let it search with filters and pagination, list mailboxes and
read additional text. Use a model supporting Chat Completions function tools.
Requests allow six provider rounds and 16 tool calls within 120 seconds, with
60 KB of initial body text and 120 KB of additional tool results. Bodies are
capped at 8 KB each. Answers render Markdown; validated mail citations open the
referenced message across folders and pages. It can summarize and suggest reply
text. Move proposals show the target and require Apply. Web AI cannot send,
change tags/stars, unsubscribe, or view images. Mail content is untrusted input,
and proposed IDs/actions are validated.

AI opens in a right-side panel with AI and Reply tabs. The history button lists
conversations, and the plus button starts a new one. Follow-ups include the
completed conversation and refresh current mail state through MCP tools.
Chat history and unfinished prompts are saved on this device, separately for
each account. Stop cancels an AI request; Retry retains the previous conversation.
The server accepts up to 100 completed chat turns within its request size limits.
History cannot introduce system roles, and earlier citations are rechecked
against the current account.

Replies, forwards and new messages use the same panel and existing composer.
Switching tabs or closing the panel keeps open editors in memory. The history
button in the Reply tab lists open drafts; Save draft stores a draft on the mail
server. Closing a panel alone does not save that draft across browser reloads.
The draft API stores bodies only, so saving a draft with attachments keeps its
editor open to retain those files until Send.
Sending remains an explicit user action, with duplicate submissions blocked.
On narrow screens the panel occupies the mail area; citation clicks reopen mail.
Bulk actions use icon buttons and the animated folder menu.

## MCP and OAuth

Enable MCP in **Settings → AI & MCP**, then connect an MCP client to:

```text
https://YOUR-MAIL-HOST/webmail/mcp
```

The server uses stateless Streamable HTTP with JSON responses, OAuth
authorization code + mandatory PKCE S256, exact registered redirects, resource
audience binding, and dynamic public-client registration. Discovery on the
webmail listener:

```text
/.well-known/oauth-protected-resource/webmail/mcp
/.well-known/oauth-authorization-server/webmail
```

Custom listener paths are reflected in metadata. HTTPS is required except on
loopback for local development. Supported scopes:

| Scope | Permission |
| --- | --- |
| `mail:read` | Mailboxes, search, mail bodies/headers, attachments/images, tags |
| `mail:move` | Batch mailbox moves |
| `mail:write` | Tags, stars, read flags, Keep/Junk, local organization |
| `mail:send` | Submit outgoing mail through normal Mox checks |

Tools: `mailboxes`, `search`, `read_mail`, `mail_body`, `attachment`, `tags`,
`action`, `create_tag`, `send_mail`. Search accepts the same server Query filters
and returns NextOffset. Pages are limited to 100 messages; actions to 500 IDs;
body/attachment reads to 5 MB. Images return MCP image content. Mail reads do not
mark messages seen. Sending retains Mox's sender, size and rate-limit checks.

The consent page uses the webmail app's login, buttons and animated checkboxes.
It lists all four permissions, preselects the client's requested permissions,
and allows users to select additional permissions or deselect requested ones.
Users can revoke grants in settings. Disabling MCP revokes account grants.
OAuth registrations/grants/tokens are stored in oauth.db and do not expire
until revoked. Only token hashes are retained. Browser Origins must match the
mail server; server/desktop MCP clients use bearer authentication.

Protocol references: [MCP authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization),
[MCP transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports),
[Chat Completions](https://developers.openai.com/api/reference/resources/chat).

## Passkeys

Webmail and account settings can be used with a passkey (WebAuthn) instead of a
password. Add one in **Account → Security → Passkeys** (also in **Mail →
Settings → Read**). Sign in with **Sign in with passkey** on the login screen.

The credential (id, COSE public key, sign counter) is stored in the account
database. Only the public key is kept; the private key never leaves the
authenticator. Registration uses `residentKey: required` and
`userVerification: required`, so a passkey can be found without typing an
address. The relying party id is the host of the mail server, and the origin
must match exactly.

Passkey login reuses the normal login-token cookie, so a third-party site
cannot trigger a login. Password login keeps working as a fallback. IMAP and
SMTP are unchanged: they still use password, SCRAM or TLS client certificates.

## Storage organizer

**Mail → Settings → Storage** lists messages with large attachments (1 MB and
up) and can permanently delete them in bulk. IMAP cannot rewrite one part in
place, so the whole message is removed. Use it to reclaim space from old
attachments.

## Local testing

Production must remain IMAP read-only. All mutation tests use isolated localserve:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o mox-static .
ls -lh mox-static
./mox-static localserve -dir /tmp/mox-organizer-localserve -ip 127.0.0.1
```

Use `-ip` only when creating the directory. For subsequent starts:

```bash
./mox-static localserve -dir /tmp/mox-organizer-localserve
```

Open http://localhost:1080/webmail/ with `mox@localhost` / `moxmoxmox`.
The disposable integration script creates test messages/mailboxes, sends mail,
moves/tags/marks/deletes local mail, and exercises OAuth/MCP and SSE:

```bash
python testdata/organizer/smoke.py
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
cd webmail/ui
bun test tests
bun run build
```

The script hardcodes loopback destinations and never accepts production credentials.
AI behavior is tested with a mock provider. Visual checks remain manual when no
browser surface is available: open a receipt in Text/HTML/HTML + images, preview
a document, use the Inbox tabs, and move a selected row while SSE is connected.
