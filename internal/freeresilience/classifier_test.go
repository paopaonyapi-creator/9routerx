// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import (
	"net/http"
	"testing"
)

func TestClassifyFreeTierError_StatusRules(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   FreeTierErrorKind
	}{
		{"429 is a rate limit", http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, KindRateLimit},
		{"429 with quota body refines to quota", http.StatusTooManyRequests, `{"error":{"message":"quota exceeded for project"}}`, KindQuota},
		{"429 with abuse body refines to abuse", http.StatusTooManyRequests, `{"error":{"message":"abuse detected on this egress"}}`, KindAbuse},
		{"403 is a free-tier refusal", http.StatusForbidden, `{"error":{"message":"not allowed"}}`, KindForbidden},
		{"403 FreeTierError token", http.StatusForbidden, `{"error":{"type":"FreeTierError","message":"free tier unavailable"}}`, KindQuota},
		{"400 stays visible", http.StatusBadRequest, `{"error":{"message":"rate limit check failed: messages malformed"}}`, KindNone},
		{"401 stays visible even with quota words", http.StatusUnauthorized, `{"error":{"message":"quota: invalid api key"}}`, KindNone},
		{"404 stays visible", http.StatusNotFound, `{"error":{"message":"no such model (quota plan)"}}`, KindNone},
		{"500 with rate limit body triggers", http.StatusInternalServerError, `upstream said: rate limit reached`, KindRateLimit},
		{"500 without free-tier words is none", http.StatusInternalServerError, `{"error":{"message":"internal error"}}`, KindNone},
		{"no status with quota body triggers", 0, `{"error":{"type":"FreeUsageLimitError"}}`, KindQuota},
		{"no status, no body is none", 0, ``, KindNone},
		{"abuse wording alone triggers", 503, `we have detected abuse traffic`, KindAbuse},
		{"too many requests wording", 502, `<html>Too Many Requests</html>`, KindRateLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyFreeTierError(tt.status, []byte(tt.body))
			if got != tt.want {
				t.Errorf("ClassifyFreeTierError(%d, %q) = %q, want %q", tt.status, tt.body, got, tt.want)
			}
		})
	}
}

func TestClassifyFreeTierError_EmptyBody(t *testing.T) {
	if got := ClassifyFreeTierError(http.StatusTooManyRequests, nil); got != KindRateLimit {
		t.Errorf("429 with no body = %q, want rate_limit", got)
	}
	if got := ClassifyFreeTierError(http.StatusForbidden, nil); got != KindForbidden {
		t.Errorf("403 with no body = %q, want forbidden", got)
	}
	if got := ClassifyFreeTierError(0, nil); got != KindNone {
		t.Errorf("status 0 with no body = %q, want none", got)
	}
}
