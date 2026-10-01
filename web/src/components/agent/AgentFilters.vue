<script setup lang="ts">
import { useAgentsStore } from '../../stores/connection'

const agents = useAgentsStore()

const KINDS = [
  { v: 'all', label: 'all' },
  { v: 'agent', label: 'agents' },
  { v: 'shell', label: 'shells' },
] as const

const STATUSES = [
  { v: 'blocked', label: 'blocked', cls: 'blocked' },
  { v: 'working', label: 'working', cls: 'working' },
  { v: 'done', label: 'done', cls: 'done' },
  { v: 'todo', label: 'todo', cls: 'todo' },
] as const
</script>

<template>
  <div class="filters">
    <!-- Status stats row (above search) — multi-select -->
    <div class="stats" role="group" aria-label="Status filter">
      <button
        v-for="s in STATUSES" :key="s.v"
        class="stat" :class="[s.cls, { on: agents.statusFilter.has(s.v) }]"
        :title="`Filter by ${s.label}`"
        @click="agents.toggleStatusFilter(s.v)"
      >
        <span class="n">{{ (agents.counts as any)[s.v] }}</span>
        <span class="l">{{ s.label }}</span>
      </button>
    </div>

    <!-- Search input -->
    <div class="search-row">
      <div class="search-box">
        <span class="search-glyph" aria-hidden="true">⌕</span>
        <input
          v-model="agents.search"
          class="search-input"
          type="search"
          placeholder="filter by project, agent, host…"
          aria-label="Filter agents"
        />
        <button v-if="agents.search" class="clear" aria-label="Clear search" title="Clear" @click="agents.search = ''">×</button>
      </div>
    </div>

    <!-- Kind chips + type select (below search) -->
    <div class="chip-rows">
      <div class="chip-row" role="group" aria-label="Kind filter">
        <button
          v-for="k in KINDS" :key="k.v"
          class="chip" :class="{ on: agents.kindFilter === k.v }"
          @click="agents.kindFilter = k.v"
        >{{ k.label }}</button>

        <select
          v-model="agents.typeFilter"
          class="type-select"
          aria-label="Agent type filter"
        >
          <option value="all">all types</option>
          <option v-for="t in agents.agentTypes" :key="t" :value="t">{{ t }}</option>
        </select>
      </div>
    </div>
  </div>
</template>

<style scoped>
.filters { padding: 12px 18px 0; flex-shrink: 0; }

/* Stats row — colored status filter buttons */
.stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 6px;
  margin-bottom: 9px;
}
.stat {
  display: flex; flex-direction: column; align-items: center;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 8px 4px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s, box-shadow 0.15s;
}
.stat:hover { border-color: var(--border-strong); }
.stat .n {
  font-size: var(--text-lg);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}
.stat .l {
  font-size: var(--text-2xs);
  font-weight: 600;
  color: var(--muted);
  margin-top: 3px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
/* Status colors */
.stat.blocked .n { color: var(--red); }
.stat.working .n { color: var(--orange); }
.stat.done .n { color: var(--green); }
.stat.todo .n { color: var(--blue); }
/* Active state — the heavier outline is an inset shadow, not a wider border,
   so selecting a filter never changes the button's box (no layout shift). */
.stat.blocked.on { border-color: var(--red); background: var(--red-soft); box-shadow: inset 0 0 0 1px var(--red); }
.stat.working.on { border-color: var(--orange); background: var(--orange-soft); box-shadow: inset 0 0 0 1px var(--orange); }
.stat.done.on { border-color: var(--green); background: var(--green-soft); box-shadow: inset 0 0 0 1px var(--green); }
.stat.todo.on { border-color: var(--blue); background: var(--blue-soft); box-shadow: inset 0 0 0 1px var(--blue); }

/* Search */
.search-row { margin-bottom: 9px; }
.search-box {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 7px 11px;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.search-box:focus-within {
  border-color: rgba(96, 165, 250, 0.5);
  box-shadow: 0 0 0 3px rgba(96, 165, 250, 0.1);
}
.search-glyph { color: var(--faint); font-size: var(--text-md); }
.search-input {
  flex: 1; min-width: 0;
  background: transparent; border: none; outline: none;
  color: var(--text);
  font-family: var(--font-display);
  font-size: var(--text-sm);
}
.search-input::-webkit-search-cancel-button { -webkit-appearance: none; appearance: none; }
.search-input::placeholder { color: var(--faint); }
.clear {
  color: var(--muted); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: 50%;
  width: 20px; height: 20px; line-height: 1; font-size: 14px;
  display: grid; place-items: center;
}
.clear:hover { color: var(--text); }

/* Kind chips */
.chip-rows { display: flex; flex-direction: column; gap: 7px; padding-bottom: 10px; }
.chip-row { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.chip {
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 4px 11px;
  transition: color 0.15s, background 0.15s, border-color 0.15s;
}
.chip:hover { color: var(--text); border-color: var(--border-strong); }
.chip.on { color: var(--bg); background: var(--text-dim); border-color: var(--text-dim); font-weight: 700; }

.type-select {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 4px 8px;
  max-width: 140px;
}
.type-select:focus { outline: none; border-color: rgba(96,165,250,.5); }
</style>
