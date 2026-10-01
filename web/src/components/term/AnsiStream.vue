<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ansiToHtmlWith } from '../../utils/ansi'
import { linkifyFilePaths } from '../../utils/linkify'
import { truncateSeparators } from '../../utils/truncLines'
import { applyHighlights, clearHighlights, setActiveGroup } from '../../utils/domHighlight'

// `highlight` wraps matches of a search query in <mark>; `hits` reports how
// many there are (capped at domHighlight's MAX_HITS) and setActiveHit() moves the emphasis.
// `zoom` only signals a font-size change, so the character width (used to fit
// separator lines to the screen) is measured again.
const props = defineProps<{ raw: string; validPaths?: string[]; highlight?: string; zoom?: number }>()
const emit = defineEmits<{
  openFile: [payload: { path: string; line: number | null }]
  hits: [count: number]
}>()

const el = ref<HTMLElement | null>(null)
const charWidth = ref(0)
const containerWidth = ref(0)

function measureCharWidth() {
  if (!el.value || typeof getComputedStyle === 'undefined') return
  try {
    const span = document.createElement('span')
    const cs = getComputedStyle(el.value)
    span.style.fontFamily = cs.fontFamily
    span.style.fontSize = cs.fontSize
    span.style.visibility = 'hidden'
    span.style.position = 'absolute'
    span.textContent = 'M'.repeat(50)
    el.value.appendChild(span)
    const rect = span.getBoundingClientRect()
    charWidth.value = rect.width / 50
    el.value.removeChild(span)
  } catch { /* noop in test env */ }
}

function measureContainer() {
  if (!el.value) return
  containerWidth.value = el.value.clientWidth
}

let resizeObserver: ResizeObserver | null = null

onMounted(() => {
  // Defer to a microtask: the parent applies --term-zoom in its own mounted
  // hook, which runs after this child's — measuring now would read the 1x
  // font size when a saved zoom factor is restored.
  nextTick(measureCharWidth)
  measureContainer()
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => {
      measureContainer()
    })
    if (el.value) resizeObserver.observe(el.value)
  }
})

onUnmounted(() => {
  resizeObserver?.disconnect()
})

// Re-measure char width when content changes (font might not be loaded yet)
watch(() => props.raw, () => {
  if (charWidth.value === 0) measureCharWidth()
})
// …and after a zoom, once the new font size has been applied.
watch(() => props.zoom, () => nextTick(measureCharWidth))

const maxChars = computed(() => {
  if (charWidth.value === 0 || containerWidth.value === 0) return 9999
  // Account for padding (14px left + 16px right from term-scroll)
  return Math.floor((containerWidth.value - 30) / charWidth.value)
})

const truncatedRaw = computed(() => {
  if (maxChars.value >= 9999) return props.raw
  return truncateSeparators(props.raw, maxChars.value)
})

const html = computed(() => {
  const validSet = props.validPaths ? new Set(props.validPaths) : null
  return ansiToHtmlWith(truncatedRaw.value, (s) => linkifyFilePaths(s, validSet))
})

// --- search highlighting ---
// Marks are injected into the v-html output after each render. Vue rewrites
// that subtree whenever `html` changes, so highlights are re-applied (post
// flush) on every html / query change.
const htmlEl = ref<HTMLElement | null>(null)
let groups: HTMLElement[][] = []
let activeIdx = -1

function refreshHighlights() {
  const root = htmlEl.value
  if (!root) return
  const had = groups.length > 0
  if (had) clearHighlights(root)
  groups = props.highlight ? applyHighlights(root, props.highlight) : []
  if (activeIdx >= groups.length) activeIdx = groups.length - 1
  if (groups.length && activeIdx < 0) activeIdx = 0
  setActiveGroup(groups, activeIdx)
  if (props.highlight || had) emit('hits', groups.length)
}

watch([html, () => props.highlight], refreshHighlights, { flush: 'post' })
onMounted(() => { if (props.highlight) refreshHighlights() })

/** Emphasise hit `index` (0-based); `scroll` brings it into view. */
function setActiveHit(index: number, scroll = true) {
  if (!groups.length) return
  activeIdx = Math.min(Math.max(index, 0), groups.length - 1)
  const mark = setActiveGroup(groups, activeIdx)
  if (scroll) mark?.scrollIntoView?.({ block: 'center', inline: 'nearest' })
}

defineExpose({ setActiveHit })

function onClick(e: MouseEvent) {
  const link = (e.target as HTMLElement).closest('a.term-file-link') as HTMLAnchorElement | null
  if (!link) return
  e.preventDefault()
  const fp = link.getAttribute('data-fp')
  if (!fp) return
  const lineAttr = link.getAttribute('data-line')
  emit('openFile', {
    path: decodeURIComponent(fp),
    line: lineAttr ? parseInt(lineAttr, 10) : null,
  })
}
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -->
  <pre ref="el" class="ansi-stream" @click="onClick"><span ref="htmlEl" v-html="html"></span><slot></slot></pre>
</template>

<style>
/* not scoped: v-html content + link styling must reach injected nodes */
.ansi-stream {
  font-family: var(--font-mono);
  font-size: inherit;
  line-height: 1.6;
  color: var(--term-fg);
  white-space: pre;
  overflow-x: auto;
  margin: 0;
}
/* Spans carrying a background: stretch the paint over the half-leading so
   consecutive background lines (tmux status bars, htop, fzf…) render as a
   solid block instead of showing gaps at line-height 1.6.
   Vertical padding on inline boxes only extends the painted background — it
   does not change line-box layout, and since all inline backgrounds paint
   before all text, the slight bleed can never cover neighboring glyphs.
   Keep in sync with line-height above: padding ≈ (line-height - 1) / 2. */
.ansi-stream .has-bg {
  padding-top: 0.3em;
  padding-bottom: 0.3em;
}
.ansi-stream a.term-file-link {
  color: var(--blue);
  text-decoration: underline;
  text-decoration-style: dotted;
  text-underline-offset: 3px;
  cursor: pointer;
  border-radius: 3px;
  padding: 0 1px;
  transition: background 0.15s, color 0.15s;
}
.ansi-stream a.term-file-link:hover {
  background: var(--blue-soft);
  text-decoration-style: solid;
}
</style>
