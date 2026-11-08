// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import (
	"net/http"
	"strings"
)

// FreeTierErrorKind is the class an upstream failure falls into from the
// free-tier resilience suite's point of view. Only the free-tier families
// trigger a fallback or a recovery event; everything else — a 400 the client
// built wrong, a 401 on a mistyped key — says nothing about the provider's
// free tier and must stay visible instead of being papered over by a retry
// somewhere else.
type FreeTierErrorKind string

const (
	// KindNone means the failure is not a free-tier signal: no fallback, no
	// recovery event.
	KindNone FreeTierErrorKind = ""
	// KindRateLimit is a throttle: HTTP 429, or a body that names a rate
	// limit without the status surviving proxies intact.
	KindRateLimit FreeTierErrorKind = "rate_limit"
	// KindForbidden is HTTP 403, including the 403 FreeTierError shape the
	// OpenCode-style gates answer with when they are not serving free
	// traffic from this egress.
	KindForbidden FreeTierErrorKind = "forbidden"
	// KindQuota is a spent budget named in the body: quota, FreeTierError,
	// free allowance wording.
	KindQuota FreeTierErrorKind = "quota"
	// KindAbuse is an abuse-detection refusal.
	KindAbuse FreeTierErrorKind = "abuse"
)

// bodyTriggers is the body vocabulary that marks a free-tier refusal. Matched
// case-insensitively against the RAW body (not the extracted message): the
// OpenCode gate's verdict lives in the error `type` token ("FreeTierError"),
// which extractErrorText-style message extraction would drop.
var bodyTriggers = []struct {
	needle string
	kind   FreeTierErrorKind
}{
	// The type token is the stable half of the gate's answer.
	{"freetiererror", KindQuota},
	{"freeusagelimiterror", KindQuota},
	{"quota", KindQuota},
	{"free allowance", KindQuota},
	{"abuse", KindAbuse},
	{"rate limit", KindRateLimit},
	{"rate_limit", KindRateLimit},
	{"ratelimit", KindRateLimit},
	{"too many requests", KindRateLimit},
}

// ClassifyFreeTierError reports which free-tier family an upstream failure
// belongs to.
//
// Status rules first, then the body vocabulary:
//   - 429 is a rate limit (the body may refine it to abuse/quota wording);
//   - 403 is a free-tier refusal (the body may refine it the same way);
//   - every other 4xx is KindNone — a 400/401 is a config or request bug and
//     falling back would only hide it;
//   - 5xx (and status 0, a dial failure) trigger only when the body itself
//     names a free-tier condition, because a bare 500 says nothing about the
//     free tier.
func ClassifyFreeTierError(statusCode int, body []byte) FreeTierErrorKind {
	refined := classifyBody(body)
	switch {
	case statusCode == http.StatusTooManyRequests:
		if refined == KindAbuse {
			return KindAbuse
		}
		if refined == KindQuota {
			return KindQuota
		}
		return KindRateLimit
	case statusCode == http.StatusForbidden:
		if refined == KindAbuse {
			return KindAbuse
		}
		if refined == KindQuota {
			return KindQuota
		}
		return KindForbidden
	case statusCode >= 400 && statusCode < 500:
		// A 4xx that is neither 429 nor 403 is the caller's or the config's
		// problem. Keep it visible: no fallback, no recovery event.
		return KindNone
	default:
		// 5xx and status-less failures: only the body's own words make them a
		// free-tier event.
		return refined
	}
}

// classifyBody matches the body vocabulary. An empty body matches nothing.
func classifyBody(body []byte) FreeTierErrorKind {
	if len(body) == 0 {
		return KindNone
	}
	lower := strings.ToLower(string(body))
	for _, trigger := range bodyTriggers {
		if strings.Contains(lower, trigger.needle) {
			return trigger.kind
		}
	}
	return KindNone
}
