<script setup lang="ts">
import { computed } from 'vue'
import { listShortcuts, type ShortcutScope } from '../../composables/useShortcuts'

const emit = defineEmits<{ close: [] }>()

const groups: { scope: ShortcutScope; title: string }[] = [
  { scope: 'global', title: 'Global' },
  { scope: 'terminal', title: 'Terminal' },
  { scope: 'input', title: 'Input' },
  { scope: 'overlay', title: 'Overlay' },
]

const listed = computed(() => listShortcuts())

function rows(scope: ShortcutScope) {
  return listed.value.filter((s) => s.def.scope === scope && !s.unavailable)
}

const unavailable = computed(() => listed.value.filter((s) => s.unavailable))
</script>

<template>
  <div class="shortcut-help-backdrop" @click.self="emit('close')">
    <section class="shortcut-help" role="dialog" aria-modal="true" aria-labelledby="shortcut-help-title">
      <header class="shortcut-help-head">
        <h2 id="shortcut-help-title">Shortcuts</h2>
        <button type="button" class="shortcut-help-close" aria-label="Close shortcuts" @click="emit('close')">×</button>
      </header>
      <div class="shortcut-help-body">
        <section v-for="g in groups" :key="g.scope">
          <template v-if="rows(g.scope).length">
            <h3>{{ g.title }}</h3>
            <ul>
              <li v-for="s in rows(g.scope)" :key="s.def.id">
                <kbd>{{ s.chord }}</kbd>
                <span>{{ s.def.label }}</span>
              </li>
            </ul>
          </template>
        </section>
        <section v-if="unavailable.length">
          <h3>Unavailable here</h3>
          <ul>
            <li v-for="s in unavailable" :key="s.def.id">
              <kbd>{{ s.chord }}</kbd>
              <span>{{ s.def.label }}。{{ s.unavailable }}</span>
            </li>
          </ul>
        </section>
      </div>
    </section>
  </div>
</template>

<style scoped>
.shortcut-help-backdrop {
  position: fixed;
  inset: 0;
  z-index: 80;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 10vh 16px 16px;
}
.shortcut-help {
  width: min(440px, 100%);
  max-height: min(70vh, 640px);
  overflow: auto;
  background: var(--surface);
  color: var(--text);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
}
.shortcut-help-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 14px;
  border-bottom: 1px solid var(--border);
}
.shortcut-help-head h2 { margin: 0; font-size: var(--text-md); }
.shortcut-help-close {
  border: none;
  background: transparent;
  color: var(--muted);
  font-size: 20px;
  cursor: pointer;
}
.shortcut-help-body { padding: 8px 14px 16px; }
.shortcut-help-body h3 {
  margin: 12px 0 6px;
  font-size: var(--text-xs);
  color: var(--muted);
  font-weight: 600;
}
.shortcut-help-body ul { list-style: none; margin: 0; padding: 0; }
.shortcut-help-body li {
  display: flex;
  gap: 10px;
  align-items: baseline;
  padding: 4px 0;
  font-size: var(--text-sm);
}
.shortcut-help-body kbd {
  flex: 0 0 7.5em;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--text);
}
</style>
