package store

import (
	cryptorand "crypto/rand"
	"strings"
	"time"
	"unicode"

	"github.com/mjl-/bstore"
)

// AddressBook is a per-account contact learned from sent and received mail.
type AddressBook struct {
	ID       int64
	Address  string `bstore:"unique"` // Unicode email address, lower-cased.
	Name     string
	LastUsed time.Time `bstore:"index"`
	Uses     int
}

// Snooze holds a message that should return to a mailbox later.
type Snooze struct {
	ID                int64
	MessageID         int64     `bstore:"unique"`
	Until             time.Time `bstore:"index"`
	ReturnMailboxID   int64
	ReturnMailboxName string
}

// HideAlias is a generated catchall address (you+token@domain) that can be
// disabled without changing DNS or destinations.
type HideAlias struct {
	ID        int64
	Address   string `bstore:"unique"` // Unicode email.
	Localpart string
	Domain    string
	Note      string
	Enabled   bool
	Created   time.Time `bstore:"default now"`
}

// PushSubscription is a Web Push endpoint for this account's browser.
type PushSubscription struct {
	ID       int64
	Endpoint string `bstore:"unique"`
	P256dh   string
	Auth     string
	Created  time.Time `bstore:"default now"`
}

func NormalizeAddress(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func AddressBookUpsert(tx *bstore.Tx, address, name string) error {
	address = NormalizeAddress(address)
	if address == "" || !strings.Contains(address, "@") {
		return nil
	}
	name = strings.TrimSpace(name)
	if strings.ContainsFunc(name, func(r rune) bool { return r < 32 && r != '\t' }) {
		name = ""
	}
	e, err := bstore.QueryTx[AddressBook](tx).FilterNonzero(AddressBook{Address: address}).Get()
	now := time.Now()
	if err == bstore.ErrAbsent {
		return tx.Insert(&AddressBook{Address: address, Name: name, LastUsed: now, Uses: 1})
	}
	if err != nil {
		return err
	}
	e.LastUsed = now
	e.Uses++
	if name != "" && (e.Name == "" || e.Name == e.Address) {
		e.Name = name
	}
	return tx.Update(&e)
}

func AddressBookSuggest(tx *bstore.Tx, q string, limit int) ([]AddressBook, error) {
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	q = strings.ToLower(strings.TrimSpace(q))
	all, err := bstore.QueryTx[AddressBook](tx).SortDesc("LastUsed").Limit(200).List()
	if err != nil {
		return nil, err
	}
	out := make([]AddressBook, 0, limit)
	for _, e := range all {
		if q == "" || strings.Contains(strings.ToLower(e.Address), q) || strings.Contains(strings.ToLower(e.Name), q) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func HideAliasDisabled(tx *bstore.Tx, address string) (bool, error) {
	e, err := bstore.QueryTx[HideAlias](tx).FilterNonzero(HideAlias{Address: NormalizeAddress(address)}).Get()
	if err == bstore.ErrAbsent {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !e.Enabled, nil
}

func RandomAliasToken() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return time.Now().UTC().Format("150405")
	}
	var b [8]byte
	for i, c := range raw {
		b[i] = alphabet[int(c)%len(alphabet)]
	}
	return string(b[:])
}

// AfterDeliver is called after a successful mailbox delivery. Used for web push.
// Must not assume acc stays open; copy what you need or reopen by name.
var AfterDeliver func(accountName string, m Message)

func SafeMailboxName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 200 || strings.HasPrefix(s, "/") || strings.Contains(s, "//") {
		return false
	}
	for _, r := range s {
		if r == 0 || r == '\n' || r == '\r' || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
