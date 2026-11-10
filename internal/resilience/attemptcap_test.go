// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package resilience

import "testing"

func TestAttemptCapLimitResolution(t *testing.T) {
	tests := []struct {
		name     string
		cap      AttemptCap
		provider string
		model    string
		expected int
	}{
		{
			name:     "empty cap means unlimited",
			cap:      AttemptCap{},
			provider: "openai",
			model:    "gpt-4o",
			expected: 0,
		},
		{
			name:     "global applies when nothing more specific",
			cap:      AttemptCap{Global: 5},
			provider: "openai",
			model:    "gpt-4o",
			expected: 5,
		},
		{
			name:     "provider beats global",
			cap:      AttemptCap{Global: 5, Providers: map[string]int{"openai": 3}},
			provider: "openai",
			model:    "gpt-4o",
			expected: 3,
		},
		{
			name:     "qualified model beats provider",
			cap:      AttemptCap{Global: 5, Providers: map[string]int{"openai": 3}, Models: map[string]int{"openai/gpt-4o": 2}},
			provider: "openai",
			model:    "gpt-4o",
			expected: 2,
		},
		{
			name:     "bare model beats provider",
			cap:      AttemptCap{Global: 5, Providers: map[string]int{"openai": 3}, Models: map[string]int{"gpt-4o": 2}},
			provider: "openai",
			model:    "gpt-4o",
			expected: 2,
		},
		{
			name:     "qualified model wins over bare model",
			cap:      AttemptCap{Models: map[string]int{"gpt-4o": 4, "openai/gpt-4o": 2}},
			provider: "openai",
			model:    "gpt-4o",
			expected: 2,
		},
		{
			name:     "zero on the most specific tier disables the cap",
			cap:      AttemptCap{Global: 5, Providers: map[string]int{"openai": 0}},
			provider: "openai",
			model:    "gpt-4o",
			expected: 0,
		},
		{
			name:     "negative values clamp to zero",
			cap:      AttemptCap{Global: -3},
			provider: "openai",
			model:    "gpt-4o",
			expected: 0,
		},
		{
			name:     "other providers keep the global",
			cap:      AttemptCap{Global: 5, Providers: map[string]int{"openai": 3}},
			provider: "anthropic",
			model:    "claude-3",
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cap.Limit(tt.provider, tt.model)
			if got != tt.expected {
				t.Errorf("Limit(%q, %q) = %d, want %d", tt.provider, tt.model, got, tt.expected)
			}
		})
	}
}

func TestAttemptCapReached(t *testing.T) {
	cap := AttemptCap{Global: 2}

	if cap.Reached("openai", "gpt-4o", 0) {
		t.Error("first attempt of a capped-2 walk must not be blocked")
	}
	if cap.Reached("openai", "gpt-4o", 1) {
		t.Error("second attempt of a capped-2 walk must not be blocked")
	}
	if !cap.Reached("openai", "gpt-4o", 2) {
		t.Error("third attempt of a capped-2 walk must be blocked")
	}

	// Zero (or an empty cap) never blocks: unlimited stays unlimited.
	if (AttemptCap{}).Reached("openai", "gpt-4o", 99) {
		t.Error("empty cap must never block")
	}
}

func TestAttemptCapEmpty(t *testing.T) {
	if !(AttemptCap{}).Empty() {
		t.Error("zero AttemptCap must report Empty")
	}
	if (AttemptCap{Global: 1}).Empty() {
		t.Error("a configured global must not report Empty")
	}
	if (AttemptCap{Providers: map[string]int{"openai": 2}}).Empty() {
		t.Error("a configured provider must not report Empty")
	}
}

func TestAttemptCapReachedScoped(t *testing.T) {
	cap := AttemptCap{Global: 3, Providers: map[string]int{"openai": 1}, Models: map[string]int{"openai/gpt-4o": 2}}

	// Global tier: 3 total attempts across any provider.
	if cap.ReachedScoped("anthropic", "claude-3", 2, 0, 0) {
		t.Error("2 total attempts must stay under a global cap of 3")
	}
	if !cap.ReachedScoped("anthropic", "claude-3", 3, 0, 0) {
		t.Error("3 total attempts must reach a global cap of 3")
	}
	// Provider tier: 1 openai attempt regardless of the model.
	if cap.ReachedScoped("openai", "gpt-4o-mini", 0, 0, 0) {
		t.Error("0 provider attempts must stay under a provider cap of 1")
	}
	if !cap.ReachedScoped("openai", "gpt-4o-mini", 0, 1, 0) {
		t.Error("1 provider attempt must reach a provider cap of 1")
	}
	// Model tier: 2 dispatches of the exact pair, checked before the
	// provider tier.
	if !cap.ReachedScoped("openai", "gpt-4o", 0, 1, 2) {
		t.Error("2 exact-model attempts must reach a model cap of 2")
	}
	// The model tier only counts the exact pair.
	if cap.ReachedScoped("openai", "gpt-4o", 0, 1, 1) {
		t.Error("1 exact-model attempt must stay under a model cap of 2")
	}

	// A tier of 0 disables itself and falls through.
	zero := AttemptCap{Global: 2, Providers: map[string]int{"openai": 0}}
	if zero.ReachedScoped("openai", "gpt-4o", 5, 5, 5) {
		t.Error("a provider tier of 0 must disable the cap for that provider")
	}
	if !zero.ReachedScoped("anthropic", "claude-3", 2, 0, 0) {
		t.Error("other providers still obey the global tier")
	}
}
