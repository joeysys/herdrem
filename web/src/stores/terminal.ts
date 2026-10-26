import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ws } from '../api/ws'
import { useAgentsStore, useConnectionStore } from './connection'
import type { ReadPane, SendText, SendKeys } from '../api/types'
import { dispatchRespond } from '../utils/guard'
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

// Keep the last `limit` lines. Same visual count as lineCount.
function trimToLines(text: string, limit: number): string {
  const extra = lineCount(text) - limit
  if (extra <= 0) return text
  let seen = 0
  for (let i = 0; i < text.length; i++) {
    if (text[i] !== '\n') continue
    seen++
    if (seen === extra) return text.slice(i + 1)
  }
  return text
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
  // and localStorage; keep a generous but bounded window instead.
  const MAX_CONTENT_CHARS = 1_000_000 // ~1MB in-memory cap per pane
  const MAX_CACHE_TEXT = 200_000 // ~200KB persisted cap per pane
  // Relay observe baselines widen up to 100000 lines (maxBaselineLines).
  // Stay 50 under that so a resync of lineCount+50 is not cut short.
  const MAX_BASELINE_LINES = 100_000 - 50

  const activePane = ref('')
  const content = ref('')
  const loading = ref(false)
  const stickBottom = ref(true)
  const lastReadAt = ref(0)
  const fullscreen = ref(false)
  const fillSignal = ref<{ text: string; ts: number; kind: 'append' | 'prepend' | 'replace' } | null>(null)
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
      // grow the quota indefinitely.
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
  // kind 'append' inserts at the cursor with space padding; 'prepend' puts the
  // text at the front followed by a space (command-style chips); 'replace'
  // swaps the whole input (picking an entry from the history panel).
  function requestFill(text: string, kind: 'append' | 'prepend' | 'replace' = 'append') {
    fillSignal.value = { text, ts: Date.now(), kind }
  }

  // fillPath inserts a file/directory path into the active pane's terminal
  // input at the cursor (the consumer handles spacing and draft persistence).
  function fillPath(path: string) {
    fillSignal.value = { text: path, ts: Date.now(), kind: 'append' }
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
    delete contentGens.value[paneId]
    delete contentEpochs.value[paneId]
    if (paneId === activePane.value) {
      content.value = ''
      loading.value = true
      // Refresh asks for a baseline the next delta can extend. An observed
      // pane is answered from that cache; anything else is a direct read.
      requestRead(500, true)
    }
    saveCache()
  }

  // Clear every pane's terminal cache (memory + localStorage) and reset the
  // current view, used by the global "clear cache & reload" action.
  function clearAllCache() {
    contentCache.value = {}
    contentRevs.value = {}
    contentGens.value = {}
    contentEpochs.value = {}
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
  // Generation of the cached bytes. herdr's rev stays put while output streams,
  // so this is what orders a refresh reply against a delta that passed it.
  const contentGens = ref<Record<string, number>>({})
  // Epoch of the relay process that issued contentGens. A restart begins again
  // at gen 1; the new epoch is a new sequence, not an older snapshot.
  const contentEpochs = ref<Record<string, number>>({})
  // One resync at a time. Observe appends can arrive every coalesce window
  // while a poll has dropped the baseline; further mismatches wait until the
  // full snapshot (or the timeout below) reopens the gate. A poll that finds
  // a resync already running must not clear this flag.
  let resyncInFlight = false
  let resyncTimer: ReturnType<typeof setTimeout> | null = null

  function setContent(
    paneId: string,
    text: string,
    append = false,
    rev?: number,
    baseRev?: number,
    poll = false,
    gen?: number,
    baseGen?: number,
    epoch?: number,
  ) {
    const heldEpoch = contentEpochs.value[paneId]
    const epochMoved = epoch !== undefined && heldEpoch !== undefined && epoch !== heldEpoch
    // Ignore reads meant for other panes (e.g. grid previews later)
    if (paneId !== activePane.value) return
    if (append) {
      // We must already hold a baseline AND the delta must extend exactly
      // that baseline. Any mismatch (unknown base, stale rev, or a delta that
      // raced ahead of the initial full read) is dropped and a fresh full read
      // is requested so the view resyncs instead of corrupting.
      const cur = content.value
      const last = contentRevs.value[paneId]
      const heldGen = contentGens.value[paneId]
      // A versioned delta cannot be applied to an unversioned cached/direct
      // snapshot: request a paired full snapshot first.
      if (cur === '' || (baseRev !== undefined && baseRev !== last)) {
        // mismatch: request a full resync (guarded by activePane still matching)
        if (paneId === activePane.value) requestResync()
        return
      }
      // Same rule for the content generation. A missing local gen is the
      // upgrade case: rev already matched, so adopt the generation this delta
      // names. A mismatch means the delta extends bytes we do not hold.
      if (epochMoved || (baseGen !== undefined && heldGen !== undefined && baseGen !== heldGen)) {
        if (paneId === activePane.value) requestResync()
        return
      }
      content.value = cur + text
      if (rev !== undefined) contentRevs.value[paneId] = rev
      if (gen !== undefined) contentGens.value[paneId] = gen
      if (epoch !== undefined) contentEpochs.value[paneId] = epoch
    } else if (poll) {
      // Poll-mode full response (6s subscription refresh): the content is a
      // truncated tail that must NOT replace the delta-grown view. Only
      // revalidate the revision baseline; a mismatch means we are out of sync
      // and a full resync is needed. This branch never releases resyncInFlight:
      // requestResync() returning false means one is already running.
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
          // and silently drop real output.
          delete contentRevs.value[paneId]
          if (paneId === activePane.value) requestResync()
        }
        return
      }

      const merged = mergePollTail(content.value, text)
      if (merged === null) {
        requestResync()
        return
      }
      if (merged === content.value) return
      content.value = merged
      // The server supplied no revision, so a later versioned delta cannot be
      // proven to extend this locally merged content.
      delete contentRevs.value[paneId]
    } else {
      const heldGen = contentGens.value[paneId]
      // A refresh reply that left the server before a newer delta must not
      // replace the bytes that delta already appended. herdr rev does not
      // move on streamed output, so the generation is the order.
      if (!epochMoved && gen !== undefined && heldGen !== undefined && gen < heldGen) {
        releaseResync()
        loading.value = false
        return
      }
      content.value = text
      if (rev !== undefined) contentRevs.value[paneId] = rev
      else delete contentRevs.value[paneId]
      if (gen !== undefined) contentGens.value[paneId] = gen
      else delete contentGens.value[paneId]
      if (epoch !== undefined) contentEpochs.value[paneId] = epoch
      else delete contentEpochs.value[paneId]
      releaseResync()
    }
    // Bound retained content: keep the tail so the terminal keeps working
    // while the unbounded history is dropped.
    if (content.value.length > MAX_CONTENT_CHARS) {
      content.value = content.value.slice(-MAX_CONTENT_CHARS)
    }
    if (lineCount(content.value) > MAX_BASELINE_LINES) {
      content.value = trimToLines(content.value, MAX_BASELINE_LINES)
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
    // small line count keeps the poll response tiny.
    if (poll) msg.poll = true
    // Declare incremental support so the relay only routes append deltas to
    // this client. Older clients that can't interpret deltas get full snapshots
    // only.
    if (useConnectionStore().hasFeature('incremental-terminal')) msg.incremental = true
    ws.send(msg)
  }

  // A corrective full snapshot must not be shorter than the history it will
  // replace. Add one poll window for output produced during the round trip.
  // refresh=true asks the relay for a baseline the next delta can extend.
  // An observed pane is served from the cache that delta uses. Anything else
  // is a direct herdr read: ContentSnapshot can hold a version-confirmed but
  // older baseline (in-place TUI redraws don't move rev/seq).
  function releaseResync() {
    resyncInFlight = false
    if (resyncTimer) {
      clearTimeout(resyncTimer)
      resyncTimer = null
    }
  }

  function requestResync() {
    if (resyncInFlight) return false
    resyncInFlight = true
    const timer = setTimeout(() => {
      resyncInFlight = false
      resyncTimer = null
    }, 10_000)
    ;(timer as { unref?: () => void }).unref?.()
    resyncTimer = timer
    requestRead(Math.max(500, lineCount(content.value) + 50), true)
    return true
  }

  function sendText(text: string, submit = true, extra?: { requestId?: string; expectTail?: string }) {
    if (!activePane.value) return false
    const msg: SendText = { type: 'send_text', pane_id: activePane.value, text, submit }
    // Line breaks would reach the pane as Enter; a bracketed paste inserts them.
    if (/[\r\n]/.test(text) && useConnectionStore().hasFeature('send-text-paste')) msg.paste = true
    if (extra?.requestId) msg.request_id = extra.requestId
    if (extra?.expectTail) msg.expect_tail = extra.expectTail
    const ok = ws.send(msg)
    if (ok) scheduleRefresh()
    return ok
  }

  function sendKey(key: string, requestId?: string) {
    if (!activePane.value) return false
    const msg: SendKeys = { type: 'send_keys', pane_id: activePane.value, keys: key }
    if (requestId) msg.request_id = requestId
    const ok = ws.send(msg)
    if (ok) scheduleRefresh()
    return ok
  }

  // Send a sequence of keys. Without opts this is the old relay path: one
  // frame per key, timed on the client, no receipt. With opts the whole
  // sequence is one send_keys, so the relay can check prompt_id once and
  // stop on the first failure. One request_id covers the approval.
  function sendKeySequence(keys: string[], opts?: { promptId?: string; requestId?: string }): boolean {
    if (!activePane.value || !keys.length) return false
    if (!opts) {
      const gapMs = 55
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
      return true
    }
    const msg: SendKeys = { type: 'send_keys', pane_id: activePane.value, keys: '', sequence: keys }
    if (opts.promptId) msg.prompt_id = opts.promptId
    if (opts.requestId) msg.request_id = opts.requestId
    const ok = ws.send(msg)
    if (ok) scheduleRefresh()
    return ok
  }

  function respond(text: string, optIndex?: number, submit = false) {
    if (!activePane.value) return false
    const pane = useAgentsStore().agents.find((a) => a.pane_id === activePane.value)
    const ok = dispatchRespond(activePane.value, text, {
      optIndex,
      submit: submit ? true : undefined,
      promptId: pane?.prompt_id,
    })
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
