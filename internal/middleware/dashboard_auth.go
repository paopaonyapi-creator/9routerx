// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"net/http"
	"time"

	"9routerx/proxy/internal/auth"
	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/keikey"
)

// RequireDashboardAuth keeps operator management separate from engine keys.
// Engine credentials are admitted only to explicit read-only metadata routes.

// PasswordHeaderCarriesOwnAuth names the one always-protected path whose handler
// re-verifies the dashboard password header itself.
//
// The header is a step-up credential the middleware cannot check on its own —
// verification needs the stored bcrypt hash, which lives in a repo the gate does
// not hold. So it is honoured only where the handler behind the gate validates
// the value: the backup download. Every other always-protected path keeps
// requiring a session or the CLI token, because none of them re-check it, and
// admitting the header by presence alone let any caller able to set one
// arbitrary header reach shutdown, self-update, health reset and the OAuth
// auto-imports.
const PasswordHeaderCarriesOwnAuth = "/api/settings/database"

// DashboardPasswordHeader is the step-up credential the backup download
// re-verifies itself. It lives here as well because the middleware has to let a
// request carrying it reach that verification.
const DashboardPasswordHeader = "x-9r-password"

// allowsPasswordHeader reports whether the password header should admit a
// request past the gate. This is a path check, not a credential check: only the
// handler that re-verifies the value can turn its presence into access.
func allowsPasswordHeader(path string) bool {
	return path == PasswordHeaderCarriesOwnAuth
}

func IsAlwaysProtectedPath(path string) bool {
	switch path {
	case "/api/shutdown",
		"/api/settings/database",
		"/api/settings/database/prune",
		"/api/settings/database/prune-usage",
		"/api/settings/database/vacuum",
		"/api/version/shutdown",
		"/api/version/update",
		"/admin/health/reset",
		"/api/oauth/cursor/auto-import",
		"/api/oauth/kiro/auto-import":
		return true
	default:
		return false
	}
}

// storageMaintenancePaths are the destructive storage actions the dashboard
// offers beside its storage numbers: prune the diagnostics, prune the billing
// ledger, and compact the file.
//
// They sit on the always-protected list because they delete rows. But the
// dashboard has to be able to run them, and an install that does not require a
// login has no session to run them with — the SPA authenticates with nothing
// at all in that mode, because there is nothing to authenticate against. A
// gate that refuses every credential there leaves the operator with buttons
// that cannot work: the storage card in the profile page reported "Unauthorized:
// admin session or CLI token required" on every press, and the only way to
// prune was a curl carrying the CLI token.
//
// So these three are the deliberate exception to the always-protected rule:
// with login off, the dashboard operator is admitted. That is the same trust
// the rest of the dashboard API already extends to an open install, where
// anyone reaching it can delete every API key and every connection — the
// storage card is not the sensitive surface. An install that does require a
// login keeps the full gate: a session cookie or the CLI token, never a client
// API key.
//
// The backup download is deliberately NOT in this set. It hands out the whole
// database, keys included, so it stays behind the session or the CLI token.
var storageMaintenancePaths = map[string]bool{
	"/api/settings/database/prune":       true,
	"/api/settings/database/prune-usage": true,
	"/api/settings/database/vacuum":      true,
}

// RequireDashboardSessionAuth gates the dashboard endpoints that describe the
// install itself — the security log (failed attempts and probes), the
// notification feed (quota pressure) and the team cost centers — behind a
// dashboard session or the local CLI token. An engine client API key is
// deliberately refused here (port(serenhope) parity): the credential handed to
// Cursor or Claude Code must not be able to read which IPs probed the gateway
// or which keys are near their budget. An install with login disabled keeps
// working, because there is no session to present and the whole dashboard is
// already open in that mode.
func engineDashboardReadAllowed(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch path {
	case "/api/providers/catalog", "/api/models/caps":
		return true
	default:
		return false
	}
}

// Classify against the current row, not a cached engine snapshot, without
// incrementing engine usage. Sentinel and pending credentials are never grants.
func dashboardEngineCredential(w http.ResponseWriter, r *http.Request, repo *db.Repo, allowRead bool) bool {
	key := ExtractApiKey(r)
	if key == "" {
		return false
	}
	lookup := keikey.LookupHash(key)
	obj, err := resolveUncachedApiKey(repo, lookup, key)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "Internal server error")
		return true
	}
	if obj == nil || !verifyApiKey(key, obj) || obj.IsActive != 1 || apiKeyExpired(obj.ExpiresAt, time.Now()) {
		handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Invalid or inactive API key")
		return true
	}
	if !allowRead {
		// A named code rather than the status-derived one: 403 means quota
		// exhaustion elsewhere in this gateway, and telling an operator their
		// key "ran out of quota" when the real answer is "this route is not
		// yours" sends them to the wrong fix.
		handlerutil.WriteJSONErrorCode(w, http.StatusForbidden, "forbidden", "Operator credentials required")
		return true
	}
	return false
}

func RequireDashboardSessionAuth(repo *db.Repo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.SessionValid(r) || auth.ValidCLIToken(r.Header.Get(auth.CLITokenHeader)) {
				next.ServeHTTP(w, r)
				return
			}
			if dashboardEngineCredential(w, r, repo, false) {
				return
			}
			if !auth.RequireLogin(repo) {
				next.ServeHTTP(w, r)
				return
			}
			handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Unauthorized: dashboard session required")
		})
	}
}

// RequireConsoleLogAuth gates console-log APIs to dashboard sessions or the
// local CLI token. Engine client API keys never grant access to operational
// logs, and requireLogin=false still allows an unauthenticated local dashboard.
func RequireConsoleLogAuth(repo *db.Repo) func(http.Handler) http.Handler {
	return RequireDashboardSessionAuth(repo)
}

// CallerIsDashboardPrivileged reports whether the request should be served
// secret material, as opposed to an engine client key.
//
// The dashboard gate admits three kinds of caller: a valid session cookie, the
// local CLI token, and — when requireLogin is off — anything at all. The first
// two are operator credentials. The third is not a credential, but it is the
// default local install reaching its own dashboard over loopback and the only
// way that install could manage keys at all; excluding it would show an
// operator with login disabled a table of masks and no way to reveal one.
// An engine client key is the single excluded caller: it is handed to a client
// app, and F-6 existed so it could not dump the rest of the key set.
func CallerIsDashboardPrivileged(repo *db.Repo, r *http.Request) bool {
	if auth.SessionValid(r) || auth.ValidCLIToken(r.Header.Get(auth.CLITokenHeader)) {
		return true
	}
	return ExtractApiKey(r) == "" && !auth.RequireLogin(repo)
}

// RequireAdminAuth ensures that only requests with a valid dashboard session
// (auth_token cookie) or local CLI token (x-9r-cli-token) can proceed. Client
// API keys are rejected.
//
// The password header is admitted on exactly one path — the backup download —
// whose handler re-verifies it against the stored hash before exporting.
// Without that exception the re-check was unreachable over HTTP, so the step-up
// path existed only in tests mounting the handler directly, and an operator
// scripting a backup had no credential that worked from outside the process.
// The scoping is load-bearing: this gate fronts /api/version/shutdown,
// /api/version/update and /admin/health/reset, whose handlers verify no
// credential of their own, so accepting the header by presence there turned an
// arbitrary header into remote shutdown, remote self-update, and a health reset.
func RequireAdminAuth(repos ...*db.Repo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.SessionValid(r) ||
				auth.ValidCLIToken(r.Header.Get(auth.CLITokenHeader)) ||
				allowsPasswordHeader(r.URL.Path) && r.Header.Get(DashboardPasswordHeader) != "" {
				next.ServeHTTP(w, r)
				return
			}
			if len(repos) > 0 && dashboardEngineCredential(w, r, repos[0], false) {
				return
			}
			handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Unauthorized: admin session or CLI token required")
		})
	}
}

func RequireDashboardAuth(repo *db.Repo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.SessionValid(r) || auth.ValidCLIToken(r.Header.Get(auth.CLITokenHeader)) {
				next.ServeHTTP(w, r)
				return
			}
			keyPresented := ExtractApiKey(r) != ""
			if dashboardEngineCredential(w, r, repo, !IsAlwaysProtectedPath(r.URL.Path) && engineDashboardReadAllowed(r.Method, r.URL.Path)) {
				return
			}
			if keyPresented {
				next.ServeHTTP(w, r)
				return
			}
			if IsAlwaysProtectedPath(r.URL.Path) {
				if allowsPasswordHeader(r.URL.Path) && r.Header.Get(DashboardPasswordHeader) != "" {
					next.ServeHTTP(w, r)
					return
				}
				if storageMaintenancePaths[r.URL.Path] && !auth.RequireLogin(repo) {
					next.ServeHTTP(w, r)
					return
				}
				handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Unauthorized: admin session or CLI token required")
				return
			}
			if !auth.RequireLogin(repo) {
				next.ServeHTTP(w, r)
				return
			}
			handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Unauthorized: dashboard session required")
		})
	}
}

// RequireDashboardPage redirects browser navigations to /login when login is
// required and the session cookie is missing, mirroring upstream's
// dashboardGuard redirect for /dashboard routes. A tunnel/tailscale host with
// dashboard access disabled is also bounced to /login (upstream parity).
func RequireDashboardPage(repo *db.Repo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.RequireLogin(repo) && tunnelPageBlocked(repo, r) {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			if !auth.RequireLogin(repo) || auth.SessionValid(r) {
				next.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
		})
	}
}

// tunnelPageBlocked reports whether the navigation arrives via a
// tunnel/tailscale hostname whose dashboard access is disabled.
func tunnelPageBlocked(repo *db.Repo, r *http.Request) bool {
	raw, err := repo.GetSettingsRaw()
	if err != nil || raw == nil {
		return false
	}
	return auth.TunnelLoginBlocked(r, raw)
}
