// Install label baked into the PWA at `npm run build`. Each system sets
// HERDREM_PWA_NAME in the build environment; the running relay never reads it.

export const DEFAULT_PWA_NAME = 'Herdrem'
export const MAX_PWA_NAME_LENGTH = 48

const TITLE_MARKER = '<title>Herdrem</title>'
const APPLE_TITLE_MARKER = '<meta name="apple-mobile-web-app-title" content="Herdrem" />'

export function resolvePwaName(env: Record<string, string | undefined>): string {
  const raw = env.HERDREM_PWA_NAME ?? ''
  const cleaned = raw
    .replace(/[\t\n\r\f\v]/g, ' ')
    .replace(/[\u0000-\u001F\u007F-\u009F]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
  if (!cleaned) return DEFAULT_PWA_NAME
  // Slice can land on a space; trim again so the install label does not
  // end with whitespace.
  return Array.from(cleaned).slice(0, MAX_PWA_NAME_LENGTH).join('').trim() || DEFAULT_PWA_NAME
}

export function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (ch) => {
    switch (ch) {
      case '&':
        return '&amp;'
      case '<':
        return '&lt;'
      case '>':
        return '&gt;'
      case '"':
        return '&quot;'
      case "'":
        return '&#39;'
      default:
        return ch
    }
  })
}

// index.html keeps the default name as the marker. The build replaces those
// two spots; a missing marker means a later edit dropped the install label.
export function applyPwaNameToHtml(html: string, name: string): string {
  if (!html.includes(TITLE_MARKER) || !html.includes(APPLE_TITLE_MARKER)) {
    throw new Error('index.html is missing the Herdrem title markers')
  }
  const escaped = escapeHtml(name)
  // A function replacer inserts the string literally. A string replacer
  // would expand $$, $&, $` and $' inside the name.
  return html
    .replace(TITLE_MARKER, () => `<title>${escaped}</title>`)
    .replace(APPLE_TITLE_MARKER, () => `<meta name="apple-mobile-web-app-title" content="${escaped}" />`)
}
