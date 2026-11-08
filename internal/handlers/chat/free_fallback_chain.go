// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	json "9routerx/proxy/internal/fastjson"
	"9routerx/proxy/internal/freeresilience"
	"9routerx/proxy/internal/log"
	"9routerx/proxy/internal/providers"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Free-provider fallback chain (task B.1).
//
// When a request routed to a FREE provider fails with a free-tier refusal —
// HTTP 429, a 403 FreeTierError shape, or a body naming quota / abuse / rate
// limit — the router retries the request against the next healthy free
// provider that serves the same model, instead of answering the client with
// the refusal. The chain never silently reroutes onto a paid provider: paid
// candidates only run after every free candidate failed, and the response
// then carries x-9routerx-fallback-paid so the operator can see it happened.
//
// The chain runs at the single-model dispatch level, after the failed
// provider's own account rotation (handleAccountFallback) is exhausted: that
// layer already tried the provider's other accounts, so what is left is a
// provider-level condition, which is exactly what another provider can fix.
// Combo requests already fall back across their own member list and are not
// touched here; pinned connections are honoured, not overridden.

const (
	// HeaderFallbackChain names the providers one request walked through, in
	// order, arrow-separated: "groq→nvidia". Set before the first chain
	// attempt and extended as the walk proceeds, so a success carries the
	// full path and a failure carries how far it got.
	HeaderFallbackChain = "x-9routerx-fallback-chain"
	// HeaderFallbackPaid marks a response that was served by a PAID provider
	// after every free candidate failed. The operator pays for that turn;
	// the marking is what keeps it from happening silently.
	HeaderFallbackPaid = "x-9routerx-fallback-paid"
)

// isFreeRoutableProvider reports whether the provider id (or alias) is one
// the free-tier suite governs: it has catalogued free models, or its registry
// entry files it under a free section. Paid providers answer false — the
// chain never re-routes them, so their routing behaviour is unchanged.
func isFreeRoutableProvider(provider string) bool {
	if provider == "" {
		return false
	}
	if providers.ProviderHasFreeModels(provider) {
		return true
	}
	category, _, _ := providers.ClassifyProvider(providers.ResolveAlias(provider))
	return category == providers.CategoryFree || category == providers.CategoryFreeTier
}

// providerServesModel reports whether the provider can plausibly serve the
// model id: it is catalogued free there, or the provider's model list names
// it. Both checks are registry/catalog reads — no network, no DB.
func providerServesModel(provider, model string) bool {
	if model == "" {
		return false
	}
	if providers.IsFreeModel(provider, providers.FreeModelCandidate{ID: model}) {
		return true
	}
	for _, listed := range providers.GetProviderModels(provider) {
		if listed == model {
			return true
		}
	}
	return false
}

// freeFallbackCandidates lists the healthy free providers other than the
// failed one that serve the model, in chain order: the operator's configured
// order first, then health score descending. Providers in their cooldown
// window are skipped, not deleted — the moment their reset passes (or a
// success lifts it early) they are candidates again, with no restart.
func (h *ChatHandler) freeFallbackCandidates(failedProvider, model string, exclude map[string]bool) []string {
	failedProvider = providers.ResolveAlias(failedProvider)
	exclude[failedProvider] = true

	var eligible []string
	seen := map[string]bool{}
	addEligible := func(pid string) {
		if pid == "" || seen[pid] || exclude[pid] {
			return
		}
		seen[pid] = true
		if !providerServesModel(pid, model) {
			return
		}
		if cooling, _ := h.FreeResilience.IsCooling(pid); cooling {
			return
		}
		eligible = append(eligible, pid)
	}
	// Catalogued free providers first...
	for _, pid := range providers.FreeCatalogProviders() {
		addEligible(pid)
	}
	// ...then registry-free providers outside the catalogue (keyless web
	// tiers and friends), which can serve a model by list membership alone.
	for pid := range providers.KnownProviders {
		if category, _, _ := providers.ClassifyProvider(pid); category == providers.CategoryFree || category == providers.CategoryFreeTier {
			addEligible(pid)
		}
	}
	return freeresilience.OrderCandidates(h.FreeResilience.FallbackConfig().Order, eligible, h.FreeResilience.HealthScore)
}

// paidFallbackCandidates lists paid providers that serve the model — the last
// resort after every free candidate failed. Excludes free providers (already
// tried or skipped) and the failed provider.
func (h *ChatHandler) paidFallbackCandidates(failedProvider, model string, exclude map[string]bool) []string {
	failedProvider = providers.ResolveAlias(failedProvider)
	var out []string
	for pid := range providers.KnownProviders {
		if pid == failedProvider || exclude[pid] {
			continue
		}
		if isFreeRoutableProvider(pid) {
			continue
		}
		if !providerServesModel(pid, model) {
			continue
		}
		out = append(out, pid)
	}
	// Deterministic order: the registry map's iteration order is random, and
	// a chain that differs run to run would make the header (and this suite)
	// unexplainable. Alphabetical: stable, and the health ordering that
	// matters only applies to free candidates.
	sort.Strings(out)
	return out
}

// upstreamErrorParts pulls the status code and body out of a failed
// attempt's error for the classifier.
func upstreamErrorParts(err error) (int, []byte) {
	var ue *upstreamError
	if !errors.As(err, &ue) {
		return 0, nil
	}
	return ue.StatusCode, ue.Body
}

// runFreeFallbackChain walks the free fallback chain for a request whose
// primary provider failed. body is the PRISTINE client request (pre model
// rewrite, pre jailbreak), so each candidate gets its own rewrite and its own
// injection. Returns nil when a candidate served the response (headers are
// already on the writer); otherwise it returns origErr unchanged — the client
// hears about the provider it actually asked for, with the chain's progress
// in the log and in x-9routerx-fallback-chain.
func (h *ChatHandler) runFreeFallbackChain(
	ctx context.Context,
	cw *committedResponseWriter,
	body []byte,
	failedProvider, model, requestedModel string,
	isStream, translateResponse bool,
	endpoint string,
	origErr error,
) error {
	if h.FreeResilience == nil || !h.FreeResilience.FallbackEnabled() {
		return origErr
	}
	// Paid providers are never re-routed by the free chain: their routing
	// behaviour is unchanged by this feature.
	if !isFreeRoutableProvider(failedProvider) {
		return origErr
	}
	statusCode, errBody := upstreamErrorParts(origErr)
	if freeresilience.ClassifyFreeTierError(statusCode, errBody) == freeresilience.KindNone {
		// A 400/401/404 is a config or request bug: it must stay visible,
		// not vanish into a retry somewhere else.
		return origErr
	}

	cfg := h.FreeResilience.FallbackConfig()
	exclude := map[string]bool{}
	candidates := h.freeFallbackCandidates(failedProvider, model, exclude)
	chain := []string{providers.ResolveAlias(failedProvider)}
	log.Info("fallback", "free chain start",
		"failed", failedProvider, "model", model, "candidates", strings.Join(candidates, ","), "max_attempts", cfg.ClampMaxAttempts())

	attempts := 0
	tryCandidate := func(cand string, paid bool) bool {
		if cw.IsCommitted() {
			// A previous attempt already streamed headers to the client;
			// there is nothing left to fall back into.
			return false
		}
		attempts++
		chain = append(chain, cand)
		cw.Header().Set(HeaderFallbackChain, strings.Join(chain, "→"))
		if paid {
			cw.Header().Set(HeaderFallbackPaid, cand)
		}
		// Pristine body per candidate: the client's model id back, rewritten
		// for this provider's wire, jailbreak resolved for this leg.
		var attemptBody map[string]any
		if err := json.Unmarshal(body, &attemptBody); err != nil {
			return false
		}
		attemptBody["model"] = h.wireModelForNode(cand, model)
		repairToolCallIDsInMap(attemptBody)
		attemptCtx := h.applyJailbreak(ctx, attemptBody, requestedModel, jailbreakLegIDs(cand, model)...)
		upstreamJSON, err := json.Marshal(attemptBody)
		if err != nil {
			return false
		}
		log.Info("fallback", "free chain attempt",
			"from", failedProvider, "to", cand, "model", model, "paid", paid, "attempt", attempts)
		return h.handleAccountFallback(attemptCtx, cw, cand, model, "", upstreamJSON, isStream, translateResponse, endpoint) == nil
	}

	for _, cand := range candidates {
		if attempts >= cfg.ClampMaxAttempts() {
			break
		}
		if tryCandidate(cand, false) {
			log.Info("fallback", "free chain served", "chain", strings.Join(chain, "→"), "model", model)
			return nil
		}
	}

	// Every free candidate failed. The paid lane is the last resort, and it
	// must be visible: the header marks exactly which paid provider served.
	for _, cand := range h.paidFallbackCandidates(failedProvider, model, exclude) {
		if attempts >= cfg.ClampMaxAttempts() {
			break
		}
		if tryCandidate(cand, true) {
			log.Warn("fallback", "free chain exhausted, paid provider served",
				"chain", strings.Join(chain, "→"), "model", model, "paid", cand)
			return nil
		}
	}

	if len(chain) > 1 {
		log.Warn("fallback", "free chain exhausted, reporting original failure",
			"chain", strings.Join(chain, "→"), "model", model)
	}
	return origErr
}

// recordFreeResilienceOutcome funnels every free-provider attempt's result
// into the resilience tracker: health counters for every outcome, a cooldown
// event when the classifier calls the failure a free-tier limit, and the
// early-recovery clear when a success arrives during a cooldown. Paid
// providers are not tracked — the suite is free-tier scoped.
func (h *ChatHandler) recordFreeResilienceOutcome(provider, model string, err error, latencyMs int64) {
	if h == nil || h.FreeResilience == nil || provider == "" {
		return
	}
	if !isFreeRoutableProvider(provider) {
		return
	}
	status, body := upstreamErrorParts(err)
	if err != nil && status == 0 {
		status = 500
	}
	if err == nil {
		status = 200
	}
	var header http.Header
	var ue *upstreamError
	if errors.As(err, &ue) {
		header = ue.Header
	}
	h.FreeResilience.RecordOutcome(providers.ResolveAlias(provider), model, status, body, latencyMs, header)
}

// freeChainArmed reports whether the chain could run for a provider, so the
// dispatch paths only pay for a pristine body copy when the chain may
// actually walk it.
func (h *ChatHandler) freeChainArmed(provider string) bool {
	return h != nil && h.FreeResilience != nil && h.FreeResilience.FallbackEnabled() && isFreeRoutableProvider(provider)
}

// deepCopyJSONMap clones a parsed request body through a JSON round trip, so
// a chain attempt's rewrite and injection cannot leak into the next
// candidate's body.
func deepCopyJSONMap(src map[string]any) (map[string]any, error) {
	if src == nil {
		return nil, nil
	}
	raw, err := json.Marshal(src)
	if err != nil {
		return nil, fmt.Errorf("copy request body: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("copy request body: %w", err)
	}
	return out, nil
}
