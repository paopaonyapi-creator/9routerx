// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import "testing"

// resetStreamStateForTests empties the package-level stream session state so a
// test that drives the session-less TranslateOpenAIToClaudeStream (whose state
// is keyed by chunk.ID in a global map) starts from a clean slate. Without it
// the second -count=2 iteration sees run one's MessageStartSent/FinishReason
// and every "first chunk" assertion fails — the production callers clear state
// with a deferred ClearStreamState; tests must do the same bookkeeping.
func resetStreamStateForTests(t *testing.T) {
	t.Helper()
	statesMu.Lock()
	defer statesMu.Unlock()
	states = make(map[string]*StreamState)
	pendingJSON = make(map[string]pendingFragment)
	promptEstimates = make(map[string]promptEstimate)
}
