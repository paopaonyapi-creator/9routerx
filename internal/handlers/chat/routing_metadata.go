// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"sync"
)

// CandidateEvaluation records one model/provider's evaluation outcome in a routing decision.
type CandidateEvaluation struct {
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
	Status   string `json:"status"` // "selected" | "skipped" | "failed"
	Reason   string `json:"reason,omitempty"`
}

// RoutingMetadata holds explainable route data for a request per B7 spec.
type RoutingMetadata struct {
	Strategy   string                `json:"strategy,omitempty"`
	Candidates []string              `json:"candidates,omitempty"`
	Selected   string                `json:"selected,omitempty"`
	Reason     string                `json:"reason,omitempty"`
	Evaluated  []CandidateEvaluation `json:"evaluated,omitempty"`
}

// RoutingRecorder collects candidate evaluations and final selection for one request.
type RoutingRecorder struct {
	mu   sync.Mutex
	data RoutingMetadata
}

// NewRoutingRecorder creates a fresh recorder.
func NewRoutingRecorder() *RoutingRecorder {
	return &RoutingRecorder{}
}

type routingRecorderCtxKey struct{}

// WithRoutingRecorder binds a RoutingRecorder to ctx.
func WithRoutingRecorder(ctx context.Context, r *RoutingRecorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, routingRecorderCtxKey{}, r)
}

// RoutingRecorderFrom recovers the recorder from ctx.
func RoutingRecorderFrom(ctx context.Context) *RoutingRecorder {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(routingRecorderCtxKey{}).(*RoutingRecorder)
	return r
}

// SetCandidates records the candidate seats and strategy considered for this request.
func (r *RoutingRecorder) SetCandidates(strategy string, candidates []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data.Strategy = strategy
	r.data.Candidates = append([]string(nil), candidates...)
}

// RecordEvaluation records one candidate evaluation (e.g. skipped or failed).
func (r *RoutingRecorder) RecordEvaluation(model, provider, status, reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data.Evaluated = append(r.data.Evaluated, CandidateEvaluation{
		Model:    model,
		Provider: provider,
		Status:   status,
		Reason:   reason,
	})
}

// RecordSelection records the chosen candidate and why it was selected.
func (r *RoutingRecorder) RecordSelection(model, provider, reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	selected := model
	if provider != "" && model != "" {
		selected = provider + "/" + model
	}
	r.data.Selected = selected
	r.data.Reason = reason
	r.data.Evaluated = append(r.data.Evaluated, CandidateEvaluation{
		Model:    model,
		Provider: provider,
		Status:   "selected",
		Reason:   reason,
	})
}

// Snapshot returns an immutable copy of the recorded routing metadata.
func (r *RoutingRecorder) Snapshot() RoutingMetadata {
	if r == nil {
		return RoutingMetadata{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.data
	out.Candidates = append([]string(nil), r.data.Candidates...)
	out.Evaluated = append([]CandidateEvaluation(nil), r.data.Evaluated...)
	return out
}

// RecordCandidateSkip is a package helper to record a candidate skip reason on ctx.
func RecordCandidateSkip(ctx context.Context, model, provider, reason string) {
	if rec := RoutingRecorderFrom(ctx); rec != nil {
		rec.RecordEvaluation(model, provider, "skipped", reason)
	}
}

// RecordCandidateSelection is a package helper to record candidate selection on ctx.
func RecordCandidateSelection(ctx context.Context, model, provider, reason string) {
	if rec := RoutingRecorderFrom(ctx); rec != nil {
		rec.RecordSelection(model, provider, reason)
	}
}
