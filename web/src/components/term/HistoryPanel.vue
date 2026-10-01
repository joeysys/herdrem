<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { kindOf, type HistoryEntry, type HistoryKind, type Scope, type Where } from '../../utils/cmdHistory'

// Searchable input history: shell commands and prompts sent to any agent
// through this app, shared by every device using the relay (the ^R of the
// phone). It opens on the current workspace; the scope chips widen it to the
// whole relay or narrow it to this pane. Picking a row fills the current
// pane's input; it never sends anything.
const props = withDefaults(
  defineProps<{ where?: Where & { workspaceLabel?: string }; defaultKind?: HistoryKind }>(),
  { defaultKind: 'sh', where: () => ({}) },
)
const emit = defineEmits<{ pick: [text: string]; close: [] }>()

const history = useCmdHistoryStore()
const query = ref('')
const kind = ref<'all' | HistoryKind>(props.defaultKind)
const agent = ref('')
const scope = ref<Scope>(props.where.workspaceId ? 'workspace' : 'relay')
const active = ref(0)
const confirmClear = ref(false)
const inputEl = ref<HTMLInputElement | null>(null)
const listEl = ref<HTMLElement | null>(null)

const scopes = computed<Array<{ v: Scope; label: string; title: string }>>(() => {
  const out: Array<{ v: Scope; label: string; title: string }> = []
  if (props.where.paneId) out.push({ v: 'pane', label: 'pane', title: 'Only what was entered in this pane' })
  if (props.where.workspaceId) {
    const name = props.where.workspaceLabel
    out.push({ v: 'workspace', label: 'workspace', title: name ? `Workspace: ${name}` : 'This workspace' })
  }
  out.push({ v: 'relay', label: 'relay', title: 'Everything on this relay, from every workspace and device' })
  return out
})

const KINDS: Array<{ v: 'all' | HistoryKind; label: string }> = [
  { v: 'all', label: 'all' },
  { v: 'ag', label: 'agents' },
  { v: 'sh', label: 'shells' },
]

// The agent-type filter only makes sense while prompts are in view.
const showAgentFilter = computed(() => kind.value !== 'sh' && history.agentTypes.length > 0)
watch(kind, (k) => {
  if (k === 'sh') agent.value = ''
})
watch(() => history.agentTypes, (types) => {
  if (agent.value && !types.includes(agent.value)) agent.value = ''
})

const filter = computed(() => ({
  ...props.where,
  kind: kind.value,
  agent: showAgentFilter.value ? agent.value : '',
  scope: scope.value,
}))
const results = computed(() => history.search(query.value, filter.value, 100))

watch([query, kind, agent, scope], () => { active.value = 0 })
watch(results, (r) => { if (active.value >= r.length) active.value = Math.max(0, r.length - 1) })

// Clearing follows the scope: on the relay scope it empties the relay (for
// every device); on pane / workspace it empties that workspace only.
const clearsRelay = computed(() => scope.value === 'relay' || !props.where.workspaceId)
const clearLabel = computed(() => {
  if (confirmClear.value) return 'tap again to clear'
  const what = kind.value === 'all' ? 'all' : kind.value === 'ag' ? 'prompts' : 'commands'
  return clearsRelay.value ? `clear ${what}` : `clear ${what} here`
})
const clearTitle = computed(() =>
  clearsRelay.value ? 'Removes it from the relay, for every device' : 'Removes this workspace\'s entries, for every device',
)
const clearable = computed(() =>
  history.count({ ...props.where, kind: kind.value, scope: clearsRelay.value ? 'relay' : 'workspace' }) > 0,
)

let clearTimer: ReturnType<typeof setTimeout> | null = null
function onClear() {
  // Two-step so a stray tap cannot wipe the history.
  if (!confirmClear.value) {
    confirmClear.value = true
    clearTimer = setTimeout(() => { confirmClear.value = false }, 3000)
    return
  }
  if (clearTimer) clearTimeout(clearTimer)
  confirmClear.value = false
  const { host, workspaceId } = props.where
  history.clear(kind.value, clearsRelay.value || !workspaceId ? undefined : { host: host ?? '', workspaceId })
}

function move(delta: number) {
  const n = results.value.length
  if (!n) return
  active.value = (active.value + delta + n) % n
  nextTick(() => {
    const el = listEl.value?.querySelector<HTMLElement>(`[data-i="${active.value}"]`)
    el?.scrollIntoView?.({ block: 'nearest' })
  })
}

function pickActive() {
  const e = results.value[active.value]
  if (e) emit('pick', e.c)
}

// Only the search field and the list handle these; the filter controls keep
// their native keyboard behaviour (Enter on a chip toggles it, etc.).
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') { e.preventDefault(); emit('close'); return }
  if ((e.target as HTMLElement | null)?.closest('.hp-filters, .hp-foot')) return
  if (e.key === 'ArrowDown') { e.preventDefault(); move(1) }
  else if (e.key === 'ArrowUp') { e.preventDefault(); move(-1) }
  else if (e.key === 'Enter') { e.preventDefault(); pickActive() }
}

function ago(ts: number): string {
  const s = Math.max(0, Math.floor((Date.now() - ts) / 1000))
  if (s < 60) return 'now'
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

// "claude · api · herdrem · ×3 · 2h": agent and pane for prompts, the
// workspace when looking across the relay, then count and age.
function meta(e: HistoryEntry): string {
  const parts: string[] = []
  if (kindOf(e) === 'ag') {
    if (e.a) parts.push(e.a)
    if (e.w) parts.push(e.w)
  }
  if (scope.value === 'relay' && e.l) parts.push(e.l)
  if (e.n > 1) parts.push(`×${e.n}`)
  parts.push(ago(e.t))
  return parts.join(' · ')
}

onMounted(() => nextTick(() => inputEl.value?.focus()))
onBeforeUnmount(() => { if (clearTimer) clearTimeout(clearTimer) })
</script>

<template>
  <aside class="hist-panel" role="dialog" aria-label="Input history" @keydown="onKeydown" @click.stop>
    <header class="hp-head">
      <h3>history</h3>
      <button type="button" class="hp-close" aria-label="Close history" @click="emit('close')">×</button>
    </header>

    <div class="hp-search">
      <input
        ref="inputEl"
        v-model="query"
        type="search"
        placeholder="search history…"
        autocomplete="off"
        autocapitalize="off"
        autocorrect="off"
        spellcheck="false"
        enterkeyhint="done"
        aria-label="Search input history"
        aria-controls="hist-list"
      />
    </div>

    <div class="hp-filters" role="group" aria-label="History filters">
      <button
        v-for="k in KINDS"
        :key="k.v"
        type="button"
        class="hp-chip"
        :class="{ on: kind === k.v }"
        :aria-pressed="kind === k.v"
        @click="kind = k.v"
      >{{ k.label }}</button>
      <select v-if="showAgentFilter" v-model="agent" class="hp-select" aria-label="Filter by agent type">
        <option value="">all agent types</option>
        <option v-for="t in history.agentTypes" :key="t" :value="t">{{ t }}</option>
      </select>
    </div>

    <div class="hp-filters hp-scopes" role="group" aria-label="History scope">
      <button
        v-for="sc in scopes"
        :key="sc.v"
        type="button"
        class="hp-chip scope"
        :class="{ on: scope === sc.v }"
        :aria-pressed="scope === sc.v"
        :title="sc.title"
        @click="scope = sc.v"
      >{{ sc.label }}</button>
    </div>

    <ul id="hist-list" ref="listEl" class="hp-list" role="listbox" aria-label="Matching history">
      <li v-if="!results.length" class="hp-empty" role="presentation">
        {{ history.entries.length ? 'nothing matches' : 'nothing yet — send something from a pane' }}
      </li>
      <li v-for="(e, i) in results" :key="kindOf(e) + e.c" class="hp-row" :class="{ active: i === active }" role="presentation">
        <button
          type="button"
          class="hp-cmd"
          role="option"
          :data-i="i"
          :aria-selected="i === active"
          :title="e.c"
          @click="emit('pick', e.c)"
          @mouseenter="active = i"
        >
          <span class="text">{{ e.c }}</span>
          <span class="meta">{{ meta(e) }}</span>
        </button>
        <button type="button" class="hp-del" :aria-label="`Remove ${e.c} from history`" title="Remove" @click="history.remove(e)">×</button>
      </li>
    </ul>

    <footer class="hp-foot">
      <span class="hint">↑↓ select · enter fill · esc close</span>
      <button type="button" class="hp-clear" :class="{ confirm: confirmClear }" :disabled="!clearable" :title="clearTitle" @click="onClear">
        {{ clearLabel }}
      </button>
    </footer>
  </aside>
</template>

<style scoped>
.hist-panel {
  position: absolute; right: 0; top: 0; bottom: 0; z-index: 78;
  width: min(380px, 100%);
  display: flex; flex-direction: column;
  background: var(--bg); border-left: 1px solid var(--border); box-shadow: var(--shadow-2);
  animation: view-in 0.18s ease both;
}
.hp-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 16px; border-bottom: 1px solid var(--border); flex-shrink: 0;
}
.hp-head h3 { font-size: var(--text-sm); font-weight: 700; text-transform: uppercase; letter-spacing: 0.12em; color: var(--muted); }
.hp-close {
  width: 30px; height: 30px; display: grid; place-items: center;
  font-size: var(--text-lg); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
}
.hp-close:hover { color: var(--text); }
.hp-search { padding: 10px 12px 6px; flex-shrink: 0; }
.hp-search input {
  width: 100%;
  font-family: var(--font-mono); font-size: var(--text-sm);
  background: var(--term-bg); color: var(--term-fg);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 8px 10px; outline: none;
}
.hp-search input:focus { border-color: rgba(96, 165, 250, 0.5); }
.hp-search input::-webkit-search-cancel-button { -webkit-appearance: none; appearance: none; }
.hp-filters { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; padding: 4px 12px 8px; flex-shrink: 0; }
.hp-chip {
  font-family: var(--font-mono); font-size: var(--text-2xs); text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--muted); background: var(--surface); border: 1px solid var(--border);
  border-radius: 999px; padding: 3px 10px;
}
.hp-chip:hover { color: var(--text); border-color: var(--border-strong); }
.hp-chip.on { color: var(--bg); background: var(--text-dim); border-color: var(--text-dim); font-weight: 700; }
.hp-select {
  font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--muted);
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 3px 6px; max-width: 130px;
}
.hp-scopes { padding-top: 0; }
.hp-chip.scope.on { background: var(--blue-soft); color: var(--blue); border-color: rgba(96, 165, 250, 0.5); }
.hp-list { flex: 1; min-height: 0; overflow-y: auto; margin: 0; padding: 0 8px 8px; list-style: none; }
.hp-empty { text-align: center; color: var(--faint); padding: 30px 12px; font-size: var(--text-sm); }
.hp-row { display: flex; align-items: stretch; gap: 4px; border-radius: var(--radius); }
.hp-row.active { background: var(--blue-soft); }
.hp-cmd {
  flex: 1; min-width: 0; text-align: left;
  display: flex; flex-direction: column; gap: 2px;
  padding: 8px 10px; background: transparent; border: none; color: var(--text);
}
.hp-cmd .text {
  font-family: var(--font-mono); font-size: var(--text-xs); line-height: 1.5;
  display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical;
  overflow: hidden; word-break: break-word; white-space: pre-wrap;
}
.hp-cmd .meta { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--faint); }
.hp-del {
  width: 30px; flex-shrink: 0; align-self: center; height: 30px;
  color: var(--faint); background: transparent; border: none; font-size: var(--text-md);
}
.hp-del:hover { color: var(--red); }
.hp-foot {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  padding: 8px 12px; border-top: 1px solid var(--border); flex-shrink: 0;
}
.hp-foot .hint { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--faint); }
.hp-clear {
  font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 4px 10px;
}
.hp-clear:hover:not(:disabled) { color: var(--text); }
.hp-clear.confirm { color: var(--red); border-color: rgba(248, 113, 113, 0.5); }
.hp-clear:disabled { opacity: 0.4; }
</style>
