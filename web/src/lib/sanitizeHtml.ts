// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
/**
 * Dependency-free HTML sanitizer for the three places the dashboard renders
 * server- or markdown-derived markup with {@html}: the changelog modal, the
 * update modal's release notes, and the persona prompt preview.
 *
 * The inputs are not first-party trusted: release notes and imported presets
 * come from remote sources, and a changelog ships with the binary but is read
 * back over HTTP. Rendering any of them unsanitized hands script execution to
 * whoever controls the source, inside a page that holds the dashboard session.
 *
 * The strategy is allowlisting by removal: parse into a detached document,
 * drop the elements that can execute or load anything on their own, strip
 * every event-handler and style attribute, and drop any URL attribute whose
 * scheme is not http(s) or a fragment/mailto. Everything else — the headings,
 * lists, code blocks, tables and links a changelog actually uses — is kept.
 */

/** Elements whose content or mere presence can execute or load resources. */
const REMOVED_TAGS = new Set([
  'script',
  'style',
  'iframe',
  'frame',
  'frameset',
  'object',
  'embed',
  'applet',
  'link',
  'meta',
  'base',
  'form',
  'input',
  'button',
  'select',
  'textarea',
  'svg',
  'math',
  'noscript',
  'template',
])

/** Attributes that carry executable or exfiltrating URLs. */
const URL_ATTRS = new Set(['href', 'src', 'xlink:href', 'action', 'formaction', 'poster', 'background', 'dynsrc', 'lowsrc'])

const SAFE_URL_RE = /^(?:https?:|mailto:|tel:|#|\/)/i

/** Exported for the unit tests; the sanitizer itself is the only caller. */
export function isSafeUrl(value: string): boolean {
  // Whitespace and control characters are stripped before the scheme check so
  // a tab inside the scheme (java\tscript:) cannot smuggle past the allowlist.
  const trimmed = value.trim().replace(/[\p{C}\s]/gu, '')
  // A scheme-ish prefix that is not an allowlisted scheme is rejected even
  // before the regex: `java\tscript:` evades naive matching.
  if (/^[a-z][a-z0-9+.-]*:/i.test(trimmed)) {
    return /^(?:https?|mailto|tel):/i.test(trimmed)
  }
  return SAFE_URL_RE.test(trimmed) || !trimmed.includes(':')
}

function sanitizeNode(node: Element): void {
  for (const child of Array.from(node.children)) {
    if (REMOVED_TAGS.has(child.tagName.toLowerCase())) {
      child.remove()
      continue
    }
    for (const attr of Array.from(child.attributes)) {
      const name = attr.name.toLowerCase()
      if (name.startsWith('on')) {
        child.removeAttribute(attr.name)
        continue
      }
      if (URL_ATTRS.has(name) && !isSafeUrl(attr.value)) {
        child.removeAttribute(attr.name)
        continue
      }
      // style can carry expression() payloads and exfil URLs; srcset loads
      // arbitrary resources. target is allowed, with the rel hardening below.
      if (name === 'style' || name === 'srcset') {
        child.removeAttribute(attr.name)
      }
    }
    // A link that survives keeps its href, but must not be able to open
    // window.opener when the changelog links out.
    if (child.tagName.toLowerCase() === 'a' && child.hasAttribute('target')) {
      child.setAttribute('rel', 'noopener noreferrer')
    }
    sanitizeNode(child)
  }
}

/**
 * Sanitize an HTML string for {@html} rendering. Returns the sanitized body
 * markup; outside a DOM (unit tests, SSR) it degrades to a tag-stripped
 * string so the call sites never render raw HTML by accident.
 */
export function sanitizeHtml(html: string): string {
  if (typeof document === 'undefined') {
    return html.replace(/<[^>]*>/g, '')
  }
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  sanitizeNode(parsed.body)
  // DOMParser leaves the parsed document inert: scripts inside it do not run,
  // and serializing body.innerHTML cannot resurrect an execution context.
  return parsed.body.innerHTML
}
