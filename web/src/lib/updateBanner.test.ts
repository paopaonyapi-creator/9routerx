// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
import { afterEach, beforeEach, describe, expect, it } from 'bun:test'
import {
  ASSET_BY_OS,
  NPM_PACKAGE_CMD,
  SKIP_STORAGE_KEY,
  assetForUserAgent,
  isVersionSkipped,
  readSkippedVersions,
  releaseUrlTrust,
  skipVersion
} from './updateBanner'

describe('asset per-OS mapping', () => {
  it('follows the documented naming convention', () => {
    expect(ASSET_BY_OS.windows).toBe('9routerx_windows_amd64.exe')
    expect(ASSET_BY_OS.macos).toBe('9routerx_darwin_arm64.tar.gz')
    expect(ASSET_BY_OS.linux).toBe('9routerx_linux_amd64.tar.gz')
  })

  it('maps a Windows UA to the windows asset', () => {
    expect(assetForUserAgent('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36')).toBe(
      ASSET_BY_OS.windows
    )
  })

  it('maps a macOS UA to the darwin asset', () => {
    expect(assetForUserAgent('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)')).toBe(ASSET_BY_OS.macos)
  })

  it('maps a Linux UA to the linux asset', () => {
    expect(assetForUserAgent('Mozilla/5.0 (X11; Linux x86_64)')).toBe(ASSET_BY_OS.linux)
  })

  it('degrades to the linux tarball for an unknown or empty UA', () => {
    expect(assetForUserAgent('')).toBe(ASSET_BY_OS.linux)
    expect(assetForUserAgent('weird-robot-browser')).toBe(ASSET_BY_OS.linux)
  })
})

describe('release URL trust (anti link injection)', () => {
  it('links the real release host', () => {
    expect(releaseUrlTrust('https://github.com/yanstd888/9RouterX/releases/tag/v0.6.0')).toBe('trusted')
  })

  it('refuses lookalike hosts and other repos', () => {
    expect(releaseUrlTrust('https://github.com.evil.example/yanstd888/9routerx/releases')).toBe('untrusted')
    expect(releaseUrlTrust('https://github.com/other/9routerx/releases/latest')).toBe('untrusted')
    expect(releaseUrlTrust('https://gitlab.com/yanstd888/9routerx/releases')).toBe('untrusted')
  })

  it('refuses non-https and non-URL values', () => {
    expect(releaseUrlTrust('http://github.com/yanstd888/9routerx/releases')).toBe('untrusted')
    expect(releaseUrlTrust('javascript:alert(1)')).toBe('untrusted')
    expect(releaseUrlTrust('not a url')).toBe('untrusted')
  })

  it('reports an absent URL as absent, not untrusted', () => {
    expect(releaseUrlTrust(undefined)).toBe('absent')
    expect(releaseUrlTrust('')).toBe('absent')
  })
})

describe('skip list (localStorage)', () => {
  // Bun has no DOM storage; a stub is installed per test and the previous
  // descriptor restored after (same shape as displayPrefs.test.ts).
  let entries: Record<string, string> = {}
  const storageStub = {
    getItem: (key: string) => entries[key] ?? null,
    setItem: (key: string, value: string) => {
      entries[key] = value
    },
    removeItem: (key: string) => {
      delete entries[key]
    },
    clear: () => {
      entries = {}
    }
  }
  const previousDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')

  beforeEach(() => {
    entries = {}
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      writable: true,
      value: storageStub
    })
  })

  afterEach(() => {
    if (previousDescriptor) {
      Object.defineProperty(globalThis, 'localStorage', previousDescriptor)
    } else {
      Reflect.deleteProperty(globalThis, 'localStorage')
    }
  })

  it('starts empty', () => {
    expect(readSkippedVersions()).toEqual([])
    expect(isVersionSkipped('1.9.17')).toBe(false)
  })

  it('stores skipped versions and reports them', () => {
    skipVersion('1.9.17')
    skipVersion('1.9.17') // idempotent
    expect(readSkippedVersions()).toEqual(['1.9.17'])
    expect(isVersionSkipped('1.9.17')).toBe(true)
    expect(isVersionSkipped('1.9.18')).toBe(false)
  })

  it('ignores corrupted storage instead of throwing', () => {
    localStorage.setItem(SKIP_STORAGE_KEY, 'not-json{')
    expect(readSkippedVersions()).toEqual([])
  })

  it('ignores non-string entries', () => {
    localStorage.setItem(SKIP_STORAGE_KEY, '[1, null, "1.9.17"]')
    expect(readSkippedVersions()).toEqual(['1.9.17'])
  })

  it('never skips without a version', () => {
    expect(isVersionSkipped(undefined)).toBe(false)
    expect(isVersionSkipped('')).toBe(false)
  })

  it('the npm command is the static, documented string', () => {
    expect(NPM_PACKAGE_CMD).toBe('npm i -g 9routerx@latest')
  })
})
