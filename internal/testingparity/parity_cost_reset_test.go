// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package testingparity

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	json "encoding/json/v2"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/middleware"
	"9routerx/proxy/internal/models"
)

// parity_cost_reset_test.go ports the daily-cost-cap semantics of serenhope
// tests/unit/api-key-cost-guard.test.js onto the Go db layer and the real
// key-policy gate (FASE 8 task 2). Serenhope-parity: same inputs, same
// verdicts.
//
// Upstream drives its real SQLite adapter because the lazy rollover lives in
// SQL (date('now')); the same is true here — the rollover lives in the CASE
// inside BumpApiKeyCostSpent's UPDATE, so every row below goes through
// db.OpenDatabase + db.EnsureCoreSchema on a throwaway file and through the
// middleware that reads it (internal/middleware/keypolicy.go refuseDailyCost).
//
// Upstream's verdicts come from validateApiKey; the Go gate answers over HTTP
// (429 "API key daily cost limit exceeded"), which is the same verdict one
// layer higher. The write side is BumpApiKeyCostSpent (upstream
// saveRequestUsage), the read side ApiKeyCostToday (upstream's in-transaction
// SELECT), and the day bucket is the same YYYY-MM-DD UTC string upstream's
// utcDayKey stamps.

// almostEqual compares float64 spend read back through SQLite REAL.
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// newParityCostDB opens the throwaway schema'd database every row runs
// against, mirroring upstream's per-test temp DATA_DIR.
func newParityCostDB(t *testing.T) *db.Repo {
	t.Helper()
	conn, err := db.OpenDatabase(filepath.Join(t.TempDir(), "data.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

// utcDayKey formats the UTC day bucket exactly as the gate and the bump
// write compare it (internal/middleware/keypolicy.go utcDayKey).
func utcDayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// requestThroughKeyPolicy sends one authenticated GET through the real
// KeyPolicy middleware with the repo as its cost reader — the same path a
// production request takes between auth and the proxy.
func requestThroughKeyPolicy(t *testing.T, repo *db.Repo, key *models.APIKey) *httptest.ResponseRecorder {
	t.Helper()
	policy := middleware.NewKeyPolicy(repo)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ApiKeyContextKey, key))
	rec := httptest.NewRecorder()
	policy.Middleware(next).ServeHTTP(rec, req)
	return rec
}

// createParityKey seeds one active key row, the equivalent of upstream's
// seedKey helper with its default (unlimited) columns.
func createParityKey(t *testing.T, repo *db.Repo, id string) *models.APIKey {
	t.Helper()
	if err := repo.CreateApiKey(id, "sk-parity-cost-"+id, "cost guard parity", "machine"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	key, err := repo.GetApiKeyByID(id)
	if err != nil || key == nil {
		t.Fatalf("GetApiKeyByID(%s): %v (key nil: %v)", id, err, key == nil)
	}
	return key
}

// TestSerenehopeCostLandsInCostSpentAndCostDay ports "adds the cost of a
// logged request to today's counter": the charge lands in costSpent with
// costDay stamped today.
func TestSerenehopeCostLandsInCostSpentAndCostDay(t *testing.T) {
	repo := newParityCostDB(t)
	key := createParityKey(t, repo, "k-lands")
	today := utcDayKey(time.Now())

	if err := repo.BumpApiKeyCostSpent(key.ID, 0.15, today); err != nil {
		t.Fatalf("BumpApiKeyCostSpent: %v", err)
	}

	row, err := repo.GetApiKeyByID(key.ID)
	if err != nil || row == nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	if !almostEqual(row.CostSpent, 0.15) {
		t.Errorf("serenhope-parity: costSpent = %v, want 0.15", row.CostSpent)
	}
	if row.CostDay != today {
		t.Errorf("serenhope-parity: costDay = %q, want %q", row.CostDay, today)
	}
	if spent, err := repo.ApiKeyCostToday(key.ID, today); err != nil || !almostEqual(spent, 0.15) {
		t.Errorf("serenhope-parity: ApiKeyCostToday = %v, %v; want 0.15", spent, err)
	}
}

// TestSerenehopeCostRefusesAtCap ports "refuses with COST_LIMIT_EXCEEDED once
// the day's spend reaches the cap" (429 at the Go gate) and its sibling "lets
// the key through while today's spend is still below the cap".
func TestSerenehopeCostRefusesAtCap(t *testing.T) {
	repo := newParityCostDB(t)
	today := utcDayKey(time.Now())

	// At the cap: 0.15 spent against a 0.1 ceiling refuses.
	atCap := createParityKey(t, repo, "k-at-cap")
	if err := repo.SetApiKeyDailyCostLimit(atCap.ID, 0.1); err != nil {
		t.Fatalf("SetApiKeyDailyCostLimit: %v", err)
	}
	if err := repo.BumpApiKeyCostSpent(atCap.ID, 0.15, today); err != nil {
		t.Fatalf("BumpApiKeyCostSpent: %v", err)
	}
	// Re-read the row: the gate judges the key object on the request context,
	// so it must carry the limit that was just set.
	atCapRow, err := repo.GetApiKeyByID(atCap.ID)
	if err != nil || atCapRow == nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	rec := requestThroughKeyPolicy(t, repo, atCapRow)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("serenhope-parity: status = %d, want 429", rec.Code)
	}
	// The gate answers upstream's exact sentence inside the JSON error
	// envelope: {"error":{"message":"API key daily cost limit exceeded",...}}.
	var errBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("serenhope-parity: 429 body is not JSON: %v (body %q)", err, rec.Body.String())
	}
	if want := "API key daily cost limit exceeded"; errBody.Error.Message != want {
		t.Errorf("serenhope-parity: 429 message = %q, want %q", errBody.Error.Message, want)
	}

	// Below the cap: 0.15 spent against a 1 ceiling passes.
	belowCap := createParityKey(t, repo, "k-below-cap")
	if err := repo.SetApiKeyDailyCostLimit(belowCap.ID, 1); err != nil {
		t.Fatalf("SetApiKeyDailyCostLimit: %v", err)
	}
	if err := repo.BumpApiKeyCostSpent(belowCap.ID, 0.15, today); err != nil {
		t.Fatalf("BumpApiKeyCostSpent: %v", err)
	}
	belowCapRow, err := repo.GetApiKeyByID(belowCap.ID)
	if err != nil || belowCapRow == nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	rec = requestThroughKeyPolicy(t, repo, belowCapRow)
	if rec.Code != http.StatusOK {
		t.Errorf("serenhope-parity: status = %d, want 200 (below the cap)", rec.Code)
	}
}

// TestSerenehopeCostCapZeroIsUnlimited ports "ignores the cap when it is
// zero, even with spend already recorded". The gate short-circuits on
// DailyCostLimit <= 0 before the spend is even read, so a recorded 99 spent
// still passes.
func TestSerenehopeCostCapZeroIsUnlimited(t *testing.T) {
	repo := newParityCostDB(t)
	today := utcDayKey(time.Now())
	key := createParityKey(t, repo, "k-zero-cap") // fresh keys carry dailyCostLimit = 0
	if key.DailyCostLimit != 0 {
		t.Fatalf("serenhope-parity: fresh DailyCostLimit = %v, want 0 (unlimited)", key.DailyCostLimit)
	}
	if err := repo.BumpApiKeyCostSpent(key.ID, 99, today); err != nil {
		t.Fatalf("BumpApiKeyCostSpent: %v", err)
	}

	row, err := repo.GetApiKeyByID(key.ID)
	if err != nil || row == nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	rec := requestThroughKeyPolicy(t, repo, row)
	if rec.Code != http.StatusOK {
		t.Errorf("serenhope-parity: status = %d, want 200 (cap 0 is unlimited)", rec.Code)
	}
}

// TestSerenehopeCostWrongDayReadsZero ports the read half of "rolls over: a
// stamp from an earlier day neither blocks nor survives the next write": a
// costDay stamp from any other day reads as zero spend, so yesterday's 5 USD
// must not block today's first request.
func TestSerenehopeCostWrongDayReadsZero(t *testing.T) {
	repo := newParityCostDB(t)
	today := utcDayKey(time.Now())
	key := createParityKey(t, repo, "k-rollover")

	// Seed yesterday's spend: costDay is empty, so the bump REPLACES the
	// accumulator with 5 under the 2000-01-01 stamp.
	if err := repo.BumpApiKeyCostSpent(key.ID, 5, "2000-01-01"); err != nil {
		t.Fatalf("BumpApiKeyCostSpent (seed): %v", err)
	}

	spent, err := repo.ApiKeyCostToday(key.ID, today)
	if err != nil {
		t.Fatalf("ApiKeyCostToday: %v", err)
	}
	if spent != 0 {
		t.Errorf("serenhope-parity: ApiKeyCostToday on wrong day = %v, want 0", spent)
	}

	// The gate must pass on that read: yesterday's 5 USD does not block.
	row, err := repo.GetApiKeyByID(key.ID)
	if err != nil || row == nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	if err := repo.SetApiKeyDailyCostLimit(key.ID, 0.1); err != nil {
		t.Fatalf("SetApiKeyDailyCostLimit: %v", err)
	}
	if rec := requestThroughKeyPolicy(t, repo, row); rec.Code != http.StatusOK {
		t.Errorf("serenhope-parity: status = %d, want 200 (stale stamp must not block)", rec.Code)
	}
}

// TestSerenehopeCostLazyResetReplacesNotAdds ports the write half of the same
// upstream row: after the rollover the next write REPLACES the accumulator —
// 0.15, not 5.15.
func TestSerenehopeCostLazyResetReplacesNotAdds(t *testing.T) {
	repo := newParityCostDB(t)
	today := utcDayKey(time.Now())
	key := createParityKey(t, repo, "k-replace")

	if err := repo.BumpApiKeyCostSpent(key.ID, 5, "2000-01-01"); err != nil {
		t.Fatalf("BumpApiKeyCostSpent (seed): %v", err)
	}
	if err := repo.BumpApiKeyCostSpent(key.ID, 0.15, today); err != nil {
		t.Fatalf("BumpApiKeyCostSpent: %v", err)
	}

	row, err := repo.GetApiKeyByID(key.ID)
	if err != nil || row == nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	if !almostEqual(row.CostSpent, 0.15) {
		t.Errorf("serenhope-parity: costSpent = %v, want 0.15 (replaced, not 5.15)", row.CostSpent)
	}
	if row.CostDay != today {
		t.Errorf("serenhope-parity: costDay = %q, want %q", row.CostDay, today)
	}
}
