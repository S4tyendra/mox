package webmail

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mjl-/sherpa"

	"github.com/mjl-/mox/store"
	"github.com/mjl-/mox/webauth"
	"github.com/mjl-/mox/webauthn"
)

type Passkey struct {
	CredentialID string
	Name         string
	Created      string
	LastUsed     string
}

type PasskeyBegin struct {
	ChallengeID string
	Options     map[string]any
}

func requestOrigin(isForwarded bool, host, proto string, tls bool) (origin, rpid string) {
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	scheme := "http"
	if isForwarded && proto == "https" || !isForwarded && tls {
		scheme = "https"
	}
	return scheme + "://" + host, hostname
}

func (w Webmail) PasskeyLoginBegin(ctx context.Context) PasskeyBegin {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	id, raw, err := webauthn.NewChallenge()
	xcheckf(ctx, err, "challenge")
	var tok [8]byte
	cryptorand.Read(tok[:])
	loginToken := base64.RawURLEncoding.EncodeToString(tok[:])
	webauth.LoginPrepAge(ctx, ri.Log, "webmail", w.cookiePath, w.isForwarded, ri.Response, ri.Request, loginToken, 120)
	_, rpid := requestOrigin(w.isForwarded, ri.Request.Host, ri.Request.Header.Get("X-Forwarded-Proto"), ri.Request.TLS != nil)
	return PasskeyBegin{
		ChallengeID: id + " " + loginToken,
		Options: map[string]any{
			"challenge":        base64.RawURLEncoding.EncodeToString(raw),
			"timeout":          120000,
			"rpId":             rpid,
			"userVerification": "required",
		},
	}
}

func (w Webmail) PasskeyLoginFinish(ctx context.Context, challengeID, credentialJSON string) store.CSRFToken {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	id, loginToken, _ := strings.Cut(challengeID, " ")
	cred, err := webauthn.ParseCredential(credentialJSON)
	if err != nil {
		panic(&sherpa.Error{Code: "user:loginFailed", Message: "invalid passkey"})
	}
	cid := cred.ID
	if cid == "" {
		cid = cred.RawID
	}
	stored, err := store.WebAuthnCredentialGet(ctx, cid)
	if err != nil {
		panic(&sherpa.Error{Code: "user:loginFailed", Message: "unknown passkey"})
	}
	origin, rpid := requestOrigin(w.isForwarded, ri.Request.Host, ri.Request.Header.Get("X-Forwarded-Proto"), ri.Request.TLS != nil)
	count, userHandle, _, err := webauthn.FinishLogin(id, credentialJSON, origin, rpid, stored.COSEKey, stored.Alg, stored.SignCount)
	if err != nil {
		ri.Log.Infox("passkey login", err)
		panic(&sherpa.Error{Code: "user:loginFailed", Message: "passkey verification failed"})
	}
	if err := webauthn.VerifyUserHandle(stored.UserHandle, userHandle); err != nil {
		ri.Log.Infox("passkey login", err)
		panic(&sherpa.Error{Code: "user:loginFailed", Message: "passkey verification failed"})
	}
	stored.SignCount = count
	stored.LastUsed = time.Now()
	_ = store.WebAuthnCredentialUpdate(ctx, &stored)
	csrf, err := webauth.LoginVerified(ctx, ri.Log, webauth.Accounts, "webmail", w.cookiePath, w.isForwarded, ri.Response, ri.Request, loginToken, stored.Account, stored.LoginAddress, "webauthn")
	if e, ok := err.(*sherpa.Error); ok {
		panic(e)
	}
	xcheckf(ctx, err, "passkey login")
	return csrf
}

func (w Webmail) PasskeyRegisterBegin(ctx context.Context, name string) PasskeyBegin {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	id, raw, err := webauthn.NewChallenge()
	xcheckf(ctx, err, "challenge")
	handle, err := passkeyUserHandle(ctx, ri.Account.Name, ri.LoginAddress)
	xcheckf(ctx, err, "user handle")
	webauthn.BindUser(id, []byte(handle), ri.Account.Name)
	_, rpid := requestOrigin(w.isForwarded, ri.Request.Host, ri.Request.Header.Get("X-Forwarded-Proto"), ri.Request.TLS != nil)
	return PasskeyBegin{
		ChallengeID: id,
		Options: map[string]any{
			"rp":               map[string]any{"name": "mox", "id": rpid},
			"user":             map[string]any{"id": handle, "name": ri.LoginAddress, "displayName": ri.LoginAddress},
			"challenge":        base64.RawURLEncoding.EncodeToString(raw),
			"pubKeyCredParams": []map[string]any{{"type": "public-key", "alg": -7}, {"type": "public-key", "alg": -257}},
			"timeout":          120000,
			"attestation":      "none",
			"authenticatorSelection": map[string]any{
				"residentKey":      "required",
				"userVerification": "required",
			},
		},
	}
}

func (w Webmail) PasskeyRegisterFinish(ctx context.Context, name, challengeID, credentialJSON string) Passkey {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	origin, rpid := requestOrigin(w.isForwarded, ri.Request.Host, ri.Request.Header.Get("X-Forwarded-Proto"), ri.Request.TLS != nil)
	reg, err := webauthn.FinishRegistration(challengeID, credentialJSON, origin, rpid)
	xcheckuserf(ctx, err, "register passkey")
	cid := base64.RawURLEncoding.EncodeToString(reg.CredentialID)
	u, err := store.WebAuthnUserEnsure(ctx, ri.Account.Name, ri.LoginAddress, "")
	xcheckf(ctx, err, "user handle")
	c := store.WebAuthnCredential{
		CredentialID: cid,
		UserHandle:   u.Handle,
		Account:      ri.Account.Name,
		LoginAddress: ri.LoginAddress,
		Name:         name,
		COSEKey:      reg.COSEKey,
		Alg:          reg.Alg,
		SignCount:    reg.SignCount,
	}
	xcheckf(ctx, store.WebAuthnCredentialAdd(ctx, &c), "save passkey")
	return Passkey{CredentialID: cid, Name: name, Created: c.Created.UTC().Format("2006-01-02")}
}

func (Webmail) PasskeyList(ctx context.Context) []Passkey {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	l, err := store.WebAuthnCredentialList(ctx, ri.Account.Name)
	xcheckf(ctx, err, "list passkeys")
	out := []Passkey{}
	for _, c := range l {
		used := ""
		if !c.LastUsed.IsZero() {
			used = c.LastUsed.UTC().Format("2006-01-02")
		}
		out = append(out, Passkey{c.CredentialID, c.Name, c.Created.UTC().Format("2006-01-02"), used})
	}
	return out
}

func (Webmail) PasskeyRemove(ctx context.Context, credentialID string) {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	err := store.WebAuthnCredentialRemove(ctx, ri.Account.Name, credentialID)
	xcheckuserf(ctx, err, "remove passkey")
}

func passkeyUserHandle(ctx context.Context, account, login string) (string, error) {
	u, err := store.WebAuthnUserEnsure(ctx, account, login, "")
	if err != nil {
		return "", err
	}
	if u.Handle == "" {
		return "", fmt.Errorf("missing user handle")
	}
	return u.Handle, nil
}
