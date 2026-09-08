package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/config"
	"github.com/mjl-/mox/metrics"
	"github.com/mjl-/mox/mox-"
	"github.com/mjl-/mox/store"
)

var ctxbg = context.Background()

func TestMain(m *testing.M) {
	m.Run()
	if metrics.Panics.Load() > 0 {
		fmt.Println("unhandled panics encountered")
		os.Exit(2)
	}
}

func tcheck(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %s", msg, err)
	}
}

type mockAPI struct {
	mu       sync.Mutex
	messages []sentMsg
}

type sentMsg struct {
	Method string
	Body   map[string]any
	Text   string
}

func (m *mockAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i := strings.LastIndex(r.URL.Path, "/")
	method := r.URL.Path[i+1:]
	body, _ := io.ReadAll(r.Body)
	var obj map[string]any
	_ = json.Unmarshal(body, &obj)
	m.mu.Lock()
	sm := sentMsg{Method: method, Body: obj}
	if t, ok := obj["text"].(string); ok {
		sm.Text = t
	}
	m.messages = append(m.messages, sm)
	n := len(m.messages)
	m.mu.Unlock()

	var result any
	switch method {
	case "getMe":
		result = user{ID: 1, IsBot: true, Username: "moxbot", FirstName: "Mox"}
	case "sendMessage":
		chatID := int64(42)
		if v, ok := obj["chat_id"].(float64); ok {
			chatID = int64(v)
		}
		result = tgMessage{MessageID: int64(100 + n), Chat: chat{ID: chatID, Type: "private"}}
	case "sendDocument":
		result = tgMessage{MessageID: int64(200 + n), Chat: chat{ID: 42, Type: "private"}}
	default:
		result = true
	}
	raw, _ := json.Marshal(result)
	fmt.Fprintf(w, `{"ok":true,"result":%s}`, raw)
}

func (m *mockAPI) texts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, sm := range m.messages {
		if sm.Text != "" {
			out = append(out, sm.Text)
		}
	}
	return out
}

func (m *mockAPI) hasText(sub string) bool {
	for _, s := range m.texts() {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (m *mockAPI) waitText(t *testing.T, sub string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if m.hasText(sub) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for telegram text containing %q, got %q", sub, m.texts())
}

func setup(t *testing.T) (*mockAPI, *store.Account, func()) {
	t.Helper()
	os.RemoveAll("../testdata/telegram/data")
	mox.Context = ctxbg
	mox.ConfigStaticPath = filepath.FromSlash("../testdata/telegram/mox.conf")
	mox.ConfigDynamicPath = filepath.FromSlash("../testdata/telegram/domains.conf")
	mox.MustLoadConfig(true, false)

	mock := &mockAPI{}
	ts := httptest.NewServer(mock)
	t.Cleanup(ts.Close)
	origAPI := APIURL
	APIURL = ts.URL

	mox.Conf.Static.TelegramBot = &config.TelegramBot{
		Token:   "test-token",
		BaseURL: "https://mail.example.com",
		Webhook: "https://mail.example.com/telegram/webhook",
	}

	err := store.Init(ctxbg)
	tcheck(t, err, "store init")
	stopSwitch := store.Switchboard()

	acc, err := store.OpenAccount(pkglog, "mjl", false)
	tcheck(t, err, "open account")
	err = acc.SetPassword(pkglog, "testtesttest")
	tcheck(t, err, "set password")

	err = Start()
	if err != nil {
		_ = acc.Close()
		acc.WaitClosed()
		stopSwitch()
		_ = store.Close()
		t.Fatalf("telegram start: %s", err)
	}

	cleanup := func() {
		err := Close()
		tcheck(t, err, "telegram close")
		err = acc.Close()
		pkglog.Check(err, "closing account")
		acc.WaitClosed()
		stopSwitch()
		err = store.Close()
		tcheck(t, err, "store close")
		os.RemoveAll("../testdata/telegram/data")
		APIURL = origAPI
	}
	return mock, acc, cleanup
}

func TestLoginAndNotify(t *testing.T) {
	mock, acc, cleanup := setup(t)
	defer cleanup()

	if BotUsername() != "moxbot" {
		t.Fatalf("bot username %q", BotUsername())
	}

	s := srv
	s.handleUpdate(ctxbg, update{Message: &tgMessage{
		MessageID: 1,
		From:      &user{ID: 9, Username: "alice"},
		Chat:      chat{ID: 42, Type: "private"},
		Text:      "/start",
	}})
	if !mock.hasText("/login") {
		t.Fatalf("start text: %q", mock.texts())
	}

	s.handleUpdate(ctxbg, update{Message: &tgMessage{
		MessageID: 2,
		From:      &user{ID: 9, Username: "alice"},
		Chat:      chat{ID: 42, Type: "private"},
		Text:      "/login mjl@mox.example testtesttest",
	}})
	mock.waitText(t, "Linked to", 2*time.Second)

	sess, err := sessionByChat(ctxbg, 42)
	tcheck(t, err, "session")
	if sess.AccountName != "mjl" || sess.LoginAddress != "mjl@mox.example" {
		t.Fatalf("session %#v", sess)
	}
	waitWatch(t, "mjl")

	// Deliver a new Inbox message; the store.Comm watcher (same as webmail SSE)
	// should push a Telegram notification.
	deliver(t, acc, "Inbox", "From: sender@remote.example\r\nTo: mjl@mox.example\r\nSubject: hello telegram\r\n\r\nbody of the mail\r\n")
	mock.waitText(t, "hello telegram", 3*time.Second)
	if !mock.hasText("sender@remote.example") {
		t.Fatalf("notification missing from, got %q", mock.texts())
	}
}

func TestPairAndActions(t *testing.T) {
	mock, acc, cleanup := setup(t)
	defer cleanup()

	code, _, err := CreatePairCode(ctxbg, "mjl", "mjl@mox.example")
	tcheck(t, err, "pair code")
	srv.handleUpdate(ctxbg, update{Message: &tgMessage{
		MessageID: 3,
		From:      &user{ID: 11, Username: "bob"},
		Chat:      chat{ID: 43, Type: "private"},
		Text:      "/pair " + code,
	}})
	mock.waitText(t, "Linked to", 2*time.Second)
	waitWatch(t, "mjl")

	deliver(t, acc, "Inbox", "From: other@remote.example\r\nTo: mjl@mox.example\r\nSubject: act on me\r\n\r\nplease archive\r\n")
	mock.waitText(t, "act on me", 3*time.Second)

	msgid := latestInbox(t, acc)
	srv.handleUpdate(ctxbg, update{CallbackQuery: &callbackQuery{
		ID:      "cb1",
		From:    user{ID: 11},
		Message: &tgMessage{MessageID: 1, Chat: chat{ID: 43, Type: "private"}},
		Data:    fmt.Sprintf("s:%d", msgid),
	}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m := store.Message{ID: msgid}
		if acc.DB.Get(ctxbg, &m) == nil && m.Seen {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	m := store.Message{ID: msgid}
	tcheck(t, acc.DB.Get(ctxbg, &m), "get message")
	if !m.Seen {
		t.Fatalf("expected seen after telegram read action")
	}
}

func TestFileToken(t *testing.T) {
	s := &service{cfg: config.TelegramBot{Token: "test-token", BaseURL: "https://mail.example.com"}, log: pkglog}
	link := s.fileLink("mjl", 7, []int{0, 1}, "huge.bin")
	if !strings.HasPrefix(link, "https://mail.example.com/telegram/file/") {
		t.Fatalf("link %s", link)
	}
	tok := strings.Split(strings.TrimPrefix(link, "https://mail.example.com/telegram/file/"), "/")[0]
	c, err := s.verifyFileClaim(tok)
	tcheck(t, err, "verify")
	if c.Acc != "mjl" || c.ID != 7 || c.Name != "huge.bin" {
		t.Fatalf("claim %#v", c)
	}
	if _, err := s.verifyFileClaim("not-a-token"); err == nil {
		t.Fatal("expected error")
	}
}

func TestLargeAttachmentListedAsLink(t *testing.T) {
	s := &service{cfg: config.TelegramBot{Token: "x", BaseURL: "https://mail.example.com"}, log: pkglog}
	link := s.fileLink("mjl", 1, []int{1}, "huge.bin")
	if !strings.Contains(link, "/telegram/file/") {
		t.Fatalf("expected download link, got %q", link)
	}
}

func waitWatch(t *testing.T, account string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := srv
		if s != nil {
			s.Lock()
			_, ok := s.watches[account]
			s.Unlock()
			if ok {
				time.Sleep(50 * time.Millisecond)
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("telegram watch for %s did not start", account)
}

func deliver(t *testing.T, acc *store.Account, mailbox, msg string) {
	t.Helper()
	acc.WithWLock(func() {
		f, err := store.CreateMessageTemp(pkglog, "telegram-test")
		tcheck(t, err, "temp")
		defer store.CloseRemoveTempFile(pkglog, f, "temp")
		_, err = f.Write([]byte(msg))
		tcheck(t, err, "write")
		m := store.Message{Size: int64(len(msg))}
		err = acc.DeliverMailbox(pkglog, mailbox, "", &m, f)
		tcheck(t, err, "deliver")
	})
}

func latestInbox(t *testing.T, acc *store.Account) int64 {
	t.Helper()
	var id int64
	err := acc.DB.Read(ctxbg, func(tx *bstore.Tx) error {
		mb, err := acc.MailboxFind(tx, "Inbox")
		if err != nil {
			return err
		}
		m, err := bstore.QueryTx[store.Message](tx).FilterNonzero(store.Message{MailboxID: mb.ID}).FilterEqual("Expunged", false).SortDesc("ID").Limit(1).Get()
		if err != nil {
			return err
		}
		id = m.ID
		return nil
	})
	tcheck(t, err, "latest inbox")
	return id
}
