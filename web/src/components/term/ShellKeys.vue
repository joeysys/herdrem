<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useTerminalStore } from '../../stores/terminal'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { useHaptic } from '../../composables/useHaptic'
import { useKeyRepeat } from '../../composables/useKeyRepeat'
import { useQuickPopover } from '../../composables/useQuickPopover'
import { useWrappingInput } from '../../composables/useWrappingInput'
import { useConnectionStore } from '../../stores/connection'
import {
  detectMenu, isStickyChoice, menuLabel,
} from '../../utils/menu'
import { safeStorage } from '../../utils/storage'
import { whereOf } from '../../utils/cmdHistory'
import type { Pane } from '../../api/types'
import { useKeyRowConfig } from '../../composables/useKeyRowConfig'
import ArrowPad from './ArrowPad.vue'
import KeyRowConfigModal from './KeyRowConfigModal.vue'

const props = defineProps<{ pane: Pane | null; modelValue?: string }>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  fill: [text: string]
  edit: []
  // The optional input box reports key/focus events so TermView can drive
  // command suggestions for it exactly as for the inline prompt.
  inputKeydown: [e: KeyboardEvent]
  inputFocus: [focused: boolean]
}>()

const term = useTerminalStore()
const history = useCmdHistoryStore()
const { haptic } = useHaptic()
const { start, stop } = useKeyRepeat()

const showArrows = ref(false)
const arrowBtnEl = ref<HTMLElement | null>(null)
const rootEl = ref<HTMLElement | null>(null)
const arrowPos = ref({ left: -1, right: -1, bottom: 0 })
const isDesktop = ref(typeof window !== 'undefined' && window.matchMedia('(hover: hover) and (pointer: fine)').matches)
const agentName = computed(() => String(props.pane?.display_agent || props.pane?.agent || ''))

// --- quick popover (shell cmds only in shell mode) ---
const qp = useQuickPopover(() => agentName.value, 'shell')
const qpBtnCmdsEl = ref<HTMLElement | null>(null)
function openCmds() {
  qp.btnEl.value = qpBtnCmdsEl.value
  qp.toggle('shellCmds')
}
function hoverCmds() {
  qp.btnEl.value = qpBtnCmdsEl.value
  qp.hoverOpen('shellCmds')
}
function qpFill(text: string) {
  haptic()
  emit('fill', text)
  qp.close()
}
function qpEdit() {
  emit('edit')
  qp.close()
}
function toggleArrows() {
  showArrows.value = !showArrows.value
  if (showArrows.value && arrowBtnEl.value) {
    const r = arrowBtnEl.value.getBoundingClientRect()
    // Anchor above the whole key bar. On shell panes the inline input line
    // sits at the bottom of the scroll area just above this bar — ~two line
    // heights of gap keeps the pad from covering the line being typed.
    const top = rootEl.value?.getBoundingClientRect().top ?? r.top
    const bottom = window.innerHeight - top + 44
    if (isDesktop.value) {
      arrowPos.value = { left: r.left, right: -1, bottom }
    } else {
      arrowPos.value = { left: -1, right: 12, bottom }
    }
  }
}

// --- Ctrl modifier ---
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

// --- approval options (shell panes can also get blocked) ---
interface ApproveOption { payload: string; label: string; full: string; index: number; tone: 'yes' | 'no' | 'neutral'; sticky: boolean }

const isBlocked = computed(() => (props.pane?.status || '').toLowerCase().includes('block'))

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
    const full = labels[i] || payload
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

function toneFor(payload: string, label?: string): 'yes' | 'no' | 'neutral' {
  const s = `${payload} ${label || ''}`.toLowerCase()
  if (/\byes\b|approve|allow|accept|continue|^y\b/.test(s)) return 'yes'
  if (/\bno\b|deny|reject|abort|skip|^n\b/.test(s)) return 'no'
  return 'neutral'
}

function approve(opt: ApproveOption) {
  haptic(18)
  term.respond(opt.payload, opt.index, true)
}

// --- optional input box ---
// The inline prompt input inside the terminal stays the default; this toggle
// reveals a regular input row below the key bar and is persisted per device.
const SHOW_INPUT_KEY = 'herdrem_shell_input'
const showInput = ref(safeStorage.getItem(SHOW_INPUT_KEY) === 'true')
watch(showInput, (v) => {
  safeStorage.setItem(SHOW_INPUT_KEY, String(v))
})

// Shared command buffer — v-model bound to TermView's shellText so the inline
// prompt and this box always show the same draft.
const text = computed({
  get: () => props.modelValue ?? '',
  set: (v) => emit('update:modelValue', v),
})

const boxInputEl = ref<HTMLTextAreaElement | null>(null)

function toggleInput() {
  haptic(8)
  showInput.value = !showInput.value
  if (showInput.value) {
    nextTick(() => {
      wrap.fit() // text kept from last time may need more than one line
      boxInputEl.value?.focus()
    })
  }
}

function submitInput() {
  haptic()
  const t = text.value
  if (!t) return
  term.sendText(t, true)
  history.record(t, { kind: 'sh', ...whereOf(props.pane) })
  text.value = ''
}

// The element the parent should treat as the shell's active text input:
// the box when shown, otherwise null (TermView falls back to the inline one).
defineExpose({
  inputEl: () => (showInput.value ? boxInputEl.value : null),
})

// Enter submits (like the form's own submit); long text wraps; Shift+Enter
// adds a line where the relay can paste multi-line text.
const conn = useConnectionStore()
const wrap = useWrappingInput(boxInputEl, text, () => submitInput(), () => conn.hasFeature('send-text-paste'))

function onBoxKeydown(e: KeyboardEvent) {
  wrap.onKeydown(e)
  emit('inputKeydown', e)
}

// --- special keys ---
const { visibleItems: keyRowItems } = useKeyRowConfig('shell')
const showConfig = ref(false)

function openConfig() {
  qp.close()
  showArrows.value = false
  showConfig.value = true
}

function sendKey(key: string) {
  haptic(8)
  term.sendKey(consumeCtrl(key))
}

function pressSpecial(k: { key: string; repeat?: boolean }) {
  if (k.repeat) start(() => sendKey(k.key))
  else sendKey(k.key)
}

function onArrowKey(key: string) {
  term.sendKey(consumeCtrl(key))
}
</script>

<template>
  <div ref="rootEl" class="term-input">
    <div v-if="isBlocked" class="approve-bar" role="group" aria-label="Approve">
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
      <template v-for="item in keyRowItems" :key="item.id">
        <button
          v-if="item.id === 'input'"
          type="button"
          class="key input-toggle"
          :class="{ active: showInput }"
          aria-label="Toggle input box"
          :aria-pressed="showInput"
          title="Input box"
          @click="toggleInput"
          @pointerdown.prevent
        >❯</button>
        <div v-else-if="item.id === 'shellCmds'" class="qp-wrap" @mouseleave="qp.hoverClose()">
          <button
            :ref="(el) => { if (el) qpBtnCmdsEl = el as HTMLElement }"
            type="button"
            class="key qp-btn cmds"
            :class="{ on: qp.open.value === 'shellCmds' }"
            :disabled="!qp.shellCmdItems.value.length"
            aria-label="Shell commands"
            title="Shell commands"
            @click.stop="openCmds"
            @pointerdown.prevent
            @mouseenter="hoverCmds"
          >▶</button>
        </div>
        <button
          v-else-if="item.id === 'arrows'"
          :ref="(el) => { if (el) arrowBtnEl = el as HTMLElement }"
          type="button"
          class="key arrows"
          :class="{ active: showArrows }"
          aria-label="Arrow pad"
          title="Arrow keys pad"
          @click="toggleArrows"
          @pointerdown.prevent
        >⌖</button>
        <button
          v-else-if="item.id === 'ctrl'"
          type="button"
          class="key ctrl"
          :class="{ active: ctrlActive }"
          aria-label="Ctrl modifier"
          :aria-pressed="ctrlActive"
          title="Ctrl modifier (next key sent as Ctrl+key)"
          @click="toggleCtrl"
        >ctrl</button>
        <button
          v-else-if="item.key"
          type="button"
          class="key"
          :class="{ danger: item.danger }"
          :aria-label="item.key"
          :title="item.key"
          @pointerdown="pressSpecial(item as { key: string; repeat?: boolean })"
          @pointerup="stop"
          @pointerleave="stop"
        >{{ item.label }}</button>
      </template>
      <button
        type="button"
        class="key config-btn"
        aria-label="Customize keys"
        title="Customize shortcut keys"
        @click="openConfig"
        @pointerdown.prevent
      >⚙</button>
    </div>

    <Teleport to="body">
      <KeyRowConfigModal
        v-if="showConfig"
        type="shell"
        @close="showConfig = false"
      />
    </Teleport>

    <form v-if="showInput" class="input-row" @submit.prevent="submitInput">
      <span class="prompt" aria-hidden="true">❯</span>
      <textarea
        ref="boxInputEl"
        v-model="text"
        class="cmd"
        rows="1"
        placeholder="send to shell…"
        autocomplete="off"
        autocapitalize="off"
        autocorrect="off"
        spellcheck="false"
        enterkeyhint="send"
        aria-label="Command to send to shell"
        @beforeinput="wrap.onBeforeInput"
        @input="wrap.onInput"
        @keydown="onBoxKeydown"
        @focus="emit('inputFocus', true)"
        @blur="emit('inputFocus', false)"
      ></textarea>
      <button type="submit" class="send-btn" :disabled="!text.length" aria-label="Send command to shell">send</button>
    </form>

    <Teleport to="body">
      <div v-if="qp.open.value === 'shellCmds'" class="qp-pop" :style="{ left: qp.pos.value.left + 'px', bottom: qp.pos.value.bottom + 'px' }" @click.stop @mouseenter="qp.cancelClose()" @mouseleave="qp.hoverClose()">
        <button
          v-for="(c, i) in qp.shellCmdItems.value"
          :key="'sc' + i"
          type="button"
          class="qp-item"
          :title="c"
          @click="qpFill(c)"
        >{{ c.length > 30 ? c.slice(0, 30) + '…' : c }}</button>
        <button type="button" class="qp-edit" @click="qpEdit">✎ edit commands</button>
      </div>
    </Teleport>

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
  background: var(--surface);
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
.key.ctrl.active, .key.arrows.active, .key.input-toggle.active {
  color: var(--blue);
  background: var(--blue-soft);
  border-color: rgba(96, 165, 250, 0.5);
}
.key.config-btn {
  color: var(--muted);
  padding: 5px 8px;
}
.key.config-btn:hover {
  color: var(--text);
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
  /* a textarea that wraps long text and grows up to six lines */
  display: block;
  margin: 0;
  padding: 0;
  resize: none;
  line-height: 1.45;
  max-height: calc(1.45em * 6);
  overflow-y: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
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

/* Quick popover buttons inline in key-row */
.qp-wrap { position: relative; flex-shrink: 0; }
.qp-btn { height: 28px; display: inline-grid; place-items: center; line-height: 1; }
.qp-btn:active { transform: none; border-bottom-width: 2px; }
.qp-btn.cmds.on { color: var(--blue); border-color: rgba(96,165,250,.5); background: var(--blue-soft); }
</style>

<style>
/* Teleported popover — global, position: fixed to escape overflow
   (shared with TermInput.vue, defined once here for shell mode) */
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
