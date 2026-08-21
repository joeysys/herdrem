// truncLines.ts — truncate long horizontal separator lines to fit container width.
//
// Agent terminals (e.g. Claude) draw box-drawing borders using characters like
// ─ ━ ═ ─ ╌ ┄ etc. These lines are generated for the local terminal width and
// can be very long. On the web, if the container is narrower, the lines would
// overflow horizontally. This function truncates lines that consist primarily
// of horizontal line/box-drawing characters to the given character width.

import { stripAnsi } from './ansi'

// Characters that count as "horizontal line" characters
const LINE_CHARS = new Set([
  '─', '━', '═', '─', '╌', '╍', '┄', '┅', '┆', '┇', '┈', '┉', '┊', '┋',
  '¯', '‾', '▔', '─',
])

// Corner/junction characters that can appear at the start of separator lines
const CORNER_CHARS = new Set([
  '┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼',
  '╔', '╗', '╚', '╝', '╠', '╣', '╦', '╩', '╬',
  '╓', '╖', '╙', '╜', '╟', '╢', '╤', '╧', '╫',
])

/**
 * Check if a plain-text line is a "separator line" — i.e. it consists
 * primarily of horizontal line characters, optionally with corner chars
 * at the start/end. Lines must have at least 3 line chars to qualify.
 */
function isSeparatorLine(plain: string): boolean {
  let lineCount = 0
  let otherCount = 0
  for (const ch of plain) {
    if (LINE_CHARS.has(ch)) {
      lineCount++
    } else if (CORNER_CHARS.has(ch) || ch === ' ' || ch === '\r') {
      // corners and spaces are OK in separator lines
    } else {
      otherCount++
    }
  }
  // Must be mostly line chars, and have at least 3
  return lineCount >= 3 && otherCount <= 2
}

/**
 * Truncate separator lines in raw ANSI text to fit maxChars width.
 * Only truncates trailing horizontal line characters; preserves ANSI styling
 * and any non-line content at the start of the line.
 *
 * Strategy: split by \n, for each line check if the stripped text is a
 * separator line. If so, find where to cut: keep characters up to maxChars,
 * then remove trailing line/corner characters beyond that point.
 */
export function truncateSeparators(raw: string, maxChars: number): string {
  if (maxChars < 10) return raw
  const lines = raw.split('\n')
  let modified = false

  for (let idx = 0; idx < lines.length; idx++) {
    const line = lines[idx]
    const plain = stripAnsi(line)
    if (!isSeparatorLine(plain)) continue
    if (plain.length <= maxChars) continue

    // Need to truncate: keep the first maxChars visible chars,
    // then strip the rest (which should all be line/corner chars).
    // We walk through the ANSI line tracking visible char position.
    let visiblePos = 0
    let allLineAfter = true

    const ANSI_RE = /\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|.)/g
    let lastTextEnd = 0
    let m: RegExpExecArray | null

    while ((m = ANSI_RE.exec(line)) !== null) {
      // Text before this escape sequence
      const textChunk = line.slice(lastTextEnd, m.index)
      for (const ch of textChunk) {
        if (visiblePos >= maxChars) {
          // Check if remaining chars are all line/corner chars
          if (!LINE_CHARS.has(ch) && !CORNER_CHARS.has(ch) && ch !== ' ' && ch !== '\r') {
            allLineAfter = false
          }
        }
        visiblePos++
      }
      lastTextEnd = ANSI_RE.lastIndex
    }
    // Remaining text after last escape
    const tail = line.slice(lastTextEnd)
    for (const ch of tail) {
      if (visiblePos >= maxChars) {
        if (!LINE_CHARS.has(ch) && !CORNER_CHARS.has(ch) && ch !== ' ' && ch !== '\r') {
          allLineAfter = false
        }
      }
      visiblePos++
    }

    if (!allLineAfter) continue

    // Now truncate: walk again and cut at maxChars visible chars
    visiblePos = 0
    let result = ''
    lastTextEnd = 0
    ANSI_RE.lastIndex = 0
    let done = false

    while (!done && (m = ANSI_RE.exec(line)) !== null) {
      const textChunk = line.slice(lastTextEnd, m.index)
      for (const ch of textChunk) {
        if (visiblePos < maxChars) {
          result += ch
        } else {
          done = true
          break
        }
        visiblePos++
      }
      if (!done) {
        result += line.slice(m.index, ANSI_RE.lastIndex)
        lastTextEnd = ANSI_RE.lastIndex
      }
    }
    if (!done) {
      const tailText = line.slice(lastTextEnd)
      for (const ch of tailText) {
        if (visiblePos < maxChars) {
          result += ch
        } else {
          break
        }
        visiblePos++
      }
    }

    lines[idx] = result
    modified = true
  }

  return modified ? lines.join('\n') : raw
}
