package junk

import (
	"golang.org/x/net/html"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mjl-/mox/mlog"
)

func TestHTMLWordBoundariesAndHosts(t *testing.T) {
	meta := map[string]struct{}{}
	r := &htmlTextReader{t: html.NewTokenizer(strings.NewReader(`<p>hello</p><p>world</p><div>pay<span>ment</span></div><script>ignore</script><a href="https://offer.example/path?private=123">offer</a>`)), meta: meta}
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(buf)
	if strings.Contains(s, "helloworld") || !strings.Contains(s, "payment") || strings.Contains(s, "ignore") {
		t.Fatal(s)
	}
	if _, ok := meta["html-link-host:offer.example"]; !ok {
		t.Fatal(meta)
	}
	for k := range meta {
		if strings.Contains(k, "private") {
			t.Fatal("URL query must not become model feature")
		}
	}
}

func FuzzParseMessage(f *testing.F) {
	f.Add("")
	add := func(p string) {
		buf, err := os.ReadFile(p)
		if err != nil {
			f.Fatalf("reading file %q: %v", p, err)
		}
		f.Add(string(buf))
	}
	add(filepath.FromSlash("../testdata/junk/parse.eml"))
	add(filepath.FromSlash("../testdata/junk/parse2.eml"))
	add(filepath.FromSlash("../testdata/junk/parse3.eml"))

	dbPath := filepath.FromSlash("../testdata/junk/parse.db")
	bloomPath := filepath.FromSlash("../testdata/junk/parse.bloom")
	os.Remove(dbPath)
	os.Remove(bloomPath)
	params := Params{Twograms: true}
	log := mlog.New("junk", nil)
	jf, err := NewFilter(ctxbg, log, params, dbPath, bloomPath)
	if err != nil {
		f.Fatalf("new filter: %v", err)
	}
	f.Fuzz(func(t *testing.T, s string) {
		jf.tokenizeMail(s)
	})
}
