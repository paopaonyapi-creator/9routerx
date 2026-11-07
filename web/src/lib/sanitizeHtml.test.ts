// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
import { describe, expect, it } from 'bun:test'
import { isSafeUrl, sanitizeHtml } from './sanitizeHtml'

// bun test runs without a DOM, so sanitizeHtml takes its no-document
// degradation path here: markup is tag-stripped. The URL allowlist and the
// degradation are both pinned below; the full DOM walk is exercised in the
// browser by the e2e suite (no script executes from a sanitized changelog).

describe('isSafeUrl allowlist', () => {
  it('allows http(s), mailto, tel, fragments and relative paths', () => {
    expect(isSafeUrl('https://example.com/a')).toBe(true)
    expect(isSafeUrl('http://localhost:20131/changelog')).toBe(true)
    expect(isSafeUrl('mailto:ops@example.com')).toBe(true)
    expect(isSafeUrl('tel:+123')).toBe(true)
    expect(isSafeUrl('#section')).toBe(true)
    expect(isSafeUrl('/docs/page')).toBe(true)
    expect(isSafeUrl('changelog.md')).toBe(true)
  })

  it('rejects script-bearing schemes', () => {
    expect(isSafeUrl('javascript:alert(1)')).toBe(false)
    expect(isSafeUrl('data:text/html;base64,PHNjcmlwdD4=')).toBe(false)
    expect(isSafeUrl('vbscript:msgbox')).toBe(false)
    expect(isSafeUrl('file:///etc/passwd')).toBe(false)
  })

  it('rejects whitespace-hidden scheme smuggling', () => {
    expect(isSafeUrl('java\tscript:alert(1)')).toBe(false)
    expect(isSafeUrl(' jav\nascript:alert(1)')).toBe(false)
  })
})

describe('sanitizeHtml without a DOM', () => {
  it('strips markup instead of returning raw html', () => {
    const out = sanitizeHtml('<p>hello</p><script>alert(1)</script>')
    expect(out).not.toContain('<')
    expect(out).toContain('hello')
  })
})
