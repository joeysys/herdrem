// Reproduction/regression tests for mobile system-back handling while the
// file browser (directory tree) is open.
//
// Bug: on mobile, pressing system back from the directory tree exits the
// file browser (or worse) instead of climbing one directory level.
//
// Desired behavior from the directory screen:
//   1. tapping a file → close the directory panel and show the preview
//      (mobile: the panel is always an overlay, whatever state it was in)
//   2. in a subdirectory → go up one level, keep the tree
//   3. at sandbox root   → close the file browser (back to terminal)
//   4. no overlays left  → keep closing terminal / layers, never exit the app
//
// A preview opened from a terminal path is independent of the directory:
// one back closes the preview and returns directly to the terminal. A preview
// opened from the directory also returns to the terminal — opening the file
// already closed the panel.
//
// Back must never exit the app without the "press again to exit" toast:
// every view-level back re-pushes a matching history entry so the next back
// still lands on a real entry instead of the first one (which would exit).

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { nextTick } from 'vue';
import App from './App.vue';
import { useLayoutMode } from './composables/useLayoutMode';
import { useAgentsStore, useConnectionStore } from './stores/connection';
import { useFilesStore } from './stores/files';

vi.mock('./api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'connected',
  },
}));

async function flush() {
  // Give jsdom's async history traversal time to fire popstate, then let Vue re-render.
  await new Promise((r) => setTimeout(r, 20));
  await nextTick();
  await nextTick();
}

/** counts popstate events so tests can tell whether a back still landed on a
 *  real history entry or fell off the first one (which would exit the app). */
function countPops() {
  let pops = 0;
  const onPop = () => pops++;
  window.addEventListener('popstate', onPop);
  return {
    get count() {
      return pops;
    },
    stop() {
      window.removeEventListener('popstate', onPop);
    },
  };
}

function seedAgent() {
  const agents = useAgentsStore();
  agents.setAgents([
    {
      pane_id: 'p1',
      project: 'demo',
      cwd: '/root/proj',
      host: 'local',
      status: 'idle',
      kind: 'agent',
      last_active: 1,
    },
  ]);
}

/** Mark the connection as booted so App.vue renders the main UI instead of
 *  the boot splash. */
function bootApp() {
  useConnectionStore().booted = true;
}

async function enterTerminal(wrapper: ReturnType<typeof mount>) {
  bootApp();
  seedAgent();
  await flush();
  const card = wrapper.find('.agent-card');
  expect(card.exists()).toBe(true);
  await card.trigger('click');
  await flush();
  expect(wrapper.find('.term-wrap').exists()).toBe(true);
}

async function openTreeAtSubdir(wrapper: ReturnType<typeof mount>) {
  const files = useFilesStore();
  files.openBrowser('/root/proj', 'local');
  files.setListing('/root/proj', [{ name: 'sub', type: 'dir', size: 0 }]);
  await flush();
  expect(wrapper.find('.fb-drawer').exists()).toBe(true);
  // navigate into a subdirectory (same as clicking the dir entry)
  files.navigate('/root/proj/sub');
  files.setListing('/root/proj/sub', []);
  await flush();
  expect(files.path).toBe('/root/proj/sub');
}

describe('system back from the directory tree (mobile browser)', () => {
  it('climbs directories before closing the tree, and never exits the app', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    await openTreeAtSubdir(wrapper);

    const files = useFilesStore();
    const pops = countPops();

    // back in a subdirectory goes UP, the tree stays open
    history.back();
    await flush();
    expect(files.browserOpen).toBe(true);
    expect(files.path).toBe('/root/proj');
    expect(wrapper.find('.term-wrap').exists()).toBe(true);
    expect(pops.count).toBe(1);

    // back at the sandbox root closes the file browser, terminal stays
    history.back();
    await flush();
    expect(files.browserOpen).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(true);
    expect(pops.count).toBe(2);

    // next back closes the terminal
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    expect(wrapper.find('.agent-card').exists()).toBe(true);
    expect(pops.count).toBe(3);

    // next back shows the exit toast instead of exiting
    history.back();
    await flush();
    expect(wrapper.find('.agent-card').exists()).toBe(true);
    expect(pops.count).toBe(4);

    // next back confirms the exit (drains to the first entry)
    history.back();
    await flush();
    expect(pops.count).toBe(5);

    // nothing left to pop — this is where a real PWA would leave
    history.back();
    await flush();
    expect(pops.count).toBe(5);

    pops.stop();
    wrapper.unmount();
    await flush();
  });
});

describe('system back from the independent preview (mobile browser)', () => {
  it('opened from the tree: panel closes, back closes preview and returns to the terminal', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    await openTreeAtSubdir(wrapper);

    const files = useFilesStore();
    files.setListing('/root/proj/sub', [{ name: 'a.txt', type: 'file', size: 1 }]);
    await flush();

    // tap a file in the tree — the directory panel closes and the preview
    // opens as the top mobile screen, whatever state the panel was in
    await wrapper.find('.fb-entry.file').trigger('click');
    await flush();
    expect(files.preview?.path).toBe('/root/proj/sub/a.txt');
    expect(files.browserOpen).toBe(false);
    expect(wrapper.find('.fp-path').exists()).toBe(true);
    expect(wrapper.find('.fb-drawer').exists()).toBe(false);

    // one back: preview closes, terminal is behind it (no directory screen)
    history.back();
    await flush();
    expect(files.preview).toBeNull();
    expect(files.browserOpen).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(true);

    // next back closes the terminal
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    expect(wrapper.find('.agent-card').exists()).toBe(true);

    wrapper.unmount();
    await flush();
  });

  it('opened from a terminal file link: returns straight to the terminal, skipping the directory tree', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    const files = useFilesStore();
    // clicking a file link in the terminal
    files.openPath('/root/proj/src/a.txt', '/root/proj', 'local', null);
    await flush();
    expect(files.preview).not.toBeNull();
    expect(files.browserOpen).toBe(false);

    // one back: preview closes; no unrelated directory screen appears
    history.back();
    await flush();
    expect(files.preview).toBeNull();
    expect(files.browserOpen).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(true);

    // next back closes the terminal
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    expect(wrapper.find('.agent-card').exists()).toBe(true);

    wrapper.unmount();
    await flush();
  });

  it('opens a path node over the preview without closing it', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    const files = useFilesStore();
    files.openPath('/root/proj/sub/a.txt', '/root/proj', 'local', null);
    await flush();

    await wrapper.find('.fp-dir').trigger('click');
    await flush();
    expect(files.preview?.path).toBe('/root/proj/sub/a.txt');
    expect(files.browserOpen).toBe(true);
    expect(files.path).toBe('/root/proj');
    expect(wrapper.find('.fb-crumbs').exists()).toBe(true);
    expect(wrapper.find('.term-wrap').exists()).toBe(true);

    history.back();
    await flush();
    expect(files.browserOpen).toBe(false);
    expect(files.preview?.path).toBe('/root/proj/sub/a.txt');

    wrapper.unmount();
    await flush();
  });

  it('opens the containing directory from the preview toggle', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    const files = useFilesStore();
    files.openPath('/root/proj/sub/a.txt', '/root/proj', 'local', null);
    await flush();

    const toggle = wrapper.find('.directory-toggle');
    expect(toggle.attributes('aria-pressed')).toBe('false');
    await toggle.trigger('click');
    await flush();

    expect(files.browserOpen).toBe(true);
    expect(files.path).toBe('/root/proj/sub');
    expect(files.preview?.path).toBe('/root/proj/sub/a.txt');

    wrapper.unmount();
    await flush();
  });

  it('closes the mobile directory when a file is opened from it', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    const files = useFilesStore();
    files.openPath('/root/proj/sub/a.txt', '/root/proj', 'local', null);
    await flush();
    await wrapper.find('.fp-dir').trigger('click');
    files.setListing('/root/proj', [{ name: 'b.txt', type: 'file', size: 1 }]);
    await flush();

    // even when the directory was raised over an existing preview, picking a
    // file dismisses the panel so the new preview is visible
    expect(wrapper.find('.main').classes()).toContain('directory-over-preview');
    await wrapper.find('.fb-entry.file').trigger('click');
    await flush();

    expect(files.preview?.path).toBe('/root/proj/b.txt');
    expect(files.browserOpen).toBe(false);
    expect(wrapper.find('.main').classes()).not.toContain('directory-over-preview');

    wrapper.unmount();
    await flush();
  });
});

describe('back never exits without the exit-toast confirmation (mobile browser)', () => {
  it('keeps re-pushing view entries so back always hits the toast first', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);

    // close the terminal → list (no toast yet)
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    expect(wrapper.find('.agent-card').exists()).toBe(true);
    expect(wrapper.find('.exit-toast').exists()).toBe(false);

    // list → exit toast shown; the app is still alive, NOT exited.
    // (Without the fix this back lands on the first history entry and the
    // app would exit here — no toast, no way back.)
    history.back();
    await flush();
    expect(wrapper.find('.exit-toast').exists()).toBe(true);
    expect(wrapper.find('.agent-card').exists()).toBe(true);

    // toast → confirmed exit (drains history toward the first entry)
    history.back();
    await flush();
    expect(wrapper.find('.exit-toast').exists()).toBe(false);

    wrapper.unmount();
    await flush();
  });

  it('a stale exit toast does not fast-track the next back into exiting', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);

    // back → list, back → exit toast shown
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    history.back();
    await flush();
    expect(wrapper.find('.exit-toast').exists()).toBe(true);

    // user immediately re-enters a terminal (before the 2.5s toast timer)
    seedAgent();
    await flush();
    await wrapper.find('.agent-card').trigger('click');
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(true);
    expect(wrapper.find('.exit-toast').exists()).toBe(false);

    // back from the terminal → list again
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    expect(wrapper.find('.agent-card').exists()).toBe(true);

    // back must show the toast again — NOT exit because of the stale toast.
    // (Without the fix the stale toast would have been consumed here and the
    // exit sequence started — the next back would leave the app, no toast.)
    history.back();
    await flush();
    expect(wrapper.find('.agent-card').exists()).toBe(true);
    expect(wrapper.find('.exit-toast').exists()).toBe(true);

    wrapper.unmount();
    await flush();
  });
});

describe('system back from the directory tree (installed PWA)', () => {
  it('climbs directories before closing the tree, and never exits the app', async () => {
    (window as unknown as { __setStandalone: (v: boolean) => void }).__setStandalone(true);
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    await openTreeAtSubdir(wrapper);

    const files = useFilesStore();
    const pops = countPops();

    // back in a subdirectory goes UP, the tree stays open
    history.back();
    await flush();
    expect(files.browserOpen).toBe(true);
    expect(files.path).toBe('/root/proj');
    expect(wrapper.find('.term-wrap').exists()).toBe(true);
    expect(pops.count).toBe(1);

    // back at the sandbox root closes the file browser, terminal stays
    history.back();
    await flush();
    expect(files.browserOpen).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(true);
    expect(pops.count).toBe(2);

    // next back closes the terminal
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    expect(wrapper.find('.agent-card').exists()).toBe(true);
    expect(pops.count).toBe(3);

    // next back shows the exit toast instead of exiting
    history.back();
    await flush();
    expect(wrapper.find('.agent-card').exists()).toBe(true);
    expect(pops.count).toBe(4);

    pops.stop();
    wrapper.unmount();
    (window as unknown as { __setStandalone: (v: boolean) => void }).__setStandalone(false);
    await flush();
  });
});

describe('system back from the independent preview (installed PWA)', () => {
  it('returns straight to the terminal, skipping the directory tree', async () => {
    (window as unknown as { __setStandalone: (v: boolean) => void }).__setStandalone(true);
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();

    await enterTerminal(wrapper);
    const files = useFilesStore();
    files.openPath('/root/proj/src/a.txt', '/root/proj', 'local', null);
    await flush();
    expect(files.preview).not.toBeNull();

    // one back: preview closes and terminal stays
    history.back();
    await flush();
    expect(files.preview).toBeNull();
    expect(files.browserOpen).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(true);

    // next back closes the terminal
    history.back();
    await flush();
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
    expect(wrapper.find('.agent-card').exists()).toBe(true);

    wrapper.unmount();
    (window as unknown as { __setStandalone: (v: boolean) => void }).__setStandalone(false);
    await flush();
  });
});

describe('desktop file panel placement', () => {
  it('keeps the selected float mode when a preview path opens the directory', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();
    await enterTerminal(wrapper);

    const layout = useLayoutMode();
    layout.setPreference('desktop');
    const files = useFilesStore();
    files.browserDocked = false;
    files.openPath('/root/proj/sub/a.txt', '/root/proj', 'local', null);
    await flush();

    await wrapper.find('.fp-dir').trigger('click');
    await flush();
    expect(files.browserOpen).toBe(true);
    expect(files.browserDocked).toBe(false);
    expect(wrapper.find('.fb-drawer').classes()).not.toContain('docked');
    expect(files.preview?.path).toBe('/root/proj/sub/a.txt');

    wrapper.unmount();
    layout.setPreference('auto');
    await flush();
  });

  it('keeps a docked directory open when a file is opened from it', async () => {
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await flush();
    await enterTerminal(wrapper);

    const layout = useLayoutMode();
    layout.setPreference('desktop');
    const files = useFilesStore();
    files.browserDocked = true;
    files.openBrowser('/root/proj', 'local');
    files.setListing('/root/proj', [{ name: 'a.txt', type: 'file', size: 1 }]);
    await flush();

    await wrapper.find('.fb-entry.file').trigger('click');
    await flush();

    // docked shares the row with the preview — the panel stays
    expect(files.preview?.path).toBe('/root/proj/a.txt');
    expect(files.browserOpen).toBe(true);

    wrapper.unmount();
    layout.setPreference('auto');
    await flush();
  });
});
