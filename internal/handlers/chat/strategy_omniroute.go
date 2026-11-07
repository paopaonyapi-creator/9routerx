// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"crypto/rand"
	"math"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"9routerx/proxy/internal/models"
	"9routerx/proxy/internal/pricing"
	"9routerx/proxy/internal/resilience"
	"9routerx/proxy/internal/routing"
)

// strategy_omniroute.go wires the four omniroute routing strategies into the
// combo dispatch: cache-optimized (prompt-prefix stickiness via rendezvous
// hashing), auto (the 5-factor score), quota-weighted (weighted random over
// remaining quota) and reset-aware (window-pressure ordering). The strategy
// math lives in internal/routing as pure functions over caller-supplied
// telemetry; this file is the adapter feeding them what the handler can
// actually observe — the tokenmaxxing quota snapshot, the resilience breaker
// states, the requestDetails latency ring and the pricing tables — through
// 60-second TTL caches shaped like the tokenmaxxing one.
//
// Degradation contract: telemetry a model does not expose is neutral, never
// invented. A model without quota data is not penalized by the quota factor;
// one without latency history is not penalized by the latency factor. A
// strategy whose inputs are all neutral returns the candidate list unchanged
// (deterministically), so a combo over providers with no telemetry behaves
// exactly like the fallback strategy it used to be.

const (
	strategyCacheTTL = 60 * time.Second
	strategyCacheMax = 500

	// quotaScoreNormalize converts the tokenmaxxing snapshot (a percentage)
	// into the [0,1] unit the routing strategies score in.
	quotaScoreNormalize = 100.0

	// The omniroute window lengths the reset-aware score assumes when a
	// provider publishes no reset instant: the pressure term reads a full
	// window of remaining time as zero urgency, so those providers are scored
	// on remaining alone instead of invented evidence.
	strategySessionWindow = 5 * time.Hour
	strategyWeeklyWindow  = 7 * 24 * time.Hour
)

// strategyLatencyEntry is one cached latency mean for a provider/model pair.
type strategyLatencyEntry struct {
	meanMs    float64
	fetchedAt time.Time
}

var (
	strategyLatencyMu    sync.RWMutex
	strategyLatencyCache = make(map[string]strategyLatencyEntry)
)

// strategyLatencyForModels resolves one mean-latency number per model from the
// requestDetails ring, one query for the whole candidate set (the read is the
// same one the analytics perf feed pays). Models absent from the ring get no
// entry — the caller treats that as neutral.
func (h *ChatHandler) strategyLatencyForModels(models []string) map[string]float64 {
	out := make(map[string]float64, len(models))
	now := time.Now()

	strategyLatencyMu.RLock()
	allFresh := true
	for _, m := range models {
		entry, ok := strategyLatencyCache[m]
		if !ok || now.Sub(entry.fetchedAt) >= strategyCacheTTL {
			allFresh = false
			break
		}
		out[m] = entry.meanMs
	}
	strategyLatencyMu.RUnlock()
	if allFresh {
		return out
	}

	if h.Repo == nil {
		return out
	}
	aggs, err := h.Repo.GetModelLatency()
	if err != nil {
		return out
	}
	strategyLatencyMu.Lock()
	defer strategyLatencyMu.Unlock()
	for _, agg := range aggs {
		key := agg.Provider + "/" + agg.Model
		strategyLatencyCache[key] = strategyLatencyEntry{
			meanMs:    float64(agg.TotalMs),
			fetchedAt: now,
		}
		if len(strategyLatencyCache) > strategyCacheMax {
			for k := range strategyLatencyCache {
				delete(strategyLatencyCache, k)
				break
			}
		}
		out[key] = float64(agg.TotalMs)
	}
	return out
}

// strategyBreakerState resolves the breaker state for one model's provider.
// An unknown provider (no prefix, or an alias the resolver refuses) counts
// healthy: the breaker only downgrades a provider on evidence.
func (h *ChatHandler) strategyBreakerState(model string) string {
	prefix, _, ok := strings.Cut(model, "/")
	if !ok || h.Resilience == nil {
		return ""
	}
	return string(h.Resilience.BreakerStatus(prefix).State)
}

// strategyCostPer1M resolves one model's blended per-million-token price.
// Unpriced models return 0 — the pool normalization treats a fully unpriced
// pool as neutral rather than penalizing anyone.
func strategyCostPer1M(provider, model string) float64 {
	return pricing.EstimateCost(provider, model, pricing.TokenCounts{
		PromptTokens:     500_000,
		CompletionTokens: 500_000,
	})
}

// strategyRand01 returns one uniform sample in [0,1) from the crypto source,
// so the weighted draws are never scheduler-seeded.
func strategyRand01() float64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err != nil {
		return 0
	}
	return float64(n.Int64()) / float64(int64(1)<<53)
}

// strategyQuotaRemaining resolves one canonical provider's remaining-quota
// fraction in [0,1] through the tokenmaxxing snapshot (its own TTL cache
// applies). ok=false means the provider exposes no quota telemetry.
func (h *ChatHandler) strategyQuotaRemaining(ctx context.Context, canonicalProvider string) (float64, bool) {
	allTableConns := make([]*models.ProviderConnection, 0)
	fetched := false
	score := h.resolveProviderTokenmaxxingScore(ctx, canonicalProvider, time.Now(), &allTableConns, &fetched)
	if score == nil {
		return 0, false
	}
	return clampStrategyUnit(*score / quotaScoreNormalize), true
}

// strategyScored is one candidate with its computed auto score.
type strategyScored struct {
	model string
	score float64
}

// applyCacheOptimizedRouting orders the combo's models by rendezvous weight
// for the request's prompt cache key: the same prompt prefix always tries the
// same model first, which is what keeps the upstream prompt cache warm. A
// request carrying an explicit prompt_cache_key pins harder (verbatim key); a
// request with no reusable prefix leaves the order untouched — there is
// nothing for the stickiness to stick to.
func (h *ChatHandler) applyCacheOptimizedRouting(body []byte, models []string) []string {
	if len(models) <= 1 {
		return models
	}
	key := routing.PromptCacheKey(body)
	if key == "" {
		return models
	}
	return routing.OrderByRendezvous(key, models)
}

// applyAutoRouting scores every model with the 5-factor weighted score and
// sorts descending. Factors absent for a model fold to their neutral value
// inside routing.ScoreCandidate, so a pool mixing telemetry-rich and
// telemetry-blind providers still orders deterministically.
func (h *ChatHandler) applyAutoRouting(ctx context.Context, body []byte, models []string) []string {
	if len(models) <= 1 {
		return models
	}
	cacheKey := routing.PromptCacheKey(body)
	sticky := ""
	if cacheKey != "" {
		sticky = routing.PickByRendezvous(cacheKey, models)
	}
	latency := h.strategyLatencyForModels(models)

	// Pool-wide cost normalization: the priciest candidate defines the scale,
	// so a pool of unpriced models scores the cost factor neutral (1.0) for
	// everyone rather than inventing a ranking.
	maxCost := 0.0
	costs := make(map[string]float64, len(models))
	for _, m := range models {
		prefix, _, ok := strings.Cut(m, "/")
		cost := 0.0
		if ok {
			cost = strategyCostPer1M(prefix, strings.TrimPrefix(m, prefix+"/"))
		}
		costs[m] = cost
		if cost > maxCost {
			maxCost = cost
		}
	}

	scoredModels := make([]strategyScored, 0, len(models))
	for _, m := range models {
		prefix, _, ok := strings.Cut(m, "/")
		canonical := ""
		if ok {
			canonical = resolveProviderAlias(prefix)
		}
		quotaRemaining, quotaTotal := 0.0, 0.0
		if score, has := h.strategyQuotaRemaining(ctx, canonical); has {
			quotaRemaining, quotaTotal = score, 1.0
		}
		in := routing.ScoreInputs{
			BreakerState:     h.strategyBreakerState(m),
			QuotaRemaining:   quotaRemaining,
			QuotaTotal:       quotaTotal,
			CostPer1M:        costs[m],
			MaxCostPer1M:     maxCost,
			P95LatencyMs:     latency[m],
			MaxLatencyMs:     poolMax(latency, models),
			StickyAccount:    sticky,
			CandidateAccount: m,
		}
		scoredModels = append(scoredModels, strategyScored{model: m, score: routing.ScoreCandidate(in)})
	}

	sort.SliceStable(scoredModels, func(i, j int) bool {
		return scoredModels[i].score > scoredModels[j].score
	})
	out := make([]string, 0, len(scoredModels))
	for _, s := range scoredModels {
		out = append(out, s.model)
	}
	return out
}

// applyQuotaWeightedRouting orders models by weighted random draw over their
// remaining quota: the tokenmaxxing snapshot supplies each provider's remaining
// percentage, routing.QuotaWeightedPick prefers the above-floor pool and draws
// once; the winner moves to the front and the rest keep their relative order.
func (h *ChatHandler) applyQuotaWeightedRouting(ctx context.Context, models []string) []string {
	if len(models) <= 1 {
		return models
	}
	candidates := make([]routing.QuotaCandidate, 0, len(models))
	for _, m := range models {
		prefix, _, ok := strings.Cut(m, "/")
		remaining := 0.0
		if ok {
			if score, has := h.strategyQuotaRemaining(ctx, resolveProviderAlias(prefix)); has {
				remaining = score
			}
		}
		candidates = append(candidates, routing.QuotaCandidate{
			RemainingPct: remaining,
			FloorPct:     0.01,
		})
	}
	winner := routing.QuotaWeightedPick(candidates, make([]int, len(candidates)), strategyRand01())
	if winner < 0 || winner >= len(models) {
		return models
	}
	out := make([]string, 0, len(models))
	out = append(out, models[winner])
	out = append(out, models[:winner]...)
	out = append(out, models[winner+1:]...)
	return out
}

// applyResetAwareRouting orders models by the omniroute window score. The
// tokenmaxxing snapshot has one remaining number per provider and no reset
// instant, so both windows are fed the same remaining with the full window as
// the reset distance — providers that publish no window are scored on
// remaining alone, which is the neutral reading; a provider that does report
// a reset instant can feed SessionResetIn/WeeklyResetIn through this same
// call shape later without touching the dispatch.
func (h *ChatHandler) applyResetAwareRouting(ctx context.Context, models []string) []string {
	if len(models) <= 1 {
		return models
	}
	candidates := make([]routing.ResetAwareCandidate, 0, len(models))
	for _, m := range models {
		prefix, _, ok := strings.Cut(m, "/")
		remaining := 0.5 // neutral: no quota telemetry, no pressure evidence
		if ok {
			if score, has := h.strategyQuotaRemaining(ctx, resolveProviderAlias(prefix)); has {
				remaining = score
			}
		}
		candidates = append(candidates, routing.ResetAwareCandidate{
			SessionRemaining: remaining,
			WeeklyRemaining:  remaining,
			SessionResetIn:   strategySessionWindow,
			WeeklyResetIn:    strategyWeeklyWindow,
		})
	}
	order := routing.OrderResetAware(candidates, time.Now())
	out := make([]string, 0, len(models))
	for _, idx := range order {
		if idx >= 0 && idx < len(models) {
			out = append(out, models[idx])
		}
	}
	return out
}

func clampStrategyUnit(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func poolMax(latency map[string]float64, models []string) float64 {
	max := 0.0
	for _, m := range models {
		if v, ok := latency[m]; ok && v > max {
			max = v
		}
	}
	return max
}

// modelLockoutSkipped reports whether the in-memory per-model lockout
// (internal/resilience modellockout) currently holds the tuple. It rides
// BESIDE the persisted db lock, not instead of it: the db lock survives
// restarts, this one carries the Retry-After precision and the escalation
// streak, and either refusing skips the candidate for this pass.
func modelLockoutSkipped(provider, connectionID, model string) bool {
	locked, _ := resilience.ModelLocked(provider, connectionID, model)
	return locked
}

// modelLockoutRecordFailure feeds one upstream failure to the in-memory
// per-model lockout at the same site the persisted lock is written, so the
// two stay consistent. retryAfter is the cooldown the strike classifier
// computed (already folding the upstream Retry-After header).
func modelLockoutRecordFailure(provider, connectionID, model string, status int, retryAfter time.Duration) {
	resilience.RecordModelFailure(provider, connectionID, model, status, retryAfter)
}

// modelLockoutRecordSuccess clears the tuple's in-memory streak when the
// attempt is served, so one success stops the escalation the next failure
// would otherwise inherit.
func modelLockoutRecordSuccess(provider, connectionID, model string) {
	resilience.RecordModelSuccess(provider, connectionID, model)
}
