import { defineStore } from 'pinia';
import { ref, computed, watch } from 'vue';
import { ws } from '../api/ws';
import { useTodoStore } from './todo';
import type { ServerMessage, Pane, TodoBinding } from '../api/types';
import { safeStorage } from '../utils/storage';
import { markSessionCookieHint, clearSessionCookieHint, extractRelayCredentials, sessionTokenFor, saveSessionToken, dropSessionToken } from '../utils/relay';

export const useConnectionStore = defineStore('connection', () => {
  const status = ref<'disconnected' | 'connecting' | 'connected'>('disconnected');
  const features = ref<string[]>([]);
  const serverVersion = ref('');
  // True once the first WS hello has been received — gates the main UI
  // to avoid a flash of the empty agent list before data arrives.
  const booted = ref(false);
  // Safety timeout so a missing hello (server bug, slow relay, auth
  // failure after upgrade) doesn't permanently trap the user behind the
  // boot splash. If hello doesn't arrive within 5s, reveal the main UI
  // so the relay switch and settings remain accessible for self-recovery.
  let bootTimer: ReturnType<typeof setTimeout> | null = null;
  const bootTimedOut = ref(false);
  // Default: derive same-origin /ws from current page (works with tunnel + embed)
  const defaultUrl = (() => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${location.host}/ws`;
  })();
  const storedUrl = safeStorage.getItem('herdrem_url');
  const storedCreds = storedUrl ? extractRelayCredentials(storedUrl) : { url: '', token: '' };
  if (storedCreds.url && storedCreds.url !== storedUrl) {
    safeStorage.setItem('herdrem_url', storedCreds.url);
  }
  const url = ref(storedCreds.url || defaultUrl);
  // Legacy localStorage token keys are scrubbed — relay tokens live in
  // sessionStorage (survives reload incl. server restarts, dies with the
  // tab) and same-origin reconnects prefer the HttpOnly session cookie.
  safeStorage.removeItem('herdrem_token');
  const token = ref(storedCreds.token || sessionTokenFor(url.value));

  let unsubStatus: (() => void) | null = null;
  // Bumped per connect/disconnect so a stale async auth probe cannot apply
  // its verdict to a newer connection attempt.
  let connectSeq = 0;

  function hasFeature(f: string): boolean {
    return features.value.includes(f);
  }

  function sessionHttpBase(): string | null {
    try {
      const httpBase = url.value.replace(/^ws/i, 'http').replace(/\/ws$/i, '');
      if (new URL(httpBase).origin !== window.location.origin) return null;
      return httpBase;
    } catch {
      return null;
    }
  }

  function mintSessionCookie() {
    if (typeof window === 'undefined' || !token.value) return;
    const httpBase = sessionHttpBase();
    if (!httpBase) return;
    const seq = connectSeq;
    // Bearer header keeps the relay token out of the URL (history/logs);
    // the cookie the server sets carries an opaque session id, not the token.
    void fetch(`${httpBase}/session-auth`, {
      credentials: 'include',
      headers: { Authorization: `Bearer ${token.value}` },
    }).then((res) => {
      if (res.ok || res.status === 204) markSessionCookieHint();
      // A 401 means the token itself was rejected — fail fast to the auth
      // prompt instead of letting the WS retry loop cycle under the splash.
      else if (res.status === 401 || res.status === 429) failBootToAuth(seq);
    }).catch(() => {
      /* ignore mint failures; WS query token still authenticates */
    });
  }

  // failBootToAuth converts a same-origin 401 during connect into the token
  // prompt: drop the dead-session hint, flag the boot as timed out (App.vue's
  // watcher re-probes /session-auth for the local-relay case), and stop the
  // WS retry loop — retrying a rejected credential only flickers the splash.
  // booted is left false so the splash stays up while that re-probe lands.
  function failBootToAuth(seq: number) {
    if (seq !== connectSeq) return;
    clearSessionCookieHint();
    // A rejected token must not be replayed on the next reload.
    if (token.value) dropSessionToken(url.value);
    bootTimedOut.value = true;
    disconnect();
  }

  // probeSessionAuth rides alongside a tokenless same-origin connect — the
  // WS dial then relies on the HttpOnly session cookie, and a 401 means the
  // cookie is dead (expired, revoked, or signed under a rotated token).
  // Failing fast here turns a ~5s splash/retry oscillation into a single
  // splash → token-prompt transition.
  function probeSessionAuth(seq: number) {
    const httpBase = sessionHttpBase();
    if (!httpBase) return;
    void fetch(`${httpBase}/session-auth`, { credentials: 'include' })
      .then((res) => {
        if (res.status === 401 || res.status === 429) failBootToAuth(seq);
      })
      .catch(() => {
        /* relay unreachable — the WS retry loop keeps trying */
      });
  }

  // slideTimer re-mints the session cookie while the WS stays connected.
  // The signed server cookie has a fixed expiry (no server-side slide), so
  // without this an open app's session would lapse mid-use; each slide is a
  // fresh mint, giving the connected client a rolling sessionTTL window.
  let slideTimer: ReturnType<typeof setInterval> | null = null;

  function startSessionSlide() {
    // Same-origin only — a remote relay never carries the session cookie.
    if (slideTimer || typeof window === 'undefined' || !sessionHttpBase()) return;
    slideTimer = setInterval(() => {
      const httpBase = sessionHttpBase();
      if (!httpBase || status.value !== 'connected') return;
      void fetch(`${httpBase}/session-auth`, { credentials: 'include' }).then((res) => {
        if (res.ok || res.status === 204) markSessionCookieHint();
        // A dead session mid-connection only matters for the NEXT reload —
        // the live WS stays open, so drop the stale hint without kicking.
        else if (res.status === 401) clearSessionCookieHint();
      }).catch(() => {
        /* retry next interval */
      });
    }, 30 * 60 * 1000); // comfortably under the signed sessionTTL
  }

  function stopSessionSlide() {
    if (slideTimer) {
      clearInterval(slideTimer);
      slideTimer = null;
    }
  }

  // Re-auth mints a fresh server-side session; revoke the previous one so a
  // stolen/rotated cookie stops working immediately.
  function revokeSessionCookie(): Promise<void> {
    const httpBase = typeof window === 'undefined' ? null : sessionHttpBase();
    if (!httpBase) return Promise.resolve();
    return fetch(`${httpBase}/session-logout`, { method: 'POST', credentials: 'include' })
      .then((res) => {
        if (res.ok || res.status === 204) clearSessionCookieHint();
      })
      .catch(() => {
        /* ignore revoke failures; the new session still mints */
      });
  }

  function persistRelayUrl(raw: string): string {
    const creds = extractRelayCredentials(raw);
    const clean = creds.url || raw;
    url.value = clean;
    safeStorage.setItem('herdrem_url', clean);
    return creds.token;
  }

  function prepareAuth(targetUrl: string) {
    const fromQuery = persistRelayUrl(targetUrl);
    token.value = fromQuery;
    if (bootTimer) {
      clearTimeout(bootTimer);
      bootTimer = null;
    }
    bootTimedOut.value = false;
    disconnect();
  }

  function connect(targetUrl?: string, targetToken?: string) {
    const mySeq = ++connectSeq;
    if (targetUrl) {
      const fromQuery = persistRelayUrl(targetUrl);
      if (targetToken === undefined && fromQuery) token.value = fromQuery;
    }
    if (targetToken !== undefined) {
      token.value = targetToken;
      safeStorage.removeItem('herdrem_token');
    }
    if (targetToken) {
      // Explicit re-auth: drop the previous session server-side before
      // minting, so the old cookie cannot linger as a live credential.
      // (Tokenless connects must NOT revoke — the session cookie is the
      // credential the WS dial is about to rely on.)
      void revokeSessionCookie().then(mintSessionCookie);
    } else {
      // Tokenless dial: a session-stored token for this relay (survives
      // reload incl. server restarts) beats the cookie path entirely.
      if (!token.value) token.value = sessionTokenFor(url.value);
      mintSessionCookie();
      // Still tokenless = the WS relies on the session cookie; probe it in
      // parallel so a dead session fails fast instead of cycling retries.
      // (A token lifted from the URL query counts as a credential — the
      // mintSessionCookie fetch above already carries it as Bearer.)
      if (!token.value) probeSessionAuth(mySeq);
    }
    if (token.value) saveSessionToken(url.value, token.value);
    // Clear agent state to prevent stale data from old relay bleeding through
    const agents = useAgentsStore();
    agents.setAgents([]);
    agents.resetSnapshotKey();
    features.value = [];
    if (unsubStatus) unsubStatus();
    // Arm the boot timeout so a missing hello doesn't permanently
    // trap the user behind the boot splash.
    if (bootTimer) clearTimeout(bootTimer);
    if (!booted.value || bootTimedOut.value) {
      bootTimedOut.value = false;
      bootTimer = setTimeout(() => {
        bootTimer = null;
        booted.value = true;
        bootTimedOut.value = true;
        // Cookieless dial failed to hello: the session cookie is missing or
        // stale, so the next reload should prompt instead of retrying blindly.
        if (!token.value) clearSessionCookieHint();
      }, 5000);
    }
    ws.connect(url.value, token.value);
    unsubStatus = ws.onStatus((s) => {
      status.value = s as typeof status.value;
      if (s === 'connected') startSessionSlide();
      else stopSessionSlide();
    });
  }

  function disconnect() {
    connectSeq++; // invalidate in-flight auth probes from prior connects
    stopSessionSlide();
    ws.disconnect();
  }

  function setHello(version: string, feats: string[]) {
    if (bootTimer) {
      clearTimeout(bootTimer);
      bootTimer = null;
    }
    serverVersion.value = version;
    features.value = feats || [];
    booted.value = true;
    bootTimedOut.value = false;
  }

  return {
    status,
    features,
    serverVersion,
    booted,
    bootTimedOut,
    url,
    token,
    connect,
    disconnect,
    prepareAuth,
    hasFeature,
    setHello,
  };
});

export const useAgentsStore = defineStore('agents', () => {
  const agents = ref<Pane[]>([]);
  const kindFilter = ref<'all' | 'agent' | 'shell'>(
    (safeStorage.getItem('herdrem_kind_filter') as 'all' | 'agent' | 'shell') || 'all',
  );
  const statusFilter = ref<Set<string>>(new Set());
  const typeFilter = ref<string>('all');
  const search = ref('');

  // Persist kindFilter
  watch(kindFilter, (v) => {
    safeStorage.setItem('herdrem_kind_filter', v);
  });

  // Cache the last serialized snapshot to skip redundant updates.
  // The backend polls every 3s and broadcasts even when nothing changed;
  // without this, every snapshot triggers computed re-evaluation + Vue re-render.
  let lastSnapshotKey = '';

  function setAgents(list: Pane[]) {
    // Serialize the whole pane object: a hand-picked field list silently drops
    // enrichment updates — e.g. a blocked snapshot may arrive before attachMenu
    // fills prompt/options/option_labels, and the follow-up snapshot would be
    // deduped away while status/last_active stay identical.
    const key = list.map((a) => JSON.stringify(a)).join('|');
    if (key === lastSnapshotKey) return;
    lastSnapshotKey = key;
    agents.value = list;
  }

  /** Reset the snapshot dedup key (call on WS reconnect to force next update through). */
  function resetSnapshotKey() {
    lastSnapshotKey = '';
  }

  /** distinct agent type names for the type filter dropdown */
  const agentTypes = computed(() => {
    const set = new Set<string>();
    for (const a of agents.value) {
      const name = agentTypeName(a);
      if (name) set.add(name);
    }
    return [...set].sort((x, y) => x.localeCompare(y));
  });

  /** reactive filtered list: kind AND status AND type AND search, sorted by last_active desc */
  const filtered = computed<Pane[]>(() => {
    const q = search.value.trim().toLowerCase();
    const todo = useTodoStore();
    const sf = statusFilter.value;
    const list = agents.value.filter((a) => {
      if (kindFilter.value !== 'all' && paneKind(a) !== kindFilter.value) return false;
      if (sf.size > 0) {
        const b = bucket(a.status);
        const matchStatus = sf.has(b);
        const matchTodo = sf.has('todo') && todo.hasItems(todoKey(a));
        if (!matchStatus && !matchTodo) return false;
      }
      if (typeFilter.value !== 'all' && agentTypeName(a) !== typeFilter.value) return false;
      if (q) {
        const hay = [a.project, displayAgent(a), a.pane_id, a.host, a.workspace_label].map((s) =>
          (s || '').toLowerCase(),
        );
        if (!hay.some((s) => s.includes(q))) return false;
      }
      return true;
    });
    // Blocked agents first (approval inbox), then last_active descending.
    // Shells never pin — they are tools, not approval targets.
    list.sort((a, b) => {
      const ba = paneKind(a) === 'agent' && bucket(a.status) === 'blocked' ? 0 : 1;
      const bb = paneKind(b) === 'agent' && bucket(b.status) === 'blocked' ? 0 : 1;
      return ba - bb || (b.last_active || 0) - (a.last_active || 0);
    });
    return list;
  });

  const counts = computed(() => {
    const todo = useTodoStore();
    const c = { all: agents.value.length, agents: 0, shells: 0, blocked: 0, working: 0, done: 0, todo: 0 };
    const todoWorkspaces = new Set<string>();
    for (const a of agents.value) {
      if (paneKind(a) === 'shell') c.shells++;
      else c.agents++;
      const b = bucket(a.status);
      if (b === 'blocked') c.blocked++;
      else if (b === 'working') c.working++;
      else if (b === 'done') c.done++;
      const binding = todoKey(a);
      if (todo.hasItems(binding)) todoWorkspaces.add(`${binding.host}:${binding.workspace_id}`);
    }
    c.todo = todoWorkspaces.size;
    return c;
  });

  /** Toggle a status filter on/off (multi-select). Creates a new Set for reactivity. */
  function toggleStatusFilter(v: string) {
    const next = new Set(statusFilter.value);
    if (next.has(v)) next.delete(v);
    else next.add(v);
    statusFilter.value = next;
  }

  return {
    agents,
    kindFilter,
    statusFilter,
    typeFilter,
    search,
    setAgents,
    agentTypes,
    filtered,
    counts,
    toggleStatusFilter,
    resetSnapshotKey,
  };
});

export function paneKind(a: Pane): 'agent' | 'shell' {
  // Backend now sets kind explicitly. Fall back to agent presence for older relays.
  if (a.kind === 'shell') return 'shell';
  if (a.kind === 'agent') return 'agent';
  return a.agent ? 'agent' : 'shell';
}

export function displayAgent(a: Pane): string {
  return String(a.display_agent || a.agent || '').trim();
}

/** agentTypeName returns the name used by the type filter.
 *  Only the commandcode display convention "cmd•model" is grouped as "cmd";
 *  other custom display names remain exact filter values. */
export function agentTypeName(a: Pane): string {
  const name = displayAgent(a);
  return name.startsWith('cmd•') ? 'cmd' : name;
}

/** paneTitle returns the best display title for a pane.
 *  Agents: terminal_title_stripped (herdr's own title) → project → agent → pane_id
 *  Shells: cwd basename → terminal_title_stripped → pane_id */
export function paneTitle(a: Pane): string {
  const stripped = String(a.terminal_title_stripped || '').trim();
  if (paneKind(a) === 'shell') {
    if (a.cwd) {
      const base = a.cwd.replace(/\/+$/, '').split('/').pop();
      if (base) return base;
    }
    if (stripped) return stripped;
    return a.pane_id;
  }
  if (stripped) return stripped;
  if (a.project) return a.project;
  if (a.agent) return a.agent;
  return a.pane_id;
}

/** workspaceLabel returns the workspace name for a pane, or the id as fallback. */
export function workspaceLabel(a: Pane): string {
  return a.workspace_label || a.workspace_id || '';
}

/** spaceTitle returns a workspace label from the first pane's workspace_label. */
export function spaceTitle(panes: Pane[]): string {
  if (!panes.length) return 'default';
  return panes[0].workspace_label || panes[0].workspace_id || 'default';
}

/** cwdBase returns the basename of a pane's cwd, or ''. */
export function cwdBase(a: Pane): string {
  if (!a.cwd) return '';
  return a.cwd.replace(/\/+$/, '').split('/').pop() || '';
}

export function bucket(status?: string): string {
  if (!status) return 'idle';
  const s = status.toLowerCase();
  if (s === 'unknown') return 'idle';
  // Match the relay's isBlocked (poller.go): block/wait/confirm are all
  // approval states that carry prompt + options for quick-approve.
  if (s.includes('block') || s.includes('wait') || s.includes('confirm')) return 'blocked';
  if (s.includes('work') || s.includes('run') || s.includes('think')) return 'working';
  if (s.includes('done') || s.includes('complete') || s.includes('finish')) return 'done';
  return 'idle';
}

/** canMarkRead reports whether the relay accepts mark_pane_read for this pane:
 *  AckDone requires kind 'agent' AND the canonical status 'done' — the wider
 *  bucket synonyms (completed/finished) are rejected by the backend. */
export function canMarkRead(a: Pane): boolean {
  return paneKind(a) === 'agent' && (a.status || '').toLowerCase() === 'done';
}

/** shellLabel returns a short label for shell panes based on terminal_title_stripped.
 *  Returns '' if the title is just a cwd path (no meaningful program name). */
export function shellLabel(a: Pane): string {
  const t = (a.terminal_title_stripped || '').trim();
  if (!t) return '';
  // Strip cwd path that herdr appends (e.g. "nvim . ~/workspace/demo" → "nvim .")
  const parts = t.split(/\s+/);
  while (parts.length > 1 && (parts[parts.length - 1].startsWith('~') || parts[parts.length - 1].startsWith('/'))) {
    parts.pop();
  }
  const result = parts.join(' ');
  // If what remains is itself a path, there's no meaningful program name
  if (result.startsWith('~') || result.startsWith('/')) return '';
  return result;
}

/** relTime returns a short relative time string from an epoch seconds timestamp.
 *  e.g. "3m", "2h", "5d", "" if no timestamp. */
export function relTime(epoch?: number, now = Math.floor(Date.now() / 1000)): string {
  if (!epoch) return '';
  const diff = now - epoch;
  if (diff < 60) return 'now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
  if (diff < 604800) return `${Math.floor(diff / 86400)}d`;
  return `${Math.floor(diff / 604800)}w`;
}

/** todoKey returns the workspace binding for a pane. */
export function todoKey(a: Pane): TodoBinding {
  return {
    host: a.host || 'local',
    workspace_id: a.workspace_id || 'default',
    workspace_label: a.workspace_label,
  };
}

// Global message handler — dispatches to stores
export function handleMessage(msg: ServerMessage) {
  const agents = useAgentsStore();
  const conn = useConnectionStore();
  switch (msg.type) {
    case 'agents':
      agents.setAgents(msg.agents || []);
      break;
    case 'hello':
      conn.setHello(msg.server_version, msg.features || []);
      break;
    case 'error':
      console.error('server error:', msg.op, msg.message);
      break;
  }
}
