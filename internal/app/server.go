// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package app

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/fx"

	"9routerx/proxy/internal/a2a"
	"9routerx/proxy/internal/config"
	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/freeresilience"
	"9routerx/proxy/internal/handlers/chat"
	appLog "9routerx/proxy/internal/log"
	"9routerx/proxy/internal/mcpserver"
	"9routerx/proxy/internal/notifications"
	"9routerx/proxy/internal/providers"
	"9routerx/proxy/internal/proxy"
	"9routerx/proxy/internal/proxy/executor"
	"9routerx/proxy/internal/proxy/oauth"
	"9routerx/proxy/internal/schedule"
	"9routerx/proxy/internal/shutdown"
	"9routerx/proxy/internal/tlsstealth"
	"9routerx/proxy/internal/updater"
	"9routerx/proxy/internal/uptime"
	"9routerx/proxy/internal/webexec"
	"9routerx/proxy/internal/webhooks"
)

// ServerModule provides *http.Server and manages its lifecycle and background tasks.
var ServerModule = fx.Module("server",
	fx.Provide(
		ProvideServer,
	),
	fx.Invoke(
		func(*http.Server) {},
	),
)

// ServerParams defines inputs for constructing the HTTP server and lifecycle hooks.
type ServerParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Config    *config.Config
	Repo      *db.Repo
	Handler   http.Handler
	CLIParams CLIParams
	// ChatHandler is the engine's chat handler. The auto-ping scheduler sends
	// its pings through it, so a ping uses the same credential resolution and
	// the same provider executors as real traffic. It is optional: a server
	// built without one (the tests) simply runs no ping loop.
	//
	// It is the SAME instance the routes are mounted on — the one
	// handlers.SetupServerRouter returns — not a second one built here. A second
	// chat handler is a second PersistentStore over the same SQLite file with
	// its own in-memory LRU, so the dashboard would report zero entries while
	// the engine served hits.
	ChatHandler *chat.ChatHandler `optional:"true"`
}

// Server timeouts are fixed rather than configurable on purpose: the values
// below bound how long one client can hold one connection, so making them
// tunable would put a Slowloris knob in reach of anyone who edits .env.
const (
	// serverReadHeaderTimeout caps how long a client may take to finish
	// sending request headers. Same value as the OAuth callback listener in
	// internal/proxy/oauth/codex_proxy.go.
	serverReadHeaderTimeout = 10 * time.Second
	// serverIdleTimeout closes keep-alive connections with no request in
	// flight. It does not apply while a request is being served, so SSE and
	// WebSocket streams run to their natural end.
	serverIdleTimeout = 120 * time.Second
	// serverMaxHeaderBytes states the header limit the gateway intends.
	// It does not close a hole: net/http falls back to
	// http.DefaultMaxHeaderBytes (1 MiB) when this is zero, so the enforced
	// value is the same either way. Pinning it keeps the limit a decision
	// here rather than a stdlib default this repo never reviewed.
	serverMaxHeaderBytes = 1 << 20
)

// ProvideServer creates *http.Server and registers lifecycle hooks.
func ProvideServer(p ServerParams) *http.Server {
	host := strings.TrimSpace(p.Config.Host)
	if host == "" {
		host = "127.0.0.1"
	} else if host == "*" {
		host = "0.0.0.0"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(p.Config.Port))
	server := &http.Server{
		Addr:    addr,
		Handler: p.Handler,
		// WriteTimeout stays unset on purpose: internal/proxy/stall.go lets an
		// SSE stream idle for DefaultStallTimeout (6 minutes), and a write
		// deadline would sever those streams mid-response.
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}

	var notifScanner *notifications.ConditionScanner

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
				appLog.Warn("server", "listening on all interfaces; use a strong dashboard password and authenticated tunnels", "addr", addr)
			}
			autoUpdate := p.CLIParams.AutoUpdate
			if !autoUpdate && p.Repo != nil {
				if settings, sErr := p.Repo.GetSettings(); sErr == nil && settings != nil {
					autoUpdate = settings.AutoUpdate
					// The "Check updates" toggle governs the banner + freshness
					// ticker, not the auto-apply flow above. Default ON, so the
					// flag only matters when the operator wrote an explicit
					// false (internal/db/settings_parse.go).
					updater.SetCheckEnabled(settings.UpdateCheckEnabled)
				}
			}
			updater.StartBackgroundCheck(shutdown.Context(), autoUpdate)
			updater.StartCheckLoop(shutdown.Context())
			log.Printf("[config] auto-update enabled=%v check-updates=%v", autoUpdate, updater.IsCheckEnabled())

			catalogPath := filepath.Join(filepath.Dir(p.Config.DatabasePath), "model-catalog.json")
			providers.StartBackgroundCatalogSync(shutdown.Context(), nil, catalogPath)
			oauth.StartBackgroundRefresh(shutdown.Context(), p.Repo)
			db.StartRetentionLoop(shutdown.Context(), p.Repo, db.DefaultRequestDetailRetention)
			// FASE 10.6: the compliance audit log's retention job. Daily ticker,
			// 180-day default window — the same fire-and-forget shape as the
			// diagnostics prune above.
			db.StartAuditLogRetention(shutdown.Context(), p.Repo, db.DefaultAuditLogRetentionDays)
			// The Gemini Web session cookie is renewed by the site every few
			// minutes, and the renewed value is what keeps a signed-in
			// account reaching the models it pays for. Without this loop the
			// credential has to be pasted again every few minutes.
			executor.StartGeminiWebRotation(shutdown.Context(), p.Repo)
			startAutoPingScheduler(shutdown.Context(), p.Repo, p.ChatHandler)
			// Fase 5 (port(serenhope)): web-session executors are grey-zone
			// ToS surface and ship OFF. One switch re-enables them; see
			// executor.CheckWebExecutorAllowed for the per-request gate.
			executor.SetWebExecutorsEnabled(p.Config.EnableWebExecutors)
			// FASE 10.1 free-tier tracking: seed the catalog budgets the
			// tracking needs (INSERT OR IGNORE, so operator overrides
			// survive) and roll each provider's counter over on its own
			// reset day. Seeding failure is logged, not fatal: the gateway
			// serves traffic without the counters, the usage ledger is the
			// source of truth.
			if p.Repo != nil {
				if err := p.Repo.SeedFreeTierCatalogBudgets(); err != nil {
					log.Printf("[freetier] seeding catalog budgets failed: %v", err)
				}
				db.StartFreeTierResetLoop(shutdown.Context(), p.Repo)
				// Free-tier resilience suite: the recovery scheduler expires
				// cooldowns whose reset passed (the provider rejoins rotation
				// without a restart) and flushes the health outcome rings to
				// SQLite. The tracker lives on the engine's chat handler —
				// the instance the request path actually uses.
				if p.ChatHandler != nil && p.ChatHandler.FreeResilience != nil {
					freeresilience.StartScheduler(shutdown.Context(), p.ChatHandler.FreeResilience, freeresilience.DefaultSchedulerInterval)
				}
			}
			// FASE 10.0: the web-executor session pool's health job. It
			// publishes itself, so the dashboard's /api/webexec endpoints
			// report on the same instance without threading it through the
			// router constructors.
			startWebExecHealthJob(shutdown.Context(), p.Repo)
			// FASE 10.2 A2A + FASE 10.7 TLS stealth flags. Both ship OFF:
			// the A2A surface refuses with 503 until ROUTERX_ENABLE_A2A,
			// and the stealth lane keeps its plain client until
			// ROUTERX_TLS_STEALTH.
			a2a.SetEnabled(p.Config.EnableA2A)
			if p.Config.EnableA2A && strings.TrimSpace(os.Getenv("ROUTERX_A2A_TOKEN")) == "" {
				appLog.Warn("a2a", "A2A token not configured; enabled requests are refused")
			}
			tlsstealth.SetEnabled(p.Config.EnableTLSStealth)
			if p.Config.EnableTLSStealth {
				// The daily compliance alert is not optional while the
				// flag is on: one notification-feed entry (day-scoped
				// dedupe key) plus one webhook fan-out per 24h. The
				// dispatcher is built per dispatch; it owns its own
				// timeouts and drops a failed target silently.
				dispatcher := webhooks.NewDispatcher(p.Repo)
				tlsstealth.Default.StartAlertLoop(shutdown.Context(), p.Repo,
					func(eventType, title, body, severity string) {
						dispatcher.DispatchAll(shutdown.Context(), webhooks.Event{
							Type: eventType, Title: title, Body: body, Severity: severity,
						})
					})
			}
			// Fase 3: dispatching chat traffic to private/loopback provider
			// endpoints ships OFF and turns on only by operator opt-in in
			// the settings row; see proxy.AssertDispatchURLAllowed for the
			// per-request gate. Read once here like the web-executor
			// switch: the guard is a security posture, not a live knob.
			if s, sErr := p.Repo.GetSettings(); sErr == nil && s != nil {
				proxy.SetAllowPrivateProviderURLs(s.AllowPrivateProviderURLs)
			}
			startModelHealthScheduler(shutdown.Context(), p.Repo, p.ChatHandler)
			startUptimeHeartbeat(shutdown.Context(), p.Repo)

			notifScanner = notifications.NewScanner(p.Repo)
			notifScanner.Start()
			// MCP server (stdio for IDE integrations). The HTTP endpoint is
			// mounted on the router above; the stdio lane serves tools to
			// Claude Desktop / Cursor processes.
			mcpSrv := mcpserver.NewMCPServer(p.Repo)
			mcpSrv.RunStdio(shutdown.Context())

			log.Printf("9RouterX Proxy (%s) starting on port %d", updater.CurrentVersion, p.Config.Port)

			go func() {
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Fatalf("Server failed: %v", err)
				}
			}()

			fmt.Fprintf(os.Stdout, "\n  🚀 9RouterX Proxy (%s) on %s\n\n", updater.CurrentVersion, addr)
			log.Printf("Server is ready to handle requests at %s", addr)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			fmt.Fprintln(os.Stdout, "\n  Shutting down...")
			notifScanner.Stop()

			// Signal in-flight SSE streams to end promptly: the stall reader closes each
			// upstream body, handlers emit a final [DONE], and Shutdown completes well
			// within its deadline instead of waiting out the full timeout.
			shutdown.Cancel()

			// Refuse NEW keep-alive reuse immediately: idle browser/dashboard
			// connections otherwise hold Shutdown for the full deadline even
			// when zero requests are in flight (the "shutdown takes forever"
			// complaint). In-flight requests still drain gracefully below.
			server.SetKeepAlivesEnabled(false)

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Printf("Server shutdown did not complete in time: %v", err)
			} else {
				log.Println("Server stopped gracefully")
			}
			// Responses are all done by now, so every usage write they enqueued
			// is in the queue: drain it so the last turns are not billed into
			// nothing by the exit.
			if !chat.FlushUsageWrites(3 * time.Second) {
				log.Printf("Usage write queue did not fully drain before exit")
			}
			return nil
		},
	})

	return server
}

// startWebExecHealthJob boots the FASE 10.0 web-executor session pool: the
// per-account pool with sliding-window rate limiting and idle reclamation,
// plus the 15-minute health pass that validates every stored session of every
// enabled web executor and raises a deduped warning when one has expired.
// The validation is injected; nil selects the stored-session presence check,
// which exercises the shared infrastructure until an executor registers its
// real per-provider probe.
func startWebExecHealthJob(ctx context.Context, repo *db.Repo) {
	if repo == nil {
		return
	}
	webexec.StartHealthJob(ctx, repo, nil)
	log.Printf("[webexec] health job started (interval %s)", webexec.HealthInterval)
}

// startAutoPingScheduler starts the quota auto-ping loop.
//
// The loop ticks once a minute and pings the connections whose anchored slot
// has come due, so a provider's 5h window starts on time instead of at the
// first real request. Upstream runs the same loop
// (src/shared/services/quotaAutoPing.js:318-322, started from
// initializeApp.js:112).
//
// The configuration is read from the settings row on EVERY tick, never
// cached, so a schedule saved in the dashboard takes effect on the next tick
// without a restart.
//
// The loop is a no-op without a chat handler or a repository: there is
// nothing to ping without a connection to ping. It is bound to the shutdown
// context, so it stops with the other background loops.
func startAutoPingScheduler(ctx context.Context, repo *db.Repo, chatH *chat.ChatHandler) {
	if repo == nil || chatH == nil {
		return
	}

	sched := schedule.NewScheduler(
		schedule.ConfigSourceFromSettings(func() (map[string]any, error) {
			return repo.GetSettingsRaw()
		}),
		schedule.PingFuncFromChat(chatH),
	)

	go sched.Run(ctx)
	log.Println("[autoping] scheduler started")
}

// startUptimeHeartbeat starts the Uptime Kuma push loop.
//
// The loop ticks on the operator's configured interval and pushes a heartbeat
// to the push URL saved in settings, so a monitor learns the gateway is down
// from the missing heartbeat rather than an operator learning it from a CLI
// tool that stopped answering.
//
// The configuration is read from the settings row on EVERY tick, never cached,
// so a URL saved in the dashboard takes effect on the next heartbeat without a
// restart. A tick with no configuration is a no-op rather than an error: an
// operator who never set a push URL must not produce a failure log a minute.
//
// The loop is bound to the shutdown context, so it stops with the other
// background loops. A failed push is logged and dropped — the monitor marks
// the gateway down when the next push does not arrive either, and retrying
// inside the tick would stall the loop on a monitor that is already gone.
func startUptimeHeartbeat(ctx context.Context, repo *db.Repo) {
	if repo == nil {
		return
	}
	loop := uptime.NewLoop(
		func() uptime.Config {
			s, err := repo.GetSettings()
			if err != nil || s == nil {
				return uptime.Config{}
			}
			return uptime.Config{
				Enabled:         s.UptimeKumaEnabled,
				PushURL:         s.UptimeKumaPushURL,
				IntervalSeconds: s.UptimeKumaInterval,
			}
		},
		func() uptime.Pusher {
			s, err := repo.GetSettings()
			if err != nil || s == nil {
				return nil
			}
			return &uptime.HTTPPusher{URL: s.UptimeKumaPushURL}
		},
	)
	go loop.Run(ctx)
	log.Println("[uptime] heartbeat loop started")
}
