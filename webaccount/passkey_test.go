package webaccount

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

func passkeyAuthData(rpid string, flags byte, count uint32, credID, cose []byte) []byte {
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

func passkeyCOSE(x, y *big.Int) []byte {
	xb, yb := x.FillBytes(make([]byte, 32)), y.FillBytes(make([]byte, 32))
	return cborMap([]any{
		cborInt(1), cborInt(2),
		cborInt(3), cborInt(-7),
		cborInt(-1), cborInt(1),
		cborInt(-2), cborBytes(xb),
		cborInt(-3), cborBytes(yb),
	})
}

// passkeyAttestation builds a "none" format attestation object.
func passkeyAttestation(authData []byte) []byte {
	return cborMap([]any{
		cborText("fmt"), cborText("none"),
		cborText("authData"), cborBytes(authData),
		cborText("attStmt"), cborMap(nil),
	})
}

func cborHead(mt byte, n uint64) []byte {
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

func cborInt(n int64) []byte {
	if n >= 0 {
		return cborHead(0, uint64(n))
	}
	return cborHead(1, uint64(-1-n))
}

func cborBytes(b []byte) []byte {
	return append(cborHead(2, uint64(len(b))), b...)
}

func cborText(s string) []byte {
	return append(cborHead(3, uint64(len(s))), s...)
}

func cborMap(items []any) []byte {
	out := cborHead(5, uint64(len(items)/2))
	for _, i := range items {
		out = append(out, i.([]byte)...)
	}
	return out
}

func passkeyClientData(typ, origin string, chal []byte) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":      typ,
		"origin":    origin,
		"challenge": base64.RawURLEncoding.EncodeToString(chal),
	})
	return b
}

func TestPasskeyLogin(t *testing.T) {
	log := pkglog
	os.RemoveAll("../testdata/httpaccount/data")
	mox.ConfigStaticPath = filepath.FromSlash("../testdata/httpaccount/mox.conf")
	mox.ConfigDynamicPath = filepath.Join(filepath.Dir(mox.ConfigStaticPath), "domains.conf")
	mox.MustLoadConfig(true, false)
	err := store.Init(ctxbg)
	tcheck(t, err, "store init")
	defer func() {
		err := store.Close()
		tcheck(t, err, "store close")
	}()
	defer store.Switchboard()()

	acc, err := store.OpenAccount(log, "mjl☺", false)
	tcheck(t, err, "open account")
	err = acc.SetPassword(log, "test1234")
	tcheck(t, err, "set password")
	defer acc.Close()

	api := Account{cookiePath: "/account/"}

	// Register a passkey, as if the user is logged in.
	respRec := httptest.NewRecorder()
	reqInfo := requestInfo{"mjl☺@mox.example", "mjl☺", "", respRec, &http.Request{
		RemoteAddr: "127.0.0.1:1234",
		Host:       "mox.example",
		Header:     http.Header{},
	}}
	ctx := context.WithValue(ctxbg, requestInfoCtxKey, reqInfo)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tcheck(t, err, "generate key")
	credID := make([]byte, 16)
	rand.Read(credID)

	regBegin := api.PasskeyRegisterBegin(ctx, "Test key")
	if regBegin.ChallengeID == "" {
		t.Fatal("no challenge id")
	}
	opts, ok := regBegin.Options["challenge"].(string)
	if !ok {
		t.Fatal("no challenge in options")
	}
	chal, err := base64.RawURLEncoding.DecodeString(opts)
	tcheck(t, err, "decode challenge")

	authCreate := passkeyAuthData("mox.example", 0x45, 1, credID, passkeyCOSE(key.X, key.Y))
	attObj := passkeyAttestation(authCreate)
	cd := passkeyClientData("webauthn.create", "http://mox.example", chal)
	credJSON, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cd),
			"attestationObject": base64.RawURLEncoding.EncodeToString(attObj),
		},
	})
	pk := api.PasskeyRegisterFinish(ctx, "Test key", regBegin.ChallengeID, string(credJSON))
	tcompare(t, pk.Name, "Test key")
	if pk.CredentialID == "" {
		t.Fatal("no credential id")
	}

	// List passkeys.
	keys := api.PasskeyList(ctx)
	tcompare(t, len(keys), 1)
	tcompare(t, keys[0].Name, "Test key")

	// The user handle the authenticator will echo back.
	uhRec, err := store.WebAuthnUserEnsure(ctx, "mjl☺", "mjl☺@mox.example", "")
	tcheck(t, err, "user handle")
	userHandleBytes, err := base64.RawURLEncoding.DecodeString(uhRec.Handle)
	tcheck(t, err, "decode user handle")

	// Log in with the passkey, without a password.
	loginRec := httptest.NewRecorder()
	loginInfo := requestInfo{"", "", "", loginRec, &http.Request{
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
		t.Fatal("no login token in challenge id")
	}
	loginTok := begin.ChallengeID[idx+1:]
	loginCookie := &http.Cookie{Name: "webaccountlogin", Value: loginTok}
	loginInfo.Request.Header = http.Header{"Cookie": []string{loginCookie.String()}}

	chalB64, _ := begin.Options["challenge"].(string)
	chal2, err := base64.RawURLEncoding.DecodeString(chalB64)
	tcheck(t, err, "decode challenge 2")
	authGet := passkeyAuthData("mox.example", 0x05, 2, nil, nil)
	cd2 := passkeyClientData("webauthn.get", "http://mox.example", chal2)
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
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandleBytes),
		},
	})
	csrf := api.PasskeyLoginFinish(loginCtx, begin.ChallengeID, string(loginJSON))
	if csrf == "" {
		t.Fatal("no csrf token")
	}
	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == "webaccountsession" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie after passkey login")
	}

	// A wrong signature must fail.
	begin2 := api.PasskeyLoginBegin(loginCtx)
	idx2 := -1
	for i, c := range begin2.ChallengeID {
		if c == ' ' {
			idx2 = i
			break
		}
	}
	tok2 := begin2.ChallengeID[idx2+1:]
	loginCookie2 := &http.Cookie{Name: "webaccountlogin", Value: tok2}
	loginInfo.Request.Header = http.Header{"Cookie": []string{loginCookie2.String()}}
	badJSON, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cd2),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authGet),
			"signature":         base64.RawURLEncoding.EncodeToString(sig),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandleBytes),
		},
	})
	tneedErrorCode(t, "user:loginFailed", func() {
		api.PasskeyLoginFinish(loginCtx, begin2.ChallengeID, string(badJSON))
	})

	// Remove the passkey.
	api.PasskeyRemove(ctx, pk.CredentialID)
	tcompare(t, len(api.PasskeyList(ctx)), 0)
}
