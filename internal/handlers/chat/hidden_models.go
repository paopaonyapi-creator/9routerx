// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/log"
	"9routerx/proxy/internal/models"
	"9routerx/proxy/internal/providers"
)

// FASE 10.3 model visibility. Hidden is the third axis of per-key model
// policy, beside the allowlist (access.go) and the disabled-models KV scope:
//
//   - the allowlist decides what a key MAY call (403 when refused),
//   - disabled decides what the gateway offers anyone,
//   - hidden decides what THIS key can SEE (dropped from /v1/models, 404 on
//     dispatch — never 403, which would confirm the model exists).
//
// A key's hide list is the union of its own apiKeys.hiddenModels patterns and
// the global settings blob's hiddenModels. Patterns are the allowlist glob
// (providers.MatchAllowPattern): exact, `prefix*`, `*suffix`, or `*mid*`.
//
// Like the allowlist, dispatch and listing ask the same question through the
// same loader and the same matcher, so a client can never call a model it was
// not offered.

// HiddenPolicyCacheTTL bounds how long a merged hide list stays cached. The
// dashboard invalidates on write, so the TTL is only the belt: a policy write
// takes effect immediately, and a write that skips the invalidation (a hand
// edited settings blob, a restore) lands within half a minute.
const HiddenPolicyCacheTTL = 30 * time.Second

// hiddenPolicyCacheCapacity bounds the cached lists. One per key id; the
// keyless entry ("" — global list only) is one of them.
const hiddenPolicyCacheCapacity = 256

// hiddenPolicyEntry is one cached merged hide list.
type hiddenPolicyEntry struct {
	patterns []string
	expires  time.Time
}

// hiddenPolicyCache is a bounded, TTL'd cache of merged hide lists, keyed by
// API key id. The shape follows keikey.Cache (internal/keikey/cache.go): map
// for lookup, insertion-ordered slice for oldest-first eviction. Safe for
// concurrent use.
type hiddenPolicyCache struct {
	mu      sync.Mutex
	entries map[string]hiddenPolicyEntry
	ttl     time.Duration
	max     int
	order   []string
}

func newHiddenPolicyCache(ttl time.Duration, capacity int) *hiddenPolicyCache {
	if ttl <= 0 {
		ttl = HiddenPolicyCacheTTL
	}
	if capacity <= 0 {
		capacity = hiddenPolicyCacheCapacity
	}
	return &hiddenPolicyCache{
		entries: make(map[string]hiddenPolicyEntry, capacity),
		ttl:     ttl,
		max:     capacity,
		order:   make([]string, 0, capacity),
	}
}

// get returns the cached list for keyID when present and unexpired.
func (c *hiddenPolicyCache) get(keyID string) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[keyID]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		c.removeLocked(keyID)
		return nil, false
	}
	return e.patterns, true
}

// set stores the list under keyID, dropping expired entries and, when still
// over capacity, the oldest ones.
func (c *hiddenPolicyCache) set(keyID string, patterns []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[keyID]; !exists {
		c.order = append(c.order, keyID)
	}
	c.entries[keyID] = hiddenPolicyEntry{patterns: patterns, expires: time.Now().Add(c.ttl)}
	now := time.Now()
	live := c.order[:0]
	for _, k := range c.order {
		if e, ok := c.entries[k]; ok && now.After(e.expires) {
			delete(c.entries, k)
			continue
		}
		live = append(live, k)
	}
	c.order = live
	for len(c.entries) > c.max && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// removeLocked deletes one entry; the caller holds mu.
func (c *hiddenPolicyCache) removeLocked(keyID string) {
	delete(c.entries, keyID)
	for i, k := range c.order {
		if k == keyID {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// clear drops every cached list.
func (c *hiddenPolicyCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]hiddenPolicyEntry, c.max)
	c.order = c.order[:0]
}

// defaultHiddenPolicyCache backs the visibility filter. A package-level cache
// avoids threading another field through every ChatHandler construction site,
// the same trade keikey.DefaultAuthCache makes; its TTL bounds how long a
// hidden model stays listed after a policy write.
var defaultHiddenPolicyCache = newHiddenPolicyCache(HiddenPolicyCacheTTL, hiddenPolicyCacheCapacity)

// InvalidateHiddenModelsCache drops every cached hide list. The dashboard
// calls it after a per-key or global hidden-models write so the change is
// visible on the next request, not on the next TTL lapse.
func InvalidateHiddenModelsCache() {
	defaultHiddenPolicyCache.clear()
}

// effectiveHiddenModels is the ONE loader for a caller's hide list: the key's
// own patterns unioned with the global list, cached per key id.
//
// An empty result hides nothing, which is every key and every install written
// before this feature. A storage failure fails OPEN (hide nothing) rather
// than 404-ing the whole catalog: hidden is a cosmetic policy, and a transient
// database error must not turn every model invisible.
func (h *ChatHandler) effectiveHiddenModels(keyID string) []string {
	if cached, ok := defaultHiddenPolicyCache.get(keyID); ok {
		return cached
	}
	merged := h.loadHiddenModels(keyID)
	defaultHiddenPolicyCache.set(keyID, merged)
	return merged
}

// loadHiddenModels reads the two halves fresh. Both halves degrade to ""
// individually: a failed per-key read must not wipe the global list, and vice
// versa.
func (h *ChatHandler) loadHiddenModels(keyID string) []string {
	if h == nil || h.Repo == nil {
		return nil
	}
	own := ""
	if keyID != "" {
		stored, err := h.Repo.GetApiKeyHiddenModels(keyID)
		if err != nil {
			log.Error("chat", "hidden models read failed, fail-open", "error", err, "apiKeyId", keyID)
		} else {
			own = stored
		}
	}
	global, err := h.Repo.GetHiddenModels()
	if err != nil {
		log.Error("chat", "global hidden models read failed, fail-open", "error", err)
	}
	return mergeHiddenPatterns(own, global)
}

// mergeHiddenPatterns parses two stored JSON arrays of model patterns into one
// ordered, de-duplicated list. Blanks are dropped; a malformed array
// contributes nothing — both stores are validated on write, so garbage there
// means a hand edit, and hiding nothing is the safe reading of a value the
// operator cannot have written deliberately.
func mergeHiddenPatterns(own, global string) []string {
	seen := make(map[string]bool, 8)
	out := make([]string, 0, 8)
	for _, raw := range []string{own, global} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var patterns []string
		if err := json.Unmarshal([]byte(raw), &patterns); err != nil {
			continue
		}
		for _, p := range patterns {
			p = strings.TrimSpace(p)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// modelHidden is the single hide decision: any pattern matching any candidate
// spelling hides the model. The matcher is deliberately the allowlist one
// (providers.MatchAllowPattern) so a pattern written in the policy modal means
// the same thing on both sides of the feature.
func modelHidden(patterns []string, candidates ...string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, pattern := range patterns {
		for _, candidate := range candidates {
			if providers.MatchAllowPattern(pattern, candidate) {
				return true
			}
		}
	}
	return false
}

// hiddenKeyID is the cache/lookup identity of a request's key. A keyless
// request (no API-key middleware) still sees the GLOBAL hide list, so its
// identity is "" rather than "skip".
func hiddenKeyID(key *models.APIKey) string {
	if key == nil {
		return ""
	}
	return key.ID
}

// filterModelsByHidden narrows a published model list to what the caller can
// see. It loads through effectiveHiddenModels — the same loader the dispatch
// gate uses — so the two cannot disagree.
func (h *ChatHandler) filterModelsByHidden(key *models.APIKey, data []ModelInfoObject) []ModelInfoObject {
	patterns := h.effectiveHiddenModels(hiddenKeyID(key))
	if len(patterns) == 0 {
		return data
	}
	out := make([]ModelInfoObject, 0, len(data))
	for _, m := range data {
		if !modelHidden(patterns, modelEntryCandidates(m)...) {
			out = append(out, m)
		}
	}
	return out
}

// ErrModelHidden is the typed 404 a hidden model answers with. It is
// deliberately indistinguishable from "no such model" on the wire: the
// message is the same shape the resolution path writes, so a client cannot
// probe which models exist behind the hide list.
type ErrModelHidden struct {
	Model string
}

func (e *ErrModelHidden) Error() string {
	return "model not found: " + e.Model
}

// IsModelHidden reports whether err is the typed hidden-model refusal.
func IsModelHidden(err error) bool {
	var target *ErrModelHidden
	return errors.As(err, &target)
}

// checkModelHidden is the visibility half of the dispatch gate. Hidden takes
// precedence over the allowlist verdict: a hidden model answers 404 even for
// a key whose allowlist would have refused it with 403, because the 403 would
// confirm the model exists.
func (h *ChatHandler) checkModelHidden(key *models.APIKey, modelID string) error {
	patterns := h.effectiveHiddenModels(hiddenKeyID(key))
	if len(patterns) == 0 {
		return nil
	}
	if modelHidden(patterns, h.accessCandidates(modelID)...) {
		return &ErrModelHidden{Model: modelID}
	}
	return nil
}

// writeModelHiddenError writes the 404 a hidden model answers with. The code
// is model_not_found — OpenAI's unknown-model code — so the refusal reads as
// a resolution failure, not a policy one.
func writeModelHiddenError(w http.ResponseWriter, model string) {
	handlerutil.WriteJSONErrorCode(w, http.StatusNotFound, "model_not_found", "model not found: "+model)
}
