package webmail

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/dns"
	"github.com/mjl-/mox/mox-"
	"github.com/mjl-/mox/store"
	"github.com/mjl-/mox/webops"
)

func organizerContacts(ctx context.Context, q string) []store.AddressBook {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	var list []store.AddressBook
	xdbread(ctx, ri.Account, func(tx *bstore.Tx) {
		var err error
		list, err = store.AddressBookSuggest(tx, q, 12)
		xcheckf(ctx, err, "suggest contacts")
	})
	if list == nil {
		list = []store.AddressBook{}
	}
	return list
}

func organizerIdentities(ctx context.Context) []store.FromAddressSettings {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	var list []store.FromAddressSettings
	xdbread(ctx, ri.Account, func(tx *bstore.Tx) {
		var err error
		list, err = bstore.QueryTx[store.FromAddressSettings](tx).List()
		xcheckf(ctx, err, "list identities")
	})
	if list == nil {
		list = []store.FromAddressSettings{}
	}
	return list
}

func organizerSnooze(ctx context.Context, w Webmail, ids []int64, until time.Time) {
	organizerCheck(ctx, len(ids) == 0 || len(ids) > 500, "select 1 to 500 messages")
	organizerCheck(ctx, until.Before(time.Now().Add(time.Minute)), "snooze at least one minute")
	organizerCheck(ctx, until.After(time.Now().Add(365*24*time.Hour)), "snooze at most one year")
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	var snoozeID int64
	var created []store.Change
	xdbwrite(ctx, ri.Account, func(tx *bstore.Tx) {
		modseq := store.ModSeq(0)
		mb, chl, err := ri.Account.MailboxEnsure(tx, "Snoozed", true, store.SpecialUse{}, &modseq)
		xcheckf(ctx, err, "ensure Snoozed mailbox")
		created = chl
		snoozeID = mb.ID
		for _, id := range ids {
			m := xmessageID(ctx, tx, id)
			s := store.Snooze{MessageID: id, Until: until, ReturnMailboxID: m.MailboxID, ReturnMailboxName: mailboxName(tx, m.MailboxID)}
			old, err := bstore.QueryTx[store.Snooze](tx).FilterNonzero(store.Snooze{MessageID: id}).Get()
			if err == nil {
				s.ID = old.ID
				if s.ReturnMailboxID == snoozeID {
					s.ReturnMailboxID = old.ReturnMailboxID
					s.ReturnMailboxName = old.ReturnMailboxName
				}
				xcheckf(ctx, tx.Update(&s), "update snooze")
			} else if err == bstore.ErrAbsent {
				xcheckf(ctx, tx.Insert(&s), "insert snooze")
			} else {
				xcheckf(ctx, err, "lookup snooze")
			}
		}
	})
	if len(created) > 0 {
		store.BroadcastChanges(ri.Account, created)
	}
	w.MessageMove(ctx, ids, snoozeID, false)
}

func mailboxName(tx *bstore.Tx, id int64) string {
	mb := store.Mailbox{ID: id}
	if tx.Get(&mb) != nil {
		return "Inbox"
	}
	return mb.Name
}

func organizerAliasList(ctx context.Context) []store.HideAlias {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	var list []store.HideAlias
	xdbread(ctx, ri.Account, func(tx *bstore.Tx) {
		var err error
		list, err = bstore.QueryTx[store.HideAlias](tx).SortDesc("Created").List()
		xcheckf(ctx, err, "list hide aliases")
	})
	if list == nil {
		list = []store.HideAlias{}
	}
	return list
}

func organizerAliasSave(ctx context.Context, address, note string, enabled *bool, create bool) store.HideAlias {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	note = strings.TrimSpace(note)
	organizerCheck(ctx, len(note) > 200, "note is too long")
	var out store.HideAlias
	xdbwrite(ctx, ri.Account, func(tx *bstore.Tx) {
		if create {
			addr, lp, domain := hideAliasAddress(ctx, ri)
			out = store.HideAlias{Address: addr, Localpart: lp, Domain: domain, Note: note, Enabled: true, Created: time.Now()}
			xcheckf(ctx, tx.Insert(&out), "create hide alias")
			return
		}
		address = store.NormalizeAddress(address)
		out, err := bstore.QueryTx[store.HideAlias](tx).FilterNonzero(store.HideAlias{Address: address}).Get()
		organizerCheck(ctx, err == bstore.ErrAbsent, "unknown alias")
		xcheckf(ctx, err, "get alias")
		if enabled != nil {
			out.Enabled = *enabled
		}
		if note != "" || enabled != nil {
			out.Note = note
		}
		xcheckf(ctx, tx.Update(&out), "update alias")
	})
	return out
}

func hideAliasAddress(ctx context.Context, ri requestInfo) (address, localpart, domain string) {
	login := ri.LoginAddress
	at := strings.LastIndex(login, "@")
	organizerCheck(ctx, at <= 0, "login address missing")
	lp, dom := login[:at], login[at+1:]
	d, err := dns.ParseDomain(dom)
	xcheckf(ctx, err, "parse domain")
	conf, ok := mox.Conf.Domain(d)
	seps := conf.LocalpartCatchallSeparatorsEffective
	organizerCheck(ctx, !ok || len(seps) == 0, "enable a catchall separator such as + on this domain first")
	sep := seps[0]
	if slices.Contains(seps, "+") {
		sep = "+"
	}
	if i := strings.IndexAny(lp, strings.Join(seps, "")); i >= 0 {
		lp = lp[:i]
	}
	token := store.RandomAliasToken()
	localpart = lp + sep + token
	domain = d.Name()
	address = store.NormalizeAddress(localpart + "@" + domain)
	return
}

func organizerPushSave(ctx context.Context, endpoint, p256dh, auth string, remove bool) {
	organizerCheck(ctx, !strings.HasPrefix(endpoint, "https://") || len(endpoint) > 2048, "invalid push endpoint")
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	xdbwrite(ctx, ri.Account, func(tx *bstore.Tx) {
		s, err := bstore.QueryTx[store.PushSubscription](tx).FilterNonzero(store.PushSubscription{Endpoint: endpoint}).Get()
		if remove {
			if err == nil {
				xcheckf(ctx, tx.Delete(&s), "remove push subscription")
			}
			return
		}
		organizerCheck(ctx, p256dh == "" || auth == "" || len(p256dh) > 200 || len(auth) > 200, "invalid push keys")
		if err == bstore.ErrAbsent {
			s = store.PushSubscription{Endpoint: endpoint, P256dh: p256dh, Auth: auth, Created: time.Now()}
			xcheckf(ctx, tx.Insert(&s), "save push subscription")
		} else {
			xcheckf(ctx, err, "get push subscription")
			s.P256dh, s.Auth = p256dh, auth
			xcheckf(ctx, tx.Update(&s), "update push subscription")
		}
	})
}

func wakeSnoozed() {
	log := pkglog
	for {
		select {
		case <-mox.Shutdown.Done():
			return
		case <-time.After(time.Minute):
		}
		for _, name := range mox.Conf.Accounts() {
			acc, err := store.OpenAccount(log, name, false)
			if err != nil {
				continue
			}
			wakeAccountSnoozes(acc)
			err = acc.Close()
			log.Check(err, "closing account after snooze wake")
		}
	}
}

func wakeAccountSnoozes(acc *store.Account) {
	ctx := mox.Shutdown
	now := time.Now()
	var due []store.Snooze
	acc.DB.Read(ctx, func(tx *bstore.Tx) error {
		q := bstore.QueryTx[store.Snooze](tx)
		q.FilterLessEqual("Until", now)
		due, _ = q.List()
		return nil
	})
	if len(due) == 0 {
		return
	}
	xops := webops.XOps{
		DBWrite: func(ctx context.Context, a *store.Account, fn func(*bstore.Tx)) {
			xdbwrite(ctx, a, fn)
		},
		Checkf:     xcheckf,
		Checkuserf: xcheckuserf,
	}
	acc.WithWLock(func() {
		for _, s := range due {
			destID := s.ReturnMailboxID
			acc.DB.Read(ctx, func(tx *bstore.Tx) error {
				mb := store.Mailbox{ID: destID}
				if tx.Get(&mb) != nil || mb.Expunged {
					if inbox, err := acc.MailboxFind(tx, "Inbox"); err == nil && inbox != nil {
						destID = inbox.ID
					}
				}
				return nil
			})
			func() {
				defer func() { recover() }()
				xops.MessageMove(ctx, pkglog, acc, []int64{s.MessageID}, "", destID, false)
			}()
			acc.DB.Write(ctx, func(tx *bstore.Tx) error {
				return tx.Delete(&s)
			})
		}
	})
}

func StartBackground() {
	go wakeSnoozed()
	go initVAPID()
	go oauthLoad()
}
