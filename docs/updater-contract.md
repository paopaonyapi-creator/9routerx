# 9RouterX Updater Contract — handoff to the release-publishing agent (Bagian B)

This document is the single source of truth for the version feed that
`internal/updater` consumes and the dashboard banner (Bagian A, this repo)
renders. Bagian B (the GitHub repo + release pipeline + npm package) must
publish assets that match these names and this shape **exactly** — every key
below is read by code that ships today, and a mismatch degrades silently by
design (the banner just does not show).

Everything in Bagian A was built and verified against local mocks only; no
repository, workflow, or npm package was created or pushed.

---

## 1. version.json contract (merged)

Published at the hard-coded primary channel:

```
https://raw.githubusercontent.com/yanstd888/9routerx/main/version.json
```

with fallback to the GitHub Releases API:

```
https://api.github.com/repos/yanstd888/9routerx/releases/latest
```

Both hosts are compiled-in constants (`internal/updater/updater.go`,
`pinnedManifestHost`, `githubReleasesAPI`). Environment overrides
(`UPDATE_URL`, `UPDATE_REPO`) can select a different manifest *path* or repo
*name* on those pinned hosts, but can never point the updater at another host
(anti-SSRF; `TestManifestURL_HostPinned` proves a foreign host, an http
downgrade, and a lookalike domain are all ignored).

The manifest merges two generations of keys — all are accepted:

```json
{
  "version": "0.6.0",
  "minVersion": "0.5.0",
  "releaseUrl": "https://github.com/yanstd888/9RouterX/releases/tag/v0.6.0",
  "notes": "ringkasan singkat",
  "publishedAt": "2026-11-07T00:00:00Z",

  "latestVersion": "0.6.0",
  "downloadUrl": "https://github.com/yanstd888/9RouterX/releases/latest",
  "sha256": "<hex sha256 of the platform asset>",
  "releaseNotes": "optional longer notes"
}
```

| Key | Required | Read by | Notes |
|---|---|---|---|
| `version` or `latestVersion` | yes (one of them) | `checkManifest` | `latestVersion` wins if both present; leading `v` is trimmed |
| `downloadUrl` | for binary installs | `PerformSelfUpdate` | per-platform URL, see `downloadUrls` below |
| `sha256` | for binary installs | `PerformSelfUpdate` | a manifest that advertises an update **without** a digest falls through to the Releases API; an up-to-date manifest needs none |
| `downloadUrls` | optional | `checkManifest` | map keyed `"<goos>_<goarch>"` with a `"default"` fallback |
| `releaseNotes` / `notes` | optional | banner | either spelling; `releaseNotes` wins |
| `minVersion` | optional | banner (informational) | oldest release still supported |
| `releaseUrl` | optional | banner | **only linked when its host is github.com/yanstd888** (client-side `releaseUrlTrust`, server never follows it) |
| `publishedAt` | optional | debug dump | ISO 8601 |

The Releases API fallback fills `tag_name`, `body`, `assets` (and reads the
digest from a `SHA256SUMS.txt` asset). The Go type is
`updater.UpdateInfo` — the JSON the dashboard receives is
`GET /api/update/check` (`UpdateCheckResponse`): the fields above plus
`currentVersion`, `channel`, `cacheHit`, `inFlight`, `checkEnabled`, `stale`.

## 2. Asset naming per OS (the constant map)

The banner shows the asset the operator's browser should download, from
`web/src/lib/updateBanner.ts` (`ASSET_BY_OS`, unit-pinned):

| User-Agent platform | Asset name |
|---|---|
| Windows | `9routerx_windows_amd64.exe` |
| macOS (`Macintosh`/`Mac OS X`) | `9routerx_darwin_arm64.tar.gz` |
| everything else (Linux/Android/X11, unknown) | `9routerx_linux_amd64.tar.gz` |

Convention: `9routerx_{goos}_{goarch}` + platform suffix (`.exe` on Windows,
`.tar.gz` elsewhere). Bagian B must name release assets exactly like this —
the server-side matcher (`matchReleaseAsset`) also substring-matches
`{goos}` + arch aliases (`amd64/x86_64/x64`, `arm64/aarch64`) inside asset
names. Intel-mac caveat is documented in the map: UA detection cannot tell
arches apart, so mac defaults to the arm64 tarball and the release page link
is the fallback for Intel.

## 3. Environment, flags, settings (what Bagian A created)

| Name | Kind | Default | Meaning |
|---|---|---|---|
| `UPDATE_CHECK_INTERVAL` | env | `6h` | background ticker period. Go duration (`30m`, `1.5h`) or bare hours (`6`). `"0"` disables the ticker; dashboard-open checks still refresh a cache older than the freshness window. Unparsable = default ON. |
| `UPDATE_DEBUG` | env | off | `1` mirrors every check step (start, fetch result, decision, error+cause) verbosely to stdout as `[updater:debug] …` |
| `UPDATE_DEBUG_ENDPOINT` | env | off | `1` enables `GET /api/update/debug` (admin-gated; 404 otherwise) |
| `updateCheckEnabled` | settings key | **true** | the dashboard "Check updates" toggle (Settings page → Updates card). Live-applied; governs checking only |
| `autoUpdate` | settings key (pre-existing) | false | still governs the auto-APPLY flow (`PerformSelfUpdate`), untouched by the banner |

The check itself: fetch timeout **5s**, single in-flight fetch process-wide,
cache-first (`/api/update/check` never fetches per request). One background
ticker goroutine (`StartCheckLoop`), separate from the pre-existing
auto-apply cycle (`StartBackgroundCheck`), both writing the same cache.

## 4. Channels and banner behavior

`updater.DetectDistChannel()` classifies the build (first match wins):

1. `9ROUTERX_NPM_INSTALL=1` in the environment → **npm**. Bagian B's npm
   package bin wrapper must export this before exec'ing the Go binary.
2. `CurrentVersion` empty or `-dev`-suffixed → **dev** (no version metadata).
3. Executable path inside the Go build cache (`go run`) → **dev**.
4. Otherwise → **binary**.

Banner behavior per channel (`web/src/components/UpdateBanner.svelte`):

- **binary** — "Update now" opens `releaseUrl` (only if trusted, §1) and the
  banner shows the per-OS asset name + `sha256` when present. No
  auto-download; `9routerx update` remains the install path.
- **npm** — static monospace command `npm i -g 9routerx@latest` + copy
  button. The package name **`9routerx`** is fixed in
  `updateBanner.ts` (`NPM_PACKAGE_CMD`); Bagian B publishes under that name.
- **dev** — info line + release link only (no skip button).
- "Skip this version" (binary/npm) stores the version in
  `localStorage["9routerx.update.skipped"]`; skipped or equal/older versions
  render no banner.

## 5. Failure behavior (the graceful matrix, mocked in
`internal/updater/check_integration_test.go`)

Higher version → banner data; same → nothing; lower (rollback) → nothing.
404 / timeout (5s) / malformed JSON / HTTP 429 → silent degradation: the
endpoint answers 200 with the last cache and `stale: true`, the cause is
classified (`not_found`, `timeout`, `parse`, `rate_limited`, `dns`,
`network`) into the structured log, the check record, and `/api/update/debug`.

## 6. Rollback procedure (release drill — Bagian B §5.5)

How to pull a bad release off every channel. Order matters: the manifest is
what existing installs poll, so it goes last so users are not offered a
version that no longer exists anywhere.

**1. GitHub Release + tag** (the binary/self-update channel):

```sh
gh release delete vX.Y.Z --repo yanstd888/9routerx --cleanup-tag --yes
```

`--cleanup-tag` removes the git tag with it (equivalently
`git push --delete origin vX.Y.Z`). After deletion the Releases API fallback
404s — the client degrades silently (`stale: true`, no banner), which is the
desired "no update offered" state.

**2. Rewind the manifest** (`version.json` on `main` is what
`9routerx update` and the banner poll first — deleting the release does not
touch it):

```sh
./scripts/bump-version.sh <previous-stable>   # then commit + push to main
```

Until this is pushed, every install still sees the bad version as "latest"
from raw.githubusercontent.com even though its release page is gone — never
skip this step.

**3. npm** (wrapper `9routerx` + platform packages
`@yanstd888/9routerx-<os>-<arch>`):

- Within 72h of publish: `npm unpublish --force <pkg>@<version>` per package.
  After an unpublish, npm blocks republishing the same version for 24h —
  roll the version instead of retrying the same number.
- Past the 72h window unpublish is refused by npm policy: deprecate instead —
  `npm deprecate <pkg>@<version> "broken; upgrade to X.Y.Z"`. Deprecation
  surfaces a warning on install and keeps `npm i -g 9routerx@latest` working.
- The wrapper pins its `optionalDependencies` to exact versions, so a broken
  platform package is fixed by publishing `<patch+1>` of that one package and
  a wrapper release pinning it — no need to unpublish anything.

**4. Docker** (`yanstd88/9routerx`): tags are mutable — rebuild from the
previous good tag and push to overwrite `:latest`/`:1.Y`/`:1.Y.Z`
(the release workflow's docker job does this on any re-tag), or delete the
single bad tag in Docker Hub → Tags.

**Drill record:** the test release (`v0.0.0-test.1` pattern, spec §5.1/§5.5)
is deleted the same way after E2E passes: `gh release delete --cleanup-tag`,
manifest untouched (test versions never enter `version.json`).

---

*Bagian A (this repo): channel detection, check scheduler, banner UI, debug
endpoint, tests, screenshots (`audit-screens-updater/`, local-only). Bagian B
owns: creating the GitHub repo + version.json + releases with the asset names
above, the npm package + wrapper that sets `9ROUTERX_NPM_INSTALL=1`, and any
release automation.*
