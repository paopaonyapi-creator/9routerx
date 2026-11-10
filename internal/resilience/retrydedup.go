// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package resilience

import "hash/fnv"

// RetryDedup remembers the request bodies a single client request has already
// sent upstream, so a retry storm cannot double-bill the same prompt: when a
// combo walk reaches a candidate that would receive a byte-identical body one
// it already failed, the candidate is skipped rather than dialled again.
//
// The registry lives for exactly one request and is built by the handler that
// owns the fallback walk; nothing here is shared between requests, so there is
// no mutex and no eviction — the lifetime of the map is the lifetime of the
// walk that created it.
//
// Identity is a 64-bit FNV-1a hash of the exact body bytes, not the parsed
// semantic: two dispatches that differ in model field, jailbreak injection or
// tool-id repair have different hashes and are both dialled, because they are
// genuinely different requests to the upstream. The false-positive rate of a
// 64-bit hash over the handful of bodies one request produces is negligible,
// and skipping a dial that would have succeeded costs the walk one candidate —
// the same price a failed dial costs, minus the quota it would have burned.
type RetryDedup struct {
	seen map[uint64]struct{}
}

// NewRetryDedup returns an empty registry for one request's dispatch walk.
func NewRetryDedup() *RetryDedup {
	return &RetryDedup{seen: make(map[uint64]struct{})}
}

// BodyHash derives the dedup identity of a request body. Exported so a test
// can assert on the hash without re-implementing FNV.
func BodyHash(body []byte) uint64 {
	h := fnv.New64a()
	// hash.Hash never returns an error on Write.
	_, _ = h.Write(body)
	return h.Sum64()
}

// Mark records a body as already dispatched. Call it right before (or right
// after) forwarding; the walk then treats the body as spent.
func (d *RetryDedup) Mark(body []byte) {
	if d == nil || d.seen == nil {
		return
	}
	d.seen[BodyHash(body)] = struct{}{}
}

// ShouldSkip reports whether this exact body has already been dispatched by
// the current walk. A nil registry — the unconfigured path — skips nothing,
// which is the behaviour every install had before this setting existed.
func (d *RetryDedup) ShouldSkip(body []byte) bool {
	if d == nil || d.seen == nil {
		return false
	}
	_, hit := d.seen[BodyHash(body)]
	return hit
}

// SeatHash derives the dedup identity of one combo seat: the request body
// paired with the provider/model it would be dialled under. The same body
// sent under a different seat is a genuinely different upstream request —
// a different model field, a different lane — so only the seat-level pair
// dedups. This is the granularity the combo walk marks at: once per entry,
// before its connection loop, so a multi-account provider still tries its
// remaining connections for the seat the dedup let through.
func SeatHash(body []byte, provider, model string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(body)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(provider))
	_, _ = h.Write([]byte{'/'})
	_, _ = h.Write([]byte(model))
	return h.Sum64()
}

// MarkSeat records one (body, provider, model) seat as dispatched.
func (d *RetryDedup) MarkSeat(body []byte, provider, model string) {
	if d == nil || d.seen == nil {
		return
	}
	d.seen[SeatHash(body, provider, model)] = struct{}{}
}

// ShouldSkipSeat reports whether this seat has already been dispatched by
// the current walk.
func (d *RetryDedup) ShouldSkipSeat(body []byte, provider, model string) bool {
	if d == nil || d.seen == nil {
		return false
	}
	_, hit := d.seen[SeatHash(body, provider, model)]
	return hit
}
