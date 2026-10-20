package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"testing"
)

func TestPasskeyRegisterAndLogin(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rpid := "localhost"
	origin := "http://localhost"
	cid, raw, err := NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	credID := make([]byte, 16)
	rand.Read(credID)
	cose := coseES256(key.X, key.Y)
	authCreate := authData(rpid, 0x45, 1, credID, cose)
	att := cborEncodeMapInt(nil)
	// map with string keys: fmt, authData, attStmt
	att = encodeAttestation(authCreate)
	cd := clientData("webauthn.create", origin, raw)
	regJSON, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credID),
		"rawId": base64.RawURLEncoding.EncodeToString(credID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cd),
			"attestationObject": base64.RawURLEncoding.EncodeToString(att),
		},
	})
	reg, err := FinishRegistration(cid, string(regJSON), origin, rpid)
	if err != nil {
		t.Fatal(err)
	}
	if string(reg.CredentialID) != string(credID) || reg.Alg != algES256 {
		t.Fatalf("%x %d", reg.CredentialID, reg.Alg)
	}

	cid2, raw2, err := NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	authGet := authData(rpid, 0x05, 2, nil, nil)
	cd2 := clientData("webauthn.get", origin, raw2)
	cdHash := sha256.Sum256(cd2)
	signed := append(authGet, cdHash[:]...)
	sum := sha256.Sum256(signed)
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
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
			"userHandle":        base64.RawURLEncoding.EncodeToString([]byte("user")),
		},
	})
	count, uh, gotID, err := FinishLogin(cid2, string(loginJSON), origin, rpid, reg.COSEKey, reg.Alg, 1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || string(gotID) != string(credID) || string(uh) != "user" {
		t.Fatalf("%d %x %s", count, gotID, uh)
	}
}

func clientData(typ, origin string, chal []byte) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":      typ,
		"origin":    origin,
		"challenge": base64.RawURLEncoding.EncodeToString(chal),
	})
	return b
}

func authData(rpid string, flags byte, count uint32, credID, cose []byte) []byte {
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

func coseES256(x, y *big.Int) []byte {
	xb, yb := x.FillBytes(make([]byte, 32)), y.FillBytes(make([]byte, 32))
	return cborEncodeValue(cborValue{kind: 5, pairs: []cborPair{
		{cborValue{kind: 0, u: 1}, cborValue{kind: 0, u: 2}},
		{cborValue{kind: 0, u: 3}, cborValue{kind: 1, u: 6}}, // alg -7
		{cborValue{kind: 1, u: 0}, cborValue{kind: 0, u: 1}}, // crv P-256 = -1 -> 1
		{k: cborValue{kind: 1, u: 1}, v: cborValue{kind: 2, b: xb}},
		{k: cborValue{kind: 1, u: 2}, v: cborValue{kind: 2, b: yb}},
	}})
}

func encodeAttestation(authData []byte) []byte {
	return cborEncodeValue(cborValue{kind: 5, pairs: []cborPair{
		{cborValue{kind: 3, b: []byte("fmt")}, cborValue{kind: 3, b: []byte("none")}},
		{cborValue{kind: 3, b: []byte("authData")}, cborValue{kind: 2, b: authData}},
		{cborValue{kind: 3, b: []byte("attStmt")}, cborValue{kind: 5}},
	}})
}
