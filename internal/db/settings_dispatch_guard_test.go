// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import "testing"

// TestParseSettings_AllowPrivateProviderURLs pins the dispatch-guard opt-in:
// the key must read through to the field, a missing key must leave the default
// (false), and a wrongly typed value must not widen the guard.
func TestParseSettings_AllowPrivateProviderURLs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "absent key defaults false",
			raw:  `{}`,
			want: false,
		},
		{
			name: "explicit true parses",
			raw:  `{"allowPrivateProviderURLs": true}`,
			want: true,
		},
		{
			name: "explicit false stays false",
			raw:  `{"allowPrivateProviderURLs": false}`,
			want: false,
		},
		{
			name: "wrongly typed value falls back to false",
			raw:  `{"allowPrivateProviderURLs": "yes"}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := parseSettings(tt.raw)
			if !ok {
				t.Fatalf("parseSettings(%s) not ok", tt.raw)
			}
			if s.AllowPrivateProviderURLs != tt.want {
				t.Fatalf("AllowPrivateProviderURLs = %v, want %v", s.AllowPrivateProviderURLs, tt.want)
			}
		})
	}
}

// TestGetSettings_AllowPrivateProviderURLsRoundTrip pins that the stored row
// reads back through the repo path, not just the parser.
func TestGetSettings_AllowPrivateProviderURLsRoundTrip(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	r := NewRepo(database)

	if err := r.UpdateSettingsRaw(map[string]any{"allowPrivateProviderURLs": true}); err != nil {
		t.Fatalf("UpdateSettingsRaw: %v", err)
	}
	s, err := r.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !s.AllowPrivateProviderURLs {
		t.Fatal("round-trip lost AllowPrivateProviderURLs=true")
	}
}
