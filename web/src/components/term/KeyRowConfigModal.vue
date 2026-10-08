<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useKeyRowConfig, type KeyRowPaneType } from '../../composables/useKeyRowConfig'
import { addOverlaySource, registerTopLayer } from '../../composables/useShortcuts'

const props = withDefaults(
  defineProps<{ type?: KeyRowPaneType }>(),
  { type: 'agent' },
)
const emit = defineEmits<{ close: [] }>()

const currentType = ref<KeyRowPaneType>(props.type)
const modalEl = ref<HTMLElement | null>(null)

const agentConfig = useKeyRowConfig('agent')
const shellConfig = useKeyRowConfig('shell')

const activeConfig = computed(() => {
  return currentType.value === 'agent' ? agentConfig : shellConfig
})

let offTopLayer: (() => void) | null = null
let offOverlay: (() => void) | null = null

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation()
    emit('close')
  }
}

onMounted(() => {
  offOverlay = addOverlaySource(() => true)
  offTopLayer = registerTopLayer(() => {
    emit('close')
    return true
  })
  window.addEventListener('keydown', onKeydown)
  modalEl.value?.focus()
})

onUnmounted(() => {
  offTopLayer?.()
  offTopLayer = null
  offOverlay?.()
  offOverlay = null
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="krc-backdrop" @click.self="emit('close')">
    <section
      ref="modalEl"
      tabindex="-1"
      class="krc-modal view-enter"
      role="dialog"
      aria-modal="true"
      aria-labelledby="krc-title"
    >
      <header class="krc-head">
        <div class="krc-title-row">
          <h2 id="krc-title">customize shortcut keys</h2>
          <div class="krc-head-actions">
            <button
              type="button"
              class="krc-reset-btn"
              title="Reset keys to default order and visibility"
              @click="activeConfig.reset()"
            >reset</button>
            <button
              type="button"
              class="krc-close-btn"
              aria-label="Close"
              title="Close"
              @click="emit('close')"
            >×</button>
          </div>
        </div>
        <div class="krc-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            class="krc-tab"
            :class="{ active: currentType === 'agent' }"
            :aria-selected="currentType === 'agent'"
            @click="currentType = 'agent'"
          >agent pane</button>
          <button
            type="button"
            role="tab"
            class="krc-tab"
            :class="{ active: currentType === 'shell' }"
            :aria-selected="currentType === 'shell'"
            @click="currentType = 'shell'"
          >shell pane</button>
        </div>
        <p class="krc-hint">Reorder or toggle visibility of buttons in the shortcut bar.</p>
      </header>

      <div class="krc-body">
        <ul class="krc-list">
          <li
            v-for="(item, i) in activeConfig.items.value"
            :key="item.id"
            class="krc-item"
            :class="{ hidden: !item.visible }"
          >
            <label class="krc-check-wrap">
              <input
                type="checkbox"
                class="krc-checkbox"
                :checked="item.visible"
                :aria-label="'Show ' + item.name"
                @change="activeConfig.toggleVisible(item.id)"
              />
              <span class="krc-badge" :class="{ danger: item.danger }">{{ item.label }}</span>
            </label>
            <div class="krc-info">
              <span class="krc-name">{{ item.name }}</span>
              <span v-if="item.description" class="krc-desc">{{ item.description }}</span>
            </div>
            <div class="krc-reorder">
              <button
                type="button"
                class="krc-arrow-btn"
                :disabled="i === 0"
                :aria-label="`Move ${item.name} up`"
                title="Move up"
                @click="activeConfig.moveUp(i)"
              >▲</button>
              <button
                type="button"
                class="krc-arrow-btn"
                :disabled="i === activeConfig.items.value.length - 1"
                :aria-label="`Move ${item.name} down`"
                title="Move down"
                @click="activeConfig.moveDown(i)"
              >▼</button>
            </div>
          </li>
        </ul>
      </div>
    </section>
  </div>
</template>

<style scoped>
.krc-backdrop {
  position: fixed;
  inset: 0;
  z-index: 10000;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
}

.krc-modal {
  width: min(440px, 100%);
  max-height: min(85vh, 620px);
  background: var(--surface);
  color: var(--text);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.krc-modal:focus {
  outline: none;
}

.krc-head {
  padding: 12px 14px 8px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.krc-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.krc-title-row h2 {
  margin: 0;
  font-size: var(--text-md);
  font-weight: 600;
}

.krc-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.krc-reset-btn {
  border: 1px solid var(--border);
  background: var(--surface-2);
  color: var(--text-dim);
  font-size: var(--text-xs);
  padding: 3px 8px;
  border-radius: var(--radius);
  cursor: pointer;
  transition: all 0.15s;
}

.krc-reset-btn:hover {
  background: var(--surface-3);
  color: var(--text);
}

.krc-close-btn {
  border: none;
  background: transparent;
  color: var(--muted);
  font-size: 20px;
  line-height: 1;
  padding: 2px 6px;
  cursor: pointer;
  border-radius: var(--radius);
}

.krc-close-btn:hover {
  color: var(--text);
}

.krc-tabs {
  display: flex;
  gap: 6px;
  margin-bottom: 8px;
}

.krc-tab {
  flex: 1;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  padding: 5px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-2);
  color: var(--text-dim);
  cursor: pointer;
  transition: all 0.15s;
}

.krc-tab.active {
  background: var(--blue-soft);
  color: var(--blue);
  border-color: rgba(96, 165, 250, 0.45);
  font-weight: 600;
}

.krc-hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--muted);
}

.krc-body {
  flex: 1;
  overflow-y: auto;
  padding: 6px 12px 14px;
}

.krc-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.krc-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 8px;
  border-radius: var(--radius);
  background: var(--surface-2);
  border: 1px solid var(--border);
  transition: background 0.15s, opacity 0.15s;
}

.krc-item.hidden {
  opacity: 0.55;
  background: var(--surface);
}

.krc-check-wrap {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  flex-shrink: 0;
}

.krc-checkbox {
  cursor: pointer;
}

.krc-badge {
  display: inline-block;
  min-width: 32px;
  text-align: center;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 600;
  color: var(--text);
  background: var(--surface-3);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 2px 6px;
}

.krc-badge.danger {
  color: var(--red);
}

.krc-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.krc-name {
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.krc-desc {
  font-size: var(--text-2xs);
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.krc-reorder {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.krc-arrow-btn {
  border: 1px solid var(--border);
  background: var(--surface-3);
  color: var(--text-dim);
  font-size: 10px;
  line-height: 1;
  padding: 5px 7px;
  border-radius: var(--radius);
  cursor: pointer;
  transition: all 0.12s;
}

.krc-arrow-btn:hover:not(:disabled) {
  background: var(--surface);
  color: var(--text);
}

.krc-arrow-btn:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}
</style>
