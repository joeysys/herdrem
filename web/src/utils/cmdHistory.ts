// Pure helpers for the input history (recording rules + matching).
// The Pinia store owns persistence; everything here is deterministic and
// side-effect free so it can be unit-tested without a DOM.
//
// Two kinds share one list per relay: shell commands ('sh') and prompts typed
// to agents ('ag'). They are ranked and capped separately — an English prompt
// must never show up as a suggestion for a shell command, or vice versa.
//
// History lives on the relay (shared by every device using it) and each entry
// remembers the workspace it was entered in, so lookups can be scoped to the
// current pane, the current workspace, or the whole relay. The field names are
// the wire/disk format shared with the Go store (internal/store/history.go).

import type { Pane } from '../api/types'

export type HistoryKind = 'sh' | 'ag'

export interface HistoryEntry {
  /** the text as submitted (trimmed) */
  c: string
  /** last used (ms since epoch) */
  t: number
  /** times submitted */
  n: number
  /** pane it was last submitted in; used to rank that pane's entries first */
  p?: string
  /** kind; absent means 'sh' (entries written before agent prompts were kept) */
  k?: HistoryKind
  /** agent type the prompt went to (agent entries only), e.g. "claude" */
  a?: string
  /** pane title at submit time — pane ids come and go, this stays readable */
  w?: string
  /** host and workspace it was entered in (absent for entries that predate this) */
  h?: string
  s?: string
  /** workspace label at submit time */
  l?: string
}

/** Where the user is right now; used to rank and scope lookups. */
export interface Where {
  paneId?: string
  host?: string
  workspaceId?: string
}

export interface RecordMeta extends Where {
  kind: HistoryKind
  agent?: string
  /** pane title */
  label?: string
  workspaceLabel?: string
}

/** The workspace identity of a pane, defaulting like the todo binding does. */
export function whereOf(pane: Pane | null | undefined): Where & { workspaceLabel?: string } {
  if (!pane) return {}
  return {
    paneId: pane.pane_id,
    host: pane.host || 'local',
    workspaceId: pane.workspace_id || 'default',
    workspaceLabel: pane.workspace_label || undefined,
  }
}

export interface Limits { maxEntries: number; maxLen: number }
// Keep in step with MaxShellLen & co. in internal/store/history.go.
export const LIMITS: Record<HistoryKind, Limits> = {
  sh: { maxEntries: 1500, maxLen: 500 },
  ag: { maxEntries: 600, maxLen: 4000 },
}
const MAX_META_LEN = 80
const MAX_ID_LEN = 256

export const kindOf = (e: HistoryEntry): HistoryKind => e.k ?? 'sh'

// Text that looks like it carries a credential is never stored: history is
// kept in localStorage, which any script on the page origin can read.
const SECRET_RE = new RegExp(
  [
    // KEY=value / key: value where the key names a credential
    String.raw`(?:pass(?:word|wd)?|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)\s*[=:]`,
    // --password foo, --token=foo …
    String.raw`--(?:password|passwd|token|secret|api-key|apikey)\b`,
    // Authorization headers / bearer tokens
    String.raw`\bauthorization\s*:`,
    String.raw`\bbearer\s+\S`,
    // scheme://user:password@host
    String.raw`:\/\/[^\s/@:]+:[^\s/@]+@`,
    // private key blocks pasted into a prompt
    String.raw`-----BEGIN [A-Z ]*PRIVATE KEY-----`,
  ].join('|'),
  'i',
)

/** Whether text looks like it carries a credential. */
export function looksSecret(text: string): boolean {
  return SECRET_RE.test(text)
}

/**
 * Whether a submitted line should be remembered. `raw` is the text exactly as
 * typed: a leading space opts out (same convention as HISTCONTROL=ignorespace).
 * Shell commands are single-line; agent prompts may span lines and are longer.
 */
export function shouldRecord(raw: string, kind: HistoryKind = 'sh'): boolean {
  if (!raw || !raw.trim()) return false
  if (/^\s/.test(raw)) return false
  const t = raw.trim()
  if (t.length > LIMITS[kind].maxLen) return false
  if (kind === 'sh' && /[\r\n]/.test(t)) return false
  if (looksSecret(raw)) return false
  return true
}

const clip = (s: string | undefined, max = MAX_META_LEN): string | undefined => {
  const v = (s ?? '').trim().slice(0, max)
  return v || undefined
}

const sameWorkspace = (e: HistoryEntry, w: Where): boolean =>
  !!w.workspaceId && e.s === w.workspaceId && (e.h ?? '') === (w.host ?? '')

/**
 * Returns a new list with `cmd` recorded. The same text in the same workspace
 * is one entry (counted); in another workspace it is a separate entry, so each
 * workspace keeps its own recent list. Each kind is capped on its own.
 */
export function addEntry(
  list: readonly HistoryEntry[],
  cmd: string,
  meta: RecordMeta,
  now: number,
  maxOverride?: number,
): HistoryEntry[] {
  const c = cmd.trim()
  const host = meta.host || undefined
  const ws = meta.workspaceId || undefined
  const same = (e: HistoryEntry) =>
    e.c === c && kindOf(e) === meta.kind && (e.h ?? '') === (host ?? '') && (e.s ?? '') === (ws ?? '')
  const prev = list.find(same)
  const entry: HistoryEntry = { c, t: now, n: (prev?.n ?? 0) + 1 }
  const paneId = meta.paneId || prev?.p
  if (paneId) entry.p = paneId
  if (host) entry.h = host
  if (ws) entry.s = ws
  const wl = clip(meta.workspaceLabel, MAX_ID_LEN) ?? prev?.l
  if (wl) entry.l = wl
  if (meta.kind === 'ag') {
    entry.k = 'ag'
    const a = clip(meta.agent) ?? prev?.a
    const w = clip(meta.label) ?? prev?.w
    if (a) entry.a = a
    if (w) entry.w = w
  }
  return capEntries([entry, ...list.filter((e) => !same(e))], maxOverride)
}

/** Newest first, each kind capped on its own. */
export function capEntries(list: readonly HistoryEntry[], maxOverride?: number): HistoryEntry[] {
  const sorted = [...list].sort((a, b) => b.t - a.t)
  const seen: Record<HistoryKind, number> = { sh: 0, ag: 0 }
  return sorted.filter((e) => {
    const k = kindOf(e)
    return ++seen[k] <= (maxOverride ?? LIMITS[k].maxEntries)
  })
}

/**
 * Folds `incoming` into `base` slot by slot (same rule as the relay's import):
 * never lose a newer time or a higher count.
 */
export function mergeEntries(base: readonly HistoryEntry[], incoming: readonly HistoryEntry[]): HistoryEntry[] {
  const slot = (e: HistoryEntry) => `${kindOf(e)}\u0000${e.h ?? ''}\u0000${e.s ?? ''}\u0000${e.c}`
  const map = new Map<string, HistoryEntry>()
  for (const e of base) map.set(slot(e), e)
  for (const e of incoming) {
    const old = map.get(slot(e))
    if (!old) map.set(slot(e), e)
    else map.set(slot(e), { ...(e.t > old.t ? e : old), n: Math.max(old.n, e.n), t: Math.max(old.t, e.t) })
  }
  return capEntries([...map.values()])
}

// Closer beats farther: this pane, then this workspace, then the rest of the
// relay; recency (then frequency) decides within a tier.
function tier(e: HistoryEntry, w: Where): number {
  if (w.paneId && e.p === w.paneId) return 2
  return sameWorkspace(e, w) ? 1 : 0
}

function rank(a: HistoryEntry, b: HistoryEntry, w: Where = {}): number {
  const dt = tier(b, w) - tier(a, w)
  if (dt) return dt
  if (a.t !== b.t) return b.t - a.t
  return b.n - a.n
}

export interface SuggestCtx extends Where { kind: HistoryKind }

/**
 * Inline suggestions for what is being typed: entries of the same kind that
 * start with `input` first, then (from two characters on) ones merely
 * containing it. Within each group the current pane's and workspace's entries
 * come first, then the rest of the relay. Each text is offered once, and the
 * exact input itself never.
 */
export function suggest(
  list: readonly HistoryEntry[],
  input: string,
  ctx: SuggestCtx,
  limit = 6,
): string[] {
  if (!input.trim()) return []
  const lower = input.toLowerCase()
  const allowContains = input.trim().length >= 2
  const prefix: HistoryEntry[] = []
  const contains: HistoryEntry[] = []
  for (const e of list) {
    if (kindOf(e) !== ctx.kind) continue
    if (e.c === input || e.c === input.trim()) continue
    if (e.c.startsWith(input)) prefix.push(e)
    else if (allowContains && e.c.toLowerCase().includes(lower)) contains.push(e)
  }
  prefix.sort((a, b) => rank(a, b, ctx))
  contains.sort((a, b) => rank(a, b, ctx))
  const out: string[] = []
  for (const e of [...prefix, ...contains]) {
    if (out.includes(e.c)) continue // same text from another workspace
    out.push(e.c)
    if (out.length >= limit) break
  }
  return out
}

/**
 * One row per text: the newest copy wins and the counts add up. Used when a
 * view spans workspaces, where the same command would otherwise repeat.
 */
export function collapseByText(list: readonly HistoryEntry[]): HistoryEntry[] {
  const map = new Map<string, HistoryEntry>()
  for (const e of list) {
    const key = `${kindOf(e)}\u0000${e.c}`
    const old = map.get(key)
    if (!old) {
      map.set(key, e)
    } else {
      const newest = e.t > old.t ? e : old
      map.set(key, { ...newest, n: old.n + e.n })
    }
  }
  return [...map.values()]
}

function isSubsequence(needle: string, hay: string): boolean {
  let i = 0
  for (let j = 0; j < hay.length && i < needle.length; j++) {
    if (hay[j] === needle[i]) i++
  }
  return i === needle.length
}

export type Scope = 'pane' | 'workspace' | 'relay'

export interface SearchFilter extends Where {
  kind?: 'all' | HistoryKind
  /** only agent prompts that went to this agent type */
  agent?: string
  /** how far to look; default 'relay'. 'pane' / 'workspace' need the matching Where field. */
  scope?: Scope
}

/**
 * History search (the ^R-style panel). Whitespace-separated tokens must all
 * appear (any order); results whose text starts with the whole query rank
 * above plain token matches, which rank above loose subsequence matches. An
 * empty query returns everything that passes the filter, nearest first.
 *
 * Scope 'pane' / 'workspace' keep only entries from there; 'relay' looks at
 * everything and shows each text once (the same command run in several
 * workspaces is one row).
 */
export function search(
  list: readonly HistoryEntry[],
  query: string,
  filter: SearchFilter = {},
  limit = 100,
): HistoryEntry[] {
  const kind = filter.kind ?? 'all'
  const scope = filter.scope ?? 'relay'
  let pool = list.filter((e) => {
    if (kind !== 'all' && kindOf(e) !== kind) return false
    if (filter.agent && !(kindOf(e) === 'ag' && e.a === filter.agent)) return false
    if (scope === 'pane' && filter.paneId && e.p !== filter.paneId) return false
    if (scope === 'workspace' && filter.workspaceId && !sameWorkspace(e, filter)) return false
    return true
  })
  if (scope === 'relay') pool = collapseByText(pool)
  const q = query.trim().toLowerCase()
  if (!q) return pool.sort((a, b) => rank(a, b, filter)).slice(0, limit)
  const tokens = q.split(/\s+/)
  const scored: Array<{ e: HistoryEntry; s: number }> = []
  for (const e of pool) {
    const lc = e.c.toLowerCase()
    let s = 0
    if (tokens.every((tok) => lc.includes(tok))) s = lc.startsWith(q) ? 3 : 2
    else if (isSubsequence(q.replace(/\s+/g, ''), lc)) s = 1
    if (s) scored.push({ e, s })
  }
  scored.sort((a, b) => b.s - a.s || rank(a.e, b.e, filter))
  return scored.slice(0, limit).map((x) => x.e)
}

/** Validates data read back from storage; anything malformed is dropped. */
export function sanitizeEntries(raw: unknown): HistoryEntry[] {
  if (!Array.isArray(raw)) return []
  const out: HistoryEntry[] = []
  for (const x of raw) {
    if (!x || typeof x !== 'object') continue
    const e = x as Record<string, unknown>
    const kind: HistoryKind = e.k === 'ag' ? 'ag' : 'sh'
    if (typeof e.c !== 'string' || !e.c.trim() || e.c.length > LIMITS[kind].maxLen) continue
    if (typeof e.t !== 'number' || !Number.isFinite(e.t)) continue
    if (looksSecret(e.c)) continue // never keep or show one, whatever wrote it
    const n = typeof e.n === 'number' && e.n >= 1 ? Math.floor(e.n) : 1
    const entry: HistoryEntry = { c: e.c, t: e.t, n }
    if (typeof e.p === 'string' && e.p) entry.p = e.p
    const h = typeof e.h === 'string' ? clip(e.h, MAX_ID_LEN) : undefined
    const sid = typeof e.s === 'string' ? clip(e.s, MAX_ID_LEN) : undefined
    const l = typeof e.l === 'string' ? clip(e.l, MAX_ID_LEN) : undefined
    if (h) entry.h = h
    if (sid) entry.s = sid
    if (l) entry.l = l
    if (kind === 'ag') {
      entry.k = 'ag'
      const a = typeof e.a === 'string' ? clip(e.a) : undefined
      const w = typeof e.w === 'string' ? clip(e.w) : undefined
      if (a) entry.a = a
      if (w) entry.w = w
    }
    out.push(entry)
  }
  out.sort((a, b) => b.t - a.t)
  const seen: Record<HistoryKind, number> = { sh: 0, ag: 0 }
  return out.filter((e) => ++seen[kindOf(e)] <= LIMITS[kindOf(e)].maxEntries)
}
