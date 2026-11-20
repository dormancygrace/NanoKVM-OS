package middleware

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/journal"
)

// Requests that change something but are no change of a setting: input to
// the host, previews, checks and tests, signing in and out, file transfers,
// and actions the journal records with their own words (buttons, the virtual
// drive's medium). Route patterns (gin's FullPath); a pattern ending in "*"
// covers what starts with it.
var journalSkipped = []string{
	"/api/auth/login", "/api/auth/login/totp", "/api/auth/logout",
	"/api/auth/activity",
	"/api/hid/paste", "/api/hid/sequence", "/api/hid/reset",
	"/api/vm/gpio", "/api/atx*", "/api/network/wol",
	"/api/storage/image/mount", "/api/storage/remote/*",
	"/api/storage/user-disk/files/*", "/api/storage/image/delete",
	"/api/download/*",
	"/api/stream/state", "/api/stream/mjpeg/detect/stop",
	"/api/network/wifi/verify",
	"/api/vm/serial-console/test", "/api/vm/schedule/preview", "/api/vm/schedule/run",
	"/api/vm/script/run", "/api/vm/video/retry", "/api/vm/tls/reload",
	"/api/vm/oled/preview",
	"*/preview", "*/check", "*/test", "*/oauth/*", "*/verify",
	"/api/os/update/notice/check",
	"/api/system/recovery/export",
	"/api/picoclaw/actions", "/api/picoclaw/mcp", "/api/picoclaw/load-image",
	"/api/picoclaw/sessions/*", "/api/picoclaw/runtime/session",
}

// Requests that do something to NanoKVM itself rather than change a setting.
// Shutdown is recorded at its commit point in standbyRouter, registered before
// this middleware; prepare/cancel are not shutdowns.
var journalSystemActions = map[string]string{
	"/api/vm/system/reboot": "reboot",
	"/api/vm/hdmi/reset":    "HDMI reset",
}

func journalSkips(route string) bool {
	for _, pattern := range journalSkipped {
		lead, trail := strings.HasPrefix(pattern, "*"), strings.HasSuffix(pattern, "*")
		core := strings.Trim(pattern, "*")
		switch {
		case lead && trail:
			if strings.Contains(route, core) {
				return true
			}
		case lead:
			if strings.HasSuffix(route, core) {
				return true
			}
		case trail:
			if strings.HasPrefix(route, core) {
				return true
			}
		case route == pattern:
			return true
		}
	}
	return false
}

// failed reports a JSON answer with a code other than 0 at its start.
func failed(head []byte) bool {
	if len(head) == 0 || head[0] != '{' {
		return false
	}
	i := bytes.Index(head, []byte(`"code":`))
	if i < 0 {
		return false
	}
	rest := bytes.TrimLeft(head[i+len(`"code":`):], " ")
	return len(rest) > 0 && rest[0] != '0'
}

// settingName is a route in words: "/api/vm/video/watermark" is
// "video › watermark", ":parameters" by their values.
func settingName(c *gin.Context, route string) string {
	parts := strings.Split(strings.TrimPrefix(route, "/api/"), "/")
	words := make([]string, 0, len(parts))
	for i, part := range parts {
		if part == "" || (i == 0 && part == "vm" && len(parts) > 1) {
			continue
		}
		if strings.HasPrefix(part, ":") {
			part = c.Param(part[1:])
		}
		words = append(words, strings.ReplaceAll(part, "-", " "))
	}
	return strings.Join(words, " › ")
}

// journalWriter keeps the start of the body, where the response's code is.
type journalWriter struct {
	gin.ResponseWriter
	head bytes.Buffer
}

func (w *journalWriter) Write(b []byte) (int, error) {
	if room := 256 - w.head.Len(); room > 0 {
		w.head.Write(b[:min(room, len(b))])
	}
	return w.ResponseWriter.Write(b)
}

func (w *journalWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// JournalChanges records in the event journal every successful request of a
// signed-in user that changes something: a setting ("settings.changed") or an
// action on NanoKVM itself ("system.action"), with who did it.
func JournalChanges() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		default:
			c.Next()
			return
		}
		writer := &journalWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		c.Writer = writer.ResponseWriter
		route := c.FullPath()
		// Only the action slot selects a preview/check operation. Other route
		// parameters (for example a user named "test") stay literal here.
		operation := strings.ReplaceAll(route, ":action", c.Param("action"))
		if route == "" || !strings.HasPrefix(route, "/api/") || journalSkips(operation) || c.Writer.Status() != http.StatusOK {
			return
		}
		principal, ok := CurrentPrincipal(c)
		if !ok {
			return
		}
		// Handlers answer {"code": 0, ...} on success; a body that is not
		// such an answer (a file, a stream) counts by its status alone.
		if failed(writer.head.Bytes()) {
			return
		}
		ctx := journal.WithActor(c.Request.Context(), principal.Username)
		if action, ok := journalSystemActions[route]; ok {
			journal.Record(ctx, "system", "system.action", map[string]string{"action": action})
			return
		}
		setting := settingName(c, route)
		if c.Request.Method == http.MethodDelete {
			setting += " (removed)"
		}
		journal.Record(ctx, "settings", "settings.changed", map[string]string{"setting": setting})
	}
}
