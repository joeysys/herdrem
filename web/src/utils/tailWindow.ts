/**
 * tailWindow keeps only the last `maxLines` visual lines of raw terminal content.
 *
 * Rationale: pane reads fetch ~500 lines (~100KB of ANSI) which render to
 * thousands of styled spans. On low-end phones each full re-render costs
 * ~100ms+ of main-thread time (DOM rebuild + layout) per poll, which makes the
 * UI janky while an agent is streaming output. The viewport shows ~40 lines,
 * so rendering a bounded tail window cuts that cost by an order of magnitude.
 *
 * A "visual line" is text between newlines; a trailing newline does not add an
 * extra empty line (a final newline merely terminates the last visual line).
 * Returns the input unchanged when it fits within the window, so callers can
 * rely on reference equality to skip re-renders.
 *
 * ANSI state preservation: cutting at a line boundary can discard an active
 * SGR style whose reset falls after the cutoff (e.g. a long-lived ESC[31m
 * block spanning many lines). To keep the rendered tail faithful, the active
 * style at the cutoff point is replayed as a prefix on the sliced result.
 */

const SGR_RE = /\x1b\[([0-9;]*)m/g

/** activeSgrPrefix extracts the SGR state active at position `end` of `raw`
 *  and returns a CSI sequence that re-establishes it (e.g. "\x1b[31m").
 *  Returns '' when the state is default (nothing to replay). */
export function activeSgrPrefix(raw: string, end: number): string {
  // Scan the complete prefix. ANSI state can legitimately span more than the
  // rendered window (and more than a small heuristic scan budget), so a
  // bounded rescan would silently lose long-lived styles.
  let fg: number[] | null = null
  let bg: number[] | null = null
  let bold = false
  let dim = false
  let italic = false
  let underline = false
  let strike = false
  let inverse = false

  SGR_RE.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = SGR_RE.exec(raw)) !== null) {
    if (m.index >= end) break
    const codes = (m[1] === '' ? ['0'] : m[1].split(';')).map((p) => parseInt(p, 10) || 0)
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i]
      if (isNaN(c)) continue
      if (c === 0) {
        fg = null; bg = null; bold = dim = italic = underline = strike = inverse = false
      } else if (c === 1) bold = true
      else if (c === 2) dim = true
      else if (c === 3) italic = true
      else if (c === 4) underline = true
      else if (c === 7) inverse = true
      else if (c === 9) strike = true
      else if (c === 22) { bold = false; dim = false }
      else if (c === 23) italic = false
      else if (c === 24) underline = false
      else if (c === 27) inverse = false
      else if (c === 29) strike = false
      else if (c >= 30 && c <= 37) fg = [c]
      else if (c === 39) fg = null
      else if (c === 38 && codes[i + 1] === 5 && codes[i + 2] !== undefined) {
        fg = [38, 5, codes[i + 2]]
        i += 2
      } else if (c === 38 && codes[i + 1] === 2 && codes[i + 4] !== undefined) {
        fg = [38, 2, codes[i + 2], codes[i + 3], codes[i + 4]]
        i += 4
      } else if (c >= 40 && c <= 47) bg = [c]
      else if (c === 49) bg = null
      else if (c >= 90 && c <= 97) fg = [c]
      else if (c === 48 && codes[i + 1] === 5 && codes[i + 2] !== undefined) {
        bg = [48, 5, codes[i + 2]]
        i += 2
      } else if (c === 48 && codes[i + 1] === 2 && codes[i + 4] !== undefined) {
        bg = [48, 2, codes[i + 2], codes[i + 3], codes[i + 4]]
        i += 4
      } else if (c >= 100 && c <= 107) bg = [c]
    }
  }
  if (fg === null && bg === null && !bold && !dim && !italic && !underline && !strike && !inverse) {
    return ''
  }
  const codes: string[] = []
  if (fg !== null) codes.push(...fg.map(String))
  if (bg !== null) codes.push(...bg.map(String))
  if (bold) codes.push('1')
  if (dim) codes.push('2')
  if (italic) codes.push('3')
  if (underline) codes.push('4')
  if (inverse) codes.push('7')
  if (strike) codes.push('9')
  return `\x1b[${codes.join(';')}m`
}

export interface TailWindowResult {
  text: string
  /** number of raw bytes dropped from the front (the window offset) */
  droppedBytes: number
  /** number of visual lines dropped from the front */
  droppedLines: number
}

/** computeTailWindow is the core windowing logic; it returns the sliced text
 *  (with any replayed ANSI prefix) plus how many raw bytes/lines were dropped
 *  from the front so callers can translate scroll offsets between windowed and
 *  full rendering. */
export function computeTailWindow(raw: string, maxLines: number): TailWindowResult {
  if (!raw || maxLines <= 0) return { text: raw, droppedBytes: 0, droppedLines: 0 }
  const n = raw.length
  // Count newlines once to determine how many visual lines there are.
  let newlines = 0
  for (let i = 0; i < n; i++) {
    if (raw.charCodeAt(i) === 10) newlines++
  }
  const visualLines = newlines + (raw.charCodeAt(n - 1) === 10 ? 0 : 1)
  if (visualLines <= maxLines) return { text: raw, droppedBytes: 0, droppedLines: 0 }

  // Drop the first (visualLines - maxLines) visual lines: advance past that
  // many newlines, then slice. No intermediate copies.
  const drop = visualLines - maxLines
  let seen = 0
  let idx = 0
  for (let i = 0; i < n && seen < drop; i++) {
    if (raw.charCodeAt(i) === 10) {
      seen++
      idx = i + 1
    }
  }
  const cut = raw.slice(idx)
  const prefix = activeSgrPrefix(raw, idx)
  return { text: prefix ? prefix + cut : cut, droppedBytes: idx, droppedLines: drop }
}

export function tailWindow(raw: string, maxLines: number): string {
  return computeTailWindow(raw, maxLines).text
}
