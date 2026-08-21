<script setup lang="ts">
import { computed, ref } from 'vue'
import { useTerminalStore } from '../../stores/terminal'
import { useHaptic } from '../../composables/useHaptic'
import { useKeyRepeat } from '../../composables/useKeyRepeat'
import { useQuickPopover } from '../../composables/useQuickPopover'
import {
  detectMenu, isStickyChoice, menuLabel,
} from '../../utils/menu'
import type { Pane } from '../../api/types'
import ArrowPad from './ArrowPad.vue'

const props = defineProps<{ pane: Pane | null }>()
const emit = defineEmits<{ fill: [text: string]; edit: [] }>()

const term = useTerminalStore()
const { haptic } = useHaptic()
const { start, stop } = useKeyRepeat()

const showArrows = ref(false)
const arrowBtnEl = ref<HTMLElement | null>(null)
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
    const bottom = window.innerHeight - r.top + 8
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
  term.respond(opt.payload, opt.index)
}

// --- special keys ---
const SHELL_KEYS: Array<{ label: string; key: string; danger?: boolean; repeat?: boolean }> = [
  { label: 'Esc', key: 'Escape' },
  { label: 'Tab', key: 'Tab' },
  { label: '^C', key: 'Ctrl+c', danger: true, repeat: true },
  { label: '^D', key: 'Ctrl+d' },
  { label: '^Z', key: 'Ctrl+z' },
  { label: '^L', key: 'Ctrl+l' },
  { label: '^R', key: 'Ctrl+r' },
  { label: '^A', key: 'Ctrl+a' },
  { label: '^E', key: 'Ctrl+e' },
  { label: '^W', key: 'Ctrl+w' },
]
const SPECIAL_KEYS = computed(() => SHELL_KEYS)

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
  <div class="term-input">
    <div v-if="isBlocked" class="approve-bar" role="group" aria-label="Approve">
      <span class="approve-label">waiting</span>
      <button
        v-for="opt in approveOptions"
        :key="opt.payload + opt.index"
        class="appr-btn"
        :class="[opt.tone, { sticky: opt.sticky }]"
        :title="opt.full"
        @click="approve(opt)"
      >{{ opt.label }}</button>
    </div>

    <div class="key-row">
      <div class="qp-wrap" @mouseleave="qp.hoverClose()">
        <button
          ref="qpBtnCmdsEl"
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
        ref="arrowBtnEl"
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
        class="key"
        :class="{ danger: k.danger }"
        :title="k.key"
        @pointerdown="pressSpecial(k)"
        @pointerup="stop"
        @pointerleave="stop"
      >{{ k.label }}</button>
      <button
        class="key ctrl"
        :class="{ active: ctrlActive }"
        :aria-pressed="ctrlActive"
        title="Ctrl modifier (next key sent as Ctrl+key)"
        @click="toggleCtrl"
      >ctrl</button>
    </div>

    <Teleport to="body">
      <div v-if="qp.open.value === 'shellCmds'" class="qp-pop" :style="{ left: qp.pos.value.left + 'px', bottom: qp.pos.value.bottom + 'px' }" @click.stop @mouseenter="qp.cancelClose()" @mouseleave="qp.hoverClose()">
        <button
          v-for="(c, i) in qp.shellCmdItems.value"
          :key="'sc' + i"
          class="qp-item"
          :title="c"
          @click="qpFill(c)"
        >{{ c.length > 30 ? c.slice(0, 30) + '…' : c }}</button>
        <button class="qp-edit" @click="qpEdit">✎ edit commands</button>
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
.key.ctrl.active, .key.arrows.active {
  color: var(--blue);
  background: var(--blue-soft);
  border-color: rgba(96, 165, 250, 0.5);
}

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
