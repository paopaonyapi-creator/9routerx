// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
/**
 * Update banner helpers: per-OS asset naming, the skip list, and the one
 * place that decides whether a release URL may be linked.
 *
 * The asset naming convention and the channel behaviors are the contract
 * handed to the release-publishing agent (Bagian B) — they are documented in
 * docs/updater-contract.md and must move together with that document.
 */

/** The npm package the banner instructs users to install (Bagian B publishes it). */
export const NPM_PACKAGE_CMD = 'npm i -g 9routerx@latest'

/** localStorage key holding the JSON array of skipped version strings. */
export const SKIP_STORAGE_KEY = '9routerx.update.skipped'

/**
 * Asset naming convention per OS/arch — the constant map Bagian B names its
 * release assets after. `9routerx_{os}_{arch}` plus the platform suffix:
 * `.exe` on Windows, nothing on the tarballs.
 */
export const ASSET_BY_OS: Record<string, string> = {
  windows: '9routerx_windows_amd64.exe',
  macos: '9routerx_darwin_arm64.tar.gz',
  linux: '9routerx_linux_amd64.tar.gz'
}

/**
 * Maps a browser userAgent to the release asset this user should download.
 * Platform detection from a UA is best effort and documented as such:
 *  - "Windows" → windows
 *  - "Mac OS X"/"Macintosh" → macos (Apple Silicon is the published default;
 *    Intel macs read the release page — the UA cannot tell them apart)
 *  - anything else (Linux/Android/X11) → linux
 * An empty or unknown UA still answers linux, the tarball that runs
 * everywhere — a banner that cannot detect the OS must not render nothing.
 */
export function assetForUserAgent(userAgent: string): string {
  const ua = (userAgent || '').toLowerCase()
  if (ua.includes('windows')) return ASSET_BY_OS.windows
  if (ua.includes('mac os x') || ua.includes('macintosh')) return ASSET_BY_OS.macos
  return ASSET_BY_OS.linux
}

/**
 * The only host a release URL may be linked from. Remote data is never
 * trusted: a manifest that answers `releaseUrl: "javascript:..."` or a
 * lookalike domain must render as plain text, not a link.
 */
const TRUSTED_RELEASE_HOSTS = new Set(['github.com', 'www.github.com'])

/** releaseUrlTrust reports whether url is safe to render as a link. */
export function releaseUrlTrust(url: string | undefined | null): 'trusted' | 'untrusted' | 'absent' {
  if (!url) return 'absent'
  try {
    const parsed = new URL(url)
    if (parsed.protocol !== 'https:') return 'untrusted'
    if (!parsed.pathname.toLowerCase().startsWith('/yanstd888/9routerx')) return 'untrusted'
    return TRUSTED_RELEASE_HOSTS.has(parsed.hostname.toLowerCase()) ? 'trusted' : 'untrusted'
  } catch {
    return 'untrusted'
  }
}

/** Reads the stored skip list. A corrupted entry reads as empty, never throws. */
export function readSkippedVersions(): string[] {
  try {
    const raw = localStorage.getItem(SKIP_STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((v): v is string => typeof v === 'string')
  } catch {
    return []
  }
}

/** Adds one version to the skip list (idempotent). */
export function skipVersion(version: string): string[] {
  const current = readSkippedVersions()
  if (!current.includes(version)) {
    current.push(version)
    try {
      localStorage.setItem(SKIP_STORAGE_KEY, JSON.stringify(current))
    } catch {
      // Storage full or blocked (private mode): the banner returns next
      // session, which is the correct degraded behavior for a skipped update.
    }
  }
  return current
}

/** True when the given version has been skipped by the operator. */
export function isVersionSkipped(version: string | undefined | null): boolean {
  if (!version) return false
  return readSkippedVersions().includes(version)
}
