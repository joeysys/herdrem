<script setup lang="ts">
// Command suggestions for the shell input, as tappable chips. Floats over the
// terminal (the parent positions it) so appearing/disappearing while typing
// never changes the layout.
defineProps<{
  items: string[]
  /** what has been typed so far; the matching prefix is dimmed */
  typed: string
  /** index of the keyboard-highlighted item */
  active: number
}>()
const emit = defineEmits<{ pick: [cmd: string] }>()

// Dim the part the user already typed so the eye lands on the completion.
function split(cmd: string, typed: string): [string, string] {
  return cmd.startsWith(typed) ? [typed, cmd.slice(typed.length)] : ['', cmd]
}
</script>

<template>
  <ul v-if="items.length" class="suggest" role="listbox" aria-label="Command suggestions">
    <li v-for="(cmd, i) in items" :key="cmd" role="presentation">
      <!-- pointerdown.prevent keeps focus in the input so the soft keyboard stays up -->
      <button
        type="button"
        class="chip"
        :class="{ active: i === active }"
        role="option"
        :aria-selected="i === active"
        :title="cmd"
        @pointerdown.prevent
        @click="emit('pick', cmd)"
      ><span class="typed">{{ split(cmd, typed)[0] }}</span><span class="rest">{{ split(cmd, typed)[1] }}</span></button>
    </li>
  </ul>
</template>

<style scoped>
.suggest {
  display: flex;
  gap: 6px;
  margin: 0;
  padding: 6px 8px;
  list-style: none;
  overflow-x: auto;
  scrollbar-width: none;
  background: color-mix(in srgb, var(--surface) 92%, transparent);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.25);
}
.suggest::-webkit-scrollbar { display: none; }
.chip {
  max-width: 70vw;
  flex-shrink: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--text);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 4px 11px;
}
.chip:hover { border-color: var(--border-strong); }
.chip.active { border-color: var(--blue); background: var(--blue-soft); }
.chip .typed { color: var(--muted); }
</style>
