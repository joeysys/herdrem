<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { bucket, displayAgent, paneTitle, workspaceLabel } from '../../stores/connection'
import type { Pane } from '../../api/types'

const props = defineProps<{
  panes: Pane[]
  canCreate?: boolean
  canWorktree?: boolean
}>()

const emit = defineEmits<{
  close: []
  openPane: [paneId: string]
  openSpace: [workspaceId: string]
  /** Built-in commands: 'new-workspace', 'new-shell' (pane-create capability) */
  command: [id: string]
  /** Open the workspace's ⋯ management menu (worktree actions live there) */
  workspaceMenu: [workspaceId: string]
}>()

interface PalItem {
  key: string
  kind: 'pane' | 'workspace' | 'command'
  label: string
  detail: string
  run: () => void
}

interface PalGroup {
  title: string
  items: { item: PalItem; idx: number }[]
}

const query = ref('')
const statusFilter = ref('all')
const active = ref(0)
const inputEl = ref<HTMLInputElement | null>(null)
const listEl = ref<HTMLElement | null>(null)

const STATUS_CHIPS = ['all', 'blocked', 'working', 'done', 'idle']

function paneMatches(p: Pane, q: string): boolean {
  if (!q) return true
  const hay = [
    paneTitle(p),
    p.pane_id,
    displayAgent(p),
    p.project,
    p.host,
    p.workspace_label,
    p.cwd,
    p.terminal_title_stripped,
  ].map((s) => (s || '').toLowerCase())
  return hay.some((s) => s.includes(q))
}

interface WsInfo {
  id: string
  label: string
  host: string
}

const workspaces = computed<WsInfo[]>(() => {
  const seen = new Map<string, WsInfo>()
  for (const p of props.panes) {
    const id = p.workspace_id || 'default'
    if (seen.has(id)) continue
    seen.set(id, { id, label: workspaceLabel(p) || id, host: p.host || 'local' })
  }
  return [...seen.values()]
})

/** Sections: Commands first, then one per workspace (workspace jump row,
 *  worktree action, then that workspace's panes). */
const groups = computed<PalGroup[]>(() => {
  const q = query.value.trim().toLowerCase()
  const out: PalGroup[] = []
  let idx = 0
  const push = (title: string, items: PalItem[]) => {
    if (!items.length) return
    out.push({
      title,
      items: items.map((item) => ({ item, idx: idx++ })),
    })
  }

  if (props.canCreate) {
    const cmds: PalItem[] = []
    const addCmd = (id: string, label: string, detail: string) => {
      if (q && !label.toLowerCase().includes(q)) return
      cmds.push({
        key: `cmd:${id}`,
        kind: 'command',
        label,
        detail,
        run: () => emit('command', id),
      })
    }
    addCmd('new-workspace', 'New workspace', 'pane_create')
    addCmd('new-shell', 'New shell', 'pane_create')
    push('Commands', cmds)
  }

  for (const ws of workspaces.value) {
    const wsMatch = !q || ws.label.toLowerCase().includes(q) || ws.id.toLowerCase().includes(q)
    const items: PalItem[] = []
    if (wsMatch) {
      items.push({
        key: `ws:${ws.id}`,
        kind: 'workspace',
        label: ws.label,
        detail: `workspace · ${ws.host}`,
        run: () => emit('openSpace', ws.id),
      })
      if (props.canWorktree) {
        items.push({
          key: `wt:${ws.id}`,
          kind: 'command',
          label: 'New worktree…',
          detail: `in ${ws.label}`,
          run: () => emit('workspaceMenu', ws.id),
        })
      }
    }
    for (const p of props.panes) {
      if ((p.workspace_id || 'default') !== ws.id) continue
      if (statusFilter.value !== 'all' && bucket(p.status) !== statusFilter.value) continue
      if (!paneMatches(p, q)) continue
      items.push({
        key: `pane:${p.pane_id}`,
        kind: 'pane',
        label: paneTitle(p),
        detail: `${displayAgent(p)}${p.status ? ` · ${bucket(p.status)}` : ''}`,
        run: () => emit('openPane', p.pane_id),
      })
    }
    push(ws.label, items)
  }
  return out
})

const flat = computed<PalItem[]>(() =>
  groups.value.flatMap((g) => g.items.map((i) => i.item)),
)

watch([query, statusFilter], () => {
  active.value = 0
})

function move(delta: number) {
  const n = flat.value.length
  if (!n) return
  active.value = (active.value + delta + n) % n
  nextTick(() => {
    listEl.value?.querySelector(`[data-idx="${active.value}"]`)?.scrollIntoView({ block: 'nearest' })
  })
}

function pick(item: PalItem) {
  item.run()
  emit('close')
}

// Section-level keys only: work wherever focus sits inside the palette.
// Nav/Enter keys stay on the input so the filter chips (and any future
// buttons) keep their native Enter/Space activation.
function onPaletteKey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    emit('close')
    return
  }
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
  }
}

function onInputKey(e: KeyboardEvent) {
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      move(1)
      return
    case 'ArrowUp':
      e.preventDefault()
      move(-1)
      return
    case 'Home':
      e.preventDefault()
      active.value = 0
      return
    case 'End':
      e.preventDefault()
      active.value = Math.max(0, flat.value.length - 1)
      return
    case 'Enter':
      e.preventDefault()
      if (flat.value[active.value]) pick(flat.value[active.value])
      return
  }
}

onMounted(() => {
  inputEl.value?.focus()
})
</script>

<template>
  <div class="pal-scrim" @click.self="emit('close')">
    <section class="pal" role="dialog" aria-modal="true" aria-label="Command palette" @keydown="onPaletteKey">
      <input
        ref="inputEl"
        v-model="query"
        class="pal-input"
        type="text"
        placeholder="Jump to pane, workspace, or command…"
        aria-label="Command palette search"
        role="combobox"
        aria-autocomplete="list"
        aria-expanded="true"
        aria-controls="pal-results"
        :aria-activedescendant="flat[active] ? `pal-opt-${active}` : undefined"
        @keydown="onInputKey"
      />
      <div class="pal-chips" role="group" aria-label="Filter panes by status">
        <button
          v-for="c in STATUS_CHIPS"
          :key="c"
          type="button"
          class="pal-chip"
          :class="{ on: statusFilter === c }"
          @click="statusFilter = c"
        >
          {{ c }}
        </button>
      </div>
      <div id="pal-results" ref="listEl" class="pal-list" role="listbox" aria-label="Results">
        <div v-for="grp in groups" :key="grp.title" class="pal-section" role="group" :aria-label="grp.title">
          <div class="pal-group" role="presentation">{{ grp.title }}</div>
          <div
            v-for="entry in grp.items"
            :id="`pal-opt-${entry.idx}`"
            :key="entry.item.key"
            :data-idx="entry.idx"
            class="pal-item"
            :class="[entry.item.kind, { active: entry.idx === active }]"
            role="option"
            :aria-selected="entry.idx === active"
            @click="pick(entry.item)"
            @mouseenter="active = entry.idx"
          >
            <span class="pal-label">{{ entry.item.label }}</span>
            <span class="pal-detail">{{ entry.item.detail }}</span>
          </div>
        </div>
        <p v-if="!flat.length" class="pal-empty">No matches.</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.pal-scrim {
  position: fixed;
  inset: 0;
  z-index: 80;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 8vh 16px 16px;
}
.pal {
  width: min(520px, 100%);
  max-height: min(72vh, 640px);
  display: flex;
  flex-direction: column;
  background: var(--surface);
  color: var(--text);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
}
.pal-input {
  margin: 10px 12px 6px;
  padding: 8px 10px;
  font: inherit;
  font-size: var(--text-sm);
  color: var(--text);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.pal-input:focus {
  outline: none;
  border-color: var(--blue);
}
.pal-chips {
  display: flex;
  gap: 6px;
  padding: 0 12px 8px;
  border-bottom: 1px solid var(--border);
}
.pal-chip {
  font-size: var(--text-2xs);
  padding: 2px 8px;
  border-radius: 99px;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
  text-transform: capitalize;
}
.pal-chip.on {
  color: var(--blue);
  border-color: var(--blue);
  background: var(--blue-soft);
}
.pal-list {
  overflow-y: auto;
  padding: 4px 6px 8px;
}
.pal-group {
  margin: 8px 6px 2px;
  font-size: var(--text-2xs);
  font-weight: 600;
  color: var(--faint);
  text-transform: uppercase;
  letter-spacing: 0.08em;
}
.pal-item {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 6px 8px;
  border-radius: var(--radius);
  cursor: pointer;
}
.pal-item.active {
  background: var(--blue-soft);
}
.pal-label {
  font-size: var(--text-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pal-item.command .pal-label {
  color: var(--blue);
}
.pal-item.workspace .pal-label {
  font-weight: 600;
}
.pal-detail {
  margin-left: auto;
  flex: 0 0 auto;
  font-size: var(--text-2xs);
  color: var(--faint);
}
.pal-empty {
  padding: 16px;
  text-align: center;
  color: var(--muted);
  font-size: var(--text-sm);
}
</style>
