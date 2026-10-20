package imapserver

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/message"
	"github.com/mjl-/mox/store"
)

// ../rfc/5256
func (c *conn) cmdSort(tag, cmd string, p *parser) {
	c.cmdxSort(false, tag, cmd, p)
}

func (c *conn) cmdUIDSort(tag, cmd string, p *parser) {
	c.cmdxSort(true, tag, cmd, p)
}

type sortCriterion struct {
	reverse bool
	key     string // ARRIVAL, CC, DATE, FROM, SIZE, SUBJECT, TO
}

func (c *conn) cmdxSort(isUID bool, tag, cmd string, p *parser) {
	// Syntax: SORT (sort-criterion ...) charset search-criteria
	// ../rfc/5256:183
	p.xspace()
	p.xtake("(")
	var criteria []sortCriterion
	for {
		reverse := p.take("REVERSE")
		if reverse {
			p.xspace()
		}
		key := strings.ToUpper(p.xatom())
		switch key {
		case "ARRIVAL", "CC", "DATE", "FROM", "SIZE", "SUBJECT", "TO":
		default:
			xsyntaxErrorf("unsupported sort key %q", key)
		}
		criteria = append(criteria, sortCriterion{reverse, key})
		if p.take(")") {
			break
		}
		p.xspace()
	}
	if len(criteria) == 0 {
		xsyntaxErrorf("need at least one sort criterion")
	}
	p.xspace()
	charset := strings.ToUpper(p.xastring())
	if charset != "US-ASCII" && charset != "UTF-8" {
		xusercodeErrorf("BADCHARSET", "only US-ASCII and UTF-8 supported")
	}
	p.xspace()
	sk := &searchKey{searchKeys: []searchKey{*p.xsearchKey()}}
	for !p.empty() {
		p.xspace()
		sk.searchKeys = append(sk.searchKeys, *p.xsearchKey())
	}
	if c.uidonly && sk.hasSequenceNumbers() {
		xsyntaxCodeErrorf("UIDREQUIRED", "cannot search message sequence numbers with uidonly enabled")
	}

	var msgs []store.Message
	var seqs []msgseq
	c.account.RLock()
	defer c.account.RUnlock()
	c.xdbread(func(tx *bstore.Tx) {
		mb := c.xmailboxID(tx, c.mailboxID)
		q := bstore.QueryTx[store.Message](tx)
		q.FilterNonzero(store.Message{MailboxID: mb.ID})
		q.FilterEqual("Expunged", false)
		q.FilterLess("UID", c.uidnext)
		q.SortAsc("UID")
		var seq msgseq = 1
		msgCount := c.exists
		for m, err := range q.All() {
			xcheckf(err, "list messages")
			if c.searchMatch(tx, msgCount, seq, m, *sk, nil, nil, func() store.UID { return 0 }) {
				msgs = append(msgs, m)
				seqs = append(seqs, seq)
			}
			seq++
		}
	})

	type envPart struct {
		Envelope *message.Envelope
	}
	env := func(m store.Message) *message.Envelope {
		var p envPart
		_ = json.Unmarshal(m.ParsedBuf, &p)
		return p.Envelope
	}
	addr := func(l []message.Address) string {
		if len(l) == 0 {
			return ""
		}
		return strings.ToLower(l[0].User + "@" + l[0].Host)
	}
	value := func(m store.Message, key string) (string, int64, time.Time) {
		switch key {
		case "ARRIVAL":
			return "", 0, m.Received
		case "SIZE":
			return "", m.Size, time.Time{}
		case "SUBJECT":
			return strings.ToLower(m.SubjectBase), 0, time.Time{}
		case "FROM":
			return strings.ToLower(string(m.MsgFromLocalpart) + "@" + m.MsgFromDomain), 0, time.Time{}
		case "DATE":
			if e := env(m); e != nil && !e.Date.IsZero() {
				return "", 0, e.Date
			}
			return "", 0, m.Received
		case "TO":
			if e := env(m); e != nil {
				return addr(e.To), 0, time.Time{}
			}
		case "CC":
			if e := env(m); e != nil {
				return addr(e.CC), 0, time.Time{}
			}
		}
		return "", 0, time.Time{}
	}

	order := make([]int, len(msgs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(i, j int) int {
		a, b := msgs[i], msgs[j]
		for _, cr := range criteria {
			as, an, at := value(a, cr.key)
			bs, bn, bt := value(b, cr.key)
			var c int
			if !at.IsZero() || !bt.IsZero() {
				c = at.Compare(bt)
			} else if an != 0 || bn != 0 {
				c = cmp.Compare(an, bn)
			} else {
				c = strings.Compare(as, bs)
			}
			if c != 0 {
				if cr.reverse {
					return -c
				}
				return c
			}
		}
		return 0
	})

	var s strings.Builder
	for _, i := range order {
		v := uint64(msgs[i].UID)
		if !isUID {
			v = uint64(seqs[i])
		}
		s.WriteString(" ")
		s.WriteString(strconv.FormatUint(v, 10))
	}
	c.xbwritelinef("* SORT%s", s.String())
	c.ok(tag, cmd)
}
