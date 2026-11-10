### Added

- B6 bounds for the combo fallback walk: a per-request **attempt cap** (`attemptCap` — `global`/`providers`/`models` tiers, most-specific-wins, a tier set to `0` disables the cap for everything it covers) and a **zero-retry dedup** (`retryDedupEnabled`) that skips a candidate which would receive a byte-identical body one dispatch already failed, so a retry storm cannot double-bill the same prompt. Both off by default; absent keys keep the pre-setting behaviour.
- A3 **Context Window Guard** (`contextGuardEnabled`, default on; `contextGuardMode` = `skip` | `truncate`) — combo fallback skips a candidate whose context window cannot hold the request's estimated prompt tokens; `truncate` drops middle history to fit.
- C1 **Budget Guard / Mode Hemat** (`budgetFallbackEnabled` + `budgetFallbackModel`) — a key that has exhausted its period budget is downgraded to the fallback model instead of rejected, dispatched through both chat formats.
- Per-candidate routing metadata: combo walks record each model/provider's evaluation (selected/skipped/failed + reason) on the request context, and a breaker-skip test pins that providers with an open circuit breaker are skipped without dispatch.

### Validation

- Full Go unit suite and go vet re-verified on the rebased base; the feature integration suite (real router, real DB, fake upstreams) covers the 429 contract, absent spend headers, reset timing and economy dispatch.
