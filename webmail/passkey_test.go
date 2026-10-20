package webmail

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mjl-/mox/mox-"
	"github.com/mjl-/mox/store"
)

func pkAuthData(rpid string, flags byte, count uint32, credID, cose []byte) []byte {
	sum := sha256.Sum256([]byte(rpid))
	buf := append([]byte{}, sum[:]...)
	buf = append(buf, flags)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], count)
	buf = append(buf, n[:]...)
	if flags&0x40 != 0 {
		buf = append(buf, make([]byte, 16)...)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(credID)))
		buf = append(buf, l[:]...)
		buf = append(buf, credID...)
		buf = append(buf, cose...)
	}
	return buf
}

func pkCborHead(mt byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{mt<<5 | byte(n)}
	case n < 256:
		return []byte{mt<<5 | 24, byte(n)}
	case n < 65536:
		return []byte{mt<<5 | 25, byte(n >> 8), byte(n)}
	default:
		b := []byte{mt<<5 | 26, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(b[1:], uint32(n))
		return b
	}
}

func pkCborInt(n int64) []byte {
	if n >= 0 {
		return pkCborHead(0, uint64(n))
	}
	return pkCborHead(1, uint64(-1-n))
}

func pkCborBytes(b []byte) []byte { return append(pkCborHead(2, uint64(len(b))), b...) }

func pkCborText(s string) []byte { return append(pkCborHead(3, uint64(len(s))), s...) }

func pkCborMap(items []any) []byte {
	out := pkCborHead(5, uint64(len(items)/2))
	for _, i := range items {
		out = append(out, i.([]byte)...)
	}
	return out
}

func pkCOSE(x, y *big.Int) []byte {
	xb, yb := x.FillBytes(make([]byte, 32)), y.FillBytes(make([]byte, 32))
	return pkCborMap([]any{
		pkCborInt(1), pkCborInt(2),
		pkCborInt(3), pkCborInt(-7),
		pkCborInt(-1), pkCborInt(1),
		pkCborInt(-2), pkCborBytes(xb),
		pkCborInt(-3), pkCborBytes(yb),
	})
}

func pkClientData(typ, origin string, chal []byte) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":      typ,
		"origin":    origin,
		"challenge": base64.RawURLEncoding.EncodeToString(chal),
	})
	return b
}

func TestWebmailPasskeyLogin(t *testing.T) {
	mox.LimitersInit()
	os.RemoveAll("../testdata/webmail/data")
	mox.Context = ctxbg
	mox.ConfigStaticPath = filepath.FromSlash("../testdata/webmail/mox.conf")
	mox.MustLoadConfig(true, false)
	err := store.Init(ctxbg)
	tcheck(t, err, "store init")
	defer func() {
		err := store.Close()
		tcheck(t, err, "store close")
	}()
	defer store.Switchboard()()

	acc, err := store.OpenAccount(pkglog, "mjl", false)
	tcheck(t, err, "open account")
	err = acc.SetPassword(pkglog, "test1234")
	tcheck(t, err, "set password")
	defer acc.Close()

	api := Webmail{maxMessageSize: 1024 * 1024, cookiePath: "/webmail/"}

	// Register a passkey as an authenticated user.
	regRec := httptest.NewRecorder()
	regInfo := requestInfo{pkglog, "mjl@mox.example", acc, "", regRec, &http.Request{
		RemoteAddr: "127.0.0.1:1234",
		Host:       "mox.example",
		Header:     http.Header{},
	}}
	regCtx := context.WithValue(ctxbg, requestInfoCtxKey, regInfo)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tcheck(t, err, "generate key")
	credID := make([]byte, 16)
	rand.Read(credID)

	regBegin := api.PasskeyRegisterBegin(regCtx, "Test key")
	chalB64, ok := regBegin.Options["challenge"].(string)
	if !ok {
		t.Fatal("no challenge")
	}
	chal, err := base64.RawURLEncoding.DecodeString(chalB64)
	tcheck(t, err, "decode challenge")
	authCreate := pkAuthData("mox.example", 0x45, 1, credID, pkCOSE(key.X, key.Y))
	att := pkCborMap([]any{
		pkCborText("fmt"), pkCborText("none"),
		pkCborText("authData"), pkCborBytes(authCreate),
		pkCborText("attStmt"), pkCborMap(nil),
	})
	cd := pkClientData("webauthn.create", "http://mox.example", chal)
	credJSON, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cd),
			"attestationObject": base64.RawURLEncoding.EncodeToString(att),
		},
	})
	pk := api.PasskeyRegisterFinish(regCtx, "Test key", regBegin.ChallengeID, string(credJSON))
	if pk.CredentialID == "" {
		t.Fatal("no credential id")
	}
	tcompare(t, len(api.PasskeyList(regCtx)), 1)

	uh, err := store.WebAuthnUserEnsure(regCtx, "mjl", "mjl@mox.example", "")
	tcheck(t, err, "user handle")
	userHandle, err := base64.RawURLEncoding.DecodeString(uh.Handle)
	tcheck(t, err, "decode user handle")

	// Log in with the passkey, no password.
	loginRec := httptest.NewRecorder()
	loginInfo := requestInfo{pkglog, "", nil, "", loginRec, &http.Request{
		RemoteAddr: "127.0.0.1:1234",
		Host:       "mox.example",
		Header:     http.Header{},
	}}
	loginCtx := context.WithValue(ctxbg, requestInfoCtxKey, loginInfo)
	begin := api.PasskeyLoginBegin(loginCtx)
	idx := -1
	for i, c := range begin.ChallengeID {
		if c == ' ' {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no login token")
	}
	loginCookie := &http.Cookie{Name: "webmaillogin", Value: begin.ChallengeID[idx+1:]}
	loginInfo.Request.Header = http.Header{"Cookie": []string{loginCookie.String()}}

	chal2B64, _ := begin.Options["challenge"].(string)
	chal2, err := base64.RawURLEncoding.DecodeString(chal2B64)
	tcheck(t, err, "decode challenge 2")
	authGet := pkAuthData("mox.example", 0x05, 2, nil, nil)
	cd2 := pkClientData("webauthn.get", "http://mox.example", chal2)
	cdHash := sha256.Sum256(cd2)
	signed := append(authGet, cdHash[:]...)
	sum := sha256.Sum256(signed)
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	tcheck(t, err, "sign")
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	loginJSON, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cd2),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authGet),
			"signature":         base64.RawURLEncoding.EncodeToString(sig),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandle),
		},
	})
	csrf := api.PasskeyLoginFinish(loginCtx, begin.ChallengeID, string(loginJSON))
	if csrf == "" {
		t.Fatal("no csrf token")
	}
	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == "webmailsession" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie")
	}

	// A credential from another origin must be rejected.
	begin2 := api.PasskeyLoginBegin(loginCtx)
	idx2 := -1
	for i, c := range begin2.ChallengeID {
		if c == ' ' {
			idx2 = i
			break
		}
	}
	loginCookie2 := &http.Cookie{Name: "webmaillogin", Value: begin2.ChallengeID[idx2+1:]}
	loginInfo.Request.Header = http.Header{"Cookie": []string{loginCookie2.String()}}
	chal3, err := base64.RawURLEncoding.DecodeString(begin2.Options["challenge"].(string))
	tcheck(t, err, "decode challenge 3")
	cd3 := pkClientData("webauthn.get", "http://evil.example", chal3)
	cdHash3 := sha256.Sum256(cd3)
	signed3 := append(pkAuthData("mox.example", 0x05, 3, nil, nil), cdHash3[:]...)
	sum3 := sha256.Sum256(signed3)
	r3, s3, err := ecdsa.Sign(rand.Reader, key, sum3[:])
	tcheck(t, err, "sign 3")
	sig3 := make([]byte, 64)
	r3.FillBytes(sig3[:32])
	s3.FillBytes(sig3[32:])
	evilJSON, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cd3),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(pkAuthData("mox.example", 0x05, 3, nil, nil)),
			"signature":         base64.RawURLEncoding.EncodeToString(sig3),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandle),
		},
	})
	tneedErrorCode(t, "user:loginFailed", func() {
		api.PasskeyLoginFinish(loginCtx, begin2.ChallengeID, string(evilJSON))
	})
}
