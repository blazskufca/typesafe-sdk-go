package typesafe

import (
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
)

// secretHeaders never reach a log handler with their value intact.
var secretHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"api-key":             true,
	"cookie":              true,
	"set-cookie":          true,
}

// logLevels maps the values accepted by TYPESAFE_LOG_LEVEL.
var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// levelLogger builds the logger a TYPESAFE_LOG_LEVEL value asks for, or a silent
// one when the value is empty, "off", or unrecognized.
func levelLogger(name string) *slog.Logger {
	level, known := logLevels[strings.ToLower(strings.TrimSpace(name))]
	if !known {
		return slog.New(slog.DiscardHandler)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// redacted wraps headers so that credential-bearing values are masked when, and
// only when, a handler actually formats them. It is the single redaction point:
// no call site can leak a secret header.
type redacted http.Header

// LogValue implements [slog.LogValuer].
func (h redacted) LogValue() slog.Value {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	slices.Sort(names)
	attrs := make([]slog.Attr, 0, len(names))
	for _, name := range names {
		value := strings.Join(h[name], ", ")
		if isSecretHeader(name) {
			value = "***"
		}
		attrs = append(attrs, slog.String(name, value))
	}
	return slog.GroupValue(attrs...)
}

// isSecretHeader reports whether a header carries a credential.
func isSecretHeader(name string) bool {
	lowered := strings.ToLower(name)
	return secretHeaders[lowered] || strings.Contains(lowered, "token") || strings.Contains(lowered, "secret")
}
