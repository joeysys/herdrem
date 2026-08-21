<script setup lang="ts">
import { useKeyRepeat } from '../../composables/useKeyRepeat'
import { useHaptic } from '../../composables/useHaptic'

const emit = defineEmits<{ key: [key: string] }>()
const { start, stop } = useKeyRepeat()
const { haptic } = useHaptic()

const REPEATABLE = new Set(['Up', 'Down', 'Left', 'Right'])

function press(key: string) {
  haptic(8)
  emit('key', key)
}

function hold(key: string) {
  if (REPEATABLE.has(key)) start(() => press(key))
  else press(key)
}
</script>

<template>
  <div class="arrow-pad" role="group" aria-label="Arrow keys">
    <button class="pad up" aria-label="Up"
      @pointerdown="hold('Up')" @pointerup="stop" @pointerleave="stop">▲</button>
    <button class="pad left" aria-label="Left"
      @pointerdown="hold('Left')" @pointerup="stop" @pointerleave="stop">◀</button>
    <button class="pad center" aria-label="Enter" @click="press('Enter')">⏎</button>
    <button class="pad right" aria-label="Right"
      @pointerdown="hold('Right')" @pointerup="stop" @pointerleave="stop">▶</button>
    <button class="pad down" aria-label="Down"
      @pointerdown="hold('Down')" @pointerup="stop" @pointerleave="stop">▼</button>
  </div>
</template>

<style scoped>
.arrow-pad {
  display: grid;
  grid-template-columns: repeat(3, 52px);
  grid-template-rows: repeat(3, 52px);
  gap: 6px;
  padding: 10px;
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  touch-action: none;
}
.pad {
  font-size: var(--text-lg);
  color: var(--text-dim);
  background: var(--surface-3);
  border: 1px solid var(--border);
  border-bottom-width: 2px;
  border-radius: var(--radius);
  display: grid;
  place-items: center;
  user-select: none;
  transition: transform 0.08s, background 0.15s, color 0.15s;
}
.pad:active { transform: translateY(1px); background: var(--blue-soft); color: var(--blue); }
.up { grid-column: 2; grid-row: 1; }
.left { grid-column: 1; grid-row: 2; }
.center { grid-column: 2; grid-row: 2; }
.right { grid-column: 3; grid-row: 2; }
.down { grid-column: 2; grid-row: 3; }
</style>
