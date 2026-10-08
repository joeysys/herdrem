<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useDiffStore, type DiffViewMode } from '../../stores/diff';
import { useFilesStore } from '../../stores/files';
import { useConnectionStore } from '../../stores/connection';
import GitStatusView from './GitStatusView.vue';
import { useHaptic } from '../../composables/useHaptic';
import { highlight } from '../../utils/highlight';
import { parseNameStatus } from '../../utils/nameStatus';
import { parseGitLog } from '../../utils/gitLog';
import type { GitLogOptions } from '../../api/types';

const diff = useDiffStore();
const files = useFilesStore();
const conn = useConnectionStore();
const { haptic } = useHaptic();
const scrollEl = ref<HTMLElement | null>(null);

const state = computed(() => diff.state);
/** Which commit menu is open. commit = the single-commit tab; from/to = the range tab. */
const openPicker = ref<null | 'commit' | 'from' | 'to'>(null);

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

// `--name-status` view: the commit header (git show) and one row per file.
const nameStatusView = computed(() => {
  const s = state.value;
  if (!s || s.format !== 'name-status' || !s.content) return null;
  return parseNameStatus(s.content);
});
const canNameStatus = computed(
  () => conn.hasFeature('git-name-status') && state.value?.mode !== 'status' && state.value?.mode !== 'log',
);

// --- log tab ---
const LOG_SWITCHES: { key: keyof GitLogOptions; label: string; title: string }[] = [
  { key: 'oneline', label: 'oneline', title: '--oneline: one line per commit' },
  { key: 'graph', label: 'graph', title: '--graph: draw the branch lines' },
  { key: 'all', label: 'all', title: '--all: every branch and tag, not just HEAD' },
];
const logLines = computed(() => {
  const s = state.value;
  if (!s || s.mode !== 'log' || !s.content) return [];
  return parseGitLog(s.content, diff.logOptions);
});

function toggleLogOption(key: keyof GitLogOptions) {
  haptic();
  diff.setLogOption(key, !diff.logOptions[key]);
}

function openCommit(hash: string) {
  haptic();
  diff.showCommit(hash);
}

const NS_LABELS: Record<string, string> = {
  M: 'modified', A: 'added', D: 'deleted', R: 'renamed', C: 'copied', T: 'type changed', U: 'unmerged', X: 'unknown', B: 'broken pairing',
};
function nsLabel(status: string): string {
  if (status.length > 1) return `merge: ${status.split('').map((c) => NS_LABELS[c] ?? c).join(' / ')}`;
  return NS_LABELS[status] ?? status;
}

function toggleNameStatus() {
  haptic();
  diff.setNameStatus(!diff.nameStatus);
}

function showPatch(path: string) {
  haptic();
  diff.showPatchFor(path);
}

// A picker button shows only the short hash; the menu items still show
// hash + subject + author + date.
function slotHash(slot: 'commit' | 'from' | 'to'): string {
  const s = state.value;
  if (!s) return '';
  return slot === 'to' ? s.commit2 : s.commit;
}
function slotLabel(slot: 'commit' | 'from' | 'to'): string {
  const hash = slotHash(slot);
  if (hash) return hash.slice(0, 7);
  return slot === 'commit' ? 'pick commit…' : 'select…';
}
function slotTitle(slot: 'commit' | 'from' | 'to'): string {
  const hash = slotHash(slot);
  if (!hash) return slot === 'commit' ? 'Pick a commit' : `Pick the ${slot} commit`;
  const c = state.value?.commits.find((x) => x.hash === hash);
  if (c) return `${c.hash.slice(0, 7)} ${c.subject} — ${c.author} · ${c.date}`;
  return hash.slice(0, 7);
}
const rangePickers = [
  { id: 'from' as const, caption: 'from' },
  { id: 'to' as const, caption: 'to' },
];

function pickMode(mode: DiffViewMode) {
  haptic();
  openPicker.value = null;
  diff.setMode(mode);
}

function togglePicker(slot: 'commit' | 'from' | 'to') {
  openPicker.value = openPicker.value === slot ? null : slot;
}

function pickCommit(slot: 'commit' | 'from' | 'to', hash: string) {
  haptic();
  openPicker.value = null;
  if (slot === 'commit') diff.setCommit(hash);
  else diff.setRangeCommit(slot, hash);
}

function swapRange() {
  haptic();
  openPicker.value = null;
  diff.swapRange();
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
watch(openPicker, (open) => {
  if (!open) return;
  const close = () => {
    openPicker.value = null;
    document.removeEventListener('click', close);
  };
  setTimeout(() => document.addEventListener('click', close), 0);
});
</script>

<template>
  <section v-if="state" class="diff-preview" aria-label="Git diff preview">
    <header class="dp-head">
      <div class="dp-bar">
        <button class="dp-btn dp-close" aria-label="Close diff preview" title="Close" @click="diff.close()">×</button>
        <span class="dp-cwd mono" :title="state.cwd">⎇ {{ cwdLabel }}</span>
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
      </div>

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
        <button
          v-if="conn.hasFeature('git-range')"
          class="dp-mode"
          :class="{ active: state.mode === 'range' }"
          :aria-pressed="state.mode === 'range'"
          title="Diff two commits (git diff A B)"
          @click="pickMode('range')"
        >
          range
        </button>
        <button
          v-if="conn.hasFeature('git-log-view')"
          class="dp-mode"
          :class="{ active: state.mode === 'log' }"
          :aria-pressed="state.mode === 'log'"
          title="Commit history (git log)"
          @click="pickMode('log')"
        >
          log
        </button>
      </div>

      <div
        v-if="state.mode === 'log' || state.mode === 'commit' || state.mode === 'range' || canNameStatus"
        class="dp-tools"
      >
        <div v-if="state.mode === 'log'" class="dp-modes" role="group" aria-label="Log options">
          <button
            v-for="o in LOG_SWITCHES"
            :key="o.key"
            class="dp-mode"
            :class="{ active: diff.logOptions[o.key] }"
            :aria-pressed="diff.logOptions[o.key]"
            :title="o.title"
            @click="toggleLogOption(o.key)"
          >
            {{ o.label }}
          </button>
        </div>

        <div v-else-if="state.mode === 'commit'" class="dp-commit-wrap">
          <button
            class="dp-commit-btn"
            :aria-expanded="openPicker === 'commit'"
            aria-haspopup="listbox"
            :title="slotTitle('commit')"
            @click.stop="togglePicker('commit')"
          >
            <span class="dp-commit-label">{{ slotLabel('commit') }}</span>
            <span class="dp-caret" aria-hidden="true">▾</span>
          </button>
          <div v-if="openPicker === 'commit'" class="dp-commit-list" role="listbox" aria-label="Recent commits" @click.stop>
            <div v-if="state.loadingCommits" class="dp-commit-empty">loading…</div>
            <div v-else-if="state.commits.length === 0" class="dp-commit-empty">no commits</div>
            <button
              v-for="c in state.commits"
              :key="c.hash"
              class="dp-commit-item"
              role="option"
              :aria-selected="c.hash === state.commit"
              :class="{ selected: c.hash === state.commit }"
              @click="pickCommit('commit', c.hash)"
            >
              <span class="dp-ci-hash mono">{{ c.hash.slice(0, 7) }}</span>
              <span class="dp-ci-subject">{{ c.subject }}</span>
              <span class="dp-ci-meta mono">{{ c.author }} · {{ fmtCommitDate(c.date) }}</span>
            </button>
          </div>
        </div>

        <template v-else-if="state.mode === 'range'">
          <template v-for="(slot, i) in rangePickers" :key="slot.id">
            <button
              v-if="i === 1"
              type="button"
              class="dp-btn dp-swap"
              aria-label="Swap the two commits"
              title="Swap"
              @click="swapRange"
            >⇄</button>
            <div class="dp-commit-wrap">
              <button
                class="dp-commit-btn"
                :aria-expanded="openPicker === slot.id"
                aria-haspopup="listbox"
                :aria-label="`${slot.caption} commit`"
                :title="slotTitle(slot.id)"
                @click.stop="togglePicker(slot.id)"
              >
                <span class="dp-slot-name">{{ slot.caption }}</span>
                <span class="dp-commit-label">{{ slotLabel(slot.id) }}</span>
                <span class="dp-caret" aria-hidden="true">▾</span>
              </button>
              <div v-if="openPicker === slot.id" class="dp-commit-list" role="listbox" :aria-label="`${slot.caption} commit`" @click.stop>
                <div v-if="state.loadingCommits" class="dp-commit-empty">loading…</div>
                <div v-else-if="state.commits.length === 0" class="dp-commit-empty">no commits</div>
                <button
                  v-for="c in state.commits"
                  :key="c.hash"
                  class="dp-commit-item"
                  role="option"
                  :aria-selected="c.hash === slotHash(slot.id)"
                  :class="{ selected: c.hash === slotHash(slot.id) }"
                  @click="pickCommit(slot.id, c.hash)"
                >
                  <span class="dp-ci-hash mono">{{ c.hash.slice(0, 7) }}</span>
                  <span class="dp-ci-subject">{{ c.subject }}</span>
                  <span class="dp-ci-meta mono">{{ c.author }} · {{ fmtCommitDate(c.date) }}</span>
                </button>
              </div>
            </div>
          </template>
        </template>

        <button
          v-if="canNameStatus"
          class="dp-btn dp-ns-toggle"
          :class="{ active: diff.nameStatus }"
          :aria-pressed="diff.nameStatus"
          :title="diff.nameStatus ? 'Show the patch' : 'List changed files only (--name-status)'"
          @click="toggleNameStatus"
        >
          name-status
        </button>
      </div>
    </header>

    <div ref="scrollEl" class="dp-scroll">
      <GitStatusView v-if="state.mode === 'status'" />

      <div v-else-if="state.error" class="dp-error">
        <span class="dp-error-icon">⚠</span>
        <span class="dp-error-text">{{ state.error }}</span>
        <span class="dp-error-cwd mono">{{ state.cwd }}</span>
      </div>

      <div v-else-if="state.loading" class="dp-loading">loading diff…</div>

      <div v-else-if="state.mode === 'log'" class="dp-log">
        <div v-if="!logLines.length" class="dp-empty">no commits</div>
        <div v-else class="dp-log-text mono" role="region" aria-label="Commit history">
          <div v-for="(l, i) in logLines" :key="i" class="dp-log-line"><span v-if="l.graph" class="dp-log-graph">{{ l.graph }}</span><template v-if="l.hash"><span v-if="l.keyword" class="dp-log-kw">commit </span><button type="button" class="dp-log-hash" :title="`Show commit ${l.hash.slice(0, 7)}`" :aria-label="`Show commit ${l.hash.slice(0, 7)}`" @click="openCommit(l.hash)">{{ l.keyword ? l.hash : l.hash.slice(0, 7) }}</button><span v-if="l.decor" class="dp-log-decor">{{ l.decor }}</span></template>{{ l.text }}</div>
        </div>
        <p v-if="logLines.length" class="dp-log-foot">
          {{ state.logTruncated ? 'output cut at 1 MiB — ' : '' }}showing up to {{ state.logLimit }} commits
        </p>
      </div>

      <div v-else-if="!state.content" class="dp-empty">
        <span v-if="state.mode === 'commit' && !state.commit">pick a commit to show its diff</span>
        <span v-else-if="state.mode === 'range' && (!state.commit || !state.commit2)">pick two commits to compare</span>
        <span v-else>no changes</span>
      </div>

      <div v-else-if="nameStatusView" class="dp-ns">
        <pre v-if="nameStatusView.header" class="dp-ns-header mono">{{ nameStatusView.header }}</pre>
        <ul v-if="nameStatusView.entries.length" class="dp-ns-list" aria-label="Changed files">
          <li
            v-for="e in nameStatusView.entries"
            :key="e.status + (e.orig ?? '') + e.path"
            class="dp-ns-row"
            :data-path="e.path"
          >
            <span class="dp-ns-badge mono" :class="`s-${e.status[0]}`" :title="nsLabel(e.status)" :aria-label="nsLabel(e.status)">{{ e.status }}</span>
            <button type="button" class="dp-ns-path mono" :title="`Show diff of ${e.path}`" @click="showPatch(e.path)">
              {{ e.path }}<span v-if="e.orig" class="dp-ns-dim"> ← {{ e.orig }}</span>
            </button>
            <span v-if="e.score" class="dp-ns-dim mono" :title="`${e.score}% similar`">{{ +e.score }}%</span>
            <button
              v-if="e.status !== 'D'"
              type="button"
              class="dp-ns-open mono"
              :aria-label="`Open ${e.path}`"
              :title="`Open ${e.path}`"
              @click="openFilePath(e.path)"
            >open</button>
          </li>
        </ul>
        <div v-else class="dp-empty">no file changes</div>
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
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
  padding: 8px 12px 10px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
  flex-shrink: 0;
}
.dp-bar,
.dp-tools {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.dp-tools {
  flex-wrap: wrap;
}
.dp-tools > .dp-modes {
  flex: 1;
}
.dp-cwd {
  flex: 1;
  min-width: 0;
  max-width: none;
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
}

.dp-modes {
  display: flex;
  gap: 2px;
  min-width: 0;
  overflow-x: auto;
}
.dp-slot-name {
  color: var(--faint);
  flex-shrink: 0;
}
.dp-ns-toggle {
  margin-left: auto;
}
.dp-swap {
  padding-inline: 8px;
}
.dp-mode {
  flex-shrink: 0;
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

.dp-log { padding: 10px 0 16px; }
.dp-log-text {
  font-size: var(--text-xs);
  line-height: 1.55;
  color: var(--text);
  overflow-x: auto;
  padding: 0 12px;
}
.dp-log-line { white-space: pre; min-height: 1.55em; }
.dp-log-graph { color: var(--muted); }
.dp-log-kw { color: var(--orange); }
.dp-log-hash {
  font: inherit;
  color: var(--orange);
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
}
.dp-log-hash:hover { color: var(--blue); text-decoration: underline; }
.dp-log-decor { color: var(--green); }
.dp-log-foot {
  margin: 10px 12px 0;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--faint);
}

.dp-ns { padding: 10px 12px 16px; }
.dp-ns-header {
  margin: 0 0 10px;
  padding: 8px 10px;
  font-size: var(--text-2xs);
  color: var(--text-dim);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.dp-ns-list {
  list-style: none;
  margin: 0;
  padding: 4px 0;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.dp-ns-row { display: flex; align-items: center; gap: 8px; padding: 3px 10px; }
.dp-ns-row:hover { background: var(--surface-2); }
.dp-ns-badge {
  flex-shrink: 0; min-width: 18px; text-align: center; font-size: var(--text-2xs); font-weight: 700; color: var(--text-dim);
}
.dp-ns-badge.s-M, .dp-ns-badge.s-T { color: var(--orange); }
.dp-ns-badge.s-A { color: var(--green); }
.dp-ns-badge.s-D { color: var(--red); }
.dp-ns-badge.s-R, .dp-ns-badge.s-C { color: var(--blue); }
.dp-ns-badge.s-U { color: #fff; background: var(--red); border-radius: 3px; }
.dp-ns-path {
  flex: 1; min-width: 0; text-align: left; background: none; border: none; padding: 2px 0;
  font-size: var(--text-xs); color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer;
}
.dp-ns-path:hover { color: var(--blue); }
.dp-ns-dim { color: var(--faint); font-size: var(--text-2xs); flex-shrink: 0; }
.dp-ns-open {
  flex-shrink: 0; font-size: var(--text-2xs); color: var(--muted); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: var(--radius); padding: 2px 7px;
}
.dp-ns-open:hover { color: var(--text); }
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
  .dp-commit-btn {
    max-width: 160px;
  }
  .dp-commit-label {
    max-width: 120px;
  }
  .dp-commit-list {
    /* Span the screen instead of the narrow button. top must be reset:
       the base `top: calc(100% + 4px)` is a percentage of the viewport
       once position is fixed, which parks the whole menu below the screen. */
    position: fixed;
    top: auto;
    left: 8px;
    right: 8px;
    min-width: 0;
    max-width: none;
    max-height: min(60vh, calc(100dvh - 16px));
  }
}
</style>
