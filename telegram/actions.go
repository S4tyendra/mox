package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mjl-/bstore"
	"github.com/mjl-/sherpa"

	"github.com/mjl-/mox/store"
	"github.com/mjl-/mox/webops"
)

func (s *service) handleCallback(ctx context.Context, cq *callbackQuery) {
	if cq.Message == nil {
		_ = s.client.answerCallback(ctx, cq.ID, "Message gone")
		return
	}
	sess, err := sessionByChat(ctx, cq.Message.Chat.ID)
	if err != nil {
		_ = s.client.answerCallback(ctx, cq.ID, "This chat is not linked")
		return
	}

	parts := strings.Split(cq.Data, ":")
	if len(parts) < 2 {
		_ = s.client.answerCallback(ctx, cq.ID, "Unknown action")
		return
	}
	kind := parts[0]
	tag := ""
	idstr := parts[1]
	if kind == "k" && len(parts) >= 3 {
		tag = parts[1]
		idstr = parts[2]
	}
	msgID, err := strconv.ParseInt(idstr, 10, 64)
	if err != nil || msgID <= 0 {
		_ = s.client.answerCallback(ctx, cq.ID, "Unknown action")
		return
	}

	label, err := s.applyAction(ctx, sess, kind, tag, msgID)
	if err != nil {
		s.log.Infox("telegram mailbox action", err, slog.String("action", kind), slog.Int64("msgid", msgID))
		metricAction.WithLabelValues(kind, "error").Inc()
		_ = s.client.answerCallback(ctx, cq.ID, "Failed: "+clipRunes(err.Error(), 80))
		return
	}
	metricAction.WithLabelValues(kind, "ok").Inc()
	_ = s.client.answerCallback(ctx, cq.ID, label)
	_ = s.client.editReplyMarkup(ctx, cq.Message.Chat.ID, cq.Message.MessageID, &inlineKeyboard{
		InlineKeyboard: [][]inlineButton{{{Text: label}}},
	})
}

func (s *service) applyAction(ctx context.Context, sess Session, kind, tag string, msgID int64) (string, error) {
	acc, err := store.OpenAccount(s.log, sess.AccountName, false)
	if err != nil {
		return "", err
	}
	defer func() {
		err := acc.Close()
		s.log.Check(err, "closing account after telegram action")
	}()

	var label string
	err = runOps(func() {
		switch kind {
		case "a":
			label = "Archived"
			xops.MessageMove(ctx, s.log, acc, []int64{msgID}, "", specialMailboxID(ctx, acc, "Archive"), true)
		case "j":
			label = "Junk"
			xops.MessageMove(ctx, s.log, acc, []int64{msgID}, "", specialMailboxID(ctx, acc, "Junk"), true)
		case "t":
			label = "Trash"
			xops.MessageMove(ctx, s.log, acc, []int64{msgID}, "", specialMailboxID(ctx, acc, "Trash"), true)
		case "s":
			label = "Read"
			xops.MessageFlagsAdd(ctx, s.log, acc, []int64{msgID}, []string{`\seen`})
		case "k":
			organizerCheckTag := strings.ToLower(strings.TrimSpace(tag))
			if organizerCheckTag == "" || strings.HasPrefix(organizerCheckTag, "$") || strings.HasPrefix(organizerCheckTag, `\`) {
				xcheckuserf(ctx, errors.New("invalid tag"), "telegram tag")
			}
			label = "Tagged " + organizerCheckTag
			xops.MessageFlagsAdd(ctx, s.log, acc, []int64{msgID}, []string{organizerCheckTag})
		default:
			xcheckuserf(ctx, errors.New("unknown action"), "telegram action")
		}
	})
	return label, err
}

func specialMailboxID(ctx context.Context, acc *store.Account, role string) int64 {
	var id int64
	xdbread(ctx, acc, func(tx *bstore.Tx) {
		q := bstore.QueryTx[store.Mailbox](tx)
		q.FilterEqual("Expunged", false)
		switch role {
		case "Archive":
			q.FilterEqual("Archive", true)
		case "Junk":
			q.FilterEqual("Junk", true)
		case "Trash":
			q.FilterEqual("Trash", true)
		case "Sent":
			q.FilterEqual("Sent", true)
		default:
			xcheckuserf(ctx, fmt.Errorf("unknown mailbox role %s", role), "looking up mailbox")
		}
		mb, err := q.Get()
		if err == bstore.ErrAbsent {
			xcheckuserf(ctx, errors.New("not configured"), "looking up %s mailbox", role)
		}
		xcheckf(ctx, err, "looking up %s mailbox", role)
		id = mb.ID
	})
	return id
}

type opError struct {
	user bool
	msg  string
}

func (e opError) Error() string { return e.msg }

func xcheckf(ctx context.Context, err error, format string, args ...any) {
	if err == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	panic(opError{false, msg + ": " + err.Error()})
}

func xcheckuserf(ctx context.Context, err error, format string, args ...any) {
	if err == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	panic(opError{true, msg + ": " + err.Error()})
}

func xdbwrite(ctx context.Context, acc *store.Account, fn func(tx *bstore.Tx)) {
	err := acc.DB.Write(ctx, func(tx *bstore.Tx) error {
		fn(tx)
		return nil
	})
	xcheckf(ctx, err, "transaction")
}

func xdbread(ctx context.Context, acc *store.Account, fn func(tx *bstore.Tx)) {
	err := acc.DB.Read(ctx, func(tx *bstore.Tx) error {
		fn(tx)
		return nil
	})
	xcheckf(ctx, err, "transaction")
}

var xops = webops.XOps{
	DBWrite:    xdbwrite,
	Checkf:     xcheckf,
	Checkuserf: xcheckuserf,
}

func runOps(fn func()) (err error) {
	defer func() {
		x := recover()
		if x == nil {
			return
		}
		switch e := x.(type) {
		case opError:
			err = e
		case *sherpa.Error:
			err = errors.New(e.Message)
		default:
			panic(x)
		}
	}()
	fn()
	return
}
