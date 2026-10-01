<script setup lang="ts">
import { computed, ref } from 'vue'

// Floating find bar (terminal output, file preview). Purely presentational:
// the parent owns the query and the hit navigation.
const props = defineProps<{
  modelValue: string
  /** 1-based index of the active hit (0 when there is none) */
  current: number
  total: number
  /** true when the hit list was cut at the highlight cap */
  capped?: boolean
  /** what is searched — used for the accessible names, e.g. "output", "file" */
  label?: string
  /** caveat shown as a ⚠ badge, e.g. the searched text is only partial */
  note?: string
}>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  next: []
  prev: []
  close: []
}>()

const inputEl = ref<HTMLInputElement | null>(null)
const what = computed(() => props.label || 'output')

const query = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const countLabel = computed(() => {
  if (!props.modelValue) return ''
  if (!props.total) return 'no match'
  return `${props.current}/${props.total}${props.capped ? '+' : ''}`
})

defineExpose({ focus: () => { inputEl.value?.focus(); inputEl.value?.select() } })
</script>

<template>
  <div class="find-bar" role="search" :aria-label="`Search ${what}`" @click.stop>
    <input
      ref="inputEl"
      v-model="query"
      class="q"
      type="search"
      placeholder="search…"
      autocomplete="off"
      autocapitalize="off"
      autocorrect="off"
      spellcheck="false"
      enterkeyhint="search"
      :aria-label="`Search ${what}`"
      @keydown.enter.exact.prevent="emit('next')"
      @keydown.enter.shift.prevent="emit('prev')"
      @keydown.esc.prevent="emit('close')"
    />
    <span v-if="note" class="note" :title="note" role="img" :aria-label="note">⚠</span>
    <span class="count" role="status" aria-live="polite">{{ countLabel }}</span>
    <button type="button" class="nav" aria-label="Previous match" title="Previous (Shift+Enter)" :disabled="!total" @click="emit('prev')">▲</button>
    <button type="button" class="nav" aria-label="Next match" title="Next (Enter)" :disabled="!total" @click="emit('next')">▼</button>
    <button type="button" class="nav close" aria-label="Close search" title="Close (Esc)" @click="emit('close')">×</button>
  </div>
</template>

<style scoped>
.find-bar {
  position: absolute;
  top: 8px;
  right: 12px;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 4px;
  max-width: calc(100% - 24px);
  padding: 4px 6px;
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.3);
}
.q {
  width: 180px;
  min-width: 0;
  flex: 1;
  background: transparent;
  border: none;
  outline: none;
  color: var(--text);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  padding: 4px 6px;
}
.q::-webkit-search-cancel-button { -webkit-appearance: none; appearance: none; }
.q::placeholder { color: var(--faint); }
.note { color: var(--orange); font-size: var(--text-xs); cursor: help; }
.count {
  min-width: 3.5em;
  text-align: right;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.nav {
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  font-size: 11px;
  line-height: 1;
  color: var(--muted);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.nav:hover:not(:disabled) { color: var(--text); background: var(--surface-3); }
.nav:disabled { opacity: 0.4; }
.nav.close { font-size: 15px; }
</style>
