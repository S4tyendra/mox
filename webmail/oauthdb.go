package webmail

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/mox-"
	"github.com/mjl-/mox/moxvar"
	"github.com/mjl-/mox/store"
)

type oauthClientRec struct {
	ID        string `bstore:"unique"`
	Name      string
	Redirects []string
}

type oauthGrantRec struct {
	ID         string `bstore:"unique"`
	ClientID   string
	ClientName string
	Account    string `bstore:"index"`
	Address    string
	Session    store.SessionToken
	Scopes     []string
	Resource   string
	Revoked    bool
}

type oauthTokenRec struct {
	Hash    string `bstore:"unique"`
	GrantID string `bstore:"index"`
	Refresh bool
}

var oauthDB *bstore.DB

func oauthLoad() {
	p := mox.DataDirPath("oauth.db")
	os.MkdirAll(filepath.Dir(p), 0770)
	opts := bstore.Options{Timeout: 5 * time.Second, Perm: 0660, RegisterLogger: moxvar.RegisterLogger(p, pkglog.Logger)}
	db, err := bstore.Open(mox.Shutdown, p, &opts, oauthClientRec{}, oauthGrantRec{}, oauthTokenRec{})
	if err != nil {
		pkglog.Errorx("open oauth.db", err)
		return
	}
	oauthDB = db
	oauthState.Lock()
	defer oauthState.Unlock()
	clients, err := bstore.QueryDB[oauthClientRec](mox.Shutdown, db).List()
	if err != nil {
		pkglog.Errorx("load oauth clients", err)
		return
	}
	grants, err := bstore.QueryDB[oauthGrantRec](mox.Shutdown, db).List()
	if err != nil {
		pkglog.Errorx("load oauth grants", err)
		return
	}
	tokens, err := bstore.QueryDB[oauthTokenRec](mox.Shutdown, db).List()
	if err != nil {
		pkglog.Errorx("load oauth tokens", err)
		return
	}
	never := time.Now().Add(100 * 365 * 24 * time.Hour)
	for _, c := range clients {
		oauthState.Clients[c.ID] = oauthClient{c.ID, c.Name, c.Redirects, never}
	}
	byID := map[string]*oauthGrant{}
	for _, g := range grants {
		grant := &oauthGrant{g.ID, g.ClientID, g.ClientName, g.Account, g.Address, g.Session, g.Scopes, g.Resource, never, g.Revoked}
		oauthState.Grants[g.ID] = grant
		byID[g.ID] = grant
	}
	for _, t := range tokens {
		if g := byID[t.GrantID]; g != nil {
			oauthState.Tokens[t.Hash] = oauthToken{g, never, t.Refresh}
		}
	}
}

func oauthPutClientLocked(c oauthClient) {
	oauthState.Clients[c.ID] = c
	if oauthDB == nil {
		return
	}
	rec := oauthClientRec{c.ID, c.Name, c.Redirects}
	err := oauthDB.Write(mox.Shutdown, func(tx *bstore.Tx) error {
		old := oauthClientRec{ID: c.ID}
		if tx.Get(&old) == nil {
			rec = old
			rec.Name, rec.Redirects = c.Name, c.Redirects
			return tx.Update(&rec)
		}
		return tx.Insert(&rec)
	})
	if err != nil {
		pkglog.Errorx("persist oauth client", err)
	}
}

func oauthPutGrantLocked(g *oauthGrant) {
	oauthState.Grants[g.ID] = g
	if oauthDB == nil {
		return
	}
	rec := oauthGrantRec{g.ID, g.ClientID, g.ClientName, g.Account, g.Address, g.Session, g.Scopes, g.Resource, g.Revoked}
	err := oauthDB.Write(mox.Shutdown, func(tx *bstore.Tx) error {
		old := oauthGrantRec{ID: g.ID}
		if tx.Get(&old) == nil {
			rec.ID = old.ID
			return tx.Update(&rec)
		}
		return tx.Insert(&rec)
	})
	if err != nil {
		pkglog.Errorx("persist oauth grant", err)
	}
}

func oauthPutTokenLocked(hash string, t oauthToken) {
	oauthState.Tokens[hash] = t
	if oauthDB == nil || t.Grant == nil {
		return
	}
	rec := oauthTokenRec{hash, t.Grant.ID, t.Refresh}
	err := oauthDB.Write(mox.Shutdown, func(tx *bstore.Tx) error {
		old := oauthTokenRec{Hash: hash}
		if tx.Get(&old) == nil {
			return tx.Update(&rec)
		}
		return tx.Insert(&rec)
	})
	if err != nil {
		pkglog.Errorx("persist oauth token", err)
	}
}

func oauthDeleteTokenLocked(hash string) {
	delete(oauthState.Tokens, hash)
	if oauthDB == nil {
		return
	}
	_ = oauthDB.Write(mox.Shutdown, func(tx *bstore.Tx) error {
		old := oauthTokenRec{Hash: hash}
		if tx.Get(&old) == nil {
			return tx.Delete(&old)
		}
		return nil
	})
}
