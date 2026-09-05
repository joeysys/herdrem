<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useFilesStore } from '../../stores/files'
import { useConnectionStore } from '../../stores/connection'
import { useTerminalStore } from '../../stores/terminal'
import { useHaptic } from '../../composables/useHaptic'
import { safeStorage } from '../../utils/storage'
import type { FileEntry } from '../../api/types'

const files = useFilesStore()
const conn = useConnectionStore()
const term = useTerminalStore()
const { haptic } = useHaptic()
defineProps<{ docked: boolean }>()
const emit = defineEmits<{ close: []; previewOpened: []; dock: [] }>()

const listEl = ref<HTMLElement | null>(null)
const filterEl = ref<HTMLInputElement | null>(null)
const crumbsEl = ref<HTMLElement | null>(null)
const menuEntry = ref<FileEntry | null>(null)
const menuPos = ref({ left: 0, top: 0 })
const menuNotice = ref('')
const STAR_KEY = 'herdrem_file_stars'
const starred = ref(loadStars())

// Scroll the breadcrumb path bar to the right so the current directory (the
// last segment) is visible rather than the leading segments. Double rAF after
// nextTick ensures layout is complete before reading scrollWidth.
async function scrollCrumbsToRight() {
  await nextTick()
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      const el = crumbsEl.value
      if (el) el.scrollLeft = el.scrollWidth
    })
  })
}
watch(() => files.path, scrollCrumbsToRight, { immediate: true })

// --- sort / filter state ---
type SortKey = 'name' | 'mtime'
const sortKey = ref<SortKey>('mtime')
const sortDesc = ref(true) // newest first by default
const filterText = ref('')

// Auto-focus the filter input when the browser opens
onMounted(() => {
  nextTick(() => filterEl.value?.focus())
  document.addEventListener('click', closeEntryMenu)
})
onUnmounted(() => document.removeEventListener('click', closeEntryMenu))

function loadStars(): Set<string> {
  try {
    const saved = JSON.parse(safeStorage.getItem(STAR_KEY) || '[]')
    return new Set(Array.isArray(saved) ? saved.filter((v): v is string => typeof v === 'string') : [])
  } catch {
    return new Set()
  }
}

function toggleSort(key: SortKey) {
  if (sortKey.value === key) sortDesc.value = !sortDesc.value
  else { sortKey.value = key; sortDesc.value = false }
}

const sortedEntries = computed(() => {
  const list = files.entries.filter(e =>
    !filterText.value || e.name.toLowerCase().includes(filterText.value.toLowerCase())
  )
  const dirs = list.filter(e => e.type === 'dir')
  const others = list.filter(e => e.type !== 'dir')
  const cmp = (a: FileEntry, b: FileEntry) => {
    const starOrder = Number(isStarred(b)) - Number(isStarred(a))
    if (starOrder) return starOrder
    let r: number
    if (sortKey.value === 'mtime') r = (a.mtime || 0) - (b.mtime || 0)
    else r = a.name.localeCompare(b.name)
    return sortDesc.value ? -r : r
  }
  dirs.sort(cmp)
  others.sort(cmp)
  return [...dirs, ...others]
})

function fmtMtime(ts?: number): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  if (sameDay) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function fmtMtimeFull(ts?: number): string {
  if (!ts) return ''
  return new Date(ts * 1000).toLocaleString()
}

function fmtSize(bytes: number): string {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}K`
  return `${(bytes / 1024 / 1024).toFixed(1)}M`
}

function icon(type: string): string {
  if (type === 'dir') return '📁'
  if (type === 'link') return '🔗'
  return '📄'
}

function getRelativePath(e: FileEntry): string {
  const full = fullEntryPath(e)
  const root = files.root.replace(/\/+$/, '')
  let rel = full
  if (full === root) {
    rel = './'
  } else if (full.startsWith(root + '/')) {
    rel = full.slice(root.length + 1)
  }
  if (e.type === 'dir' && !rel.endsWith('/')) {
    rel += '/'
  }
  return rel
}

function fullEntryPath(e: FileEntry): string {
  return (files.path.replace(/\/+$/, '') + '/' + e.name).replace(/\/+/g, '/')
}

function starKey(e: FileEntry): string {
  return `${files.host}:${fullEntryPath(e)}`
}

function isStarred(e: FileEntry): boolean {
  return starred.value.has(starKey(e))
}

function toggleStar(e: FileEntry) {
  const key = starKey(e)
  if (starred.value.has(key)) starred.value.delete(key)
  else starred.value.add(key)
  safeStorage.setItem(STAR_KEY, JSON.stringify([...starred.value]))
}

function fillEntryPath(e: FileEntry) {
  haptic()
  const rel = getRelativePath(e)
  term.fillPath(rel)
}

const currentDirRelPath = computed(() => {
  const full = files.path.replace(/\/+$/, '')
  const root = files.root.replace(/\/+$/, '')
  if (full === root) return './'
  if (full.startsWith(root + '/')) return full.slice(root.length + 1) + '/'
  return full + '/'
})

function fillCurrentDir() {
  haptic()
  term.fillPath(currentDirRelPath.value)
}

function onEntry(name: string, type: string) {
  const full = (files.path.replace(/\/+$/, '') + '/' + name).replace(/\/+/g, '/')
  if (type === 'dir') files.navigate(full)
  else {
    files.openFile(full)
    emit('previewOpened')
  }
}

function toggleEntryMenu(e: MouseEvent, entry: FileEntry) {
  haptic()
  const button = e.currentTarget as HTMLElement
  if (menuEntry.value && starKey(menuEntry.value) === starKey(entry)) {
    closeEntryMenu()
    return
  }
  const rect = button.getBoundingClientRect()
  menuPos.value = {
    left: Math.max(8, Math.min(rect.right - 190, window.innerWidth - 198)),
    top: rect.bottom + 200 < window.innerHeight ? rect.bottom + 4 : Math.max(8, rect.top - 204),
  }
  menuEntry.value = entry
  menuNotice.value = ''
}

function closeEntryMenu() {
  menuEntry.value = null
  menuNotice.value = ''
}

async function copyEntryPath(e: FileEntry) {
  try {
    if (!navigator.clipboard) throw new Error('clipboard unavailable')
    await navigator.clipboard.writeText(getRelativePath(e))
    menuNotice.value = 'copied'
  } catch {
    menuNotice.value = 'copy failed'
  }
}

function getEntryDownloadUrl(e: FileEntry): string {
  const full = fullEntryPath(e)
  const base = conn.url.replace(/^ws/, 'http').replace(/\/ws$/, '').replace(/\/$/, '')
  const token = conn.token ? `&token=${encodeURIComponent(conn.token)}` : ''
  const host = files.host && files.host !== 'local' ? `&host=${encodeURIComponent(files.host)}` : ''
  return `${base}/file?path=${encodeURIComponent(full)}${host}${token}&download=1`
}

function downloadEntry(e: FileEntry) {
  haptic()
  const url = getEntryDownloadUrl(e)
  const a = document.createElement('a')
  a.href = url
  a.download = e.name
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  closeEntryMenu()
}

// Scroll to the target file in the tree when scrollToFile is set
watch(() => files.scrollToFile, async (target) => {
  if (!target || !listEl.value) return
  await nextTick()
  const entry = listEl.value.querySelector(`[data-name="${CSS.escape(target)}"]`) as HTMLElement | null
  if (entry) {
    entry.scrollIntoView({ block: 'center', behavior: 'smooth' })
    entry.classList.add('flash')
    setTimeout(() => entry.classList.remove('flash'), 1500)
  }
  files.clearScrollTarget()
})

// Also scroll when entries change while a target is pending
watch(() => files.entries, async () => {
  if (!files.scrollToFile) return
  await nextTick()
  if (!listEl.value) return
  const entry = listEl.value.querySelector(`[data-name="${CSS.escape(files.scrollToFile)}"]`) as HTMLElement | null
  if (entry) {
    entry.scrollIntoView({ block: 'center', behavior: 'smooth' })
    entry.classList.add('flash')
    setTimeout(() => entry.classList.remove('flash'), 1500)
  }
  files.clearScrollTarget()
}, { deep: true })
</script>

<template>
  <aside class="fb-drawer" :class="{ docked }" aria-label="File browser">
    <header class="fb-head">
      <button class="fb-btn" aria-label="Close" title="Close" @click="emit('close')">×</button>
      <nav ref="crumbsEl" class="fb-crumbs" aria-label="Directory path">
        <button class="crumb root" @click="files.navigate(files.root)">⌂</button>
        <template v-for="c in files.breadcrumbs" :key="c.path">
          <span class="sep">/</span>
          <button class="crumb" @click="files.navigate(c.path)">{{ c.name }}</button>
        </template>
      </nav>
      <button
        class="fb-btn crumb-fill"
        :title="`Fill folder path (${currentDirRelPath}) into terminal input`"
        aria-label="Fill folder path into terminal"
        @click="fillCurrentDir"
      >↙</button>
      <button
        class="fb-btn fb-dock"
        :aria-label="docked ? 'Float file browser' : 'Dock file browser'"
        :title="docked ? 'Float panel' : 'Dock to terminal'"
        @click="emit('dock')"
      >{{ docked ? '◧' : '▥' }}</button>
    </header>

    <div class="fb-body">
      <div ref="listEl" class="fb-list">
        <!-- Sort / filter toolbar (inside the tree) -->
        <div class="fb-toolbar">
          <input
            ref="filterEl"
            v-model="filterText"
            class="fb-filter"
            type="text"
            placeholder="filter…"
            aria-label="Filter files by name"
          />
          <button
            class="fb-sort"
            :class="{ active: sortKey === 'name' }"
            @click="toggleSort('name')"
            :title="'Sort by name' + (sortKey === 'name' ? (sortDesc ? ' ↓' : ' ↑') : '')"
          >Name{{ sortKey === 'name' ? (sortDesc ? ' ↓' : ' ↑') : '' }}</button>
          <button
            class="fb-sort"
            :class="{ active: sortKey === 'mtime' }"
            @click="toggleSort('mtime')"
            :title="'Sort by modified time' + (sortKey === 'mtime' ? (sortDesc ? ' ↓' : ' ↑') : '')"
          >Time{{ sortKey === 'mtime' ? (sortDesc ? ' ↓' : ' ↑') : '' }}</button>
        </div>

        <div v-if="files.loading" class="fb-loading">loading…</div>
        <div v-else-if="sortedEntries.length === 0 && !files.canGoUp" class="fb-empty">{{ filterText ? 'no matches' : 'empty directory' }}</div>
        <template v-else>
          <!-- Go up entry (only when not at root) -->
          <button
            v-if="files.canGoUp"
            class="fb-entry up"
            data-name=".."
            title="Go to parent directory"
            @click="files.goUp()"
          >
            <span class="fe-icon" aria-hidden="true">📁</span>
            <span class="fe-name">..</span>
          </button>
          <div
            v-for="e in sortedEntries"
            :key="e.name"
            class="fb-entry"
            :class="e.type"
            :data-name="e.name"
            :title="`${e.name}${e.mtime ? ' · ' + fmtMtimeFull(e.mtime) : ''}`"
            role="button"
            tabindex="0"
            @click="onEntry(e.name, e.type)"
            @keydown.enter="onEntry(e.name, e.type)"
          >
            <span class="fe-icon" aria-hidden="true">{{ icon(e.type) }}</span>
            <span class="fe-name">{{ e.name }}</span>
            <span v-if="isStarred(e)" class="fe-star" aria-label="Starred">★</span>
            <span class="fe-mtime">{{ fmtMtime(e.mtime) }}</span>
            <span class="fe-size">{{ e.type === 'file' ? fmtSize(e.size) : '' }}</span>
            <button
              class="fe-more-btn"
              title="More actions"
              :aria-label="`More actions for ${e.name}`"
              aria-haspopup="menu"
              :aria-expanded="menuEntry?.name === e.name"
              @click.stop="toggleEntryMenu($event, e)"
            >⋯</button>
          </div>
        </template>
      </div>

    </div>
  </aside>

  <Teleport to="body">
    <div v-if="menuEntry" class="fe-menu-backdrop" @click="closeEntryMenu">
      <div class="fe-menu" :style="{ left: `${menuPos.left}px`, top: `${menuPos.top}px` }" role="menu" @click.stop>
        <button class="fe-menu-item" role="menuitem" @click="onEntry(menuEntry.name, menuEntry.type); closeEntryMenu()">
          <span aria-hidden="true">{{ menuEntry.type === 'dir' ? '📁' : '↗' }}</span>{{ menuEntry.type === 'dir' ? 'open folder' : 'preview' }}
        </button>
        <button class="fe-menu-item" role="menuitem" @click="fillEntryPath(menuEntry); closeEntryMenu()">
          <span aria-hidden="true">↙</span>fill Terminal
        </button>
        <button class="fe-menu-item" role="menuitem" @click="toggleStar(menuEntry); closeEntryMenu()">
          <span aria-hidden="true">★</span>{{ isStarred(menuEntry) ? 'unstar' : 'star' }}
        </button>
        <button class="fe-menu-item" role="menuitem" @click="copyEntryPath(menuEntry)">
          <span aria-hidden="true">⧉</span>copy path
        </button>
        <button v-if="menuEntry.type === 'file'" class="fe-menu-item" role="menuitem" @click="downloadEntry(menuEntry)">
          <span aria-hidden="true">⤓</span>download
        </button>
        <span v-if="menuNotice" class="fe-menu-note" role="status">{{ menuNotice }}</span>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.fb-drawer {
  position: absolute;
  top: 0; right: 0; bottom: 0;
  width: min(360px, 100%);
  z-index: 70;
  display: flex; flex-direction: column;
  background: var(--bg);
  border-left: 1px solid var(--border);
  box-shadow: -8px 0 24px rgba(0,0,0,0.3);
  animation: drawer-in 0.2s ease both;
}
.fb-drawer.docked {
  position: relative;
  width: 360px;
  flex: 0 0 360px;
  z-index: 20;
  box-shadow: none;
  animation: none;
}
.fb-drawer:not(.docked) {
  width: 360px;
  animation: drawer-fade 0.2s ease both;
}
@keyframes drawer-fade {
  from { opacity: 0; }
  to { opacity: 1; }
}
@keyframes drawer-in {
  from { transform: translateX(100%); }
  to { transform: translateX(0); }
}
.fb-head {
  display: flex; align-items: center; gap: 4px;
  padding: 10px 12px; border-bottom: 1px solid var(--border); background: var(--surface); flex-shrink: 0;
}
.fb-btn {
  width: 30px; height: 30px; display: grid; place-items: center; flex-shrink: 0;
  font-size: var(--text-md); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
}
.fb-btn:hover:not(:disabled) { color: var(--text); }
.fb-btn:disabled { opacity: 0.35; }
.fb-crumbs {
  flex: 1; min-width: 0; display: flex; align-items: center; flex-wrap: nowrap; gap: 0;
  font-family: var(--font-mono); font-size: var(--text-xs); overflow-x: auto; overflow-y: hidden;
  white-space: nowrap; overscroll-behavior-x: contain; scrollbar-width: none;
}
.fb-crumbs::-webkit-scrollbar { display: none; }
.fb-spacer { flex: 1; }
.crumb {
  color: var(--blue); background: none; border: none;
  padding: 4px 6px; border-radius: 3px;
  display: inline-flex; align-items: center; line-height: 1; flex-shrink: 0;
}
.crumb:hover { background: var(--blue-soft); }
.crumb.root { color: var(--muted); }
.sep { color: var(--faint); display: inline-flex; align-items: center; padding: 0 1px; line-height: 1; flex-shrink: 0; }

.fb-body { flex: 1; min-height: 0; display: flex; }

/* Sort / filter toolbar — sticky inside the scrollable tree */
.fb-toolbar {
  display: flex; align-items: center; gap: 4px;
  padding: 6px 8px; border-bottom: 1px solid var(--border);
  background: var(--surface); flex-shrink: 0;
  position: sticky; top: 0; z-index: 2;
}
.fb-filter {
  flex: 1; min-width: 0;
  font-family: var(--font-mono); font-size: var(--text-xs);
  color: var(--text); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 4px 8px; outline: none;
}
.fb-filter:focus { border-color: var(--blue); }
.fb-filter::placeholder { color: var(--faint); }
.fb-sort {
  flex-shrink: 0; white-space: nowrap;
  font-family: var(--font-mono); font-size: var(--text-2xs);
  color: var(--muted); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 4px 8px; cursor: pointer; line-height: 1;
}
.fb-sort:hover { color: var(--text); }
.fb-sort.active { color: var(--blue); border-color: var(--blue); }

.fb-list {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 8px;
  background: var(--bg);
}

@media (max-width: 639px) {
  .fb-drawer,
  .fb-drawer.docked,
  .fb-drawer:not(.docked) {
    position: absolute;
    width: 100%;
    flex: none;
    z-index: 70;
  }
  .fb-list {
    flex: 1;
    width: 100%;
    max-width: 100%;
  }
  .fb-dock { display: none; }
}

.fb-loading, .fb-empty { text-align: center; color: var(--faint); padding: 40px; font-family: var(--font-mono); font-size: var(--text-xs); }
.fb-entry {
  display: flex; align-items: center; gap: 10px; width: 100%;
  text-align: left; background: transparent; border: none; border-radius: var(--radius);
  padding: 8px 10px; cursor: pointer;
  transition: background 0.12s;
}
.fb-entry:hover { background: var(--surface-2); }
.fb-entry.flash { background: var(--blue-soft); animation: flash-fade 1.5s ease both; }
@keyframes flash-fade {
  0% { background: var(--blue-soft); }
  100% { background: transparent; }
}
.fe-icon { width: 20px; text-align: center; }
.fe-name { flex: 1; min-width: 0; color: var(--text); font-size: var(--text-sm); font-family: var(--font-mono); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.fe-mtime { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--faint); flex-shrink: 0; }
.fe-size { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--faint); flex-shrink: 0; }
.fe-star { color: var(--amber); flex-shrink: 0; }

.crumb-fill {
  margin-left: 6px;
  color: var(--blue);
}
.fb-btn.crumb-fill:hover {
  color: var(--blue);
  border-color: var(--blue);
}

.fe-more-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--muted);
  background: var(--surface-3, var(--surface-2));
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 6px;
  cursor: pointer;
  line-height: 1.2;
  opacity: 0.65;
  transition: opacity 0.15s, color 0.15s, border-color 0.15s;
}
.fb-entry:hover .fe-more-btn,
.fe-more-btn:hover {
  opacity: 1;
  color: var(--blue);
  border-color: var(--blue);
}

.fe-menu-backdrop { position: fixed; inset: 0; z-index: 95; background: transparent; }
.fe-menu {
  position: fixed; min-width: 190px; padding: 5px;
  background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2); animation: view-in 0.14s ease both;
}
.fe-menu-item {
  display: flex; align-items: center; gap: 10px; width: 100%; padding: 9px 11px;
  color: var(--text-dim); background: transparent; border: 0; border-radius: var(--radius);
  font-size: var(--text-sm); text-align: left;
}
.fe-menu-item:hover { color: var(--text); background: var(--surface-3); }
.fe-menu-item span { width: 16px; text-align: center; color: var(--muted); }
.fe-menu-note { display: block; padding: 4px 11px; color: var(--muted); font-family: var(--font-mono); font-size: var(--text-2xs); }
@media (max-width: 639px) {
  .fe-menu { left: 12px !important; right: 12px; top: auto !important; bottom: 12px; }
}
</style>
