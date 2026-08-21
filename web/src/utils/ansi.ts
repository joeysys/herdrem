// ansi.ts — render ANSI-escaped terminal text to HTML.
//
// Supports 16/256/truecolor SGR colors plus
// bold/dim/italic/underline/strikethrough/inverse. Non-SGR
// sequences (CSI cursor movement, OSC, single-char escapes) are dropped.
//
// The renderer can also decorate each plain-text segment (e.g. linkify file
// paths) before it is wrapped in a style span, via ansiToHtmlWith.

export interface AnsiState {
  fg: string | null
  bg: string | null
  bold: boolean
  dim: boolean
  italic: boolean
  underline: boolean
  strike: boolean
  inverse: boolean
}

// VS Code terminal default 16-color palette.
export const STANDARD_COLORS = [
  '#000000', '#cd3131', '#0dbc79', '#e5e510',
  '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5',
  '#666666', '#f14c4c', '#23d18b', '#f5f543',
  '#3b8eea', '#d670d6', '#29b8db', '#ffffff',
]

const ANSI_RE = /\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|.)/g

/** stripAnsi removes all ANSI escape sequences (used for previews/parsing). */
export function stripAnsi(raw: string): string {
  return raw.replace(ANSI_RE, '')
}

/** ansi256ToCss maps a 256-palette index to a CSS color. */
export function ansi256ToCss(n: number): string {
  if (n < 0) n = 0
  if (n > 255) n = 255
  if (n < 16) return STANDARD_COLORS[n]
  if (n < 232) {
    const idx = n - 16
    const levels = [0, 95, 135, 175, 215, 255]
    const r = levels[Math.floor(idx / 36)]
    const g = levels[Math.floor((idx % 36) / 6)]
    const b = levels[idx % 6]
    return `rgb(${r},${g},${b})`
  }
  const v = 8 + (n - 232) * 10
  return `rgb(${v},${v},${v})`
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function resetState(s: AnsiState): void {
  s.fg = null
  s.bg = null
  s.bold = false
  s.dim = false
  s.italic = false
  s.underline = false
  s.strike = false
  s.inverse = false
}

function styleAttr(s: AnsiState): string {
  const css: string[] = []
  if (s.inverse) {
    // Swap fg/bg; fall back to terminal defaults for unset sides.
    css.push(`color:${s.bg ?? 'var(--term-bg)'}`)
    css.push(`background-color:${s.fg ?? 'var(--term-fg)'}`)
  } else {
    if (s.fg) css.push(`color:${s.fg}`)
    if (s.bg) css.push(`background-color:${s.bg}`)
  }
  if (s.bold) css.push('font-weight:700')
  if (s.dim) css.push('opacity:.62')
  if (s.italic) css.push('font-style:italic')
  const deco: string[] = []
  if (s.underline) deco.push('underline')
  if (s.strike) deco.push('line-through')
  if (deco.length) css.push(`text-decoration:${deco.join(' ')}`)
  return css.join(';')
}

function applySgr(s: AnsiState, params: string): void {
  if (params === '') {
    resetState(s)
    return
  }
  const codes = params.split(';').map((p) => {
    const n = parseInt(p, 10)
    return p === '' || isNaN(n) ? 0 : n
  })
  for (let k = 0; k < codes.length; k++) {
    const c = codes[k]
    if (c === 0) resetState(s)
    else if (c === 1) s.bold = true
    else if (c === 2) s.dim = true
    else if (c === 3) s.italic = true
    else if (c === 4) s.underline = true
    else if (c === 7) s.inverse = true
    else if (c === 9) s.strike = true
    else if (c === 22) { s.bold = false; s.dim = false }
    else if (c === 23) s.italic = false
    else if (c === 24) s.underline = false
    else if (c === 27) s.inverse = false
    else if (c === 29) s.strike = false
    else if (c >= 30 && c <= 37) s.fg = STANDARD_COLORS[c - 30]
    else if (c === 38) {
      if (codes[k + 1] === 5) { s.fg = ansi256ToCss(codes[k + 2]); k += 2 }
      else if (codes[k + 1] === 2) { s.fg = `rgb(${codes[k + 2] & 255},${codes[k + 3] & 255},${codes[k + 4] & 255})`; k += 4 }
    } else if (c === 39) s.fg = null
    else if (c >= 40 && c <= 47) s.bg = STANDARD_COLORS[c - 40]
    else if (c === 48) {
      if (codes[k + 1] === 5) { s.bg = ansi256ToCss(codes[k + 2]); k += 2 }
      else if (codes[k + 1] === 2) { s.bg = `rgb(${codes[k + 2] & 255},${codes[k + 3] & 255},${codes[k + 4] & 255})`; k += 4 }
    } else if (c === 49) s.bg = null
    else if (c >= 90 && c <= 97) s.fg = STANDARD_COLORS[c - 90 + 8]
    else if (c >= 100 && c <= 107) s.bg = STANDARD_COLORS[c - 100 + 8]
  }
}

/** ansiToHtml renders ANSI text to HTML spans (no link decoration). */
export function ansiToHtml(raw: string): string {
  return ansiToHtmlWith(raw, (escaped) => escaped)
}

/**
 * ansiToHtmlWith renders ANSI text to HTML, passing each HTML-escaped plain
 * text segment through decorateText (e.g. linkifyFilePaths) so links can be
 * injected without breaking style spans.
 */
export function ansiToHtmlWith(raw: string, decorateText: (escaped: string) => string): string {
  const state: AnsiState = {
    fg: null, bg: null, bold: false, dim: false,
    italic: false, underline: false, strike: false, inverse: false,
  }
  let out = ''
  let currentCss = ''
  let spanOpen = false

  const closeSpan = () => {
    if (spanOpen) { out += '</span>'; spanOpen = false }
  }
  const sync = () => {
    const css = styleAttr(state)
    if (css === currentCss) return
    closeSpan()
    if (css) {
      // Spans carrying a background get a marker class so AnsiStream CSS can
      // extend the paint over the line-height leading (otherwise consecutive
      // background lines show gaps at line-height > 1).
      const cls = css.includes('background-color') ? ' class="has-bg"' : ''
      out += `<span${cls} style="${css}">`
      spanOpen = true
    }
    currentCss = css
  }
  const emitText = (text: string) => {
    if (text) out += decorateText(esc(text))
  }

  let i = 0
  let textStart = 0
  const n = raw.length
  while (i < n) {
    if (raw[i] === '\x1b') {
      emitText(raw.slice(textStart, i))
      if (raw[i + 1] === '[') {
        // CSI: read until final byte @-~
        let j = i + 2
        while (j < n && !(raw[j] >= '@' && raw[j] <= '~')) j++
        if (j >= n) { i = n; textStart = n; break }
        if (raw[j] === 'm') {
          applySgr(state, raw.slice(i + 2, j))
          sync()
        }
        i = j + 1
        textStart = i
      } else if (raw[i + 1] === ']') {
        // OSC: skip until BEL or ESC\
        let j = i + 2
        while (j < n) {
          if (raw[j] === '\x07') { j++; break }
          if (raw[j] === '\x1b' && raw[j + 1] === '\\') { j += 2; break }
          j++
        }
        i = j
        textStart = i
      } else {
        // single-char escape: skip ESC + next char
        i += 2
        textStart = i
      }
    } else {
      i++
    }
  }
  emitText(raw.slice(textStart))
  closeSpan()
  return out
}
