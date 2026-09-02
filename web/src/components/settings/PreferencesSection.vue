<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useTheme } from '../../composables/useTheme'
import { useLayoutMode } from '../../composables/useLayoutMode'
import { usePrefsStore } from '../../stores/prefs'
import { useAgentsStore } from '../../stores/connection'

const { theme, themes, setTheme } = useTheme()
const { preference, setPreference } = useLayoutMode()
const prefs = usePrefsStore()
const agents = useAgentsStore()

const props = defineProps<{ focusSection?: 'shell_cmds' | 'skills' | 'agent_cmds' | null }>()

// Theme grid: show first 2 rows by default, expandable for the rest.
// Column count is detected from the grid's actual width so it works on
// any screen size (3 cols on phone, 4+ on desktop).
// The selected theme is always sorted to the first position.
const themeExpanded = ref(false)
const themeGridEl = ref<HTMLDivElement | null>(null)
const colCount = ref(4)
const SWATCH_MIN = 86
const SWATCH_GAP = 8

function updateColCount() {
  const el = themeGridEl.value
  if (!el) return
  colCount.value = Math.max(1, Math.floor((el.clientWidth + SWATCH_GAP) / (SWATCH_MIN + SWATCH_GAP)))
}
let ro: ResizeObserver | null = null
onMounted(() => {
  updateColCount()
  if (themeGridEl.value && typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver(updateColCount)
    ro.observe(themeGridEl.value)
  }
})
onUnmounted(() => ro?.disconnect())

/** Themes reordered so the selected one is first. */
const orderedThemes = computed(() => {
  const sel = theme.value
  const idx = themes.findIndex((t) => t.id === sel)
  if (idx <= 0) return themes
  return [themes[idx], ...themes.slice(0, idx), ...themes.slice(idx + 1)]
})

const visibleCount = computed(() => colCount.value * 2)
const visibleThemes = computed(() =>
  themeExpanded.value ? orderedThemes.value : orderedThemes.value.slice(0, visibleCount.value),
)
const hasMoreThemes = computed(() => orderedThemes.value.length > visibleCount.value)

// FLIP animation: capture positions BEFORE the DOM reorders, then after
// the reorder use the Web Animations API to animate from old → new.
// WAAPI is used instead of CSS transitions so it works even under e-ink's
// `transition: none !important` rule.
function selectTheme(id: string) {
  const grid = themeGridEl.value
  // First: record current positions before Vue re-renders
  const first = new Map<HTMLElement, DOMRect>()
  if (grid) {
    for (const el of Array.from(grid.children) as HTMLElement[]) {
      first.set(el, el.getBoundingClientRect())
    }
  }
  setTheme(id)
  // Last + Invert + Play: after DOM updates, compute deltas and animate
  nextTick(() => {
    if (!grid) return
    for (const el of Array.from(grid.children) as HTMLElement[]) {
      const old = first.get(el)
      if (!old) continue
      const now = el.getBoundingClientRect()
      const dx = old.left - now.left
      const dy = old.top - now.top
      if (!dx && !dy) continue
      // Play: animate from old position to new position via WAAPI
      el.animate(
        [
          { transform: `translate(${dx}px, ${dy}px)` },
          { transform: 'translate(0, 0)' },
        ],
        { duration: 300, easing: 'cubic-bezier(0.4, 0, 0.2, 1)' },
      )
    }
  })
}

const shellEditEl = ref<HTMLTextAreaElement | null>(null)
const skillsEditEl = ref<HTMLTextAreaElement | null>(null)
const agentCmdEditEl = ref<HTMLTextAreaElement | null>(null)

// Scroll to and focus the relevant textarea when focusSection changes
watch(
  () => props.focusSection,
  (section) => {
    if (!section) return
    nextTick(() => {
      const el = section === 'shell_cmds' ? shellEditEl.value
        : section === 'agent_cmds' ? agentCmdEditEl.value
        : skillsEditEl.value
      if (el) {
        el.scrollIntoView({ block: 'center', behavior: 'smooth' })
        el.focus()
      }
    })
  },
  { immediate: true },
)

// --- shell commands ---
const shellText = ref(prefs.shellCmds.join('\n'))
function saveShellCmds() {
  const list = shellText.value.split('\n').map((s) => s.trim()).filter(Boolean).slice(0, 40)
  prefs.setShellCmds(list)
}

// --- agent commands ---
const agentCmdText = ref(prefs.agentCmds.join('\n'))
function saveAgentCmds() {
  const list = agentCmdText.value.split('\n').map((s) => s.trim()).filter(Boolean).slice(0, 40)
  prefs.setAgentCmds(list)
}

// --- skills ---
const skillsText = ref(prefs.skills.map((s) => `${s.label}|${s.template}`).join('\n'))
function saveSkills() {
  const list = skillsText.value
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .slice(0, 40)
    .map((l) => {
      const [label, ...rest] = l.split('|')
      return { label: label.trim(), template: (rest.join('|') || label).trim() }
    })
  prefs.setSkills(list)
}

// --- prefix by agent ---
const prefixText = ref(
  Object.entries(prefs.prefixByAgent)
    .map(([agent, prefix]) => `${agent}|${prefix}`)
    .join('\n'),
)
function savePrefixes() {
  const map: Record<string, string> = {}
  for (const line of prefixText.value.split('\n')) {
    const [agent, ...rest] = line.split('|')
    const a = agent.trim().toLowerCase()
    const p = (rest.join('|') || '').trim()
    if (a && p) map[a] = p
  }
  if (!map.default) map.default = '/'
  prefs.setPrefixByAgent(map)
}

// --- kind overrides ---
const overrideEntries = computed(() => Object.entries(prefs.kindOverrides))
const activePaneKeys = computed(
  () => new Set(agents.agents.map((a) => `${a.host || 'local'}:${a.pane_id}`)),
)
function isStale(key: string) {
  return !activePaneKeys.value.has(key)
}
function clearOverride(key: string) {
  const [host, ...rest] = key.split(':')
  // reconstruct pane_id (may contain colons)
  const paneId = rest.join(':')
  prefs.setSection('kind_overrides', Object.fromEntries(overrideEntries.value.filter(([k]) => k !== key)))
  void host
  void paneId
}
function clearStale() {
  const active = Object.fromEntries(overrideEntries.value.filter(([k]) => !isStale(k)))
  prefs.setSection('kind_overrides', active)
}
</script>

<template>
  <div class="pref-section">
    <section class="ps-block">
      <h3>theme</h3>
      <div ref="themeGridEl" class="theme-grid">
        <button
          v-for="t in visibleThemes"
          :key="t.id"
          class="theme-swatch"
          :class="{ on: theme === t.id }"
          :data-theme="t.preview || t.id"
          :aria-label="t.label"
          @click="selectTheme(t.id)"
        >
          <span class="sw-colors">
            <i class="c1"></i><i class="c2"></i><i class="c3"></i>
          </span>
          <span class="sw-name">{{ t.label }}</span>
        </button>
      </div>
      <button
        v-if="hasMoreThemes"
        class="theme-expand"
        :aria-expanded="themeExpanded"
        @click="themeExpanded = !themeExpanded"
      >{{ themeExpanded ? '▴' : '▾' }}</button>
    </section>

    <section class="ps-block">
      <h3>layout mode</h3>
      <p class="ps-hint">switch between mobile single-column and desktop split-screen mode</p>
      <div class="layout-toggle" role="group" aria-label="Layout mode">
        <button :class="{ on: preference === 'auto' }" @click="setPreference('auto')">Auto</button>
        <button :class="{ on: preference === 'mobile' }" @click="setPreference('mobile')">Mobile</button>
        <button :class="{ on: preference === 'desktop' }" @click="setPreference('desktop')">Desktop</button>
      </div>
    </section>

    <section class="ps-block">
      <h3>shell commands</h3>
      <p class="ps-hint">quick commands for shell panes, one per line</p>
      <textarea ref="shellEditEl" v-model="shellText" class="ps-edit" rows="5" placeholder="one per line"></textarea>
      <button class="ps-save" @click="saveShellCmds">save</button>
    </section>

    <section class="ps-block">
      <h3>agent commands</h3>
      <p class="ps-hint">slash commands for agent panes, one per line (prefix auto-added)</p>
      <textarea ref="agentCmdEditEl" v-model="agentCmdText" class="ps-edit" rows="5" placeholder="/review&#10;/commit&#10;/help"></textarea>
      <button class="ps-save" @click="saveAgentCmds">save</button>
    </section>

    <section class="ps-block">
      <h3>skills</h3>
      <p class="ps-hint">format: <code>label|template</code>, use <code>{prefix}</code> for agent prefix</p>
      <textarea ref="skillsEditEl" v-model="skillsText" class="ps-edit" rows="5" placeholder="Review|{prefix} review this code"></textarea>
      <button class="ps-save" @click="saveSkills">save</button>
    </section>

    <section class="ps-block">
      <h3>prefix by agent</h3>
      <p class="ps-hint">format: <code>agent|prefix</code>, <code>default</code> is the fallback</p>
      <textarea v-model="prefixText" class="ps-edit" rows="4" placeholder="codex|$&#10;claude|/&#10;default|/"></textarea>
      <button class="ps-save" @click="savePrefixes">save</button>
    </section>

    <section class="ps-block">
      <div class="ps-row">
        <h3>kind overrides</h3>
        <button v-if="overrideEntries.some(([k]) => isStale(k))" class="ps-clear" @click="clearStale">clear stale</button>
      </div>
      <div v-if="overrideEntries.length === 0" class="ps-empty">No overrides.</div>
      <div
        v-for="[key, ov] in overrideEntries"
        :key="key"
        class="ov-item"
        :class="{ stale: isStale(key) }"
      >
        <span class="ov-key mono">{{ key }}</span>
        <span class="ov-agent mono">{{ ov.display_agent }}</span>
        <button class="ov-clear" aria-label="Remove override" @click="clearOverride(key)">×</button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.mono { font-family: var(--font-mono); }
.pref-section { display: flex; flex-direction: column; gap: 22px; }
.ps-block h3 {
  font-size: var(--text-sm); font-weight: 700; text-transform: uppercase;
  letter-spacing: 0.12em; color: var(--muted); margin-bottom: 9px;
}
.ps-row { display: flex; align-items: center; justify-content: space-between; }
.ps-hint { font-size: var(--text-2xs); color: var(--faint); margin-bottom: 6px; }
.ps-hint code { background: var(--surface-2); border-radius: 3px; padding: 0 4px; }

.theme-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(86px, 1fr)); gap: 8px; }
.theme-swatch {
  display: flex; flex-direction: column; gap: 6px; align-items: center;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  padding: 9px 7px; cursor: pointer; transition: border-color 0.15s, transform 0.1s;
}
.theme-swatch:hover { transform: translateY(-1px); border-color: var(--border-strong); }
.theme-swatch.on { border-color: var(--green); box-shadow: 0 0 0 2px rgba(52,211,153,.25); }
.sw-colors { display: flex; gap: 3px; }
.sw-colors i { width: 14px; height: 14px; border-radius: 3px; display: block; }
.theme-swatch .c1 { background: var(--surface-2); border: 1px solid var(--border); }
.theme-swatch .c2 { background: var(--green); }
.theme-swatch .c3 { background: var(--blue); }
.sw-name { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--text-dim); }
.theme-expand {
  display: block; margin: 6px auto 0; padding: 4px 24px;
  font-size: var(--text-xs); color: var(--muted);
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
  cursor: pointer; transition: color 0.15s, border-color 0.15s;
}
.theme-expand:hover { color: var(--text); border-color: var(--border-strong); }

.ps-edit {
  width: 100%; font-family: var(--font-mono); font-size: var(--text-xs);
  background: var(--term-bg); border: 1px solid var(--border); border-radius: var(--radius);
  color: var(--term-fg); padding: 9px 11px; outline: none; resize: vertical;
}
.ps-edit:focus { border-color: rgba(96,165,250,.5); }
.ps-save {
  margin-top: 7px; font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  color: #06281a; background: var(--green); border: 1px solid var(--green); border-radius: var(--radius);
  padding: 6px 16px;
}
.ps-clear { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--amber); background: var(--amber-soft); border: 1px solid rgba(251,191,36,.4); border-radius: var(--radius); padding: 3px 10px; }
.ps-empty { color: var(--faint); font-size: var(--text-sm); padding: 8px 0; }

.ov-item {
  display: flex; align-items: center; gap: 10px;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 7px 11px; margin-bottom: 6px;
}
.ov-item.stale { opacity: 0.55; border-style: dashed; }
.ov-key { flex: 1; min-width: 0; font-size: var(--text-2xs); color: var(--text-dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.ov-agent { font-size: var(--text-2xs); color: var(--green); flex-shrink: 0; }
.ov-clear { color: var(--muted); background: none; border: none; font-size: var(--text-md); }
.ov-clear:hover { color: var(--red); }

.layout-toggle { display: inline-flex; background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius); padding: 2px; }
.layout-toggle button {
  font-family: var(--font-mono); font-size: var(--text-2xs); font-weight: 600;
  padding: 5px 16px; border: none; background: transparent; color: var(--text-dim);
  border-radius: var(--radius-sm); cursor: pointer; transition: all 0.15s;
}
.layout-toggle button.on { background: var(--surface); color: var(--green); box-shadow: 0 1px 2px rgba(0,0,0,0.15); }
</style>
