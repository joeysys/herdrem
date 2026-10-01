import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ws } from '../api/ws';
import { uid } from '../utils/id';
import type {
  GitDiff, GitLog, GitDiffMode, CommitEntry, GitDiffContent, GitLogResult,
  GitStatusData, GitStatusResultMsg, GitCommitResultMsg,
} from '../api/types';

/** The panel's tabs: the three diffs plus git status (stage / commit). */
export type DiffViewMode = GitDiffMode | 'status';

export interface CommitOutcome {
  ok: boolean;
  hash?: string;
  subject?: string;
  output?: string;
  error?: string;
  command?: string;
}

export interface DiffState {
  cwd: string;
  host: string;
  mode: DiffViewMode;
  commit: string;
  content: string;
  commits: CommitEntry[];
  loading: boolean;
  loadingCommits: boolean;
  error?: string;
}

export const useDiffStore = defineStore('diff', () => {
  const open = ref(false);
  const halfScreen = ref(false);
  const state = ref<DiffState | null>(null);
  // request ids of in-flight requests, so stale responses cannot overwrite
  // the current view (e.g. a slow unstaged response arriving after the user
  // switched to commit mode).
  const diffReqId = ref('');
  const logReqId = ref('');

  // --- git status ---
  const status = ref<GitStatusData | null>(null);
  const statusLoading = ref(false);
  const statusError = ref('');
  /** a stage / unstage request is in flight */
  const staging = ref(false);
  const committing = ref(false);
  const commitOutcome = ref<CommitOutcome | null>(null);
  /** file to scroll to once a diff loads (opened from the status list) */
  const focusPath = ref('');
  const statusReqId = ref('');
  const commitReqId = ref('');

  function newReqId(): string {
    return uid();
  }

  /** Open the diff preview for a working directory. Defaults to unstaged. */
  function openDiff(cwd: string, host: string, mode: DiffViewMode = 'unstaged', commit = '') {
    if (!cwd) return;
    state.value = {
      cwd,
      host: host || 'local',
      mode,
      commit,
      content: '',
      commits: [],
      loading: true,
      loadingCommits: false,
    };
    open.value = true;
    resetStatus();
    if (mode === 'status') {
      state.value.loading = false;
      requestStatus();
      return;
    }
    requestDiff();
    // commit mode needs the commit list to pick from; fetch it lazily so the
    // default unstaged view isn't delayed by a log call.
    if (mode === 'commit') requestLog();
  }

  function close() {
    open.value = false;
    halfScreen.value = false;
    state.value = null;
    diffReqId.value = '';
    logReqId.value = '';
    resetStatus();
  }

  function resetStatus() {
    status.value = null;
    statusLoading.value = false;
    statusError.value = '';
    staging.value = false;
    committing.value = false;
    commitOutcome.value = null;
    focusPath.value = '';
    statusReqId.value = '';
    commitReqId.value = '';
  }

  /** Drop all state — used when switching relays. */
  function reset() {
    close();
  }

  /** Toggle between full-screen overlay and bottom half-screen. */
  function toggleHalfScreen() {
    halfScreen.value = !halfScreen.value;
  }

  function setMode(mode: DiffViewMode) {
    if (!state.value) return;
    if (state.value.mode === mode && (mode !== 'commit' || state.value.commit)) return;
    if (mode === 'status') {
      state.value = { ...state.value, mode, loading: false, error: undefined };
      requestStatus();
      return;
    }
    state.value = { ...state.value, mode, content: '', loading: true, error: undefined };
    if (mode === 'commit') {
      // Need a commit to show; if none selected, fetch the log so the user can
      // pick one. Don't request a diff until a commit is chosen.
      if (!state.value.commit) {
        requestLog();
        state.value.loading = false;
        return;
      }
    }
    requestDiff();
  }

  function setCommit(commit: string) {
    if (!state.value) return;
    state.value = { ...state.value, mode: 'commit', commit, content: '', loading: true, error: undefined };
    requestDiff();
  }

  function refresh() {
    if (!state.value) return;
    if (state.value.mode === 'status') {
      requestStatus();
      return;
    }
    if (state.value.mode === 'commit' && !state.value.commit) {
      requestLog();
      return;
    }
    requestDiff();
  }

  function requestDiff() {
    const s = state.value;
    if (!s || s.mode === 'status') return;
    const reqId = newReqId();
    diffReqId.value = reqId;
    const msg: GitDiff = {
      type: 'git_diff',
      cwd: s.cwd,
      host: s.host,
      mode: s.mode,
      commit: s.mode === 'commit' ? s.commit : undefined,
      req_id: reqId,
    };
    ws.send(msg);
  }

  function requestLog() {
    const s = state.value;
    if (!s) return;
    const reqId = newReqId();
    logReqId.value = reqId;
    s.loadingCommits = true;
    const msg: GitLog = { type: 'git_log', cwd: s.cwd, host: s.host, req_id: reqId };
    ws.send(msg);
  }

  function setDiffContent(msg: GitDiffContent) {
    const s = state.value;
    if (!s) return;
    if (msg.cwd !== s.cwd) return;
    if (msg.req_id && diffReqId.value && msg.req_id !== diffReqId.value) return;
    state.value = {
      ...s,
      content: msg.content || '',
      mode: msg.mode,
      commit: msg.commit || s.commit,
      loading: false,
      error: undefined,
    };
  }

  function setLogResult(msg: GitLogResult) {
    const s = state.value;
    if (!s) return;
    if (msg.cwd !== s.cwd) return;
    if (msg.req_id && logReqId.value && msg.req_id !== logReqId.value) return;
    state.value = { ...s, commits: msg.commits || [], loadingCommits: false };
    // If we're in commit mode with no commit selected, auto-pick the newest.
    if (s.mode === 'commit' && !s.commit && (msg.commits?.length ?? 0) > 0) {
      setCommit(msg.commits[0].hash);
    }
  }

  function setDiffError(reqId?: string) {
    const s = state.value;
    if (!s) return;
    if (reqId && diffReqId.value && reqId !== diffReqId.value) return;
    state.value = {
      ...s,
      content: '',
      loading: false,
      error: 'git diff failed (not a repo, bad ref, or unreachable host)',
    };
  }

  // ---- status / stage / commit ----

  function requestStatus() {
    const s = state.value;
    if (!s) return;
    const reqId = newReqId();
    statusReqId.value = reqId;
    statusLoading.value = true;
    statusError.value = '';
    ws.send({ type: 'git_status', cwd: s.cwd, host: s.host, req_id: reqId });
  }

  /** git add (or with unstage, remove from the index) these paths. */
  function stage(paths: string[], unstage = false) {
    const s = state.value;
    if (!s || !paths.length || staging.value) return;
    sendStage({ paths, unstage });
  }

  /** git add -A . (or unstage everything) under the folder. */
  function stageAll(unstage = false) {
    const s = state.value;
    if (!s || staging.value) return;
    sendStage({ all: true, unstage });
  }

  function sendStage(extra: { paths?: string[]; all?: boolean; unstage: boolean }) {
    const s = state.value!;
    const reqId = newReqId();
    statusReqId.value = reqId; // the reply is a fresh status
    staging.value = true;
    statusError.value = '';
    ws.send({ type: 'git_stage', cwd: s.cwd, host: s.host, req_id: reqId, ...extra });
  }

  /** Commit the index with a message, or (useCommand) run the configured command. */
  function commit(opts: { message?: string; useCommand?: boolean }) {
    const s = state.value;
    if (!s || committing.value) return;
    const reqId = newReqId();
    commitReqId.value = reqId;
    committing.value = true;
    commitOutcome.value = null;
    const sent = ws.send({
      type: 'git_commit',
      cwd: s.cwd,
      host: s.host,
      req_id: reqId,
      ...(opts.useCommand ? { use_command: true } : { message: opts.message ?? '' }),
    });
    if (!sent) {
      committing.value = false;
      commitOutcome.value = { ok: false, error: 'not connected' };
    }
  }

  function setStatusResult(msg: GitStatusResultMsg) {
    const s = state.value;
    if (!s || msg.cwd !== s.cwd) return;
    if (msg.req_id && statusReqId.value && msg.req_id !== statusReqId.value) return;
    const { type: _t, cwd: _c, req_id: _r, ...data } = msg;
    status.value = data;
    statusLoading.value = false;
    staging.value = false;
    statusError.value = '';
  }

  function setStatusError(reqId: string | undefined, message: string) {
    if (!state.value) return;
    if (reqId && statusReqId.value && reqId !== statusReqId.value) return;
    statusLoading.value = false;
    staging.value = false;
    statusError.value = message || 'git status failed';
  }

  function setCommitResult(msg: GitCommitResultMsg) {
    const s = state.value;
    if (!s || msg.cwd !== s.cwd) return;
    if (msg.req_id && commitReqId.value && msg.req_id !== commitReqId.value) return;
    committing.value = false;
    commitOutcome.value = {
      ok: msg.ok, hash: msg.hash, subject: msg.subject, output: msg.output, error: msg.error, command: msg.command,
    };
    // Whatever happened (a hook may have changed files, a command may have
    // staged more), show the current state.
    requestStatus();
  }

  /** Jump from the status list to a file's diff (staged → cached view). */
  function showFileDiff(path: string, staged: boolean) {
    if (!state.value) return;
    focusPath.value = path;
    setMode(staged ? 'cached' : 'unstaged');
  }

  function clearFocus() {
    focusPath.value = '';
  }

  function setLogError(reqId?: string) {
    const s = state.value;
    if (!s) return;
    if (reqId && logReqId.value && reqId !== logReqId.value) return;
    state.value = { ...s, commits: [], loadingCommits: false };
  }

  return {
    open,
    halfScreen,
    state,
    openDiff,
    close,
    reset,
    toggleHalfScreen,
    setMode,
    setCommit,
    refresh,
    setDiffContent,
    setLogResult,
    setDiffError,
    setLogError,
    status,
    statusLoading,
    statusError,
    staging,
    committing,
    commitOutcome,
    focusPath,
    requestStatus,
    stage,
    stageAll,
    commit,
    setStatusResult,
    setStatusError,
    setCommitResult,
    showFileDiff,
    clearFocus,
  };
});
