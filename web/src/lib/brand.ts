// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id

/**
 * Passive origin canaries for the dashboard (Go counterpart:
 * internal/brand/origin.go). The repo URL is stored as runtime-joined
 * fragments and the attribution line as base64, so a blanket find&replace of
 * the account name cannot scrub every reference in one pass. Rendered in the
 * sidebar footer; nothing here tracks, beacons, or reads the network.
 */
const REPO_HOST = 'github.com/'
const REPO_USER_A = 'yanstd'
const REPO_USER_B = '888'
const REPO_NAME = '9RouterX'

export const BRAND_REPO = `https://${REPO_HOST}${REPO_USER_A}${REPO_USER_B}/${REPO_NAME}`

// Attribution line ("all source code port and modification by
// https://github.com/yanstd888/ — https://t.me/aiclub_id"), base64-wrapped;
// brandAttribution below is the only reader.
const ATTRIBUTION_B64 =
  'YWxsIHNvdXJjZSBjb2RlIHBvcnQgYW5kIG1vZGlmaWNhdGlvbiBieSBodHRwczovL2dpdGh1Yi5jb20veWFuc3RkODg4LyDigJQgaHR0cHM6Ly90Lm1lL2FpY2x1Yl9pZA=='

/** Decodes the wrapped attribution line; falls back to the literal itself. */
export function brandAttribution(): string {
  try {
    return new TextDecoder()
      .decode(Uint8Array.from(atob(ATTRIBUTION_B64), (c) => c.charCodeAt(0)))
      .trim()
  } catch {
    return ATTRIBUTION_B64
  }
}
