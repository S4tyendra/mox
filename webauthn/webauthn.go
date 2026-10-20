package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	algES256 = -7
	algRS256 = -257
)

type challenge struct {
	bytes   []byte
	userID  []byte
	account string
	expires time.Time
}

var challenges = struct {
	sync.Mutex
	m map[string]challenge
}{m: map[string]challenge{}}

func b64enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func b64dec(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("empty")
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

func NewChallenge() (id string, raw []byte, err error) {
	raw = make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	idb := make([]byte, 16)
	if _, err = rand.Read(idb); err != nil {
		return "", nil, err
	}
	id = b64enc(idb)
	challenges.Lock()
	defer challenges.Unlock()
	for k, v := range challenges.m {
		if time.Now().After(v.expires) {
			delete(challenges.m, k)
		}
	}
	challenges.m[id] = challenge{bytes: raw, expires: time.Now().Add(2 * time.Minute)}
	return id, raw, nil
}

// BindUser records the user handle and account for a registration challenge, so
// the registered credential can be tied to the right user.
func BindUser(id string, userID []byte, account string) {
	challenges.Lock()
	defer challenges.Unlock()
	c, ok := challenges.m[id]
	if !ok {
		return
	}
	c.userID = userID
	c.account = account
	challenges.m[id] = c
}

func takeChallenge(id string) (challenge, bool) {
	challenges.Lock()
	defer challenges.Unlock()
	c, ok := challenges.m[id]
	if !ok || time.Now().After(c.expires) {
		delete(challenges.m, id)
		return challenge{}, false
	}
	delete(challenges.m, id)
	return c, true
}

type ClientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

type CredentialJSON struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AttestationObject string `json:"attestationObject"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
	} `json:"response"`
}

type AuthData struct {
	RPIDHash     []byte
	Flags        byte
	SignCount    uint32
	CredentialID []byte
	COSEKey      []byte
	Alg          int
}

func ParseCredential(s string) (CredentialJSON, error) {
	var c CredentialJSON
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return CredentialJSON{}, err
	}
	if c.Type != "" && c.Type != "public-key" {
		return CredentialJSON{}, fmt.Errorf("not a public-key credential")
	}
	return c, nil
}

func parseClientData(b64, wantType, origin, challengeB64 string) error {
	raw, err := b64dec(b64)
	if err != nil {
		return fmt.Errorf("clientDataJSON: %w", err)
	}
	var cd ClientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return fmt.Errorf("clientDataJSON json: %w", err)
	}
	if cd.Type != wantType {
		return fmt.Errorf("clientData type %q", cd.Type)
	}
	if cd.Origin != origin {
		return fmt.Errorf("origin %q does not match %q", cd.Origin, origin)
	}
	got, err := b64dec(cd.Challenge)
	if err != nil {
		return fmt.Errorf("challenge: %w", err)
	}
	want, err := b64dec(challengeB64)
	if err != nil {
		return fmt.Errorf("stored challenge: %w", err)
	}
	if len(got) != len(want) {
		return fmt.Errorf("challenge mismatch")
	}
	var diff byte
	for i := range got {
		diff |= got[i] ^ want[i]
	}
	if diff != 0 {
		return fmt.Errorf("challenge mismatch")
	}
	return nil
}

func parseAuthData(raw []byte, rpid string, wantAT bool) (AuthData, error) {
	if len(raw) < 37 {
		return AuthData{}, fmt.Errorf("authenticator data too short")
	}
	sum := sha256.Sum256([]byte(rpid))
	var ad AuthData
	ad.RPIDHash = raw[:32]
	if string(ad.RPIDHash) != string(sum[:]) {
		return AuthData{}, fmt.Errorf("rpId hash mismatch")
	}
	ad.Flags = raw[32]
	ad.SignCount = binary.BigEndian.Uint32(raw[33:37])
	if ad.Flags&0x01 == 0 {
		return AuthData{}, fmt.Errorf("user not present")
	}
	rest := raw[37:]
	if wantAT {
		if ad.Flags&0x40 == 0 {
			return AuthData{}, fmt.Errorf("attested credential missing")
		}
		if len(rest) < 18 {
			return AuthData{}, fmt.Errorf("attested credential short")
		}
		idLen := int(binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
		if idLen < 16 || idLen > 1023 || len(rest) < idLen {
			return AuthData{}, fmt.Errorf("credential id length")
		}
		ad.CredentialID = rest[:idLen]
		cose := rest[idLen:]
		key, alg, err := parseCOSEKey(cose)
		if err != nil {
			return AuthData{}, err
		}
		ad.COSEKey = key
		ad.Alg = alg
	}
	return ad, nil
}

func parseCOSEKey(raw []byte) ([]byte, int, error) {
	v, _, err := cborDecode(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("cose: %w", err)
	}
	algV, ok := v.mapGetInt(3)
	if !ok {
		return nil, 0, fmt.Errorf("cose missing alg")
	}
	alg, ok := algV.int()
	if !ok {
		return nil, 0, fmt.Errorf("cose alg")
	}
	if alg != algES256 && alg != algRS256 {
		return nil, 0, fmt.Errorf("unsupported alg %d", alg)
	}
	encoded := cborEncodeValue(v)
	return encoded, int(alg), nil
}

func parseAttestation(b64 string) ([]byte, error) {
	raw, err := b64dec(b64)
	if err != nil {
		return nil, err
	}
	v, _, err := cborDecode(raw)
	if err != nil {
		return nil, fmt.Errorf("attestation: %w", err)
	}
	ad, ok := v.mapGetText("authData")
	if !ok {
		return nil, fmt.Errorf("attestation missing authData")
	}
	b, ok := ad.bytes()
	if !ok {
		return nil, fmt.Errorf("authData not bytes")
	}
	return b, nil
}

type Registration struct {
	CredentialID []byte
	COSEKey      []byte
	Alg          int
	SignCount    uint32
}

func FinishRegistration(challengeID, credentialJSON, origin, rpid string) (Registration, error) {
	ch, ok := takeChallenge(challengeID)
	if !ok {
		return Registration{}, fmt.Errorf("unknown or expired challenge")
	}
	cred, err := ParseCredential(credentialJSON)
	if err != nil {
		return Registration{}, err
	}
	if err := parseClientData(cred.Response.ClientDataJSON, "webauthn.create", origin, b64enc(ch.bytes)); err != nil {
		return Registration{}, err
	}
	authRaw, err := parseAttestation(cred.Response.AttestationObject)
	if err != nil {
		return Registration{}, err
	}
	ad, err := parseAuthData(authRaw, rpid, true)
	if err != nil {
		return Registration{}, err
	}
	return Registration{CredentialID: ad.CredentialID, COSEKey: ad.COSEKey, Alg: ad.Alg, SignCount: ad.SignCount}, nil
}

func FinishLogin(challengeID, credentialJSON, origin, rpid string, coseKey []byte, alg int, prevCount uint32) (signCount uint32, userHandle []byte, credentialID []byte, err error) {
	ch, ok := takeChallenge(challengeID)
	if !ok {
		return 0, nil, nil, fmt.Errorf("unknown or expired challenge")
	}
	cred, err := ParseCredential(credentialJSON)
	if err != nil {
		return 0, nil, nil, err
	}
	if err := parseClientData(cred.Response.ClientDataJSON, "webauthn.get", origin, b64enc(ch.bytes)); err != nil {
		return 0, nil, nil, err
	}
	authRaw, err := b64dec(cred.Response.AuthenticatorData)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("authenticatorData: %w", err)
	}
	ad, err := parseAuthData(authRaw, rpid, false)
	if err != nil {
		return 0, nil, nil, err
	}
	sig, err := b64dec(cred.Response.Signature)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("signature: %w", err)
	}
	cdRaw, err := b64dec(cred.Response.ClientDataJSON)
	if err != nil {
		return 0, nil, nil, err
	}
	cdHash := sha256.Sum256(cdRaw)
	signed := append(append([]byte{}, authRaw...), cdHash[:]...)
	if err := verifyCOSE(coseKey, alg, signed, sig); err != nil {
		return 0, nil, nil, err
	}
	if ad.SignCount > 0 && prevCount > 0 && ad.SignCount <= prevCount {
		return 0, nil, nil, fmt.Errorf("cloned authenticator (sign count)")
	}
	uh, _ := b64dec(cred.Response.UserHandle)
	cid, err := b64dec(cred.RawID)
	if err != nil {
		cid, err = b64dec(cred.ID)
	}
	if err != nil {
		return 0, nil, nil, fmt.Errorf("credential id")
	}
	return ad.SignCount, uh, cid, nil
}

// VerifyUserHandle checks that the user handle from an assertion matches the
// one stored for the credential. The stored handle is base64url-encoded, the
// one from the authenticator is raw bytes.
func VerifyUserHandle(storedB64 string, got []byte) error {
	if storedB64 == "" || len(got) == 0 {
		return nil
	}
	stored, err := b64dec(storedB64)
	if err != nil {
		return fmt.Errorf("stored user handle: %w", err)
	}
	if len(stored) != len(got) {
		return fmt.Errorf("user handle mismatch")
	}
	var diff byte
	for i := range stored {
		diff |= stored[i] ^ got[i]
	}
	if diff != 0 {
		return fmt.Errorf("user handle mismatch")
	}
	return nil
}

func verifyCOSE(cose []byte, alg int, signed, sig []byte) error {
	v, _, err := cborDecode(cose)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(signed)
	switch alg {
	case algES256:
		xV, ok1 := v.mapGetInt(-2)
		yV, ok2 := v.mapGetInt(-3)
		xb, ok3 := xV.bytes()
		yb, ok4 := yV.bytes()
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return fmt.Errorf("cose ec key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
		if len(sig) == 64 {
			r := new(big.Int).SetBytes(sig[:32])
			s := new(big.Int).SetBytes(sig[32:])
			if !ecdsa.Verify(pub, sum[:], r, s) {
				return fmt.Errorf("invalid signature")
			}
			return nil
		}
		if !ecdsa.VerifyASN1(pub, sum[:], sig) {
			return fmt.Errorf("invalid signature")
		}
		return nil
	case algRS256:
		nV, ok1 := v.mapGetInt(-1)
		eV, ok2 := v.mapGetInt(-2)
		nb, ok3 := nV.bytes()
		if !ok1 || !ok2 || !ok3 {
			return fmt.Errorf("cose rsa key")
		}
		var e int
		if eb, ok := eV.bytes(); ok {
			e = int(new(big.Int).SetBytes(eb).Int64())
		} else if ei, ok := eV.int(); ok {
			e = int(ei)
		} else {
			return fmt.Errorf("cose rsa e")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
	default:
		return fmt.Errorf("alg")
	}
}

func ChallengeBytes(id string) ([]byte, bool) {
	challenges.Lock()
	defer challenges.Unlock()
	c, ok := challenges.m[id]
	if !ok {
		return nil, false
	}
	return c.bytes, true
}

// ChallengeUser returns the user handle and account bound to a challenge.
func ChallengeUser(id string) ([]byte, string, bool) {
	challenges.Lock()
	defer challenges.Unlock()
	c, ok := challenges.m[id]
	if !ok {
		return nil, "", false
	}
	return c.userID, c.account, true
}
