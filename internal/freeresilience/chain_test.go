// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import "testing"

func TestOrderCandidates_ConfiguredFirst(t *testing.T) {
	got := OrderCandidates(
		[]string{"glm", "gemini", "qoder"},
		[]string{"qoder", "gemini", "glm", "groq"},
		func(string) float64 { return 0 },
	)
	want := []string{"glm", "gemini", "qoder", "groq"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOrderCandidates_HealthOrder(t *testing.T) {
	scores := map[string]float64{"a": 0.5, "b": 0.99, "c": 0.2, "d": 0.75}
	got := OrderCandidates(nil, []string{"a", "b", "c", "d"}, func(p string) float64 { return scores[p] })
	want := []string{"b", "d", "a", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOrderCandidates_SkipsUnknownConfiguredAndDedupes(t *testing.T) {
	got := OrderCandidates(
		[]string{"not-eligible", "glm", "glm"},
		[]string{"glm", "groq"},
		nil,
	)
	if len(got) != 2 || got[0] != "glm" || got[1] != "groq" {
		t.Errorf("got %v, want [glm groq]", got)
	}
	if OrderCandidates(nil, nil, nil) != nil {
		t.Error("empty eligible set must order to nil")
	}
}

func TestOrderCandidates_EqualScoresKeepInputOrder(t *testing.T) {
	got := OrderCandidates(nil, []string{"x", "y", "z"}, func(string) float64 { return 1 })
	if got[0] != "x" || got[1] != "y" || got[2] != "z" {
		t.Errorf("equal scores must keep input order, got %v", got)
	}
}
