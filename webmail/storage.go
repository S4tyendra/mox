package webmail

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/message"
	"github.com/mjl-/mox/store"
)

type storageAttachment struct {
	Name string
	Type string
	Size int64
}

type storageMessage struct {
	ID             int64
	Mailbox        string
	Subject        string
	From           string
	Received       string
	MessageSize    int64
	AttachmentSize int64
	Attachments    []storageAttachment
}

type storageReport struct {
	TotalSize      int64
	MessageCount   int64
	AttachmentSize int64
	MinSize        int64
	Messages       []storageMessage
}

func partRawSize(p message.Part) int64 {
	if p.EndOffset > p.BodyOffset {
		return p.EndOffset - p.BodyOffset
	}
	return p.DecodedSize
}

func collectStorageAttachments(p message.Part) (atts []storageAttachment, total int64) {
	mt := strings.ToUpper(p.MediaType + "/" + p.MediaSubType)
	if p.MediaType == "MULTIPART" || mt == "MESSAGE/RFC822" || mt == "MESSAGE/GLOBAL" {
		for _, c := range p.Parts {
			a, n := collectStorageAttachments(c)
			atts = append(atts, a...)
			total += n
		}
		if p.Message != nil {
			a, n := collectStorageAttachments(*p.Message)
			atts = append(atts, a...)
			total += n
		}
		return atts, total
	}
	disp := ""
	if p.ContentDisposition != nil {
		disp = strings.ToLower(*p.ContentDisposition)
	}
	isText := p.MediaType == "TEXT" && (p.MediaSubType == "PLAIN" || p.MediaSubType == "HTML")
	if isText && !strings.HasPrefix(disp, "attachment") {
		return nil, 0
	}
	size := partRawSize(p)
	name := ""
	if p.ContentTypeParams != nil {
		name = p.ContentTypeParams["name"]
	}
	if name == "" {
		name = "(unnamed)"
	}
	typ := strings.ToLower(strings.Trim(p.MediaType+"/"+p.MediaSubType, "/"))
	if typ == "/" || typ == "" {
		typ = "application/octet-stream"
	}
	return []storageAttachment{{Name: name, Type: typ, Size: size}}, size
}

func organizerStorage(ctx context.Context, minSize int64, limit int) storageReport {
	if minSize <= 0 {
		minSize = 1024 * 1024
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	rep := storageReport{MinSize: minSize, Messages: []storageMessage{}}
	xdbread(ctx, ri.Account, func(tx *bstore.Tx) {
		du := store.DiskUsage{ID: 1}
		if tx.Get(&du) == nil {
			rep.TotalSize = du.MessageSize
		}
		mailboxes := map[int64]string{}
		qmb := bstore.QueryTx[store.Mailbox](tx)
		qmb.FilterEqual("Expunged", false)
		err := qmb.ForEach(func(mb store.Mailbox) error {
			mailboxes[mb.ID] = mb.Name
			return nil
		})
		xcheckf(ctx, err, "list mailboxes")

		q := bstore.QueryTx[store.Message](tx)
		q.FilterEqual("Expunged", false)
		q.SortDesc("Size")
		n := 0
		err = q.ForEach(func(m store.Message) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			rep.MessageCount++
			if len(m.ParsedBuf) == 0 {
				return nil
			}
			var p message.Part
			if json.Unmarshal(m.ParsedBuf, &p) != nil {
				return nil
			}
			atts, asize := collectStorageAttachments(p)
			if asize < minSize {
				return nil
			}
			rep.AttachmentSize += asize
			if n >= limit {
				return errSearchEnd
			}
			from := ""
			subj := m.SubjectBase
			if p.Envelope != nil {
				if subj == "" {
					subj = p.Envelope.Subject
				}
				if len(p.Envelope.From) > 0 {
					a := p.Envelope.From[0]
					from = a.User + "@" + a.Host
					if a.Name != "" {
						from = a.Name + " <" + from + ">"
					}
				}
			}
			rep.Messages = append(rep.Messages, storageMessage{
				ID:             m.ID,
				Mailbox:        mailboxes[m.MailboxID],
				Subject:        subj,
				From:           from,
				Received:       m.Received.UTC().Format("2006-01-02"),
				MessageSize:    m.Size,
				AttachmentSize: asize,
				Attachments:    atts,
			})
			n++
			return nil
		})
		if err != nil && err != errSearchEnd {
			xcheckf(ctx, err, "scan storage")
		}
	})
	sort.Slice(rep.Messages, func(i, j int) bool {
		return rep.Messages[i].AttachmentSize > rep.Messages[j].AttachmentSize
	})
	return rep
}
