package telegram

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/message"
	"github.com/mjl-/mox/moxio"
	"github.com/mjl-/mox/store"
)

func (s *service) handleChanges(acc *store.Account, comm *store.Comm, changes []store.Change) {
	ctx := context.Background()
	for _, ch := range changes {
		switch c := ch.(type) {
		case store.ChangeAddUID:
			s.notifyAdd(ctx, acc, c)
		case store.ChangeRemoveUIDs:
			comm.RemovalSeen(c)
		}
	}
}

func (s *service) notifyAdd(ctx context.Context, acc *store.Account, c store.ChangeAddUID) {
	log := s.log.With(slog.String("account", acc.Name), slog.Int64("mailboxid", c.MailboxID), slog.Uint64("uid", uint64(c.UID)))

	var m store.Message
	var mb store.Mailbox
	err := acc.DB.Read(ctx, func(tx *bstore.Tx) error {
		var err error
		mb, err = store.MailboxID(tx, c.MailboxID)
		if err != nil {
			return err
		}
		m, err = bstore.QueryTx[store.Message](tx).FilterEqual("Expunged", false).FilterNonzero(store.Message{MailboxID: c.MailboxID, UID: c.UID}).Get()
		return err
	})
	if err != nil {
		log.Infox("looking up message for telegram notify", err)
		metricNotify.WithLabelValues("lookup").Inc()
		return
	}
	if m.Seen {
		return
	}
	if !notifyMailbox(acc, mb) {
		return
	}

	sessions, err := sessionsForAccount(ctx, acc.Name)
	if err != nil {
		log.Infox("listing telegram sessions", err)
		return
	}
	if len(sessions) == 0 {
		return
	}

	text, atts, err := s.formatMessage(acc, m, mb.Name)
	if err != nil {
		log.Infox("formatting message for telegram", err)
		metricNotify.WithLabelValues("format").Inc()
		text = fmt.Sprintf("New message in %s (id %d).", html.EscapeString(mb.Name), m.ID)
	}

	kb := actionKeyboard(m.ID)
	for _, sess := range sessions {
		if !sess.Notify {
			continue
		}
		sent, err := s.client.sendMessage(ctx, sess.ChatID, text, kb, 0)
		if err != nil {
			log.Infox("sending telegram notification", err, slog.Int64("chatid", sess.ChatID))
			metricNotify.WithLabelValues("send").Inc()
			continue
		}
		if err := saveMsgRef(ctx, sess.ChatID, sent.MessageID, acc.Name, m.ID); err != nil {
			log.Infox("saving telegram message ref", err)
		}
		s.sendAttachments(ctx, sess.ChatID, sent.MessageID, atts)
		metricNotify.WithLabelValues("ok").Inc()
	}
}

func notifyMailbox(acc *store.Account, mb store.Mailbox) bool {
	if mb.Sent || mb.Draft || mb.Junk || mb.Trash || mb.Archive {
		return false
	}
	if strings.EqualFold(mb.Name, "Inbox") {
		return true
	}
	conf, ok := acc.Conf()
	if !ok {
		return false
	}
	if conf.Introbox != "" && mb.Name == conf.Introbox {
		return true
	}
	if conf.RejectsMailbox != "" && mb.Name == conf.RejectsMailbox {
		return false
	}
	return false
}

type tgAttach struct {
	Name        string
	ContentType string
	Size        int64
	Path        []int
	Data        []byte // Set when Size <= MaxSendFile and we could read it.
	Link        string
}

func (s *service) formatMessage(acc *store.Account, m store.Message, mailbox string) (string, []tgAttach, error) {
	msgr := acc.MessageReader(m)
	defer func() {
		err := msgr.Close()
		s.log.Check(err, "closing message reader")
	}()
	p, err := m.LoadPart(msgr)
	if err != nil {
		return "", nil, err
	}

	var from, subject string
	if env := p.Envelope; env != nil {
		subject = env.Subject
		if len(env.From) > 0 {
			a := env.From[0]
			if a.Name != "" {
				from = a.Name + " <" + a.User + "@" + a.Host + ">"
			} else {
				from = a.User + "@" + a.Host
			}
		}
	}
	if from == "" {
		from = m.MsgFromLocalpart.String()
		if m.MsgFromDomain != "" {
			from += "@" + m.MsgFromDomain
		}
	}

	preview := ""
	if m.Preview != nil {
		preview = strings.TrimSpace(*m.Preview)
	}
	if preview == "" {
		s, err := p.Preview(s.log)
		if err == nil {
			preview = strings.TrimSpace(s)
		}
	}
	preview = clipRunes(preview, 800)

	atts := s.collectAttachments(p)
	var attLines []string
	for i := range atts {
		a := &atts[i]
		if a.Size > MaxSendFile {
			if s.cfg.BaseURL != "" {
				a.Link = s.fileLink(acc.Name, m.ID, a.Path, a.Name)
				attLines = append(attLines, fmt.Sprintf("📎 %s (%s) — %s", html.EscapeString(a.Name), formatSize(a.Size), html.EscapeString(a.Link)))
			} else {
				attLines = append(attLines, fmt.Sprintf("📎 %s (%s) — too large for Telegram, open in webmail", html.EscapeString(a.Name), formatSize(a.Size)))
			}
		} else {
			attLines = append(attLines, fmt.Sprintf("📎 %s (%s)", html.EscapeString(a.Name), formatSize(a.Size)))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📬 <b>%s</b>\n", html.EscapeString(mailbox))
	fmt.Fprintf(&b, "<b>From:</b> %s\n", html.EscapeString(from))
	if subject != "" {
		fmt.Fprintf(&b, "<b>Subject:</b> %s\n", html.EscapeString(subject))
	}
	fmt.Fprintf(&b, "<b>Date:</b> %s\n", m.Received.Local().Format("2006-01-02 15:04"))
	if preview != "" {
		fmt.Fprintf(&b, "\n%s", html.EscapeString(preview))
	}
	if len(attLines) > 0 {
		fmt.Fprintf(&b, "\n\n%s", strings.Join(attLines, "\n"))
	}
	out := b.String()
	if utf8.RuneCountInString(out) > 4000 {
		out = clipRunes(out, 3990) + "…"
	}
	return out, atts, nil
}

func (s *service) collectAttachments(p message.Part) []tgAttach {
	var atts []tgAttach
	var walk func(part message.Part, path []int)
	walk = func(part message.Part, path []int) {
		mt := part.MediaType + "/" + part.MediaSubType
		for i, sp := range part.Parts {
			walk(sp, append(append([]int{}, path...), i))
		}
		if part.MediaType == "MULTIPART" || mt == "TEXT/PLAIN" || mt == "/" || mt == "TEXT/HTML" {
			if mt == "TEXT/PLAIN" || mt == "/" {
				disp, name, err := part.DispositionFilename()
				if err == nil && strings.EqualFold(disp, "attachment") {
					atts = append(atts, makeAttach(part, path, name))
				}
			}
			return
		}
		_, name, _ := part.DispositionFilename()
		if name == "" {
			name = part.ContentTypeParams["name"]
		}
		if name == "" {
			name = "attachment"
		}
		atts = append(atts, makeAttach(part, path, name))
	}
	walk(p, nil)

	for i := range atts {
		a := &atts[i]
		if a.Size > MaxSendFile || a.Size <= 0 {
			continue
		}
		ap, err := partByPath(p, a.Path)
		if err != nil {
			continue
		}
		buf, err := io.ReadAll(&moxio.LimitReader{R: ap.ReaderUTF8OrBinary(), Limit: MaxSendFile})
		if err != nil {
			s.log.Infox("reading attachment for telegram", err)
			continue
		}
		a.Data = buf
	}
	return atts
}

func makeAttach(p message.Part, path []int, name string) tgAttach {
	ct := strings.ToLower(p.MediaType + "/" + p.MediaSubType)
	if ct == "/" {
		ct = "application/octet-stream"
	}
	if name == "" {
		name = "attachment"
	}
	return tgAttach{Name: name, ContentType: ct, Size: p.DecodedSize, Path: path}
}

func partByPath(p message.Part, path []int) (message.Part, error) {
	cur := p
	for _, i := range path {
		if i < 0 || i >= len(cur.Parts) {
			return message.Part{}, fmt.Errorf("invalid part path")
		}
		cur = cur.Parts[i]
	}
	return cur, nil
}

func (s *service) sendAttachments(ctx context.Context, chatID, replyTo int64, atts []tgAttach) {
	for _, a := range atts {
		if len(a.Data) == 0 {
			continue
		}
		_, err := s.client.sendDocument(ctx, chatID, a.Name, a.ContentType, bytes.NewReader(a.Data), "", replyTo)
		if err != nil {
			s.log.Infox("sending telegram attachment", err, slog.String("filename", a.Name))
		}
	}
}

func (s *service) cmdInbox(ctx context.Context, msg *tgMessage) {
	sess, err := sessionByChat(ctx, msg.Chat.ID)
	if err == bstore.ErrAbsent {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "This chat is not linked. Send /login first.", nil, 0)
		return
	} else if err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not look up the session.", nil, 0)
		return
	}

	acc, err := store.OpenAccount(s.log, sess.AccountName, false)
	if err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not open the mailbox.", nil, 0)
		return
	}
	defer func() {
		err := acc.Close()
		s.log.Check(err, "closing account after /inbox")
	}()

	var msgs []store.Message
	err = acc.DB.Read(ctx, func(tx *bstore.Tx) error {
		mb, err := acc.MailboxFind(tx, "Inbox")
		if err != nil {
			return err
		}
		if mb == nil {
			return fmt.Errorf("inbox not found")
		}
		q := bstore.QueryTx[store.Message](tx)
		q.FilterNonzero(store.Message{MailboxID: mb.ID})
		q.FilterEqual("Expunged", false)
		q.FilterEqual("Seen", false)
		q.SortDesc("Received")
		q.Limit(8)
		msgs, err = q.List()
		return err
	})
	if err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not list Inbox.", nil, 0)
		return
	}
	if len(msgs) == 0 {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "No unread messages in Inbox.", nil, 0)
		return
	}
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("%d unread in Inbox:", len(msgs)), nil, 0)
	for _, m := range msgs {
		text, atts, err := s.formatMessage(acc, m, "Inbox")
		if err != nil {
			text = fmt.Sprintf("Message %d", m.ID)
		}
		sent, err := s.client.sendMessage(ctx, msg.Chat.ID, text, actionKeyboard(m.ID), 0)
		if err != nil {
			continue
		}
		_ = saveMsgRef(ctx, msg.Chat.ID, sent.MessageID, acc.Name, m.ID)
		s.sendAttachments(ctx, msg.Chat.ID, sent.MessageID, atts)
	}
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func formatSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func actionKeyboard(msgID int64) *inlineKeyboard {
	id := fmt.Sprintf("%d", msgID)
	return &inlineKeyboard{InlineKeyboard: [][]inlineButton{
		{
			{Text: "Archive", CallbackData: "a:" + id},
			{Text: "Junk", CallbackData: "j:" + id},
			{Text: "Trash", CallbackData: "t:" + id},
			{Text: "Read", CallbackData: "s:" + id},
		},
	}}
}
