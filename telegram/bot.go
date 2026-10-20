package telegram

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/metrics"
	"github.com/mjl-/mox/store"
)

func webhookSecret(token string) string {
	sum := sha256.Sum256([]byte("mox-telegram-webhook:" + token))
	return fmt.Sprintf("%x", sum[:16])
}

func (s *service) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "405 - method not allowed", http.StatusMethodNotAllowed)
		return
	}
	want := webhookSecret(s.cfg.Token)
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		http.Error(w, "403 - forbidden", http.StatusForbidden)
		return
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "400 - bad request", http.StatusBadRequest)
		return
	}
	var u update
	if err := json.Unmarshal(buf, &u); err != nil {
		http.Error(w, "400 - bad request", http.StatusBadRequest)
		return
	}
	s.handleUpdate(r.Context(), u)
	w.WriteHeader(http.StatusOK)
}

func (s *service) handleUpdate(ctx context.Context, u update) {
	defer func() {
		x := recover()
		if x != nil {
			s.log.Error("unhandled panic handling telegram update", slog.Any("err", x))
			debug.PrintStack()
			metrics.PanicInc(metrics.Telegram)
			metricUpdate.WithLabelValues("panic").Inc()
		}
	}()

	if u.CallbackQuery != nil {
		s.handleCallback(ctx, u.CallbackQuery)
		metricUpdate.WithLabelValues("callback").Inc()
		return
	}
	msg := u.Message
	if msg == nil {
		metricUpdate.WithLabelValues("ignored").Inc()
		return
	}
	if msg.Chat.Type != "private" {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "This bot only works in a private chat.", nil, 0)
		metricUpdate.WithLabelValues("notprivate").Inc()
		return
	}
	s.handleMessage(ctx, msg)
	metricUpdate.WithLabelValues("message").Inc()
}

func (s *service) handleMessage(ctx context.Context, msg *tgMessage) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}

	if msg.ReplyToMessage != nil && !strings.HasPrefix(text, "/") {
		s.handleReply(ctx, msg)
		return
	}

	cmd, arg, _ := strings.Cut(text, " ")
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	arg = strings.TrimSpace(arg)

	switch cmd {
	case "/start":
		s.cmdStart(ctx, msg)
	case "/help":
		s.cmdHelp(ctx, msg)
	case "/login":
		s.cmdLogin(ctx, msg, arg)
	case "/pair":
		s.cmdPair(ctx, msg, arg)
	case "/logout":
		s.cmdLogout(ctx, msg)
	case "/inbox", "/unread":
		s.cmdInbox(ctx, msg)
	case "/digest":
		s.cmdDigest(ctx, msg)
	default:
		if s.continueConvo(ctx, msg, text) {
			return
		}
		if _, err := sessionByChat(ctx, msg.Chat.ID); err == nil {
			_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Unknown command. Send /help for a list, or reply to an email message to send a reply.", nil, 0)
			return
		}
		s.cmdStart(ctx, msg)
	}
}

func (s *service) cmdHelp(ctx context.Context, msg *tgMessage) {
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, helpText(), nil, 0)
}

func helpText() string {
	return strings.TrimSpace(`
<b>Mox mail bot</b>

/start — status
/login — link this chat with email and password
/pair CODE — link with a pairing code from the account page
/logout — unlink this chat
/inbox — recent unread messages
/unread — same as /inbox
/digest — unread subjects as a single summary
/help — this message

Reply to a notification to send an email reply. Attachments under 20MB are included.

Buttons on a message: Archive, Junk, Trash, Read, and tags.
`)
}

func (s *service) cmdStart(ctx context.Context, msg *tgMessage) {
	sess, err := sessionByChat(ctx, msg.Chat.ID)
	if err == nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("Linked to <code>%s</code>. New mail in Inbox is delivered here.\n\n%s", html.EscapeString(sess.LoginAddress), helpText()), nil, 0)
		return
	}
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "This bot delivers your mailbox to Telegram.\n\nSend /login, then your email address, then your password. Or generate a pairing code on the account page and send /pair CODE.\n\n"+helpText(), nil, 0)
}

func (s *service) cmdLogin(ctx context.Context, msg *tgMessage, arg string) {
	if s.loginLocked(msg.From) {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Too many failed login attempts. Try again later.", nil, 0)
		return
	}
	email, pass, _ := strings.Cut(arg, " ")
	email = strings.TrimSpace(email)
	pass = strings.TrimSpace(pass)
	if email != "" && pass != "" {
		s.tryLogin(ctx, msg, email, pass, true)
		return
	}
	if email != "" {
		s.setConvo(msg.Chat.ID, &convo{kind: convoLoginPassword, email: email, expires: time.Now().Add(5 * time.Minute)})
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Send the password for "+html.EscapeString(email)+". It will be deleted from this chat after login.", nil, 0)
		return
	}
	s.setConvo(msg.Chat.ID, &convo{kind: convoLoginEmail, expires: time.Now().Add(5 * time.Minute)})
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Send the email address you use to log in.", nil, 0)
}

func (s *service) continueConvo(ctx context.Context, msg *tgMessage, text string) bool {
	s.Lock()
	cv := s.convos[msg.Chat.ID]
	s.Unlock()
	if cv == nil || time.Now().After(cv.expires) {
		return false
	}
	switch cv.kind {
	case convoLoginEmail:
		email := strings.TrimSpace(text)
		if !strings.Contains(email, "@") {
			_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "That does not look like an email address. Send /login to try again.", nil, 0)
			s.clearConvo(msg.Chat.ID)
			return true
		}
		s.setConvo(msg.Chat.ID, &convo{kind: convoLoginPassword, email: email, expires: time.Now().Add(5 * time.Minute)})
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Send the password. It will be deleted from this chat after login.", nil, 0)
		return true
	case convoLoginPassword:
		s.clearConvo(msg.Chat.ID)
		s.tryLogin(ctx, msg, cv.email, text, true)
		return true
	}
	return false
}

func (s *service) setConvo(chatID int64, cv *convo) {
	s.Lock()
	defer s.Unlock()
	s.convos[chatID] = cv
}

func (s *service) clearConvo(chatID int64) {
	s.Lock()
	defer s.Unlock()
	delete(s.convos, chatID)
}

func (s *service) loginLocked(u *user) bool {
	if u == nil {
		return false
	}
	s.Lock()
	defer s.Unlock()
	f, ok := s.logins[u.ID]
	return ok && time.Now().Before(f.until) && f.n >= 5
}

func (s *service) noteLoginFail(userID int64) {
	s.Lock()
	defer s.Unlock()
	f := s.logins[userID]
	f.n++
	if f.n >= 5 {
		f.until = time.Now().Add(15 * time.Minute)
	}
	s.logins[userID] = f
}

func (s *service) noteLoginOK(userID int64) {
	s.Lock()
	defer s.Unlock()
	delete(s.logins, userID)
}

func (s *service) tryLogin(ctx context.Context, msg *tgMessage, email, password string, deletePass bool) {
	if deletePass {
		_ = s.client.deleteMessage(ctx, msg.Chat.ID, msg.MessageID)
	}
	if s.loginLocked(msg.From) {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Too many failed login attempts. Try again later.", nil, 0)
		return
	}

	var userID int64
	var username string
	if msg.From != nil {
		userID = msg.From.ID
		username = msg.From.Username
	}

	la := store.LoginAttempt{
		AccountName:  "-",
		LoginAddress: email,
		Protocol:     "telegram",
		AuthMech:     "password",
		RemoteIP:     fmt.Sprintf("tg:%d", userID),
		Result:       store.AuthError,
	}
	defer store.LoginAttemptAdd(context.Background(), s.log, la)

	acc, accName, err := store.OpenEmailAuth(s.log, email, password, true)
	if err != nil && errors.Is(err, store.ErrUnknownCredentials) {
		s.noteLoginFail(userID)
		la.Result = store.AuthBadCredentials
		metricLogin.WithLabelValues("badcreds").Inc()
		time.Sleep(time.Second)
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Login failed. Check the address and password, then send /login again.", nil, 0)
		return
	} else if err != nil && errors.Is(err, store.ErrLoginDisabled) {
		la.AccountName = accName
		la.Result = store.AuthLoginDisabled
		metricLogin.WithLabelValues("disabled").Inc()
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Login is disabled for this account.", nil, 0)
		return
	} else if err != nil {
		metricLogin.WithLabelValues("error").Inc()
		s.log.Errorx("telegram login", err, slog.String("email", email))
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Login failed due to a server error.", nil, 0)
		return
	}
	err = acc.Close()
	s.log.Check(err, "closing account after telegram login")

	la.AccountName = accName
	la.Result = store.AuthSuccess
	s.noteLoginOK(userID)
	if err := s.linkSession(ctx, msg.Chat.ID, userID, username, accName, email); err != nil {
		metricLogin.WithLabelValues("error").Inc()
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Logged in, but failed to save the session.", nil, 0)
		return
	}
	metricLogin.WithLabelValues("ok").Inc()
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("Linked to <code>%s</code>. New mail in Inbox will show up here.", html.EscapeString(email)), nil, 0)
}

func (s *service) cmdPair(ctx context.Context, msg *tgMessage, code string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Usage: /pair CODE\nGenerate a code on the account page (Connect).", nil, 0)
		return
	}
	pc := PairCode{Code: code}
	err := DB.Get(ctx, &pc)
	if err == bstore.ErrAbsent || err == nil && time.Now().After(pc.Expires) {
		if err == nil {
			_ = DB.Delete(ctx, &pc)
		}
		metricLogin.WithLabelValues("badpair").Inc()
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Unknown or expired pairing code.", nil, 0)
		return
	} else if err != nil {
		metricLogin.WithLabelValues("error").Inc()
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not check pairing code.", nil, 0)
		return
	}
	_ = DB.Delete(ctx, &pc)

	var userID int64
	var username string
	if msg.From != nil {
		userID = msg.From.ID
		username = msg.From.Username
	}
	store.LoginAttemptAdd(context.Background(), s.log, store.LoginAttempt{
		AccountName:  pc.AccountName,
		LoginAddress: pc.LoginAddress,
		Protocol:     "telegram",
		AuthMech:     "pair",
		RemoteIP:     fmt.Sprintf("tg:%d", userID),
		Result:       store.AuthSuccess,
	})
	if err := s.linkSession(ctx, msg.Chat.ID, userID, username, pc.AccountName, pc.LoginAddress); err != nil {
		metricLogin.WithLabelValues("error").Inc()
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Failed to save the session.", nil, 0)
		return
	}
	metricLogin.WithLabelValues("ok").Inc()
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("Linked to <code>%s</code>. New mail in Inbox will show up here.", html.EscapeString(pc.LoginAddress)), nil, 0)
}

func (s *service) linkSession(ctx context.Context, chatID, userID int64, username, accountName, loginAddress string) error {
	sess := Session{
		ChatID:       chatID,
		UserID:       userID,
		Username:     username,
		AccountName:  accountName,
		LoginAddress: loginAddress,
		Created:      time.Now(),
		Notify:       true,
	}
	existing := Session{ChatID: chatID}
	err := DB.Get(ctx, &existing)
	if err == nil {
		if err := DB.Delete(ctx, &existing); err != nil {
			return err
		}
		if existing.AccountName != accountName {
			s.maybeUnwatch(ctx, existing.AccountName)
		}
	} else if err != bstore.ErrAbsent {
		return err
	}
	if err := DB.Insert(ctx, &sess); err != nil {
		return err
	}
	s.watchAccount(accountName)
	return nil
}

func (s *service) cmdLogout(ctx context.Context, msg *tgMessage) {
	sess, err := sessionByChat(ctx, msg.Chat.ID)
	if err == bstore.ErrAbsent {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "This chat is not linked.", nil, 0)
		return
	} else if err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not look up the session.", nil, 0)
		return
	}
	if err := DB.Delete(ctx, &sess); err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Failed to unlink.", nil, 0)
		return
	}
	s.maybeUnwatch(ctx, sess.AccountName)
	s.clearConvo(msg.Chat.ID)
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Unlinked. Send /login to link again.", nil, 0)
}
