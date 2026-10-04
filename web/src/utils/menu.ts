// menu.ts — client-side numbered-menu parser, mirroring internal/herdr/menu.go.
//
// Used as a fallback when the relay did not supply options for a blocked pane:
// the terminal approve bar can parse the pane's own text to recover real menu
// labels instead of guessing generic y/n.

const MENU_ITEM_RE = /^[\s│|┃╎┆]*(?:[❯>›»▶●•*\-]\s*)?[[\(]?([1-9])[\]\).:]\s+(\S.*?)[\s│|┃╎┆]*$/
const BORDER_RE = /^[│|┃╎┆]+/
// A horizontal rule: Claude's AskUserQuestion draws one before its trailing
// "Chat about this" item. It separates, it does not end, the menu.
const RULE_RE = /^[\s│|┃╎┆]*[─━═┄┅┈┉╌╍\-–—_]{3,}[\s│|┃╎┆]*$/

function isContinuation(line: string): boolean {
  const body = line.replace(BORDER_RE, '')
  return body.startsWith('  ') && body.trimStart().length > 0
}

/** parseNumberedMenu extracts (payloads, labels) from a numbered TUI menu. */
export function parseNumberedMenu(text: string): { payloads: string[]; labels: string[] } {
  const lines = String(text || '').split('\n')
  let run: string[] = []
  let prevLine = -1
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].replace(/\r$/, '')
    const m = MENU_ITEM_RE.exec(line)
    if (!m) continue
    const num = parseInt(m[1], 10)
    const label = m[2].trim()

    let adjacent = prevLine === -1
    if (prevLine !== -1) {
      adjacent = true
      for (let j = prevLine + 1; j < i; j++) {
        const between = lines[j].replace(/\r$/, '')
        if (!isContinuation(between) && !RULE_RE.test(between)) {
          adjacent = false
          break
        }
      }
    }

    if (num === run.length + 1 && adjacent) {
      run.push(label)
      prevLine = i
    } else if (num === 1) {
      run = [label]
      prevLine = i
    } else {
      run = []
      prevLine = -1
    }
    if (run.length === 5) break
  }
  if (run.length < 2) return { payloads: [], labels: [] }
  return { payloads: run.map((_, n) => String(n + 1)), labels: run }
}

const PAREN_RE = /\(([a-z0-9])\)\s*[a-z]/g

/** detectOptions infers approve options from pane text (numbered menu wins). */
export function detectOptions(text: string): string[] {
  if (!text) return []
  const lower = text.toLowerCase()
  const { payloads } = parseNumberedMenu(text)
  if (payloads.length) return payloads

  return parenShortcuts(lower)
}

// A real "(y)es (n)o (a)lways" prompt lists its keys on one line; matches
// scattered over the screen are prose ("the (v)erbose flag"), so only a line
// with at least two distinct keys counts — the last one, nearest the prompt.
function parenShortcuts(lower: string): string[] {
  const lines = lower.split('\n')
  for (let i = lines.length - 1; i >= 0; i--) {
    const keys = [...new Set([...lines[i].matchAll(PAREN_RE)].map((m) => m[1]))]
    if (keys.length >= 2) return keys.slice(0, 5)
  }
  return []
}

/** detectMenu returns options + labels for a blocked pane (labels may be empty). */
export function detectMenu(text: string): { options: string[]; labels: string[] } {
  const options = detectOptions(text)
  const { labels } = parseNumberedMenu(text)
  if (labels.length && labels.length === options.length) {
    return { options, labels }
  }
  return { options, labels: [] }
}

/** isCursorAgent reports whether the agent is Cursor (arrow-key menu UI). */
export function isCursorAgent(agentName: string): boolean {
  const a = (agentName || '').toLowerCase()
  return a.includes('cursor')
}

/** cursorKeySequence builds the arrow+Enter sequence for a cursor option index. */
export function cursorKeySequence(index: number): string[] {
  const keys: string[] = []
  for (let i = 0; i < index; i++) keys.push('Down')
  keys.push('Enter')
  return keys
}

/** isStickyChoice detects "don't ask again / always" style persistent options. */
export function isStickyChoice(label: string): boolean {
  const l = (label || '').toLowerCase()
  return /always|don'?t ask|trust|remember|forever/.test(l)
}

/** menuLabel shortens a full label to its head ("Yes, and allow…" → "Yes"). */
export function menuLabel(label: string): string {
  const full = (label || '').trim()
  if (!full) return full
  const head = full.split(/[,(]/)[0].replace(/^\W+\s*/, '').trim()
  return head || full
}
