// inputFill.ts — how chip/todo/path fills merge into the terminal input.
// Two modes:
//   append  — insert at the cursor (end when unknown), padded with a space on
//             each side where the neighbour isn't already whitespace. Used by
//             todo items and file-path fills.
//   prepend — command-style text goes to the very front, separated from any
//             existing text by a single trailing space. Used by skill/agent
//             cmd/shell cmd chips so "cmd <existing>" reads naturally.
// Both return the new text and the cursor position right after the insert.

export interface FillResult {
  text: string
  cursor: number
}

export function insertSnippet(current: string, pos: number | null, snippet: string): FillResult {
  const at = pos === null || pos < 0 || pos > current.length ? current.length : pos
  const before = current.slice(0, at)
  const after = current.slice(at)
  const left = before !== '' && !/\s$/.test(before) && !/^\s/.test(snippet) ? ' ' : ''
  const right = after !== '' && !/^\s/.test(after) && !/\s$/.test(snippet) ? ' ' : ''
  const head = before + left + snippet + right
  return { text: head + after, cursor: head.length }
}

export function prependSnippet(current: string, snippet: string): FillResult {
  const s = snippet.trim()
  return { text: s + ' ' + current, cursor: s.length + 1 }
}
