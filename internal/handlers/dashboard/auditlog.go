// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	json "encoding/json/v2"

	"9routerx/proxy/internal/auth"
	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/models"
)

// Audit action vocabulary. Dotted <subject>.<verb> names, written by the
// dashboard handlers that mutate state (FASE 10.6).
const (
	AuditActionCreateKey        = "api_key.create"
	AuditActionDeleteKey        = "api_key.delete"
	AuditActionUpdateKey        = "api_key.update"
	AuditActionToggleKey        = "api_key.toggle"
	AuditActionCreateConnection = "connection.create"
	AuditActionDeleteConnection = "connection.delete"
	AuditActionUpdateConnection = "connection.update"
	AuditActionLogin            = "auth.login"
	AuditActionLoginFailed      = "auth.login_failed"
	AuditActionUpdateSettings   = "settings.update"
)

// auditActor is the principal every dashboard mutation is attributed to. The
// dashboard has one login surface, so the actor names the surface and the IP
// column distinguishes the caller — the same shape the security log uses.
const auditActor = "dashboard"

// auditWriteTimeout bounds one audit insert. The write is detached from the
// request on purpose: a slow or wedged SQLite write must never be visible to
// the operator whose action it trails.
const auditWriteTimeout = 10 * time.Second

// recordAudit trails one dashboard mutation into the compliance audit log
// (FASE 10.6) and returns immediately.
//
// It is called write-ahead — before the mutation it describes — so a failed
// action still has a trace. The insert runs in its own goroutine with its own
// 10s timeout, NOT under r.Context(): the request context is cancelled the
// moment the handler returns, and an audit entry that dies with the request is
// an audit entry that mostly does not exist. A failed insert is therefore not
// reportable either; it is dropped, because an audit gap must never fail the
// operator action it trails. before/after are marshalled to the entry's
// beforeJSON/afterJSON; nil marshals to "" (pass nil for before on creates and
// for after on deletes).
//
// Callers must only pass non-secret summaries: the log is read back through
// the dashboard, so a key or a webhook URL in a payload does not belong here.
func (h *DashboardHandler) recordAudit(r *http.Request, action, target string, before, after any) {
	if h.Repo == nil {
		return
	}
	entry := db.AuditLogEntry{
		Actor:      auditActor,
		Action:     action,
		Target:     target,
		BeforeJSON: auditJSON(before),
		AfterJSON:  auditJSON(after),
		IP:         auth.LoginClientIP(r),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
		defer cancel()
		_ = h.Repo.InsertAuditLog(ctx, entry)
	}()
}

// auditJSON marshals an audit payload, mapping nil (and anything that will not
// marshal, or marshals to nothing — json/v2 encodes a channel as empty bytes
// with no error) to the column's "absent" value.
func auditJSON(v any) string {
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// maxAuditLimit caps the ?limit= parameter. The view pages; a larger value
// only serves a caller walking the whole trail in one response.
const maxAuditLimit = 1000

// HandleListAuditLogs handles GET /api/audit?limit=&actor=&action=&format=.
// format=csv streams the same filtered rows as an attachment; the default (and
// anything unrecognised) stays JSON.
func (h *DashboardHandler) HandleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxAuditLimit {
		limit = maxAuditLimit
	}
	actor := strings.TrimSpace(r.URL.Query().Get("actor"))
	action := strings.TrimSpace(r.URL.Query().Get("action"))

	entries, err := h.Repo.ListAuditLogs(r.Context(), limit, actor, action)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "csv") {
		stamp := time.Now().UTC().Format("2006-01-02")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="9routerx-audit-%s.csv"`, stamp))
		writeAuditCSV(w, entries)
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   len(entries),
	})
}

// writeAuditCSV streams the audit trail as CSV. The header row is written first
// so a truncated export is still a well-formed file. The AuditLogEntry carries
// no severity of its own — before/after are the detail payload, so they export
// as their stored JSON strings.
func writeAuditCSV(w http.ResponseWriter, entries []db.AuditLogEntry) {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"timestamp", "actor", "action", "target", "ip", "before", "after"})
	for _, e := range entries {
		_ = cw.Write([]string{
			e.Timestamp,
			sanitizeAuditCSVCell(e.Actor),
			sanitizeAuditCSVCell(e.Action),
			sanitizeAuditCSVCell(e.Target),
			sanitizeAuditCSVCell(e.IP),
			sanitizeAuditCSVCell(e.BeforeJSON),
			sanitizeAuditCSVCell(e.AfterJSON),
		})
	}
	cw.Flush()
}

// sanitizeAuditCSVCell neutralizes CSV formula injection, the same mitigation
// the usage export applies (internal/handlers/usage_export.go's
// sanitizeCSVCell, copied because it is package-private): a cell whose first
// byte a spreadsheet evaluates as a formula (=, +, -, @, tab, CR) gets a single
// quote prefix and renders as text.
func sanitizeAuditCSVCell(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

// auditSettingsKeys extracts the sorted key names of a settings payload. The
// settings audit entry records WHICH keys a write touched, never their values:
// values ride into this handler from the client and can carry credentials.
func auditSettingsKeys(updates map[string]any) []string {
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// apiKeyAuditState summarizes an api key row for the audit trail. It carries
// the operator-facing identity only: the secret, its hashes and its display
// mask never enter the log.
func apiKeyAuditState(k *models.APIKey) any {
	if k == nil {
		return nil
	}
	return map[string]any{
		"id": k.ID, "name": stringOrEmpty(k.Name), "isActive": k.IsActive == 1,
		"planId": stringOrEmpty(k.PlanID),
	}
}

// connectionAuditState summarizes a provider connection row for the audit
// trail: identity fields only. The data blob holds the credential, so it never
// enters the log.
func connectionAuditState(c *models.ProviderConnection) any {
	if c == nil {
		return nil
	}
	name := ""
	if c.Name != nil {
		name = *c.Name
	} else if c.Email != nil {
		name = *c.Email
	}
	return map[string]any{
		"id": c.ID, "provider": c.Provider, "name": name, "isActive": c.IsActive == 1,
	}
}

// mapIsActive wraps the old toggle state, nil when it was never read.
func mapIsActive(v *bool) any {
	if v == nil {
		return nil
	}
	return map[string]any{"isActive": *v}
}
