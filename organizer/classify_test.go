package organizer

import (
	"net/textproto"
	"slices"
	"testing"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		subject, tag string
		important    bool
	}{
		{"Rs 50.00 was paid on Amazon.in", "finance", true},
		{"Your login request to Groq", "security", true},
		{"Deployment failed", "updates", true},
		{"Special offer for you", "promotions", false},
	} {
		r := Classify("sender@example.org", tc.subject, "", nil, nil)
		if !slices.Contains(r.Tags, tc.tag) || r.Important != tc.important {
			t.Fatalf("%s: %#v", tc.subject, r)
		}
	}
	r := Classify("bank@example.org", "Payment successful", "", textproto.MIMEHeader{"List-Unsubscribe": {"<https://example.org/unsubscribe>"}}, []Rule{{From: "bank@", Tag: "bank", Important: true}})
	if !slices.Contains(r.Tags, "bank") || !slices.Contains(r.Tags, "newsletters") || !r.Important {
		t.Fatal(r)
	}
	// Promotions must not override importance and an empty rule must not tag every mail.
	r = Classify("a@b.c", "Security alert: special offer", "", nil, []Rule{{Tag: "all"}})
	if slices.Contains(r.Tags, "promotions") || slices.Contains(r.Tags, "all") {
		t.Fatal(r)
	}
}
