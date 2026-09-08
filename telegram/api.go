package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// APIURL is the Telegram Bot API origin. Tests replace this with a mock server.
var APIURL = "https://api.telegram.org"

var httpClient = &http.Client{Timeout: 45 * time.Second}

type apiClient struct {
	token string
}

func newAPIClient(token string) *apiClient {
	return &apiClient{token: token}
}

func (c *apiClient) url(method string) string {
	return strings.TrimRight(APIURL, "/") + "/bot" + c.token + "/" + method
}

func (c *apiClient) fileURL(path string) string {
	return strings.TrimRight(APIURL, "/") + "/file/bot" + c.token + "/" + path
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (c *apiClient) call(ctx context.Context, method string, req any, result any) error {
	var body io.Reader
	if req != nil {
		buf, err := json.Marshal(req)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}
	hreq, err := http.NewRequestWithContext(ctx, "POST", c.url(method), body)
	if err != nil {
		return err
	}
	if req != nil {
		hreq.Header.Set("Content-Type", "application/json")
	}
	return c.do(hreq, result)
}

func (c *apiClient) do(hreq *http.Request, result any) error {
	resp, err := httpClient.Do(hreq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var ar apiResponse
	if err := json.Unmarshal(buf, &ar); err != nil {
		return fmt.Errorf("telegram %s: status %d, not json: %s", hreq.URL.Path, resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	if !ar.OK {
		desc := ar.Description
		if desc == "" {
			desc = strings.TrimSpace(string(buf))
		}
		return fmt.Errorf("telegram %s: %s", hreq.URL.Path, desc)
	}
	if result != nil && len(ar.Result) > 0 {
		return json.Unmarshal(ar.Result, result)
	}
	return nil
}

type user struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgMessage struct {
	MessageID      int64       `json:"message_id"`
	From           *user       `json:"from"`
	Chat           chat        `json:"chat"`
	Date           int64       `json:"date"`
	Text           string      `json:"text"`
	Caption        string      `json:"caption"`
	ReplyToMessage *tgMessage  `json:"reply_to_message"`
	Photo          []photoSize `json:"photo"`
	Document       *document   `json:"document"`
	Video          *document   `json:"video"`
	Audio          *document   `json:"audio"`
	Voice          *document   `json:"voice"`
}

type photoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type callbackQuery struct {
	ID      string     `json:"id"`
	From    user       `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *tgMessage     `json:"message"`
	EditedMessage *tgMessage     `json:"edited_message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type inlineKeyboard struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

func (c *apiClient) getMe(ctx context.Context) (user, error) {
	var u user
	err := c.call(ctx, "getMe", nil, &u)
	return u, err
}

func (c *apiClient) getUpdates(ctx context.Context, offset int64, timeoutSec int) ([]update, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.url("getUpdates"), strings.NewReader(fmt.Sprintf(`{"offset":%d,"timeout":%d,"allowed_updates":["message","callback_query"]}`, offset, timeoutSec)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var updates []update
	if err := c.do(req, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (c *apiClient) sendMessage(ctx context.Context, chatID int64, text string, markup *inlineKeyboard, replyTo int64) (tgMessage, error) {
	body := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if markup != nil {
		body["reply_markup"] = markup
	}
	if replyTo > 0 {
		body["reply_parameters"] = map[string]any{"message_id": replyTo}
	}
	var msg tgMessage
	err := c.call(ctx, "sendMessage", body, &msg)
	return msg, err
}

func (c *apiClient) sendDocument(ctx context.Context, chatID int64, filename, contentType string, r io.Reader, caption string, replyTo int64) (tgMessage, error) {
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	_ = mp.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = mp.WriteField("caption", caption)
	}
	if replyTo > 0 {
		_ = mp.WriteField("reply_parameters", fmt.Sprintf(`{"message_id":%d}`, replyTo))
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="document"; filename="%s"`, strings.ReplaceAll(filename, `"`, "")))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h.Set("Content-Type", contentType)
	pw, err := mp.CreatePart(h)
	if err != nil {
		return tgMessage{}, err
	}
	if _, err := io.Copy(pw, r); err != nil {
		return tgMessage{}, err
	}
	if err := mp.Close(); err != nil {
		return tgMessage{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, "POST", c.url("sendDocument"), &buf)
	if err != nil {
		return tgMessage{}, err
	}
	hreq.Header.Set("Content-Type", mp.FormDataContentType())
	var msg tgMessage
	err = c.do(hreq, &msg)
	return msg, err
}

func (c *apiClient) answerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

func (c *apiClient) editReplyMarkup(ctx context.Context, chatID, messageID int64, markup *inlineKeyboard) error {
	body := map[string]any{"chat_id": chatID, "message_id": messageID}
	if markup != nil {
		body["reply_markup"] = markup
	} else {
		body["reply_markup"] = inlineKeyboard{}
	}
	return c.call(ctx, "editMessageReplyMarkup", body, nil)
}

func (c *apiClient) deleteMessage(ctx context.Context, chatID, messageID int64) error {
	return c.call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, nil)
}

func (c *apiClient) setMyCommands(ctx context.Context, cmds []botCommand) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

func (c *apiClient) setWebhook(ctx context.Context, hookURL, secret string) error {
	return c.call(ctx, "setWebhook", map[string]any{
		"url":                  hookURL,
		"secret_token":         secret,
		"drop_pending_updates": false,
		"allowed_updates":      []string{"message", "callback_query"},
	}, nil)
}

func (c *apiClient) deleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

type fileInfo struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

func (c *apiClient) getFile(ctx context.Context, fileID string) (fileInfo, error) {
	var fi fileInfo
	err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &fi)
	return fi, err
}

func (c *apiClient) downloadFile(ctx context.Context, path string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.fileURL(path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download telegram file: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max+1))
}
