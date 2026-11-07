// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"9routerx/proxy/internal/db"
)

// waitForAudit polls the repo until cond holds; recordAudit is asynchronous by
// contract, so the observation window is the test's polling loop.
func waitForAudit(t *testing.T, repo *db.Repo, cond func([]db.AuditLogEntry) bool) []db.AuditLogEntry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := repo.ListAuditLogs(context.Background(), 0, "", "")
		if err != nil {
			t.Fatalf("ListAuditLogs: %v", err)
		}
		if cond(entries) {
			return entries
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("audit entry never appeared within 5s")
	return nil
}

func TestRecordAuditWritesEntry(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	req := httptest.NewRequest(http.MethodPost, "/api/keys", nil)
	req.RemoteAddr = "203.0.113.9:12345"
	h.recordAudit(req, AuditActionCreateKey, "key-1", nil, map[string]any{"name": "ci"})

	entries := waitForAudit(t, repo, func(entries []db.AuditLogEntry) bool {
		return len(entries) == 1
	})
	e := entries[0]
	if e.Action != AuditActionCreateKey || e.Target != "key-1" || e.Actor != "dashboard" {
		t.Errorf("entry = %+v, want the create action with its target and actor", e)
	}
	if e.IP != "203.0.113.9" {
		t.Errorf("ip = %q, want the client address without its port", e.IP)
	}
	if e.BeforeJSON != "" {
		t.Errorf("beforeJSON = %q, want absent for a create", e.BeforeJSON)
	}
	if e.AfterJSON != `{"name":"ci"}` {
		t.Errorf("afterJSON = %q, want the marshalled payload", e.AfterJSON)
	}
	if e.Timestamp == "" {
		t.Error("entry carries no timestamp")
	}
}

// TestRecordAuditSurvivesUnmarshalablePayload proves a payload json/v2 refuses
// degrades to {} instead of panicking inside the detached goroutine.
func TestRecordAuditSurvivesUnmarshalablePayload(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	req := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	h.recordAudit(req, AuditActionUpdateSettings, "", make(chan int), nil)

	entries := waitForAudit(t, repo, func(entries []db.AuditLogEntry) bool {
		return len(entries) == 1
	})
	if entries[0].BeforeJSON != "{}" {
		t.Errorf("beforeJSON = %q, want the {} fallback", entries[0].BeforeJSON)
	}
}

// TestRecordAuditNilBeforeAndAfter proves the nil mapping: both absent fields
// marshal to the column's empty value, which is what creates and deletes pass.
func TestRecordAuditNilBeforeAndAfter(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	req := httptest.NewRequest(http.MethodDelete, "/api/connections/conn-1", nil)
	h.recordAudit(req, AuditActionDeleteConnection, "conn-1", nil, nil)

	entries := waitForAudit(t, repo, func(entries []db.AuditLogEntry) bool {
		return len(entries) == 1
	})
	if entries[0].BeforeJSON != "" || entries[0].AfterJSON != "" {
		t.Errorf("before/after = %q / %q, want both absent",
			entries[0].BeforeJSON, entries[0].AfterJSON)
	}
}

func TestHandleListAuditLogs(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	seed := []db.AuditLogEntry{
		{Timestamp: "2026-01-01T10:00:00Z", Actor: "dashboard", Action: "api_key.create", Target: "a"},
		{Timestamp: "2026-01-02T10:00:00Z", Actor: "dashboard", Action: "settings.update"},
		{Timestamp: "2026-01-03T10:00:00Z", Actor: "dashboard", Action: "auth.login_failed", IP: "198.51.100.7"},
	}
	for i, e := range seed {
		if err := repo.InsertAuditLog(ctx, e); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	r := chi.NewRouter()
	h := NewDashboardHandler(repo)
	r.Get("/api/audit", h.HandleListAuditLogs)

	cases := []struct {
		name       string
		query      string
		wantStatus int
		wantTotal  int
		wantFirst  string
	}{
		{"lists newest first", "", http.StatusOK, 3, "auth.login_failed"},
		{"filters by action", "?action=settings.update", http.StatusOK, 1, "settings.update"},
		{"filters by actor", "?actor=operator", http.StatusOK, 0, ""},
		{"caps the limit", "?limit=2", http.StatusOK, 2, "auth.login_failed"},
		{"ignores a bad limit", "?limit=abc", http.StatusOK, 3, "auth.login_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/audit"+tc.query, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Entries []db.AuditLogEntry `json:"entries"`
				Total   int                `json:"total"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Total != tc.wantTotal || len(body.Entries) != tc.wantTotal {
				t.Fatalf("total = %d, entries = %d, want %d", body.Total, len(body.Entries), tc.wantTotal)
			}
			if tc.wantTotal > 0 && body.Entries[0].Action != tc.wantFirst {
				t.Errorf("first action = %q, want %q", body.Entries[0].Action, tc.wantFirst)
			}
		})
	}
}

// TestHandleListAuditLogsCSV proves the CSV shape: content type, header row,
// one data row per entry, and the formula-injection mitigation on the columns a
// client-controlled string can reach.
func TestHandleListAuditLogsCSV(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	seed := []db.AuditLogEntry{
		{Timestamp: "2026-01-01T10:00:00Z", Actor: "dashboard", Action: "api_key.create", Target: "k-1", IP: "203.0.113.9"},
		{Timestamp: "2026-01-02T10:00:00Z", Actor: "dashboard", Action: "=2+5+cmd|' /C calc'!A0", Target: "@SUM(A1)", AfterJSON: `{"name":"ci"}`},
	}
	for i, e := range seed {
		if err := repo.InsertAuditLog(ctx, e); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	r := chi.NewRouter()
	h := NewDashboardHandler(repo)
	r.Get("/api/audit", h.HandleListAuditLogs)

	req := httptest.NewRequest(http.MethodGet, "/api/audit?format=csv", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/csv; charset=utf-8", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition = %q, want a .csv attachment", cd)
	}

	body := rec.Body.String()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != len(seed)+1 {
		t.Fatalf("csv has %d lines, want a header + %d rows: %q", len(lines), len(seed), body)
	}
	if header := lines[0]; header != "timestamp,actor,action,target,ip,before,after" {
		t.Errorf("header = %q, want the seven audit columns", header)
	}
	// The formula-shaped action and target cells carry the "'" prefix, so a
	// spreadsheet renders them as text instead of executing them.
	if !strings.Contains(body, `,'=2+5+cmd`) {
		t.Errorf("csv = %q, want the =-prefixed action cell neutralized", body)
	}
	if !strings.Contains(body, `,'@SUM(A1)`) {
		t.Errorf("csv = %q, want the @-prefixed target cell neutralized", body)
	}
	// A plain cell keeps no prefix. Rows are newest first, so the create entry
	// is the second data row.
	if !strings.Contains(lines[2], ",dashboard,api_key.create,k-1,203.0.113.9") {
		t.Errorf("data row = %q, want the unsanitized fields verbatim", lines[2])
	}
}

func TestHandleListAuditLogsJSONDefault(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	if err := repo.InsertAuditLog(ctx, db.AuditLogEntry{
		Timestamp: "2026-01-01T10:00:00Z", Actor: "dashboard", Action: "api_key.create",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/api/audit", NewDashboardHandler(repo).HandleListAuditLogs)

	// No format parameter, and an unrecognised one, both stay JSON.
	for _, query := range []string{"", "?format=json", "?format=xlsx"} {
		req := httptest.NewRequest(http.MethodGet, "/api/audit"+query, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", query, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "csv") {
			t.Errorf("%s: Content-Type = %q, want JSON", query, ct)
		}
		if !strings.Contains(rec.Body.String(), `"entries"`) {
			t.Errorf("%s: body = %q, want the JSON envelope", query, rec.Body.String())
		}
	}
}

// TestCreateApiKeyLeavesAuditTrail proves the wiring end to end: a dashboard
// write performed through the real handler leaves its write-ahead trace even
// though the handler never waited for it.
func TestCreateApiKeyLeavesAuditTrail(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	req := httptest.NewRequest(http.MethodPost, "/api/keys",
		strings.NewReader(`{"id":"audit-key-1","name":"traced"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/keys = %d: %s", rec.Code, rec.Body.String())
	}

	entries := waitForAudit(t, repo, func(entries []db.AuditLogEntry) bool {
		for _, e := range entries {
			if e.Action == AuditActionCreateKey && e.Target == "audit-key-1" {
				return true
			}
		}
		return false
	})
	for _, e := range entries {
		if e.Action == AuditActionCreateKey {
			if e.AfterJSON == "" || strings.Contains(e.AfterJSON, "sk-") {
				t.Errorf("afterJSON = %q, want the payload without the secret", e.AfterJSON)
			}
		}
	}
}

// TestDeleteConnectionLeavesAuditTrail proves the delete wiring carries the
// before state and no after state.
func TestDeleteConnectionLeavesAuditTrail(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// Seed a connection through the API, then delete it.
	create := httptest.NewRequest(http.MethodPost, "/api/connections",
		strings.NewReader(`{"id":"conn-audit-1","provider":"claude","authType":"apikey","name":"Traced","data":{"apiKey":"sk-upstream"}}`))
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusOK {
		t.Fatalf("POST /api/connections = %d: %s", createRec.Code, createRec.Body.String())
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/connections/conn-audit-1", nil)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, del)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/connections = %d: %s", delRec.Code, delRec.Body.String())
	}

	entries := waitForAudit(t, repo, func(entries []db.AuditLogEntry) bool {
		for _, e := range entries {
			if e.Action == AuditActionDeleteConnection {
				return true
			}
		}
		return false
	})
	for _, e := range entries {
		if e.Action != AuditActionDeleteConnection {
			continue
		}
		if e.Target != "conn-audit-1" || e.AfterJSON != "" {
			t.Errorf("delete entry = %+v, want the target and no after state", e)
		}
		if !strings.Contains(e.BeforeJSON, "conn-audit-1") {
			t.Errorf("beforeJSON = %q, want the deleted row's identity", e.BeforeJSON)
		}
		if strings.Contains(e.BeforeJSON, "sk-upstream") {
			t.Errorf("beforeJSON = %q, must never carry the credential", e.BeforeJSON)
		}
	}
}

func TestAuditSettingsKeysSorts(t *testing.T) {
	got := auditSettingsKeys(map[string]any{"zeta": 1, "alpha": 2, "mid": 3})
	want := []string{"alpha", "mid", "zeta"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("auditSettingsKeys = %v, want %v", got, want)
	}
	if auditSettingsKeys(nil) == nil {
		t.Error("auditSettingsKeys(nil) = nil, want an empty slice")
	}
}
