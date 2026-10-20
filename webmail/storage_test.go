package webmail

import (
	"testing"

	"github.com/mjl-/mox/message"
)

func TestCollectStorageAttachments(t *testing.T) {
	plain := message.Part{MediaType: "TEXT", MediaSubType: "PLAIN", DecodedSize: 100}
	pdf := message.Part{MediaType: "APPLICATION", MediaSubType: "PDF", DecodedSize: 5_000_000, EndOffset: 5_000_100, BodyOffset: 100, ContentTypeParams: map[string]string{"name": "invoice.pdf"}}
	root := message.Part{MediaType: "MULTIPART", MediaSubType: "MIXED", Parts: []message.Part{plain, pdf}}
	atts, total := collectStorageAttachments(root)
	if total != 5_000_000 || len(atts) != 1 || atts[0].Name != "invoice.pdf" {
		t.Fatalf("%d %#v", total, atts)
	}
	if n, z := collectStorageAttachments(plain); n != nil || z != 0 {
		t.Fatalf("plain text counted as attachment: %v %d", n, z)
	}
}
