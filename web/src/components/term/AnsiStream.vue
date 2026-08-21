<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ansiToHtmlWith } from '../../utils/ansi'
import { linkifyFilePaths } from '../../utils/linkify'
import { truncateSeparators } from '../../utils/truncLines'

const props = defineProps<{ raw: string; validPaths?: string[] }>()
const emit = defineEmits<{ openFile: [payload: { path: string; line: number | null }] }>()

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
  measureCharWidth()
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
  <pre ref="el" class="ansi-stream" @click="onClick"><span v-html="html"></span><slot></slot></pre>
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
