// Package telegram implements a Telegram bot for inbox notifications, email
// replies, mailbox actions, and account login.
//
// New-mail notifications use store.Comm, the same per-account change feed as
// webmail SSE and IMAP IDLE. Users authenticate by chatting with the bot
// (email+password or a pairing code from the account page).
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/config"
	"github.com/mjl-/mox/metrics"
	"github.com/mjl-/mox/mlog"
	"github.com/mjl-/mox/mox-"
	"github.com/mjl-/mox/moxvar"
	"github.com/mjl-/mox/store"
)

var pkglog = mlog.New("telegram", nil)

// MaxSendFile is the maximum attachment size we upload to Telegram. Larger
// files are sent as a download link (if BaseURL is configured).
const MaxSendFile = 20 * 1024 * 1024

var (
	metricUpdate = promauto.NewCounterVec(
		prometheus.CounterOpts{Name: "mox_telegram_updates_total", Help: "Telegram updates handled."},
		[]string{"result"},
	)
	metricNotify = promauto.NewCounterVec(
		prometheus.CounterOpts{Name: "mox_telegram_notify_total", Help: "Inbox notifications sent to Telegram."},
		[]string{"result"},
	)
	metricLogin = promauto.NewCounterVec(
		prometheus.CounterOpts{Name: "mox_telegram_login_total", Help: "Telegram login attempts."},
		[]string{"result"},
	)
	metricAction = promauto.NewCounterVec(
		prometheus.CounterOpts{Name: "mox_telegram_actions_total", Help: "Mailbox actions from Telegram."},
		[]string{"action", "result"},
	)
)

// DB and DBTypes are the telegram session database.
var DB *bstore.DB
var DBTypes = []any{Session{}, MsgRef{}, PairCode{}}

var (
	ErrNotConfigured = errors.New("telegram bot not configured")
	ErrNotFound      = errors.New("not found")
)

type service struct {
	log      mlog.Log
	cfg      config.TelegramBot
	username string // Bot username from getMe, without @.
	id       int64  // Bot user id.

	client *apiClient

	cancel context.CancelFunc

	sync.Mutex
	watches map[string]*accountWatch // Account name -> watch.
	convos  map[int64]*convo         // Chat ID -> in-progress login.
	logins  map[int64]loginFail      // Telegram user ID -> recent failures.
}

type accountWatch struct {
	stop chan struct{}
}

type convo struct {
	kind      convoKind
	email     string
	expires   time.Time
	askedPass bool
}

type convoKind int

const (
	convoNone convoKind = iota
	convoLoginEmail
	convoLoginPassword
)

type loginFail struct {
	n     int
	until time.Time
}

var srv *service

func configured() *config.TelegramBot {
	return mox.Conf.Static.TelegramBot
}

// Configured reports whether a Telegram bot is enabled.
func Configured() bool {
	tb := configured()
	return tb != nil && tb.Token != ""
}

// BotUsername returns the bot's Telegram username without @, or empty if the
// bot is not running.
func BotUsername() string {
	s := srv
	if s == nil {
		return ""
	}
	s.Lock()
	defer s.Unlock()
	return s.username
}

// Start opens telegram.db, resolves the bot identity, registers store.Comm
// watchers for linked accounts (the same mechanism as webmail SSE), and starts
// either long polling or webhook handling.
func Start() error {
	tb := configured()
	if tb == nil || tb.Token == "" {
		return nil
	}

	log := pkglog
	p := mox.DataDirPath("telegram.db")
	os.MkdirAll(filepath.Dir(p), 0770)
	opts := bstore.Options{Timeout: 5 * time.Second, Perm: 0660, RegisterLogger: moxvar.RegisterLogger(p, log.Logger)}
	db, err := bstore.Open(mox.Context, p, &opts, DBTypes...)
	if err != nil {
		return fmt.Errorf("open telegram.db: %v", err)
	}

	ctx, cancel := context.WithCancel(mox.Shutdown)
	s := &service{
		log:     log,
		cfg:     *tb,
		client:  newAPIClient(tb.Token),
		cancel:  cancel,
		watches: map[string]*accountWatch{},
		convos:  map[int64]*convo{},
		logins:  map[int64]loginFail{},
	}

	me, err := s.client.getMe(ctx)
	if err != nil {
		errc := db.Close()
		log.Check(errc, "closing telegram.db after getMe failure")
		cancel()
		return fmt.Errorf("telegram getMe: %v", err)
	}
	s.username = me.Username
	s.id = me.ID
	log.Info("telegram bot started", slog.String("username", me.Username), slog.Int64("id", me.ID))

	DB = db
	srv = s

	if err := s.startWatches(ctx); err != nil {
		log.Errorx("starting telegram account watchers", err)
	}

	_ = s.client.setMyCommands(ctx, []botCommand{
		{Command: "start", Description: "Status and how to log in"},
		{Command: "login", Description: "Link this chat to a mailbox"},
		{Command: "logout", Description: "Unlink this chat"},
		{Command: "inbox", Description: "Recent unread messages"},
		{Command: "unread", Description: "Recent unread messages"},
		{Command: "digest", Description: "Unread subject list"},
		{Command: "help", Description: "Commands and tips"},
	})

	if tb.Webhook != "" {
		secret := webhookSecret(tb.Token)
		if err := s.client.setWebhook(ctx, tb.Webhook, secret); err != nil {
			log.Errorx("setting telegram webhook", err)
		}
	} else {
		if err := s.client.deleteWebhook(ctx); err != nil {
			log.Infox("clearing telegram webhook before polling", err)
		}
		go s.poll(ctx)
	}

	go s.cleanupLoop(ctx)
	go s.digestLoop(ctx)
	return nil
}

// Close stops the bot and closes telegram.db.
func Close() error {
	s := srv
	if s == nil && DB == nil {
		return nil
	}
	if s != nil {
		s.cancel()
		s.Lock()
		for _, w := range s.watches {
			close(w.stop)
		}
		s.watches = map[string]*accountWatch{}
		s.Unlock()
		srv = nil
	}
	if DB != nil {
		err := DB.Close()
		DB = nil
		return err
	}
	return nil
}

func (s *service) startWatches(ctx context.Context) error {
	sessions, err := bstore.QueryDB[Session](ctx, DB).List()
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, sess := range sessions {
		if _, ok := seen[sess.AccountName]; ok {
			continue
		}
		seen[sess.AccountName] = struct{}{}
		s.watchAccount(sess.AccountName)
	}
	return nil
}

func (s *service) watchAccount(accountName string) {
	s.Lock()
	defer s.Unlock()
	if _, ok := s.watches[accountName]; ok {
		return
	}
	w := &accountWatch{stop: make(chan struct{})}
	s.watches[accountName] = w
	go s.runWatch(accountName, w.stop)
}

func (s *service) unwatchAccount(accountName string) {
	s.Lock()
	w, ok := s.watches[accountName]
	if ok {
		delete(s.watches, accountName)
	}
	s.Unlock()
	if ok {
		close(w.stop)
	}
}

func (s *service) runWatch(accountName string, stop <-chan struct{}) {
	defer func() {
		x := recover()
		if x != nil {
			s.log.Error("unhandled panic in telegram account watch", slog.Any("err", x), slog.String("account", accountName))
			debug.PrintStack()
			metrics.PanicInc(metrics.Telegram)
		}
	}()

	log := s.log.With(slog.String("account", accountName))
	acc, err := store.OpenAccount(log, accountName, false)
	if err != nil {
		log.Errorx("open account for telegram watch", err)
		return
	}
	defer func() {
		err := acc.Close()
		log.Check(err, "closing account after telegram watch")
	}()

	// RegisterComm is the same subscription webmail SSE uses. It blocks until
	// store.Switchboard is running.
	comm := store.RegisterComm(acc)
	defer comm.Unregister()
	log.Debug("telegram watching account via store.Comm")

	for {
		select {
		case <-stop:
			return
		case <-mox.Shutdown.Done():
			return
		case <-comm.Pending:
			overflow, changes := comm.Get()
			if overflow {
				log.Info("telegram change buffer overflow; some updates were dropped")
			}
			s.handleChanges(acc, comm, changes)
		}
	}
}

func (s *service) poll(ctx context.Context) {
	defer func() {
		x := recover()
		if x != nil {
			s.log.Error("unhandled panic in telegram poll", slog.Any("err", x))
			debug.PrintStack()
			metrics.PanicInc(metrics.Telegram)
		}
	}()

	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := s.client.getUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Infox("telegram getUpdates", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			s.handleUpdate(ctx, u)
		}
	}
}

func (s *service) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	s.cleanup(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.cleanup(ctx)
		}
	}
}

func (s *service) cleanup(ctx context.Context) {
	if DB == nil {
		return
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	n, err := bstore.QueryDB[MsgRef](ctx, DB).FilterLess("Created", cutoff).Delete()
	s.log.Check(err, "cleaning old telegram message refs", slog.Int("removed", n))
	n, err = bstore.QueryDB[PairCode](ctx, DB).FilterLess("Expires", time.Now()).Delete()
	s.log.Check(err, "cleaning expired telegram pairing codes", slog.Int("removed", n))
}

// HandleHTTP serves /telegram/webhook and /telegram/file/... on HTTP listeners.
func HandleHTTP(w http.ResponseWriter, r *http.Request) {
	s := srv
	if s == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.URL.Path == "/telegram/webhook":
		s.handleWebhook(w, r)
	case strings.HasPrefix(r.URL.Path, "/telegram/file/"):
		s.handleFile(w, r)
	default:
		http.NotFound(w, r)
	}
}
