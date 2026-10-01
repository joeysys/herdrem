<script setup lang="ts">
import type { Pane } from '../../api/types'
import { paneKind } from '../../stores/connection'

const props = defineProps<{ pane: Pane; open: boolean; pos?: { x: number; y: number } }>()
const emit = defineEmits<{
  close: []
  action: [action: string, pane: Pane]
}>()

const isShell = () => paneKind(props.pane) === 'shell'

const ACTIONS = [
  { id: 'open', label: 'open terminal', icon: '❯' },
  { id: 'refresh', label: 'read output', icon: '⟳' },
  { id: 'diff', label: 'git diff', icon: '⎇' },
  { id: 'status', label: 'git status', icon: '±' },
  { id: 'interrupt', label: 'interrupt (^C)', icon: '⊘' },
  { id: 'mark-read', label: 'mark as read', icon: '✓' },
  { id: 'mark-agent', label: 'mark as agent', icon: '★', shellOnly: true },
]

function pick(id: string) {
  emit('action', id, props.pane)
  emit('close')
}

// Position menu near click, clamped to viewport
function menuStyle() {
  const x = props.pos?.x ?? window.innerWidth - 210
  const y = props.pos?.y ?? 60
  const menuW = 200
  const menuH = 240 // approx
  const left = Math.min(x, window.innerWidth - menuW - 8)
  const top = Math.min(y, window.innerHeight - menuH - 8)
  return { left: `${left}px`, top: `${top}px` }
}
</script>

<template>
  <div v-if="open" class="ctx-backdrop" @click="emit('close')">
    <div class="ctx-menu" :style="menuStyle()" role="menu" @click.stop>
      <button
        v-for="a in ACTIONS.filter((x) => !x.shellOnly || isShell())"
        :key="a.id"
        class="ctx-item"
        role="menuitem"
        @click="pick(a.id)"
      >
        <span class="ctx-icon" aria-hidden="true">{{ a.icon }}</span>{{ a.label }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.ctx-backdrop {
  position: fixed;
  inset: 0;
  z-index: 70;
  background: rgba(4, 6, 10, 0.4);
  animation: view-in 0.12s ease both;
}
.ctx-menu {
  position: fixed;
  min-width: 190px;
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  padding: 5px;
  animation: view-in 0.14s ease both;
}
.ctx-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  text-align: left;
  font-family: var(--font-display);
  font-size: var(--text-sm);
  color: var(--text-dim);
  background: transparent;
  border: none;
  border-radius: var(--radius);
  padding: 9px 11px;
  transition: background 0.12s, color 0.12s;
}
.ctx-item:hover { background: var(--surface-3); color: var(--text); }
.ctx-icon { width: 16px; text-align: center; color: var(--muted); }
</style>
