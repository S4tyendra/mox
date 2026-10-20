package store

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"time"

	"github.com/mjl-/bstore"
)

// WebAuthnCredential is a passkey for browser login to webmail/account.
type WebAuthnCredential struct {
	CredentialID string // Raw-url-base64 credential id, primary key.
	UserHandle   string `bstore:"index"`  // Raw-url-base64 user.id, stable per account.
	Account      string `bstore:"index"`
	LoginAddress string
	Name         string // User label, e.g. "Laptop".
	COSEKey      []byte // COSE_Key public key.
	Alg          int    // COSE alg, -7 ES256 or -257 RS256.
	SignCount    uint32
	Created      time.Time `bstore:"default now"`
	LastUsed     time.Time
}

// WebAuthnUser maps an account to a stable WebAuthn user handle.
type WebAuthnUser struct {
	Account      string // Primary key.
	Handle       string // Raw-url-base64, 32 bytes.
	LoginAddress string
}

func WebAuthnCredentialGet(ctx context.Context, credentialID string) (WebAuthnCredential, error) {
	c := WebAuthnCredential{CredentialID: credentialID}
	err := AuthDB.Get(ctx, &c)
	return c, err
}

func WebAuthnCredentialList(ctx context.Context, account string) ([]WebAuthnCredential, error) {
	return bstore.QueryDB[WebAuthnCredential](ctx, AuthDB).FilterNonzero(WebAuthnCredential{Account: account}).SortDesc("Created").List()
}

func WebAuthnCredentialAdd(ctx context.Context, c *WebAuthnCredential) error {
	return AuthDB.Insert(ctx, c)
}

func WebAuthnCredentialUpdate(ctx context.Context, c *WebAuthnCredential) error {
	return AuthDB.Update(ctx, c)
}

func WebAuthnCredentialRemove(ctx context.Context, account, credentialID string) error {
	c, err := WebAuthnCredentialGet(ctx, credentialID)
	if err != nil {
		return err
	}
	if c.Account != account {
		return ErrUnknownCredentials
	}
	return AuthDB.Delete(ctx, &c)
}

func WebAuthnUserEnsure(ctx context.Context, account, loginAddress, handle string) (WebAuthnUser, error) {
	u := WebAuthnUser{Account: account}
	err := AuthDB.Get(ctx, &u)
	if err == bstore.ErrAbsent {
		if handle == "" {
			var b [32]byte
			if _, err := cryptorand.Read(b[:]); err != nil {
				return WebAuthnUser{}, err
			}
			handle = base64.RawURLEncoding.EncodeToString(b[:])
		}
		u = WebAuthnUser{Account: account, Handle: handle, LoginAddress: loginAddress}
		return u, AuthDB.Insert(ctx, &u)
	}
	return u, err
}
