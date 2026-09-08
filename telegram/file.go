package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mjl-/bstore"

	"github.com/mjl-/mox/moxio"
	"github.com/mjl-/mox/store"
)

type fileClaim struct {
	Acc  string `json:"a"`
	ID   int64  `json:"i"`
	Path []int  `json:"p"`
	Name string `json:"n"`
	Exp  int64  `json:"e"`
}

func (s *service) fileLink(accountName string, msgID int64, partPath []int, filename string) string {
	if s.cfg.BaseURL == "" {
		return ""
	}
	tok, err := s.signFileClaim(fileClaim{
		Acc:  accountName,
		ID:   msgID,
		Path: partPath,
		Name: filename,
		Exp:  time.Now().Add(24 * time.Hour).Unix(),
	})
	if err != nil {
		s.log.Infox("signing telegram file token", err)
		return ""
	}
	name := filename
	if name == "" {
		name = "attachment"
	}
	return s.cfg.BaseURL + "/telegram/file/" + url.PathEscape(tok) + "/" + url.PathEscape(name)
}

func (s *service) signFileClaim(c fileClaim) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.Token))
	mac.Write(raw)
	sum := mac.Sum(nil)
	buf := make([]byte, len(raw)+len(sum))
	copy(buf, raw)
	copy(buf[len(raw):], sum)
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *service) verifyFileClaim(token string) (fileClaim, error) {
	buf, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(buf) < sha256.Size+2 {
		return fileClaim{}, fmt.Errorf("bad token")
	}
	raw := buf[:len(buf)-sha256.Size]
	sum := buf[len(buf)-sha256.Size:]
	mac := hmac.New(sha256.New, []byte(s.cfg.Token))
	mac.Write(raw)
	if !hmac.Equal(sum, mac.Sum(nil)) {
		return fileClaim{}, fmt.Errorf("bad token")
	}
	var c fileClaim
	if err := json.Unmarshal(raw, &c); err != nil {
		return fileClaim{}, fmt.Errorf("bad token")
	}
	if time.Now().Unix() > c.Exp {
		return fileClaim{}, fmt.Errorf("expired")
	}
	return c, nil
}

func (s *service) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "405 - method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/telegram/file/")
	tok, _, _ := strings.Cut(rest, "/")
	if tok == "" {
		http.NotFound(w, r)
		return
	}
	c, err := s.verifyFileClaim(tok)
	if err != nil {
		http.Error(w, "404 - not found", http.StatusNotFound)
		return
	}

	acc, err := store.OpenAccount(s.log, c.Acc, false)
	if err != nil {
		http.Error(w, "404 - not found", http.StatusNotFound)
		return
	}
	defer func() {
		err := acc.Close()
		s.log.Check(err, "closing account after telegram file")
	}()

	var m store.Message
	err = acc.DB.Read(r.Context(), func(tx *bstore.Tx) error {
		m = store.Message{ID: c.ID}
		if err := tx.Get(&m); err != nil {
			return err
		}
		if m.Expunged {
			return fmt.Errorf("removed")
		}
		return nil
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}

	msgr := acc.MessageReader(m)
	defer msgr.Close()
	p, err := m.LoadPart(msgr)
	if err != nil {
		http.Error(w, "500 - internal server error", http.StatusInternalServerError)
		return
	}
	ap, err := partByPath(p, c.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	name := c.Name
	if name == "" {
		_, name, _ = ap.DispositionFilename()
	}
	if name == "" {
		name = "attachment"
	}
	ct := strings.ToLower(ap.MediaType + "/" + ap.MediaSubType)
	if ct == "/" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Method == "HEAD" {
		return
	}
	_, err = io.Copy(w, &moxio.LimitReader{R: ap.ReaderUTF8OrBinary(), Limit: 512 << 20})
	s.log.Check(err, "writing telegram attachment")
}
