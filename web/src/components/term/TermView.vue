<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useTerminalStore } from '../../stores/terminal'
import { useAgentsStore, bucket, paneTitle, workspaceLabel, paneKind } from '../../stores/connection'
import { useConnectionStore } from '../../stores/connection'
import { useFilesStore } from '../../stores/files'
import { useDiffStore } from '../../stores/diff'
import { useTodoStore } from '../../stores/todo'
import { usePrefsStore } from '../../stores/prefs'
import { useTermZoom } from '../../composables/useTermZoom'
import { useDoneAutoMark } from '../../composables/useDoneAutoMark'
import { ws } from '../../api/ws'
import TermInput from './TermInput.vue'
import ShellKeys from './ShellKeys.vue'
import AnsiStream from './AnsiStream.vue'
import { tailWindow, computeTailWindow } from '../../utils/tailWindow'
import { insertSnippet, prependSnippet } from '../../utils/inputFill'
import TodoPanel from '../todo/TodoPanel.vue'
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
// tail window and we track how many lines were dropped from the front. When
// the user scrolls up, stickBottom flips and the view switches to the full
// content; without compensation that transition would jump the viewport into
// the middle of the history. We preserve the visible line by translating the
// scroll position by the dropped line count (× measured line height).
const windowDroppedLines = ref(0)
const renderContent = computed(() => {
  const raw = term.content
  if (!raw) return raw
  // When the user scrolls up to read history, render the full content so the
  // scroll-back window stays intact; windowing only applies when pinned to the
  // bottom (the streaming case that dominates cost during agent output).
  if (!term.stickBottom) return raw
  return tailWindow(raw, RENDER_WINDOW_LINES)
})

// Track the window offset separately from the render computed: update it when
// content changes while pinned to the bottom (the windowed state), so the
// scroll-compensation watch below can read a stable value without the computed
// having side effects.
watch(
  () => [term.content, term.stickBottom] as const,
  ([raw, stick]) => {
    if (stick && raw) {
      windowDroppedLines.value = computeTailWindow(raw, RENDER_WINDOW_LINES).droppedLines
    }
  },
  { immediate: true },
)

function measureLineHeight(): number {
  const el = scrollEl.value
  if (!el) return 0
  // Measure the actual rendered line height by copying the computed style of
  // a real terminal text node (inherits font/line-height from .term-body and
  // .ansi-stream, including zoom), instead of a hard-coded probe.
  const sample = el.querySelector('.ansi-stream')
  if (!sample) return 0
  const probe = document.createElement('div')
  const cs = getComputedStyle(sample)
  probe.style.cssText = 'position:absolute;visibility:hidden;'
  probe.style.fontFamily = cs.fontFamily
  probe.style.fontSize = cs.fontSize
  probe.style.lineHeight = cs.lineHeight
  probe.textContent = 'M'
  el.appendChild(probe)
  const h = probe.getBoundingClientRect().height
  probe.remove()
  return h || 0
}

// Preserve the visible line when switching between windowed (bottom) and full
// rendering on stickBottom flips, so scrolling up does not jump to the middle
// of the history.
watch(
  () => term.stickBottom,
  async (now, before) => {
    const el = scrollEl.value
    if (!el) return
    await nextTick()
    const prevScrollTop = el.scrollTop
    const lineH = measureLineHeight()
    const dropped = windowDroppedLines.value
    if (!now && before) {
      // bottom (windowed) → scrolled up (full): the full content has the
      // dropped lines before what we saw, so shift down by that many lines.
      if (lineH > 0) {
        const maxTop = Math.max(0, el.scrollHeight - el.clientHeight)
        el.scrollTop = Math.min(prevScrollTop + dropped * lineH, maxTop)
      }
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
  shellText.value = ''
}

// The shell's active text input: the ShellKeys input box when it is toggled
// on, otherwise the inline input rendered inside the terminal output.
function activeShellInput(): HTMLInputElement | null {
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
    if (!el) return
    el.focus()
    el.setSelectionRange(r.cursor, r.cursor)
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
    fillShellInput(sig.text, sig.kind)
  },
)

function focusShellInput() {
  activeShellInput()?.focus()
}

// Auto-focus shell input when entering a shell pane or when pane data loads
watch(isShell, (v) => {
  if (v) nextTick(() => focusShellInput())
})
// Also focus when pane data arrives after mount (agents snapshot may be delayed)
watch(pane, (p) => {
  if (p && paneKind(p) === 'shell') nextTick(() => focusShellInput())
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

function toggleZoom() {
  if (zoom.zoom.value > 1.05) zoom.setZoom(1)
  else zoom.setZoom(1.5)
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
  // Focus shell input on mount if this is a shell pane
  if (isShell.value) nextTick(() => focusShellInput())
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
    if (isShell.value) nextTick(() => focusShellInput())
  },
)

onUnmounted(() => {
  stopPoll()
  if (scrollEl.value) zoom.unbind(scrollEl.value)
  document.removeEventListener('click', closeMenu)
  document.removeEventListener('visibilitychange', onVisibility)
  term.closePane()
})
</script>

<template>
  <!-- No view-enter here: the fade-in would reveal the page background image
       through the translucent section on every pane switch (jarring flash). -->
  <section class="term-view" :class="{ fullscreen: term.fullscreen }" aria-label="Terminal" :aria-busy="term.loading">
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

      <!-- Desktop: show all buttons inline -->
      <div class="head-actions desktop-only">
        <button class="icon-btn" aria-label="Browse files" title="Files" @click.stop="openFiles">🗂</button>
        <button v-if="conn.hasFeature('git-diff')" class="icon-btn" aria-label="Git diff" title="Diff" @click.stop="openDiff">⎇</button>
        <button
          class="icon-btn"
          :class="{ active: hasTodo }"
          aria-label="Todo notes"
          title="Todo"
          @click="showTodo = !showTodo"
        >✎</button>
        <button
          class="icon-btn"
          :class="{ active: term.fullscreen }"
          aria-label="Toggle fullscreen"
          title="Fullscreen"
          @click="term.toggleFullscreen()"
        >⛶</button>
        <button class="icon-btn" aria-label="Refresh output" title="Refresh" @click="term.clearPaneCache(props.paneId)">⟳</button>
      </div>

      <!-- Mobile: collapse into dropdown menu -->
      <div class="head-actions mobile-only">
        <button class="icon-btn" :class="{ active: showMenu }" aria-label="More actions" title="More" @click.stop="showMenu = !showMenu">⋮</button>
        <div v-if="showMenu" class="head-menu" role="menu" aria-label="Terminal actions" @click.stop>
          <button type="button" role="menuitem" aria-label="Browse files" @click.stop="openFiles(); showMenu = false">🗂 Files</button>
          <button v-if="conn.hasFeature('git-diff')" type="button" role="menuitem" aria-label="Git diff" @click.stop="openDiff(); showMenu = false">⎇ Diff</button>
          <button type="button" role="menuitem" aria-label="Todo notes" :class="{ active: hasTodo }" @click="showTodo = !showTodo; showMenu = false">✎ Todo</button>
          <button type="button" role="menuitem" aria-label="Toggle zoom" :class="{ active: zoom.zoom.value > 1.05 }" @click="toggleZoom(); showMenu = false">⤢ Zoom</button>
          <button type="button" role="menuitem" aria-label="Toggle fullscreen" :class="{ active: term.fullscreen }" @click="term.toggleFullscreen(); showMenu = false">⛶ Fullscreen</button>
          <button type="button" role="menuitem" aria-label="Refresh output" @click="term.clearPaneCache(props.paneId); showMenu = false">⟳ Refresh</button>
          <button type="button" role="menuitem" aria-label="Open settings" @click="emit('openSettings'); showMenu = false">⚙ Settings</button>
        </div>
      </div>
    </header>

    <div ref="scrollEl" class="term-scroll" @scroll.passive="onScroll" @click="isShell && focusShellInput()">
      <div v-if="term.loading && !term.content" class="term-loading" aria-live="polite">
        <span class="bar"></span>
        <span class="bar"></span>
        <span class="bar"></span>
      </div>
      <div class="term-body" :class="{ shell: isShell }">
        <AnsiStream :raw="renderContent" :valid-paths="pane?.valid_paths" @open-file="onOpenFile">
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
            />
          </span>
        </AnsiStream>
      </div>
    </div>

    <!-- Shell: special keys + quick chips; the ❯ toggle reveals an optional
         input box that shares the inline prompt's draft (v-model). -->
    <TermInput v-if="!isShell" :pane="pane" @edit="emit('editCommands', 'skills')" />
    <ShellKeys
      v-else
      ref="shellKeysEl"
      v-model="shellText"
      :pane="pane"
      @fill="onShellFill"
      @edit="emit('editCommands', 'shell_cmds')"
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
.term-body { font-size: 12.5px; }

/* Wide screens: bump terminal font for readability */
@media (min-width: 1800px) {
  .term-body { font-size: 14.5px; }
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
  font-size: 12.5px;
  z-index: 10;
}
@media (min-width: 1800px) {
  .inline-input { font-size: 14.5px; }
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
  .term-body { font-size: 14px; }
  .inline-input { font-size: 14px; }
}
</style>
