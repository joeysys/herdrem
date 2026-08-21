<script setup lang="ts">
import { computed, ref } from 'vue'
import { displayAgent, paneKind } from '../../stores/connection'
import type { Pane } from '../../api/types'

const props = withDefaults(defineProps<{ pane: Pane; size?: number }>(), { size: 22 })

const ICON_MAP: Array<[RegExp, string]> = [
  [/kiro/, 'kiro'],
  [/codex|openai/, 'codex'],
  [/claude|anthropic/, 'claude'],
  [/grok|xai/, 'grok'],
  [/cursor/, 'cursor'],
  [/devin/, 'devin'],
  [/gemini|google/, 'gemini'],
  [/copilot|github/, 'copilot'],
  [/opencode/, 'opencode'],
  [/agy|aider/, 'agy'],
  [/pi|earendil/, 'pi'],
]

const iconKey = computed(() => {
  if (paneKind(props.pane) === 'shell') return 'shell'
  const name = displayAgent(props.pane).toLowerCase()
  for (const [re, key] of ICON_MAP) {
    if (re.test(name)) return key
  }
  return 'default'
})

const iconUrl = computed(() => `${import.meta.env.BASE_URL}icons/agents/${iconKey.value}.svg`)
const fallbackText = computed(() => displayAgent(props.pane).slice(0, 2).toUpperCase() || '?')
const loadFailed = ref(false)
</script>

<template>
  <span class="agent-icon" :style="{ width: size + 'px', height: size + 'px' }" aria-hidden="true">
    <img v-if="!loadFailed" :src="iconUrl" alt="" :width="size" :height="size" @error="loadFailed = true" />
    <span v-else class="fallback">{{ fallbackText }}</span>
  </span>
</template>

<style scoped>
.agent-icon {
  display: inline-grid;
  place-items: center;
  flex-shrink: 0;
  border-radius: 6px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  overflow: hidden;
}
.agent-icon img { display: block; width: 100%; height: 100%; object-fit: contain; }
.agent-icon .fallback {
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.55em;
  color: var(--muted);
}
</style>
