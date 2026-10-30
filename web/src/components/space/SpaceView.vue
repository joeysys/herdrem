<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { bucket, paneKind, canMarkRead, paneTitle, spaceTitle, displayAgent, shellLabel, relTime } from '../../stores/connection'
import { ws } from '../../api/ws'
import { dispatchRespond, promptChecked } from '../../utils/guard'
import { approvalNotes } from '../../stores/sendAck'
import { useHaptic } from '../../composables/useHaptic'
import { safeStorage } from '../../utils/storage'
import { insertIndex } from '../../utils/session'
import type { Pane } from '../../api/types'
import AgentIcon from '../agent/AgentIcon.vue'
import SessionMenu from './SessionMenu.vue'

const props = defineProps<{
  agents: Pane[]
  now: number
  modelValue?: string | null
  activePaneId?: string
  canCreatePane?: boolean
  canManage?: boolean
  canWorktree?: boolean
  /** When set, open that workspace's ⋯ menu once mounted/updated (palette). */
  menuSpaceId?: string | null
}>()
const emit = defineEmits<{ openTerminal: [paneId: string]; 'update:modelValue': [value: string | null]; back: []; newTab: [workspaceId: string, host?: string]; 'menu-opened': [] }>()

interface Space {
  id: string
  label: string
  host: string
  panes: Pane[]
  status: string
  agentCount: number
  shellCount: number
  order?: number
  linked: boolean
}

const activeSpaceId = computed({
  get: () => props.modelValue ?? null,
  set: (v) => emit('update:modelValue', v),
})

const spaces = computed<Space[]>(() => {
  const map = new Map<string, Pane[]>()
  for (const a of props.agents) {
    const id = a.workspace_id || 'default'
    if (!map.has(id)) map.set(id, [])
    map.get(id)!.push(a)
  }
  const rows = [...map.entries()].map(([id, panes]) => {
    const label = spaceTitle(panes)
    const host = panes[0].host || 'local'
    const agentCount = panes.filter((p) => paneKind(p) === 'agent').length
    const shellCount = panes.filter((p) => paneKind(p) === 'shell').length
    return {
      id, label, host, panes,
      status: aggregateStatus(panes),
      agentCount, shellCount,
      order: spaceOrder(panes),
      linked: panes.some((p) => p.workspace_linked === true),
    }
  })
  if (!manualOrder.value) {
    return rows.sort((a, b) => spaceRecency(b.panes) - spaceRecency(a.panes))
  }
  return rows.sort((a, b) => {
    if (a.order === undefined && b.order === undefined) return spaceRecency(b.panes) - spaceRecency(a.panes)
    if (a.order === undefined) return 1
    if (b.order === undefined) return -1
    if (a.order !== b.order) return a.order - b.order
    return spaceRecency(b.panes) - spaceRecency(a.panes)
  })
})

/** Lowest reported workspace number among the panes. Undefined when this
 *  relay did not send one, so 0 stays a real position. */
function spaceOrder(panes: Pane[]): number | undefined {
  let best: number | undefined
  for (const p of panes) {
    if (typeof p.workspace_order !== 'number') continue
    if (best === undefined || p.workspace_order < best) best = p.workspace_order
  }
  return best
}

/** Grid order follows each space's most recently active pane, independent of
 *  the input order — `filtered` pins blocked panes, which must not promote a
 *  whole workspace in the grid. */
function spaceRecency(panes: Pane[]): number {
  let m = 0
  for (const p of panes) m = Math.max(m, p.last_active || 0)
  return m
}

function aggregateStatus(panes: Pane[]): string {
  const bs = panes.map((p) => bucket(p.status))
  if (bs.includes('blocked')) return 'blocked'
  if (bs.includes('done')) return 'done'
  if (bs.includes('working')) return 'working'
  return 'idle'
}

const activeSpace = computed(() => spaces.value.find((s) => s.id === activeSpaceId.value) || null)

// Group panes by branch within a space
const groupByBranch = ref(safeStorage.getItem('herdrem_group_by_branch') === 'true')
watch(groupByBranch, (v) => {
  safeStorage.setItem('herdrem_group_by_branch', String(v))
})

// Recent activity is the grid default. Herdr's workspace number is the manual
// order, used only when that sort is selected.
const manualOrder = ref(safeStorage.getItem('herdrem_space_sort') === 'manual')
watch(manualOrder, (v) => {
  safeStorage.setItem('herdrem_space_sort', v ? 'manual' : 'recent')
})

interface BranchGroup {
  key: string
  branch: string
  cwd: string
  cwdLabel?: string
  panes: Pane[]
}

const groupedPanes = computed<BranchGroup[]>(() => {
  const panes = activeSpace.value?.panes
  if (!panes) return []
  if (!groupByBranch.value) return [{ key: '', branch: '', cwd: '', panes }]
  // Branch alone is not a group identity: the backend resolves it per-cwd
  // (`git -C <cwd> branch --show-current`), so two panes at different paths
  // that both report "main" are different repos' main. Group by cwd+branch.
  const map = new Map<string, BranchGroup>()
  for (const p of panes) {
    const branch = p.branch || '(no branch)'
    const cwd = p.cwd || ''
    const key = cwd + '\u0000' + branch
    if (!map.has(key)) map.set(key, { key, branch, cwd, panes: [] })
    map.get(key)!.panes.push(p)
  }
  const groups = [...map.values()]
  // A branch name identifies a repo only within one project: in a space with
  // multiple cwds, "main" alone can't say which project it belongs to — even
  // when it is the only "main" group right now. So whenever the space spans
  // more than one cwd, label every group header with the shortest cwd suffix
  // that sets it apart from the space's other paths.
  const cwds = [...new Set(groups.map((g) => g.cwd).filter(Boolean))]
  if (cwds.length > 1) {
    for (const g of groups) {
      if (g.cwd) g.cwdLabel = minCwdSuffix(g.cwd, cwds.filter((c) => c !== g.cwd))
    }
  }
  return groups
})

/** Shortest `/`-joined trailing segments of `cwd` not equal to the same-length
 *  suffix of any path in `others`, prefixed with '…/' when truncated.
 *  Falls back to the full cwd when no shorter suffix distinguishes it. */
function minCwdSuffix(cwd: string, others: string[]): string {
  const segs = cwd.split('/').filter(Boolean)
  const otherSegs = others.map((o) => o.split('/').filter(Boolean))
  for (let n = 1; n <= segs.length; n++) {
    const suffix = segs.slice(-n).join('/')
    // Full-length match still distinguishes: a shorter path's whole name is
    // not the same suffix. Only a truncated suffix gets the '…/' prefix.
    if (otherSegs.every((o) => o.slice(-n).join('/') !== suffix)) return n < segs.length ? '…/' + suffix : cwd
  }
  return cwd
}

function enter(id: string) { activeSpaceId.value = id }

const insideEl = ref<HTMLElement | null>(null)

// Bring the open terminal's card into view when landing inside its space.
// Covers both transitions while mounted and mounting directly into a space
// (the desktop list→spaces auto-locate mounts with activeSpaceId preset).
async function scrollActiveIntoView() {
  if (!props.activePaneId) return
  await nextTick()
  insideEl.value?.querySelector('.agent-card.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
}
watch(activeSpace, scrollActiveIntoView)
onMounted(scrollActiveIntoView)

function goBack() {
  // Let the parent decide: on desktop split a term may still be open, and the
  // history stack may hold stale entries — App.vue normalizes both.
  emit('back')
}

const { haptic } = useHaptic()

function quickApprove(paneId: string, answer: string) {
  haptic(18)
  const pane = props.agents.find((a) => a.pane_id === paneId)
  dispatchRespond(paneId, answer, {
    submit: pane ? paneKind(pane) === 'shell' : undefined,
    promptId: pane?.prompt_id,
  })
}
function qaTone(payload: string, label?: string): 'yes' | 'no' | 'neutral' {
  const s = `${payload} ${label || ''}`.toLowerCase()
  if (/\byes\b|approve|allow|accept|^y\b/.test(s)) return 'yes'
  if (/\bno\b|deny|reject|skip|^n\b/.test(s)) return 'no'
  return 'neutral'
}
function qaShort(label: string): string {
  return (label.split(/[,(]/)[0].trim() || label).slice(0, 14)
}

function markRead(paneId: string) {
  haptic(12)
  ws.send({ type: 'mark_pane_read', pane_id: paneId })
}

/** Create a shell pane in the current space. Herdr opens it as a tab; the list stays flat. */
function newTab() {
  haptic(12)
  if (!activeSpace.value) return
  emit('newTab', activeSpace.value.id, activeSpace.value.host)
}

const finePointer = ref(false)
const menu = ref<{ mode: 'workspace' | 'pane'; space: Space; pane: Pane | null; index: number; count: number } | null>(null)

function hostRows(host: string): Space[] {
  return spaces.value.filter((s) => s.host === host)
}

function openWorkspaceMenu(space: Space) {
  const rows = hostRows(space.host)
  menu.value = {
    mode: 'workspace',
    space,
    pane: null,
    index: Math.max(0, rows.findIndex((s) => s.id === space.id)),
    count: rows.length,
  }
}

function openActiveMenu() {
  if (activeSpace.value) openWorkspaceMenu(activeSpace.value)
}

function openPaneMenu(pane: Pane) {
  const space = activeSpace.value
  if (!space) return
  const rows = hostRows(space.host)
  menu.value = {
    mode: 'pane',
    space,
    pane,
    index: Math.max(0, rows.findIndex((s) => s.id === space.id)),
    count: rows.length,
  }
}

/** Reorder within one host. herdr's insert_index is that daemon's list, not
 *  the mixed grid, and it is the index before the workspace is removed. */
function sendMove(space: Space, dir: -1 | 1) {
  if (!props.canManage) return
  const rows = hostRows(space.host)
  const from = rows.findIndex((s) => s.id === space.id)
  const to = from + dir
  if (from < 0 || to < 0 || to >= rows.length) return
  ws.send({
    type: 'workspace_move',
    workspace_id: space.id,
    host: space.host,
    insert_index: insertIndex(from, to),
  })
}

function onMenuMove(dir: -1 | 1) {
  if (!menu.value || menu.value.mode !== 'workspace') return
  sendMove(menu.value.space, dir)
  menu.value = null
}

// Command palette asks for a workspace's ⋯ menu (e.g. New worktree). The
// space grid is flat across hosts, so look it up by id on every request.
watch(
  () => props.menuSpaceId,
  (id) => {
    if (!id) return
    const s = spaces.value.find((x) => x.id === id)
    if (s) openWorkspaceMenu(s)
    emit('menu-opened')
  },
  { immediate: true },
)

let dragFrom = -1
function onDragStart(index: number, e: DragEvent) {
  if (!manualOrder.value || !finePointer.value || !props.canManage) {
    e.preventDefault()
    return
  }
  dragFrom = index
  e.dataTransfer?.setData('text/plain', String(index))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}

function onDrop(index: number) {
  const from = dragFrom
  dragFrom = -1
  const source = spaces.value[from]
  const target = spaces.value[index]
  if (!source || !target || source.host !== target.host || source.id === target.id) return
  const rows = hostRows(source.host)
  const fromHost = rows.findIndex((s) => s.id === source.id)
  const toHost = rows.findIndex((s) => s.id === target.id)
  if (fromHost < 0 || toHost < 0 || fromHost === toHost || !props.canManage) return
  ws.send({
    type: 'workspace_move',
    workspace_id: source.id,
    host: source.host,
    insert_index: insertIndex(fromHost, toHost),
  })
}

const PRESS_MS = 450
const PRESS_SLOP = 8
let pressTimer = 0
let pressFrom = -1
let pressX = 0
let pressY = 0
let pressing = false
let dragging = false
let suppressClick = false

function detachPress() {
  const armed = dragging
  pressing = false
  dragging = false
  pressFrom = -1
  window.clearTimeout(pressTimer)
  window.removeEventListener('pointermove', onWindowPointerMove)
  window.removeEventListener('pointerup', onWindowPointerUp)
  window.removeEventListener('pointercancel', cancelPress)
  // A long-press may not be followed by a click. Drop the suppress flag on
  // the next turn so the click that does follow pointerup is ignored, and a
  // later tap still opens the workspace.
  if (armed) window.setTimeout(() => { suppressClick = false }, 0)
}

function cancelPress() {
  detachPress()
}

function onPointerDown(index: number, e: PointerEvent) {
  if (!manualOrder.value || finePointer.value || !props.canManage) return
  if (e.pointerType === 'mouse' && e.button !== 0) return
  const target = e.target as HTMLElement | null
  if (target?.closest('.sc-menu, .pane-menu')) return
  pressFrom = index
  pressX = e.clientX
  pressY = e.clientY
  pressing = true
  dragging = false
  window.addEventListener('pointermove', onWindowPointerMove)
  window.addEventListener('pointerup', onWindowPointerUp)
  window.addEventListener('pointercancel', cancelPress)
  pressTimer = window.setTimeout(() => {
    if (!pressing) return
    dragging = true
    suppressClick = true
    haptic(12)
  }, PRESS_MS)
}

function onWindowPointerMove(e: PointerEvent) {
  if (!pressing || dragging) return
  if (Math.hypot(e.clientX - pressX, e.clientY - pressY) > PRESS_SLOP) cancelPress()
}

function onWindowPointerUp(e: PointerEvent) {
  const from = pressFrom
  const wasDragging = dragging
  detachPress()
  if (!wasDragging) return
  const el = document.elementFromPoint(e.clientX, e.clientY)
  const card = el?.closest('.space-card')
  if (!card) return
  const cards = Array.from(document.querySelectorAll('.space-grid .space-card'))
  const to = cards.indexOf(card)
  if (to < 0) return
  dragFrom = from
  onDrop(to)
}

function onCardClick(id: string) {
  if (suppressClick) {
    suppressClick = false
    return
  }
  enter(id)
}

onMounted(() => {
  finePointer.value = detectFinePointer()
})

onUnmounted(() => {
  detachPress()
})

function detectFinePointer(): boolean {
  if (typeof window.matchMedia !== 'function') return false
  try {
    return window.matchMedia('(pointer: fine)').matches
  } catch {
    return false
  }
}
</script>

<template>
  <!-- inside a space: show its panes (aligned with list view card style) -->
  <div v-if="activeSpace" ref="insideEl" class="space-inside view-enter">
    <div class="space-topbar">
      <button class="space-back" title="Back to all spaces" @click="goBack">‹ all spaces</button>
      <div v-if="props.canCreatePane" class="space-actions">
        <button class="split-btn" title="New pane" @click="newTab">+ pane</button>
      </div>
    </div>
    <div class="space-title">
      <span class="space-name">{{ activeSpace.label }}</span>
      <button
        v-if="canManage || canWorktree"
        type="button"
        class="sc-menu"
        aria-label="Workspace actions"
        @click.stop="openActiveMenu"
      >⋯</button>
      <span class="space-id mono">{{ activeSpace.id }}</span>
      <span class="space-host mono">{{ activeSpace.host }}</span>
      <button
        class="group-toggle"
        :class="{ on: groupByBranch }"
        :title="groupByBranch ? 'Ungroup by branch' : 'Group by branch'"
        @click="groupByBranch = !groupByBranch"
      >⎇ branch</button>
    </div>
    <div class="agent-list">
      <template v-for="g in groupedPanes" :key="g.key || '_all'">
        <div v-if="groupByBranch && g.branch" class="branch-header">
          <span class="branch-icon">⎇</span>
          <span class="branch-name">{{ g.branch }}</span>
          <span v-if="g.cwdLabel" class="branch-cwd" :title="g.cwd">{{ g.cwdLabel }}</span>
          <span class="branch-count">{{ g.panes.length }}</span>
        </div>
        <article
          v-for="p in g.panes"
          :key="p.pane_id"
          class="agent-card"
          :class="[bucket(p.status), { active: props.activePaneId && p.pane_id === props.activePaneId }]"
          role="button"
          tabindex="0"
          :data-pane-id="p.pane_id"
          :aria-current="props.activePaneId && p.pane_id === props.activePaneId ? 'true' : undefined"
          :aria-label="`Open terminal for ${paneTitle(p)}`"
          @click="emit('openTerminal', p.pane_id)"
          @keydown.enter.self="emit('openTerminal', p.pane_id)"
        >
          <AgentIcon :pane="p" :size="24" />
          <div class="body">
            <div class="name">{{ paneTitle(p) }}</div>
            <div class="meta">
              <span v-if="paneKind(p) === 'shell'" class="kind-tag shell">shell</span>
              <span v-else-if="displayAgent(p)" class="kind-tag agent">{{ displayAgent(p) }}</span>
              <span v-if="paneKind(p) === 'shell' && shellLabel(p)" class="shell-label">{{ shellLabel(p) }}</span>
              <span v-if="!groupByBranch && p.branch" class="branch">⎇ {{ p.branch }}</span>
              <span class="pane-id">{{ p.pane_id }}</span>
              <span v-if="p.host && p.host !== 'local'" class="host">{{ p.host }}</span>
            </div>
            <pre v-if="bucket(p.status) === 'blocked' && p.prompt" class="qa-prompt">{{ p.prompt }}</pre>
            <span v-if="bucket(p.status) === 'blocked' && promptChecked(p)" class="qa-checked">Verified</span>
            <p v-if="bucket(p.status) === 'blocked' && approvalNotes[p.pane_id]" class="qa-note" role="status">{{ approvalNotes[p.pane_id] }}</p>
            <div v-if="bucket(p.status) === 'blocked'" class="quick-approve" @click.stop>
              <template v-if="p.options && p.options.length">
                <button
                  v-for="(opt, i) in p.options.slice(0, 3)"
                  :key="opt"
                  class="qa-btn"
                  :class="qaTone(opt, p.option_labels?.[i])"
                  @click="quickApprove(p.pane_id, opt)"
                >{{ qaShort(p.option_labels?.[i] || opt) }}</button>
              </template>
              <template v-else>
                <button class="qa-btn yes" @click="quickApprove(p.pane_id, 'y')">y</button>
                <button class="qa-btn no" @click="quickApprove(p.pane_id, 'n')">n</button>
              </template>
            </div>
          </div>
          <div class="card-right">
            <button
              v-if="canManage"
              type="button"
              class="pane-menu"
              aria-label="Pane actions"
              @click.stop="openPaneMenu(p)"
            >⋯</button>
            <span v-if="paneKind(p) === 'shell' ? shellLabel(p) : true" class="status-badge" :class="bucket(p.status)">
              <i class="dot"></i>{{ paneKind(p) === 'shell' ? shellLabel(p) : (p.status || '—') }}
            </span>
            <span v-if="relTime(p.last_active, props.now)" class="last-active">{{ relTime(p.last_active, props.now) }}</span>
            <button
              v-if="canMarkRead(p)"
              class="mark-read"
              aria-label="Mark as read"
              title="Mark as read"
              @click.stop="markRead(p.pane_id)"
            >✓</button>
          </div>
        </article>
      </template>
    </div>
  </div>

  <!-- spaces grid -->
  <div v-else class="space-board view-enter">
    <div class="space-sort" role="group" aria-label="Space order">
      <button
        type="button"
        class="group-toggle"
        :class="{ on: !manualOrder }"
        :aria-pressed="!manualOrder"
        title="Most recently active first"
        @click="manualOrder = false"
      >Recent</button>
      <button
        type="button"
        class="group-toggle"
        :class="{ on: manualOrder }"
        :aria-pressed="manualOrder"
        title="Manual workspace order"
        @click="manualOrder = true"
      >Manual</button>
    </div>
    <div class="space-grid">
    <article
      v-for="(s, index) in spaces"
      :key="s.id"
      class="space-card"
      :class="s.status"
      :draggable="manualOrder && finePointer && !!canManage"
      @dragstart="onDragStart(index, $event)"
      @dragover.prevent
      @drop.prevent="onDrop(index)"
      @pointerdown="onPointerDown(index, $event)"
    >
      <button type="button" class="sc-open" draggable="false" @click="onCardClick(s.id)">
        <div class="sc-bar" aria-hidden="true"></div>
        <div class="sc-top">
          <span class="sc-label">{{ s.label }}</span>
          <span class="sc-status mono" :class="s.status">{{ s.status }}</span>
        </div>
        <div class="sc-id-host mono">
          <span class="sc-id">{{ s.id }}</span>
          <span class="sc-host">{{ s.host }}</span>
        </div>
        <div class="sc-counts">
          <span class="sc-count-item"><span class="sc-n">{{ s.panes.length }}</span> panes</span>
          <span class="sc-count-sep">·</span>
          <span class="sc-count-item"><span class="sc-n agents">{{ s.agentCount }}</span> agents</span>
          <span class="sc-count-sep">·</span>
          <span class="sc-count-item"><span class="sc-n shells">{{ s.shellCount }}</span> shells</span>
        </div>
      </button>
      <button
        v-if="canManage || canWorktree"
        type="button"
        class="sc-menu"
        draggable="false"
        aria-label="Workspace actions"
        @click.stop="openWorkspaceMenu(s)"
        @pointerdown.stop
        @dragstart.stop.prevent
      >⋯</button>
    </article>
    <div v-if="spaces.length === 0" class="space-empty">No workspaces yet.</div>
    </div>
  </div>

  <SessionMenu
    v-if="menu"
    :space-id="menu.space.id"
    :label="menu.space.label"
    :host="menu.space.host"
    :linked="menu.space.linked"
    :pane="menu.pane"
    :mode="menu.mode"
    :index="menu.index"
    :count="menu.count"
    :can-manage="!!canManage"
    :can-reorder="manualOrder"
    :can-worktree="!!canWorktree"
    @close="menu = null"
    @move="onMenuMove"
  />
</template>

<style scoped>
.mono { font-family: var(--font-mono); }

.space-topbar {
  display: flex; align-items: center; justify-content: space-between;
  gap: 10px; margin-bottom: 12px;
}
.space-back {
  font-family: var(--font-mono); font-size: var(--text-xs); color: var(--blue);
  background: var(--blue-soft); border: 1px solid rgba(96,165,250,.4); border-radius: var(--radius);
  padding: 5px 11px;
}
.space-back:hover { filter: brightness(1.15); }
.space-title { display: flex; align-items: baseline; gap: 10px; margin-bottom: 14px; flex-wrap: wrap; }
.space-name { font-size: var(--text-lg); font-weight: 700; color: var(--text); }
.space-id { font-size: var(--text-2xs); color: var(--faint); }
.space-host { font-size: var(--text-2xs); color: var(--faint); text-transform: uppercase; letter-spacing: 0.1em; }

/* Group-by-branch toggle */
.group-toggle {
  font-family: var(--font-mono); font-size: var(--text-2xs); font-weight: 600;
  color: var(--muted); background: var(--surface);
  border: 1px solid var(--border); border-radius: 999px;
  padding: 3px 10px; cursor: pointer; margin-left: auto;
  transition: color 0.15s, background 0.15s, border-color 0.15s;
}
.group-toggle:hover { color: var(--text); border-color: var(--border-strong); }
.group-toggle.on { color: var(--blue); border-color: rgba(96,165,250,.5); background: var(--blue-soft); }

/* Split buttons in space title bar */
.space-actions { display: flex; gap: 4px; }
.split-btn {
  font-family: var(--font-mono); font-size: var(--text-2xs); font-weight: 600;
  color: var(--muted); background: var(--surface);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 3px 10px; cursor: pointer;
  transition: color 0.15s, background 0.15s, border-color 0.15s;
}
.split-btn:hover { color: var(--blue); border-color: rgba(96,165,250,.5); background: var(--blue-soft); }

/* Branch group header */
.branch-header {
  display: flex; align-items: center; gap: 6px;
  padding: 10px 4px 4px;
  font-family: var(--font-mono); font-size: var(--text-2xs);
  color: var(--blue);
}
.branch-header .branch-icon { opacity: 0.7; }
.branch-header .branch-name { font-weight: 700; }
.branch-header .branch-cwd {
  color: var(--faint); font-weight: 400;
  min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.branch-header .branch-count {
  color: var(--faint); font-weight: 400;
  background: var(--surface); border: 1px solid var(--border);
  border-radius: 999px; padding: 1px 7px; font-size: 9px;
}

/* Pane list inside a space — aligned with App.vue agent-card style */
.agent-list { display: flex; flex-direction: column; gap: 7px; }
.agent-card {
  display: flex; align-items: center; gap: 12px;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  padding: 11px 13px; cursor: pointer; text-align: left;
  transition: border-color 0.15s, background 0.15s;
}
.agent-card:hover { border-color: var(--border-strong); background: var(--surface-2); }
.agent-card.blocked { border-color: rgba(248,113,113,.4); }
.agent-card .body { flex: 1; min-width: 0; }
.agent-card .name { font-weight: 600; font-size: var(--text-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.agent-card .meta { display: flex; align-items: center; gap: 7px; margin-top: 3px; font-size: var(--text-2xs); color: var(--muted); font-family: var(--font-mono); }
.agent-card .kind-tag { font-weight: 600; }
.agent-card .kind-tag.shell { color: var(--muted); }
.agent-card .kind-tag.agent { color: var(--green); }
.agent-card .shell-label { color: var(--text-dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 120px; }
.agent-card .branch { color: var(--blue); }
.agent-card .pane-id { color: var(--faint); }
.agent-card .host { color: var(--amber); }
.card-right {
  display: flex; flex-direction: column; align-items: flex-end; gap: 4px;
  flex-shrink: 0;
}
.card-right .last-active { color: var(--faint); font-size: var(--text-2xs); font-family: var(--font-mono); }
.quick-approve { display: flex; gap: 5px; margin-top: 7px; flex-wrap: wrap; }
.quick-approve .qa-btn {
  font-family: var(--font-mono); font-size: 9px; font-weight: 600;
  padding: 4px 9px; border-radius: var(--radius);
  border: 1px solid var(--border-strong); background: var(--surface-2);
  color: var(--text-dim); cursor: pointer;
  transition: filter 0.12s, transform 0.12s;
}
.quick-approve .qa-btn:active { transform: scale(0.92); }
.quick-approve .qa-btn.yes { color: var(--green); border-color: rgba(52,211,153,.5); background: var(--green-soft); }
.quick-approve .qa-btn.no { color: var(--red); border-color: rgba(248,113,113,.5); background: var(--red-soft); }
.quick-approve .qa-btn.neutral { color: var(--text-dim); border-color: var(--border-strong); background: var(--surface-2); }
.quick-approve .qa-btn:hover { filter: brightness(1.25); }
.status-badge {
  display: inline-flex; align-items: center; gap: 5px;
  font-family: var(--font-mono); font-size: var(--text-2xs);
  text-transform: uppercase; letter-spacing: 0.06em;
  color: var(--muted); flex-shrink: 0;
}
.status-badge .dot { width: 6px; height: 6px; border-radius: 50%; background: var(--muted); }
.status-badge.blocked { color: var(--red); }
.status-badge.blocked .dot { background: var(--red); }
.status-badge.working { color: var(--orange); }
.status-badge.working .dot { background: var(--orange); animation: pulse 1.5s ease-in-out infinite; }
.status-badge.done { color: var(--green); }
.status-badge.done .dot { background: var(--green); }

/* Spaces grid cards */
.space-board { display: flex; flex-direction: column; gap: 10px; }
.space-sort { display: flex; justify-content: flex-end; gap: 6px; }
.space-sort .group-toggle { margin-left: 0; }
.space-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 10px; }
.space-card {
  position: relative; overflow: hidden;
  display: block;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  color: var(--text);
  transition: transform 0.15s, border-color 0.15s, background 0.15s;
}
.space-card:hover { transform: translateY(-2px); border-color: var(--border-strong); background: var(--surface-2); }
.sc-open {
  position: relative; display: flex; flex-direction: column; gap: 6px;
  width: 100%; margin: 0; padding: 14px 36px 14px 14px;
  border: 0; background: transparent; color: inherit; font: inherit;
  text-align: left; cursor: pointer;
}
.sc-bar { position: absolute; left: 0; top: 0; bottom: 0; width: 3px; background: var(--faint); }
.space-card.blocked .sc-bar { background: var(--red); }
.space-card.working .sc-bar { background: var(--orange); }
.space-card.done .sc-bar { background: var(--green); }
.sc-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.sc-menu, .pane-menu {
  font: inherit; border: 0; background: transparent; color: var(--muted);
  cursor: pointer; padding: 0 4px; line-height: 1; flex-shrink: 0;
}
.space-card > .sc-menu {
  position: absolute; top: 12px; right: 8px; z-index: 1;
}
.sc-menu:hover, .pane-menu:hover { color: var(--text); }
.sc-open:focus-visible { outline: 2px solid var(--blue); outline-offset: -2px; }
.sc-label { font-weight: 700; font-size: var(--text-md); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sc-status { font-size: var(--text-2xs); text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted); flex-shrink: 0; }
.space-card.blocked .sc-status { color: var(--red); }
.space-card.working .sc-status { color: var(--orange); }
.space-card.done .sc-status { color: var(--green); }
.sc-id-host {
  display: flex; align-items: baseline; justify-content: space-between; gap: 8px;
  font-size: var(--text-2xs); color: var(--faint);
}
.sc-id-host .sc-id { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sc-id-host .sc-host { text-transform: uppercase; letter-spacing: 0.08em; flex-shrink: 0; }
.sc-counts { display: flex; align-items: center; gap: 5px; font-size: var(--text-2xs); color: var(--muted); }
.sc-count-item { display: inline-flex; align-items: baseline; gap: 3px; }
.sc-n { font-weight: 700; font-variant-numeric: tabular-nums; color: var(--text-dim); }
.sc-n.agents { color: var(--green); }
.sc-n.shells { color: var(--muted); }
.sc-count-sep { color: var(--faint); }
.space-empty { grid-column: 1 / -1; text-align: center; color: var(--faint); padding: 40px; }
</style>
