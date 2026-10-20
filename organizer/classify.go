// Package organizer classifies mail locally. Message content never leaves the server.
package organizer

import (
	"net/textproto"
	"slices"
	"strings"
)

type Rule struct {
	From      string
	Subject   string
	Tag       string
	Important bool
}

type Result struct {
	Tags      []string
	Important bool
	Reasons   []string
}

func Classify(from, subject, text string, headers textproto.MIMEHeader, rules []Rule) Result {
	r := Result{Tags: []string{}, Reasons: []string{}}
	s := strings.ToLower(subject)
	f := strings.ToLower(from)
	add := func(tag, reason string, important bool) {
		if !slices.Contains(r.Tags, tag) {
			r.Tags = append(r.Tags, tag)
		}
		r.Reasons = append(r.Reasons, reason)
		r.Important = r.Important || important
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	if has("verification code", "one-time", "otp", "login request", "security alert", "password reset", "sign-in", "suspicious activity") {
		add("security", "Login or account security message", true)
	}
	if has("receipt", "was paid", "payment of", "payment successful", "payment confirmation", "invoice", "transaction alert", "debited", "credited", "card statement", "bank statement") {
		add("finance", "Payment, receipt or statement", true)
	}
	if has("order confirmation", "your order", "shipped", "delivery update", "out for delivery") {
		add("orders", "Order or delivery update", false)
	}
	if strings.HasSuffix(f, "@github.com") || has("pull request", "build failed", "deployment failed", "incident", "downtime", "service disruption") {
		add("updates", "Development or service update", has("failed", "incident", "downtime", "disruption"))
	}
	// A subscription header indicates a mailing list, not spam. Never train the
	// spam model or junk a message based only on a category prediction.
	if headers.Get("List-Unsubscribe") != "" || headers.Get("List-Id") != "" {
		add("newsletters", "Mailing list", false)
	}
	if !r.Important && has("sale", "discount", "limited offer", "special offer", "deal of", "save up to") {
		add("promotions", "Promotional subject", false)
	}
	for _, rule := range rules {
		if rule.From == "" && rule.Subject == "" {
			continue
		}
		if rule.From != "" && !strings.Contains(f, strings.ToLower(rule.From)) {
			continue
		}
		if rule.Subject != "" && !strings.Contains(s, strings.ToLower(rule.Subject)) {
			continue
		}
		add(rule.Tag, "Your rule", rule.Important)
	}
	if r.Important {
		r.Tags = append(r.Tags, "important")
	}
	slices.Sort(r.Tags)
	r.Tags = slices.Compact(r.Tags)
	return r
}
