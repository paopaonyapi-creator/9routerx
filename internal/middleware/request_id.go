// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"9routerx/proxy/internal/log"
)

// RequestID returns a middleware that injects a unique request ID (Correlation ID)
// into each request context and response header (X-Request-ID).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := sanitizeRequestID(r.Header.Get("X-Request-ID"))
		if reqID == "" {
			reqID = generateRequestID()
		}

		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), log.RequestIDKey, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// maxClientRequestIDLen bounds an adopted client request ID. A caller-supplied
// value rides in the response header, every structured log line, and the
// security log entries, so an unbounded one is a memory and log-width lever.
const maxClientRequestIDLen = 128

// sanitizeRequestID makes a client-supplied request ID safe to echo into the
// response header and the logs. Control characters are dropped — a newline in
// the value is a forged log line, since the ID is written verbatim into every
// request log entry — and the value is capped.
func sanitizeRequestID(id string) string {
	if id == "" {
		return ""
	}
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, id)
	if runes := []rune(clean); len(runes) > maxClientRequestIDLen {
		clean = string(runes[:maxClientRequestIDLen])
	}
	return clean
}

// GetRequestID retrieves the request ID from an http.Request.
func GetRequestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return GetRequestIDFromContext(r.Context())
}

// GetRequestIDFromContext retrieves the request ID from a context.Context.
func GetRequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val := ctx.Value(log.RequestIDKey)
	if val == nil {
		return ""
	}
	reqID, ok := val.(string)
	if !ok {
		return ""
	}
	return reqID
}

func generateRequestID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}
