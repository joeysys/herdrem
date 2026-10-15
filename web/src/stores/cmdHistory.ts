import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useConnectionStore } from './connection'
import { ws } from '../api/ws'
import { relayHttpBase } from '../utils/relay'
import { safeStorage } from '../utils/storage'
import {
  addEntry, kindOf, mergeEntries, sanitizeEntries, search as searchEntries, shouldRecord,
  suggest as suggestEntries,
  type HistoryEntry, type HistoryKind, type RecordMeta, type SearchFilter, type SuggestCtx, type Where,
} from '../utils/cmdHistory'

// Cache of the relay's history (and, for relays too old to keep it, the history
// itself). Same key as when this lived only in the browser, so what a browser
// already has is picked up and handed to the relay on first sync.
const STORAGE_KEY = 'herdrem_cmd_history'
// Entries made while the relay could not be reached; sent on the next sync.
const PENDING_KEY = 'herdrem_history_pending'
// Relays whose browser-local history has already been queued for upload.
const SEEDED_KEY = 'herdrem_history_seeded'
const RECORD_AGENTS_KEY = 'herdrem_history_agents'
// Relays a browser has ever talked to are unbounded; keep the most recently
// used few so the stored blob cannot grow without limit.
const MAX_RELAYS = 20
// The relay rejects WebSocket messages over 32 KiB; stay well under that.
const IMPORT_CHUNK_BYTES = 20000
// The relay's read limit counts bytes, not characters — a CJK entry is ~3
// bytes per UTF-16 unit, so chunk by encoded length, not .length.
const utf8Len = (s: string) => new TextEncoder().encode(s).length

type PerRelay = Record<string, HistoryEntry[]>

function loadMap(key: string): PerRelay {
  try {
    const raw = safeStorage.getItem(key)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: PerRelay = {}
    for (const [k, v] of Object.entries(parsed)) {
      const list = sanitizeEntries(v)
      if (list.length) out[k] = list
    }
    return out
  } catch {
    return {}
  }
}

function loadSeeded(): Record<string, true> {
  try {
    const parsed = JSON.parse(safeStorage.getItem(SEEDED_KEY) || '{}')
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

/** Splits entries into messages that fit the relay's per-message limit. */
export function chunkForImport(entries: readonly HistoryEntry[], maxBytes = IMPORT_CHUNK_BYTES): HistoryEntry[][] {
  const chunks: HistoryEntry[][] = []
  let cur: HistoryEntry[] = []
  let size = 0
  for (const e of entries) {
    const len = utf8Len(JSON.stringify(e)) + 1
    if (cur.length && size + len > maxBytes) {
      chunks.push(cur)
      cur = []
      size = 0
    }
    cur.push(e)
    size += len
  }
  if (cur.length) chunks.push(cur)
  return chunks
}

// Input history — shell commands and prompts sent to agents — shared by every
// device that uses a relay. The relay is the source of truth: it sends the
// whole history on connect and pushes changes made on other devices; this
// store applies local changes immediately and forwards them. Relays that do
// not advertise the "input-history" feature keep it in this browser only.
export const useCmdHistoryStore = defineStore('cmdHistory', () => {
  const conn = useConnectionStore()
  const all = ref<PerRelay>(loadMap(STORAGE_KEY))
  const pending = ref<PerRelay>(loadMap(PENDING_KEY))
  const seeded = ref<Record<string, true>>(loadSeeded())
  // Agent prompts are more likely to hold pasted secrets or private text than
  // shell commands, so recording them can be switched off on this device
  // (default on).
  const recordAgents = ref(safeStorage.getItem(RECORD_AGENTS_KEY) !== 'false')

  const key = computed(() => relayHttpBase(conn.url).toLowerCase())
  const entries = computed<HistoryEntry[]>(() => all.value[key.value] ?? [])
  /** true when the connected relay keeps the history (shared across devices) */
  const shared = computed(() => conn.hasFeature('input-history'))
  const counts = computed(() => {
    const c = { sh: 0, ag: 0 }
    for (const e of entries.value) c[kindOf(e)]++
    return c
  })
  /** agent types that have prompt history on this relay, sorted */
  const agentTypes = computed<string[]>(() => {
    const set = new Set<string>()
    for (const e of entries.value) if (kindOf(e) === 'ag' && e.a) set.add(e.a)
    return [...set].sort((x, y) => x.localeCompare(y))
  })

  function persist() {
    const keys = Object.keys(all.value)
    if (keys.length > MAX_RELAYS) {
      const newest = (k: string) => all.value[k][0]?.t ?? 0
      keys.sort((a, b) => newest(b) - newest(a))
      for (const k of keys.slice(MAX_RELAYS)) {
        delete all.value[k]
        delete pending.value[k]
      }
    }
    safeStorage.setItem(STORAGE_KEY, JSON.stringify(all.value))
    safeStorage.setItem(PENDING_KEY, JSON.stringify(pending.value))
  }

  function setList(next: HistoryEntry[]) {
    const copy = { ...all.value }
    if (next.length) copy[key.value] = next
    else delete copy[key.value]
    all.value = copy
  }

  function setRecordAgents(v: boolean) {
    recordAgents.value = v
    safeStorage.setItem(RECORD_AGENTS_KEY, String(v))
  }

  // ---- local changes (applied at once, then forwarded to the relay) ----

  /** Remember a submitted line. `raw` is the text exactly as typed. */
  function record(raw: string, meta: RecordMeta) {
    if (meta.kind === 'ag' && !recordAgents.value) return
    if (!shouldRecord(raw, meta.kind)) return
    const text = raw.trim()
    setList(addEntry(entries.value, text, meta, Date.now()))
    if (shared.value) {
      const sent = ws.send({
        type: 'history_record',
        text,
        kind: meta.kind,
        pane_id: meta.paneId,
        host: meta.host,
        workspace_id: meta.workspaceId,
        workspace_label: meta.workspaceLabel,
        agent: meta.agent,
        title: meta.label,
      })
      // Not connected right now: keep it to upload on the next sync.
      if (!sent) {
        const added = addEntry([], text, meta, Date.now())
        pending.value = { ...pending.value, [key.value]: mergeEntries(pending.value[key.value] ?? [], added) }
      }
    }
    persist()
  }

  /** Forget one entry everywhere (matched by text and kind). */
  function remove(entry: Pick<HistoryEntry, 'c' | 'k'>) {
    const kind = entry.k ?? 'sh'
    setList(entries.value.filter((e) => !(e.c === entry.c && kindOf(e) === kind)))
    if (shared.value) ws.send({ type: 'history_remove', kind, text: entry.c })
    persist()
  }

  /**
   * Forget one kind or everything. With `workspace` only that workspace's
   * entries go (and the relay clears just those); otherwise the whole relay's.
   */
  function clearedBy(e: HistoryEntry, kind: 'all' | HistoryKind, workspace?: { host: string; workspaceId: string }) {
    if (e.f) return false
    const kindMatch = kind === 'all' || kindOf(e) === kind
    const scopeMatch = !workspace || ((e.h ?? '') === workspace.host && (e.s ?? '') === workspace.workspaceId)
    return kindMatch && scopeMatch
  }

  function clear(kind: 'all' | HistoryKind = 'all', workspace?: { host: string; workspaceId: string }) {
    setList(entries.value.filter((e) => !clearedBy(e, kind, workspace)))
    if (shared.value) {
      ws.send({ type: 'history_clear', kind, host: workspace?.host, workspace_id: workspace?.workspaceId })
    }
    persist()
  }

  /** Star or unstar one command. `everywhere` covers every workspace's copy (the relay-wide row). */
  function setFavorite(entry: Pick<HistoryEntry, 'c' | 'k' | 'h' | 's'>, fav: boolean, everywhere = false) {
    const kind = entry.k ?? 'sh'
    const host = entry.h ?? ''
    const workspaceId = entry.s ?? ''
    setList(entries.value.map((e) => {
      if (e.c !== entry.c || kindOf(e) !== kind) return e
      if (!everywhere && ((e.h ?? '') !== host || (e.s ?? '') !== workspaceId)) return e
      if (!!e.f === fav) return e
      const next = { ...e }
      if (fav) next.f = true
      else delete next.f
      return next
    }))
    if (shared.value) {
      ws.send({
        type: 'history_favorite',
        kind,
        text: entry.c,
        favorite: fav,
        everywhere,
        host: everywhere ? undefined : host,
        workspace_id: everywhere ? undefined : workspaceId,
      })
    }
    persist()
  }

  function clearable(kind: 'all' | HistoryKind, workspace?: { host: string; workspaceId: string }) {
    return entries.value.some((e) => clearedBy(e, kind, workspace))
  }

  // ---- changes coming from the relay ----

  /** The relay's full history, sent on connect. */
  function applySnapshot(incoming: unknown) {
    const snap = sanitizeEntries(incoming)
    // Anything this browser knows that the relay does not yet: what it cached
    // before the relay kept history (once per relay), plus entries made while
    // disconnected.
    let upload = pending.value[key.value] ?? []
    const firstSync = !seeded.value[key.value]
    if (firstSync) {
      upload = mergeEntries(upload, all.value[key.value] ?? [])
    }
    // ws.send only means the frame left this tab — it is not a delivery ack.
    // Only mark seeded / drop the pending queue once every chunk was actually
    // handed to an open socket; on failure the whole batch stays queued and
    // the next snapshot retries it (the relay's import merge is idempotent),
    // instead of the snapshot overwriting local entries that never arrived.
    const delivered =
      !upload.length || chunkForImport(upload).every((chunk) => ws.send({ type: 'history_import', entries: chunk }))
    if (delivered) {
      if (firstSync) {
        seeded.value = { ...seeded.value, [key.value]: true }
        safeStorage.setItem(SEEDED_KEY, JSON.stringify(seeded.value))
      }
      const rest = { ...pending.value }
      delete rest[key.value]
      pending.value = rest
    } else {
      pending.value = { ...pending.value, [key.value]: upload }
    }
    setList(upload.length ? mergeEntries(snap, upload) : snap)
    persist()
  }

  /** Another device recorded (or re-used) an entry. */
  function applyAdded(entry: unknown) {
    const [e] = sanitizeEntries([entry])
    if (!e) return
    setList(mergeEntries(entries.value.filter((x) => !(x.c === e.c && kindOf(x) === kindOf(e) && x.h === e.h && x.s === e.s)), [e]))
    persist()
  }

  /** Another device removed an entry. */
  function applyRemoved(kind: unknown, text: unknown) {
    if (typeof text !== 'string') return
    const k: HistoryKind = kind === 'ag' ? 'ag' : 'sh'
    setList(entries.value.filter((e) => !(e.c === text && kindOf(e) === k)))
    persist()
  }

  /** Another device cleared a kind, relay-wide or for one workspace. */
  function applyCleared(kind: unknown, host?: unknown, workspaceId?: unknown) {
    const k = kind === 'sh' || kind === 'ag' ? kind : 'all'
    const scoped = typeof host === 'string' && typeof workspaceId === 'string' && host !== '' && workspaceId !== ''
    setList(entries.value.filter((e) => !clearedBy(e, k, scoped ? { host, workspaceId } : undefined)))
    persist()
  }

  /** Another device starred or unstarred an entry. */
  function applyFavorited(kind: unknown, text: unknown, fav: unknown, host?: unknown, workspaceId?: unknown, everywhere?: unknown) {
    if (typeof text !== 'string') return
    const k: HistoryKind = kind === 'ag' ? 'ag' : 'sh'
    const on = fav === true
    const allCopies = everywhere === true
    const h = typeof host === 'string' ? host : ''
    const s = typeof workspaceId === 'string' ? workspaceId : ''
    setList(entries.value.map((e) => {
      if (e.c !== text || kindOf(e) !== k) return e
      if (!allCopies && ((e.h ?? '') !== h || (e.s ?? '') !== s)) return e
      if (!!e.f === on) return e
      const next = { ...e }
      if (on) next.f = true
      else delete next.f
      return next
    }))
    persist()
  }

  // ---- lookups ----

  function suggest(input: string, ctx: SuggestCtx, limit = 6): string[] {
    return suggestEntries(entries.value, input, ctx, limit)
  }

  function search(query: string, filter: SearchFilter = {}, limit = 100): HistoryEntry[] {
    return searchEntries(entries.value, query, filter, limit)
  }

  /** How many entries a filter (without a query) would show. */
  function count(filter: SearchFilter & Where): number {
    return searchEntries(entries.value, '', filter, Number.MAX_SAFE_INTEGER).length
  }

  return {
    entries, counts, agentTypes, shared, recordAgents, setRecordAgents,
    record, remove, clear, setFavorite, clearable, suggest, search, count,
    applySnapshot, applyAdded, applyRemoved, applyCleared, applyFavorited,
  }
})

