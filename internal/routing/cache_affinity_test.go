// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// sha256Hex is the reference digest the cache-key tests pin against, computed
// straight from the standard library rather than from the code under test.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestPromptCacheKeyExplicitKeyWins(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "top-level prompt_cache_key is used verbatim",
			body: `{"prompt_cache_key":"conv-42","messages":[{"role":"user","content":"hi"}]}`,
			want: "conv-42",
		},
		{
			name: "metadata.prompt_cache_key is used when no top-level key exists",
			body: `{"metadata":{"prompt_cache_key":"meta-7"},"messages":[{"role":"user","content":"hi"}]}`,
			want: "meta-7",
		},
		{
			name: "top-level key beats the metadata key",
			body: `{"prompt_cache_key":"top","metadata":{"prompt_cache_key":"meta"},"messages":[{"role":"user"}]}`,
			want: "top",
		},
		{
			name: "explicit key wins even over a hashable prefix",
			body: `{"prompt_cache_key":"explicit","messages":[{"role":"system","content":"s"},{"role":"user","content":"q"}]}`,
			want: "explicit",
		},
		{
			name: "an over-long explicit key falls through to the prefix hash",
			body: `{"prompt_cache_key":"` + strings.Repeat("x", 4097) + `","messages":[{"role":"system","content":"s"},{"role":"user","content":"q"}]}`,
			want: sha256Hex(`[{"role":"system","content":"s"}]`),
		},
		{
			name: "exactly 4096 characters is accepted",
			body: `{"prompt_cache_key":"` + strings.Repeat("x", 4096) + `"}`,
			want: strings.Repeat("x", 4096),
		},
		{
			name: "a non-string key is ignored",
			body: `{"prompt_cache_key":42}`,
			want: "",
		},
		{
			name: "an empty explicit key falls through",
			body: `{"prompt_cache_key":""}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PromptCacheKey([]byte(tt.body)); got != tt.want {
				t.Errorf("PromptCacheKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPromptCacheKeyHashedPrefix(t *testing.T) {
	systemAndUser := `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"QUESTION"}]}`
	// The same conversation one turn later: identical prefix, new user text.
	extendedTurn := `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"QUESTION"},{"role":"assistant","content":"ANSWER"},{"role":"user","content":"NEXT"}]}`

	tests := []struct {
		name    string
		body    string
		wantLen int
	}{
		{name: "prefix of two messages hashes", body: systemAndUser, wantLen: sha256.Size * 2},
		{name: "growing conversation hashes its longer prefix", body: extendedTurn, wantLen: sha256.Size * 2},
		{name: "single message has no reusable prefix", body: `{"messages":[{"role":"user","content":"hi"}]}`, wantLen: 0},
		{name: "no messages has no reusable prefix", body: `{"messages":[]}`, wantLen: 0},
		{name: "missing messages has no reusable prefix", body: `{}`, wantLen: 0},
		{name: "no user turn has no reusable prefix", body: `{"messages":[{"role":"system","content":"a"},{"role":"system","content":"b"}]}`, wantLen: 0},
		{name: "user turn as the first message leaves an empty prefix", body: `{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"a"}]}`, wantLen: 0},
		{name: "invalid body has no reusable prefix", body: `not json`, wantLen: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PromptCacheKey([]byte(tt.body))
			if len(got) != tt.wantLen {
				t.Errorf("PromptCacheKey() length = %d, want %d", len(got), tt.wantLen)
			}
		})
	}

	// Each turn's key legitimately differs — the provider-cached prefix grows
	// with the conversation. What must hold is that the extended turn's key is
	// exactly the hash of the messages before its last user turn.
	extendedPrefix := `[{"role":"system","content":"sys"},{"role":"user","content":"QUESTION"},{"role":"assistant","content":"ANSWER"}]`
	if got := PromptCacheKey([]byte(extendedTurn)); got != sha256Hex(extendedPrefix) {
		t.Errorf("extended turn key = %q, want the reference digest of its 3-message prefix", got)
	}
}

func TestPromptCacheKeyDeterministic(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"s"},{"role":"user","content":"q"}]}`)
	// The same prefix content with different client whitespace must hash the
	// same: the encoder compacts before hashing.
	pretty := []byte("{\n  \"messages\": [\n    {\"role\":  \"system\", \"content\": \"s\"},\n    {\"role\": \"user\", \"content\": \"q\"}\n  ]\n}")
	first := PromptCacheKey(body)
	if again := PromptCacheKey(body); again != first {
		t.Errorf("PromptCacheKey() = %q on second call, want stable %q", again, first)
	}
	if compacted := PromptCacheKey(pretty); compacted != first {
		t.Errorf("PromptCacheKey(pretty-printed) = %q, want the compacted hash %q", compacted, first)
	}
	// A different prefix must produce a different key.
	changed := []byte(`{"messages":[{"role":"system","content":"OTHER"},{"role":"user","content":"q"}]}`)
	if PromptCacheKey(changed) == first {
		t.Error("different prefixes produced the same key")
	}
}

func TestPromptCacheKeyMatchesReferenceDigest(t *testing.T) {
	// Pin the exact digest for a known body so an accidental change to the
	// hashing input (dropping the marshal step, changing the prefix cut)
	// cannot slip through silently. The reference input is the compacted
	// prefix the body's first message produces, with key order preserved.
	body := []byte(`{"messages":[{"role":"system","content":"s"},{"role":"user","content":"q"}]}`)
	want := sha256Hex(`[{"role":"system","content":"s"}]`)
	if got := PromptCacheKey(body); got != want {
		t.Errorf("PromptCacheKey() = %q, want reference digest %q", got, want)
	}
}

func TestPickByRendezvous(t *testing.T) {
	candidates := []string{"acct-a", "acct-b", "acct-c"}
	shuffled := []string{"acct-c", "acct-a", "acct-b"}

	tests := []struct {
		name       string
		key        string
		candidates []string
		want       string
	}{
		{
			name:       "empty candidates return empty",
			key:        "k",
			candidates: nil,
			want:       "",
		},
		{
			name:       "single candidate always wins",
			key:        "any-key",
			candidates: []string{"only"},
			want:       "only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PickByRendezvous(tt.key, tt.candidates); got != tt.want {
				t.Errorf("PickByRendezvous(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}

	t.Run("same key always picks the same winner", func(t *testing.T) {
		first := PickByRendezvous("conv-42", candidates)
		for range 3 {
			if got := PickByRendezvous("conv-42", candidates); got != first {
				t.Errorf("PickByRendezvous() = %q, want stable %q", got, first)
			}
		}
	})

	t.Run("candidate order does not change the winner", func(t *testing.T) {
		if PickByRendezvous("conv-42", shuffled) != PickByRendezvous("conv-42", candidates) {
			t.Error("candidate order changed the rendezvous winner")
		}
	})

	t.Run("winner is the maximum reference hash", func(t *testing.T) {
		// Compute the expected winner from first principles: the candidate
		// whose sha256(key + NUL + candidate) digests highest as bytes.
		best, bestSum := "", ""
		for _, c := range candidates {
			sum := sha256.Sum256([]byte("conv-42\x00" + c))
			ref := hex.EncodeToString(sum[:])
			if best == "" || ref > bestSum {
				best, bestSum = c, ref
			}
		}
		if got := PickByRendezvous("conv-42", candidates); got != best {
			t.Errorf("PickByRendezvous() = %q, want reference maximum %q", got, best)
		}
	})
}

func TestPickByRendezvousDistributesAcrossKeys(t *testing.T) {
	// A sanity check on the hash spread: with distinct keys and three
	// candidates, every candidate must be reachable, otherwise the strategy
	// silently funnels all traffic to one account.
	candidates := []string{"acct-a", "acct-b", "acct-c"}
	seen := map[string]bool{}
	for i := range 24 {
		seen[PickByRendezvous(fmt.Sprintf("key-%d", i), candidates)] = true
	}
	if len(seen) != len(candidates) {
		t.Errorf("24 keys reached %d distinct candidates, want %d", len(seen), len(candidates))
	}
}
