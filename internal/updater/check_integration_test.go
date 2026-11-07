// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// resetCheckState isolates one test from the package-global channel and cache.
// The updater deliberately keeps its state in package globals (it is a
// process-wide singleton), so every mock-server test swaps the globals the same
// way fork_channel_test.go does and cleans up after itself.
func resetCheckState(t *testing.T) {
	t.Helper()
	oldVersion, oldURL, oldRepo, oldAPI := CurrentVersion, DefaultUpdateURL, DefaultGitHubRepo, githubReleasesAPI
	t.Cleanup(func() {
		CurrentVersion, DefaultUpdateURL, DefaultGitHubRepo, githubReleasesAPI = oldVersion, oldURL, oldRepo, oldAPI
	})
	cacheMu.Lock()
	cachedInfo = nil
	lastCheckTime = time.Time{}
	cacheMu.Unlock()
	checkObs.mu.Lock()
	checkObs.last = nil
	checkObs.rawSnippet = ""
	checkObs.mu.Unlock()
	checkObs.fetching.Store(false)
	SetCheckEnabled(true)
	t.Cleanup(func() { SetCheckEnabled(true) })
	t.Setenv("UPDATE_URL", "")
	t.Setenv("UPDATE_REPO", "")
	t.Setenv("UPDATE_CHECK_INTERVAL", "")
	t.Setenv("UPDATE_DEBUG", "")
	t.Setenv("AUTO_UPDATE", "")
}

// manifestServer builds an httptest server answering /version.json with the
// given status/body, counting every request it serves.
func manifestServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const mergedManifest = `{
  "version": "1.9.17",
  "minVersion": "1.9.0",
  "releaseUrl": "https://github.com/yanstd888/9RouterX/releases/tag/v1.9.17",
  "notes": "merged contract release",
  "publishedAt": "2026-11-01T00:00:00Z",
  "latestVersion": "1.9.17",
  "downloadUrl": "https://example.invalid/asset",
  "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}`

// TestCheckUpdate_MergedManifestFields pins the merged version.json contract:
// the new keys (version/minVersion/releaseUrl/notes/publishedAt) parse into
// UpdateInfo alongside the legacy keys.
func TestCheckUpdate_MergedManifestFields(t *testing.T) {
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	srv, _ := manifestServer(t, http.StatusOK, mergedManifest)
	DefaultUpdateURL, DefaultGitHubRepo = srv.URL+"/version.json", ""

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestVersion != "1.9.17" || !info.HasUpdate {
		t.Errorf("LatestVersion/HasUpdate = %q/%v", info.LatestVersion, info.HasUpdate)
	}
	if info.MinVersion != "1.9.0" {
		t.Errorf("MinVersion = %q, want 1.9.0", info.MinVersion)
	}
	if info.ReleaseURL != "https://github.com/yanstd888/9RouterX/releases/tag/v1.9.17" {
		t.Errorf("ReleaseURL = %q", info.ReleaseURL)
	}
	if info.PublishedAt != "2026-11-01T00:00:00Z" {
		t.Errorf("PublishedAt = %q", info.PublishedAt)
	}
	if info.ReleaseNotes != "merged contract release" {
		t.Errorf("ReleaseNotes from legacy key = %q", info.ReleaseNotes)
	}
	if info.SHA256 == "" {
		t.Error("sha256 lost")
	}
}

// TestCheckUpdate_DecisionMatrix drives the mock manifest through the
// higher/same/lower versions and asserts both the UpdateInfo verdict and the
// recorded decision. A rollback (remote older than local) must NEVER surface
// as an update — the banner reads HasUpdate, so this is the guard behind the
// "rollback shows no banner" requirement.
func TestCheckUpdate_DecisionMatrix(t *testing.T) {
	tests := []struct {
		name          string
		local         string
		remoteVersion string
		wantHasUpdate bool
		wantDecision  string
	}{
		{"higher version", "1.9.16", "1.9.17", true, "update_available"},
		{"same version", "1.9.17", "1.9.17", false, "up_to_date"},
		{"lower version (rollback)", "1.9.18", "1.9.17", false, "rollback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCheckState(t)
			CurrentVersion = tt.local
			body := fmt.Sprintf(`{"latestVersion":%q,"downloadUrl":"https://x/y","sha256":%q}`,
				tt.remoteVersion, strings.Repeat("a", 64))
			srv, _ := manifestServer(t, http.StatusOK, body)
			DefaultUpdateURL, DefaultGitHubRepo = srv.URL+"/version.json", ""

			info, err := instrumentedCheck(context.Background(), "test")
			if err != nil {
				t.Fatal(err)
			}
			if info.HasUpdate != tt.wantHasUpdate {
				t.Errorf("HasUpdate = %v, want %v", info.HasUpdate, tt.wantHasUpdate)
			}
			rec := lastRecord(t)
			if rec.Decision != tt.wantDecision {
				t.Errorf("decision = %q, want %q (record %+v)", rec.Decision, tt.wantDecision, rec)
			}
			if rec.Status != "ok" || rec.Trigger != "test" || rec.Channel == "" || rec.LocalVersion != tt.local {
				t.Errorf("record incomplete: %+v", rec)
			}
		})
	}
}

// TestCheckUpdate_FailureMatrix drives the graceful-degradation cases: 404,
// timeout, malformed JSON and rate limiting must all come back as a normal
// error (cause classified, no panic, no partial cache write), never as a
// banner-worthy answer.
func TestCheckUpdate_FailureMatrix(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantCause string
	}{
		{"404 not found", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, "not_found"},
		{"rate limited 429", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		}, "rate_limited"},
		{"malformed json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"latestVersion": `))
		}, "parse"},
		{"slow server times out", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(FetchTimeout + 2*time.Second)
			_, _ = w.Write([]byte(`{"latestVersion":"9.9.9"}`))
		}, "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if testing.Short() {
				t.Skip("slow (real 5s fetch timeout)")
			}
			resetCheckState(t)
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				tt.handler(w, r)
			}))
			defer srv.Close()
			DefaultUpdateURL, DefaultGitHubRepo = srv.URL+"/version.json", ""

			info, err := instrumentedCheck(context.Background(), "test")
			if err == nil || info != nil {
				t.Fatalf("expected graceful error, got info=%v err=%v", info, err)
			}
			rec := lastRecord(t)
			if rec.Status != "error" || rec.Decision != "error" {
				t.Errorf("record = %+v, want status/decision error", rec)
			}
			if rec.ErrorCause != tt.wantCause {
				t.Errorf("errorCause = %q, want %q", rec.ErrorCause, tt.wantCause)
			}
			if hits.Load() == 0 {
				t.Error("mock server was never contacted")
			}
			// The failure must not have written a cache entry.
			if HasCheckResult() {
				t.Error("a failed check must not populate the cache")
			}
		})
	}
}

// TestCheckUpdate_404FallsBackToReleases pins the two-channel story: a dead
// manifest falls through to the GitHub Releases fallback on the pinned
// api.github.com template (pointed at the mock here).
func TestCheckUpdate_404FallsBackToReleases(t *testing.T) {
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version.json":
			http.NotFound(w, r)
		case "/repos/yanstd888/9routerx/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tag_name":"v1.9.17","body":"notes","assets":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base = srv.URL
	DefaultUpdateURL = base + "/version.json"
	DefaultGitHubRepo = "yanstd888/9routerx"
	githubReleasesAPI = base + "/repos/%s/releases/latest"

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != "github_releases" || info.LatestVersion != "1.9.17" {
		t.Errorf("fallback result = %+v", info)
	}
}

// TestManifestURL_HostPinned is the anti-SSRF evidence at the unit level: an
// env override may only ever select a path on the pinned update host. Anything
// else — another host, another scheme, a lookalike — is ignored, and the
// compiled-in default (which the mock stands in for here) is what gets dialed.
func TestManifestURL_HostPinned(t *testing.T) {
	resetCheckState(t)
	mock, _ := manifestServer(t, http.StatusOK, `{"latestVersion":"1.9.17"}`)
	DefaultUpdateURL = mock.URL + "/version.json"

	tests := []struct {
		name   string
		env    string
		honors bool
	}{
		{"pinned host honored", "https://raw.githubusercontent.com/yanstd888/9routerx/staging/version.json", true},
		{"foreign host ignored", "https://evil.example/version.json", false},
		{"http downgrade ignored", "http://raw.githubusercontent.com/yanstd888/9routerx/main/version.json", false},
		{"lookalike host ignored", "https://raw.githubusercontent.com.evil.example/version.json", false},
		{"bare host without path honored (root manifest)", "https://raw.githubusercontent.com/yanstd888/9routerx/main/version.json", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("UPDATE_URL", tt.env)
			got := manifestURL()
			honored := got == tt.env
			if honored != tt.honors {
				t.Errorf("UPDATE_URL=%q resolved to %q (honored=%v, want %v)", tt.env, got, honored, tt.honors)
			}
		})
	}
}

// TestRepoName_Validation pins the UPDATE_REPO guard: owner/name pairs pass,
// path tricks and malformed values are ignored.
func TestRepoName_Validation(t *testing.T) {
	resetCheckState(t)
	DefaultGitHubRepo = "yanstd888/9routerx"
	for env, want := range map[string]string{
		"fixture/fork":       "fixture/fork",
		"yanstd888/9routerx": "yanstd888/9routerx",
		"a/b/c":              "yanstd888/9routerx",
		"../etc":             "yanstd888/9routerx",
		"owner/na me":        "yanstd888/9routerx",
		"":                   "yanstd888/9routerx",
	} {
		t.Setenv("UPDATE_REPO", env)
		if got := repoName(); got != want {
			t.Errorf("UPDATE_REPO=%q resolved to %q, want %q", env, got, want)
		}
	}
}

// TestEnsureFreshCheck_CacheBehavior pins the caching contract of the
// dashboard endpoint: the first call fetches, later calls inside the freshness
// window are cache hits that never touch the channel, and a disabled check
// never dials at all.
func TestEnsureFreshCheck_CacheBehavior(t *testing.T) {
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	srv, hits := manifestServer(t, http.StatusOK, `{"latestVersion":"1.9.17","downloadUrl":"https://x/y","sha256":"`+strings.Repeat("a", 64)+`"}`)
	DefaultUpdateURL, DefaultGitHubRepo = srv.URL+"/version.json", ""

	first := EnsureFreshCheck(context.Background(), "test")
	if first.CacheHit || first.Info == nil || !first.Info.HasUpdate {
		t.Fatalf("first call must fetch: %+v", first)
	}

	second := EnsureFreshCheck(context.Background(), "test")
	if !second.CacheHit {
		t.Errorf("second call inside the window must be a cache hit: %+v", second)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("channel hit %d times, want exactly 1", n)
	}

	SetCheckEnabled(false)
	third := EnsureFreshCheck(context.Background(), "test")
	if !third.CacheHit || third.CheckEnabled {
		t.Errorf("disabled check must serve cache without fetching: %+v", third)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("disabled check dialed the channel: %d hits", n)
	}
}

// TestEnsureFreshCheck_SingleFlight pins the single-goroutine discipline: N
// concurrent dashboard opens while a slow fetch runs must collapse into ONE
// channel request.
func TestEnsureFreshCheck_SingleFlight(t *testing.T) {
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	var hits atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release // hold the fetch open so the goroutines pile up behind it
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"latestVersion":"1.9.17","downloadUrl":"https://x/y","sha256":"` + strings.Repeat("a", 64) + `"}`))
	}))
	defer srv.Close()
	DefaultUpdateURL, DefaultGitHubRepo = srv.URL+"/version.json", ""

	const callers = 5
	var wg sync.WaitGroup
	results := make([]CheckOutcome, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = EnsureFreshCheck(context.Background(), "test")
		}(i)
	}
	// Give the goroutines a moment to reach the gate, then let the fetch finish.
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := hits.Load(); n != 1 {
		t.Errorf("channel hit %d times across %d callers, want exactly 1", n, callers)
	}
	var fetched, collapsed int
	for _, out := range results {
		if out.InFlight {
			collapsed++
		} else {
			fetched++
		}
		if out.Info == nil {
			t.Error("every caller must leave with an answer (cache or fetch)")
		}
	}
	if fetched != 1 || collapsed != callers-1 {
		t.Errorf("fetched=%d collapsed=%d, want 1/%d", fetched, collapsed, callers-1)
	}
}

// TestBuildDebugDump_CarriesState pins the /api/update/debug payload: after a
// check the dump carries the record, the raw response snippet and the channel
// detection — the operator's answer to "why does the banner not show?".
func TestBuildDebugDump_CarriesState(t *testing.T) {
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	srv, _ := manifestServer(t, http.StatusOK, mergedManifest)
	DefaultUpdateURL, DefaultGitHubRepo = srv.URL+"/version.json", ""

	if _, err := instrumentedCheck(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	dump := BuildDebugDump()
	if dump.LastCheck == nil || dump.LastCheck.Decision != "update_available" {
		t.Errorf("dump.LastCheck = %+v", dump.LastCheck)
	}
	if !strings.Contains(dump.RawResponse, `"minVersion"`) {
		t.Errorf("dump.RawResponse missing merged contract keys: %q", dump.RawResponse)
	}
	if dump.Channel == "" || dump.ChannelReason == "" {
		t.Errorf("channel detection missing from dump: %q/%q", dump.Channel, dump.ChannelReason)
	}
	if dump.Interval != DefaultCheckInterval.String() {
		t.Errorf("dump.Interval = %q, want default %q", dump.Interval, DefaultCheckInterval)
	}
}

// TestBuildDebugDump_OffInterval pins that "0" is reported, not defaulted.
func TestBuildDebugDump_OffInterval(t *testing.T) {
	resetCheckState(t)
	t.Setenv("UPDATE_CHECK_INTERVAL", "0")
	dump := BuildDebugDump()
	if dump.Interval != "off (UPDATE_CHECK_INTERVAL=0)" {
		t.Errorf("dump.Interval = %q", dump.Interval)
	}
}

// TestUpdateDebugVerbose_LogLines pins the UPDATE_DEBUG=1 contract: every
// check writes its start, fetch result and decision (or failure with cause) to
// stdout — no empty catch, no silent path. The debugOut mirror is captured
// instead of stdout so the test reads exactly what the engine wrote.
func TestUpdateDebugVerbose_LogLines(t *testing.T) {
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	t.Setenv("UPDATE_DEBUG", "1")

	var buf syncBuffer
	oldOut := debugOut
	debugOut = &buf
	t.Cleanup(func() { debugOut = oldOut })

	okSrv, _ := manifestServer(t, http.StatusOK, mergedManifest)
	DefaultUpdateURL, DefaultGitHubRepo = okSrv.URL+"/version.json", ""
	if _, err := instrumentedCheck(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"update check start",
		"channel=",
		"localVersion=1.9.16",
		"update check decision",
		"decision=update_available",
		"source=manifest",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("UPDATE_DEBUG log missing %q; got:\n%s", want, out)
		}
	}

	buf.Reset()
	badSrv, _ := manifestServer(t, http.StatusTooManyRequests, `{"message":"rate limited"}`)
	DefaultUpdateURL = badSrv.URL + "/version.json"
	_, _ = instrumentedCheck(context.Background(), "test")
	out = buf.String()
	if !strings.Contains(out, "update check failed") || !strings.Contains(out, "cause=rate_limited") {
		t.Errorf("UPDATE_DEBUG failure log missing cause; got:\n%s", out)
	}
}

// TestRawResponseSnippetBounded pins the 4KB cap on the debug raw snippet: a
// huge (or hostile) manifest cannot balloon the process's memory via the dump.
func TestRawResponseSnippetBounded(t *testing.T) {
	resetCheckState(t)
	big := `{"latestVersion":"1.0.0","pad":"` + strings.Repeat("x", 1<<20) + `"}`
	srv, _ := manifestServer(t, http.StatusOK, big)
	DefaultUpdateURL, DefaultGitHubRepo = srv.URL+"/version.json", ""
	_, _ = CheckUpdate(context.Background())
	checkObs.mu.Lock()
	defer checkObs.mu.Unlock()
	if len(checkObs.rawSnippet) > rawMaxBytes {
		t.Errorf("raw snippet = %d bytes, cap is %d", len(checkObs.rawSnippet), rawMaxBytes)
	}
}

func lastRecord(t *testing.T) *CheckRecord {
	t.Helper()
	checkObs.mu.Lock()
	defer checkObs.mu.Unlock()
	if checkObs.last == nil {
		t.Fatal("no check record was written")
	}
	return checkObs.last
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// TestEnsureFreshCheck_FailureKeepsPriorCache pins the live-run regression:
// a failed refresh must leave the caller with the PREVIOUS cached answer (or
// the synthesized default on a cold process), never a nil info.
func TestEnsureFreshCheck_FailureKeepsPriorCache(t *testing.T) {
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	good, _ := manifestServer(t, http.StatusOK,
		`{"latestVersion":"1.9.17","downloadUrl":"https://x/y","sha256":"`+strings.Repeat("a", 64)+`"}`)
	DefaultUpdateURL, DefaultGitHubRepo = good.URL+"/version.json", ""

	first := EnsureFreshCheck(context.Background(), "test")
	if first.Info == nil || first.Info.LatestVersion != "1.9.17" {
		t.Fatalf("priming check failed: %+v", first)
	}

	bad, _ := manifestServer(t, http.StatusNotFound, "not found")
	DefaultUpdateURL = bad.URL + "/version.json"

	// Backdate the cache so the refresh actually dials the (now failing)
	// channel — a fresh cache would answer from memory without a fetch.
	cacheMu.Lock()
	lastCheckTime = time.Now().Add(-7 * 24 * time.Hour)
	cacheMu.Unlock()

	second := EnsureFreshCheck(context.Background(), "test")
	if second.Info == nil {
		t.Fatal("a failed refresh dropped the cached answer entirely")
	}
	if second.Info.LatestVersion != "1.9.17" {
		t.Errorf("failed refresh replaced the cache: latest=%q, want 1.9.17", second.Info.LatestVersion)
	}
	if !second.Stale {
		t.Error("a failed refresh must mark the served answer stale")
	}

	// Cold process + failing channel: the synthesized default survives.
	resetCheckState(t)
	CurrentVersion = "1.9.16"
	DefaultUpdateURL = bad.URL + "/version.json"
	cold := EnsureFreshCheck(context.Background(), "test")
	if cold.Info == nil || cold.Info.CurrentVersion != "1.9.16" || cold.Info.HasUpdate {
		t.Errorf("cold failure must keep the local-only default: %+v", cold.Info)
	}
}
