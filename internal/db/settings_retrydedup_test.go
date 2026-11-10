// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import "testing"

// The B6 settings must parse from the raw blob the dashboard writes and keep
// their defaults when the keys are absent: dedup off, no cap — exactly the
// behaviour every install had before these settings existed.
func TestParseSettings_RetryDedupAndAttemptCap(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantDedup  bool
		wantGlobal int
		wantProv   map[string]int
		wantModels map[string]int
	}{
		{
			name:       "absent keys keep defaults",
			raw:        `{}`,
			wantDedup:  false,
			wantGlobal: 0,
		},
		{
			name:      "dedup switch parses",
			raw:       `{"retryDedupEnabled": true}`,
			wantDedup: true,
		},
		{
			name:       "global cap parses",
			raw:        `{"attemptCap": {"global": 3}}`,
			wantGlobal: 3,
		},
		{
			name:       "provider and model tiers parse",
			raw:        `{"attemptCap": {"providers": {"openai": 2}, "models": {"openai/gpt-4o": 5, "gpt-4o-mini": 4}}}`,
			wantGlobal: 0,
			wantProv:   map[string]int{"openai": 2},
			wantModels: map[string]int{"openai/gpt-4o": 5, "gpt-4o-mini": 4},
		},
		{
			name:       "zero tier is preserved (disables the cap)",
			raw:        `{"attemptCap": {"providers": {"openai": 0}}}`,
			wantProv:   map[string]int{"openai": 0},
		},
		{
			name:       "negative values are dropped, not clamped silently into the map",
			raw:        `{"attemptCap": {"global": -1}}`,
			wantGlobal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := parseSettings(tt.raw)
			if !ok {
				t.Fatalf("parseSettings(%s) not readable", tt.raw)
			}
			if s.RetryDedupEnabled != tt.wantDedup {
				t.Errorf("RetryDedupEnabled = %v, want %v", s.RetryDedupEnabled, tt.wantDedup)
			}
			if s.AttemptCap.Global != tt.wantGlobal {
				t.Errorf("AttemptCap.Global = %d, want %d", s.AttemptCap.Global, tt.wantGlobal)
			}
			if tt.wantProv != nil {
				for k, want := range tt.wantProv {
					got, ok := s.AttemptCap.Providers[k]
					if !ok || got != want {
						t.Errorf("AttemptCap.Providers[%q] = %d (ok=%v), want %d", k, got, ok, want)
					}
				}
			} else if len(s.AttemptCap.Providers) != 0 {
				t.Errorf("AttemptCap.Providers = %v, want empty", s.AttemptCap.Providers)
			}
			if tt.wantModels != nil {
				for k, want := range tt.wantModels {
					got, ok := s.AttemptCap.Models[k]
					if !ok || got != want {
						t.Errorf("AttemptCap.Models[%q] = %d (ok=%v), want %d", k, got, ok, want)
					}
				}
			} else if len(s.AttemptCap.Models) != 0 {
				t.Errorf("AttemptCap.Models = %v, want empty", s.AttemptCap.Models)
			}
		})
	}
}

// A settings write through UpdateSettingsRaw must survive a GetSettings
// round trip: the dashboard saves the tiers as raw JSON and the chat handler
// reads them back through the cache.
func TestGetSettings_RetryDedupRoundTrip(t *testing.T) {
	r := openEphemeralSchemaDB(t)

	err := r.UpdateSettingsRaw(map[string]any{
		"retryDedupEnabled": true,
		"attemptCap":        map[string]any{"global": 6, "providers": map[string]any{"openai": 2}},
	})
	if err != nil {
		t.Fatalf("UpdateSettingsRaw: %v", err)
	}

	s, err := r.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !s.RetryDedupEnabled {
		t.Error("RetryDedupEnabled lost in the round trip")
	}
	if s.AttemptCap.Global != 6 {
		t.Errorf("AttemptCap.Global = %d, want 6", s.AttemptCap.Global)
	}
	if got := s.AttemptCap.Providers["openai"]; got != 2 {
		t.Errorf("AttemptCap.Providers[openai] = %d, want 2", got)
	}
}
