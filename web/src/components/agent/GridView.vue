<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from 'vue'
import { bucket, paneKind, paneTitle, shellLabel } from '../../stores/connection'
import { usePreviewsStore } from '../../stores/preview'
import { useHaptic } from '../../composables/useHaptic'
import { menuLabel, isStickyChoice } from '../../utils/menu'
import { ws } from '../../api/ws'
import type { Pane } from '../../api/types'
import AgentIcon from '../agent/AgentIcon.vue'

const props = defineProps<{ agents: Pane[]; justDone?: Set<string> }>()
const emit = defineEmits<{ openTerminal: [paneId: string] }>()

const previews = usePreviewsStore()
const { haptic } = useHaptic()

const STATUS_ORDER = ['blocked', 'working', 'idle', 'done', 'shell']

const sorted = computed<Pane[]>(() => {
  return [...props.agents].sort((a, b) => {
    const ka = paneKind(a) === 'shell' ? 'shell' : bucket(a.status)
    const kb = paneKind(b) === 'shell' ? 'shell' : bucket(b.status)
    return STATUS_ORDER.indexOf(ka) - STATUS_ORDER.indexOf(kb)
  })
})

// Visibility-aware preview refresh: only cards currently in the viewport get
// preview reads, and the interval stops entirely while the page is hidden.
// Cards scrolling into view fetch once via the observer; the interval then
// keeps them fresh.
const visible = new Set<string>()
const cardEls = new Map<string, Element>()
let observer: IntersectionObserver | null = null
let timer: ReturnType<typeof setInterval> | null = null

function trackCard(el: Element | null, paneId: string) {
  if (!el) {
    const old = cardEls.get(paneId)
    if (old) observer?.unobserve(old)
    cardEls.delete(paneId)
    visible.delete(paneId)
    return
  }
  cardEls.set(paneId, el)
  observer?.observe(el)
}

function refresh() {
  for (const p of props.agents) {
    if (visible.has(p.pane_id)) previews.requestPreview(p.pane_id)
  }
}

function startTimer() {
  if (!timer) timer = setInterval(refresh, 8000)
}

function stopTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

function onVisibility() {
  if (document.hidden) {
    stopTimer()
  } else {
    refresh()
    startTimer()
  }
}

onMounted(() => {
  observer = new IntersectionObserver((entries) => {
    for (const e of entries) {
      const id = (e.target as HTMLElement).dataset.paneId
      if (!id) continue
      if (e.isIntersecting) {
        // Newly visible (also the initial observation): fetch immediately so
        // the card does not wait for the next interval tick.
        if (!visible.has(id)) previews.requestPreview(id)
        visible.add(id)
      } else {
        visible.delete(id)
      }
    }
  })
  for (const el of cardEls.values()) observer.observe(el)
  if (!document.hidden) startTimer()
  document.addEventListener('visibilitychange', onVisibility)
})

onUnmounted(() => {
  stopTimer()
  observer?.disconnect()
  observer = null
  document.removeEventListener('visibilitychange', onVisibility)
})
watch(() => props.agents.length, refresh)

function previewText(p: Pane): string {
  if (previews.hasPreview(p.pane_id)) {
    return previews.previews.get(p.pane_id) || ''
  }
  return previews.isPending(p.pane_id) ? 'loading…' : 'no output yet'
}

function approve(p: Pane, payload: string) {
  haptic(18)
  ws.send({ type: 'respond', pane_id: p.pane_id, text: payload })
}

function tone(payload: string, label?: string): 'yes' | 'no' | 'neutral' {
  const s = `${payload} ${label || ''}`.toLowerCase()
  if (/\byes\b|approve|allow|accept|^y\b/.test(s)) return 'yes'
  if (/\bno\b|deny|reject|skip|^n\b/.test(s)) return 'no'
  return 'neutral'
}
</script>

<template>
  <div class="grid-view view-enter">
    <article
      v-for="p in sorted"
      :key="p.pane_id"
      :ref="(el) => trackCard(el as Element | null, p.pane_id)"
      :data-pane-id="p.pane_id"
      class="g-card"
      :class="[bucket(p.status), { shell: paneKind(p) === 'shell', 'just-done': justDone?.has(p.pane_id) }]"
      role="button"
      tabindex="0"
      @click="emit('openTerminal', p.pane_id)"
      @keydown.enter="emit('openTerminal', p.pane_id)"
    >
      <header class="g-head">
        <AgentIcon :pane="p" :size="20" />
        <span class="g-name">{{ paneTitle(p) }}</span>
        <span v-if="p.branch" class="g-branch mono">⎇ {{ p.branch }}</span>
        <span v-if="paneKind(p) === 'shell' ? shellLabel(p) : true" class="g-badge mono">{{ paneKind(p) === 'shell' ? shellLabel(p) : (p.status || 'idle') }}</span>
      </header>

      <pre class="g-preview" :class="{ empty: !previews.hasPreview(p.pane_id) }">{{ previewText(p) }}</pre>

      <footer v-if="bucket(p.status) === 'blocked'" class="g-actions" @click.stop>
        <template v-if="p.options && p.options.length">
          <button
            v-for="(opt, i) in p.options.slice(0, 3)"
            :key="opt"
            class="g-btn"
            :class="[tone(opt, p.option_labels?.[i]), { sticky: isStickyChoice(p.option_labels?.[i] || '') }]"
            @click="approve(p, opt)"
          >{{ menuLabel(p.option_labels?.[i] || opt) }}</button>
        </template>
        <template v-else>
          <button class="g-btn yes" @click="approve(p, 'y')">y</button>
          <button class="g-btn no" @click="approve(p, 'n')">n</button>
        </template>
      </footer>
    </article>

    <div v-if="sorted.length === 0" class="g-empty">No agents match.</div>
  </div>
</template>

<style scoped>
.mono { font-family: var(--font-mono); }
.grid-view {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
  gap: 11px;
}
.g-card {
  position: relative; overflow: hidden;
  display: flex; flex-direction: column;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  cursor: pointer;
  transition: transform 0.15s, border-color 0.15s, background 0.15s, box-shadow 0.15s;
}
.g-card:hover { transform: translateY(-2px); border-color: var(--border-strong); background: var(--surface-2); box-shadow: var(--shadow-2); }
.g-card::before { content: ''; position: absolute; left: 0; top: 0; bottom: 0; width: 3px; background: var(--faint); }
.g-card.blocked { border-color: rgba(248,113,113,.4); }
.g-card.blocked::before { background: var(--red); }
.g-card.working::before { background: var(--orange); }
.g-card.done::before { background: var(--green); }
.g-card.shell::before { background: var(--muted); }

.g-head { display: flex; align-items: center; gap: 9px; padding: 11px 12px 8px; }
.g-name { flex: 1; min-width: 0; font-weight: 600; font-size: var(--text-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.g-branch { font-size: var(--text-2xs); color: var(--blue); flex-shrink: 0; }
.g-badge { font-size: var(--text-2xs); text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); flex-shrink: 0; }
.g-card.blocked .g-badge { color: var(--red); }
.g-card.working .g-badge { color: var(--orange); }
.g-card.done .g-badge { color: var(--green); }

.g-preview {
  flex: 1; min-height: 76px; max-height: 130px; overflow: hidden;
  margin: 0 12px; padding: 8px 10px;
  font-family: var(--font-mono); font-size: 11px; line-height: 1.5;
  color: var(--term-fg); background: var(--term-bg);
  border: 1px solid var(--border); border-radius: var(--radius);
  white-space: pre-wrap; word-break: break-word;
}
.g-preview.empty { color: var(--faint); font-style: italic; }

.g-actions { display: flex; gap: 6px; padding: 9px 12px 11px; flex-wrap: wrap; }
.g-btn {
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  padding: 5px 12px; border-radius: var(--radius); border: 1px solid;
  transition: transform 0.1s, filter 0.15s;
}
.g-btn:active { transform: scale(0.94); }
.g-btn.yes { color: #06281a; background: var(--green); border-color: var(--green); }
.g-btn.no { color: var(--red); background: transparent; border-color: rgba(248,113,113,.5); }
.g-btn.neutral { color: var(--text-dim); background: var(--surface-2); border-color: var(--border-strong); }
.g-btn.sticky { box-shadow: 0 0 0 2px rgba(251,191,36,.3); border-style: dashed; }

.g-card.just-done { animation: done-flash 1.5s ease both; }
@keyframes done-flash {
  0%, 100% { box-shadow: none; }
  25%, 60% { box-shadow: 0 0 0 2px rgba(52,211,153,.7), 0 0 18px rgba(52,211,153,.4); }
}
.g-empty { grid-column: 1 / -1; text-align: center; color: var(--faint); padding: 40px; }
</style>
