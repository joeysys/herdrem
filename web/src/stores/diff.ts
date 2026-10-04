import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ws } from '../api/ws';
import { uid } from '../utils/id';
import { safeStorage } from '../utils/storage';
import type {
  GitDiff, GitLog, GitDiffMode, GitDiffFormat, CommitEntry, GitDiffContent, GitLogResult,
  GitLogOptions, GitLogViewReq, GitLogViewResult,
  GitStatusData, GitStatusResultMsg, GitCommitResultMsg, GitStageReq, GitCommitReq,
} from '../api/types';

// The relay drops WebSocket frames over 32 KiB (its read limit) — an oversized
// stage list or commit message kills the whole connection, not just the call.
// Keep client messages comfortably under it.
const MAX_MSG_BYTES = 24 * 1024;
// Mirrors maxCommitMessage in internal/service/gitwrite.go — the frame limit
// would hit before the server could reject a longer message.
const MAX_COMMIT_MESSAGE_BYTES = 16 * 1024;
const utf8Len = (s: string) => new TextEncoder().encode(s).length;

/** The panel's tabs: the three diffs, git status (stage / commit) and git log. */
export type DiffViewMode = GitDiffMode | 'status' | 'log';

const LOG_OPTS_KEY = 'herdrem_git_log_opts';
const DEFAULT_LOG_OPTS: GitLogOptions = { oneline: true, graph: true, all: false, first_parent: false, no_merges: false };

function loadLogOptions(): GitLogOptions {
  try {
    const saved = JSON.parse(safeStorage.getItem(LOG_OPTS_KEY) || '{}') as Record<string, unknown>;
    const opts = { ...DEFAULT_LOG_OPTS };
    for (const k of Object.keys(opts) as (keyof GitLogOptions)[]) {
      if (typeof saved[k] === 'boolean') opts[k] = saved[k] as boolean;
    }
    return opts;
  } catch {
    return { ...DEFAULT_LOG_OPTS };
  }
}

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
  /** what `content` holds: '' a patch, 'name-status' one line per file */
  format: '' | GitDiffFormat;
  commits: CommitEntry[];
  /** log tab: most commits the relay lists, and whether the text was cut */
  logLimit: number;
  logTruncated: boolean;
  loading: boolean;
  loadingCommits: boolean;
  error?: string;
}

export const useDiffStore = defineStore('diff', () => {
  const open = ref(false);
  const halfScreen = ref(false);
  /** Show `--name-status` instead of the patch. A view preference: it
   *  outlives closing the panel, unlike the rest of the state. */
  const nameStatus = ref(false);
  /** The log tab's switches, remembered on this device. */
  const logOptions = ref<GitLogOptions>(loadLogOptions());
  const logViewReqId = ref('');
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
  // The last commit result shown on this page; it survives closing the panel
  // so the relay's copy (last_commit in the status) is not shown twice.
  let seenCommitReq = '';
  let commitPollTimer: ReturnType<typeof setTimeout> | null = null;
  const COMMIT_POLL_MS = 3000;
  // Own commit whose result never shows up (relay restarted and dropped the
  // in-memory last_commit, or another client took the slot). Statuses that
  // still say the commit is running are exempt — a long git commit stays
  // busy. 15s of "not running, and not our req id" is long enough to ignore a
  // status taken before the relay registered the commit.
  const COMMIT_RESULT_WAIT_MS = 15000;
  let commitSentAt = 0;

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
      format: '',
      commits: [],
      logLimit: 0,
      logTruncated: false,
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
    if (mode === 'log') {
      requestLogView();
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
    logViewReqId.value = '';
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
    commitSentAt = 0;
    if (commitPollTimer) clearTimeout(commitPollTimer);
    commitPollTimer = null;
  }

  /** Drop all state — used when switching relays. */
  function reset() {
    close();
    nameStatus.value = false; // the next relay may not support it
  }

  /** Switch between the patch and `--name-status`, reloading the diff. */
  function setNameStatus(on: boolean) {
    if (nameStatus.value === on) return;
    nameStatus.value = on;
    const s = state.value;
    if (!s || s.mode === 'status' || (s.mode === 'commit' && !s.commit)) return;
    state.value = { ...s, content: '', loading: true, error: undefined };
    requestDiff();
  }

  /** Flip one log switch, remember it, and reload the log. */
  function setLogOption(key: keyof GitLogOptions, on: boolean) {
    if (logOptions.value[key] === on) return;
    logOptions.value = { ...logOptions.value, [key]: on };
    safeStorage.setItem(LOG_OPTS_KEY, JSON.stringify(logOptions.value));
    const s = state.value;
    if (!s || s.mode !== 'log') return;
    state.value = { ...s, content: '', loading: true, error: undefined };
    requestLogView();
  }

  /** From the log: show that commit (git show). */
  function showCommit(hash: string) {
    if (!state.value) return;
    setCommit(hash);
    // the commit tab's picker lists recent commits; fetch them once
    if (!state.value.commits.length) requestLog();
  }

  function requestLogView() {
    const s = state.value;
    if (!s) return;
    const reqId = newReqId();
    logViewReqId.value = reqId;
    const o = logOptions.value;
    const msg: GitLogViewReq = { type: 'git_log_view', cwd: s.cwd, host: s.host, req_id: reqId };
    for (const k of Object.keys(o) as (keyof GitLogOptions)[]) if (o[k]) msg[k] = true;
    ws.send(msg);
  }

  function setLogViewResult(msg: GitLogViewResult) {
    const s = state.value;
    if (!s || s.mode !== 'log' || msg.cwd !== s.cwd) return;
    if (msg.req_id && logViewReqId.value && msg.req_id !== logViewReqId.value) return;
    state.value = {
      ...s,
      content: msg.content || '',
      format: '',
      logLimit: msg.limit || 0,
      logTruncated: !!msg.truncated,
      loading: false,
      error: undefined,
    };
  }

  function setLogViewError(reqId?: string) {
    const s = state.value;
    if (!s || s.mode !== 'log') return;
    if (reqId && logViewReqId.value && reqId !== logViewReqId.value) return;
    state.value = { ...s, content: '', loading: false, error: 'git log failed (not a repo or unreachable host)' };
  }

  /** From a name-status row: back to the patch, scrolled to that file. */
  function showPatchFor(path: string) {
    focusPath.value = path;
    setNameStatus(false);
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
    if (mode === 'log') {
      requestLogView();
      return;
    }
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
    if (state.value.mode === 'log') {
      requestLogView();
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
    if (!s || s.mode === 'status' || s.mode === 'log') return;
    const reqId = newReqId();
    diffReqId.value = reqId;
    const msg: GitDiff = {
      type: 'git_diff',
      cwd: s.cwd,
      host: s.host,
      mode: s.mode,
      commit: s.mode === 'commit' ? s.commit : undefined,
      ...(nameStatus.value ? { format: 'name-status' as const } : {}),
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
    // a late diff reply must not pull the panel out of a non-diff tab
    if (!s || s.mode === 'status' || s.mode === 'log') return;
    if (msg.cwd !== s.cwd) return;
    if (msg.req_id && diffReqId.value && msg.req_id !== diffReqId.value) return;
    state.value = {
      ...s,
      content: msg.content || '',
      format: msg.format === 'name-status' ? 'name-status' : '',
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
    if (!s || s.mode === 'status' || s.mode === 'log') return;
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
    const msg: GitStageReq = { type: 'git_stage', cwd: s.cwd, host: s.host, req_id: reqId, ...extra };
    if (utf8Len(JSON.stringify(msg)) > MAX_MSG_BYTES) {
      statusError.value = 'selection too large — stage fewer files at once';
      return;
    }
    statusReqId.value = reqId; // the reply is a fresh status
    staging.value = true;
    statusError.value = '';
    if (!ws.send(msg)) {
      staging.value = false;
      statusError.value = 'not connected';
    }
  }

  /** Commit the index with a message, or (useCommand) run the configured command. */
  function commit(opts: { message?: string; useCommand?: boolean }) {
    const s = state.value;
    if (!s || committing.value) return;
    if (!opts.useCommand && utf8Len(opts.message ?? '') > MAX_COMMIT_MESSAGE_BYTES) {
      commitOutcome.value = { ok: false, error: 'commit message too long (max 16 KiB)' };
      return;
    }
    const reqId = newReqId();
    const msg: GitCommitReq = {
      type: 'git_commit',
      cwd: s.cwd,
      host: s.host,
      req_id: reqId,
      ...(opts.useCommand ? { use_command: true } : { message: opts.message ?? '' }),
    };
    // JSON escaping can double a message (quotes, backslashes, control
    // characters), so the byte cap on the text alone does not keep the frame
    // under the relay's read limit — and an oversized frame drops the whole
    // connection. Check the frame, as sendStage does.
    if (utf8Len(JSON.stringify(msg)) > MAX_MSG_BYTES) {
      commitOutcome.value = { ok: false, error: 'commit message too long once encoded — shorten it' };
      return;
    }
    commitReqId.value = reqId;
    committing.value = true;
    commitSentAt = Date.now();
    commitOutcome.value = null;
    const sent = ws.send(msg);
    if (!sent) {
      committing.value = false;
      commitOutcome.value = { ok: false, error: 'not connected' };
      return;
    }
    watchCommit();
  }

  function setStatusResult(msg: GitStatusResultMsg) {
    const s = state.value;
    if (!s || msg.cwd !== s.cwd) return;
    if (msg.req_id && statusReqId.value && msg.req_id !== statusReqId.value) return;
    const { type: _t, cwd: _c, req_id: _r, commit_running, last_commit, ...data } = msg;
    status.value = data;
    statusLoading.value = false;
    staging.value = false;
    statusError.value = '';
    syncCommitState(!!commit_running, last_commit);
  }

  // The commit reply only reaches the connection that asked for it, and
  // closing the panel forgets the request. The status therefore carries the
  // repo's commit state: a commit still running (from a closed page, or before
  // this panel reopened) keeps the button busy and is polled until it ends,
  // and its result is then taken from last_commit.
  function syncCommitState(running: boolean, last?: GitCommitResultMsg) {
    if (commitReqId.value && committing.value) {
      // Our own commit. Its reply only reaches the connection that sent it,
      // so after a reconnect the relay's last_commit is the only copy. A
      // status without it (still running, or taken before the relay saw the
      // commit) settles nothing until COMMIT_RESULT_WAIT_MS has passed.
      if (!running && last?.req_id === commitReqId.value) {
        showCommitResult(last);
        return;
      }
      if (!running && commitSentAt > 0 && Date.now() - commitSentAt >= COMMIT_RESULT_WAIT_MS) {
        committing.value = false;
        commitOutcome.value = { ok: false, error: 'commit result was not confirmed — check git status' };
        return;
      }
      return;
    }
    if (running) {
      committing.value = true;
      watchCommit();
      return;
    }
    committing.value = false;
    if (last?.req_id && last.req_id !== seenCommitReq) showCommitResult(last);
  }

  // While a commit is busy, re-read the status every few seconds until it
  // settles: nothing else asks for one after a reconnect. Skipped while a
  // stage is in flight, whose reply is the status it is waiting for.
  function watchCommit() {
    if (commitPollTimer) return;
    commitPollTimer = setTimeout(() => {
      commitPollTimer = null;
      if (!committing.value || !state.value) return;
      if (state.value.mode === 'status' && !staging.value) requestStatus();
      watchCommit();
    }, COMMIT_POLL_MS);
  }

  function showCommitResult(msg: GitCommitResultMsg) {
    committing.value = false;
    if (msg.req_id) seenCommitReq = msg.req_id;
    commitOutcome.value = {
      ok: msg.ok, hash: msg.hash, subject: msg.subject, output: msg.output, error: msg.error, command: msg.command,
    };
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
    showCommitResult(msg);
    // Whatever happened (a hook may have changed files, a command may have
    // staged more), show the current state.
    requestStatus();
  }

  /** Jump from the status list to a file's diff (staged → cached view). */
  function showFileDiff(path: string, staged: boolean) {
    if (!state.value) return;
    focusPath.value = path;
    const mode = staged ? 'cached' : 'unstaged';
    // The point is the file's patch, so leave the name-status view.
    if (nameStatus.value && state.value.mode === mode) {
      setNameStatus(false);
      return;
    }
    nameStatus.value = false;
    setMode(mode);
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
    nameStatus,
    setNameStatus,
    showPatchFor,
    logOptions,
    setLogOption,
    showCommit,
    setLogViewResult,
    setLogViewError,
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
