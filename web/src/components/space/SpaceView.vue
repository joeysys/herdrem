<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { bucket, paneKind, paneTitle, spaceTitle, displayAgent, shellLabel, relTime } from '../../stores/connection'
import { ws } from '../../api/ws'
import { useHaptic } from '../../composables/useHaptic'
import { safeStorage } from '../../utils/storage'
import type { Pane } from '../../api/types'
import AgentIcon from '../agent/AgentIcon.vue'

const props = defineProps<{ agents: Pane[]; now: number; modelValue?: string | null; activePaneId?: string; canCreatePane?: boolean }>()
const emit = defineEmits<{ openTerminal: [paneId: string]; 'update:modelValue': [value: string | null]; back: []; newTab: [workspaceId: string, host?: string] }>()

interface Space {
  id: string
  label: string
  host: string
  panes: Pane[]
  status: string
  agentCount: number
  shellCount: number
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
  return [...map.entries()].map(([id, panes]) => {
    const label = spaceTitle(panes)
    const host = panes[0].host || 'local'
    const agentCount = panes.filter((p) => paneKind(p) === 'agent').length
    const shellCount = panes.filter((p) => paneKind(p) === 'shell').length
    return { id, label, host, panes, status: aggregateStatus(panes), agentCount, shellCount }
  })
})

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

interface BranchGroup {
  branch: string
  panes: Pane[]
}

const groupedPanes = computed<BranchGroup[]>(() => {
  const panes = activeSpace.value?.panes
  if (!panes) return []
  if (!groupByBranch.value) return [{ branch: '', panes }]
  const map = new Map<string, Pane[]>()
  for (const p of panes) {
    const b = p.branch || '(no branch)'
    if (!map.has(b)) map.set(b, [])
    map.get(b)!.push(p)
  }
  return [...map.entries()].map(([branch, ps]) => ({ branch, panes: ps }))
})

function enter(id: string) { activeSpaceId.value = id }
function goBack() {
  // Let the parent decide: on desktop split a term may still be open, and the
  // history stack may hold stale entries — App.vue normalizes both.
  emit('back')
}

const { haptic } = useHaptic()

function quickApprove(paneId: string, answer: string) {
  haptic(18)
  const pane = props.agents.find((a) => a.pane_id === paneId)
  ws.send({ type: 'respond', pane_id: paneId, text: answer, submit: pane ? paneKind(pane) === 'shell' : undefined })
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

/** Create a new tab in the current space */
function newTab() {
  haptic(12)
  if (!activeSpace.value) return
  emit('newTab', activeSpace.value.id, activeSpace.value.host)
}
</script>

<template>
  <!-- inside a space: show its panes (aligned with list view card style) -->
  <div v-if="activeSpace" class="space-inside view-enter">
    <div class="space-topbar">
      <button class="space-back" title="Back to all spaces" @click="goBack">‹ all spaces</button>
      <div v-if="props.canCreatePane" class="space-actions">
        <button class="split-btn" title="New tab (shell)" @click="newTab">+ tab</button>
      </div>
    </div>
    <div class="space-title">
      <span class="space-name">{{ activeSpace.label }}</span>
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
      <template v-for="g in groupedPanes" :key="g.branch || '_all'">
        <div v-if="groupByBranch && g.branch" class="branch-header">
          <span class="branch-icon">⎇</span>
          <span class="branch-name">{{ g.branch }}</span>
          <span class="branch-count">{{ g.panes.length }}</span>
        </div>
        <article
          v-for="p in g.panes"
          :key="p.pane_id"
          class="agent-card"
          :class="[bucket(p.status), { active: props.activePaneId && p.pane_id === props.activePaneId }]"
          role="button"
          tabindex="0"
          :aria-current="props.activePaneId && p.pane_id === props.activePaneId ? 'true' : undefined"
          :aria-label="`Open terminal for ${paneTitle(p)}`"
          @click="emit('openTerminal', p.pane_id)"
          @keydown.enter="emit('openTerminal', p.pane_id)"
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
            <span v-if="paneKind(p) === 'shell' ? shellLabel(p) : true" class="status-badge" :class="bucket(p.status)">
              <i class="dot"></i>{{ paneKind(p) === 'shell' ? shellLabel(p) : (p.status || '—') }}
            </span>
            <span v-if="relTime(p.last_active, props.now)" class="last-active">{{ relTime(p.last_active, props.now) }}</span>
          </div>
        </article>
      </template>
    </div>
  </div>

  <!-- spaces grid -->
  <div v-else class="space-grid view-enter">
    <button
      v-for="s in spaces"
      :key="s.id"
      class="space-card"
      :class="s.status"
      @click="enter(s.id)"
    >
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
    <div v-if="spaces.length === 0" class="space-empty">No workspaces yet.</div>
  </div>
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
.space-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 10px; }
.space-card {
  position: relative; overflow: hidden;
  display: flex; flex-direction: column; gap: 6px;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  padding: 14px; cursor: pointer; text-align: left;
  color: var(--text);
  transition: transform 0.15s, border-color 0.15s, background 0.15s;
}
.space-card:hover { transform: translateY(-2px); border-color: var(--border-strong); background: var(--surface-2); }
.sc-bar { position: absolute; left: 0; top: 0; bottom: 0; width: 3px; background: var(--faint); }
.space-card.blocked .sc-bar { background: var(--red); }
.space-card.working .sc-bar { background: var(--orange); }
.space-card.done .sc-bar { background: var(--green); }
.sc-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
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
