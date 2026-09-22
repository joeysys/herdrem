// Desktop split view: switching the sidebar from list to spaces mode should
// land inside the workspace containing the open terminal, not at the grid.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { nextTick } from 'vue';
import App from './App.vue';
import { useLayoutMode } from './composables/useLayoutMode';
import { useAgentsStore, useConnectionStore } from './stores/connection';
import { useTodoStore } from './stores/todo';

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
  await new Promise((r) => setTimeout(r, 20));
  await nextTick();
  await nextTick();
}

function seedAgents() {
  useAgentsStore().setAgents([
    { pane_id: 'p1', project: 'proj-a', cwd: '/a', host: 'local', status: 'idle', kind: 'agent', workspace_id: 'ws-a', last_active: 1 },
    { pane_id: 'p2', project: 'proj-b', cwd: '/b', host: 'local', status: 'idle', kind: 'agent', workspace_id: 'ws-b', last_active: 2 },
    { pane_id: 'p3', project: 'proj-b2', cwd: '/b2', host: 'local', status: 'idle', kind: 'shell', workspace_id: 'ws-b', last_active: 0 },
  ]);
}

async function openTerm(wrapper: ReturnType<typeof mount>, paneId: string) {
  const card = wrapper.findAll('.agent-card').find((c) => c.text().includes(paneId));
  expect(card, `card for ${paneId}`).toBeTruthy();
  await card!.trigger('click');
  await flush();
  expect(wrapper.find('.split-term').exists()).toBe(true);
}

beforeEach(() => {
  setActivePinia(createPinia());
  localStorage.clear();
  // safeStorage's in-memory fallback survives localStorage.clear(), so a
  // previous test can leak viewmode='spaces' — seed it deterministically
  // (localStorage wins over the fallback map on read).
  localStorage.setItem('herdrem_viewmode', 'list');
  useLayoutMode().setPreference('desktop');
  useConnectionStore().booted = true;
});

afterEach(() => {
  useLayoutMode().setPreference('auto');
});

describe('spaces view auto-locate (desktop split)', () => {
  it('enters the workspace of the open terminal when switching to spaces', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    await openTerm(wrapper, 'p2');
    await wrapper.get('[aria-label="Spaces view"]').trigger('click');
    await flush();

    const inside = wrapper.find('.space-inside');
    expect(inside.exists()).toBe(true);
    expect(inside.find('.space-id').text()).toBe('ws-b');
    // the open terminal's pane card is highlighted inside the space
    expect(inside.find('.agent-card.active .pane-id').text()).toBe('p2');
    // panes from other workspaces are not listed
    expect(inside.text()).not.toContain('proj-a');
  });

  it('shows the grid when no terminal is open', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    await wrapper.get('[aria-label="Spaces view"]').trigger('click');
    await flush();

    expect(wrapper.find('.space-grid').exists()).toBe(true);
    expect(wrapper.find('.space-inside').exists()).toBe(false);
  });

  it('‹ all spaces returns to the grid (existing behavior: closes the term)', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    await openTerm(wrapper, 'p2');
    await wrapper.get('[aria-label="Spaces view"]').trigger('click');
    await flush();
    expect(wrapper.find('.space-inside').exists()).toBe(true);

    await wrapper.get('.space-back').trigger('click');
    await flush();

    // The spaces grid replaces the inside view; per onSpaceBack the terminal
    // closes too (the button means "jump straight back to all spaces").
    expect(wrapper.find('.space-grid').exists()).toBe(true);
    expect(wrapper.findAll('.space-card').length).toBe(2);
    expect(wrapper.find('.split-term').exists()).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
  });

  it('opens the todo manager scoped to the active space', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    useTodoStore().setAll([
      { id: 't1', body: 'ws-a note', binding: { host: 'local', workspace_id: 'ws-a' }, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      { id: 't2', body: 'ws-b note', binding: { host: 'local', workspace_id: 'ws-b' }, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    ]);
    await flush();

    await openTerm(wrapper, 'p2');
    await wrapper.get('[aria-label="Spaces view"]').trigger('click');
    await flush();
    expect(wrapper.find('.space-inside .space-id').text()).toBe('ws-b');

    await wrapper.get('[aria-label="Manage todos"]').trigger('click');
    await flush();

    const manager = wrapper.find('.todo-manager');
    expect(manager.exists()).toBe(true);
    expect(manager.text()).toContain('ws-b note');
    expect(manager.text()).not.toContain('ws-a note');
    // The workspace select is preset to the current space's binding.
    const select = manager.find('select[aria-label="Workspace scope: filters list, binds new todos"]');
    expect((select.element as HTMLSelectElement).value).toBe('local\u0000ws-b');
  });

  it('scopes todos to the host the space card shows (filtered-first pane)', async () => {
    const wrapper = mount(App);
    await flush();
    // One workspace_id shared by two hosts; filtered sorts by last_active so
    // h1-pane leads the ws-x group and SpaceView shows the space on h1.
    useAgentsStore().setAgents([
      { pane_id: 'h2-pane', project: 'x', cwd: '/x', host: 'h2', status: 'idle', kind: 'agent', workspace_id: 'ws-x', last_active: 1 },
      { pane_id: 'h1-pane', project: 'x', cwd: '/y', host: 'h1', status: 'idle', kind: 'agent', workspace_id: 'ws-x', last_active: 9 },
    ]);
    await flush();

    await openTerm(wrapper, 'h2-pane');
    await wrapper.get('[aria-label="Spaces view"]').trigger('click');
    await flush();
    expect(wrapper.find('.space-inside .space-id').text()).toBe('ws-x');

    await wrapper.get('[aria-label="Manage todos"]').trigger('click');
    await flush();

    const select = wrapper.find('.todo-manager select[aria-label="Workspace scope: filters list, binds new todos"]');
    expect((select.element as HTMLSelectElement).value).toBe('h1\u0000ws-x');
  });
});
