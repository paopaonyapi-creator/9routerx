// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"unicode/utf8"
)

// cacheAffinity implements the `cache-optimized` strategy: requests that share
// a stable prompt prefix should keep landing on the same account, so the
// provider's prompt cache stays warm and the discount (or latency win) is kept.
//
// Two mechanisms cooperate:
//
//   - PromptCacheKey derives a stable key for a request body: an explicit
//     `prompt_cache_key` when the caller supplies one, otherwise a SHA-256 of
//     the messages minus the last user turn (the part that changes per turn).
//   - PickByRendezvous is the sticky pick: rendezvous (highest-random-weight)
//     hashing maps a key to one candidate account, and stays on that account
//     while candidates come and go without a global ring to maintain.

// maxExplicitCacheKeyLen bounds the caller-supplied cache key. The OpenAI
// prompt-cache key convention allows up to 4096 characters; anything longer is
// treated as abuse or a wiring bug and ignored rather than hashed verbatim.
const maxExplicitCacheKeyLen = 4096

// promptCacheBody is the slice of a chat-completions body the key derivation
// needs. RawMessage keeps the messages opaque: deriving a key must not decode
// (and re-encode) potentially megabytes of content.
type promptCacheBody struct {
	PromptCacheKey string `json:"prompt_cache_key"`
	Metadata       struct {
		PromptCacheKey string `json:"prompt_cache_key"`
	} `json:"metadata"`
	Messages []json.RawMessage `json:"messages"`
}

// promptCacheMessage peeks at one message's role.
type promptCacheMessage struct {
	Role string `json:"role"`
}

// PromptCacheKey returns the stable cache key for a chat-completions request
// body, or "" when no reusable prefix exists.
//
// An explicit key wins verbatim: the body's top-level `prompt_cache_key`, then
// `metadata.prompt_cache_key`, accepted when it is a string of at most 4096
// characters. Without one, the stable prefix — every message except the last
// user turn, which is the part that changes every request — is marshalled
// deterministically (Go's encoder compacts JSON, so client whitespace does not
// change the hash) and SHA-256'd to hex. Bodies with fewer than two messages,
// with no user turn, or whose only user turn is the first message have no
// reusable prefix and return "".
func PromptCacheKey(body []byte) string {
	if key := explicitPromptCacheKey(body); key != "" {
		return key
	}
	return hashedPromptPrefix(body)
}

// explicitPromptCacheKey returns the caller-supplied cache key, preferring the
// top-level field over metadata, or "" when neither holds a usable value.
func explicitPromptCacheKey(body []byte) string {
	var parsed promptCacheBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	for _, key := range []string{parsed.PromptCacheKey, parsed.Metadata.PromptCacheKey} {
		if key == "" {
			continue
		}
		if utf8.RuneCountInString(key) > maxExplicitCacheKeyLen {
			continue
		}
		return key
	}
	return ""
}

// hashedPromptPrefix hashes messages[0:lastUser] as hex, or "" when there is
// no reusable prefix.
func hashedPromptPrefix(body []byte) string {
	var parsed promptCacheBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if len(parsed.Messages) < 2 {
		return ""
	}
	lastUser := -1
	for i := len(parsed.Messages) - 1; i >= 0; i-- {
		var msg promptCacheMessage
		if err := json.Unmarshal(parsed.Messages[i], &msg); err == nil && msg.Role == "user" {
			lastUser = i
			break
		}
	}
	if lastUser <= 0 {
		return ""
	}
	prefix, err := json.Marshal(parsed.Messages[:lastUser])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(prefix)
	return hex.EncodeToString(sum[:])
}

// PickByRendezvous picks one candidate for the key using highest-random-weight
// (rendezvous) hashing: each candidate scores sha256(key + "\0" + candidate)
// and the maximum wins. Rendezvous hashing is order-independent — the winner
// depends only on the key and the candidate set, not on their order — and when
// a candidate is removed only the keys that used to map to it move, which is
// what keeps prompt caches warm across pool changes. Returns "" for no
// candidates; hash ties keep the earliest candidate in the input.
func PickByRendezvous(key string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	keyBytes := []byte(key)
	sep := [1]byte{0}
	best := ""
	var bestHash [sha256.Size]byte
	h := sha256.New()
	for i, candidate := range candidates {
		h.Reset()
		h.Write(keyBytes)
		h.Write(sep[:])
		h.Write([]byte(candidate))
		var sum [sha256.Size]byte
		copy(sum[:], h.Sum(nil))
		if i == 0 || greaterHash(sum, bestHash) {
			best, bestHash = candidate, sum
		}
	}
	return best
}

// OrderByRendezvous returns the candidates ordered by their rendezvous weight
// for the key, highest hash first — the full-ordering form of PickByRendezvous
// that a strategy needs when it walks candidates as a fallback chain instead of
// pinning one. Order-independent on the input, deterministic on (key, set);
// hash ties keep the input's relative order (stable sort).
func OrderByRendezvous(key string, candidates []string) []string {
	if len(candidates) <= 1 {
		return append([]string(nil), candidates...)
	}
	keyBytes := []byte(key)
	sep := [1]byte{0}
	type weighted struct {
		name string
		sum  [sha256.Size]byte
	}
	weightedCandidates := make([]weighted, 0, len(candidates))
	h := sha256.New()
	for _, candidate := range candidates {
		h.Reset()
		h.Write(keyBytes)
		h.Write(sep[:])
		h.Write([]byte(candidate))
		var sum [sha256.Size]byte
		copy(sum[:], h.Sum(nil))
		weightedCandidates = append(weightedCandidates, weighted{name: candidate, sum: sum})
	}
	sort.SliceStable(weightedCandidates, func(i, j int) bool {
		return greaterHash(weightedCandidates[i].sum, weightedCandidates[j].sum)
	})
	out := make([]string, 0, len(weightedCandidates))
	for _, wc := range weightedCandidates {
		out = append(out, wc.name)
	}
	return out
}

// greaterHash reports whether a hashes higher than b, most significant byte
// first.
func greaterHash(a, b [sha256.Size]byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
