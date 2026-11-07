// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"9routerx/proxy/internal/auth"
	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/log"
	"9routerx/proxy/internal/models"
	"9routerx/proxy/internal/observ"
	"9routerx/proxy/internal/securitylog"
)

// keypolicy.go enforces the serenhope per-key governance caps (port(serenhope))
// that are budgets rather than rate windows: the daily cost ceiling first, the
// per-interval request cap after it (added in the same file because both run
// at the same point of the chain). The middleware sits BEFORE
// RequireRateLimit, mirroring upstream's validateApiKey order — a cap check
// runs before any RPM/TPM admission, so an exhausted key never consumes a
// queue slot or a rate-limit stamp on its way to a refusal.

// utcDayKey returns the UTC date a moment belongs to — the same YYYY-MM-DD
// bucket upstream's utcDayKey() (apiKeysRepo.js) stamps into costDay. The day
// boundary is deliberately UTC rather than local: the cap must mean the same
// thing on every host, and the accumulator's lazy reset compares this exact
// string.
func utcDayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// nextUTCMidnight returns the moment the UTC day rolls over after t — the
// earliest moment waiting can help a cost-limited caller again.
func nextUTCMidnight(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// CostReader reads a key's accumulated daily spend. It is an interface rather
// than a *db.Repo so the middleware tests can stand in a fake, the same
// decoupling TokenUsageReader gives the quota check.
type CostReader interface {
	ApiKeyCostToday(keyID, day string) (float64, error)
}

// KeyPolicy carries the dependencies the per-key budget gates share: the cost
// reader for the daily cap, the in-memory request-window tracker for the
// per-interval cap, and the clock the gates judge time with. The clock is a
// field rather than a direct time.Now call so a test can move the day
// boundary without sleeping.
type KeyPolicy struct {
	costs   CostReader
	windows *requestWindowTracker
	now     func() time.Time
}

// NewKeyPolicy builds the policy with the real clock and a fresh window
// tracker. The tracker lives on the policy rather than in package state so
// two instances — the server's and a test's — never share windows.
func NewKeyPolicy(costs CostReader) *KeyPolicy {
	return &KeyPolicy{costs: costs, windows: newRequestWindowTracker(), now: time.Now}
}

// RequireKeyPolicies mounts the per-key budget gates on a route group. A nil
// reader is allowed and simply disables the cost gate — installs that never
// configured a daily cost limit pay nothing.
func RequireKeyPolicies(costs CostReader) func(http.Handler) http.Handler {
	return NewKeyPolicy(costs).Middleware
}

// Middleware runs the gates in order: the IP whitelist first (no database
// read at all), then the endpoint permissions, then the daily cost cap, then
// the per-interval request cap. An unauthenticated request passes through
// untouched — the auth middleware answers it.
func (p *KeyPolicy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := GetAuthenticatedApiKey(r)
		if apiKey == nil {
			next.ServeHTTP(w, r)
			return
		}
		if refuseIPNotWhitelisted(w, r, apiKey) {
			return
		}
		if refuseDisallowedEndpoint(w, r, apiKey) {
			return
		}
		if p.refuseDailyCost(w, r, apiKey) {
			return
		}
		if p.refuseRequestLimit(w, r, apiKey) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// refuseDisallowedEndpoint answers a key whose permissions column does not
// name the endpoint family the request path belongs to (port(serenhope)
// TASK-1.6). Empty permissions allow every family — the column stores a
// restriction list, not a grant list, so keys that predate it keep working.
// A path that maps to no family (headroom, usage streams, OAuth) is never
// permission-gated; the admin surface never passes through key auth at all.
// The response is upstream's exact sentence with the task-specified
// permission_denied code.
//
// A stored value that fails to parse fails OPEN and warns, the same contract
// the IP whitelist keeps: the dashboard refuses malformed values at write
// time, and a typo must not lock every endpoint of the key down at once.
func refuseDisallowedEndpoint(w http.ResponseWriter, r *http.Request, apiKey *models.APIKey) bool {
	if apiKey.Permissions == "" {
		return false
	}
	perms, err := models.ParseKeyPermissions(apiKey.Permissions)
	if err != nil {
		log.Warn("rate limit", "key permissions unparsable; allowing", "key", apiKey.ID, "error", err)
		return false
	}
	family, ok := models.EndpointFamilyForPath(r.URL.Path)
	if !ok {
		return false
	}
	if perms.AllowsEndpoint(family) {
		return false
	}
	clientIP := auth.LoginClientIP(r)
	log.Warn("rate limit", "endpoint family not permitted for key", "key", apiKey.ID,
		"path", r.URL.Path, "family", string(family))
	observ.IncRateLimitReject("permission", apiKey.ID)
	securitylog.Record(securitylog.Entry{
		Type:      "permission_denied",
		Severity:  "warn",
		IP:        clientIP,
		Actor:     apiKey.ID,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    http.StatusForbidden,
		Detail:    fmt.Sprintf("Endpoint family %s not allowed for key", family),
		UserAgent: r.UserAgent(),
	})
	handlerutil.WriteJSONErrorCode(w, http.StatusForbidden, "permission_denied",
		"This API key does not have permission for this endpoint")
	return true
}

// refuseIPNotWhitelisted answers a caller whose address the key's whitelist
// does not name (port(serenhope) TASK-1.3).
//
// The stored list holds IP literals and CIDR ranges; the empty column allows
// every address, which is every key that predates the column. The client
// address comes from the same resolver the dashboard login limiter uses
// (auth.LoginClientIP): the immediate TCP peer unless the operator opted into
// trusting proxies — TRUSTED_PROXIES, TRUST_PROXY or TRUST_CLOUDFLARE — in
// which case X-Forwarded-For is walked right-to-left and only hops leaving a
// trusted peer are honored. With trust off, a spoofed X-Forwarded-For header
// is ignored and the real socket address decides.
//
// A stored value that fails to parse fails OPEN and warns: the dashboard
// refuses malformed entries at write time, so one that got here can only
// come from a restore or a hand edit, and a typo must not brick the key —
// the operator sees the validation error on the next dashboard edit.
func refuseIPNotWhitelisted(w http.ResponseWriter, r *http.Request, apiKey *models.APIKey) bool {
	if apiKey.IPWhitelist == "" {
		return false
	}
	prefixes, err := models.ParseIPWhitelist(apiKey.IPWhitelist)
	if err != nil {
		log.Warn("rate limit", "ip whitelist unparsable; allowing", "key", apiKey.ID, "error", err)
		return false
	}
	if len(prefixes) == 0 {
		return false
	}
	clientIP := auth.LoginClientIP(r)
	addr, err := netip.ParseAddr(clientIP)
	if err != nil {
		// An address that cannot be parsed cannot be proven to belong to the
		// whitelist, so it is refused — the resolver answers "unknown" only
		// when it has no socket address at all, and there is nothing to match.
		clientIPDeny(w, r, apiKey, clientIP)
		return true
	}
	if models.IPAllowed(addr, prefixes) {
		return false
	}
	clientIPDeny(w, r, apiKey, clientIP)
	return true
}

// clientIPDeny writes the shared 403 for a caller outside the key's
// whitelist: the envelope names the address it saw and nothing else — never
// the whitelist itself, which is policy data the client has no claim to.
func clientIPDeny(w http.ResponseWriter, r *http.Request, apiKey *models.APIKey, clientIP string) {
	log.Warn("rate limit", "ip not whitelisted for key", "key", apiKey.ID, "ip", clientIP)
	observ.IncRateLimitReject("ip_whitelist", apiKey.ID)
	securitylog.Record(securitylog.Entry{
		Type:      "guard_denied",
		Severity:  "warn",
		IP:        clientIP,
		Actor:     apiKey.ID,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    http.StatusForbidden,
		Detail:    fmt.Sprintf("IP %s is not whitelisted for this key", clientIP),
		UserAgent: r.UserAgent(),
	})
	handlerutil.WriteJSONErrorCode(w, http.StatusForbidden, "ip_not_allowed",
		fmt.Sprintf("IP %s is not whitelisted for this key", clientIP))
}

// refuseDailyCost answers a key that has spent its USD-per-day ceiling.
//
// The check is read-per-request rather than cached on the key object: the
// accumulator moves after every response, so a snapshot taken at auth time
// would let a burst of concurrent requests overshoot the ceiling by
// everything that lands inside the auth cache TTL.
//
// A read failure degrades to allowing, the same contract budgetCheck keeps
// for its spend reader: upstream reads the row inside the transaction that
// validates the key, where a failure fails the request, but a data-plane-wide
// refusal because one accounting query hiccuped would take the whole gateway
// down with the database. The cap re-arms on the very next successful read.
func (p *KeyPolicy) refuseDailyCost(w http.ResponseWriter, r *http.Request, apiKey *models.APIKey) bool {
	if apiKey.DailyCostLimit <= 0 || p.costs == nil {
		return false
	}
	now := p.now()
	spent, err := p.costs.ApiKeyCostToday(apiKey.ID, utcDayKey(now))
	if err != nil {
		log.Warn("rate limit", "daily cost read failed; allowing", "key", apiKey.ID, "error", err)
		return false
	}
	if spent < apiKey.DailyCostLimit {
		return false
	}

	resetAt := nextUTCMidnight(now)
	secs := int(resetAt.Sub(now).Seconds())
	if secs < 1 {
		secs = 1
	}
	clientIP := auth.LoginClientIP(r)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.Header().Set("X-Cost-Limit", "exceeded")
	w.Header().Set("X-Cost-Limit-Limit", strconv.FormatFloat(apiKey.DailyCostLimit, 'f', -1, 64))
	w.Header().Set("X-Cost-Limit-Spent", strconv.FormatFloat(spent, 'f', -1, 64))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
	log.Warn("rate limit", "daily cost limit exceeded", "key", apiKey.ID,
		"spent", spent, "limit", apiKey.DailyCostLimit)
	observ.IncRateLimitReject("daily_cost", apiKey.ID)
	securitylog.Record(securitylog.Entry{
		Type:      "rate_limit_exceeded",
		Severity:  "warn",
		IP:        clientIP,
		Actor:     apiKey.ID,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    http.StatusTooManyRequests,
		Detail:    fmt.Sprintf("Daily cost limit exceeded ($%v/$%v)", spent, apiKey.DailyCostLimit),
		UserAgent: r.UserAgent(),
	})
	handlerutil.WriteJSONError(w, http.StatusTooManyRequests, "API key daily cost limit exceeded")
	return true
}
