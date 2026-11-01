package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// sendTimeout bounds one attempt of one channel.
	sendTimeout = 20 * time.Second
	// maxAnswer is the longest answer to a failed request quoted in its error.
	maxAnswer = 4096
)

// sendChannel sends to one channel. Its error goes to the page and the log,
// so the channel's secrets are taken out of it, wherever they came from: a
// server can echo the request's address or a token back in its answer.
func sendChannel(ctx context.Context, ch Channel, r Rendered) error {
	return redact(ch, sendRaw(ctx, ch, r))
}

func sendRaw(ctx context.Context, ch Channel, r Rendered) error {
	switch ch.Type {
	case "telegram":
		return sendTelegram(ctx, ch, r)
	case "email":
		return sendEmail(ctx, ch, r)
	case "webhook":
		return sendWebhook(ctx, ch, r)
	case "slack":
		return postJSON(ctx, ch, ch.get("url"), map[string]string{"text": r.Title + "\n" + r.Text()}, nil)
	case "teams":
		return sendTeams(ctx, ch, r)
	case "googlechat":
		return postJSON(ctx, ch, ch.get("url"), map[string]string{"text": "*" + r.Title + "*\n" + r.Text()}, nil)
	case "discord":
		return sendDiscord(ctx, ch, r)
	case "ntfy":
		return sendNtfy(ctx, ch, r)
	case "gotify":
		return sendGotify(ctx, ch, r)
	case "pushover":
		return sendPushover(ctx, ch, r)
	case "matrix":
		return sendMatrix(ctx, ch, r)
	case "syslog":
		return sendSyslog(ctx, ch, r)
	}
	return permanent(fmt.Errorf("unknown channel type %q", ch.Type))
}

// redact replaces the channel's secrets in err's text with "***" and then
// shortens it, keeping whether it is permanent. The text is all that is kept
// of err. Shortening last cannot leave the start of a secret.
func redact(ch Channel, err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	for _, secret := range secretForms(ch) {
		text = strings.ReplaceAll(text, secret, "***")
	}
	clean := errors.New(truncate(text, 400))
	var auth authError
	if errors.As(err, &auth) {
		clean = authError{clean}
	}
	var perm permanentError
	if errors.As(err, &perm) {
		return permanent(clean)
	}
	return clean
}

// secretForms lists the channel's secret values as they may appear in an
// answer, longest first: as stored, escaped for a path or a query, and for
// a secret address its path, query and their longer parts too.
func secretForms(ch Channel) []string {
	t, _ := channelType(ch.Type)
	seen := map[string]bool{}
	var forms []string
	add := func(s string) {
		if s != "" && s != "/" && !seen[s] {
			seen[s] = true
			forms = append(forms, s)
		}
	}
	for _, f := range t.Fields {
		value := ch.get(f.Key)
		if !f.Secret || value == "" {
			continue
		}
		add(value)
		add(url.PathEscape(value))
		add(url.QueryEscape(value))
		if f.Kind != "url" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil {
			continue
		}
		add(u.RequestURI())
		add(u.EscapedPath())
		add(u.Path)
		add(u.RawQuery)
		for _, part := range strings.Split(u.Path, "/") {
			if len(part) >= 8 {
				add(part)
			}
		}
		for _, values := range u.Query() {
			for _, v := range values {
				if len(v) >= 8 {
					add(v)
					add(url.QueryEscape(v))
				}
			}
		}
	}
	slices.SortFunc(forms, func(a, b string) int { return len(b) - len(a) })
	return forms
}

func tlsConfig(ch Channel) *tls.Config {
	// The user's choice, for an internal server with a certificate of its own.
	return &tls.Config{InsecureSkipVerify: ch.flag("skipVerify")} //nolint:gosec
}

func httpClient(ch Channel) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig(ch)
	return &http.Client{Transport: transport, Timeout: sendTimeout}
}

// do sends a request and turns the answer into an error. The request's URL
// is left out of errors: webhook URLs and Telegram's carry the secret.
func do(ch Channel, req *http.Request) error {
	resp, err := httpClient(ch).Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if len(body) > maxAnswer {
		// A cut could split a secret that sendChannel then misses; an answer
		// this long is an error page, of no use in a message anyway.
		body = nil
	}
	// The answer helps tell a wrong chat id from a wrong token; sendChannel
	// takes the secrets out of it.
	err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusRequestTimeout {
		return permanent(err)
	}
	return err
}

func request(ctx context.Context, method, target, contentType string, body []byte, header map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, permanent(errors.New("invalid address"))
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "NanoKVM")
	for key, value := range header {
		req.Header.Set(key, value)
	}
	return req, nil
}

func postJSON(ctx context.Context, ch Channel, target string, payload any, header map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return permanent(err)
	}
	req, err := request(ctx, http.MethodPost, target, "application/json", body, header)
	if err != nil {
		return err
	}
	return do(ch, req)
}

// form is a multipart body: fields, then at most one file.
type form struct {
	fields    [][2]string
	fileField string
	fileName  string
	file      []byte
}

func (f form) post(ctx context.Context, ch Channel, target string) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, field := range f.fields {
		if err := w.WriteField(field[0], field[1]); err != nil {
			return permanent(err)
		}
	}
	if f.file != nil {
		part, err := w.CreateFormFile(f.fileField, f.fileName)
		if err != nil {
			return permanent(err)
		}
		if _, err = part.Write(f.file); err != nil {
			return permanent(err)
		}
	}
	if err := w.Close(); err != nil {
		return permanent(err)
	}
	req, err := request(ctx, http.MethodPost, target, w.FormDataContentType(), body.Bytes(), nil)
	if err != nil {
		return err
	}
	return do(ch, req)
}

// truncate cuts text to at most n characters.
func truncate(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)
	return string(runes[:n-1]) + "…"
}

func sendTelegram(ctx context.Context, ch Channel, r Rendered) error {
	api := strings.TrimRight(ch.get("apiUrl"), "/")
	if api == "" {
		api = "https://api.telegram.org"
	}
	base := api + "/bot" + ch.get("token") + "/"
	text := r.Title + "\n" + r.Text()
	thread := ch.get("threadId")
	if r.Image != nil {
		// A caption holds 1024 characters: a longer text follows the picture.
		caption := text
		if utf8.RuneCountInString(text) > 1024 {
			caption = r.Title
		}
		f := form{fields: [][2]string{{"chat_id", ch.get("chatId")}, {"caption", caption}},
			fileField: "photo", fileName: "screen.jpg", file: r.Image}
		if thread != "" {
			f.fields = append(f.fields, [2]string{"message_thread_id", thread})
		}
		if err := f.post(ctx, ch, base+"sendPhoto"); err != nil || caption == text {
			return err
		}
	}
	payload := map[string]any{
		"chat_id":                  ch.get("chatId"),
		"text":                     truncate(text, 4096),
		"disable_web_page_preview": true,
	}
	if thread != "" {
		id, _ := strconv.ParseInt(thread, 10, 64)
		payload["message_thread_id"] = id
	}
	return postJSON(ctx, ch, base+"sendMessage", payload, nil)
}

// webhookPayload is the body of a generic webhook; the image, when there is
// one, is a base64 JPEG.
type webhookPayload struct {
	Event    Kind   `json:"event"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	Device   string `json:"device"`
	Time     string `json:"time"`
	Image    string `json:"image,omitempty"`
}

// sendWebhook posts the event as JSON. With a secret, the header
// X-NanoKVM-Signature carries "sha256=" and the hex HMAC-SHA256 of the body.
func sendWebhook(ctx context.Context, ch Channel, r Rendered) error {
	payload := webhookPayload{
		Event:    r.Kind,
		Severity: r.Severity.String(),
		Title:    r.Title,
		Text:     r.Body,
		Device:   r.Device,
		Time:     r.Time.UTC().Format(time.RFC3339),
	}
	if r.Image != nil {
		payload.Image = base64.StdEncoding.EncodeToString(r.Image)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return permanent(err)
	}
	header := map[string]string{"X-NanoKVM-Event": string(r.Kind)}
	if secret := ch.get("hmacSecret"); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		header["X-NanoKVM-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	if auth := ch.get("authorization"); auth != "" {
		header["Authorization"] = auth
	}
	req, err := request(ctx, http.MethodPost, ch.get("url"), "application/json", body, header)
	if err != nil {
		return err
	}
	return do(ch, req)
}

// sendTeams posts an Adaptive Card to a Teams Workflows webhook ("When a
// Teams webhook request is received"), which replaced the retired Office 365
// connectors.
func sendTeams(ctx context.Context, ch Channel, r Rendered) error {
	title := map[string]any{"type": "TextBlock", "text": r.Title, "weight": "Bolder", "size": "Medium", "wrap": true}
	if r.Severity != SeverityInfo {
		title["color"] = "Attention"
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body": []any{
			title,
			map[string]any{"type": "TextBlock", "text": r.Body, "wrap": true},
			map[string]any{"type": "TextBlock", "text": r.Device + " · " + r.Time.Format("2006-01-02 15:04:05 MST"),
				"isSubtle": true, "size": "Small", "wrap": true},
		},
	}
	payload := map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content":     card,
		}},
	}
	return postJSON(ctx, ch, ch.get("url"), payload, nil)
}

func sendDiscord(ctx context.Context, ch Channel, r Rendered) error {
	content := truncate("**"+r.Title+"**\n"+r.Text(), 2000)
	if r.Image == nil {
		return postJSON(ctx, ch, ch.get("url"), map[string]string{"content": content}, nil)
	}
	payload, _ := json.Marshal(map[string]string{"content": content})
	f := form{fields: [][2]string{{"payload_json", string(payload)}},
		fileField: "files[0]", fileName: "screen.jpg", file: r.Image}
	return f.post(ctx, ch, ch.get("url"))
}

func sendNtfy(ctx context.Context, ch Channel, r Rendered) error {
	priority := map[Severity]int{SeverityInfo: 3, SeverityWarning: 4, SeverityCritical: 5}[r.Severity]
	payload := map[string]any{
		"topic":    ch.get("topic"),
		"title":    r.Title,
		"message":  r.Text(),
		"priority": priority,
		"tags":     []string{"nanokvm", string(r.Kind)},
	}
	var header map[string]string
	if token := ch.get("token"); token != "" {
		header = map[string]string{"Authorization": "Bearer " + token}
	}
	return postJSON(ctx, ch, strings.TrimRight(ch.get("server"), "/")+"/", payload, header)
}

func sendGotify(ctx context.Context, ch Channel, r Rendered) error {
	priority := map[Severity]int{SeverityInfo: 2, SeverityWarning: 5, SeverityCritical: 8}[r.Severity]
	payload := map[string]any{"title": r.Title, "message": r.Text(), "priority": priority}
	return postJSON(ctx, ch, strings.TrimRight(ch.get("server"), "/")+"/message", payload,
		map[string]string{"X-Gotify-Key": ch.get("token")})
}

func sendPushover(ctx context.Context, ch Channel, r Rendered) error {
	priority := "0"
	if r.Severity == SeverityCritical {
		priority = "1"
	}
	f := form{fields: [][2]string{
		{"token", ch.get("appToken")},
		{"user", ch.get("userKey")},
		{"title", truncate(r.Title, 250)},
		{"message", truncate(r.Text(), 1024)},
		{"priority", priority},
	}}
	if device := ch.get("device"); device != "" {
		f.fields = append(f.fields, [2]string{"device", device})
	}
	if r.Image != nil {
		f.fileField, f.fileName, f.file = "attachment", "screen.jpg", r.Image
	}
	return f.post(ctx, ch, "https://api.pushover.net/1/messages.json")
}

func sendMatrix(ctx context.Context, ch Channel, r Rendered) error {
	target := strings.TrimRight(ch.get("homeserver"), "/") + "/_matrix/client/v3/rooms/" +
		url.PathEscape(ch.get("roomId")) + "/send/m.room.message/nanokvm-" + newID()
	body, err := json.Marshal(map[string]string{"msgtype": "m.text", "body": r.Title + "\n" + r.Text()})
	if err != nil {
		return permanent(err)
	}
	req, err := request(ctx, http.MethodPut, target, "application/json", body,
		map[string]string{"Authorization": "Bearer " + ch.get("accessToken")})
	if err != nil {
		return err
	}
	return do(ch, req)
}
