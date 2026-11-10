// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package resilience

import "testing"

func TestRetryDedupMarksAndSkips(t *testing.T) {
	d := NewRetryDedup()
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)

	if d.ShouldSkip(body) {
		t.Fatal("fresh dedup must not skip an unmarked body")
	}
	d.Mark(body)
	if !d.ShouldSkip(body) {
		t.Fatal("marked body must be skipped on the next walk step")
	}

	mutated := make([]byte, len(body))
	copy(mutated, body)
	mutated[len(mutated)-2] = '2'
	if d.ShouldSkip(mutated) {
		t.Error("a different body must not be skipped")
	}
}

func TestRetryDedupNilRegistrySkipsNothing(t *testing.T) {
	var d *RetryDedup
	if d.ShouldSkip([]byte("anything")) {
		t.Error("nil registry is the unconfigured path and must skip nothing")
	}
	// Mark on nil must not panic either.
	d.Mark([]byte("anything"))
}

func TestRetryDedupHashIsStable(t *testing.T) {
	body := []byte(`{"a":1}`)
	if BodyHash(body) != BodyHash([]byte(`{"a":1}`)) {
		t.Error("equal bodies must hash equal")
	}
	if BodyHash(body) == BodyHash([]byte(`{"a":2}`)) {
		t.Error("different bodies must hash differently")
	}
}

func TestRetryDedupSeatIdentity(t *testing.T) {
	d := NewRetryDedup()
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	if d.ShouldSkipSeat(body, "openai", "gpt-4o") {
		t.Fatal("fresh dedup must not skip an unmarked seat")
	}
	d.MarkSeat(body, "openai", "gpt-4o")
	if !d.ShouldSkipSeat(body, "openai", "gpt-4o") {
		t.Fatal("marked seat must be skipped")
	}
	// A different seat (same body, different model) is a different upstream
	// request and must be dialled.
	if d.ShouldSkipSeat(body, "openai", "gpt-4o-mini") {
		t.Error("same body under a different model must not be skipped")
	}
	if d.ShouldSkipSeat(body, "anthropic", "gpt-4o") {
		t.Error("same body under a different provider must not be skipped")
	}
}
