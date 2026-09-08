package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/dkim"
	"github.com/mjl-/mox/dns"
	"github.com/mjl-/mox/message"
	"github.com/mjl-/mox/mox-"
	"github.com/mjl-/mox/moxio"
	"github.com/mjl-/mox/queue"
	"github.com/mjl-/mox/smtp"
	"github.com/mjl-/mox/store"
)

func (s *service) handleReply(ctx context.Context, msg *tgMessage) {
	sess, err := sessionByChat(ctx, msg.Chat.ID)
	if err == bstore.ErrAbsent {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "This chat is not linked. Send /login first.", nil, 0)
		return
	} else if err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not look up the session.", nil, 0)
		return
	}

	ref, err := msgRefByTelegram(ctx, msg.Chat.ID, msg.ReplyToMessage.MessageID)
	if err == bstore.ErrAbsent {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "I don't know which email that reply belongs to. Reply to a notification from this bot.", nil, 0)
		return
	} else if err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not look up the original message.", nil, 0)
		return
	}

	body := strings.TrimSpace(msg.Text)
	if body == "" {
		body = strings.TrimSpace(msg.Caption)
	}
	atts, err := s.telegramAttachments(ctx, msg)
	if err != nil {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not download an attachment: "+html.EscapeString(err.Error()), nil, 0)
		return
	}
	if body == "" && len(atts) == 0 {
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Empty reply ignored.", nil, 0)
		return
	}

	if err := s.submitReply(ctx, sess, ref.MessageID, body, atts); err != nil {
		s.log.Infox("telegram email reply", err, slog.String("account", sess.AccountName), slog.Int64("msgid", ref.MessageID))
		_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Could not send the reply: "+html.EscapeString(clipRunes(err.Error(), 200)), nil, 0)
		return
	}
	_, _ = s.client.sendMessage(ctx, msg.Chat.ID, "Reply queued for delivery.", nil, msg.MessageID)
}

type replyFile struct {
	Name        string
	ContentType string
	Data        []byte
}

func (s *service) telegramAttachments(ctx context.Context, msg *tgMessage) ([]replyFile, error) {
	var files []replyFile
	add := func(fileID, name, ctype string, size int64) error {
		if size > MaxSendFile {
			return fmt.Errorf("%s is larger than 20MB; send a link instead", name)
		}
		fi, err := s.client.getFile(ctx, fileID)
		if err != nil {
			return err
		}
		if fi.FileSize > MaxSendFile {
			return fmt.Errorf("%s is larger than 20MB; send a link instead", name)
		}
		buf, err := s.client.downloadFile(ctx, fi.FilePath, MaxSendFile)
		if err != nil {
			return err
		}
		if int64(len(buf)) > MaxSendFile {
			return fmt.Errorf("%s is larger than 20MB; send a link instead", name)
		}
		if name == "" {
			name = "attachment"
		}
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		files = append(files, replyFile{Name: name, ContentType: ctype, Data: buf})
		return nil
	}

	if msg.Document != nil {
		n := msg.Document.FileName
		if err := add(msg.Document.FileID, n, msg.Document.MimeType, msg.Document.FileSize); err != nil {
			return nil, err
		}
	}
	if msg.Video != nil {
		n := msg.Video.FileName
		if n == "" {
			n = "video.mp4"
		}
		ct := msg.Video.MimeType
		if ct == "" {
			ct = "video/mp4"
		}
		if err := add(msg.Video.FileID, n, ct, msg.Video.FileSize); err != nil {
			return nil, err
		}
	}
	if msg.Audio != nil {
		n := msg.Audio.FileName
		if n == "" {
			n = "audio"
		}
		if err := add(msg.Audio.FileID, n, msg.Audio.MimeType, msg.Audio.FileSize); err != nil {
			return nil, err
		}
	}
	if msg.Voice != nil {
		if err := add(msg.Voice.FileID, "voice.ogg", "audio/ogg", msg.Voice.FileSize); err != nil {
			return nil, err
		}
	}
	if len(msg.Photo) > 0 {
		ph := msg.Photo[len(msg.Photo)-1]
		if err := add(ph.FileID, "photo.jpg", "image/jpeg", ph.FileSize); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func (s *service) submitReply(ctx context.Context, sess Session, origID int64, text string, files []replyFile) error {
	acc, err := store.OpenAccount(s.log, sess.AccountName, false)
	if err != nil {
		return err
	}
	defer func() {
		err := acc.Close()
		s.log.Check(err, "closing account after telegram reply")
	}()

	fromAddr, err := smtp.ParseAddress(sess.LoginAddress)
	if err != nil {
		return fmt.Errorf("parsing login address: %v", err)
	}
	if ok, disabled := mox.AllowMsgFrom(acc.Name, fromAddr); disabled {
		return mox.ErrDomainDisabled
	} else if !ok {
		return errors.New("login address not allowed as From")
	}

	var toAddr message.NameAddress
	var origSubject, origMessageID string
	var origRefs []string
	var origDate time.Time
	err = acc.DB.Read(ctx, func(tx *bstore.Tx) error {
		m := store.Message{ID: origID}
		if err := tx.Get(&m); err != nil {
			return err
		}
		if m.Expunged {
			return errors.New("original message was removed")
		}
		msgr := acc.MessageReader(m)
		defer msgr.Close()
		p, err := m.LoadPart(msgr)
		if err != nil {
			return err
		}
		if p.Envelope == nil {
			return errors.New("original message has no headers")
		}
		env := p.Envelope
		origSubject = env.Subject
		origMessageID = env.MessageID
		origDate = env.Date
		if len(env.ReplyTo) > 0 {
			toAddr = nameAddress(env.ReplyTo[0])
		} else if len(env.From) > 0 {
			toAddr = nameAddress(env.From[0])
		} else {
			return errors.New("original message has no From")
		}
		h, err := p.Header()
		if err == nil {
			origRefs = h.Values("References")
			if len(origRefs) == 0 && env.InReplyTo != "" {
				origRefs = []string{env.InReplyTo}
			}
		}
		if origMessageID != "" {
			origRefs = append(origRefs, origMessageID)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := runOps(func() {
		xdbread(ctx, acc, func(tx *bstore.Tx) {
			rcpts := []smtp.Path{{Localpart: toAddr.Address.Localpart, IPDomain: dns.IPDomain{Domain: toAddr.Address.Domain}}}
			msglimit, rcptlimit, err := acc.SendLimitReached(tx, rcpts)
			if msglimit >= 0 {
				xcheckuserf(ctx, errors.New("message limit reached"), "checking outgoing rate")
			} else if rcptlimit >= 0 {
				xcheckuserf(ctx, errors.New("recipient limit reached"), "checking outgoing rate")
			}
			xcheckf(ctx, err, "checking send limit")
		})
	}); err != nil {
		return err
	}

	smtputf8 := fromAddr.Localpart.IsInternational() || toAddr.Address.Localpart.IsInternational()
	smtputf8Addr := smtputf8

	dataFile, err := store.CreateMessageTemp(s.log, "telegram-submit")
	if err != nil {
		return err
	}
	defer store.CloseRemoveTempFile(s.log, dataFile, "telegram reply")

	var xc *message.Composer
	var rcptMsgPrefix string
	var messageID, subj string
	var toPath smtp.Path
	var fp smtp.Path
	now := time.Now()
	err = runOps(func() {
		xc = message.NewComposer(dataFile, 100*1024*1024, smtputf8)
		defer func() {
			x := recover()
			if x == nil {
				return
			}
			if err, ok := x.(error); ok && (errors.Is(err, message.ErrMessageSize) || errors.Is(err, message.ErrCompose)) {
				panic(opError{true, err.Error()})
			}
			panic(x)
		}()

		fromNA := message.NameAddress{Address: fromAddr}
		if conf, ok := acc.Conf(); ok {
			fromNA.DisplayName = conf.FullName
		}
		xc.HeaderAddrs("From", []message.NameAddress{fromNA})
		xc.HeaderAddrs("To", []message.NameAddress{toAddr})
		subj = origSubject
		if !hasReplyPrefix(subj) {
			subj = "Re: " + subj
		}
		if subj != "" {
			xc.Subject(subj)
		}
		messageID = fmt.Sprintf("<%s>", mox.MessageIDGen(smtputf8))
		xc.Header("Message-Id", messageID)
		xc.Header("Date", time.Now().Format(message.RFC5322Z))
		if origMessageID != "" {
			xc.Header("In-Reply-To", origMessageID)
		}
		if len(origRefs) > 0 {
			xc.Header("References", strings.Join(origRefs, "\r\n\t"))
		}
		xc.Header("User-Agent", "mox-telegram")
		xc.Header("MIME-Version", "1.0")

		body := text
		_ = origDate

		if len(files) > 0 {
			mp := multipart.NewWriter(xc)
			xc.Header("Content-Type", fmt.Sprintf(`multipart/mixed; boundary="%s"`, mp.Boundary()))
			xc.Line()
			textBody, ct, cte := xc.TextPart("plain", body)
			th := textproto.MIMEHeader{}
			th.Set("Content-Type", ct)
			th.Set("Content-Transfer-Encoding", cte)
			tp, err := mp.CreatePart(th)
			xcheckf(ctx, err, "adding text part")
			_, err = tp.Write(textBody)
			xcheckf(ctx, err, "writing text part")
			for _, f := range files {
				ah := textproto.MIMEHeader{}
				cd := mime.FormatMediaType("attachment", map[string]string{"filename": f.Name})
				ah.Set("Content-Type", f.ContentType)
				ah.Set("Content-Disposition", cd)
				ah.Set("Content-Transfer-Encoding", "base64")
				ap, err := mp.CreatePart(ah)
				xcheckf(ctx, err, "adding attachment")
				bw := moxio.Base64Writer(ap)
				_, err = bw.Write(f.Data)
				xcheckf(ctx, err, "writing attachment")
				xcheckf(ctx, bw.Close(), "finishing attachment")
			}
			xcheckf(ctx, mp.Close(), "closing multipart")
		} else {
			textBody, ct, cte := xc.TextPart("plain", body)
			xc.Header("Content-Type", ct)
			xc.Header("Content-Transfer-Encoding", cte)
			xc.Line()
			_, err := xc.Write(textBody)
			xcheckf(ctx, err, "writing body")
		}
		xc.Flush()

		fd := fromAddr.Domain
		confDom, _ := mox.Conf.Domain(fd)
		if confDom.Disabled {
			xcheckuserf(ctx, mox.ErrDomainDisabled, "checking domain")
		}
		var msgPrefix string
		selectors := mox.DKIMSelectors(confDom.DKIM)
		if len(selectors) > 0 {
			dkimHeaders, err := dkim.Sign(ctx, s.log.Logger, fromAddr.Localpart, fd, selectors, smtputf8, dataFile)
			xcheckf(ctx, err, "sign dkim")
			msgPrefix = dkimHeaders
		}

		fp = fromAddr.Path()
		toPath = smtp.Path{Localpart: toAddr.Address.Localpart, IPDomain: dns.IPDomain{Domain: toAddr.Address.Domain}}
		recvFrom := message.HeaderCommentDomain(mox.Conf.Static.HostnameDomain, smtputf8)
		recvBy := mox.Conf.Static.HostnameDomain.XName(smtputf8)
		recvID := mox.ReceivedID(mox.CidFromCtx(ctx))
		recvHdr := &message.HeaderWriter{}
		recvHdr.Add(" ", "Received:", "from", recvFrom, "by", recvBy, "id", recvID, "for", "<"+toAddr.Address.Pack(smtputf8)+">;", now.Format(message.RFC5322Z))
		rcptMsgPrefix = recvHdr.String() + msgPrefix
		msgSize := int64(len(rcptMsgPrefix)) + xc.Size
		qm := queue.MakeMsg(fp, toPath, xc.Has8bit, xc.SMTPUTF8, smtputf8Addr, msgSize, messageID, []byte(rcptMsgPrefix), nil, now, subj)
		xcheckf(ctx, queue.Add(ctx, s.log, acc.Name, dataFile, qm), "adding to delivery queue")
	})
	if err != nil {
		return err
	}

	// Append to Sent and mark original as answered.
	ctx = context.WithoutCancel(ctx)
	return runOps(func() {
		acc.WithWLock(func() {
			var changes []store.Change
			xdbwrite(ctx, acc, func(tx *bstore.Tx) {
				modseq, err := acc.NextModSeq(tx)
				xcheckf(ctx, err, "next modseq")

				om := store.Message{ID: origID}
				err = tx.Get(&om)
				xcheckf(ctx, err, "get original message")
				if !om.Expunged {
					oflags := om.Flags
					om.Answered = true
					if !om.Junk && !om.Notjunk {
						om.Notjunk = true
					}
					if om.Flags != oflags {
						om.ModSeq = modseq
						xcheckf(ctx, tx.Update(&om), "updating original flags")
						omb, err := store.MailboxID(tx, om.MailboxID)
						xcheckf(ctx, err, "mailbox of original")
						omb.ModSeq = modseq
						xcheckf(ctx, tx.Update(&omb), "updating original mailbox")
						changes = append(changes, om.ChangeFlags(oflags, omb))
					}
				}

				sentmb, err := bstore.QueryTx[store.Mailbox](tx).FilterEqual("Expunged", false).FilterEqual("Sent", true).Get()
				if err == bstore.ErrAbsent {
					return
				}
				xcheckf(ctx, err, "looking up sent mailbox")
				sentm := store.Message{
					CreateSeq:     modseq,
					ModSeq:        modseq,
					MailboxID:     sentmb.ID,
					MailboxOrigID: sentmb.ID,
					Flags:         store.Flags{Notjunk: true, Seen: true, Answered: true},
					Size:          int64(len(rcptMsgPrefix)) + xc.Size,
					MsgPrefix:     []byte(rcptMsgPrefix),
				}
				err = acc.MessageAdd(s.log, tx, &sentmb, &sentm, dataFile, store.AddOpts{})
				xcheckf(ctx, err, "appending to sent")
				xcheckf(ctx, tx.Update(&sentmb), "updating sent mailbox")
				changes = append(changes, sentm.ChangeAddUID(sentmb), sentmb.ChangeCounts())
			})
			store.BroadcastChanges(acc, changes)
		})
	})
}

func nameAddress(a message.Address) message.NameAddress {
	d, err := dns.ParseDomain(a.Host)
	if err != nil {
		d = dns.Domain{ASCII: a.Host}
	}
	lp, err := smtp.ParseLocalpart(a.User)
	if err != nil {
		lp = smtp.Localpart(a.User)
	}
	return message.NameAddress{DisplayName: a.Name, Address: smtp.NewAddress(lp, d)}
}

func hasReplyPrefix(subject string) bool {
	s := strings.TrimSpace(subject)
	return len(s) >= 3 && strings.EqualFold(s[:3], "re:")
}
