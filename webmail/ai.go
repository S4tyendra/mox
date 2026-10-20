package webmail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mjl-/bstore"
	"github.com/mjl-/mox/store"
	"github.com/mjl-/sherpa"
)

type aiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	// Gemini 3 signs a tool call in extra_content.google.thought_signature.
	// The next Chat Completions turn is rejected with HTTP 400 if it is dropped.
	ExtraContent json.RawMessage `json:"extra_content,omitempty"`
}
type aiProviderMessage struct {
	Content          string       `json:"content"`
	ReasoningContent string       `json:"reasoning_content,omitempty"`
	ToolCalls        []aiToolCall `json:"tool_calls,omitempty"`
}

// aiStreamEvent is one progress item on the mailbox SSE connection.
// Kind is thought, text, tool, or note. Text is a delta or a short status line.
type aiStreamEvent struct {
	ChatID string
	Kind   string
	Text   string
}
type aiCitation struct {
	ID      int64
	Subject string
}
type aiMailSession struct {
	sources   map[int64]string
	mailboxes []store.Mailbox
	move      *organizerAction
	remaining int
	calls     int
}

func validateAIHistory(ctx context.Context, history []aiChatTurn) {
	organizerCheck(ctx, len(history) > 200 || len(history)%2 != 0, "chat history must contain up to 100 complete turns; start a new chat")
	size := 0
	for i, turn := range history {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		organizerCheck(ctx, turn.Role != role || len(turn.Text) == 0 || len(turn.Text) > 40000 || len(turn.Citations) > 100, "invalid chat history")
		size += len(turn.Text)
	}
	organizerCheck(ctx, size > 600000, "chat history is too large; start a new chat")
}

func (s *aiMailSession) historySources(ctx context.Context, history []aiChatTurn) {
	ids := map[int64]bool{}
	for _, turn := range history {
		for _, id := range turn.Citations {
			ids[id] = true
		}
		for _, match := range aiMailLink.FindAllStringSubmatch(turn.Text, 100) {
			id, _ := strconv.ParseInt(match[1], 10, 64)
			ids[id] = true
		}
	}
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	organizerCheck(ctx, len(ids) > 1000, "too many referenced messages; start a new chat")
	xdbread(ctx, ri.Account, func(tx *bstore.Tx) {
		state := msgState{acc: ri.Account, log: ri.Log}
		defer state.clear()
		for id := range ids {
			if _, ok := s.sources[id]; ok {
				continue
			}
			m := store.Message{ID: id}
			if id <= 0 || tx.Get(&m) != nil || m.Expunged {
				continue
			}
			mi, err := messageItem(ri.Log, m, &state, nil)
			if err == nil {
				s.sources[id] = mi.Envelope.Subject
			}
		}
	})
}

func aiMailData(mi MessageItem, text string) map[string]any {
	if len(text) > 8000 {
		text = text[:8000]
	}
	return map[string]any{
		"id": mi.Message.ID, "mailbox_id": mi.Message.MailboxID,
		"subject": mi.Envelope.Subject, "from": mi.Envelope.From,
		"received": mi.Message.Received, "seen": mi.Message.Seen,
		"unread": !mi.Message.Seen, "starred": mi.Message.Flagged,
		"tags": mi.Message.Keywords, "text": text,
	}
}

// Adapt the MCP tools to Chat Completions. The web assistant has a smaller
// capability set: text reads only, and moves are proposals, never executed.
func aiMCPTools() []map[string]any {
	result := []map[string]any{}
	for _, tool := range mcpTools() {
		switch tool.Name {
		case "mailboxes":
		case "read_mail":
			tool.Description = "Read mail metadata (including seen/unread, starred and tags) and plain text, capped at 8000 bytes. Reading does not mark seen."
		case "search":
			tool.Description = "Search mail. Every field is optional and combined with AND. words, from, to, and subject are substrings. since is inclusive and until is exclusive; use YYYY-MM-DD or RFC3339. date is one calendar day. Omit mailbox_id to skip Junk and Trash; -1 does the same, 0 includes them, any other id is one folder. unread and starred select flag state. attachment is none, any, image, pdf, archive, spreadsheet, document, or presentation. Pass offset from the previous next_offset until next_offset is null."
			tool.InputSchema = toolSchema(map[string]any{
				"words":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"from":       map[string]any{"type": "string"},
				"to":         map[string]any{"type": "string"},
				"subject":    map[string]any{"type": "string"},
				"since":      map[string]any{"type": "string"},
				"until":      map[string]any{"type": "string"},
				"date":       map[string]any{"type": "string"},
				"mailbox_id": map[string]any{"type": "integer"},
				"unread":     map[string]any{"type": "boolean"},
				"starred":    map[string]any{"type": "boolean"},
				"attachment": map[string]any{"type": "string", "enum": []string{"", "none", "any", "image", "pdf", "archive", "spreadsheet", "document", "presentation"}},
				"offset":     map[string]any{"type": "integer", "minimum": 0},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			})
		case "mail_body":
			tool.Description = "Read the plain text body of a mail. Only Format=text is permitted. Reading does not mark seen."
			tool.InputSchema = toolSchema(map[string]any{"ID": map[string]any{"type": "integer", "minimum": 1}, "Format": map[string]any{"type": "string", "enum": []string{"text"}}}, "ID", "Format")
		case "action":
			tool.Description = "Propose moving messages. This NEVER executes a move: the user must click Apply. Action must be move. Only IDs returned by mail tools or initial context are allowed."
			tool.InputSchema = toolSchema(map[string]any{"Action": map[string]any{"type": "string", "enum": []string{"move"}}, "IDs": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "minItems": 1, "maxItems": 500}, "MailboxID": map[string]any{"type": "integer", "minimum": 1}}, "Action", "IDs", "MailboxID")
		default:
			continue
		}
		result = append(result, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema, "strict": false}})
	}
	return result
}

func (s *aiMailSession) validMove(a *organizerAction) bool {
	if a == nil || a.Action != "move" || len(a.IDs) == 0 || len(a.IDs) > 500 || len(a.Tags) > 0 {
		return false
	}
	for _, id := range a.IDs {
		if _, ok := s.sources[id]; !ok {
			return false
		}
	}
	for _, mb := range s.mailboxes {
		if mb.ID == a.MailboxID {
			return true
		}
	}
	return false
}

func (s *aiMailSession) tool(ctx context.Context, call aiToolCall) (result any) {
	defer func() {
		if x := recover(); x != nil {
			if e, ok := x.(*sherpa.Error); ok {
				result = map[string]string{"Error": e.Message}
			} else {
				result = map[string]string{"Error": "Mail tool failed"}
			}
		}
	}()
	s.calls++
	organizerCheck(ctx, s.calls > 16, "tool limit reached; answer using results already available")
	args := json.RawMessage(call.Function.Arguments)
	organizerCheck(ctx, len(args) > 16384 || !json.Valid(args), "invalid tool arguments")
	if call.Function.Name == "action" {
		var a organizerAction
		d := json.NewDecoder(bytes.NewReader(args))
		d.DisallowUnknownFields()
		xcheckuserf(ctx, d.Decode(&a), "invalid move proposal")
		organizerCheck(ctx, !s.validMove(&a) || len(a.Tags) > 0, "only a move of known messages to a known mailbox can be proposed")
		s.move = &a
		return map[string]any{"Proposal": a, "Status": "Awaiting user approval; no messages moved"}
	}
	switch call.Function.Name {
	case "mailboxes", "read_mail":
	case "search":
		args = normalizeSearchArgs(args)
	case "mail_body":
		var req struct {
			ID     int64
			Format string
		}
		xcheckuserf(ctx, json.Unmarshal(args, &req), "invalid body request")
		organizerCheck(ctx, req.Format != "text", "web AI can only read text")
	default:
		organizerCheck(ctx, true, "tool is not available to web AI")
	}
	// Use the same account-scoped implementation and scope checks as OAuth MCP.
	value, _ := (&mcpServer{webmail: Webmail{}}).call(ctx, []string{"mail:read"}, call.Function.Name, args)
	newSources := map[int64]string{}
	switch v := value.(type) {
	case organizerSearchResult:
		mails := []map[string]any{}
		for _, mi := range v.Messages {
			newSources[mi.Message.ID] = mi.Envelope.Subject
			preview := ""
			if mi.Message.Preview != nil {
				preview = *mi.Message.Preview
			}
			mails = append(mails, aiMailData(mi, preview))
		}
		result = map[string]any{"Messages": mails, "NextOffset": v.NextOffset}
	case organizerMessage:
		newSources[v.Item.Message.ID] = v.Item.Envelope.Subject
		result = aiMailData(v.Item, strings.Join(v.Parsed.Texts, "\n\n"))
	case map[string]any:
		var req struct{ ID int64 }
		json.Unmarshal(args, &req)
		mi := Webmail{}.MessageItem(ctx, req.ID)
		newSources[req.ID] = mi.Envelope.Subject
		result = aiMailData(mi, fmt.Sprint(v["Text"]))
	default:
		result = value
	}
	data, err := json.Marshal(result)
	xcheckf(ctx, err, "encode mail tool result")
	organizerCheck(ctx, len(data) > s.remaining, "mail context limit reached; narrow the search or answer using available results")
	s.remaining -= len(data)
	for id, subject := range newSources {
		s.sources[id] = subject
	}
	return result
}

var aiMailLink = regexp.MustCompile(`\]\(mail:([0-9]+)\)`)

func (s *aiMailSession) citations(result *aiResult) {
	requested := result.Citations
	for _, match := range aiMailLink.FindAllStringSubmatch(result.Text, 100) {
		id, _ := strconv.ParseInt(match[1], 10, 64)
		requested = append(requested, aiCitation{ID: id})
	}
	result.Citations = []aiCitation{}
	seen := map[int64]bool{}
	for _, cite := range requested {
		if subject, ok := s.sources[cite.ID]; ok && !seen[cite.ID] {
			result.Citations = append(result.Citations, aiCitation{ID: cite.ID, Subject: subject})
			seen[cite.ID] = true
		}
	}
}

func validAIChatID(id string) bool {
	if len(id) < 8 || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return false
		}
	}
	return true
}

func geminiEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "generativelanguage.googleapis.com" || strings.HasSuffix(host, ".googleapis.com")
}

// aiPace batches token deltas so a stream does not crowd mailbox events.
type aiPace struct {
	emit            func(kind, text string)
	thought, text   strings.Builder
	last            time.Time
	thoughtN, textN int
}

func (e *aiPace) add(kind, chunk string) {
	if e == nil || e.emit == nil || chunk == "" {
		return
	}
	var buf *strings.Builder
	var n *int
	switch kind {
	case "thought":
		buf, n = &e.thought, &e.thoughtN
	case "text":
		buf, n = &e.text, &e.textN
	default:
		e.flush()
		e.emit(kind, chunk)
		return
	}
	if *n >= 8000 {
		return
	}
	if *n+len(chunk) > 8000 {
		chunk = chunk[:8000-*n]
	}
	*n += len(chunk)
	buf.WriteString(chunk)
	if buf.Len() >= 48 || e.last.IsZero() || time.Since(e.last) >= 70*time.Millisecond {
		e.flush()
	}
}

func (e *aiPace) flush() {
	if e == nil || e.emit == nil {
		return
	}
	if e.thought.Len() > 0 {
		e.emit("thought", e.thought.String())
		e.thought.Reset()
	}
	if e.text.Len() > 0 {
		e.emit("text", e.text.String())
		e.text.Reset()
	}
	e.last = time.Now()
}

func aiCompletion(ctx context.Context, s store.OrganizerSettings, request map[string]any, emit func(kind, text string)) aiProviderMessage {
	var resp *http.Response
	providerErr := ""
	request["stream"] = true
	if geminiEndpoint(s.AIEndpoint) {
		request["extra_body"] = map[string]any{"google": map[string]any{"thinking_config": map[string]any{"include_thoughts": true, "thinking_level": "low"}}}
	}
	for attempt := 0; attempt < 3; attempt++ {
		body, _ := json.Marshal(request)
		r, err := http.NewRequestWithContext(ctx, "POST", s.AIEndpoint+"/chat/completions", bytes.NewReader(body))
		xcheckf(ctx, err, "create AI request")
		r.Header.Set("Content-Type", "application/json")
		if s.AIKey != "" {
			r.Header.Set("Authorization", "Bearer "+s.AIKey)
		}
		resp, err = organizerAIClient.Do(r)
		organizerCheck(ctx, err != nil, "AI endpoint could not be reached")
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			break
		}
		var failure struct {
			Error struct{ Param, Message string }
		}
		json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&failure)
		resp.Body.Close()
		providerErr = strings.TrimSpace(failure.Error.Message)
		msg := strings.ToLower(failure.Error.Message)
		switch {
		case resp.StatusCode == http.StatusBadRequest && (failure.Error.Param == "max_tokens" || strings.Contains(failure.Error.Message, "max_tokens") && strings.Contains(failure.Error.Message, "max_completion_tokens")):
			delete(request, "max_tokens")
			request["max_completion_tokens"] = 1500
			continue
		case request["extra_body"] != nil && resp.StatusCode == http.StatusBadRequest && (strings.Contains(msg, "extra_body") || strings.Contains(msg, "thinking_config") || strings.Contains(msg, "include_thoughts") || strings.Contains(msg, "thinking_level")):
			if strings.Contains(msg, "thinking_level") && !strings.Contains(msg, "include_thoughts") && !strings.Contains(msg, "extra_body") {
				request["extra_body"] = map[string]any{"google": map[string]any{"thinking_config": map[string]any{"include_thoughts": true}}}
			} else {
				delete(request, "extra_body")
			}
			continue
		}
		break
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := providerErr
		if len(detail) > 300 {
			detail = detail[:300]
		}
		if detail == "" {
			detail = "use a model supporting Chat Completions tools"
		}
		organizerCheck(ctx, true, "AI endpoint returned HTTP %d; %s", resp.StatusCode, detail)
	}
	paced := &aiPace{emit: emit}
	reply, stats, err := readAIProvider(io.LimitReader(resp.Body, 4*1024*1024), paced.add)
	paced.flush()
	xcheckf(ctx, err, "read AI response")
	model, _ := request["model"].(string)
	tools := strings.Join(stats.Tools, ",")
	pkglog.Info("ai completion",
		slog.String("model", model),
		slog.Int("status", resp.StatusCode),
		slog.String("finish", stats.Finish),
		slog.String("tools", tools),
		slog.Int("signature_bytes", stats.SignatureBytes),
		slog.Int("thought_bytes", stats.ThoughtBytes),
		slog.Int("text_bytes", stats.TextBytes),
		slog.Int("deltas", stats.Deltas),
		slog.String("fields", stats.Fields),
	)
	if len(stats.Tools) > 0 && stats.SignatureBytes == 0 {
		pkglog.Error("ai tool call missing thought signature", slog.String("model", model), slog.String("tools", tools), slog.Int("deltas", stats.Deltas))
	}
	if reply.ReasoningContent != "" {
		paced.add("thought", reply.ReasoningContent)
		paced.flush()
	}
	organizerCheck(ctx, len(reply.ToolCalls) == 0 && strings.TrimSpace(reply.Content) == "", "AI endpoint returned no answer")
	return reply
}

type aiStreamStats struct {
	Deltas, ThoughtBytes, TextBytes, SignatureBytes int
	Finish, Fields                                  string
	Tools                                           []string
}

type aiDeltaCall struct {
	id, typ, name string
	args          strings.Builder
	extra         json.RawMessage
}

func signatureValue(raw json.RawMessage) string {
	var v struct {
		Google struct {
			ThoughtSignature string `json:"thought_signature"`
		} `json:"google"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v.Google.ThoughtSignature
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}

// readAIProvider accepts either a Chat Completions JSON body or an SSE stream.
// A thought signature that arrives on a text delta is copied onto the first
// tool call that does not already carry one.
func readAIProvider(r io.Reader, emit func(kind, text string)) (aiProviderMessage, aiStreamStats, error) {
	br := bufio.NewReader(r)
	peek, _ := br.Peek(64)
	trimmed := bytes.TrimLeft(peek, " \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("{")) {
		return readAIJSON(br)
	}
	return readAISSE(br, emit)
}

func readAIJSON(r io.Reader) (aiProviderMessage, aiStreamStats, error) {
	var body struct {
		Choices []struct {
			Message      aiProviderMessage
			FinishReason string `json:"finish_reason"`
		}
		Error struct{ Message string }
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return aiProviderMessage{}, aiStreamStats{}, err
	}
	if body.Error.Message != "" && len(body.Choices) == 0 {
		return aiProviderMessage{}, aiStreamStats{}, fmt.Errorf("%s", body.Error.Message)
	}
	if len(body.Choices) == 0 {
		return aiProviderMessage{}, aiStreamStats{}, nil
	}
	msg := body.Choices[0].Message
	stats := aiStreamStats{Finish: body.Choices[0].FinishReason, ThoughtBytes: len(msg.ReasoningContent), TextBytes: len(msg.Content), Fields: "json"}
	for _, call := range msg.ToolCalls {
		stats.Tools = append(stats.Tools, call.Function.Name)
		stats.SignatureBytes += len(signatureValue(call.ExtraContent))
	}
	return msg, stats, nil
}

func readAISSE(r io.Reader, emit func(kind, text string)) (aiProviderMessage, aiStreamStats, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var (
		stats   aiStreamStats
		content strings.Builder
		thought strings.Builder
		calls   = map[int]*aiDeltaCall{}
		order   []int
		loose   string
		provErr string
		fields  = map[string]bool{}
	)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          json.RawMessage `json:"content"`
					ReasoningContent json.RawMessage `json:"reasoning_content"`
					ExtraContent     json.RawMessage `json:"extra_content"`
					ToolCalls        []struct {
						Index        *int            `json:"index"`
						ID           string          `json:"id"`
						Type         string          `json:"type"`
						ExtraContent json.RawMessage `json:"extra_content"`
						Function     struct {
							Name      string          `json:"name"`
							Arguments json.RawMessage `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal([]byte(line), &chunk) != nil {
			pkglog.Info("ai stream chunk skipped", slog.Int("bytes", len(line)))
			continue
		}
		if chunk.Error.Message != "" {
			provErr = chunk.Error.Message
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		stats.Deltas++
		choice := chunk.Choices[0]
		if choice.FinishReason != nil {
			stats.Finish = *choice.FinishReason
		}
		delta := choice.Delta
		if len(delta.Content) > 0 {
			fields["content"] = true
		}
		if len(delta.ReasoningContent) > 0 {
			fields["reasoning_content"] = true
		}
		if len(delta.ExtraContent) > 0 {
			fields["extra_content"] = true
		}
		if len(delta.ToolCalls) > 0 {
			fields["tool_calls"] = true
		}
		thoughtFlag, thoughtText := googleThought(delta.ExtraContent)
		if sig := signatureValue(delta.ExtraContent); sig != "" {
			loose = sig
		}
		if text := rawText(delta.ReasoningContent); text != "" {
			thought.WriteString(text)
			stats.ThoughtBytes += len(text)
			if emit != nil {
				emit("thought", text)
			}
		}
		if thoughtText != "" {
			thought.WriteString(thoughtText)
			stats.ThoughtBytes += len(thoughtText)
			if emit != nil {
				emit("thought", thoughtText)
			}
		}
		if text := rawText(delta.Content); text != "" {
			if thoughtFlag {
				thought.WriteString(text)
				stats.ThoughtBytes += len(text)
				if emit != nil {
					emit("thought", text)
				}
			} else {
				content.WriteString(text)
				stats.TextBytes += len(text)
				if emit != nil {
					emit("text", text)
				}
			}
		}
		for i, tc := range delta.ToolCalls {
			index := i
			if tc.Index != nil {
				index = *tc.Index
			}
			call := calls[index]
			if call == nil {
				call = &aiDeltaCall{}
				calls[index] = call
				order = append(order, index)
			}
			if tc.ID != "" {
				call.id = tc.ID
			}
			if tc.Type != "" {
				call.typ = tc.Type
			}
			if tc.Function.Name != "" && call.name == "" {
				call.name = tc.Function.Name
				stats.Tools = append(stats.Tools, tc.Function.Name)
			}
			if args := rawText(tc.Function.Arguments); args != "" {
				call.args.WriteString(args)
			}
			if signatureValue(tc.ExtraContent) != "" && signatureValue(call.extra) == "" {
				call.extra = append(json.RawMessage(nil), tc.ExtraContent...)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return aiProviderMessage{}, stats, err
	}
	if provErr != "" && content.Len() == 0 && len(calls) == 0 {
		return aiProviderMessage{}, stats, fmt.Errorf("%s", provErr)
	}
	msg := aiProviderMessage{Content: content.String()}
	sort.Ints(order)
	if loose != "" && len(order) > 0 && signatureValue(calls[order[0]].extra) == "" {
		wrapped, _ := json.Marshal(map[string]any{"google": map[string]string{"thought_signature": loose}})
		calls[order[0]].extra = wrapped
	}
	for _, index := range order {
		call := calls[index]
		item := aiToolCall{ID: call.id, Type: call.typ, ExtraContent: call.extra}
		if item.Type == "" {
			item.Type = "function"
		}
		item.Function.Name = call.name
		item.Function.Arguments = call.args.String()
		stats.SignatureBytes += len(signatureValue(item.ExtraContent))
		msg.ToolCalls = append(msg.ToolCalls, item)
	}
	stats.ThoughtBytes = thought.Len()
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	stats.Fields = strings.Join(names, ",")
	return msg, stats, nil
}

var aiCitationDump = regexp.MustCompile(`(?s)\n*Citations:\s*\[\s*(\{\s*"ID"\s*:\s*\d+\s*\}\s*,?\s*)+\]\s*$`)

func cleanAIText(text string) string {
	return strings.TrimSpace(aiCitationDump.ReplaceAllString(text, ""))
}

func normalizeSearchArgs(raw json.RawMessage) json.RawMessage {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return raw
	}
	query, _ := doc["Query"].(map[string]any)
	if query == nil {
		query = map[string]any{}
	}
	filter, _ := query["Filter"].(map[string]any)
	if filter == nil {
		filter = map[string]any{}
	}
	notFilter, _ := query["NotFilter"].(map[string]any)
	if notFilter == nil {
		notFilter = map[string]any{}
	}
	_, sawMailbox := filter["MailboxID"]
	move := func(src map[string]any, from, to string, dest map[string]any) {
		v, ok := src[from]
		if !ok {
			return
		}
		delete(src, from)
		if to == "MailboxID" {
			sawMailbox = true
		}
		if _, exists := dest[to]; !exists {
			dest[to] = v
		}
	}
	for _, src := range []map[string]any{doc, query} {
		if v, ok := src["date"]; ok {
			delete(src, "date")
			if _, exists := filter["Oldest"]; !exists {
				filter["Oldest"] = v
			}
			if _, exists := filter["Newest"]; !exists {
				filter["Newest"] = v
			}
		}
		for from, to := range map[string]string{"words": "Words", "from": "From", "to": "To", "subject": "Subject", "labels": "Labels", "since": "Oldest", "until": "Newest", "oldest": "Oldest", "newest": "Newest", "mailbox_id": "MailboxID", "attachment": "Attachments"} {
			move(src, from, to, filter)
		}
		for _, key := range []string{"Words", "From", "To", "Subject", "Labels", "Oldest", "Newest", "MailboxID", "Attachments", "Headers", "SizeMin", "SizeMax", "MailboxChildrenIncluded"} {
			move(src, key, key, filter)
		}
		move(src, "Offset", "Offset", doc)
		move(src, "Limit", "Limit", doc)
		move(src, "offset", "Offset", doc)
		move(src, "limit", "Limit", doc)
		if v, ok := src["unread"].(bool); ok && v {
			notFilter["Labels"] = []any{`\seen`}
		}
		delete(src, "unread")
		if v, ok := src["starred"].(bool); ok && v {
			filter["Labels"] = appendList(filter["Labels"], `\flagged`)
		}
		delete(src, "starred")
	}
	if !sawMailbox {
		filter["MailboxID"] = -1
	}
	for _, key := range []string{"Words", "From", "To", "Subject", "Labels"} {
		if v, ok := filter[key]; ok {
			if list := stringList(v); list != nil {
				filter[key] = list
			} else {
				delete(filter, key)
			}
		}
		if v, ok := notFilter[key]; ok {
			if list := stringList(v); list != nil {
				notFilter[key] = list
			} else {
				delete(notFilter, key)
			}
		}
	}
	if v, ok := filter["Oldest"]; ok {
		filter["Oldest"] = dateTime(v)
	}
	if v, ok := filter["Newest"]; ok {
		filter["Newest"] = dateTime(v)
	}
	if oldest, ok := filter["Oldest"].(string); ok {
		if newest, ok := filter["Newest"].(string); ok {
			filter["Newest"] = extendUntil(oldest, newest)
		}
	}
	query["Filter"] = filter
	if len(notFilter) > 0 {
		query["NotFilter"] = notFilter
	} else {
		delete(query, "NotFilter")
	}
	cleanQuery := map[string]any{}
	for _, key := range []string{"Filter", "NotFilter", "OrderAsc", "Threading"} {
		if v, ok := query[key]; ok {
			cleanQuery[key] = v
		}
	}
	clean := map[string]any{"Query": cleanQuery}
	if v, ok := doc["Limit"]; ok {
		clean["Limit"] = v
	}
	if v, ok := doc["Offset"]; ok {
		clean["Offset"] = v
	}
	out, err := json.Marshal(clean)
	if err != nil {
		return raw
	}
	return out
}

func stringList(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		if len(t) == 0 {
			return nil
		}
		return t
	default:
		return v
	}
}

var dateOrdinal = regexp.MustCompile(`(\d+)(st|nd|rd|th)\b`)

func dateTime(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	s = strings.TrimSpace(dateOrdinal.ReplaceAllString(s, "$1"))
	if t, ok := parseAITime(s); ok {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

func parseAITime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02", "2006-01-02 15:04:05", "2006-01-02 15:04", "January 2, 2006", "2 January 2006", "Jan 2, 2006", "2 Jan 2006", "02 Jan 2006", "January 2 2006", "Jan 2 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// A date-only until equal to since would exclude that whole day. Push it to the next midnight.
func extendUntil(oldest, newest string) string {
	o, err1 := time.Parse(time.RFC3339, oldest)
	n, err2 := time.Parse(time.RFC3339, newest)
	if err1 != nil || err2 != nil || n.After(o) {
		return newest
	}
	return o.Add(24 * time.Hour).Format(time.RFC3339)
}

func appendList(v any, item string) []any {
	switch t := v.(type) {
	case nil:
		return []any{item}
	case string:
		if t == "" || t == item {
			return []any{item}
		}
		return []any{t, item}
	case []any:
		return append(t, item)
	default:
		return []any{item}
	}
}

type aiToolMail struct {
	ID       int64
	Subject  string
	From     string
	Received string
}
type aiToolCard struct {
	Summary string
	Mails   []aiToolMail `json:",omitempty"`
}

func aiToolEvent(call aiToolCall, result any, mailboxes []store.Mailbox) string {
	card := aiToolCard{Summary: aiToolSummary(call, result, mailboxes), Mails: aiToolMails(result)}
	if len(card.Mails) > 20 {
		card.Mails = card.Mails[:20]
	}
	data, err := json.Marshal(card)
	if err != nil {
		return ""
	}
	return string(data)
}

func aiToolSummary(call aiToolCall, result any, mailboxes []store.Mailbox) string {
	if text, ok := aiToolError(result); ok {
		return "Could not " + call.Function.Name + ": " + text
	}
	switch call.Function.Name {
	case "mailboxes":
		return "Listed mailboxes"
	case "read_mail", "mail_body":
		if mail, ok := aiOneMail(result); ok {
			return "Read " + quote(mail.Subject)
		}
		return "Read a message"
	case "action":
		var req struct {
			IDs       []int64
			MailboxID int64
		}
		json.Unmarshal([]byte(call.Function.Arguments), &req)
		return fmt.Sprintf("Proposed moving %d %s %s", len(req.IDs), plural(len(req.IDs), "mail", "mails"), mailboxLabel(req.MailboxID, mailboxes))
	case "search":
		return searchSummary(call.Function.Arguments, result, mailboxes)
	default:
		return call.Function.Name
	}
}

func searchSummary(args string, result any, mailboxes []store.Mailbox) string {
	var req organizerSearchRequest
	json.Unmarshal(normalizeSearchArgs([]byte(args)), &req)
	f := req.Query.Filter
	var parts []string
	if len(f.Words) > 0 {
		quoted := make([]string, len(f.Words))
		for i, word := range f.Words {
			quoted[i] = quote(word)
		}
		parts = append(parts, "for "+strings.Join(quoted, ", "))
	}
	if len(f.From) > 0 {
		parts = append(parts, "from "+strings.Join(f.From, ", "))
	}
	if len(f.To) > 0 {
		parts = append(parts, "to "+strings.Join(f.To, ", "))
	}
	if len(f.Subject) > 0 {
		quoted := make([]string, len(f.Subject))
		for i, word := range f.Subject {
			quoted[i] = quote(word)
		}
		parts = append(parts, "subject "+strings.Join(quoted, ", "))
	}
	if f.Oldest != nil {
		parts = append(parts, "since "+dayLabel(*f.Oldest))
	}
	if f.Newest != nil {
		parts = append(parts, "until "+dayLabel(*f.Newest))
	}
	if f.Attachments != "" {
		parts = append(parts, string(f.Attachments)+" attachments")
	}
	parts = append(parts, mailboxLabel(f.MailboxID, mailboxes))
	for _, label := range req.Query.NotFilter.Labels {
		if label == `\seen` {
			parts = append(parts, "unread only")
		}
	}
	for _, label := range f.Labels {
		if label == `\flagged` {
			parts = append(parts, "starred")
		}
	}
	if req.Offset > 0 {
		parts = append(parts, fmt.Sprintf("from result %d", req.Offset+1))
	}
	summary := "Searched " + strings.Join(parts, ", ")
	n := 0
	switch v := result.(type) {
	case map[string]any:
		if msgs, ok := v["Messages"].([]map[string]any); ok {
			n = len(msgs)
		}
		if _, more := v["NextOffset"]; more && v["NextOffset"] != nil {
			summary += fmt.Sprintf(" · %d %s, more remain", n, plural(n, "mail", "mails"))
			return summary
		}
	}
	summary += fmt.Sprintf(" · %d %s", n, plural(n, "mail", "mails"))
	return summary
}

func mailboxLabel(id int64, mailboxes []store.Mailbox) string {
	switch id {
	case -1:
		return "except Junk and Trash"
	case 0:
		return "in every mailbox"
	}
	for _, mb := range mailboxes {
		if mb.ID == id {
			return "in " + mb.Name
		}
	}
	return fmt.Sprintf("in mailbox %d", id)
}

func dayLabel(t time.Time) string {
	t = t.UTC()
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		return t.Format("2 Jan 2006")
	}
	return t.Format("2 Jan 2006 15:04")
}

func quote(s string) string {
	return `"` + s + `"`
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func aiToolError(result any) (string, bool) {
	switch v := result.(type) {
	case map[string]string:
		if v["Error"] != "" {
			return v["Error"], true
		}
	case map[string]any:
		if text, ok := v["Error"].(string); ok && text != "" {
			return text, true
		}
	}
	return "", false
}

func aiToolMails(result any) []aiToolMail {
	switch v := result.(type) {
	case map[string]any:
		if msgs, ok := v["Messages"].([]map[string]any); ok {
			mails := make([]aiToolMail, 0, len(msgs))
			for _, msg := range msgs {
				if mail, ok := aiMailLine(msg); ok {
					mails = append(mails, mail)
				}
			}
			return mails
		}
		if mail, ok := aiMailLine(v); ok && mail.Subject != "" {
			return []aiToolMail{mail}
		}
	}
	return nil
}

func aiOneMail(result any) (aiToolMail, bool) {
	if m, ok := result.(map[string]any); ok {
		return aiMailLine(m)
	}
	return aiToolMail{}, false
}

func aiMailLine(m map[string]any) (aiToolMail, bool) {
	id, ok := aiInt(m["id"])
	if !ok {
		return aiToolMail{}, false
	}
	subject, _ := m["subject"].(string)
	line := aiToolMail{ID: id, Subject: subject, From: aiFrom(m["from"])}
	switch received := m["received"].(type) {
	case time.Time:
		line.Received = received.UTC().Format("2 Jan 2006 15:04")
	case string:
		line.Received = received
	}
	return line, true
}

func aiFrom(v any) string {
	list, ok := v.([]MessageAddress)
	if !ok || len(list) == 0 {
		return ""
	}
	a := list[0]
	host := a.Domain.Unicode
	if host == "" {
		host = a.Domain.ASCII
	}
	email := ""
	if a.User != "" && host != "" {
		email = a.User + "@" + host
	}
	if a.Name != "" && email != "" {
		return a.Name + " <" + email + ">"
	}
	if a.Name != "" {
		return a.Name
	}
	return email
}

func aiInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func googleThought(raw json.RawMessage) (bool, string) {
	var v struct {
		Google struct {
			Thought any `json:"thought"`
		} `json:"google"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil || v.Google.Thought == nil {
		return false, ""
	}
	switch thought := v.Google.Thought.(type) {
	case bool:
		return thought, ""
	case string:
		if thought == "" {
			return false, ""
		}
		return true, thought
	default:
		return false, ""
	}
}
