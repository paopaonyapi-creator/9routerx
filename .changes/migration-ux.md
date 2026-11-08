### Added
- Add confirmed `service take-over` migration, shared port/process diagnostics, first-run alternative-port selection and actionable noninteractive conflict errors.
- Inspect legacy autostart read-only and disable only exact legacy gateway path matches; preserve unrelated Node apps and 9RouterX entries, and report unresolved parent respawn sources.
- Verify the installed gateway with health/root fingerprint and read-only database counts; document per-OS autostart discovery and migration.
- Sync the site renderer's icon annotations with the current README and pin every current section in a regression test, restoring the existing Pages test gate without rewriting README content.

### Fixed
- Read GIF dimensions in the existing image-header helper so the current README hero passes the Pages layout-shift guard without changing the asset or README markup.
- Preserve executable paths containing spaces in daemon pidfiles.
- Persist the effective service port/data paths, keep supervised Windows launches out of the terminal picker, import long scheduled-task commands via UTF-16 XML, and place cron/desktop global flags before `start`.

Verification: `go build ./...`, internal race tests, isolated Windows scheduled-task/Run-key migration and real pseudo-console first-run checks. Upstream reference: decolua/9router commit 1339107e624d1ab150c6a0c2c2ee22caa7e9db2f; its broad shutdown helper also terminates unrelated matching processes/cloudflared, so migration uses a verified listener PID instead.
