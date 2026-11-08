// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

// OrderCandidates orders the fallback chain's candidate providers.
//
// The operator's configured order (fallback.free.order) leads — names are
// matched as given, so both ids and aliases work. Named candidates that are
// also in the eligible set keep their configured sequence; eligible
// candidates the operator did not name follow, ordered by health score
// descending (healthiest first). scoreOf is the tracker's HealthScore, which
// already sorts cooling providers to the very end.
//
// The result is a new slice; the input is not modified.
func OrderCandidates(configured []string, eligible []string, scoreOf func(string) float64) []string {
	if len(eligible) == 0 {
		return nil
	}
	eligibleSet := make(map[string]bool, len(eligible))
	for _, id := range eligible {
		eligibleSet[id] = true
	}

	ordered := make([]string, 0, len(eligible))
	seen := make(map[string]bool, len(eligible))
	for _, name := range configured {
		if eligibleSet[name] && !seen[name] {
			ordered = append(ordered, name)
			seen[name] = true
		}
	}

	var rest []string
	for _, id := range eligible {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	if scoreOf != nil {
		// Stable sort so equal scores keep the caller's order deterministic.
		sortByScoreStable(rest, scoreOf)
	}
	return append(ordered, rest...)
}

// sortByScoreStable sorts ids by score descending, keeping the input order
// for equal scores. Insertion sort on purpose: the candidate set is a few
// dozen at most and a stable O(n^2) pass is cheaper to reason about than a
// comparator that must also break ties deterministically.
func sortByScoreStable(ids []string, scoreOf func(string) float64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && scoreOf(ids[j]) > scoreOf(ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
