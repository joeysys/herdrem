<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useDiffStore } from '../../stores/diff';
import { useFilesStore } from '../../stores/files';
import { usePrefsStore } from '../../stores/prefs';
import { useConnectionStore } from '../../stores/connection';
import { useHaptic } from '../../composables/useHaptic';
import type { GitStatusEntry } from '../../api/types';

// git status for the diff panel's folder: pick unstaged files (or all) and
// `git add` them, unstage, and commit — with a typed message, or by running
// the commit command configured in prefs (on the relay, in this folder).
const diff = useDiffStore();
const files = useFilesStore();
const prefs = usePrefsStore();
const conn = useConnectionStore();
const { haptic } = useHaptic();

const st = computed(() => diff.status);
const cwd = computed(() => diff.state?.cwd || '');
const host = computed(() => diff.state?.host || 'local');

// Everything that can be staged: tracked changes, new files, conflicts.
const changes = computed<GitStatusEntry[]>(() => {
  const s = st.value;
  if (!s) return [];
  return [...s.conflicted, ...s.unstaged, ...s.untracked];
});
const staged = computed<GitStatusEntry[]>(() => st.value?.staged ?? []);
const clean = computed(() => !!st.value && !changes.value.length && !staged.value.length);

// --- selection (paths of `changes`) ---
const selected = ref<Set<string>>(new Set());
watch(changes, (list) => {
  // Keep only selections that still exist after a refresh.
  const live = new Set(list.map((e) => e.path));
  selected.value = new Set([...selected.value].filter((p) => live.has(p)));
});
const allSelected = computed(() => changes.value.length > 0 && selected.value.size === uniquePaths(changes.value).length);
const someSelected = computed(() => selected.value.size > 0 && !allSelected.value);

function uniquePaths(list: GitStatusEntry[]): string[] {
  return [...new Set(list.map((e) => e.path))];
}
function toggle(path: string) {
  const next = new Set(selected.value);
  if (next.has(path)) next.delete(path);
  else next.add(path);
  selected.value = next;
}
function toggleAll() {
  selected.value = allSelected.value ? new Set() : new Set(uniquePaths(changes.value));
}

function stageSelected() {
  haptic();
  diff.stage([...selected.value]);
  selected.value = new Set();
}
function stageOne(path: string) {
  haptic();
  diff.stage([path]);
}
function stageAll() {
  haptic();
  diff.stageAll();
  selected.value = new Set();
}
function unstageOne(path: string) {
  haptic();
  diff.stage([path], true);
}
function unstageAll() {
  haptic();
  diff.stageAll(true);
}

const canSync = computed(() => conn.hasFeature('git-sync'));
const busy = computed(() => !!diff.syncing || diff.committing || diff.staging);
const canPush = computed(() => {
  const s = st.value;
  if (!s || !canSync.value || busy.value) return false;
  if (s.detached || s.initial || !s.upstream) return false;
  return s.ahead > 0 && s.behind === 0;
});
const canPull = computed(() => {
  const s = st.value;
  if (!s || !canSync.value || busy.value) return false;
  if (s.detached || s.initial || !s.upstream) return false;
  if (s.ahead !== 0 || s.behind < 1) return false;
  return clean.value && !s.staged_outside;
});
const canFetch = computed(() => canSync.value && !busy.value);
function canDiscard(e: GitStatusEntry): boolean {
  return canSync.value && e.status !== '?' && e.status.length === 1;
}

function doPush() {
  const s = st.value;
  if (!s || !canPush.value) return;
  const n = s.ahead;
  const ok = window.confirm(
    `Push ${n} commit${n === 1 ? '' : 's'} to ${s.upstream}?\n\nThis pushes the whole branch, not just this folder.`,
  );
  if (!ok) return;
  haptic(18);
  diff.sync('push');
}
function doFetch() {
  if (!canFetch.value) return;
  haptic();
  diff.sync('fetch');
}
function doPull() {
  const s = st.value;
  if (!s || !canPull.value) return;
  const n = s.behind;
  const ok = window.confirm(
    `Pull ${n} commit${n === 1 ? '' : 's'} from ${s.upstream}?\n\nFast-forward only. This updates the whole branch, not just this folder.`,
  );
  if (!ok) return;
  haptic(18);
  diff.sync('pull');
}
function discardOne(path: string) {
  if (busy.value) return;
  const ok = window.confirm(`Discard unstaged changes in ${path}?\n\nThis cannot be undone.`);
  if (!ok) return;
  haptic(18);
  diff.discard(path);
}

const syncText = computed(() => {
  const o = diff.syncOutcome;
  if (!o) return '';
  if (!o.ok) return `✗ ${o.error || `${o.action} failed`}`;
  return `✓ ${o.action}`;
});

// Click a file: its diff (staged → cached view); a new file has no diff yet,
// so preview it instead (or browse it, for an untracked folder).
function openEntry(e: GitStatusEntry, isStaged: boolean) {
  haptic();
  if (e.status === '?') {
    const full = `${cwd.value.replace(/\/+$/, '')}/${e.path}`.replace(/\/+/g, '/');
    if (e.path.endsWith('/')) files.openBrowserAt(full.replace(/\/+$/, ''), cwd.value, host.value);
    else files.openPath(full, cwd.value, host.value, null);
    return;
  }
  diff.showFileDiff(e.path, isStaged);
}

const STATUS_LABELS: Record<string, string> = {
  M: 'modified', A: 'added', D: 'deleted', R: 'renamed', C: 'copied', T: 'type changed', U: 'unmerged', '?': 'new file',
};
function statusLabel(code: string): string {
  if (code.length === 2) return 'conflict';
  return STATUS_LABELS[code] ?? code;
}
function badgeClass(code: string): string {
  return code.length === 2 ? 's-conflict' : `s-${badge(code)}`;
}
function badge(code: string): string {
  return code.length === 2 ? '!' : code === '?' ? 'N' : code;
}

const branchLabel = computed(() => {
  const s = st.value;
  if (!s) return '';
  if (s.detached) return 'detached HEAD';
  return s.branch;
});

// --- commit ---
const message = ref('');
const commitCmd = computed(() => prefs.gitCommitCmd);
// With a command configured the button runs it; "write a message" switches
// back to a typed message for this commit.
const manual = ref(false);
const useCommand = computed(() => !!commitCmd.value && !manual.value);
const stagedCount = computed(() => staged.value.length + (st.value?.staged_outside ?? 0));
const canCommit = computed(() => {
  if (diff.committing) return false;
  if (useCommand.value) return true; // the command may stage by itself (e.g. git commit -a)
  return stagedCount.value > 0 && message.value.trim().length > 0;
});
const commitHint = computed(() => {
  if (useCommand.value) return '';
  if (!stagedCount.value) return 'stage something first';
  if (!message.value.trim()) return 'write a commit message';
  return '';
});

function doCommit() {
  if (!canCommit.value) return;
  haptic(18);
  if (useCommand.value) diff.commit({ useCommand: true });
  else diff.commit({ message: message.value });
}

// A successful typed commit clears the message box.
watch(() => diff.commitOutcome, (o) => {
  if (o?.ok && !o.command) message.value = '';
});

const outcomeText = computed(() => {
  const o = diff.commitOutcome;
  if (!o) return '';
  if (!o.ok) return `✗ ${o.error || 'commit failed'}`;
  if (o.hash) return `✓ committed ${o.hash.slice(0, 7)} ${o.subject ?? ''}`.trim();
  return '✓ command finished — no new commit';
});

// --- commit command (relay prefs, shared by all devices) ---
const cmdDraft = ref(prefs.gitCommitCmd);
watch(() => prefs.gitCommitCmd, (v) => { cmdDraft.value = v; });
const cmdDirty = computed(() => cmdDraft.value.trim() !== prefs.gitCommitCmd);
function saveCmd() {
  haptic();
  prefs.setGitCommitCmd(cmdDraft.value);
  manual.value = false;
}
function clearCmd() {
  haptic();
  cmdDraft.value = '';
  prefs.setGitCommitCmd('');
}
</script>

<template>
  <div class="gs">
    <div v-if="diff.statusError" class="gs-error" role="alert">
      <span aria-hidden="true">⚠</span> {{ diff.statusError }}
    </div>
    <div v-if="!st && diff.statusLoading" class="gs-loading">loading status…</div>

    <template v-if="st">
      <div class="gs-branch">
        <span class="gs-branch-name mono">⎇ {{ branchLabel }}</span>
        <span v-if="st.upstream" class="gs-dim mono" :title="`tracking ${st.upstream}`">→ {{ st.upstream }}</span>
        <span v-if="st.ahead" class="gs-ab mono" :title="`${st.ahead} commit(s) to push`">↑{{ st.ahead }}</span>
        <span v-if="st.behind" class="gs-ab mono" :title="`${st.behind} commit(s) to pull`">↓{{ st.behind }}</span>
        <span v-if="st.initial" class="gs-dim">no commits yet</span>
        <span v-if="diff.statusLoading || diff.staging || diff.syncing" class="gs-dim" aria-live="polite">updating…</span>
      </div>

      <div v-if="canSync" class="gs-sync" role="group" aria-label="Remote">
        <button type="button" class="gs-btn" :disabled="!canPush" :title="canPush ? `Push ${st?.ahead} commit(s) to ${st?.upstream}` : 'Push is available when this branch is ahead of its upstream and not behind'" @click="doPush">
          {{ diff.syncing === 'push' ? 'pushing…' : (st?.ahead ? `push ${st.ahead}` : 'push') }}
        </button>
        <button type="button" class="gs-btn" :disabled="!canFetch" title="Fetch from the upstream remote. Does not merge." @click="doFetch">
          {{ diff.syncing === 'fetch' ? 'fetching…' : 'fetch' }}
        </button>
        <button type="button" class="gs-btn" :disabled="!canPull" :title="canPull ? `Fast-forward ${st?.behind} commit(s) from ${st?.upstream}` : 'Pull is available when the branch is strictly behind and the working tree is clean'" @click="doPull">
          {{ diff.syncing === 'pull' ? 'pulling…' : (st?.behind ? `pull ${st.behind}` : 'pull') }}
        </button>
        <p class="gs-note">push and pull affect the whole branch, not just this folder.</p>
        <div v-if="diff.syncOutcome" class="gs-outcome" :class="{ ok: diff.syncOutcome.ok, err: !diff.syncOutcome.ok }" role="status">
          <div class="gs-outcome-line">{{ syncText }}</div>
          <pre v-if="diff.syncOutcome.output" class="gs-output mono">{{ diff.syncOutcome.output }}</pre>
        </div>
      </div>

      <p v-if="clean" class="gs-clean">nothing to commit, working tree clean</p>

      <!-- changes: unstaged + new + conflicts -->
      <section v-if="changes.length" class="gs-sec" aria-label="Changes not staged">
        <header class="gs-sec-head">
          <label class="gs-check">
            <input
              type="checkbox"
              :checked="allSelected"
              :indeterminate="someSelected"
              aria-label="Select all changes"
              @change="toggleAll"
            />
            <span>changes <span class="gs-count">{{ changes.length }}</span></span>
          </label>
          <button
            type="button"
            class="gs-btn"
            :disabled="!selected.size || diff.staging"
            @click="stageSelected"
          >add selected{{ selected.size ? ` (${selected.size})` : '' }}</button>
          <button type="button" class="gs-btn" :disabled="diff.staging" @click="stageAll">add all</button>
        </header>
        <ul class="gs-list">
          <li v-for="e in changes" :key="'c' + e.status + e.path" class="gs-row">
            <input
              type="checkbox"
              class="gs-row-check"
              :checked="selected.has(e.path)"
              :aria-label="`Select ${e.path}`"
              @change="toggle(e.path)"
            />
            <span class="gs-badge" :class="badgeClass(e.status)" :title="statusLabel(e.status)" :aria-label="statusLabel(e.status)">{{ badge(e.status) }}</span>
            <button type="button" class="gs-path mono" :title="e.status === '?' ? `Preview ${e.path}` : `Show diff of ${e.path}`" @click="openEntry(e, false)">{{ e.path }}</button>
            <button
              v-if="canDiscard(e)"
              type="button"
              class="gs-mini"
              :aria-label="`Discard unstaged changes in ${e.path}`"
              title="Discard unstaged changes"
              :disabled="busy"
              @click="discardOne(e.path)"
            >×</button>
            <button type="button" class="gs-mini" :aria-label="`Add ${e.path}`" title="git add" :disabled="diff.staging" @click="stageOne(e.path)">+</button>
          </li>
        </ul>
      </section>

      <!-- staged -->
      <section v-if="staged.length" class="gs-sec" aria-label="Staged changes">
        <header class="gs-sec-head">
          <span>staged <span class="gs-count">{{ staged.length }}</span></span>
          <button type="button" class="gs-btn" :disabled="diff.staging" @click="unstageAll">unstage all</button>
        </header>
        <ul class="gs-list">
          <li v-for="e in staged" :key="'s' + e.status + e.path" class="gs-row">
            <span class="gs-badge" :class="badgeClass(e.status)" :title="statusLabel(e.status)" :aria-label="statusLabel(e.status)">{{ badge(e.status) }}</span>
            <button type="button" class="gs-path mono" :title="`Show staged diff of ${e.path}`" @click="openEntry(e, true)">
              {{ e.path }}<span v-if="e.orig" class="gs-dim"> ← {{ e.orig }}</span>
            </button>
            <button type="button" class="gs-mini" :aria-label="`Unstage ${e.path}`" title="unstage" :disabled="diff.staging" @click="unstageOne(e.path)">−</button>
          </li>
        </ul>
      </section>

      <p v-if="st.truncated" class="gs-warn">The list is cut at 2000 entries.</p>

      <!-- commit -->
      <section class="gs-commit" aria-label="Commit">
        <p v-if="st.staged_outside" class="gs-warn">
          This commit also records {{ st.staged_outside }} staged file{{ st.staged_outside === 1 ? '' : 's' }} outside this folder.
        </p>

        <template v-if="useCommand">
          <button type="button" class="gs-commit-btn" :disabled="!canCommit" @click="doCommit">
            {{ diff.committing ? 'running…' : 'commit' }}
          </button>
          <p class="gs-note">runs <code class="mono">{{ commitCmd }}</code> on the relay in this folder</p>
          <button type="button" class="gs-link" @click="manual = true">write a message instead</button>
        </template>
        <template v-else>
          <textarea
            v-model="message"
            class="gs-msg mono"
            rows="3"
            placeholder="commit message"
            aria-label="Commit message"
            @keydown.ctrl.enter.prevent="doCommit"
            @keydown.meta.enter.prevent="doCommit"
          ></textarea>
          <div class="gs-commit-row">
            <button type="button" class="gs-commit-btn" :disabled="!canCommit" :title="commitHint" @click="doCommit">
              {{ diff.committing ? 'committing…' : `commit${stagedCount ? ` · ${stagedCount} file${stagedCount === 1 ? '' : 's'}` : ''}` }}
            </button>
            <span v-if="commitHint && !diff.committing" class="gs-dim">{{ commitHint }}</span>
          </div>
          <button v-if="commitCmd" type="button" class="gs-link" @click="manual = false">use the commit command</button>
        </template>

        <div v-if="diff.commitOutcome" class="gs-outcome" :class="{ ok: diff.commitOutcome.ok, err: !diff.commitOutcome.ok }" role="status">
          <div class="gs-outcome-line">{{ outcomeText }}</div>
          <pre v-if="diff.commitOutcome.output" class="gs-output mono">{{ diff.commitOutcome.output }}</pre>
        </div>

        <details class="gs-cmd-edit">
          <summary>⚙ commit command</summary>
          <div class="gs-cmd-row">
            <input
              v-model="cmdDraft"
              class="gs-cmd-input mono"
              type="text"
              spellcheck="false"
              autocomplete="off"
              autocapitalize="off"
              placeholder='e.g. git commit -s -m "$(aicommit)"'
              aria-label="Commit command"
              @keydown.enter.prevent="cmdDirty && saveCmd()"
            />
            <button type="button" class="gs-btn" :disabled="!cmdDirty" @click="saveCmd">save</button>
            <button v-if="prefs.gitCommitCmd" type="button" class="gs-btn" @click="clearCmd">clear</button>
          </div>
          <p class="gs-note">
            Runs with <code>sh</code> on the relay in the repository folder — no input, stopped after 5 minutes —
            and its output is shown here. Shared by all devices. Leave it empty to type a message for each commit.
          </p>
        </details>
      </section>
    </template>
  </div>
</template>

<style scoped>
.mono { font-family: var(--font-mono); }
.gs { padding: 12px 14px 24px; display: flex; flex-direction: column; gap: 12px; color: var(--text); }
.gs-error { color: var(--red); font-size: var(--text-sm); }
.gs-loading, .gs-clean { color: var(--faint); font-family: var(--font-mono); font-size: var(--text-xs); text-align: center; padding: 20px 0; margin: 0; }
.gs-branch { display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px; font-size: var(--text-xs); }
.gs-branch-name { font-weight: 700; color: var(--text); }
.gs-dim { color: var(--faint); font-size: var(--text-2xs); }
.gs-ab { color: var(--orange); font-size: var(--text-2xs); }
.gs-sync { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.gs-sync .gs-note, .gs-sync .gs-outcome { flex-basis: 100%; }

.gs-sec { border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); }
.gs-sec-head {
  display: flex; align-items: center; gap: 8px; padding: 6px 10px; border-bottom: 1px solid var(--border);
  font-family: var(--font-mono); font-size: var(--text-2xs); text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted);
}
.gs-sec-head > :first-child { flex: 1; min-width: 0; }
.gs-check { display: inline-flex; align-items: center; gap: 8px; cursor: pointer; }
.gs-count { color: var(--text-dim); }
.gs-list { list-style: none; margin: 0; padding: 4px 0; max-height: 40vh; overflow-y: auto; }
.gs-row { display: flex; align-items: center; gap: 8px; padding: 3px 10px; }
.gs-row:hover { background: var(--surface-2); }
.gs-row-check { flex-shrink: 0; }
.gs-badge {
  flex-shrink: 0; width: 18px; text-align: center; font-family: var(--font-mono); font-size: var(--text-2xs); font-weight: 700;
  border-radius: 3px; color: var(--text-dim);
}
.gs-badge.s-M, .gs-badge.s-T { color: var(--orange); }
.gs-badge.s-A, .gs-badge.s-N { color: var(--green); }
.gs-badge.s-D { color: var(--red); }
.gs-badge.s-R, .gs-badge.s-C { color: var(--blue); }
.gs-badge.s-conflict { color: #fff; background: var(--red); }
.gs-path {
  flex: 1; min-width: 0; text-align: left; background: none; border: none; padding: 2px 0;
  font-size: var(--text-xs); color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer;
}
.gs-path:hover { color: var(--blue); }
.gs-mini {
  flex-shrink: 0; width: 24px; height: 22px; display: grid; place-items: center; font-family: var(--font-mono); font-size: var(--text-sm);
  color: var(--muted); background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
}
.gs-mini:hover:not(:disabled) { color: var(--text); border-color: var(--border-strong); }
.gs-btn {
  flex-shrink: 0; font-family: var(--font-mono); font-size: var(--text-2xs); text-transform: none; letter-spacing: 0;
  color: var(--text-dim); background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius); padding: 3px 9px;
}
.gs-btn:hover:not(:disabled) { color: var(--text); border-color: var(--border-strong); }
.gs-btn:disabled, .gs-mini:disabled { opacity: 0.4; }
.gs-warn { margin: 0; color: var(--orange); font-size: var(--text-xs); }

.gs-commit { display: flex; flex-direction: column; gap: 8px; }
.gs-msg {
  width: 100%; resize: vertical; font-size: var(--text-xs); color: var(--term-fg); background: var(--term-bg);
  border: 1px solid var(--border); border-radius: var(--radius); padding: 8px 10px; outline: none;
}
.gs-msg:focus { border-color: rgba(96, 165, 250, 0.5); }
.gs-commit-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.gs-commit-btn {
  align-self: flex-start; font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  color: #06281a; background: var(--green); border: 1px solid var(--green); border-radius: var(--radius); padding: 6px 16px;
}
.gs-commit-btn:disabled { opacity: 0.45; }
.gs-note { margin: 0; color: var(--faint); font-size: var(--text-2xs); line-height: 1.5; }
.gs-note code { color: var(--text-dim); word-break: break-all; }
.gs-link { align-self: flex-start; background: none; border: none; padding: 0; color: var(--blue); font-size: var(--text-2xs); text-decoration: underline; }
.gs-outcome { border-radius: var(--radius); padding: 8px 10px; font-size: var(--text-xs); border: 1px solid var(--border); }
.gs-outcome.ok { border-color: rgba(52, 211, 153, 0.5); background: var(--green-soft); }
.gs-outcome.err { border-color: rgba(248, 113, 113, 0.5); background: var(--red-soft); }
.gs-output { margin: 6px 0 0; max-height: 30vh; overflow: auto; white-space: pre-wrap; word-break: break-word; font-size: var(--text-2xs); color: var(--text-dim); }
.gs-cmd-edit summary { cursor: pointer; color: var(--muted); font-family: var(--font-mono); font-size: var(--text-2xs); }
.gs-cmd-row { display: flex; gap: 6px; margin: 8px 0 6px; }
.gs-cmd-input {
  flex: 1; min-width: 0; font-size: var(--text-xs); color: var(--term-fg); background: var(--term-bg);
  border: 1px solid var(--border); border-radius: var(--radius); padding: 5px 8px; outline: none;
}
.gs-cmd-input:focus { border-color: rgba(96, 165, 250, 0.5); }
</style>
