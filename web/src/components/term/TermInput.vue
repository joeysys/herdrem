<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useTerminalStore } from '../../stores/terminal'
import { useTunnelsStore } from '../../stores/tunnels'
import { useHaptic } from '../../composables/useHaptic'
import { useKeyRepeat } from '../../composables/useKeyRepeat'
import { useQuickPopover } from '../../composables/useQuickPopover'
import {
  detectMenu, isCursorAgent, cursorKeySequence, isStickyChoice, menuLabel,
} from '../../utils/menu'
import type { Pane } from '../../api/types'
import ArrowPad from './ArrowPad.vue'

const props = defineProps<{ pane: Pane | null }>()
const emit = defineEmits<{ edit: [] }>()
const term = useTerminalStore()
const tunnelsStore = useTunnelsStore()
const { haptic } = useHaptic()
const { start, stop } = useKeyRepeat()

const text = ref('')
const cmdInputEl = ref<HTMLInputElement | null>(null)
const showArrows = ref(false)
const arrowBtnEl = ref<HTMLElement | null>(null)
const arrowPos = ref({ left: -1, right: -1, bottom: 0 })
const isDesktop = ref(typeof window !== 'undefined' && window.matchMedia('(hover: hover) and (pointer: fine)').matches)

function toggleArrows() {
  showArrows.value = !showArrows.value
  if (showArrows.value && arrowBtnEl.value) {
    const r = arrowBtnEl.value.getBoundingClientRect()
    const bottom = window.innerHeight - r.top + 8
    if (isDesktop.value) {
      arrowPos.value = { left: r.left, right: -1, bottom }
    } else {
      // Mobile: right-align
      arrowPos.value = { left: -1, right: 12, bottom }
    }
  }
}

// Restore draft when pane changes
watch(
  () => props.pane?.pane_id,
  (id) => {
    if (id) text.value = term.getInputDraft(id)
  },
  { immediate: true },
)
// Persist draft on change
watch(text, (t) => {
  if (props.pane?.pane_id) term.setInputDraft(props.pane.pane_id, t)
})

const isBlocked = computed(() => (props.pane?.status || '').toLowerCase().includes('block'))
const agentName = computed(() => String(props.pane?.display_agent || props.pane?.agent || ''))
const isCursor = computed(() => isCursorAgent(agentName.value))

// Disarm filled text when the approve-bar appears so a stale armed state
// can't cause an extra Enter to be sent after the user approves.
watch(isBlocked, (blocked) => {
  if (blocked) filledText.value = ''
})

// --- quick popover (skills / agent cmds) ---
const qp = useQuickPopover(() => agentName.value, 'agent')
const qpBtnSkillsEl = ref<HTMLElement | null>(null)
const qpBtnCmdsEl = ref<HTMLElement | null>(null)
function openSkills() {
  qp.btnEl.value = qpBtnSkillsEl.value
  qp.toggle('skills')
}
function openCmds() {
  qp.btnEl.value = qpBtnCmdsEl.value
  qp.toggle('agentCmds')
}
function hoverSkills() {
  qp.btnEl.value = qpBtnSkillsEl.value
  qp.hoverOpen('skills')
}
function hoverCmds() {
  qp.btnEl.value = qpBtnCmdsEl.value
  qp.hoverOpen('agentCmds')
}
function qpFill(text: string) {
  haptic()
  onFill(text)
  qp.close()
  // Auto-focus input at end of text so Enter fills, Ctrl+Enter quick-sends
  nextTick(() => {
    const el = cmdInputEl.value
    if (!el) return
    el.focus()
    const len = el.value.length
    el.setSelectionRange(len, len)
  })
}
function qpEdit() {
  emit('edit')
  qp.close()
}

// --- Ctrl modifier (auto-reset after send or 5s idle) ---
const ctrlActive = ref(false)
let ctrlTimer: ReturnType<typeof setTimeout> | null = null
const CTRL_SKIP = new Set(['Enter', 'Tab', 'Escape', 'Backspace', 'Up', 'Down', 'Left', 'Right'])

function toggleCtrl() {
  ctrlActive.value = !ctrlActive.value
  if (ctrlTimer) clearTimeout(ctrlTimer)
  if (ctrlActive.value) ctrlTimer = setTimeout(() => (ctrlActive.value = false), 5000)
}
function consumeCtrl(key: string): string {
  if (ctrlActive.value && !CTRL_SKIP.has(key) && key.length === 1) {
    resetCtrl()
    return 'Ctrl+' + key.toLowerCase()
  }
  resetCtrl()
  return key
}
function resetCtrl() {
  ctrlActive.value = false
  if (ctrlTimer) { clearTimeout(ctrlTimer); ctrlTimer = null }
}

// --- approval options: relay → client-parse fallback → generic ---
interface ApproveOption { payload: string; label: string; full: string; index: number; tone: 'yes' | 'no' | 'neutral'; sticky: boolean }

const approveOptions = computed<ApproveOption[]>(() => {
  const p = props.pane
  if (!p) return []
  let options = p.options || []
  let labels = p.option_labels || []

  if (!options.length && term.content) {
    const parsed = detectMenu(term.content)
    options = parsed.options
    labels = parsed.labels
  }
  if (!options.length) {
    return [
      { payload: 'y', label: 'y · approve', full: 'approve', index: 0, tone: 'yes', sticky: false },
      { payload: 'n', label: 'n · deny', full: 'deny', index: 1, tone: 'no', sticky: false },
    ]
  }
  return options.slice(0, 4).map((payload, i) => {
    const full = labels[i] || cursorLabel(payload)
    return {
      payload,
      label: menuLabel(full) || payload,
      full,
      index: i,
      tone: toneFor(payload, full),
      sticky: isStickyChoice(full),
    }
  })
})

function cursorLabel(payload: string): string {
  if (isCursor.value) {
    const map: Record<string, string> = { y: 'allow once', a: 'allow always', n: 'reject' }
    return map[payload] || payload
  }
  return payload
}

function toneFor(payload: string, label?: string): 'yes' | 'no' | 'neutral' {
  const s = `${payload} ${label || ''}`.toLowerCase()
  if (/\byes\b|approve|allow|accept|continue|^y\b/.test(s)) return 'yes'
  if (/\bno\b|deny|reject|abort|skip|^n\b/.test(s)) return 'no'
  return 'neutral'
}

function approve(opt: ApproveOption) {
  haptic(18)
  if (isCursor.value) {
    term.sendKeySequence(cursorKeySequence(opt.index))
  } else {
    term.respond(opt.payload, opt.index)
  }
}

// --- keys & input ---
const AGENT_KEYS: Array<{ label: string; key: string; danger?: boolean; repeat?: boolean }> = [
  { label: 'Enter', key: 'Enter' },
  { label: 'Esc', key: 'Escape' },
  { label: '^C', key: 'Ctrl+c', danger: true, repeat: true },
  { label: '^D', key: 'Ctrl+d', danger: true, repeat: true },
]
const SPECIAL_KEYS = computed(() => AGENT_KEYS)

// Two-phase send: first click fills text into term (no Enter),
// second click sends Enter to execute.
const filledText = ref('')
const isArmed = computed(() => filledText.value !== '')

function sendKey(key: string) {
  haptic(8)
  term.sendKey(consumeCtrl(key))
  filledText.value = '' // any key press disarms
}

function pressSpecial(k: { key: string; repeat?: boolean }) {
  if (k.repeat) start(() => sendKey(k.key))
  else sendKey(k.key)
}

function onArrowKey(key: string) {
  term.sendKey(consumeCtrl(key))
  filledText.value = ''
}

function submit() {
  haptic()
  const rawText = text.value.trim()
  // F10: Use !tunnel prefix instead of /tunnel to avoid hijacking shell input.
  // The ! prefix is a common command-mode convention and won't conflict with
  // programs that accept /tunnel as literal input.
  const tunnelMatch = rawText.match(/^!tunnel\s+(\d+)$/i)
  if (tunnelMatch) {
    const port = parseInt(tunnelMatch[1], 10)
    if (port > 0 && port <= 65535) {
      tunnelsStore.createTunnel(port)
      tunnelsStore.showPanel = true
      text.value = ''
      filledText.value = ''
      return
    }
  }

  if (isArmed.value) {
    // Second click: send Enter to execute
    term.sendKey('Enter')
    text.value = ''
    filledText.value = ''
  } else {
    // First click: fill text into term without submitting
    const t = text.value
    if (!t) return
    term.sendText(t, false)
    filledText.value = t
    text.value = '' // clear input after fill
  }
}

function onInput() {
  // New text typed — disarm so next send re-fills
  filledText.value = ''
}

// Ctrl+Enter: quick send — fill text + Enter in one shot (skips two-phase)
function quickSend() {
  haptic()
  const t = text.value
  if (!t) return
  term.sendText(t, true)
  text.value = ''
  filledText.value = ''
}

function onFill(t: string) {
  text.value = t
  filledText.value = '' // chip fill doesn't send to term, so disarm
}

// react to fill requests from the todo panel (or elsewhere)
watch(
  () => term.fillSignal,
  (sig) => {
    if (sig) text.value = sig.text
  },
)
</script>

<template>
  <div class="term-input">
    <div v-if="isBlocked" class="approve-bar" role="group" aria-label="Approve agent">
      <span class="approve-label">waiting</span>
      <button
        v-for="opt in approveOptions"
        :key="opt.payload + opt.index"
        type="button"
        class="appr-btn"
        :class="[opt.tone, { sticky: opt.sticky }]"
        :title="opt.full"
        @click="approve(opt)"
      >{{ opt.label }}</button>
    </div>

    <div class="key-row">
      <div class="qp-wrap" @mouseleave="qp.hoverClose()">
        <button
          ref="qpBtnSkillsEl"
          type="button"
          class="key qp-btn skills"
          :class="{ on: qp.open.value === 'skills' }"
          :disabled="!qp.skillItems.value.length"
          aria-label="Skills"
          title="Skills"
          @click.stop="openSkills"
          @pointerdown.prevent
          @mouseenter="hoverSkills"
        ><svg class="skill-icon" viewBox="0 0 24 24" width="14" height="14" fill="currentColor" aria-hidden="true"><path d="M13 2L3 14h7l-1 8 10-12h-7l1-8z"/></svg></button>
      </div>
      <div class="qp-wrap" @mouseleave="qp.hoverClose()">
        <button
          ref="qpBtnCmdsEl"
          type="button"
          class="key qp-btn cmds"
          :class="{ on: qp.open.value === 'agentCmds' }"
          :disabled="!qp.agentCmdItems.value.length"
          aria-label="Agent commands"
          title="Agent commands"
          @click.stop="openCmds"
          @pointerdown.prevent
          @mouseenter="hoverCmds"
        >▶</button>
      </div>
      <button
        ref="arrowBtnEl"
        type="button"
        class="key arrows"
        :class="{ active: showArrows }"
        aria-label="Arrow pad"
        title="Arrow keys pad"
        @click="toggleArrows"
        @pointerdown.prevent
      >⌖</button>
      <button
        v-for="k in SPECIAL_KEYS"
        :key="k.key"
        type="button"
        class="key"
        :class="{ danger: k.danger }"
        :title="k.key"
        @pointerdown="pressSpecial(k)"
        @pointerup="stop"
        @pointerleave="stop"
      >{{ k.label }}</button>
      <button
        type="button"
        class="key ctrl"
        :class="{ active: ctrlActive }"
        :aria-pressed="ctrlActive"
        title="Ctrl modifier (next key sent as Ctrl+key)"
        @click="toggleCtrl"
      >ctrl</button>
    </div>

    <Teleport to="body">
      <div v-if="qp.open.value === 'skills'" class="qp-pop" :style="{ left: qp.pos.value.left + 'px', bottom: qp.pos.value.bottom + 'px' }" @click.stop @mouseenter="qp.cancelClose()" @mouseleave="qp.hoverClose()">
        <button
          v-for="(s, i) in qp.skillItems.value"
          :key="'sk' + i"
          type="button"
          class="qp-item"
          :title="s.text"
          @click="qpFill(s.text)"
        >{{ s.label }}</button>
        <button type="button" class="qp-edit" @click="qpEdit">✎ edit skills</button>
      </div>
      <div v-if="qp.open.value === 'agentCmds'" class="qp-pop" :style="{ left: qp.pos.value.left + 'px', bottom: qp.pos.value.bottom + 'px' }" @click.stop @mouseenter="qp.cancelClose()" @mouseleave="qp.hoverClose()">
        <button
          v-for="(c, i) in qp.agentCmdItems.value"
          :key="'ac' + i"
          type="button"
          class="qp-item"
          :title="c"
          @click="qpFill(c)"
        >{{ c.length > 30 ? c.slice(0, 30) + '…' : c }}</button>
        <button type="button" class="qp-edit" @click="qpEdit">✎ edit commands</button>
      </div>
    </Teleport>

    <form class="input-row" @submit.prevent="submit">
      <span class="prompt" aria-hidden="true">❯</span>
      <input
        ref="cmdInputEl"
        v-model="text"
        class="cmd"
        type="text"
        :placeholder="isArmed ? 'press send to execute…' : 'send to pane…'"
        autocomplete="off"
        autocapitalize="off"
        autocorrect="off"
        spellcheck="false"
        enterkeyhint="send"
        aria-label="Command to send to pane"
        @input="onInput"
        @keydown.ctrl.enter.prevent="quickSend"
      />
      <button type="submit" class="send-btn" :class="{ armed: isArmed }" :disabled="!text.length && !isArmed">
        {{ isArmed ? 'send' : 'fill' }}
      </button>
    </form>

    <Teleport to="body">
      <div v-if="showArrows" class="arrow-pop" :style="{
        left: arrowPos.left >= 0 ? arrowPos.left + 'px' : 'auto',
        right: arrowPos.right >= 0 ? arrowPos.right + 'px' : 'auto',
        bottom: arrowPos.bottom + 'px',
      }" @click.stop>
        <ArrowPad @key="onArrowKey" />
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.term-input {
  position: relative;
  flex-shrink: 0;
  border-top: 1px solid var(--border);
  background: var(--term-input-bg);
  padding: 8px 12px calc(10px + env(safe-area-inset-bottom, 0px));
}

.approve-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 9px;
  padding: 7px 10px;
  border-radius: var(--radius);
  border: 1px solid rgba(248, 113, 113, 0.35);
  background: linear-gradient(90deg, var(--red-soft), transparent 80%);
  animation: view-in 0.2s ease both;
}
.approve-label {
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  text-transform: uppercase;
  letter-spacing: 0.14em;
  color: var(--red);
  animation: pulse 1.2s ease-in-out infinite;
}
.appr-btn {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 700;
  padding: 7px 14px;
  border-radius: var(--radius);
  border: 1px solid;
  transition: transform 0.1s, filter 0.15s;
}
.appr-btn:active { transform: scale(0.94); }
.appr-btn.yes { color: #06281a; background: var(--green); border-color: var(--green); }
.appr-btn.yes:hover { filter: brightness(1.12); }
.appr-btn.no { color: var(--red); background: transparent; border-color: rgba(248, 113, 113, 0.5); }
.appr-btn.no:hover { background: var(--red-soft); }
.appr-btn.neutral { color: var(--text-dim); background: var(--surface-2); border-color: var(--border-strong); }
.appr-btn.neutral:hover { background: var(--surface-3); color: var(--text); }
.appr-btn.sticky { box-shadow: 0 0 0 2px rgba(251, 191, 36, 0.35); border-style: dashed; }

.key-row { display: flex; gap: 6px; margin-bottom: 6px; padding-bottom: 2px; overflow-x: auto; scrollbar-width: none; }
.key-row::-webkit-scrollbar { display: none; }
.key { flex-shrink: 0; }
.key {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 600;
  color: var(--text-dim);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-bottom-width: 2px;
  border-radius: var(--radius);
  padding: 5px 12px;
  user-select: none;
  transition: transform 0.08s, background 0.15s, color 0.15s;
}
.key:hover { background: var(--surface-3); color: var(--text); }
.key:active { transform: translateY(1px); border-bottom-width: 1px; }
.key.danger { color: var(--red); }
.key.danger:hover { background: var(--red-soft); }
.key.ctrl.active, .key.arrows.active {
  color: var(--blue);
  background: var(--blue-soft);
  border-color: rgba(96, 165, 250, 0.5);
}

.input-row {
  display: flex;
  align-items: center;
  gap: 9px;
  background: var(--term-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 8px 10px;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.input-row:focus-within {
  border-color: rgba(96, 165, 250, 0.5);
  box-shadow: 0 0 0 3px rgba(96, 165, 250, 0.12);
}
.prompt { color: var(--green); font-family: var(--font-mono); font-weight: 700; }
.cmd {
  flex: 1; min-width: 0;
  background: transparent; border: none; outline: none;
  color: var(--term-fg);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  caret-color: var(--green);
}
.cmd::placeholder { color: var(--faint); }
.send-btn {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--blue);
  background: var(--blue-soft);
  border: 1px solid rgba(96, 165, 250, 0.4);
  border-radius: var(--radius);
  padding: 6px 14px;
  transition: opacity 0.15s, filter 0.15s, transform 0.1s;
}
.send-btn:hover:not(:disabled) { filter: brightness(1.2); }
.send-btn:active:not(:disabled) { transform: scale(0.94); }
.send-btn:disabled { opacity: 0.35; cursor: default; }
.send-btn.armed { color: #06281a; background: var(--green); border-color: var(--green); }

/* Quick popover buttons inline in key-row */
.qp-wrap { position: relative; flex-shrink: 0; }
.qp-btn { height: 28px; display: inline-grid; place-items: center; line-height: 1; }
.qp-btn:active { transform: none; border-bottom-width: 2px; }
.qp-btn.skills.on { color: var(--amber); border-color: rgba(251,191,36,.5); background: var(--amber-soft); }
.qp-btn.cmds.on { color: var(--blue); border-color: rgba(96,165,250,.5); background: var(--blue-soft); }
.skill-icon { display: block; }
</style>

<style>
/* Teleported popover — global, position: fixed to escape overflow */
.qp-pop {
  position: fixed;
  z-index: 9999;
  min-width: 160px;
  max-width: 280px;
  max-height: 240px;
  overflow-y: auto;
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  padding: 5px;
  animation: view-in 0.15s ease both;
}
.qp-pop .qp-item {
  display: block; width: 100%; text-align: left;
  font-family: var(--font-mono); font-size: var(--text-xs);
  color: var(--text-dim); background: transparent; border: none;
  border-radius: var(--radius); padding: 7px 10px; cursor: pointer;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  transition: background 0.12s, color 0.12s;
}
.qp-pop .qp-item:hover { background: var(--surface-2); color: var(--text); }
.qp-pop .qp-edit {
  display: block; width: 100%; text-align: left;
  font-family: var(--font-mono); font-size: var(--text-2xs);
  color: var(--muted); background: transparent; border: none;
  border-top: 1px dashed var(--border);
  border-radius: 0 0 var(--radius) var(--radius);
  padding: 7px 10px; cursor: pointer; margin-top: 2px;
}
.qp-pop .qp-edit:hover { color: var(--text); background: var(--surface-2); }

/* Teleported arrow pad — global */
.arrow-pop {
  position: fixed;
  z-index: 9999;
  animation: view-in 0.16s ease both;
}
</style>
