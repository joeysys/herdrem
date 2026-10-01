// Highlights search hits inside already-rendered terminal HTML.
//
// Terminal output is split into many <span>s by ANSI colour changes, so a
// single hit can straddle several text nodes ("git s" + "tatus"). We match
// against the concatenated text and wrap each piece in its own <mark>; the
// marks of one hit share a group.

export const HIT_CLASS = 'term-hit'
export const ACTIVE_CLASS = 'active'
/** Hits beyond this are not highlighted (a 1-char query on 1MB of output). */
export const MAX_HITS = 500

/**
 * Smart case: a query with an uppercase letter is case-sensitive, anything
 * else is not. Shared so callers can pre-filter text the same way the
 * highlighter will match it.
 */
export function matchesQuery(text: string, query: string): boolean {
  if (!query) return false
  return query !== query.toLowerCase() ? text.includes(query) : text.toLowerCase().includes(query)
}

/**
 * Removes every highlight under `root`, restoring the original text nodes.
 * Only the parents that held a mark are normalised, so text nodes elsewhere
 * (e.g. ones a framework renders itself) are left alone.
 */
export function clearHighlights(root: HTMLElement): void {
  const marks = root.querySelectorAll(`mark.${HIT_CLASS}`)
  if (!marks.length) return
  const parents = new Set<Node>()
  marks.forEach((m) => {
    const parent = m.parentNode
    if (!parent) return
    while (m.firstChild) parent.insertBefore(m.firstChild, m)
    parent.removeChild(m)
    parents.add(parent)
  })
  parents.forEach((p) => p.normalize())
}

interface Piece { node: Text; start: number; end: number }

/**
 * Wraps every occurrence of `query` under `root` and returns one group of
 * <mark> elements per hit, in document order. Matching is case-insensitive
 * unless the query contains an uppercase letter (smart case). At most
 * `limit` hits are wrapped. Call `clearHighlights` first when re-applying.
 */
export function applyHighlights(root: HTMLElement, query: string, limit = MAX_HITS): HTMLElement[][] {
  if (!query || limit <= 0) return []
  const doc = root.ownerDocument
  const walker = doc.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const nodes: Text[] = []
  const starts: number[] = []
  let text = ''
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const t = n as Text
    nodes.push(t)
    starts.push(text.length)
    text += t.data
  }
  if (!text) return []

  const caseSensitive = query !== query.toLowerCase()
  const hay = caseSensitive ? text : text.toLowerCase()
  const needle = caseSensitive ? query : query.toLowerCase()

  // 1. Find the match ranges in the concatenated text.
  const ranges: Array<[number, number]> = []
  for (let i = hay.indexOf(needle); i !== -1 && ranges.length < limit; i = hay.indexOf(needle, i + needle.length)) {
    ranges.push([i, i + needle.length])
  }
  if (!ranges.length) return []

  // 2. Split each range into per-node pieces (binary search for the first node).
  const locate = (pos: number): number => {
    let lo = 0
    let hi = nodes.length - 1
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1
      if (starts[mid] <= pos) lo = mid
      else hi = mid - 1
    }
    return lo
  }
  const hits: Piece[][] = ranges.map(([from, to]) => {
    const pieces: Piece[] = []
    for (let ni = locate(from); ni < nodes.length && starts[ni] < to; ni++) {
      const nodeStart = starts[ni]
      const len = nodes[ni].data.length
      const s = Math.max(from, nodeStart) - nodeStart
      const e = Math.min(to, nodeStart + len) - nodeStart
      if (e > s) pieces.push({ node: nodes[ni], start: s, end: e })
    }
    return pieces
  })

  // 3. Wrap from the last piece backwards: splitting a node only shifts the
  //    offsets that come after the split point, so earlier pieces stay valid.
  const groups: HTMLElement[][] = hits.map(() => [])
  for (let h = hits.length - 1; h >= 0; h--) {
    const pieces = hits[h]
    for (let p = pieces.length - 1; p >= 0; p--) {
      const { node, start, end } = pieces[p]
      const target = start > 0 ? node.splitText(start) : node
      if (end - start < target.data.length) target.splitText(end - start)
      const mark = doc.createElement('mark')
      mark.className = HIT_CLASS
      target.parentNode?.insertBefore(mark, target)
      mark.appendChild(target)
      groups[h].unshift(mark)
    }
  }
  return groups
}

/** Marks group `index` as the active hit (clearing the previous one). */
export function setActiveGroup(groups: HTMLElement[][], index: number): HTMLElement | null {
  groups.forEach((g, i) => g.forEach((m) => m.classList.toggle(ACTIVE_CLASS, i === index)))
  return groups[index]?.[0] ?? null
}
