### Fixed

- Stop helper children from flashing console windows: every process spawn from the windowless tray companion (schtasks / powershell probes) gets `CREATE_NO_WINDOW` via the new `internal/hideconsole` package (no-op stubs on non-Windows builds).
- Tray autostart refresh tick reduced from 1s to 5s to cut CPU/disk churn.

### Validation

- Build/vet plus `internal/service` and `cmd/...` test suites green on a Windows host; tray companion rebuilt and run locally on Windows 11 — no console flash during autostart probes.
