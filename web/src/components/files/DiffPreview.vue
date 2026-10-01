<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useDiffStore, type DiffViewMode } from '../../stores/diff';
import { useFilesStore } from '../../stores/files';
import { useConnectionStore } from '../../stores/connection';
import GitStatusView from './GitStatusView.vue';
import { useHaptic } from '../../composables/useHaptic';
import { highlight } from '../../utils/highlight';

const diff = useDiffStore();
const files = useFilesStore();
const conn = useConnectionStore();
const { haptic } = useHaptic();
const scrollEl = ref<HTMLElement | null>(null);

const state = computed(() => diff.state);
const showCommitList = ref(false);

const cwdLabel = computed(() => {
  const cwd = state.value?.cwd || '';
  // Show the basename + parent for brevity, like a repo name.
  const parts = cwd.replace(/\/+$/, '').split('/');
  if (parts.length <= 2) return cwd || '/';
  return '…/' + parts.slice(-2).join('/');
});

// Per-line diff marker (+ / - / space) for row tinting.
function lineMarker(html: string): '' | 'add' | 'del' | 'hunk' | 'meta' {
  // The rendered HTML may start with re-opened spans; the actual diff char is
  // the first *text* character. Strip leading tags to inspect it.
  const text = html.replace(/^<[^>]+>+/, '');
  if (text.startsWith('+')) return 'add';
  if (text.startsWith('-')) return 'del';
  if (text.startsWith('@@')) return 'hunk';
  if (
    text.startsWith('diff ') ||
    text.startsWith('index ') ||
    /^commit /.test(text) ||
    text.startsWith('---') ||
    text.startsWith('+++')
  )
    return 'meta';
  return '';
}

interface DiffLine {
  html: string;
  marker: '' | 'add' | 'del' | 'hunk' | 'meta';
  fileLine: number | null; // actual line number in the file (from hunk header)
}

interface DiffSection {
  path: string; // file path; '' for the commit header section (git show)
  isHeader: boolean; // true = commit metadata before the first diff --git
  lines: DiffLine[];
  maxLineNum: number; // largest line number in this file (for auto-width)
}

// Extract the file path from a `diff --git a/foo b/bar` line.
function extractPath(diffLine: string): string {
  const m = diffLine.match(/^diff --git a\/(.*) b\/(.*)$/);
  if (m) return m[2] || m[1];
  return diffLine.replace(/^diff --git /, '');
}

// Parse a hunk header `@@ -oldStart,oldLen +newStart,newLen @@` and return
// the starting old/new line numbers. Length defaults to 1 when omitted.
function parseHunkHeader(line: string): { oldLine: number; newLine: number } | null {
  const m = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
  if (!m) return null;
  return { oldLine: parseInt(m[1], 10), newLine: parseInt(m[2], 10) };
}

// Parse the diff into per-file sections so each file gets its own visually
// distinct block with a sticky header. `git show` output has a commit-
// metadata block before the first `diff --git`; that becomes an isHeader
// section with no file header.
//
// Line numbers are the actual file line numbers derived from the hunk
// headers (`@@ -a,b +c,d @@`), not the global diff output line number:
//   - context ` ` and added `+` lines → new file line number
//   - deleted `-` lines → old file line number
const sections = computed<DiffSection[]>(() => {
  const s = state.value;
  if (!s || !s.content) return [];
  const rawLines = s.content.split('\n');
  const htmlLines = highlight(s.content, 'diff');
  const result: DiffSection[] = [];
  let current: DiffSection | null = null;
  let oldLine = 0;
  let newLine = 0;
  let maxLineNum = 0;
  for (let i = 0; i < rawLines.length; i++) {
    const raw = rawLines[i];
    if (raw.startsWith('diff --git ')) {
      if (current) {
        current.maxLineNum = maxLineNum;
        result.push(current);
      }
      current = { path: extractPath(raw), isHeader: false, lines: [], maxLineNum: 0 };
      oldLine = 0;
      newLine = 0;
      maxLineNum = 0;
    } else if (!current) {
      current = { path: '', isHeader: true, lines: [], maxLineNum: 0 };
    }
    const html = htmlLines[i] || '';
    const marker = lineMarker(html);
    let fileLine: number | null = null;
    if (marker === 'hunk') {
      const h = parseHunkHeader(raw);
      if (h) {
        oldLine = h.oldLine;
        newLine = h.newLine;
      }
    } else if (marker === 'add') {
      fileLine = newLine++;
    } else if (marker === 'del') {
      fileLine = oldLine++;
    } else if (marker === '') {
      // context line — both counters advance; show the new (current) line
      fileLine = newLine;
      oldLine++;
      newLine++;
    }
    if (fileLine && fileLine > maxLineNum) maxLineNum = fileLine;
    current.lines.push({ html, marker, fileLine });
  }
  if (current) {
    current.maxLineNum = maxLineNum;
    result.push(current);
  }
  return result;
});

// The commit dropdown button shows only the short hash; the dropdown items
// still show hash + subject + author + date.
const selectedCommitLabel = computed(() => {
  const s = state.value;
  if (!s || s.mode !== 'commit' || !s.commit) return '';
  return s.commit.slice(0, 7);
});

const selectedCommitTitle = computed(() => {
  const s = state.value;
  if (!s || s.mode !== 'commit' || !s.commit) return '';
  const c = s.commits.find((x) => x.hash === s.commit);
  if (c) return `${c.hash.slice(0, 7)} ${c.subject} — ${c.author} · ${c.date}`;
  return s.commit.slice(0, 7);
});

function pickMode(mode: DiffViewMode) {
  haptic();
  showCommitList.value = false;
  diff.setMode(mode);
}

function pickCommit(hash: string) {
  haptic();
  showCommitList.value = false;
  diff.setCommit(hash);
}

function refresh() {
  haptic();
  diff.refresh();
}

function fmtCommitDate(d: string): string {
  return d;
}

/** Compute the line-number column width for a file based on its max line
 *  number. Each digit ≈ 7.2px (mono 11.5px) + 24px padding. Min 28px so
 *  single-digit files still have breathing room. */
function lnWidth(maxLineNum: number): string {
  if (!maxLineNum || maxLineNum < 1) return '28px';
  const digits = String(maxLineNum).length;
  return `${Math.max(28, digits * 7.2 + 24)}px`;
}

/** Open the file in the file preview panel. The diff path is relative to
 *  the diff's cwd, so we join them into a full path first. */
function openFilePath(path: string) {
  const s = state.value;
  if (!s || !path) return;
  haptic();
  const full = path.startsWith('/') ? path : (s.cwd.replace(/\/+$/, '') + '/' + path).replace(/\/+/g, '/');
  files.openPath(full, s.cwd || '/', s.host || 'local', null);
}

// Opened from the status list: bring that file's section into view once the
// diff has rendered.
watch(
  () => [diff.focusPath, state.value?.content, state.value?.loading] as const,
  async ([path, content, loading]) => {
    if (!path || loading || !content) return;
    await nextTick();
    const el = scrollEl.value?.querySelector<HTMLElement>(`[data-path="${CSS.escape(path)}"]`);
    if (el) {
      el.scrollIntoView?.({ block: 'start' });
      el.classList.add('flash');
      setTimeout(() => el.classList.remove('flash'), 1500);
    }
    diff.clearFocus();
  },
);

// Close the commit dropdown on outside click.
watch(showCommitList, (open) => {
  if (!open) return;
  const close = () => {
    showCommitList.value = false;
    document.removeEventListener('click', close);
  };
  setTimeout(() => document.addEventListener('click', close), 0);
});
</script>

<template>
  <section v-if="state" class="diff-preview" aria-label="Git diff preview">
    <header class="dp-head">
      <button class="dp-btn dp-close" aria-label="Close diff preview" title="Close" @click="diff.close()">×</button>
      <span class="dp-cwd mono" :title="state.cwd">⎇ {{ cwdLabel }}</span>

      <div class="dp-modes" role="group" aria-label="Diff mode">
        <button
          v-if="conn.hasFeature('git-commit')"
          class="dp-mode"
          :class="{ active: state.mode === 'status' }"
          :aria-pressed="state.mode === 'status'"
          title="Status: stage files and commit (git status)"
          @click="pickMode('status')"
        >
          status
        </button>
        <button
          class="dp-mode"
          :class="{ active: state.mode === 'unstaged' }"
          :aria-pressed="state.mode === 'unstaged'"
          title="Unstaged changes (git diff)"
          @click="pickMode('unstaged')"
        >
          unstaged
        </button>
        <button
          class="dp-mode"
          :class="{ active: state.mode === 'cached' }"
          :aria-pressed="state.mode === 'cached'"
          title="Staged changes (git diff --cached)"
          @click="pickMode('cached')"
        >
          cached
        </button>
        <button
          class="dp-mode"
          :class="{ active: state.mode === 'commit' }"
          :aria-pressed="state.mode === 'commit'"
          title="Show a commit (git show)"
          @click="pickMode('commit')"
        >
          commit
        </button>
      </div>

      <div v-if="state.mode === 'commit'" class="dp-commit-wrap">
        <button
          class="dp-commit-btn"
          :aria-expanded="showCommitList"
          aria-haspopup="listbox"
          :title="selectedCommitTitle || 'Pick a commit'"
          @click.stop="showCommitList = !showCommitList"
        >
          <span class="dp-commit-label">{{ selectedCommitLabel || 'pick commit…' }}</span>
          <span class="dp-caret" aria-hidden="true">▾</span>
        </button>
        <div v-if="showCommitList" class="dp-commit-list" role="listbox" aria-label="Recent commits" @click.stop>
          <div v-if="state.loadingCommits" class="dp-commit-empty">loading…</div>
          <div v-else-if="state.commits.length === 0" class="dp-commit-empty">no commits</div>
          <button
            v-for="c in state.commits"
            :key="c.hash"
            class="dp-commit-item"
            role="option"
            :aria-selected="c.hash === state.commit"
            :class="{ selected: c.hash === state.commit }"
            @click="pickCommit(c.hash)"
          >
            <span class="dp-ci-hash mono">{{ c.hash.slice(0, 7) }}</span>
            <span class="dp-ci-subject">{{ c.subject }}</span>
            <span class="dp-ci-meta mono">{{ c.author }} · {{ fmtCommitDate(c.date) }}</span>
          </button>
        </div>
      </div>

      <button
        class="dp-btn"
        :class="{ active: diff.halfScreen }"
        :aria-label="diff.halfScreen ? 'Expand to full screen' : 'Switch to half screen'"
        :title="diff.halfScreen ? 'Full screen' : 'Half screen'"
        :aria-pressed="diff.halfScreen"
        @click="diff.toggleHalfScreen()"
      >
        ⇕
      </button>
      <button class="dp-btn" aria-label="Refresh diff" title="Refresh" @click="refresh">⟳</button>
    </header>

    <div ref="scrollEl" class="dp-scroll">
      <GitStatusView v-if="state.mode === 'status'" />

      <div v-else-if="state.error" class="dp-error">
        <span class="dp-error-icon">⚠</span>
        <span class="dp-error-text">{{ state.error }}</span>
        <span class="dp-error-cwd mono">{{ state.cwd }}</span>
      </div>

      <div v-else-if="state.loading" class="dp-loading">loading diff…</div>

      <div v-else-if="!state.content" class="dp-empty">
        <span v-if="state.mode === 'commit' && !state.commit">pick a commit to show its diff</span>
        <span v-else>no changes</span>
      </div>

      <div v-else class="dp-files">
        <div v-for="(sec, si) in sections" :key="si" class="dp-file" :data-path="sec.isHeader ? undefined : sec.path">
          <button v-if="!sec.isHeader" class="dp-file-head" :title="`Open ${sec.path}`" @click="openFilePath(sec.path)">
            <span class="dp-file-icon" aria-hidden="true">📄</span>
            <span class="dp-file-path mono">{{ sec.path }}</span>
          </button>
          <div class="dp-code-scroll">
            <table class="dp-code" :class="{ 'no-ln': sec.isHeader }">
              <colgroup>
                <col v-if="!sec.isHeader" :style="{ width: lnWidth(sec.maxLineNum) }" />
                <col />
              </colgroup>
              <tbody>
                <tr v-for="(line, li) in sec.lines" :key="li" :class="line.marker">
                  <td v-if="!sec.isHeader" class="ln">{{ line.fileLine ? line.fileLine : '' }}</td>
                  <td class="lc" v-html="line.html || '&nbsp;'"></td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.mono {
  font-family: var(--font-mono);
}
.diff-preview {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.dp-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
  flex-shrink: 0;
  flex-wrap: wrap;
}
.dp-btn {
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--muted);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 4px 9px;
  flex-shrink: 0;
}
.dp-btn:hover {
  color: var(--text);
}
.dp-btn.active {
  color: var(--blue);
  border-color: var(--blue);
  background: var(--blue-soft);
}
.dp-close {
  width: 30px;
  height: 30px;
  padding: 0;
  font-size: var(--text-md);
}
.dp-cwd {
  font-size: var(--text-xs);
  color: var(--text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 220px;
  flex-shrink: 0;
}

.dp-modes {
  display: inline-flex;
  gap: 2px;
  flex-shrink: 0;
}
.dp-mode {
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--muted);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 4px 9px;
  cursor: pointer;
  line-height: 1;
}
.dp-mode:hover {
  color: var(--text);
}
.dp-mode.active {
  color: var(--blue);
  border-color: var(--blue);
  background: var(--blue-soft);
}

.dp-commit-wrap {
  position: relative;
  flex-shrink: 0;
  min-width: 0;
}
.dp-commit-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--text-dim);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 4px 9px;
  cursor: pointer;
  max-width: 280px;
}
.dp-commit-btn:hover {
  color: var(--text);
  border-color: var(--blue);
}
.dp-commit-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 240px;
}
.dp-caret {
  color: var(--muted);
  flex-shrink: 0;
}

.dp-commit-list {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  min-width: 320px;
  max-width: 460px;
  max-height: 60vh;
  overflow-y: auto;
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  z-index: 10;
  padding: 4px;
  animation: view-in 0.14s ease both;
}
.dp-commit-empty {
  padding: 12px;
  color: var(--faint);
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
}
.dp-commit-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: var(--radius);
  padding: 7px 10px;
  cursor: pointer;
}
.dp-commit-item:hover {
  background: var(--surface-3);
}
.dp-commit-item.selected {
  background: var(--blue-soft);
}
.dp-ci-hash {
  color: var(--blue);
  font-size: var(--text-2xs);
}
.dp-ci-subject {
  color: var(--text);
  font-size: var(--text-xs);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.dp-ci-meta {
  color: var(--faint);
  font-size: var(--text-2xs);
}

.dp-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--term-bg);
}
.dp-loading,
.dp-empty {
  padding: 40px;
  text-align: center;
  color: var(--faint);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}
.dp-error {
  padding: 40px 24px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.dp-error-icon {
  font-size: 28px;
  color: var(--orange);
}
.dp-error-text {
  color: var(--muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.dp-error-cwd {
  color: var(--faint);
  font-size: var(--text-2xs);
  word-break: break-all;
  max-width: 100%;
}

/* Per-file sections: each file is a visually distinct block with its own
   border and spacing. The file header is sticky (sticks to the top of the
   scroll container as you scroll through that file's content) and is
   pushed away when the next file's header reaches the top.
   Horizontal scrolling is per-file: each .dp-code-scroll handles its own
   overflow-x, so one long file doesn't force the whole panel to scroll
   sideways. The file header stays fixed-width (not scrolled).
   NOTE: .dp-file must NOT use overflow:hidden — that would create a scroll
   container and break the sticky header (it would stick within the file
   block instead of the outer .dp-scroll). The rounded corners are applied
   to the header instead. */
.dp-file.flash { box-shadow: inset 3px 0 0 var(--blue); transition: box-shadow 0.3s; }
.dp-files {
  min-width: 100%;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.dp-file {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--term-bg);
}
.dp-file-head {
  position: sticky;
  top: 0;
  z-index: 3;
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 14px;
  background: var(--surface-2);
  border: none;
  border-bottom: 2px solid var(--border-strong);
  border-radius: var(--radius) var(--radius) 0 0;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--text);
  text-align: left;
  cursor: pointer;
  /* Show the full path — wrap instead of truncating with ellipsis. */
  white-space: normal;
  word-break: break-all;
  line-height: 1.4;
  transition:
    background 0.12s,
    color 0.12s;
}
.dp-file-head:hover {
  background: var(--surface-3);
  color: var(--blue);
}
.dp-file-icon {
  flex-shrink: 0;
  font-size: var(--text-md);
}
.dp-file-path {
  min-width: 0;
}

/* Per-file horizontal scroll container. overflow-x: auto lets long lines
   scroll within this file only; the file header above is not affected. */
.dp-code-scroll {
  overflow-x: auto;
  overflow-y: hidden;
}

.dp-code {
  width: 100%;
  min-width: max-content;
  border-collapse: collapse;
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.6;
}
.dp-code td {
  padding: 0;
  vertical-align: top;
}
.dp-code .ln {
  padding: 0 10px 0 14px;
  text-align: right;
  color: var(--faint);
  user-select: none;
  background: var(--term-bg);
  position: sticky;
  left: 0;
  z-index: 1;
}
/* No wrapping — long lines scroll horizontally instead of wrapping. */
.dp-code .lc {
  padding: 0 14px 0 12px;
  white-space: pre;
  color: var(--term-fg);
}

/* diff row tints */
.dp-code tr.add .lc {
  background: rgba(46, 160, 67, 0.14);
}
.dp-code tr.add .ln {
  color: #3fb950;
}
.dp-code tr.del .lc {
  background: rgba(248, 81, 73, 0.14);
}
.dp-code tr.del .ln {
  color: #f85149;
}
.dp-code tr.hunk .lc {
  color: var(--blue);
  background: var(--blue-soft);
}
.dp-code tr.hunk .ln {
  color: var(--blue);
}
.dp-code tr.meta .lc {
  color: var(--muted);
  font-weight: 600;
}

/* syntax tokens (shared with FilePreview) */
:deep(.hljs) {
  color: var(--term-fg);
}
:deep(.hljs-comment),
:deep(.hljs-quote) {
  color: #6a9955;
  font-style: italic;
}
:deep(.hljs-keyword),
:deep(.hljs-literal),
:deep(.hljs-section) {
  color: #c586c0;
}
:deep(.hljs-string),
:deep(.hljs-attr),
:deep(.hljs-addition) {
  color: #7ec699;
}
:deep(.hljs-number),
:deep(.hljs-meta) {
  color: #b5cea8;
}
:deep(.hljs-title),
:deep(.hljs-name) {
  color: #569cd6;
}
:deep(.hljs-deletion) {
  color: #d16969;
}

@media (max-width: 639px) {
  .dp-cwd {
    max-width: 120px;
  }
  .dp-commit-btn {
    max-width: 160px;
  }
  .dp-commit-label {
    max-width: 120px;
  }
  .dp-commit-list {
    /* On mobile, position fixed relative to the viewport so the dropdown
       spans the full screen width instead of being constrained by the
       narrow .dp-commit-wrap parent. */
    position: fixed;
    left: 8px;
    right: 8px;
    min-width: 0;
    max-width: none;
  }
}
</style>
