<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import {
  useConnectionStore,
  useAgentsStore,
  handleMessage,
  bucket,
  paneKind,
  paneTitle,
  workspaceLabel,
  displayAgent,
  shellLabel,
  relTime,
} from './stores/connection';
import { useTodoStore } from './stores/todo';
import { useTerminalStore } from './stores/terminal';
import { useRelaysStore } from './stores/relays';
import { usePreviewsStore } from './stores/preview';
import { useFilesStore } from './stores/files';
import { useDiffStore } from './stores/diff';
import { usePrefsStore } from './stores/prefs';
import { useTunnelsStore } from './stores/tunnels';
import { useRelayConnection } from './composables/useRelayConnection';
import { useNotify } from './composables/useNotify';
import { useHaptic } from './composables/useHaptic';
import { useHistoryTrap, isStandalone, tryLockPortrait } from './composables/useHistoryTrap';
import { ws } from './api/ws';
import type { ServerMessage, Pane } from './api/types';
import { canCookieReconnect, relayHostLabel, relayNeedsToken } from './utils/relay';
import TermView from './components/term/TermView.vue';
import AgentIcon from './components/agent/AgentIcon.vue';
import AgentFilters from './components/agent/AgentFilters.vue';
import ContextMenu from './components/agent/ContextMenu.vue';
import SpaceView from './components/space/SpaceView.vue';
import SettingsPanel from './components/settings/SettingsPanel.vue';
import FileBrowser from './components/files/FileBrowser.vue';
import FilePreview from './components/files/FilePreview.vue';
import DiffPreview from './components/files/DiffPreview.vue';
import TunnelPanel from './components/tunnel/TunnelPanel.vue';
import TodoManager from './components/todo/TodoManager.vue';

const conn = useConnectionStore();
const agents = useAgentsStore();
const todo = useTodoStore();
const term = useTerminalStore();
const relays = useRelaysStore();
const previews = usePreviewsStore();
const files = useFilesStore();
const diff = useDiffStore();
const prefs = usePrefsStore();
const tunnelsStore = useTunnelsStore();
const { boot, switchTo } = useRelayConnection();
const notify = useNotify();
const { haptic } = useHaptic();
const trap = useHistoryTrap();
const { toasts, justDonePanes } = notify;

let unsubMsg: (() => void) | null = null;

import { safeStorage } from './utils/storage';

// --- view state ---
const view = ref<'list' | 'term'>('list');
const activePaneId = ref('');
const viewMode = ref<'list' | 'spaces'>((safeStorage.getItem('herdrem_viewmode') as 'list' | 'spaces') || 'list');
const activeSpaceId = ref<string | null>(null);
const showSettings = ref(false);
const showTodoManager = ref(false);
const settingsSection = ref<'shell_cmds' | 'skills' | 'agent_cmds' | null>(null);
const showRelaySwitch = ref(false);
const bootToken = ref('');
const showBootAuth = computed(() => {
  const current = relays.current();
  const targetUrl = current?.url || conn.url;
  const hasMemoryToken = Boolean(conn.token || current?.token);
  return (
    conn.status !== 'connected' &&
    relayNeedsToken(targetUrl) &&
    (conn.bootTimedOut || (!hasMemoryToken && !canCookieReconnect(targetUrl)))
  );
});
const ctxPane = ref<Pane | null>(null);
const ctxOpen = ref(false);
const ctxPos = ref({ x: 0, y: 0 });
const showNewPane = ref(false);
const directoryOverPreview = ref(false);
watch([() => files.browserOpen, () => files.preview], ([browser, preview]) => {
  if (!browser || !preview) directoryOverPreview.value = false;
});
// Pane created via pane_create that is waiting for the poller to learn it
// (so hostFor can route read_pane correctly) before we auto-open it.
let pendingOpenPane: string | null = null;
const now = ref(Math.floor(Date.now() / 1000));
let relativeTimeTimer: ReturnType<typeof setInterval> | null = null;

// --- history / back-button management ---
// States: 'root' (list/spaces grid) → 'space' (inside a space) → 'term' (terminal)
// Each push adds a history entry so the browser/PWA back button works.
// At root, first back shows "press again to exit" toast; second back exits.
const exitToast = ref(false);
let exitToastTimer: ReturnType<typeof setTimeout> | null = null;
// Set when the user confirmed exit: cascading pops skip re-showing the toast
// so stale root entries on the stack get drained until the document leaves.
let exiting = false;

function pushHistory(state: string) {
  exiting = false;
  clearExitToast();
  if (history.state?.view === state) return;
  history.pushState({ view: state }, '');
}

/** clearExitToast drops any pending "press again to exit" state — any
 *  navigation away from root (or back into it) starts a fresh back sequence,
 *  so a stale toast can never fast-track the next back into exiting. */
function clearExitToast() {
  exitToast.value = false;
  if (exitToastTimer) clearTimeout(exitToastTimer);
  exitToastTimer = null;
}

function currentStateName(): string {
  return view.value === 'term' ? 'term' : activeSpaceId.value ? 'space' : 'root';
}

/** closes a topmost overlay (non-view) layer; returns true if something closed */
function closeTopLayer(): boolean {
  if (showTodoManager.value) {
    showTodoManager.value = false;
    return true;
  }
  if (showSettings.value) {
    showSettings.value = false;
    settingsSection.value = null;
    return true;
  }
  if (showNewPane.value) {
    showNewPane.value = false;
    return true;
  }
  if (ctxOpen.value) {
    ctxOpen.value = false;
    return true;
  }
  if (showRelaySwitch.value) {
    showRelaySwitch.value = false;
    return true;
  }
  if (diff.open) {
    diff.close();
    return true;
  }
  if (files.preview && files.browserOpen && directoryOverPreview.value) {
    files.closeBrowser();
    directoryOverPreview.value = false;
    return true;
  }
  if (files.preview) {
    files.closePreview();
    return true;
  }
  if (files.browserOpen) {
    // System back inside the directory tree climbs one level instead of
    // exiting the file browser; the drawer only closes at the sandbox root.
    if (files.canGoUp) {
      files.goUp();
      return true;
    }
    files.closeBrowser();
    return true;
  }
  return false;
}

function onPopState(ev: PopStateEvent) {
  // While the standalone trap is armed it owns back handling entirely;
  // processing the same pop here too would double-close layers.
  if (trap.isActive()) return;
  // Exit in progress: keep draining stale entries until the document leaves.
  if (exiting) {
    history.back();
    return;
  }
  // Close topmost overlay layers first (file preview → file drawer), re-pushing
  // the current view state so history depth stays consistent with the UI.
  if (closeTopLayer()) {
    history.pushState({ view: currentStateName() }, '');
    return;
  }
  const st = ev.state?.view || 'root';
  if (st === 'term') {
    // Navigated forward to term (shouldn't normally happen via back, but handle it)
    return;
  }
  if (st === 'space') {
    // Back from term to space — close terminal, keep space. Re-push the view
    // state so the history stack stays aligned with the UI and the next back
    // lands on a real entry instead of the first one (which would exit).
    if (view.value === 'term') {
      closeTerminal();
      clearExitToast();
      history.pushState({ view: currentStateName() }, '');
    }
    return;
  }
  // st === 'root'
  if (view.value === 'term') {
    closeTerminal();
    clearExitToast();
    // Push a fresh root entry: the next back then hits the "press again to
    // exit" toast instead of being the last entry and exiting the app.
    history.pushState({ view: currentStateName() }, '');
  } else if (activeSpaceId.value) {
    activeSpaceId.value = null;
    clearExitToast();
    history.pushState({ view: currentStateName() }, '');
  } else {
    // Already at root — user pressed back again → show exit toast
    if (exitToast.value) {
      exitToast.value = false;
      exiting = true;
      history.back();
    } else {
      history.pushState({ view: 'root' }, '');
      exitToast.value = true;
      if (exitToastTimer) clearTimeout(exitToastTimer);
      exitToastTimer = setTimeout(() => {
        exitToast.value = false;
      }, 2500);
    }
  }
}

onMounted(() => {
  relativeTimeTimer = setInterval(() => {
    now.value = Math.floor(Date.now() / 1000);
  }, 60_000);

  // Ensure root state exists
  if (!history.state?.view) {
    history.replaceState({ view: 'root' }, '');
  }
  window.addEventListener('popstate', onPopState);

  // PWA: when a new Service Worker takes control, reload once so the page
  // runs under the new SW immediately (prevents stale-asset / install failures).
  // Skip the reload on the FIRST install — the page already has the correct
  // assets from the network, and reloading causes a visible flicker.
  if ('serviceWorker' in navigator) {
    let hadController = !!navigator.serviceWorker.controller;
    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (hadController) {
        window.location.reload();
      }
      hadController = true;
    });
  }
});

onUnmounted(() => {
  if (relativeTimeTimer) clearInterval(relativeTimeTimer);
  window.removeEventListener('popstate', onPopState);
});

// Push history entry when entering a space (for back-button support)
watch(activeSpaceId, (id, oldId) => {
  if (id && !oldId) {
    // Entering a space from grid
    pushHistory('space');
  }
});

// Auto-hide topbar on scroll-down (mobile list view)
const listScrollEl = ref<HTMLElement | null>(null);
const hideHeader = ref(false);
let lastScrollTop = 0;
let suppressScroll = false;
function onListScroll() {
  if (suppressScroll) return;
  if (isDesktop.value) return;
  const el = listScrollEl.value;
  if (!el) return;
  const st = el.scrollTop;
  const maxScroll = el.scrollHeight - el.clientHeight;
  // Don't hide if there's barely any scroll room (hiding would cause jitter)
  if (maxScroll < 120) {
    if (hideHeader.value) {
      suppressScroll = true;
      hideHeader.value = false;
      requestAnimationFrame(() => {
        lastScrollTop = el.scrollTop;
        suppressScroll = false;
      });
    }
    return;
  }
  // Don't toggle at the very bottom — clamping after hide causes jitter
  const atBottom = st >= maxScroll - 2;
  const atTop = st <= 2;
  if (atBottom || atTop) {
    lastScrollTop = st;
    if (atTop && hideHeader.value) {
      suppressScroll = true;
      hideHeader.value = false;
      requestAnimationFrame(() => {
        lastScrollTop = el.scrollTop;
        suppressScroll = false;
      });
    }
    return;
  }
  if (st > 80 && st > lastScrollTop + 4) {
    if (!hideHeader.value) {
      suppressScroll = true;
      hideHeader.value = true;
      requestAnimationFrame(() => {
        lastScrollTop = el.scrollTop;
        suppressScroll = false;
      });
    }
  } else if (st < lastScrollTop - 4 || st < 80) {
    if (hideHeader.value) {
      suppressScroll = true;
      hideHeader.value = false;
      requestAnimationFrame(() => {
        lastScrollTop = el.scrollTop;
        suppressScroll = false;
      });
    }
  } else {
    lastScrollTop = st;
  }
}

import { useLayoutMode } from './composables/useLayoutMode';

const { isDesktop } = useLayoutMode();
watch(isDesktop, (val) => {
  if (val) hideHeader.value = false;
});

const splitMode = computed(() => isDesktop.value && view.value === 'term' && activePaneId.value !== '');
// The docked/float state is the user's choice, toggled only via the directory
// header dock button. directoryOverPreview controls layering (mobile z-index,
// breadcrumb visibility) but never forces float — opening the directory from
// the preview must not change the user's dock preference.
const directoryDocked = computed(() => isDesktop.value && files.browserDocked);

const currentRelayName = computed(() => {
  const r = relays.current();
  return r ? r.name || relayHostLabel(r.url) : relayHostLabel(conn.url);
});

function setViewMode(m: 'list' | 'spaces') {
  viewMode.value = m;
  safeStorage.setItem('herdrem_viewmode', m);
}

function openTerminal(paneId: string) {
  activePaneId.value = paneId;
  view.value = 'term';
  ctxOpen.value = false;
  files.closeBrowser(); // close file browser when switching panes
  files.closePreview(); // and any preview overlay left open
  diff.close(); // and any diff preview left open
  pushHistory('term');
}

function closeTerminal() {
  view.value = 'list';
  activePaneId.value = '';
  // Keep activeSpaceId so SpaceView re-enters the same space on back
}

function goBackFromTerm() {
  // Use history.back() so popstate handler closes terminal consistently
  history.back();
}

// Desktop: a floating directory closes when the user clicks outside it.
function onTermAreaClick(e: MouseEvent) {
  if (!isDesktop.value) return;
  const target = e.target as HTMLElement;
  // Ignore clicks on the term header (buttons, title, status badge)
  if (target.closest('.term-head')) return;

  // File browser (float closes on outside click; docked shares the row)
  if (files.browserOpen && !directoryDocked.value) {
    if (target.closest('.fb-drawer')) return; // inside panel
    files.closeBrowser();
    directoryOverPreview.value = false;
    return;
  }
}

// "‹ all spaces" button: jump straight back to the spaces grid. Direct state
// change + replaceState instead of history.back(), because the stack may hold
// stale entries (e.g. split-term's own back button closes the term without
// popping) that would swallow the back without changing the UI.
function onSpaceBack() {
  if (view.value === 'term') closeTerminal();
  activeSpaceId.value = null;
  history.replaceState({ view: 'root' }, '');
}

/** Create a new tab in a space from within the space view */
function onSpaceNewTab(workspaceId: string, host?: string) {
  createPane('tab', { workspaceId, host });
}

function quickApprove(paneId: string, answer: string) {
  haptic(18);
  ws.send({ type: 'respond', pane_id: paneId, text: answer });
}

function qaTone(payload: string, label?: string): 'yes' | 'no' | 'neutral' {
  const s = `${payload} ${label || ''}`.toLowerCase();
  if (/\byes\b|approve|allow|accept|^y\b/.test(s)) return 'yes';
  if (/\bno\b|deny|reject|skip|^n\b/.test(s)) return 'no';
  return 'neutral';
}
function qaShort(label: string): string {
  return (label.split(/[,(]/)[0].trim() || label).slice(0, 14);
}

// --- context menu ---
function openCtx(pane: Pane, e: Event) {
  e.stopPropagation();
  ctxPane.value = pane;
  const me = e as MouseEvent;
  ctxPos.value = { x: me.clientX, y: me.clientY };
  ctxOpen.value = true;
}
function onCtxAction(action: string, pane: Pane) {
  switch (action) {
    case 'open':
    case 'refresh':
      openTerminal(pane.pane_id);
      break;
    case 'interrupt':
      ws.send({ type: 'send_keys', pane_id: pane.pane_id, keys: 'Ctrl+c' });
      break;
    case 'mark-read':
      ws.send({ type: 'mark_pane_read', pane_id: pane.pane_id });
      break;
    case 'mark-agent':
      ws.send({ type: 'kind_override_set', pane_id: pane.pane_id, host: pane.host, display_agent: 'agent' });
      break;
    case 'diff':
      if (conn.hasFeature('git-diff') && pane.cwd) {
        activePaneId.value = pane.pane_id;
        view.value = 'term';
        diff.openDiff(pane.cwd, pane.host || 'local');
        pushHistory('term');
      }
      break;
  }
}

/** Create a new shell (split), workspace, or tab pane */
function createPane(
  mode: 'shell' | 'workspace' | 'tab',
  opts: {
    fromPaneId?: string;
    workspaceId?: string;
    direction?: 'right' | 'down';
    host?: string;
    cwd?: string;
    label?: string;
  } = {},
) {
  showNewPane.value = false;
  ws.send({
    type: 'pane_create',
    mode,
    pane_id: opts.fromPaneId,
    workspace_id: opts.workspaceId,
    host: opts.host,
    direction: opts.direction,
    cwd: opts.cwd,
    label: opts.label,
  });
}

/** Open the tunnel manager panel from the + dropdown */
function openTunnelPanel() {
  showNewPane.value = false;
  tunnelsStore.togglePanel();
}

/** Split a new shell from the current (or first available) pane. */
function splitNewShell() {
  const from = agents.agents.find((a) => a.pane_id === activePaneId.value) || agents.agents[0];
  if (!from) return;
  createPane('shell', { fromPaneId: from.pane_id, direction: 'right', host: from.host });
}

// --- relay switching ---
function onSwitchRelay(id: string) {
  showRelaySwitch.value = false;
  view.value = 'list';
  switchTo(id);
}

function submitBootToken() {
  const token = bootToken.value.trim();
  if (token) boot(token);
}
function onEditCommands(section: 'shell_cmds' | 'skills' | 'agent_cmds') {
  settingsSection.value = section;
  showSettings.value = true;
}
function onSettingsSwitched() {
  view.value = 'list';
}

// --- file open (M3 preview) ---
function onOpenFile(p: { path: string; line: number | null; paneId: string }) {
  if (!conn.hasFeature('files')) {
    notify.push({ message: `file preview needs relay "files" (M3): ${p.path.split('/').pop()}`, type: 'info' });
    return;
  }
  const pane = agents.agents.find((a) => a.pane_id === p.paneId);
  // resolve relative paths against the pane's cwd
  let full = p.path;
  if (!full.startsWith('/') && pane?.cwd) {
    full = (pane.cwd.replace(/\/+$/, '') + '/' + full).replace(/\/+/g, '/');
  }
  files.openPath(full, pane?.cwd || '/', pane?.host || 'local', p.line);
}

// Opening the directory from the preview (path node or toggle) raises it above
// the preview on every layout: on mobile via the .directory-over-preview class
// (drops the preview z-index), on desktop by rendering the browser as a float
// overlay (z-index 80 > preview 75) through the docked-prop override below.
function onOpenPreviewDirectory(p: { path: string; root: string; host: string }) {
  files.openBrowserAt(p.path, p.root, p.host);
  directoryOverPreview.value = true;
}

function onTogglePreviewDirectory(p: { path: string; root: string; host: string }) {
  if (files.browserOpen) {
    files.closeBrowser();
    directoryOverPreview.value = false;
    return;
  }
  onOpenPreviewDirectory(p);
}

// A float (non-docked) directory is a temporary picker: once a file is opened
// from it, dismiss it so the preview is visible instead of hiding behind the
// overlay. A docked directory stays (it shares the row with the preview).
function onPreviewOpenedFromBrowser() {
  if (isDesktop.value && !directoryDocked.value) {
    files.closeBrowser();
  }
  directoryOverPreview.value = false;
}

// Dock button: the only place that toggles the user's docked/float preference.
function onBrowserDock() {
  files.toggleDocked();
}

onMounted(() => {
  unsubMsg = ws.onMessage((msg: ServerMessage) => {
    handleMessage(msg);
    if (msg.type === 'todos') todo.setAll(msg.todos);
    if (msg.type === 'todo_updated') todo.updateItem(msg.todo, msg.deleted);
    if (msg.type === 'tunnels') tunnelsStore.setTunnels(msg.tunnels || []);
    if (msg.type === 'tunnel_updated') tunnelsStore.updateTunnel(msg.tunnel);
    if (msg.type === 'pane_content') {
      // route preview reads to the previews store, terminal reads to the terminal
      if (msg.purpose === 'preview') previews.setPreview(msg.pane_id, msg.content);
      else if (conn.hasFeature('incremental-terminal'))
        term.setContent(msg.pane_id, msg.content, msg.append === true, msg.rev, msg.base_rev, msg.poll === true);
      else term.setContent(msg.pane_id, msg.content);
    }
    if (msg.type === 'agents') {
      notify.processSnapshot(msg.agents || []);
      // Open the terminal for a freshly created pane once it appears in an
      // agents snapshot (poller has learned its host by then).
      if (pendingOpenPane && msg.agents?.some((a) => a.pane_id === pendingOpenPane)) {
        openTerminal(pendingOpenPane);
        pendingOpenPane = null;
      }
    }
    if (msg.type === 'prefs' || msg.type === 'prefs_updated') prefs.setPrefs(msg.prefs);
    if (msg.type === 'file_listing') files.setListing(msg.path, msg.entries || [], msg.req_id);
    if (msg.type === 'file_content') files.setContent(msg);
    if (msg.type === 'git_diff_content') diff.setDiffContent(msg);
    if (msg.type === 'git_log_result') diff.setLogResult(msg);
    if (msg.type === 'error' && msg.op === 'file_list') files.setListError(msg.req_id);
    if (msg.type === 'error' && msg.op === 'file_read') files.setPreviewError(msg.req_id);
    if (msg.type === 'error' && msg.op === 'git_diff') diff.setDiffError(msg.req_id);
    if (msg.type === 'error' && msg.op === 'git_log') diff.setLogError(msg.req_id);
    if (msg.type === 'error' && msg.op === 'mark_pane_read') {
      notify.push({ message: `failed to mark idle: ${msg.message}`, type: 'info' });
    }
    if (msg.type === 'pane_created') {
      // The poller learns the new pane on its next poll (~3s). Prefer opening
      // immediately when the pane is already in the agents store (the snapshot
      // may have arrived before pane_created); otherwise wait for a
      // snapshot containing it.
      if (agents.agents.some((a) => a.pane_id === msg.pane_id)) {
        openTerminal(msg.pane_id);
      } else {
        pendingOpenPane = msg.pane_id;
      }
      const label = msg.mode === 'workspace' ? 'workspace' : msg.mode === 'tab' ? 'tab' : 'shell';
      notify.push({ message: `new ${label} pane created`, type: 'info' });
    }
    if (msg.type === 'error' && msg.op === 'pane_create') {
      notify.push({ message: `failed to create pane: ${msg.message}`, type: 'info' });
    }
  });
  notify.ensurePermission();
  // PWA: lock portrait on first gesture + intercept back button
  if (isStandalone()) {
    const lockOnce = () => {
      tryLockPortrait();
      document.removeEventListener('pointerdown', lockOnce);
    };
    document.addEventListener('pointerdown', lockOnce, { once: true });
    trap.arm(backHandler);
  }
  boot();

  // Desktop: global click handler to close TodoPanel when clicking outside it.
  // (Settings already has its own backdrop; FileBrowser is handled by onTermAreaClick.)
  function onDocClick(e: MouseEvent) {
    if (!isDesktop.value) return;
    const target = e.target as HTMLElement;
    // Ignore clicks on disconnected elements (unmounted during click handler, e.g. edit button in Todo)
    if (!target || !target.isConnected) return;
    // Ignore clicks on the term header — those are panel toggle buttons
    if (target.closest('.term-head')) return;
    // Close todo panel if open and click is outside it
    const todoPanel = document.querySelector('.todo-panel');
    if (todoPanel && !todoPanel.contains(target)) {
      document.dispatchEvent(new CustomEvent('close-todo-panel'));
    }
  }
  document.addEventListener('click', onDocClick);
});

/** closes the topmost UI layer on back; returns true if something was closed */
function backHandler(): boolean {
  // replaceState normalizes the popped-to entry so stale view entries
  // (e.g. a 'term' entry left after closeTerminal) can't confuse later backs.
  if (closeTopLayer()) {
    history.replaceState({ view: currentStateName() }, '');
    return true;
  }
  if (view.value === 'term') {
    closeTerminal();
    clearExitToast();
    history.replaceState({ view: currentStateName() }, '');
    return true;
  }
  if (activeSpaceId.value) {
    activeSpaceId.value = null;
    clearExitToast();
    history.replaceState({ view: currentStateName() }, '');
    return true;
  }
  // At root: first back shows the exit toast, second back exits for real
  if (exitToast.value) {
    exitToast.value = false;
    exiting = true;
    history.back(); // pop the remaining root entry → actually leave the PWA
    return false; // disarm the trap
  }
  exitToast.value = true;
  if (exitToastTimer) clearTimeout(exitToastTimer);
  exitToastTimer = setTimeout(() => {
    exitToast.value = false;
  }, 2500);
  return true;
}

onUnmounted(() => {
  unsubMsg?.();
  conn.disconnect();
});
</script>

<template>
  <div v-if="showBootAuth" class="boot-splash">
    <div class="boot-splash-inner">
      <img src="/brand-mark.png" alt="" width="64" height="58" aria-hidden="true" />
      <form class="relay-auth-form" @submit.prevent="submitBootToken">
        <h2>relay token required</h2>
        <p>Enter HERDREM_TOKEN for this address.</p>
        <label for="boot-relay-token">Relay token</label>
        <input
          id="boot-relay-token"
          v-model="bootToken"
          class="relay-auth-input"
          type="password"
          autocomplete="current-password"
          required
          autofocus
        />
        <button class="relay-auth-button" type="submit">connect</button>
      </form>
    </div>
  </div>
  <!-- Boot splash: shown only during the initial WS handshake (connecting
       and not yet booted). If the connection fails or hello doesn't arrive
       within 5s (see connection.ts bootTimer), booted becomes true and the
       main UI renders so the user can access relay switch and settings. -->
  <div v-else-if="!conn.booted && conn.status === 'connecting'" class="boot-splash">
    <div class="boot-splash-inner">
      <img src="/brand-mark.png" alt="" width="64" height="58" aria-hidden="true" />
      <div class="boot-spinner"></div>
    </div>
  </div>
  <div v-else class="app">
    <header
      class="topbar"
      :class="{
        'hide-mobile': view === 'term' && !splitMode,
        'auto-hidden': hideHeader,
        'dropdown-open': showRelaySwitch || showNewPane,
      }"
    >
      <div class="brand">
        <img class="brand-mark" src="/brand-mark.png" alt="" width="24" height="22" aria-hidden="true" />
        <h1>Herdrem</h1>
      </div>

      <div class="relay-chip-wrap">
        <button
          class="relay-chip"
          :class="conn.status"
          aria-label="Switch relay"
          @click="showRelaySwitch = !showRelaySwitch"
        >
          <span class="rc-dot" aria-hidden="true"></span>
          <span class="rc-name">{{ currentRelayName }}</span>
          <span class="rc-caret">▾</span>
        </button>
        <div v-if="showRelaySwitch" class="relay-pop" @click.stop>
          <button
            v-for="r in relays.relays"
            :key="r.id"
            class="relay-pop-item"
            :class="{ on: r.id === relays.currentId }"
            @click="onSwitchRelay(r.id)"
          >
            {{ r.name || relayHostLabel(r.url) }}
          </button>
          <button
            class="relay-pop-item manage"
            @click="
              showRelaySwitch = false;
              showSettings = true;
            "
          >
            manage…
          </button>
        </div>
      </div>

      <div v-if="conn.hasFeature('pane-create') || conn.hasFeature('tunnels')" class="new-pane-wrap">
        <button
          class="top-icon"
          :class="{ 'has-badge': tunnelsStore.activeCount > 0 }"
          aria-label="New pane or tunnel"
          title="New pane or tunnel"
          @click="showNewPane = !showNewPane"
        >
          +
        </button>
        <div v-if="showNewPane" class="new-pane-pop" @click.stop>
          <button v-if="conn.hasFeature('pane-create')" class="new-pane-item" @click="createPane('workspace')">
            <span class="np-icon" aria-hidden="true">⊞</span>
            <span class="np-label">New workspace</span>
          </button>
          <button v-if="conn.hasFeature('pane-create')" class="new-pane-item" @click="splitNewShell">
            <span class="np-icon" aria-hidden="true">⎇</span>
            <span class="np-label">New shell</span>
          </button>
          <button v-if="conn.hasFeature('tunnels')" class="new-pane-item" @click="openTunnelPanel">
            <span class="np-icon" aria-hidden="true">⊕</span>
            <span class="np-label">Tunnel</span>
          </button>
        </div>
      </div>

      <button class="top-icon" aria-label="Settings" title="Settings" @click="showSettings = true">⚙</button>
    </header>

    <main
      class="main"
      :class="{
        split: splitMode,
        'files-docked': files.browserOpen && directoryDocked,
        'has-file-preview': files.preview,
        'directory-over-preview': directoryOverPreview,
      }"
    >
      <div v-if="view === 'term' && !splitMode" class="term-wrap" @click="onTermAreaClick">
        <TermView
          :pane-id="activePaneId"
          @back="goBackFromTerm"
          @open-file="onOpenFile"
          @edit-commands="onEditCommands"
          @open-settings="showSettings = true"
        />
      </div>

      <!-- Agent list: sidebar in split mode, standalone on mobile/list view -->
      <div
        v-if="splitMode || view !== 'term'"
        :class="splitMode ? 'split-list' : 'list-wrap'"
        @click="
          showRelaySwitch = false;
          showNewPane = false;
          files.closeBrowser();
        "
      >
        <div class="list-view view-enter">
          <div class="list-head" :class="{ 'auto-hidden': hideHeader }">
            <div class="head-counts">
              <span class="count-group"
                ><span class="count agents">{{ agents.counts.agents }}</span
                ><span class="label">agents</span></span
              >
              <span class="count-sep">·</span>
              <span class="count-group"
                ><span class="count shells">{{ agents.counts.shells }}</span
                ><span class="label">shells</span></span
              >
            </div>
            <div class="head-actions">
              <span v-if="agents.counts.blocked" class="blocked-note">⚠ {{ agents.counts.blocked }}</span>
              <div class="vm-toggle" role="group" aria-label="View and Todo controls">
                <button
                  :class="{ on: viewMode === 'list' }"
                  aria-label="List view"
                  title="List view"
                  @click="setViewMode('list')"
                >
                  ☰
                </button>
                <button
                  :class="{ on: viewMode === 'spaces' }"
                  aria-label="Spaces view"
                  title="Spaces view"
                  @click="setViewMode('spaces')"
                >
                  ⊞
                </button>
                <button
                  class="todo-manager-trigger"
                  aria-label="Manage todos"
                  title="Todo manager"
                  @click.stop="showTodoManager = true"
                >
                  ☑
                </button>
              </div>
            </div>
          </div>

          <AgentFilters :class="{ 'auto-hidden': hideHeader }" />

          <div ref="listScrollEl" class="list-scroll" @scroll.passive="onListScroll">
            <SpaceView
              v-if="viewMode === 'spaces'"
              v-model="activeSpaceId"
              :agents="agents.filtered"
              :now="now"
              :active-pane-id="splitMode ? activePaneId : ''"
              :can-create-pane="conn.hasFeature('pane-create')"
              @open-terminal="openTerminal"
              @back="onSpaceBack"
              @new-tab="onSpaceNewTab"
            />

            <div v-else class="agent-list">
              <div v-if="agents.filtered.length === 0" class="empty">
                <div class="glyph" aria-hidden="true">⌀</div>
                <div class="title">No agents match</div>
                <div class="hint">Adjust filters, start herdr, or install a notification plugin.</div>
              </div>

              <article
                v-for="a in agents.filtered"
                :key="a.pane_id"
                class="agent-card"
                :class="[
                  bucket(a.status),
                  { 'just-done': justDonePanes.has(a.pane_id), active: splitMode && a.pane_id === activePaneId },
                ]"
                role="button"
                tabindex="0"
                :aria-current="splitMode && a.pane_id === activePaneId ? 'true' : undefined"
                :aria-label="`Open terminal for ${a.project || a.agent || a.pane_id}`"
                @click="openTerminal(a.pane_id)"
                @keydown.enter="openTerminal(a.pane_id)"
              >
                <AgentIcon :pane="a" :size="24" />
                <div class="body">
                  <div class="name">{{ paneTitle(a) }}</div>
                  <div class="meta">
                    <span v-if="paneKind(a) === 'shell'" class="kind-tag shell">shell</span>
                    <span v-else-if="displayAgent(a)" class="kind-tag agent">{{ displayAgent(a) }}</span>
                    <span v-if="paneKind(a) === 'shell' && shellLabel(a)" class="shell-label">{{ shellLabel(a) }}</span>
                    <span v-if="workspaceLabel(a)" class="ws-label">{{ workspaceLabel(a) }}</span>
                    <span v-if="a.branch" class="branch">⎇ {{ a.branch }}</span>
                    <span class="pane-id">{{ a.pane_id }}</span>
                    <span v-if="a.host && a.host !== 'local'" class="host">{{ a.host }}</span>
                  </div>
                </div>

                <div v-if="bucket(a.status) === 'blocked'" class="quick-approve" @click.stop>
                  <template v-if="a.options && a.options.length">
                    <button
                      v-for="(opt, i) in a.options.slice(0, 3)"
                      :key="opt"
                      class="qa-btn"
                      :class="qaTone(opt, a.option_labels?.[i])"
                      @click="quickApprove(a.pane_id, opt)"
                    >
                      {{ qaShort(a.option_labels?.[i] || opt) }}
                    </button>
                  </template>
                  <template v-else>
                    <button
                      class="qa-btn yes"
                      aria-label="Approve"
                      title="Approve (y)"
                      @click="quickApprove(a.pane_id, 'y')"
                    >
                      y
                    </button>
                    <button class="qa-btn no" aria-label="Deny" title="Deny (n)" @click="quickApprove(a.pane_id, 'n')">
                      n
                    </button>
                  </template>
                </div>

                <div class="card-right">
                  <span v-if="relTime(a.last_active, now)" class="last-active">{{ relTime(a.last_active, now) }}</span>
                  <button
                    class="ctx-trigger"
                    aria-label="More actions"
                    title="More actions"
                    @click="openCtx(a, $event)"
                  >
                    ⋯
                  </button>
                </div>
              </article>
            </div>
          </div>
        </div>
      </div>

      <div v-if="splitMode" class="split-term" @click="onTermAreaClick">
        <TermView
          :key="activePaneId"
          :pane-id="activePaneId"
          @back="closeTerminal"
          @open-file="onOpenFile"
          @open-settings="showSettings = true"
        />
      </div>
      <FileBrowser
        v-if="files.browserOpen"
        :docked="directoryDocked"
        @close="
          files.closeBrowser();
          directoryOverPreview = false;
        "
        @preview-opened="onPreviewOpenedFromBrowser"
        @dock="onBrowserDock"
      />
      <div v-if="files.preview" class="file-preview-panel">
        <FilePreview
          :directory-open="files.browserOpen"
          @close="files.closePreview()"
          @open-directory="onOpenPreviewDirectory"
          @toggle-directory="onTogglePreviewDirectory"
        />
      </div>
      <div v-if="diff.open" class="diff-preview-panel" :class="{ 'half-screen': diff.halfScreen }">
        <DiffPreview />
      </div>
    </main>

    <ContextMenu
      :pane="ctxPane"
      :open="ctxOpen && !!ctxPane"
      :pos="ctxPos"
      @close="ctxOpen = false"
      @action="onCtxAction"
      v-if="ctxPane"
    />
    <SettingsPanel
      v-if="showSettings"
      :focus-section="settingsSection"
      @close="
        showSettings = false;
        settingsSection = null;
      "
      @switched="onSettingsSwitched"
    />
    <TunnelPanel v-if="tunnelsStore.showPanel" />
    <TodoManager v-if="showTodoManager" @close="showTodoManager = false" />

    <div class="toast-stack" aria-live="polite">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="t.type" @click="notify.dismissToast(t.id)">
        {{ t.message }}
      </div>
      <div v-if="exitToast" class="toast exit-toast" @click="exitToast = false">press back again to exit</div>
    </div>
  </div>
</template>

<style scoped>
.relay-chip-wrap {
  position: relative;
}

/* Desktop split layout — JS-controlled via useLayoutMode; no CSS fallback needed
   since .main.split class is only applied when isDesktop is true. */
.main.split {
  flex-direction: row;
}
.term-wrap {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.list-wrap {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--pane-bg);
  backdrop-filter: var(--glass);
  -webkit-backdrop-filter: var(--glass);
}
.split-list {
  width: 340px;
  flex-shrink: 0;
  border-right: 1px solid var(--border);
  background: var(--pane-bg);
  backdrop-filter: var(--glass);
  -webkit-backdrop-filter: var(--glass);
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.split-list .list-view {
  flex: 1;
  min-height: 0;
}
.split-term {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.file-preview-panel {
  position: absolute;
  inset: 0 0 0 auto;
  z-index: 75;
  width: min(960px, 100%);
  display: flex;
  min-width: 0;
  background: var(--bg);
  border-left: 1px solid var(--border);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.3);
}
/* Diff preview overlays the file preview (and the directory), so it sits
   above both. Like the file preview it covers the terminal area. */
.diff-preview-panel {
  position: absolute;
  inset: 0 0 0 auto;
  z-index: 70;
  width: min(960px, 100%);
  display: flex;
  min-width: 0;
  background: var(--bg);
  border-left: 1px solid var(--border);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.3);
}
/* Half-screen mode: dock to the bottom, covering only 50% of the height.
   The terminal above stays visible so the user can read diff + terminal
   side by side. Overrides the full-screen `inset` and `width`. */
.diff-preview-panel.half-screen {
  inset: auto 0 0 0;
  width: 100%;
  height: 50%;
  border-left: none;
  border-top: 1px solid var(--border-strong);
  box-shadow: 0 -8px 24px rgba(0, 0, 0, 0.3);
}
/* A float (non-docked) directory overlays the preview on every layout, so it
   must sit above the preview panel. :deep() is required because FileBrowser is
   a fragment component (two roots: <aside> + <Teleport>), so Vue does NOT add
   this file's scope attribute to its root <aside> — a plain scoped selector
   would silently fail to match. The desktop docked layout instead shares the
   row (file-preview-panel right: 360px). */
.main.has-file-preview > :deep(.fb-drawer:not(.docked)) {
  top: 49px;
  z-index: 80;
}
@media (min-width: 900px) {
  .main.split .file-preview-panel {
    left: 340px;
    width: auto;
  }
  .main.files-docked .file-preview-panel {
    right: 360px;
  }
  .main.split .diff-preview-panel {
    left: 340px;
    width: auto;
  }
  .main.files-docked .diff-preview-panel {
    right: 360px;
  }
  /* Half-screen overrides desktop split/docked positioning: it always
     spans the full width at the bottom, regardless of split mode. */
  .main.split .diff-preview-panel.half-screen {
    left: 0;
    width: 100%;
  }
  .main.files-docked .diff-preview-panel.half-screen {
    right: 0;
  }
}
@media (max-width: 899px) {
  .main.directory-over-preview .file-preview-panel {
    z-index: 65;
  }
  .diff-preview-panel {
    width: 100%;
  }
}
.relay-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--text-dim);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 4px 11px;
  max-width: 200px;
  transition:
    color 0.25s,
    border-color 0.25s,
    background 0.25s;
}
.relay-chip:hover {
  border-color: var(--border-strong);
  color: var(--text);
}
.rc-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--faint);
  flex-shrink: 0;
  transition: background 0.25s;
}
.relay-chip.connected {
  color: var(--green);
  border-color: rgba(52, 211, 153, 0.35);
  background: var(--green-soft);
}
.relay-chip.connected .rc-dot {
  background: var(--green);
}
.relay-chip.connecting {
  color: var(--orange);
  border-color: rgba(251, 146, 60, 0.35);
  background: var(--orange-soft);
}
.relay-chip.connecting .rc-dot {
  background: var(--orange);
}
.relay-chip.disconnected .rc-dot {
  background: var(--red);
}
.rc-name {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.rc-caret {
  color: var(--faint);
}
.relay-pop {
  position: absolute;
  top: calc(100% + 6px);
  right: 0;
  z-index: 50;
  min-width: 180px;
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  padding: 5px;
  animation: view-in 0.14s ease both;
}
.relay-pop-item {
  display: block;
  width: 100%;
  text-align: left;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--text-dim);
  background: transparent;
  border: none;
  border-radius: var(--radius);
  padding: 8px 11px;
}
.relay-pop-item:hover {
  background: var(--surface-3);
  color: var(--text);
}
.relay-pop-item.on {
  color: var(--green);
}
.relay-pop-item.manage {
  color: var(--blue);
  border-top: 1px solid var(--border);
  border-radius: 0 0 var(--radius) var(--radius);
}

/* New-pane popover */
.new-pane-wrap {
  position: relative;
}
.new-pane-pop {
  position: absolute;
  top: calc(100% + 6px);
  right: 0;
  z-index: 50;
  min-width: 200px;
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  padding: 5px;
  animation: view-in 0.14s ease both;
}
.new-pane-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  text-align: left;
  background: transparent;
  border: none;
  border-radius: var(--radius);
  padding: 9px 11px;
  transition: background 0.12s;
}
.new-pane-item:hover {
  background: var(--surface-3);
}
.new-pane-item .np-icon {
  width: 16px;
  text-align: center;
  color: var(--muted);
  font-size: var(--text-md);
  flex-shrink: 0;
}
.new-pane-item .np-label {
  font-family: var(--font-display);
  font-size: var(--text-sm);
  color: var(--text);
  font-weight: 600;
}

.top-icon {
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  font-size: var(--text-md);
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.top-icon:hover {
  color: var(--text);
}
.top-icon.has-badge {
  color: #38bdf8;
  border-color: rgba(56, 189, 248, 0.35);
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.vm-toggle {
  display: flex;
  gap: 4px;
}
.vm-toggle button {
  width: 30px;
  height: 30px;
  display: grid;
  place-items: center;
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  font-size: var(--text-sm);
}
.vm-toggle button.on {
  color: var(--blue);
  border-color: rgba(96, 165, 250, 0.5);
  background: var(--blue-soft);
}
.todo-manager-trigger {
  /* Layout and default visual state come from .vm-toggle button. */
}
.todo-manager-trigger:hover {
  color: var(--green);
  border-color: rgba(52, 211, 153, 0.5);
}

.host {
  color: var(--amber);
}
.branch {
  color: var(--blue);
}
.ws-label {
  color: var(--text-dim);
  font-weight: 600;
}
.kind-tag {
  font-weight: 600;
}
.kind-tag.shell {
  color: var(--muted);
}
.kind-tag.agent {
  color: var(--green);
}
.shell-label {
  color: var(--text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 120px;
}
.card-right {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 4px;
  flex-shrink: 0;
}
.last-active {
  color: var(--faint);
  font-size: var(--text-2xs);
  font-family: var(--font-mono);
}
.head-counts {
  display: flex;
  align-items: baseline;
  gap: 4px;
}
.count-group {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
}
.count-sep {
  color: var(--faint);
}

.ctx-trigger {
  width: 30px;
  height: 30px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  color: var(--faint);
  background: transparent;
  border: none;
  border-radius: var(--radius);
  font-size: var(--text-lg);
  line-height: 1;
}
.ctx-trigger:hover {
  color: var(--text);
  background: var(--surface-3);
}

.agent-card.just-done {
  animation: done-flash 1.5s ease both;
}
@keyframes done-flash {
  0%,
  100% {
    box-shadow: none;
  }
  25%,
  60% {
    box-shadow:
      0 0 0 2px rgba(52, 211, 153, 0.7),
      0 0 18px rgba(52, 211, 153, 0.4);
  }
}

.toast-stack {
  position: fixed;
  bottom: 18px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 90;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: center;
  pointer-events: none;
}
.toast {
  pointer-events: auto;
  cursor: pointer;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--text);
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-radius: 999px;
  padding: 8px 16px;
  box-shadow: var(--shadow-2);
  animation: view-in 0.2s ease both;
  max-width: min(90vw, 480px);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.toast.blocked {
  border-color: rgba(248, 113, 113, 0.5);
  color: #ffd9d9;
}
.toast.done {
  border-color: rgba(52, 211, 153, 0.5);
  color: #c9f5e3;
}
.toast.exit-toast {
  border-color: rgba(251, 191, 36, 0.5);
  color: #fde68a;
}

/* Boot splash — shown until the first WS hello arrives. Matches the inline
   splash in index.html so the transition from pre-Vue to Vue is seamless. */
.boot-splash {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg);
  z-index: 9999;
}
.boot-splash-inner {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 24px;
}
.boot-spinner {
  width: 48px;
  height: 48px;
  border: 3px solid var(--border);
  border-top-color: var(--blue);
  border-radius: 50%;
  animation: bp-spin 0.8s linear infinite;
}
.relay-auth-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: min(86vw, 360px);
  color: var(--text-dim);
  text-align: left;
}
.relay-auth-form h2 {
  margin: 0;
  color: var(--text);
  font-size: var(--text-lg);
  text-align: center;
}
.relay-auth-form p {
  margin: 0 0 4px;
  font-size: var(--text-sm);
  text-align: center;
}
.relay-auth-form label {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--muted);
}
.relay-auth-input {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  color: var(--text);
  background: var(--term-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  font-family: var(--font-mono);
  outline: none;
}
.relay-auth-input:focus { border-color: rgba(96,165,250,.5); }
.relay-auth-button {
  padding: 10px 12px;
  color: var(--bg);
  background: var(--blue);
  border: 0;
  border-radius: var(--radius);
  font-family: var(--font-mono);
  font-weight: 700;
}
@keyframes bp-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
