// Approval inbox (list view): blocked cards render the pane prompt so approval
// is not a blind tap, and done agent cards expose a mark-read archive action.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { nextTick } from 'vue';
import App from './App.vue';
import { useLayoutMode } from './composables/useLayoutMode';
import { useAgentsStore, useConnectionStore } from './stores/connection';
import { ws } from './api/ws';

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
    {
      pane_id: 'p1',
      project: 'proj-blocked',
      cwd: '/a',
      host: 'local',
      status: 'blocked',
      kind: 'agent',
      workspace_id: 'ws-a',
      last_active: 3,
      prompt: 'Allow edits to src/main.ts?\n[y] yes  [n] no',
      options: ['y', 'n'],
      option_labels: ['Yes', 'No'],
    },
    { pane_id: 'p2', project: 'proj-done', cwd: '/b', host: 'local', status: 'done', kind: 'agent', workspace_id: 'ws-a', last_active: 2 },
    { pane_id: 'p3', project: 'proj-done-shell', cwd: '/c', host: 'local', status: 'done', kind: 'shell', workspace_id: 'ws-a', last_active: 1 },
  ]);
}

function cardByPaneId(wrapper: ReturnType<typeof mount>, paneId: string) {
  const card = wrapper.findAll('.agent-card').find((c) => c.find('.pane-id').text() === paneId);
  expect(card, `card for ${paneId}`).toBeTruthy();
  return card!;
}

beforeEach(() => {
  setActivePinia(createPinia());
  localStorage.clear();
  localStorage.setItem('herdrem_viewmode', 'list');
  useLayoutMode().setPreference('desktop');
  useConnectionStore().booted = true;
  vi.mocked(ws.send).mockClear();
});

afterEach(() => {
  useLayoutMode().setPreference('auto');
});

describe('approval inbox cards', () => {
  it('renders the pane prompt on blocked cards', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    const card = cardByPaneId(wrapper, 'p1');
    const prompt = card.find('.qa-prompt');
    expect(prompt.exists()).toBe(true);
    expect(prompt.text()).toContain('Allow edits to src/main.ts?');
  });

  it('omits .qa-prompt when the pane carries no prompt', async () => {
    const wrapper = mount(App);
    await flush();
    useAgentsStore().setAgents([
      { pane_id: 'p9', project: 'proj-noprompt', cwd: '/x', host: 'local', status: 'blocked', kind: 'agent', workspace_id: 'ws-a', last_active: 1 },
    ]);
    await flush();

    const card = cardByPaneId(wrapper, 'p9');
    expect(card.find('.qa-prompt').exists()).toBe(false);
    // The quick-approve row still renders.
    expect(card.find('.quick-approve').exists()).toBe(true);
  });

  it('sends mark_pane_read from a done agent card without opening the terminal', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    const card = cardByPaneId(wrapper, 'p2');
    const btn = card.find('button[aria-label="Mark as read"]');
    expect(btn.exists()).toBe(true);
    await btn.trigger('click');
    await flush();

    expect(ws.send).toHaveBeenCalledWith({ type: 'mark_pane_read', pane_id: 'p2' });
    // @click.stop must keep the card's openTerminal handler from firing.
    expect(wrapper.find('.split-term').exists()).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
  });

  it('does not offer mark-read on shell panes', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    const card = cardByPaneId(wrapper, 'p3');
    expect(card.find('button[aria-label="Mark as read"]').exists()).toBe(false);
  });

  it('renders prompt and approve buttons for waiting panes too', async () => {
    // The relay enriches waiting/confirm panes the same as blocked (isBlocked
    // covers block|wait|confirm); the card must treat them as approval states.
    const wrapper = mount(App);
    await flush();
    useAgentsStore().setAgents([
      {
        pane_id: 'p8',
        project: 'proj-waiting',
        cwd: '/w',
        host: 'local',
        status: 'waiting',
        kind: 'agent',
        agent: 'claude',
        workspace_id: 'ws-a',
        last_active: 1,
        prompt: 'Proceed with deploy?',
        options: ['y', 'n'],
      },
    ]);
    await flush();

    const card = cardByPaneId(wrapper, 'p8');
    expect(card.find('.qa-prompt').text()).toContain('Proceed with deploy?');
    expect(card.find('.quick-approve').exists()).toBe(true);
  });

  it('hides Mark as read on non-canonical done statuses the relay rejects', async () => {
    // bucket() maps complete/finish to done, but AckDone only accepts the
    // canonical 'done' — the button must not appear where it would fail.
    const wrapper = mount(App);
    await flush();
    useAgentsStore().setAgents([
      { pane_id: 'p7', project: 'proj-completed', cwd: '/d', host: 'local', status: 'completed', kind: 'agent', agent: 'claude', workspace_id: 'ws-a', last_active: 1 },
    ]);
    await flush();

    const card = cardByPaneId(wrapper, 'p7');
    expect(card.find('button[aria-label="Mark as read"]').exists()).toBe(false);
  });

  it('Enter on the mark-read button does not open the terminal', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    const btn = cardByPaneId(wrapper, 'p2').find('button[aria-label="Mark as read"]');
    await btn.trigger('keydown.enter');
    await flush();

    // The article's openTerminal keydown must not fire for nested controls.
    expect(wrapper.find('.split-term').exists()).toBe(false);
    expect(wrapper.find('.term-wrap').exists()).toBe(false);
  });

  it('Enter on the card itself still opens the terminal', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    await cardByPaneId(wrapper, 'p2').trigger('keydown.enter');
    await flush();

    expect(wrapper.find('.split-term').exists()).toBe(true);
  });

  it('quick-approve still sends respond on a blocked card', async () => {
    const wrapper = mount(App);
    await flush();
    seedAgents();
    await flush();

    const card = cardByPaneId(wrapper, 'p1');
    await card.find('.qa-btn.yes').trigger('click');
    await flush();

    expect(ws.send).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'respond', pane_id: 'p1', text: 'y' }),
    );
  });
});
