// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	json "encoding/json/v2"
	"testing"
)

func TestRoutingMetadataRecorder(t *testing.T) {
	rec := NewRoutingRecorder()
	ctx := WithRoutingRecorder(context.Background(), rec)

	candidates := []string{"openai/gpt-4o", "anthropic/claude-3-5-sonnet", "opencode/space-bunny-free"}
	rec.SetCandidates("round-robin", candidates)

	// Record skips with reasons per B7 explainability requirement
	RecordCandidateSkip(ctx, "openai/gpt-4o", "openai", "attempt cap reached")
	RecordCandidateSkip(ctx, "anthropic/claude-3-5-sonnet", "anthropic", "prompt exceeds context window (200000)")

	// Record selection with reason
	RecordCandidateSelection(ctx, "space-bunny-free", "opencode", "candidate seat served response")

	snap := rec.Snapshot()
	if snap.Strategy != "round-robin" {
		t.Errorf("Strategy = %q, want round-robin", snap.Strategy)
	}
	if len(snap.Candidates) != 3 {
		t.Fatalf("len(Candidates) = %d, want 3", len(snap.Candidates))
	}
	if snap.Selected != "opencode/space-bunny-free" {
		t.Errorf("Selected = %q, want opencode/space-bunny-free", snap.Selected)
	}
	if snap.Reason != "candidate seat served response" {
		t.Errorf("Reason = %q, want candidate seat served response", snap.Reason)
	}
	if len(snap.Evaluated) != 3 {
		t.Fatalf("len(Evaluated) = %d, want 3", len(snap.Evaluated))
	}

	if snap.Evaluated[0].Status != "skipped" || snap.Evaluated[0].Reason != "attempt cap reached" {
		t.Errorf("Evaluated[0] = %+v, want skipped due to cap", snap.Evaluated[0])
	}
	if snap.Evaluated[1].Status != "skipped" || snap.Evaluated[1].Reason != "prompt exceeds context window (200000)" {
		t.Errorf("Evaluated[1] = %+v, want skipped due to context", snap.Evaluated[1])
	}
	if snap.Evaluated[2].Status != "selected" {
		t.Errorf("Evaluated[2] = %+v, want selected", snap.Evaluated[2])
	}

	// Verify JSON serialization into detail format
	bytes, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal RoutingMetadata: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if raw["selected"] != "opencode/space-bunny-free" {
		t.Errorf("raw[selected] = %v, want opencode/space-bunny-free", raw["selected"])
	}
}
