import { defineStore } from 'pinia';
import { ref, computed, watch } from 'vue';
import { ws } from '../api/ws';
import { useTodoStore } from './todo';
import type { ServerMessage, Pane, TodoBinding } from '../api/types';
import { safeStorage } from '../utils/storage';

export const useConnectionStore = defineStore('connection', () => {
  const status = ref<'disconnected' | 'connecting' | 'connected'>('disconnected');
  const features = ref<string[]>([]);
  const serverVersion = ref('');
  // True once the first WS hello has been received — gates the main UI
  // to avoid a flash of the empty agent list before data arrives.
  const booted = ref(false);
  // Default: derive same-origin /ws from current page (works with tunnel + embed)
  const defaultUrl = (() => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${location.host}/ws`;
  })();
  const url = ref(safeStorage.getItem('herdrem_url') || defaultUrl);
  // F19: keep the live relay token in memory only. A persisted copy in
  // localStorage can be stolen by XSS; same-origin sessions use an HttpOnly
  // cookie minted below, and the relays store still holds per-relay tokens
  // in memory for switching.
  safeStorage.removeItem('herdrem_token');
  const token = ref('');

  let unsubStatus: (() => void) | null = null;

  function hasFeature(f: string): boolean {
    return features.value.includes(f);
  }

  function mintSessionCookie() {
    if (typeof window === 'undefined' || !token.value) return;
    try {
      const httpBase = url.value.replace(/^ws/i, 'http').replace(/\/ws$/i, '');
      if (new URL(httpBase).origin !== window.location.origin) return;
      const q = `?token=${encodeURIComponent(token.value)}`;
      void fetch(`${httpBase}/session-auth${q}`, { credentials: 'include' });
    } catch {
      /* ignore mint failures; WS query token still authenticates */
    }
  }

  function connect(targetUrl?: string, targetToken?: string) {
    if (targetUrl) {
      url.value = targetUrl;
      safeStorage.setItem('herdrem_url', targetUrl);
    }
    if (targetToken !== undefined) {
      token.value = targetToken;
      safeStorage.removeItem('herdrem_token');
    }
    mintSessionCookie();
    // Clear agent state to prevent stale data from old relay bleeding through
    const agents = useAgentsStore();
    agents.setAgents([]);
    agents.resetSnapshotKey();
    features.value = [];
    if (unsubStatus) unsubStatus();
    ws.connect(url.value, token.value);
    unsubStatus = ws.onStatus((s) => {
      status.value = s as typeof status.value;
    });
  }

  function disconnect() {
    ws.disconnect();
  }

  function setHello(version: string, feats: string[]) {
    serverVersion.value = version;
    features.value = feats || [];
    booted.value = true;
  }

  return { status, features, serverVersion, booted, url, token, connect, disconnect, hasFeature, setHello };
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
    // Include workspace identity because Todo scope and grouping depend on it.
    const key = list
      .map(
        (a) =>
          `${a.host || ''}:${a.pane_id}:${a.workspace_id || ''}:${a.workspace_label || ''}:${a.status}:${a.last_active || 0}:${a.branch || ''}:${a.cwd || ''}:${JSON.stringify(a.valid_paths || [])}`,
      )
      .join('|');
    if (key === lastSnapshotKey) return;
    lastSnapshotKey = key;
    agents.value = list;
  }

  /** Reset the snapshot dedup key (call on WS reconnect to force next update through). */
  function resetSnapshotKey() {
    lastSnapshotKey = '';
  }

  /** distinct agent display names for the type filter dropdown */
  const agentTypes = computed(() => {
    const set = new Set<string>();
    for (const a of agents.value) {
      const name = displayAgent(a);
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
      if (typeFilter.value !== 'all' && displayAgent(a) !== typeFilter.value) return false;
      if (q) {
        const hay = [a.project, displayAgent(a), a.pane_id, a.host, a.workspace_label].map((s) =>
          (s || '').toLowerCase(),
        );
        if (!hay.some((s) => s.includes(q))) return false;
      }
      return true;
    });
    // Sort by last_active descending (most recent first); no timestamp → end
    list.sort((a, b) => (b.last_active || 0) - (a.last_active || 0));
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
  if (s.includes('block')) return 'blocked';
  if (s.includes('work') || s.includes('run') || s.includes('think')) return 'working';
  if (s.includes('done') || s.includes('complete') || s.includes('finish')) return 'done';
  return 'idle';
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
