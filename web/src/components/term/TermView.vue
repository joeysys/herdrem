<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useTerminalStore } from '../../stores/terminal'
import { useAgentsStore, bucket, paneTitle, workspaceLabel, paneKind } from '../../stores/connection'
import { useConnectionStore } from '../../stores/connection'
import { useFilesStore } from '../../stores/files'
import { useDiffStore } from '../../stores/diff'
import { useTodoStore } from '../../stores/todo'
import { usePrefsStore } from '../../stores/prefs'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { useTermZoom } from '../../composables/useTermZoom'
import { useDoneAutoMark } from '../../composables/useDoneAutoMark'
import { ws } from '../../api/ws'
import TermInput from './TermInput.vue'
import ShellKeys from './ShellKeys.vue'
import AnsiStream from './AnsiStream.vue'
import FindBar from '../common/FindBar.vue'
import HistorySuggest from './HistorySuggest.vue'
import HistoryPanel from './HistoryPanel.vue'
import { MAX_HITS } from '../../utils/domHighlight'
import { useHistorySuggest } from '../../composables/useHistorySuggest'
import { whereOf } from '../../utils/cmdHistory'
import { tailWindow } from '../../utils/tailWindow'
import { insertSnippet, prependSnippet } from '../../utils/inputFill'
import { autoFocus } from '../../utils/softKeyboard'
import { caretOnEdgeLine } from '../../composables/useWrappingInput'
import { ensureShellShortcuts, isShortcutActive, isTextInput, register, shellActions, shortcutChord, shortcutVersion } from '../../composables/useShortcuts'
import TodoPanel from '../todo/TodoPanel.vue'
import TermIcon from './TermIcon.vue'
import type { TodoBinding } from '../../api/types'

const props = defineProps<{ paneId: string }>()
const emit = defineEmits<{
  back: []
  openFile: [payload: { path: string; line: number | null; paneId: string }]
  editCommands: [section: 'shell_cmds' | 'skills' | 'agent_cmds']
  openSettings: []
}>()

const term = useTerminalStore()
const agents = useAgentsStore()
const conn = useConnectionStore()
const files = useFilesStore()
const diff = useDiffStore()
const todo = useTodoStore()
const prefs = usePrefsStore()
const history = useCmdHistoryStore()

const rootEl = ref<HTMLElement | null>(null)

const showTodo = ref(false)
const showMenu = ref(false)

const pane = computed(() => agents.agents.find((a) => a.pane_id === props.paneId) || null)
const todoBinding = computed<TodoBinding>(() => ({
  host: pane.value?.host || 'local',
  workspace_id: pane.value?.workspace_id || 'default',
  workspace_label: pane.value?.workspace_label,
}))
const hasTodo = computed(() => todo.hasItems(todoBinding.value))
const statusBucket = computed(() => bucket(pane.value?.status))
const isShell = computed(() => pane.value ? paneKind(pane.value) === 'shell' : false)
const title = computed(() => pane.value ? paneTitle(pane.value) : props.paneId)
const subTitle = computed(() => {
  if (!pane.value) return props.paneId
  const parts: string[] = []
  const ws = workspaceLabel(pane.value)
  if (ws) parts.push(ws)
  if (pane.value.branch) parts.push(`⎇ ${pane.value.branch}`)
  parts.push(pane.value.pane_id)
  return parts.join(' · ')
})

const scrollEl = ref<HTMLElement | null>(null)
const zoom = useTermZoom(scrollEl)

// Render only the tail of the pane content instead of the full 500-line read.
// Full reads can be ~100KB of ANSI producing thousands of styled spans; on a
// phone each full re-render costs ~130ms of main-thread time (DOM rebuild +
// layout) every poll. The terminal viewport shows at most ~40 lines, so
// keeping a bounded tail window (with a small scroll-back buffer) cuts that
// to single-digit milliseconds while preserving bottom-anchored streaming.
const RENDER_WINDOW_LINES = 200

// When windowing is active (pinned to bottom), the rendered content is the
// tail window. When the user scrolls up, stickBottom flips and the view
// switches to the full content; without compensation that transition would
// jump the viewport into the middle of the history (see the stickBottom watch).
const renderContent = computed(() => {
  const raw = term.content
  if (!raw) return raw
  // When the user scrolls up to read history, render the full content so the
  // scroll-back window stays intact; windowing only applies when pinned to the
  // bottom (the streaming case that dominates cost during agent output).
  // Searching needs every retained line, not just the tail window.
  if (!term.stickBottom || searchOpen.value) return raw
  return tailWindow(raw, RENDER_WINDOW_LINES)
})

// Preserve the visible line when switching between windowed (bottom) and full
// rendering on stickBottom flips, so scrolling up does not jump into the
// history. The window is exactly the tail of the full content, so the
// viewport's distance from the bottom is the same in both renderings: keep
// that instead of estimating the height of the lines added above. A line
// height estimate is always short — glyphs from fallback fonts (box drawing,
// emoji, CJK) make real lines taller — and the error adds up over every
// dropped line, landing the view far above where the user was.
// Runs as a pre-flush watcher, so the distance is read before the re-render.
watch(
  () => term.stickBottom,
  async (now, before) => {
    const el = scrollEl.value
    if (!el) return
    const fromBottom = el.scrollHeight - el.scrollTop
    await nextTick()
    if (!now && before) {
      el.scrollTop = Math.max(0, el.scrollHeight - fromBottom)
    } else if (now && !before) {
      // back to bottom: jump straight to the end.
      el.scrollTop = el.scrollHeight
    }
  },
)
const shellText = ref('')
const shellFocused = ref(false)
const shellInputEl = ref<HTMLInputElement | null>(null)
const shellKeysEl = ref<InstanceType<typeof ShellKeys> | null>(null)
const termInputEl = ref<InstanceType<typeof TermInput> | null>(null)

// Restore/persist shell input draft per pane
watch(
  () => props.paneId,
  (id) => {
    if (id) shellText.value = term.getInputDraft(id)
  },
  { immediate: true },
)
watch(shellText, (t) => {
  if (props.paneId) term.setInputDraft(props.paneId, t)
})

function shellSubmit() {
  const t = shellText.value
  if (!t) return
  term.sendText(t, true)
  history.record(t, { kind: 'sh', ...whereOf(pane.value) })
  shellText.value = ''
}

// The shell's active text input: the ShellKeys input box when it is toggled
// on, otherwise the inline input rendered inside the terminal output.
function activeShellInput(): HTMLInputElement | HTMLTextAreaElement | null {
  return shellKeysEl.value?.inputEl?.() || shellInputEl.value
}

// Merge fill text into the shell input: 'prepend' (quick-command chips) puts
// the command at the front with a trailing space; 'append' (todo items, file
// paths) inserts at the cursor with space padding. Focus lands after it.
function fillShellInput(snippet: string, kind: 'append' | 'prepend') {
  const el = activeShellInput()
  const r = kind === 'prepend'
    ? prependSnippet(shellText.value, snippet)
    : insertSnippet(shellText.value, el ? el.selectionStart : null, snippet)
  shellText.value = r.text
  nextTick(() => {
    if (autoFocus(el)) el!.setSelectionRange(r.cursor, r.cursor)
  })
}

function onShellFill(t: string) {
  fillShellInput(t, 'prepend')
}

// Todo/path fills (fillSignal) — TermInput only exists on agent panes, so the
// shell inline input consumes them here.
watch(
  () => term.fillSignal,
  (sig) => {
    if (!sig || !isShell.value) return
    if (sig.kind === 'replace') setShellText(sig.text)
    else fillShellInput(sig.text, sig.kind)
  },
)

function focusShellInput() {
  activeShellInput()?.focus()
}

// Convenience focus (pane opened, output tapped, panel closed): never raises
// the soft keyboard on its own — see utils/softKeyboard.
function autoFocusShellInput() {
  autoFocus(activeShellInput())
}

// --- input history (per-relay): suggestions + panel ---
const boxFocused = ref(false)
const showHistory = ref(false)

// Replace the whole shell input with `cmd`, caret at the end.
function setShellText(cmd: string) {
  const el = activeShellInput()
  shellText.value = cmd
  nextTick(() => {
    if (!el) return
    el.focus()
    el.setSelectionRange(cmd.length, cmd.length)
  })
}

function openHistory() {
  showTodo.value = false
  showHistory.value = true
}
function closeHistory() {
  showHistory.value = false
  if (isShell.value) nextTick(() => autoFocusShellInput())
}
// Picking never runs anything: it only fills the current pane's input, so an
// agent prompt can be reused in a shell and vice versa.
function pickHistory(text: string) {
  showHistory.value = false
  if (isShell.value) setShellText(text)
  else term.requestFill(text, 'replace')
}

// Shell prompt suggestions (the agent input has its own, in TermInput). Shown
// while typing in either shell input, hidden while the history panel is open.
const shellSuggest = useHistorySuggest({
  text: shellText,
  kind: 'sh',
  where: () => whereOf(pane.value),
  enabled: () => isShell.value && (shellFocused.value || boxFocused.value) && !showHistory.value,
  accept: setShellText,
  openHistory,
})
const suggestions = shellSuggest.suggestions
const activeSuggest = shellSuggest.active

let recalling = false
let histCursor = -1
let histDraft = ''
// flush sync: the default pre watcher runs after this turn, by which time
// recallShellHistory has already cleared `recalling`, so every ArrowUp looked
// like a user edit and snapped the cursor back to the newest entry.
watch(shellText, () => { if (!recalling) histCursor = -1 }, { flush: 'sync' })

function recallShellHistory(dir: 1 | -1): boolean {
  const list = history.entries.filter((e) => (e.k ?? 'sh') === 'sh').map((e) => e.c)
  if (!list.length) return false
  if (histCursor < 0) histDraft = shellText.value
  const next = histCursor + dir
  if (next < -1 || next >= list.length) return false
  histCursor = next
  recalling = true
  shellText.value = next < 0 ? histDraft : list[next]
  recalling = false
  nextTick(() => {
    const el = activeShellInput()
    const at = shellText.value.length
    el?.setSelectionRange(at, at)
  })
  return true
}

function onShellKeydown(e: KeyboardEvent) {
  if (e.isComposing) return
  if (!suggestions.value.length && (e.key === 'ArrowUp' || e.key === 'ArrowDown') && !e.altKey && !e.ctrlKey && !e.metaKey) {
    // in multi-line text ↑ / ↓ move between lines until the first / last
    const up = e.key === 'ArrowUp'
    if (caretOnEdgeLine(e.target, up ? 'up' : 'down') && recallShellHistory(up ? 1 : -1)) {
      e.preventDefault()
      return
    }
  }
  shellSuggest.onKeydown(e)
}

function inputHasSelection(e: KeyboardEvent): boolean {
  const t = e.target
  if ((t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement) && t.selectionStart !== t.selectionEnd) return true
  const sel = window.getSelection()
  return !!sel && !sel.isCollapsed && sel.toString().length > 0
}

// --- output search ---
// Searches every line the view retains. Opening it asks the relay once for a
// deeper scrollback (SEARCH_LINES) so older output is findable too.
const SEARCH_LINES = 3000
const searchOpen = ref(false)
const searchQuery = ref('')
const appliedQuery = ref('')
const hitCount = ref(0)
const hitIndex = ref(0)
const searchRef = ref<InstanceType<typeof FindBar> | null>(null)
const ansiRef = ref<InstanceType<typeof AnsiStream> | null>(null)
let deepLoadedFor = ''
let pendingJump = false
let searchDebounce: ReturnType<typeof setTimeout> | null = null

// Debounced so typing does not re-highlight a large DOM on every keystroke.
watch(searchQuery, (q) => {
  if (searchDebounce) clearTimeout(searchDebounce)
  if (!q) {
    appliedQuery.value = ''
    hitCount.value = 0
    return
  }
  searchDebounce = setTimeout(() => {
    pendingJump = true
    appliedQuery.value = q
  }, 120)
})

function openSearch() {
  if (searchOpen.value) {
    searchRef.value?.focus()
    return
  }
  searchOpen.value = true
  pendingJump = true
  if (deepLoadedFor !== props.paneId) {
    deepLoadedFor = props.paneId
    term.requestRead(SEARCH_LINES, true)
  }
  nextTick(() => {
    // Windowed → full render keeps the scroll offset, which would land in the
    // middle of the history; stay pinned to the bottom until a hit is chosen.
    const el = scrollEl.value
    if (el && term.stickBottom) el.scrollTop = el.scrollHeight
    searchRef.value?.focus()
  })
}

function closeSearch() {
  if (searchDebounce) clearTimeout(searchDebounce)
  searchOpen.value = false
  searchQuery.value = ''
  appliedQuery.value = ''
  hitCount.value = 0
  hitIndex.value = 0
  pendingJump = false
  nextTick(() => {
    const el = scrollEl.value
    if (el && term.stickBottom) el.scrollTop = el.scrollHeight
    if (isShell.value) autoFocusShellInput()
  })
}

// Newest match first: terminal output grows downwards, so the hit closest to
// the prompt is the one you usually want.
function onHits(n: number) {
  hitCount.value = n
  if (!n) {
    hitIndex.value = 0
    return
  }
  if (pendingJump) {
    pendingJump = false
    hitIndex.value = n - 1
    ansiRef.value?.setActiveHit(hitIndex.value, true)
  } else {
    hitIndex.value = Math.min(hitIndex.value, n - 1)
  }
}

function stepHit(delta: number) {
  const n = hitCount.value
  if (!n) return
  hitIndex.value = (hitIndex.value + delta + n) % n
  ansiRef.value?.setActiveHit(hitIndex.value, true)
}

function focusPrompt() {
  if (isShell.value) focusShellInput()
  else termInputEl.value?.focusInput()
}

function insertSlash() {
  if (isShell.value) {
    const el = activeShellInput()
    const value = shellText.value
    const here = document.activeElement === el
    const start = here ? (el?.selectionStart ?? value.length) : value.length
    const end = here ? (el?.selectionEnd ?? start) : start
    shellText.value = value.slice(0, start) + '/' + value.slice(end)
    nextTick(() => {
      const input = activeShellInput()
      input?.focus()
      const at = start + 1
      input?.setSelectionRange(at, at)
    })
    return
  }
  termInputEl.value?.insertText('/')
}

function termFindOk(): boolean {
  if (files.preview) return false
  const active = document.activeElement
  return !active || active === document.body || !!rootEl.value?.contains(active)
}

let offTermShortcuts: Array<() => void> = []

function bindTermShortcuts() {
  ensureShellShortcuts()
  shellActions.active = () => isShell.value && (shellFocused.value || boxFocused.value)
  shellActions.interrupt = (e) => {
    if (inputHasSelection(e)) return false
    term.sendKey('Ctrl+c')
    shellText.value = ''
    return true
  }
  shellActions.eof = (e) => {
    if (inputHasSelection(e)) return false
    term.sendKey('Ctrl+d')
    return true
  }
  shellActions.clear = (e) => {
    if (inputHasSelection(e)) return false
    term.sendKey('Ctrl+l')
    return true
  }
  const add = (def: Parameters<typeof register>[0]) => offTermShortcuts.push(register(def))
  add({ id: 'term.find', label: 'Search output', scope: 'global', key: 'f', mod: true, browserTab: 'blocked', blockedNote: 'In a normal browser tab, Ctrl/⌘+F is the browser find. Use the ⌕ search button', when: termFindOk, run: () => openSearch() })
  add({ id: 'term.history', label: 'Input history', scope: 'terminal', key: 'r', mod: true, browserTab: 'blocked', blockedNote: 'In a normal browser tab, Ctrl/⌘+R reloads the page. Use the ⏱ history button', when: () => termFindOk() && !isTextInput(document.activeElement), run: () => openHistory() })
  add({ id: 'term.insert', label: 'Focus the input', scope: 'terminal', key: 'i', run: () => focusPrompt() })
  add({ id: 'term.slash', label: 'Focus the input and type /', scope: 'terminal', key: '/', run: () => insertSlash() })
  add({ id: 'term.files', label: 'File tree', scope: 'terminal', key: 'e', when: () => conn.hasFeature('files'), run: () => openFiles() })
  add({ id: 'term.diff', label: 'Git Diff', scope: 'terminal', key: 'g', when: () => conn.hasFeature('git-diff'), run: () => openDiff() })
  add({ id: 'term.todo', label: 'Todo', scope: 'terminal', key: 't', run: () => { showTodo.value = !showTodo.value } })
}

function liveChord(id: string, name: string) {
  void shortcutVersion.value
  if (!isShortcutActive(id)) return name
  const chord = shortcutChord(id)
  return chord ? `${name} (${chord})` : name
}
const fileTitle = computed(() => liveChord('term.files', 'Files'))
const diffTitle = computed(() => liveChord('term.diff', 'Diff'))
const todoTitle = computed(() => liveChord('term.todo', 'Todo'))
const searchTitle = computed(() => liveChord('term.find', 'Search output'))
const historyTitle = computed(() => liveChord('term.history', 'Input history'))

// Auto-focus shell input when entering a shell pane or when pane data loads
// after mount (agents snapshot may be delayed). Keyed on isShell, not pane:
// pane is a new object on every snapshot, which would re-steal focus.
watch(isShell, (v) => {
  if (v) nextTick(() => autoFocusShellInput())
})

function onScroll() {
  const el = scrollEl.value
  if (!el) return
  const near = el.scrollHeight - el.scrollTop - el.clientHeight < 48
  term.stickBottom = near
}

// Keep the view pinned to the bottom as fresh output arrives
watch(
  () => term.content,
  async () => {
    if (!term.stickBottom) return
    await nextTick()
    const el = scrollEl.value
    if (el) el.scrollTop = el.scrollHeight
  },
)

function onOpenFile(p: { path: string; line: number | null }) {
  emit('openFile', { ...p, paneId: props.paneId })
}

function openFiles() {
  const cwd = pane.value?.cwd || '/'
  const h = pane.value?.host || 'local'
  files.openBrowser(cwd, h)
}

function openDiff() {
  if (!conn.hasFeature('git-diff')) {
    return
  }
  const cwd = pane.value?.cwd || '/'
  const h = pane.value?.host || 'local'
  diff.openDiff(cwd, h)
}

function openStatus() {
  if (!conn.hasFeature('git-commit')) return
  diff.openDiff(pane.value?.cwd || '/', pane.value?.host || 'local', 'status')
}

// Any zoom other than 100% — larger or smaller — is "zoomed": the menu item
// shows it, a header chip shows the value, and the toggle goes back to 100%.
const zoomed = computed(() => zoom.percent.value !== 100)

function toggleZoom() {
  zoom.setZoom(zoomed.value ? 1 : 1.5)
}

function markDone() {
  if (!pane.value || statusBucket.value !== 'done') return
  ws.send({ type: 'mark_pane_read', pane_id: pane.value.pane_id })
}

// Viewing a finished pane for auto_mark_read_ms auto-marks it read → idle.
// paneId is passed so a done→done switch on a reused view (mobile) restarts
// the delay for the new pane instead of firing the old pane's timer.
const isDone = computed(() => statusBucket.value === 'done')
const autoMarkReadMs = computed(() => prefs.autoMarkReadMs)
const paneIdentity = computed(() => props.paneId)
useDoneAutoMark(isDone, autoMarkReadMs, paneIdentity, markDone)

function onTodoFill(text: string) {
  term.requestFill(text)
  showTodo.value = false
}

let pollTimer: ReturnType<typeof setInterval> | null = null

// Interval comes from relay-side prefs (terminal.poll_ms); 6s is the default
// when unset. Changing the pref restarts the timer live.
const pollMs = computed(() => prefs.termPollMs || 6000)

function startPoll() {
  if (!pollTimer) pollTimer = setInterval(() => term.requestRead(50, false, true), pollMs.value)
}

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(pollMs, () => {
  if (pollTimer) {
    stopPoll()
    startPoll()
  }
})

// While the page is hidden the subscription poll stops (the relay lets the
// pane subscription expire); on return, re-read immediately before resuming.
function onVisibility() {
  if (document.hidden) {
    stopPoll()
  } else {
    term.requestRead(50, false, true)
    startPoll()
  }
}

onMounted(() => {
  term.openPane(props.paneId)
  if (scrollEl.value) zoom.bind(scrollEl.value)
  if (!document.hidden) startPoll()
  document.addEventListener('visibilitychange', onVisibility)
  bindTermShortcuts()
  // Focus shell input on mount if this is a shell pane
  if (isShell.value) nextTick(() => autoFocusShellInput())
  // Close mobile menu on outside click
  document.addEventListener('click', closeMenu)
  // Close todo panel on external close-todo-panel event (from App.vue global click)
  document.addEventListener('close-todo-panel', () => { showTodo.value = false })
})

function closeMenu() {
  showMenu.value = false
}

// React to pane switches while staying mounted (desktop split mode)
watch(
  () => props.paneId,
  (id) => {
    term.openPane(id)
    deepLoadedFor = ''
    closeSearch()
    showHistory.value = false
    if (isShell.value) nextTick(() => autoFocusShellInput())
  },
)

onUnmounted(() => {
  stopPoll()
  if (scrollEl.value) zoom.unbind(scrollEl.value)
  document.removeEventListener('click', closeMenu)
  document.removeEventListener('visibilitychange', onVisibility)
  offTermShortcuts.forEach((fn) => fn())
  offTermShortcuts = []
  shellActions.active = () => false
  shellActions.interrupt = () => false
  shellActions.eof = () => false
  shellActions.clear = () => false
  if (searchDebounce) clearTimeout(searchDebounce)
  term.closePane()
})
</script>

<template>
  <!-- No view-enter here: the fade-in would reveal the page background image
       through the translucent section on every pane switch (jarring flash). -->
  <section ref="rootEl" class="term-view" :class="{ fullscreen: term.fullscreen }" aria-label="Terminal" :aria-busy="term.loading">
    <header class="term-head">
      <button class="icon-btn" aria-label="Back to agent list" title="Back" @click="emit('back')">‹</button>
      <div class="term-title">
        <span class="name" :class="[conn.status, { loading: term.loading }]">{{ title }}</span>
        <span class="pane-id">{{ subTitle }}</span>
      </div>
      <span
        v-if="!isShell"
        class="status-badge"
        :class="[statusBucket, { clickable: statusBucket === 'done' }]"
        :role="statusBucket === 'done' ? 'button' : undefined"
        :title="statusBucket === 'done' ? 'Click to mark as idle' : undefined"
        @click="statusBucket === 'done' && markDone()"
      >
        <i class="dot"></i>{{ pane?.status || '—' }}
      </span>

      <button
        v-if="zoomed"
        type="button"
        class="zoom-chip"
        :aria-label="`Zoom ${zoom.percent.value}%, reset to 100%`"
        title="Reset zoom to 100%"
        @click="zoom.setZoom(1)"
      >{{ zoom.percent.value }}%</button>

      <!-- Desktop: show all buttons inline -->
      <div class="head-actions desktop-only">
        <button class="icon-btn" aria-label="Browse files" :title="fileTitle" @click.stop="openFiles"><TermIcon name="files" /></button>
        <button v-if="conn.hasFeature('git-diff')" class="icon-btn" aria-label="Git diff" :title="diffTitle" @click.stop="openDiff"><TermIcon name="diff" /></button>
        <button v-if="conn.hasFeature('git-commit')" class="icon-btn" aria-label="Git status" title="Git status: stage and commit" @click.stop="openStatus"><TermIcon name="status" /></button>
        <button
          class="icon-btn"
          :class="{ active: hasTodo }"
          aria-label="Todo notes"
          :title="todoTitle"
          @click="showTodo = !showTodo"
        ><TermIcon name="todo" /></button>
        <button
          class="icon-btn"
          :class="{ active: searchOpen }"
          aria-label="Search output"
          :title="searchTitle"
          :aria-pressed="searchOpen"
          @click="searchOpen ? closeSearch() : openSearch()"
        ><TermIcon name="search" /></button>
        <button
          class="icon-btn"
          :class="{ active: showHistory }"
          aria-label="Input history"
          :title="historyTitle"
          :aria-pressed="showHistory"
          @click="showHistory ? closeHistory() : openHistory()"
        ><TermIcon name="history" /></button>
        <button
          class="icon-btn wide-only"
          :class="{ active: term.fullscreen }"
          aria-label="Toggle fullscreen"
          title="Fullscreen"
          @click="term.toggleFullscreen()"
        ><TermIcon name="fullscreen" /></button>
        <button class="icon-btn" aria-label="Refresh output" title="Refresh" @click="term.clearPaneCache(props.paneId)"><TermIcon name="refresh" /></button>
      </div>

      <!-- Mobile: collapse into dropdown menu -->
      <div class="head-actions mobile-only">
        <button class="icon-btn" :class="{ active: showMenu }" aria-label="More actions" title="More" @click.stop="showMenu = !showMenu"><TermIcon name="more" /></button>
        <div v-if="showMenu" class="head-menu" role="menu" aria-label="Terminal actions" @click.stop>
          <button type="button" role="menuitem" aria-label="Browse files" @click.stop="openFiles(); showMenu = false"><TermIcon name="files" />Files</button>
          <button v-if="conn.hasFeature('git-diff')" type="button" role="menuitem" aria-label="Git diff" @click.stop="openDiff(); showMenu = false"><TermIcon name="diff" />Diff</button>
          <button v-if="conn.hasFeature('git-commit')" type="button" role="menuitem" aria-label="Git status" @click.stop="openStatus(); showMenu = false"><TermIcon name="status" />Status</button>
          <button type="button" role="menuitem" aria-label="Todo notes" :class="{ active: hasTodo }" @click="showTodo = !showTodo; showMenu = false"><TermIcon name="todo" />Todo</button>
          <button type="button" role="menuitem" aria-label="Search output" :class="{ active: searchOpen }" @click="searchOpen ? closeSearch() : openSearch(); showMenu = false"><TermIcon name="search" />Search</button>
          <button type="button" role="menuitem" aria-label="Input history" :class="{ active: showHistory }" @click="showHistory ? closeHistory() : openHistory(); showMenu = false"><TermIcon name="history" />History</button>
          <button type="button" role="menuitem" :aria-label="`Toggle zoom (now ${zoom.percent.value}%)`" :class="{ active: zoomed }" @click="toggleZoom(); showMenu = false"><TermIcon name="zoom" />Zoom<span class="menu-val">{{ zoom.percent.value }}%</span></button>
          <button type="button" role="menuitem" aria-label="Refresh output" @click="term.clearPaneCache(props.paneId); showMenu = false"><TermIcon name="refresh" />Refresh</button>
          <button type="button" role="menuitem" aria-label="Open settings" @click="emit('openSettings'); showMenu = false"><TermIcon name="settings" />Settings</button>
        </div>
      </div>
    </header>

    <div class="term-main">
      <div ref="scrollEl" class="term-scroll" @scroll.passive="onScroll" @click="isShell && autoFocusShellInput()">
        <div v-if="term.loading && !term.content" class="term-loading" aria-live="polite">
          <span class="bar"></span>
          <span class="bar"></span>
          <span class="bar"></span>
        </div>
        <div class="term-body" :class="{ shell: isShell }">
          <AnsiStream
            ref="ansiRef"
            :raw="renderContent"
            :valid-paths="pane?.valid_paths"
            :highlight="searchOpen ? appliedQuery : ''"
            :zoom="zoom.zoom.value"
            @open-file="onOpenFile"
            @hits="onHits"
          >
            <!-- Shell: inline input inside <pre>, directly after content on same line -->
            <span v-if="isShell" class="inline-input-wrap" :class="{ focused: shellFocused }" @click.stop="focusShellInput">
              <span class="inline-text">{{ shellText }}<span class="inline-cursor" aria-hidden="true"></span></span>
              <input
                ref="shellInputEl"
                v-model="shellText"
                class="inline-input"
                type="text"
                autocomplete="off"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                enterkeyhint="send"
                aria-label="Command input"
                @focus="shellFocused = true"
                @blur="shellFocused = false"
                @keydown.enter.prevent="shellSubmit"
                @keydown="onShellKeydown"
              />
            </span>
          </AnsiStream>
        </div>
      </div>
      <!-- Floating (absolute) so showing/hiding never shifts the layout. -->
      <FindBar
        v-if="searchOpen"
        ref="searchRef"
        v-model="searchQuery"
        label="output"
        :current="hitCount ? hitIndex + 1 : 0"
        :total="hitCount"
        :capped="hitCount >= MAX_HITS"
        @next="stepHit(1)"
        @prev="stepHit(-1)"
        @close="closeSearch"
      />
      <HistorySuggest
        v-if="isShell"
        class="suggest-float"
        :items="suggestions"
        :typed="shellText"
        :active="activeSuggest"
        @pick="setShellText"
      />
    </div>

    <!-- Shell: special keys + quick chips; the ❯ toggle reveals an optional
         input box that shares the inline prompt's draft (v-model). -->
    <TermInput v-if="!isShell" ref="termInputEl" :pane="pane" @edit="emit('editCommands', 'skills')" @history="openHistory" />
    <ShellKeys
      v-else
      ref="shellKeysEl"
      v-model="shellText"
      :pane="pane"
      @fill="onShellFill"
      @edit="emit('editCommands', 'shell_cmds')"
      @input-keydown="onShellKeydown"
      @input-focus="boxFocused = $event"
    />
    <HistoryPanel
      v-if="showHistory"
      :where="whereOf(pane)"
      :default-kind="isShell ? 'sh' : 'ag'"
      @pick="pickHistory"
      @close="closeHistory"
    />
    <TodoPanel
      v-if="showTodo"
      :binding="todoBinding"
      :title="`todo · ${title}`"
      @close="showTodo = false"
      @fill="onTodoFill"
    />
  </section>
</template>

<style scoped>
.term-view {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.term-view.fullscreen {
  position: fixed;
  inset: 0;
  z-index: 60;
  background: var(--term-bg);
}

.term-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
  flex-shrink: 0;
  position: relative;
  z-index: 10;
}
.icon-btn {
  font-family: var(--font-mono);
  font-size: var(--text-md);
  line-height: 1;
  color: var(--muted);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  transition: color 0.15s, background 0.15s, transform 0.1s;
  flex-shrink: 0;
}
.icon-btn:hover { color: var(--text); background: var(--surface-3); }
.icon-btn:active { transform: scale(0.92); }
.icon-btn.active { color: var(--blue); border-color: rgba(96,165,250,.5); background: var(--blue-soft); }

.head-actions { display: flex; gap: 6px; flex-shrink: 0; }
.desktop-only { display: flex; }
.mobile-only { display: none; position: relative; }
.head-menu {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  z-index: 100;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 8px 24px rgba(0,0,0,0.3);
  min-width: 140px;
  animation: view-in 0.15s ease both;
}
.head-menu button {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  color: var(--text-dim);
  background: transparent;
  border: none;
  border-radius: var(--radius);
  text-align: left;
  white-space: nowrap;
}
.head-menu button:hover { background: var(--surface-2); color: var(--text); }
.head-menu button.active { color: var(--blue); }
.menu-val { margin-left: auto; padding-left: 12px; font-size: var(--text-2xs); color: var(--muted); }
.head-menu button.active .menu-val { color: var(--blue); }
.zoom-chip {
  flex-shrink: 0;
  height: 24px;
  padding: 0 8px;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--blue);
  background: var(--blue-soft);
  border: 1px solid rgba(96, 165, 250, .5);
  border-radius: 999px;
}
.zoom-chip:hover { filter: brightness(1.15); }

@media (max-width: 639px) {
  .desktop-only { display: none; }
  .mobile-only { display: flex; }
}

.term-title { flex: 1; min-width: 0; }
.term-title .name {
  display: block;
  font-weight: 700;
  font-size: var(--text-md);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.term-title .name.connected { color: var(--green); }
.term-title .name.connecting { color: var(--orange); animation: pulse 1.6s ease-in-out infinite; }
.term-title .name.disconnected { color: var(--red); }
.term-title .name.loading { color: var(--green); animation: pulse 1.6s ease-in-out infinite; }
.term-title .pane-id {
  display: block;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-top: 1px;
}

.status-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  text-transform: uppercase;
  letter-spacing: 0.1em;
  padding: 4px 10px;
  border-radius: 999px;
  border: 1px solid var(--border);
  color: var(--muted);
  flex-shrink: 0;
}
.status-badge .dot { width: 6px; height: 6px; border-radius: 50%; background: var(--faint); }
.status-badge.working { color: var(--orange); border-color: rgba(251,146,60,.4); background: var(--orange-soft); }
.status-badge.working .dot { background: var(--orange); animation: pulse 1.6s ease-in-out infinite; }
.status-badge.blocked { color: var(--red); border-color: rgba(248,113,113,.4); background: var(--red-soft); }
.status-badge.blocked .dot { background: var(--red); animation: pulse 0.85s ease-in-out infinite; }
.status-badge.done { color: var(--green); border-color: rgba(52,211,153,.4); background: var(--green-soft); }
.status-badge.done .dot { background: var(--green); }
.status-badge.clickable { cursor: pointer; transition: filter 0.15s; }
.status-badge.clickable:hover { filter: brightness(1.15); }

.term-main {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
/* Sits just above the prompt line (term-scroll bottom padding + one line), so
   it never covers what is being typed. */
.suggest-float {
  position: absolute;
  left: 8px;
  right: 8px;
  bottom: 40px;
  z-index: 15;
}

.term-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  background:
    radial-gradient(50% 150% at 50% 0%, #60a5fa0a, var(--term-bg) 70%),
    var(--term-bg);
  padding: 14px 16px 20px;
}
.term-body { font-size: calc(12.5px * var(--term-zoom, 1)); }

/* Wide screens: bump terminal font for readability */
@media (min-width: 1800px) {
  .term-body { font-size: calc(14.5px * var(--term-zoom, 1)); }
}

/* Shell inline input — rendered inline after terminal content on same line */
.term-body.shell { cursor: text; }
.inline-input-wrap {
  display: inline;
  position: relative;
  cursor: text;
}
.inline-text {
  color: var(--term-fg);
  font-family: var(--font-mono);
  white-space: pre;
}
.inline-cursor {
  display: inline-block;
  width: 7px;
  height: 14px;
  vertical-align: -2px;
  background: var(--green);
  margin-left: 1px;
}
.inline-input-wrap.focused .inline-cursor {
  animation: blink 1.1s step-end infinite;
}
.inline-input-wrap:not(.focused) .inline-cursor {
  opacity: 0.4;
}
.inline-input {
  position: absolute;
  top: 0; left: 0;
  width: 100%;
  height: 100%;
  opacity: 0;
  background: transparent;
  border: none;
  outline: none;
  color: transparent;
  caret-color: transparent;
  font-family: var(--font-mono);
  font-size: calc(12.5px * var(--term-zoom, 1));
  z-index: 10;
}
@media (min-width: 1800px) {
  .inline-input { font-size: calc(14.5px * var(--term-zoom, 1)); }
}

.term-loading { display: flex; flex-direction: column; gap: 9px; padding-top: 6px; }
.term-loading .bar {
  height: 11px;
  border-radius: 3px;
  background: linear-gradient(90deg, var(--surface-2) 25%, var(--surface-3) 50%, var(--surface-2) 75%);
  background-size: 200% 100%;
  animation: shimmer 1.3s linear infinite;
}
.term-loading .bar:nth-child(1) { width: 82%; }
.term-loading .bar:nth-child(2) { width: 64%; animation-delay: 0.12s; }
.term-loading .bar:nth-child(3) { width: 73%; animation-delay: 0.24s; }

@keyframes pulse { 0%, 100% { opacity: 0.4; } 50% { opacity: 0.8; } }

/* Desktop: larger terminal text (12.5px is tuned for mobile) */
@media (min-width: 900px) {
  .term-body { font-size: calc(14px * var(--term-zoom, 1)); }
  .inline-input { font-size: calc(14px * var(--term-zoom, 1)); }
}

/* Below 900px the app header is already hidden in the terminal view, so the
   terminal fills the screen and "fullscreen" would change nothing. */
@media (max-width: 899px) {
  .wide-only { display: none; }
}
</style>
