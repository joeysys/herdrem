import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ws } from '../api/ws'
import { useConnectionStore } from './connection'
import type { ReadPane, SendText, SendKeys, Respond } from '../api/types'
import { safeStorage } from '../utils/storage'

const MIN_POLL_OVERLAP_LINES = 20

function hasLoneCR(text: string): boolean {
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\r' && text[i + 1] !== '\n') return true
  }
  return false
}

function lineCount(text: string): number {
  if (!text) return 0
  let lines = text.endsWith('\n') ? 0 : 1
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\n') lines++
  }
  return lines
}

// Merge an unversioned 50-line poll response only when it proves continuity
// with retained content. Return null for redraws/discontinuities so the caller
// can request one authoritative full snapshot instead.
function mergePollTail(current: string, tail: string): string | null {
  if (current === tail) return current
  if (!current || !tail) return null

  if (tail.startsWith(current)) {
    const delta = tail.slice(current.length)
    return hasLoneCR(delta) ? null : tail
  }

  // A rolling-tail merge is only safe when we retained more history than the
  // poll returned. This excludes fixed-height TUI screens that merely redraw.
  if (lineCount(current) <= lineCount(tail)) return null

  const starts = [0]
  for (let i = 0; i < current.length; i++) {
    if (current[i] === '\n' && i + 1 < current.length) starts.push(i + 1)
  }
  for (const start of starts.slice(-64)) {
    const overlap = current.slice(start)
    if (lineCount(overlap) < MIN_POLL_OVERLAP_LINES || !tail.startsWith(overlap)) continue
    const delta = tail.slice(overlap.length)
    if (hasLoneCR(delta)) return null
    return current + delta
  }
  return null
}

export const useTerminalStore = defineStore('terminal', () => {
  // Upper bounds on retained terminal text per pane. Rendering only shows the
  // tail (tailWindow), so retaining the entire unbounded history wastes memory
  // and localStorage; keep a generous but bounded window instead (F3).
  const MAX_CONTENT_CHARS = 1_000_000 // ~1MB in-memory cap per pane
  const MAX_CACHE_TEXT = 200_000 // ~200KB persisted cap per pane

  const activePane = ref('')
  const content = ref('')
  const loading = ref(false)
  const stickBottom = ref(true)
  const lastReadAt = ref(0)
  const fullscreen = ref(false)
  const fillSignal = ref<{ text: string; ts: number } | null>(null)
  const inputDrafts = ref<Record<string, string>>({})

  // Local content cache: paneId → { text, ts }
  // Persisted to localStorage so it survives page refresh.
  const CACHE_KEY = 'herdrem_term_cache'
  const contentCache = ref<Record<string, { text: string; ts: number }>>(loadCache())

  function loadCache(): Record<string, { text: string; ts: number }> {
    try {
      const raw = safeStorage.getItem(CACHE_KEY)
      if (raw) return JSON.parse(raw)
    } catch { /* noop */ }
    return {}
  }

  function saveCache() {
    try {
      // Only keep the 20 most recent entries to avoid bloating localStorage,
      // and cap each pane's persisted text so a long-running session cannot
      // grow the quota indefinitely (F3).
      const entries = Object.entries(contentCache.value)
        .sort((a, b) => b[1].ts - a[1].ts)
        .slice(0, 20)
      const obj = Object.fromEntries(
        entries.map(([k, v]) => [k, { ...v, text: v.text.length > MAX_CACHE_TEXT ? v.text.slice(-MAX_CACHE_TEXT) : v.text }]),
      )
      safeStorage.setItem(CACHE_KEY, JSON.stringify(obj))
    } catch { /* quota exceeded — noop */ }
  }

  const isOpen = computed(() => activePane.value !== '')

  function toggleFullscreen(force?: boolean) {
    fullscreen.value = force !== undefined ? force : !fullscreen.value
  }

  // requestFill asks the terminal input to insert text (from todo/quick chips).
  function requestFill(text: string) {
    fillSignal.value = { text, ts: Date.now() }
  }

  // fillPath appends or sets a file/directory path in the active pane's terminal input
  function fillPath(path: string) {
    const paneId = activePane.value
    const current = paneId ? getInputDraft(paneId) : ''
    let next = ''
    if (!current) {
      next = path
    } else if (/\s$/.test(current)) {
      next = current + path
    } else {
      next = current + ' ' + path
    }
    if (paneId) setInputDraft(paneId, next)
    fillSignal.value = { text: next, ts: Date.now() }
  }

  function getInputDraft(paneId: string): string {
    return inputDrafts.value[paneId] || ''
  }
  function setInputDraft(paneId: string, text: string) {
    inputDrafts.value[paneId] = text
  }

  function openPane(paneId: string) {
    activePane.value = paneId
    stickBottom.value = true
    // Restore cached content immediately if available
    const cached = contentCache.value[paneId]
    if (cached) {
      content.value = cached.text
      loading.value = true // still loading fresh content in background
    } else {
      content.value = ''
      loading.value = true
    }
    requestRead()
  }

  function closePane() {
    activePane.value = ''
    content.value = ''
    loading.value = false
  }

  // Clear the persisted + in-memory terminal cache for one pane, and if it is
  // the active pane, drop the rendered content so the next read is a forced
  // fresh full snapshot (the manual Refresh button).
  function clearPaneCache(paneId: string) {
    delete contentCache.value[paneId]
    delete contentRevs.value[paneId]
    if (paneId === activePane.value) {
      content.value = ''
      loading.value = true
      // Force a direct herdr read (bypass the poller cache) so a manual
      // Refresh always shows the authoritative current content.
      requestRead(500, true)
    }
    saveCache()
  }

  // Clear every pane's terminal cache (memory + localStorage) and reset the
  // current view, used by the global "clear cache & reload" action.
  function clearAllCache() {
    contentCache.value = {}
    contentRevs.value = {}
    content.value = ''
    loading.value = true
    safeStorage.removeItem(CACHE_KEY)
  }

  // Last accepted revision per pane: the server stamps every full snapshot and
  // every delta with a revision; a delta is only spliced onto our content when
  // its base_rev matches the revision of what we hold. This prevents a delta
  // computed against a different baseline (another client, a raced read) from
  // corrupting the view — the mismatch triggers a full resync instead.
  const contentRevs = ref<Record<string, number>>({})

  function setContent(paneId: string, text: string, append = false, rev?: number, baseRev?: number, poll = false) {
    // Ignore reads meant for other panes (e.g. grid previews later)
    if (paneId !== activePane.value) return
    if (append) {
      // We must already hold a baseline AND the delta must extend exactly
      // that baseline. Any mismatch (unknown base, stale rev, or a delta that
      // raced ahead of the initial full read) is dropped and a fresh full read
      // is requested so the view resyncs instead of corrupting.
      const cur = content.value
      const last = contentRevs.value[paneId]
      // A versioned delta cannot be applied to an unversioned cached/direct
      // snapshot: request a paired full snapshot first.
      if (cur === '' || (baseRev !== undefined && baseRev !== last)) {
        // mismatch: request a full resync (guarded by activePane still matching)
        if (paneId === activePane.value) requestRead(500)
        return
      }
      content.value = cur + text
      if (rev !== undefined) contentRevs.value[paneId] = rev
    } else if (poll) {
      // Poll-mode full response (6s subscription refresh): the content is a
      // truncated tail that must NOT replace the delta-grown view. Only
      // revalidate the revision baseline; a mismatch means we are out of sync
      // and a full resync is needed (N2).
      const last = contentRevs.value[paneId]
      if (rev !== undefined) {
        if (last !== undefined && rev === last) {
          // in sync: refresh the baseline only
          contentRevs.value[paneId] = rev
        } else {
          // Out of sync: we DO NOT hold the server's baseline. Mark it as
          // unknown (delete, not adopt) so the append path rejects any delta
          // until the full resync lands — adopting the server's rev while our
          // content is stale would let a delta splice onto the wrong baseline
          // and silently drop real output (N2 verify-reject).
          delete contentRevs.value[paneId]
          if (paneId === activePane.value) requestRead(500)
        }
        return
      }

      const merged = mergePollTail(content.value, text)
      if (merged === null) {
        requestRead(500)
        return
      }
      if (merged === content.value) return
      content.value = merged
      // The server supplied no revision, so a later versioned delta cannot be
      // proven to extend this locally merged content.
      delete contentRevs.value[paneId]
    } else {
      content.value = text
      if (rev !== undefined) contentRevs.value[paneId] = rev
      else delete contentRevs.value[paneId]
    }
    // Bound retained content: keep the tail so the terminal keeps working
    // while the unbounded history is dropped (F3).
    if (content.value.length > MAX_CONTENT_CHARS) {
      content.value = content.value.slice(-MAX_CONTENT_CHARS)
    }
    loading.value = false
    lastReadAt.value = Date.now()
    // Update cache
    contentCache.value[paneId] = { text: content.value, ts: Date.now() }
    saveCache()
  }

  function requestRead(lines = 500, refresh = false, poll = false) {
    if (!activePane.value) return
    const msg: ReadPane = { type: 'read_pane', pane_id: activePane.value, lines, purpose: 'term' }
    if (refresh) msg.refresh = true
    // Poll-mode responses are subscription baseline refreshes: the client
    // keeps its delta-grown content and only revalidates the revision. A
    // small line count keeps the poll response tiny (N2).
    if (poll) msg.poll = true
    // Declare incremental support so the relay only routes append deltas to
    // this client (F2: older clients that can't interpret deltas get full
    // snapshots only).
    if (useConnectionStore().hasFeature('incremental-terminal')) msg.incremental = true
    ws.send(msg)
  }

  function sendText(text: string, submit = true) {
    if (!activePane.value) return false
    const msg: SendText = { type: 'send_text', pane_id: activePane.value, text, submit }
    const ok = ws.send(msg)
    if (ok) scheduleRefresh()
    return ok
  }

  function sendKey(key: string) {
    if (!activePane.value) return false
    const msg: SendKeys = { type: 'send_keys', pane_id: activePane.value, keys: key }
    const ok = ws.send(msg)
    if (ok) scheduleRefresh()
    return ok
  }

  // Send a sequence of keys with a gap between each (e.g. cursor approval).
  function sendKeySequence(keys: string[], gapMs = 55) {
    if (!activePane.value || !keys.length) return
    let i = 0
    const step = () => {
      if (i >= keys.length || !activePane.value) return
      const msg: SendKeys = { type: 'send_keys', pane_id: activePane.value, keys: keys[i] }
      ws.send(msg)
      i++
      if (i < keys.length) setTimeout(step, gapMs)
      else scheduleRefresh()
    }
    step()
  }

  function respond(text: string, optIndex?: number) {
    if (!activePane.value) return false
    const msg: Respond = { type: 'respond', pane_id: activePane.value, text }
    if (optIndex !== undefined) msg.opt_index = optIndex
    const ok = ws.send(msg)
    if (ok) scheduleRefresh()
    return ok
  }

  // Give the agent a beat to react before re-reading the pane
  let refreshTimer: ReturnType<typeof setTimeout> | null = null
  function scheduleRefresh(delay = 450) {
    if (refreshTimer) clearTimeout(refreshTimer)
    refreshTimer = setTimeout(() => requestRead(), delay)
  }

  return {
    activePane,
    content,
    loading,
    stickBottom,
    isOpen,
    lastReadAt,
    fullscreen,
    fillSignal,
    inputDrafts,
    toggleFullscreen,
    requestFill,
    fillPath,
    getInputDraft,
    setInputDraft,
    openPane,
    closePane,
    setContent,
    requestRead,
    clearPaneCache,
    clearAllCache,
    sendText,
    sendKey,
    sendKeySequence,
    respond,
  }
})
