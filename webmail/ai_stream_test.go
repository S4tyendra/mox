package webmail

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReadAIStream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Looking","extra_content":{"google":{"thought":true,"thought_signature":"abc"}}},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"reasoning_content":" at mail","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"Q\":"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")
	var thought, text, tool string
	msg, stats, err := readAIProvider(strings.NewReader(body), func(kind, chunk string) {
		switch kind {
		case "thought":
			thought += chunk
		case "text":
			text += chunk
		case "tool":
			tool = chunk
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if thought != "Looking at mail" || text != "" || tool != "" {
		t.Fatal(thought, text, tool)
	}
	if stats.Finish != "tool_calls" || stats.SignatureBytes != 3 || len(msg.ToolCalls) != 1 {
		t.Fatal(stats, msg.ToolCalls)
	}
	call := msg.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "search" || call.Function.Arguments != `{"Q":1}` || !strings.Contains(string(call.ExtraContent), `"thought_signature":"abc"`) {
		t.Fatal(call)
	}
	if strings.Contains(string(call.ExtraContent), "Looking") {
		t.Fatal("thought text leaked into signature", string(call.ExtraContent))
	}

	owned := `data: {"choices":[{"delta":{"content":"","extra_content":{"google":{"thought_signature":"loose"}},"tool_calls":[{"index":0,"id":"c","type":"function","extra_content":{"google":{"thought_signature":"owned"}},"function":{"name":"search","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
	msg, stats, err = readAIProvider(strings.NewReader(owned+"\n"), nil)
	if err != nil || stats.SignatureBytes != len("owned") || !strings.Contains(string(msg.ToolCalls[0].ExtraContent), "owned") {
		t.Fatal(err, stats, msg.ToolCalls)
	}

	jsonBody := `{"choices":[{"finish_reason":"stop","message":{"content":"ok","reasoning_content":"hmm","tool_calls":[{"id":"c","type":"function","function":{"name":"search","arguments":"{}"},"extra_content":{"google":{"thought_signature":"sig"}}}]}}]}`
	msg, stats, err = readAIProvider(strings.NewReader(jsonBody), nil)
	if err != nil || msg.Content != "ok" || msg.ReasoningContent != "hmm" || stats.SignatureBytes != 3 {
		t.Fatal(err, msg, stats)
	}
}

func TestNormalizeSearchArgs(t *testing.T) {
	out := normalizeSearchArgs([]byte(`{"Words":"amazon","since":"2026-09-14","unread":true,"Query":{"Offset":20}}`))
	var req organizerSearchRequest
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatal(err, string(out))
	}
	if req.Offset != 20 || req.Query.Filter.Oldest == nil || len(req.Query.Filter.Words) != 1 || req.Query.Filter.Words[0] != "amazon" || len(req.Query.NotFilter.Labels) != 1 {
		t.Fatal(string(out), req)
	}
	nested := normalizeSearchArgs([]byte(`{"Query":{"Filter":{"MailboxID":-1,"Words":["bill"]},"NotFilter":{"Labels":["\\seen"]}},"Limit":1}`))
	dec = json.NewDecoder(bytes.NewReader(nested))
	dec.DisallowUnknownFields()
	req = organizerSearchRequest{}
	if err := dec.Decode(&req); err != nil || req.Limit != 1 || req.Query.Filter.MailboxID != -1 || len(req.Query.NotFilter.Labels) != 1 {
		t.Fatal(err, string(nested), req)
	}
	if cleanAIText("Total ₹1.\n\nCitations:[{\"ID\":897},{\"ID\":896}]") != "Total ₹1." {
		t.Fatal(cleanAIText("Total ₹1.\n\nCitations:[{\"ID\":897},{\"ID\":896}]"))
	}
	day := normalizeSearchArgs([]byte(`{"words":["invoice"],"from":"Kotak","date":"August 14th, 2026"}`))
	req = organizerSearchRequest{}
	if err := json.Unmarshal(day, &req); err != nil || req.Query.Filter.Oldest == nil || req.Query.Filter.Newest == nil || req.Query.Filter.MailboxID != -1 {
		t.Fatal(err, string(day))
	}
	if !req.Query.Filter.Newest.Equal(req.Query.Filter.Oldest.Add(24 * time.Hour)) {
		t.Fatal(req.Query.Filter.Oldest, req.Query.Filter.Newest)
	}
	summary := searchSummary(`{"words":["invoice"],"from":"Kotak","date":"August 14th, 2026"}`, map[string]any{"Messages": []map[string]any{{"id": int64(4), "subject": "Bill"}}}, nil)
	if !strings.Contains(summary, `for "invoice"`) || !strings.Contains(summary, "from Kotak") || !strings.Contains(summary, "since 14 Aug 2026") || !strings.Contains(summary, "until 15 Aug 2026") || !strings.Contains(summary, "1 mail") {
		t.Fatal(summary)
	}
}
