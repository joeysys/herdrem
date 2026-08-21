<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useFilesStore } from '../../stores/files'
import { useTerminalStore } from '../../stores/terminal'
import { useHaptic } from '../../composables/useHaptic'
import FilePreview from './FilePreview.vue'
import type { FileEntry } from '../../api/types'

const files = useFilesStore()
const term = useTerminalStore()
const { haptic } = useHaptic()
const emit = defineEmits<{ close: [] }>()

const listEl = ref<HTMLElement | null>(null)
const filterEl = ref<HTMLInputElement | null>(null)

// --- sort / filter state ---
type SortKey = 'name' | 'mtime'
const sortKey = ref<SortKey>('mtime')
const sortDesc = ref(true) // newest first by default
const filterText = ref('')

// Auto-focus the filter input when the browser opens
onMounted(() => {
  nextTick(() => filterEl.value?.focus())
})

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
  const full = (files.path.replace(/\/+$/, '') + '/' + e.name).replace(/\/+/g, '/')
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
  else files.openFile(full)
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
  <div class="fb-drawer" :class="{ full: files.fullMax, 'tree-only': !files.preview }">
    <header class="fb-head">
      <button class="fb-btn" aria-label="Close" title="Close" @click="emit('close')">×</button>
      <nav class="fb-crumbs" aria-label="Breadcrumb">
        <button class="crumb root" @click="files.navigate(files.root)">⌂</button>
        <template v-for="c in files.breadcrumbs" :key="c.path">
          <span class="sep">/</span>
          <button class="crumb" @click="files.navigate(c.path)">{{ c.name }}</button>
        </template>
        <button
          class="crumb-fill"
          :title="`Fill folder path (${currentDirRelPath}) into terminal input`"
          aria-label="Fill folder path into terminal"
          @click="fillCurrentDir"
        >↙ term</button>
      </nav>
    </header>

    <div class="fb-body">
      <!-- File tree: side-by-side in fullMax, floating overlay otherwise -->
      <div v-if="!files.previewMax || !files.preview" class="fb-list" :class="{ overlay: !files.fullMax }" ref="listEl">
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
            <span class="fe-mtime">{{ fmtMtime(e.mtime) }}</span>
            <span class="fe-size">{{ e.type === 'file' ? fmtSize(e.size) : '' }}</span>
            <button
              class="fe-fill-btn"
              :title="`Fill ${e.type === 'dir' ? 'folder' : 'file'} path into terminal (${getRelativePath(e)})`"
              aria-label="Fill path into terminal"
              @click.stop="fillEntryPath(e)"
            >↙ term</button>
          </div>
        </template>
      </div>

      <FilePreview v-if="files.preview" />
    </div>
  </div>
</template>

<style scoped>
.fb-drawer {
  position: absolute;
  top: 0; right: 0; bottom: 0;
  width: min(960px, 100%);
  z-index: 70;
  display: flex; flex-direction: column;
  background: var(--bg);
  border-left: 1px solid var(--border);
  box-shadow: -8px 0 24px rgba(0,0,0,0.3);
  animation: drawer-in 0.2s ease both;
}
/* Split mode: cap at split-term width (subtract 340px sidebar) */
.main.split > .fb-drawer:not(.full) {
  width: min(960px, calc(100% - 340px));
}
/* Full mode: cover entire .main regardless of split */
.fb-drawer.full {
  width: 100%;
  z-index: 80;
}
/* Tree-only mode: shrink to tree width, use fade instead of slide to avoid
   the visual "page shifts left" effect when only the narrow tree appears. */
.fb-drawer.tree-only {
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
  flex: 1; min-width: 0; display: flex; align-items: center; flex-wrap: wrap; gap: 0;
  font-family: var(--font-mono); font-size: var(--text-xs); overflow: hidden;
}
.crumb {
  color: var(--blue); background: none; border: none;
  padding: 4px 6px; border-radius: 3px;
  display: inline-flex; align-items: center; line-height: 1;
}
.crumb:hover { background: var(--blue-soft); }
.crumb.root { color: var(--muted); }
.sep { color: var(--faint); display: inline-flex; align-items: center; padding: 0 1px; line-height: 1; }

.fb-body { flex: 1; min-height: 0; display: flex; position: relative; }

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

/* File tree: default side-by-side (fullMax mode) */
.fb-list {
  flex: 0 0 360px;
  overflow-y: auto;
  padding: 8px;
  border-right: 1px solid var(--border);
  background: var(--bg);
}

/* Non-fullMax mode: tree floats as overlay, content keeps full width */
.fb-list.overlay {
  position: absolute;
  top: 0; left: 0; bottom: 0;
  z-index: 5;
  width: 360px;
  max-width: 100%;
  box-shadow: 4px 0 12px rgba(0,0,0,0.25);
}

/* Mobile: overlay takes full width */
@media (max-width: 639px) {
  .fb-drawer.tree-only {
    width: 100%;
  }
  .fb-list,
  .fb-list.overlay {
    flex: 1;
    width: 100%;
    max-width: 100%;
  }
  .fb-list.overlay {
    z-index: 10;
  }
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

.crumb-fill {
  margin-left: 6px;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--blue);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 6px;
  cursor: pointer;
  line-height: 1;
}
.crumb-fill:hover {
  background: var(--surface-3, var(--surface));
  border-color: var(--blue);
}

.fe-fill-btn {
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
.fb-entry:hover .fe-fill-btn,
.fe-fill-btn:hover {
  opacity: 1;
  color: var(--blue);
  border-color: var(--blue);
}
</style>
