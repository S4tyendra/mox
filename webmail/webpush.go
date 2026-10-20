package webmail

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/mox-"
	"github.com/mjl-/mox/store"
)

type vapidKeyFile struct {
	D, X, Y string
}

var vapidKey *ecdsa.PrivateKey
var vapidPubUncompressed []byte

func initVAPID() {
	p := mox.DataDirPath("vapid.json")
	os.MkdirAll(filepath.Dir(p), 0770)
	if b, err := os.ReadFile(p); err == nil {
		var f vapidKeyFile
		if json.Unmarshal(b, &f) == nil {
			d, ok1 := new(big.Int).SetString(f.D, 16)
			x, ok2 := new(big.Int).SetString(f.X, 16)
			y, ok3 := new(big.Int).SetString(f.Y, 16)
			if ok1 && ok2 && ok3 {
				vapidKey = &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, D: d}
			}
		}
	}
	if vapidKey == nil {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			pkglog.Errorx("generate vapid key", err)
			return
		}
		vapidKey = k
		b, _ := json.Marshal(vapidKeyFile{k.D.Text(16), k.X.Text(16), k.Y.Text(16)})
		_ = os.WriteFile(p, b, 0600)
	}
	vapidPubUncompressed = elliptic.Marshal(elliptic.P256(), vapidKey.X, vapidKey.Y)
}

func vapidPublicKey() string {
	if len(vapidPubUncompressed) == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(vapidPubUncompressed)
}

func NotifyPush(acc *store.Account, title, body string) {
	if vapidKey == nil {
		return
	}
	var subs []store.PushSubscription
	ctx := context.Background()
	_ = acc.DB.Read(ctx, func(tx *bstore.Tx) error {
		var err error
		subs, err = bstore.QueryTx[store.PushSubscription](tx).List()
		return err
	})
	if len(subs) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]string{"title": title, "body": body})
	for _, sub := range subs {
		sub := sub
		go func() {
			if err := sendWebPush(sub, payload); err != nil {
				pkglog.Infox("web push", err)
				if strings.Contains(err.Error(), "status 410") || strings.Contains(err.Error(), "status 404") {
					acc.DB.Write(ctx, func(tx *bstore.Tx) error {
						return tx.Delete(&sub)
					})
				}
			}
		}()
	}
}

func sendWebPush(sub store.PushSubscription, payload []byte) error {
	p256dh, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		p256dh, err = base64.URLEncoding.DecodeString(sub.P256dh)
	}
	if err != nil {
		return err
	}
	auth, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil {
		auth, err = base64.URLEncoding.DecodeString(sub.Auth)
	}
	if err != nil {
		return err
	}
	body, localPub, err := encryptPush(p256dh, auth, payload)
	if err != nil {
		return err
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil {
		return err
	}
	jwt, err := vapidJWT(u.Scheme + "://" + u.Host)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("TTL", "86400")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+vapidPublicKey())
	req.Header.Set("Crypto-Key", "p256ecdsa="+vapidPublicKey())
	_ = localPub
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("status %d: %s", resp.StatusCode, b)
	}
	return nil
}

func encryptPush(clientPub, authSecret, plaintext []byte) (body, localPub []byte, err error) {
	local, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	localPub = elliptic.Marshal(elliptic.P256(), local.X, local.Y)
	x, y := elliptic.Unmarshal(elliptic.P256(), clientPub)
	if x == nil {
		return nil, nil, fmt.Errorf("invalid client key")
	}
	sx, _ := elliptic.P256().ScalarMult(x, y, local.D.Bytes())
	shared := sx.Bytes()
	if pad := 32 - len(shared); pad > 0 {
		shared = append(make([]byte, pad), shared...)
	}
	info := append(append([]byte("WebPush: info\x00"), clientPub...), localPub...)
	ikm, err := hkdfExpand(hmacSHA256(authSecret, shared), info, 32)
	if err != nil {
		return nil, nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	cek, err := hkdfExpand(hmacSHA256(salt, ikm), []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := hkdfExpand(hmacSHA256(salt, ikm), []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	record := append(plaintext, 2)
	ciphertext := gcm.Seal(nil, nonce, record, nil)
	var hdr bytes.Buffer
	hdr.Write(salt)
	binary.Write(&hdr, binary.BigEndian, uint32(4096))
	hdr.WriteByte(byte(len(localPub)))
	hdr.Write(localPub)
	hdr.Write(ciphertext)
	return hdr.Bytes(), localPub, nil
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func hkdfExpand(prk, info []byte, n int) ([]byte, error) {
	var out, prev []byte
	var i byte
	for len(out) < n {
		i++
		m := hmac.New(sha256.New, prk)
		m.Write(prev)
		m.Write(info)
		m.Write([]byte{i})
		prev = m.Sum(nil)
		out = append(out, prev...)
	}
	return out[:n], nil
}

func vapidJWT(audience string) (string, error) {
	if vapidKey == nil {
		return "", fmt.Errorf("no vapid key")
	}
	header, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	payload, _ := json.Marshal(map[string]any{"aud": audience, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": "mailto:postmaster@" + mox.Conf.Static.HostnameDomain.ASCII})
	b64 := base64.RawURLEncoding.EncodeToString
	unsigned := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, vapidKey, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return unsigned + "." + b64(sig), nil
}
