// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"9routerx/proxy/internal/log"
)

// FetchTimeout bounds one update-check fetch. The check runs against a
// far-away CDN; a check that cannot finish in five seconds is a failure and
// must be treated as one (the banner degrades to nothing) rather than hanging
// a dashboard request or a scheduler cycle.
const FetchTimeout = 5 * time.Second

// startupCheckDelay separates the first background check from process boot so
// the gateway's listening socket is always up before the check dials out.
const startupCheckDelay = 30 * time.Second

// checkEnabled mirrors the dashboard "Check updates" setting. Default ON: a
// fresh install checks for releases unless the operator turns it off
// (settings key updateCheckEnabled; distinct from autoUpdate, which governs
// the pre-existing self-APPLY flow).
var checkEnabled atomic.Bool

func init() {
	checkEnabled.Store(true)
}

// SetCheckEnabled flips the "Check updates" setting at runtime. The scheduler
// consults it before every cycle; /api/update/check consults it before
// refreshing a stale cache.
func SetCheckEnabled(enabled bool) {
	checkEnabled.Store(enabled)
}

// IsCheckEnabled reports whether update checks may reach the network.
func IsCheckEnabled() bool {
	return checkEnabled.Load()
}

// CheckRecord is one completed update check, kept for observability. The
// updater fails silently by design (a dashboard must not grow an error banner
// because GitHub hiccuped), so this record — surfaced by /api/update/debug —
// is the only place an operator can see WHY a check said what it said.
type CheckRecord struct {
	CheckedAt     string      `json:"checkedAt"`
	DurationMs    int64       `json:"durationMs"`
	Trigger       string      `json:"trigger"` // "startup" | "interval" | "dashboard" | "auto_update_cycle" | "manual"
	Channel       DistChannel `json:"channel"`
	LocalVersion  string      `json:"localVersion"`
	Source        string      `json:"source,omitempty"`
	RemoteVersion string      `json:"remoteVersion,omitempty"`
	Status        string      `json:"status"`   // "ok" | "error"
	Decision      string      `json:"decision"` // "update_available" | "up_to_date" | "rollback" | "error"
	HasUpdate     bool        `json:"hasUpdate"`
	ErrorCause    string      `json:"errorCause,omitempty"` // classified: timeout | dns | http_429 | ...
	Error         string      `json:"error,omitempty"`
}

// CheckOutcome is what a caller of EnsureFreshCheck gets back: the cached (or
// freshly fetched) info plus how it was obtained, so the HTTP layer can tell
// the dashboard "this came from cache" without leaking internals.
type CheckOutcome struct {
	Info *UpdateInfo
	// CacheHit is true when the answer was served without any network fetch.
	CacheHit bool
	// InFlight is true when another goroutine was already fetching and this
	// caller got the stale-but-valid cache instead of a duplicate request.
	InFlight bool
	// CheckEnabled mirrors the setting; a disabled check never fetches.
	CheckEnabled bool
	Channel      DistChannel
	// Stale is true when the served answer is older than the freshness window
	// (a refresh was triggered but this caller did not wait for it, or the
	// refresh failed and the old answer is all there is).
	Stale bool
}

// checkState is the observability side of the check engine. Guarded by its own
// mutex: the cache (cachedInfo) keeps its own, and neither may hold both at
// once or a debug dump during a fetch could deadlock.
type checkState struct {
	mu         sync.Mutex
	last       *CheckRecord
	rawSnippet string
	fetching   atomic.Bool
}

// rawMaxBytes bounds the raw-response snippet kept for /api/update/debug. A
// manifest is a few hundred bytes; a GitHub release payload can be tens of KB.
const rawMaxBytes = 4096

var checkObs checkState

// recordRawResponse keeps a bounded prefix of the last raw remote body for the
// debug endpoint. Called from the parsers with whatever the channel answered —
// including a body that then failed to parse, which is exactly the case the
// dump exists for.
func recordRawResponse(body []byte) {
	snippet := body
	if len(snippet) > rawMaxBytes {
		snippet = snippet[:rawMaxBytes]
	}
	checkObs.mu.Lock()
	checkObs.rawSnippet = string(snippet)
	checkObs.mu.Unlock()
}

// errorCause classifies a check failure for the structured log and the debug
// dump. The spec forbids empty catches: every failure path names its cause.
func errorCause(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if errors.Is(urlErr.Err, context.DeadlineExceeded) {
			return "timeout"
		}
		return "network"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "HTTP 429"), strings.Contains(msg, "status 429"):
		return "rate_limited"
	case strings.Contains(msg, "HTTP 404"), strings.Contains(msg, "status 404"):
		return "not_found"
	case strings.Contains(msg, "HTTP "), strings.Contains(msg, "status "):
		return "http_status"
	// encoding/json/v2 reports syntax faults as jsontext.SyntacticError with
	// "unexpected EOF"/"invalid character" wording; unmarshal mismatches say
	// "cannot unmarshal".
	case strings.Contains(msg, "jsontext"), strings.Contains(msg, "invalid character"),
		strings.Contains(msg, "unexpected end of JSON"), strings.Contains(msg, "cannot unmarshal"):
		return "parse"
	default:
		return "unknown"
	}
}

// debugOut is the verbose mirror used when UPDATE_DEBUG=1. A variable so the
// log-capture test can read what was written.
var debugOut io.Writer = os.Stdout

// updateDebug reports whether verbose stdout mirroring is on. A function over
// the env so a test can flip it with t.Setenv.
func updateDebug() bool {
	return os.Getenv("UPDATE_DEBUG") == "1"
}

// debugStep emits one structured check step. The structured line always goes
// to the logger (debug level — the check must stay silent in normal logs);
// with UPDATE_DEBUG=1 the same line is mirrored verbosely to stdout.
func debugStep(msg string, kv ...any) {
	log.Debug("updater", msg, kv...)
	if updateDebug() {
		var b strings.Builder
		fmt.Fprintf(&b, "[updater:debug] %s", msg)
		for i := 0; i+1 < len(kv); i += 2 {
			fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
		}
		fmt.Fprintln(debugOut, b.String())
	}
}

// instrumentedCheck is the ONE instrumented path to CheckUpdate. Every caller
// — dashboard request, background ticker, auto-update cycle, CLI — goes
// through here so the log line set (start, fetch result, compare decision,
// error with cause) is identical no matter what triggered the check.
func instrumentedCheck(ctx context.Context, trigger string) (*UpdateInfo, error) {
	channel, _ := DetectDistChannel()
	start := time.Now()
	debugStep("update check start", "trigger", trigger, "channel", channel, "localVersion", CurrentVersion)

	checkCtx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	info, err := CheckUpdate(checkCtx)
	duration := time.Since(start)

	rec := CheckRecord{
		CheckedAt:    start.UTC().Format(time.RFC3339),
		DurationMs:   duration.Milliseconds(),
		Trigger:      trigger,
		Channel:      channel,
		LocalVersion: CurrentVersion,
	}
	if err != nil {
		rec.Status = "error"
		rec.Decision = "error"
		rec.ErrorCause = errorCause(err)
		rec.Error = err.Error()
		debugStep("update check failed", "trigger", trigger, "cause", rec.ErrorCause, "duration", duration.String(), "error", err)
	} else {
		rec.Status = "ok"
		rec.Source = info.Source
		rec.RemoteVersion = info.LatestVersion
		rec.HasUpdate = info.HasUpdate
		switch {
		case info.HasUpdate:
			rec.Decision = "update_available"
		case CompareVersions(info.LatestVersion, strings.TrimPrefix(CurrentVersion, "v")) < 0:
			// The remote channel is OLDER than this build: a rollback, or a
			// dev build ahead of the published releases. Never a banner.
			rec.Decision = "rollback"
		default:
			rec.Decision = "up_to_date"
		}
		debugStep("update check decision", "trigger", trigger, "source", info.Source,
			"remoteVersion", info.LatestVersion, "localVersion", CurrentVersion,
			"decision", rec.Decision, "duration", duration.String())
	}

	checkObs.mu.Lock()
	checkObs.last = &rec
	checkObs.mu.Unlock()
	return info, err
}

// EnsureFreshCheck answers "is there an update?" from the in-memory cache and
// only dials the channel when the cache is empty or older than the freshness
// window. This is what GET /api/update/check calls: the endpoint never fetches
// per request, and concurrent dashboard opens are collapsed into one in-flight
// fetch by the atomic flag.
func EnsureFreshCheck(ctx context.Context, trigger string) CheckOutcome {
	channel, _ := DetectDistChannel()
	out := CheckOutcome{
		CheckEnabled: IsCheckEnabled(),
		Channel:      channel,
		Info:         GetCachedInfo(),
	}
	_, age, has := lastCheckAge()
	out.Stale = has && age > checkFreshnessWindow()
	if has && age <= checkFreshnessWindow() {
		out.CacheHit = true
		debugStep("update check cache hit", "age", age.Round(time.Second).String())
		return out
	}
	if !out.CheckEnabled {
		debugStep("update check skipped: setting off", "trigger", trigger)
		return out
	}
	// Single-flight: one fetch at a time process-wide. A second caller during
	// a fetch leaves with the current cache rather than stampeding the channel.
	if !checkObs.fetching.CompareAndSwap(false, true) {
		out.InFlight = true
		out.CacheHit = true
		debugStep("update check collapsed: fetch already in flight", "trigger", trigger)
		return out
	}
	defer checkObs.fetching.Store(false)
	previous := out.Info
	_, _, wasChecked := lastCheckAge()
	info, err := instrumentedCheck(ctx, trigger)
	out.CacheHit = false
	out.Stale = false
	if err != nil {
		// Silent failure by design: the caller keeps whatever the cache held
		// before (the engine only rewrites it on success), and the cause went
		// to the structured log and the debug record.
		out.Info = previous
		out.Stale = wasChecked
	} else {
		out.Info = info
	}
	return out
}

// lastCheckAge reports how long ago the last successful cache write happened.
func lastCheckAge() (time.Time, time.Duration, bool) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if lastCheckTime.IsZero() {
		return time.Time{}, 0, false
	}
	return lastCheckTime, time.Since(lastCheckTime), true
}

// checkFreshnessWindow is how long a cached answer stays fresh. It follows the
// configured check interval: checking every 6h means an answer is worth 6h.
// With the ticker off ("0") the window stays at the default so a dashboard
// open still refreshes a day-old cache.
func checkFreshnessWindow() time.Duration {
	interval, enabled := ParseCheckInterval(os.Getenv("UPDATE_CHECK_INTERVAL"))
	if enabled && interval > DefaultCheckInterval {
		return interval
	}
	return DefaultCheckInterval
}

// ParseCheckInterval reads UPDATE_CHECK_INTERVAL: a Go duration string ("6h",
// "30m") or a bare number, which is treated as hours. "0" disables the
// background ticker (the dashboard-open check still refreshes stale caches).
// Unparsable values fall back to the 6h default with the ticker on — a typo
// must not silently turn update checking off.
func ParseCheckInterval(env string) (time.Duration, bool) {
	env = strings.TrimSpace(env)
	if env == "" {
		return DefaultCheckInterval, true
	}
	if n, err := strconv.Atoi(env); err == nil {
		if n <= 0 {
			return 0, false
		}
		return time.Duration(n) * time.Hour, true
	}
	if d, err := time.ParseDuration(env); err == nil {
		if d <= 0 {
			return 0, false
		}
		return d, true
	}
	return DefaultCheckInterval, true
}

// StartCheckLoop starts the background check ticker: exactly one goroutine for
// the process lifetime, cancelled with ctx. UPDATE_CHECK_INTERVAL=0 (or a
// negative value) starts nothing. Per-cycle work consults the "Check updates"
// setting, so a disabled check costs one atomic load, not a goroutine.
func StartCheckLoop(ctx context.Context) {
	interval, enabled := ParseCheckInterval(os.Getenv("UPDATE_CHECK_INTERVAL"))
	if !enabled {
		log.Info("updater", "background update check disabled (UPDATE_CHECK_INTERVAL=0)")
		return
	}
	log.Info("updater", "background update check scheduled", "interval", interval.String())

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(startupCheckDelay):
		}
		if IsCheckEnabled() {
			_, _ = instrumentedCheck(ctx, "startup")
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !IsCheckEnabled() {
					continue
				}
				// A check failure is silent: instrumentedCheck already logged
				// the cause. The return value feeds nothing here — the banner
				// reads the cache.
				_, _ = instrumentedCheck(ctx, "interval")
			}
		}
	}()
}

// DebugDump is the payload of GET /api/update/debug. Everything the engine
// knows about its own last check, in one struct — the updater fails silently,
// so this dump is how an operator answers "why does the banner not show?".
type DebugDump struct {
	CurrentVersion string       `json:"currentVersion"`
	Channel        DistChannel  `json:"channel"`
	ChannelReason  string       `json:"channelReason"`
	CheckEnabled   bool         `json:"checkEnabled"`
	Interval       string       `json:"checkInterval"`
	Freshness      string       `json:"freshnessWindow"`
	ManifestURL    string       `json:"manifestUrl"`
	ReleasesAPI    string       `json:"releasesApiUrl"`
	LastCheck      *CheckRecord `json:"lastCheck,omitempty"`
	CachedInfo     *UpdateInfo  `json:"cachedInfo,omitempty"`
	RawResponse    string       `json:"rawResponse,omitempty"`
	FetchInFlight  bool         `json:"fetchInFlight"`
	Note           string       `json:"note,omitempty"`
}

// DebugDump assembles the engine state. Never fetches.
func BuildDebugDump() DebugDump {
	channel, why := DetectDistChannel()
	checkObs.mu.Lock()
	last := checkObs.last
	raw := checkObs.rawSnippet
	inFlight := checkObs.fetching.Load()
	checkObs.mu.Unlock()

	interval, enabled := ParseCheckInterval(os.Getenv("UPDATE_CHECK_INTERVAL"))
	dump := DebugDump{
		CurrentVersion: CurrentVersion,
		Channel:        channel,
		ChannelReason:  why,
		CheckEnabled:   IsCheckEnabled(),
		ManifestURL:    manifestURL(),
		ReleasesAPI:    fmt.Sprintf(githubReleasesAPI, repoName()),
		LastCheck:      last,
		CachedInfo:     GetCachedInfo(),
		RawResponse:    raw,
		FetchInFlight:  inFlight,
	}
	if !enabled {
		dump.Interval = "off (UPDATE_CHECK_INTERVAL=0)"
		dump.Note = "background ticker disabled; dashboard-open checks still refresh caches older than the default window"
	} else {
		dump.Interval = interval.String()
	}
	dump.Freshness = checkFreshnessWindow().String()
	return dump
}
