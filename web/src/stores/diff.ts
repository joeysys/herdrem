import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ws } from '../api/ws';
import { uid } from '../utils/id';
import type { GitDiff, GitLog, GitDiffMode, CommitEntry, GitDiffContent, GitLogResult } from '../api/types';

export interface DiffState {
  cwd: string;
  host: string;
  mode: GitDiffMode;
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

  function newReqId(): string {
    return uid();
  }

  /** Open the diff preview for a working directory. Defaults to unstaged. */
  function openDiff(cwd: string, host: string, mode: GitDiffMode = 'unstaged', commit = '') {
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
  }

  /** Drop all state — used when switching relays. */
  function reset() {
    close();
  }

  /** Toggle between full-screen overlay and bottom half-screen. */
  function toggleHalfScreen() {
    halfScreen.value = !halfScreen.value;
  }

  function setMode(mode: GitDiffMode) {
    if (!state.value) return;
    if (state.value.mode === mode && (mode !== 'commit' || state.value.commit)) return;
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
    if (state.value.mode === 'commit' && !state.value.commit) {
      requestLog();
      return;
    }
    requestDiff();
  }

  function requestDiff() {
    const s = state.value;
    if (!s) return;
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
  };
});
