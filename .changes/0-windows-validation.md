### Fixed

- Preserve the full executable path in daemon PID claims when installation or user-profile directories contain spaces. Normalize live process image paths with the existing resolver so Windows 8.3 names match the daemon's recorded path.
- Keep LF-delimited golden fixtures stable under Windows Git checkout conversion. Validate junction targets and containment without pinning incidental standard-library path spelling, and allow valid 8.3 path components in client-config checks.
- Scope usage-chart keyboard assertions to the chart's status region so the update banner does not collide with the tooltip locator.

### Validation

- Full Go test suite and integration suite, Go vet, frontend unit tests, oxlint, Svelte ratchet, frontend build and CGO-disabled Windows binary build.
- Browser acceptance suite against the rebuilt binary, covering desktop and mobile dashboard flows.
- The Svelte ratchet retains its existing baseline of 75 type errors with no unresolved identifiers. Provider calls in the integration suite use isolated fake upstreams.
