// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9routerx/proxy/internal/updater"
)

// fakeCheckOutcome builds the engine answer a test wants, without any network.
func fakeCheckOutcome(info *updater.UpdateInfo, cacheHit bool) updater.CheckOutcome {
	if info == nil {
		info = &updater.UpdateInfo{CurrentVersion: updater.CurrentVersion}
	}
	ch, _ := updater.DetectDistChannel()
	return updater.CheckOutcome{
		Info:         info,
		CacheHit:     cacheHit,
		CheckEnabled: true,
		Channel:      ch,
	}
}

// swapUpdateFakes points the handler seams at test doubles and restores them.
func swapUpdateFakes(t *testing.T, calls *int, outcome updater.CheckOutcome, dump updater.DebugDump) {
	t.Helper()
	oldCheck, oldDebug := updateCheckFn, updateDebugFn
	updateCheckFn = func(ctx context.Context, trigger string) updater.CheckOutcome {
		*calls++
		return outcome
	}
	updateDebugFn = func() updater.DebugDump { return dump }
	t.Cleanup(func() { updateCheckFn, updateDebugFn = oldCheck, oldDebug })
}

// TestHandleUpdateCheck_ServesWithoutFetching pins the endpoint contract: the
// dashboard may call /api/update/check as often as it likes; the handler calls
// the engine exactly once per request and NEVER per render — the engine's
// cache decides whether a fetch happens. With a fake reporting a cache hit,
// the wire answer carries the banner's fields.
func TestHandleUpdateCheck_ServesWithoutFetching(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	calls := 0
	outcome := fakeCheckOutcome(&updater.UpdateInfo{
		CurrentVersion: "1.9.16",
		LatestVersion:  "1.9.17",
		HasUpdate:      true,
		ReleaseURL:     "https://github.com/yanstd888/9RouterX/releases/tag/v1.9.17",
		SHA256:         "aa11",
		Source:         "manifest",
	}, true)
	swapUpdateFakes(t, &calls, outcome, updater.DebugDump{})

	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("handler called the engine %d times for one request", calls)
	}
	var body struct {
		CurrentVersion string `json:"currentVersion"`
		LatestVersion  string `json:"latestVersion"`
		HasUpdate      bool   `json:"hasUpdate"`
		Channel        string `json:"channel"`
		CacheHit       bool   `json:"cacheHit"`
		CheckEnabled   bool   `json:"checkEnabled"`
		ReleaseURL     string `json:"releaseUrl"`
		SHA256         string `json:"sha256"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.HasUpdate || body.LatestVersion != "1.9.17" || body.CurrentVersion != "1.9.16" {
		t.Errorf("banner fields wrong: %+v", body)
	}
	if body.Channel == "" || !body.CacheHit || !body.CheckEnabled {
		t.Errorf("check metadata missing: %+v", body)
	}
	if body.ReleaseURL == "" || body.SHA256 != "aa11" {
		t.Errorf("release contract fields missing: %+v", body)
	}
}

// TestHandleUpdateCheck_FailureIsNotAnError pins graceful degradation at the
// HTTP layer: when the engine reports a failed/absent check, the endpoint
// still answers 200 with the local version and hasUpdate=false — the dashboard
// renders no banner instead of an error.
func TestHandleUpdateCheck_FailureIsNotAnError(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	calls := 0
	outcome := fakeCheckOutcome(nil, false)
	outcome.CheckEnabled = false
	outcome.Stale = true
	swapUpdateFakes(t, &calls, outcome, updater.DebugDump{})

	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with degraded body", rec.Code)
	}
	var body struct {
		HasUpdate      bool   `json:"hasUpdate"`
		CheckEnabled   bool   `json:"checkEnabled"`
		Stale          bool   `json:"stale"`
		CurrentVersion string `json:"currentVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.HasUpdate {
		t.Error("a degraded check must never claim an update")
	}
	if !body.Stale || body.CheckEnabled {
		t.Errorf("degraded metadata wrong: %+v", body)
	}
}

// TestHandleUpdateDebug_GatedByEnvAndAdmin pins the debug endpoint's two
// locks: UPDATE_DEBUG_ENDPOINT=1 else 404, and RequireAdminAuth in the real
// route table. Here the env gate is exercised; the admin gate is mounted in
// router.go's RequireAdminAuth group.
func TestHandleUpdateDebug_GatedByEnvAndAdmin(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	calls := 0
	dump := updater.DebugDump{CurrentVersion: "1.9.16", Channel: updater.ChannelBinary, ChannelReason: "packaged"}
	swapUpdateFakes(t, &calls, fakeCheckOutcome(nil, true), dump)

	// Default: off → 404, engine never consulted.
	req := httptest.NewRequest(http.MethodGet, "/api/update/debug", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("default status = %d, want 404", rec.Code)
	}
	if calls != 0 {
		t.Fatalf("debug dump ran %d times while disabled", calls)
	}

	// Opted in via env → dumps state.
	t.Setenv("UPDATE_DEBUG_ENDPOINT", "1")
	req = httptest.NewRequest(http.MethodGet, "/api/update/debug", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("opted-in status = %d", rec.Code)
	}
	var body updater.DebugDump
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CurrentVersion != "1.9.16" || body.Channel != updater.ChannelBinary {
		t.Errorf("dump body wrong: %+v", body)
	}
}

// TestUpdateSettingsRuntimeToggle pins the live "Check updates" toggle: a
// settings write carrying updateCheckEnabled flips the engine flag in the same
// request, without a restart.
func TestUpdateSettingsRuntimeToggle(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	if !updater.IsCheckEnabled() {
		updater.SetCheckEnabled(true)
	}
	t.Cleanup(func() { updater.SetCheckEnabled(true) })

	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"updateCheckEnabled": false}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings write failed: %d %s", rec.Code, rec.Body.String())
	}
	if updater.IsCheckEnabled() {
		t.Error("updateCheckEnabled=false must flip the runtime flag off immediately")
	}

	req = httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"updateCheckEnabled": true}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings write failed: %d %s", rec.Code, rec.Body.String())
	}
	if !updater.IsCheckEnabled() {
		t.Error("updateCheckEnabled=true must flip the runtime flag back on")
	}
}
