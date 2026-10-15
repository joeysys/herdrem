// SpaceView inside-space cards carry the same approval-inbox affordances as
// the list view: blocked prompt excerpt and the done-card mark-read action.
// The two card markups are independent implementations, so both are tested.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import SpaceView from './SpaceView.vue'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'connected',
  },
}))

const panes = [
  {
    pane_id: 'p1',
    project: 'proj-blocked',
    cwd: '/a',
    host: 'local',
    status: 'blocked',
    kind: 'agent' as const,
    agent: 'claude',
    workspace_id: 'ws-a',
    last_active: 3,
    prompt: 'Allow edits to src/main.ts?\n[y] yes  [n] no',
    options: ['y', 'n'],
    option_labels: ['Yes', 'No'],
  },
  {
    pane_id: 'p2',
    project: 'proj-done',
    cwd: '/b',
    host: 'local',
    status: 'done',
    kind: 'agent' as const,
    agent: 'claude',
    workspace_id: 'ws-a',
    last_active: 2,
  },
]

function cardByPaneId(wrapper: ReturnType<typeof mount>, paneId: string) {
  const card = wrapper.findAll('.agent-card').find((c) => c.find('.pane-id').text() === paneId)
  expect(card, `card for ${paneId}`).toBeTruthy()
  return card!
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.mocked(ws.send).mockClear()
})

describe('SpaceView inside-space cards', () => {
  function mountInside() {
    return mount(SpaceView, {
      props: { agents: panes, now: Date.now(), modelValue: 'ws-a' },
    })
  }

  it('renders the pane prompt on the blocked card', () => {
    const wrapper = mountInside()
    const card = cardByPaneId(wrapper, 'p1')
    const prompt = card.find('.qa-prompt')
    expect(prompt.exists()).toBe(true)
    expect(prompt.text()).toContain('Allow edits to src/main.ts?')
  })

  it('sends mark_pane_read from the done card without emitting openTerminal', async () => {
    const wrapper = mountInside()
    const card = cardByPaneId(wrapper, 'p2')
    const btn = card.find('button[aria-label="Mark as read"]')
    expect(btn.exists()).toBe(true)
    await btn.trigger('click')

    expect(ws.send).toHaveBeenCalledWith({ type: 'mark_pane_read', pane_id: 'p2' })
    expect(wrapper.emitted('openTerminal')).toBeUndefined()
  })

  it('quick-approve sends respond without emitting openTerminal', async () => {
    const wrapper = mountInside()
    const card = cardByPaneId(wrapper, 'p1')
    await card.find('.qa-btn.yes').trigger('click')

    expect(ws.send).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'respond', pane_id: 'p1', text: 'y' }),
    )
    expect(wrapper.emitted('openTerminal')).toBeUndefined()
  })

  it('Enter keydown on the mark-read button does not emit openTerminal', async () => {
    const wrapper = mountInside()
    const card = cardByPaneId(wrapper, 'p2')
    await card.find('button[aria-label="Mark as read"]').trigger('keydown.enter')

    expect(wrapper.emitted('openTerminal')).toBeUndefined()
  })

  it('Enter keydown on the card itself emits openTerminal', async () => {
    const wrapper = mountInside()
    await cardByPaneId(wrapper, 'p2').trigger('keydown.enter')

    expect(wrapper.emitted('openTerminal')).toEqual([['p2']])
  })

  it('groups by branch+path: same branch name at different cwds stays separate', () => {
    // Branch is resolved per-cwd server-side, so "main" at /repo/a and "main"
    // at /repo/b are different repos' main — one group per (cwd, branch).
    localStorage.setItem('herdrem_group_by_branch', 'true')
    const wrapper = mount(SpaceView, {
      props: {
        now: Date.now(),
        modelValue: 'ws-a',
        agents: [
          { ...panes[1], pane_id: 'a1', cwd: '/repo/a', branch: 'main' },
          { ...panes[1], pane_id: 'a2', cwd: '/repo/b', branch: 'main' },
          { ...panes[1], pane_id: 'a3', cwd: '/repo/a', branch: 'main' },
        ],
      },
    })

    const headers = wrapper.findAll('.branch-header')
    expect(headers.length).toBe(2)
    expect(headers[0].find('.branch-count').text()).toBe('2')
    expect(headers[1].find('.branch-count').text()).toBe('1')
    // Identical branch names are disambiguated by the shortest distinct cwd
    // suffix, and the full path stays available on hover.
    expect(wrapper.findAll('.branch-cwd').map((n) => n.text())).toEqual(['…/a', '…/b'])
    expect(wrapper.findAll('.branch-cwd')[0].attributes('title')).toBe('/repo/a')
  })

  it('widens the cwd suffix until it disambiguates colliding basenames', () => {
    localStorage.setItem('herdrem_group_by_branch', 'true')
    const wrapper = mount(SpaceView, {
      props: {
        now: Date.now(),
        modelValue: 'ws-a',
        agents: [
          { ...panes[1], pane_id: 'a1', cwd: '/x/api', branch: 'main' },
          { ...panes[1], pane_id: 'a2', cwd: '/y/api', branch: 'main' },
          { ...panes[1], pane_id: 'a3', cwd: '/a/proj', branch: 'main' },
          { ...panes[1], pane_id: 'a4', cwd: '/b/a/proj', branch: 'main' },
        ],
      },
    })

    expect(wrapper.findAll('.branch-cwd').map((n) => n.text())).toEqual([
      '/x/api', // all 2 segments needed — complete path, no ellipsis
      '/y/api',
      '/a/proj', // whole path — every shorter suffix also ends the longer path
      '/b/a/proj',
    ])
  })

  it('labels every group in a multi-cwd space, even a unique branch name', () => {
    // Project identity is positional: the user cannot tell "main @ /repo/a"
    // from "main @ /repo/b" by the branch alone, so each group names its path.
    localStorage.setItem('herdrem_group_by_branch', 'true')
    const wrapper = mount(SpaceView, {
      props: {
        now: Date.now(),
        modelValue: 'ws-a',
        agents: [
          { ...panes[1], pane_id: 'a1', cwd: '/repo/a', branch: 'main' },
          { ...panes[1], pane_id: 'a2', cwd: '/repo/b', branch: 'dev' },
          { ...panes[1], pane_id: 'a3', cwd: '/repo/c' }, // no branch
        ],
      },
    })

    expect(wrapper.findAll('.branch-header').length).toBe(3)
    expect(wrapper.findAll('.branch-cwd').map((n) => n.text())).toEqual(['…/a', '…/b', '…/c'])
  })

  it('shows no cwd suffix when all panes share one cwd', () => {
    localStorage.setItem('herdrem_group_by_branch', 'true')
    const wrapper = mount(SpaceView, {
      props: {
        now: Date.now(),
        modelValue: 'ws-a',
        agents: [
          { ...panes[1], pane_id: 'a1', cwd: '/repo/a', branch: 'main' },
          { ...panes[1], pane_id: 'a2', cwd: '/repo/a', branch: 'dev' },
        ],
      },
    })

    expect(wrapper.findAll('.branch-header').length).toBe(2)
    expect(wrapper.findAll('.branch-cwd').length).toBe(0)
  })

  it('grid order follows workspace recency, not the blocked-first input order', () => {
    // `filtered` pins blocked panes; App.vue hands that sorted array straight
    // to SpaceView — a workspace containing a blocked pane must not jump the
    // grid queue ahead of a more recently active workspace.
    const wrapper = mount(SpaceView, {
      props: {
        now: Date.now(),
        agents: [
          { ...panes[0], pane_id: 'b1', workspace_id: 'ws-old', last_active: 1 },
          { pane_id: 'i1', project: 'proj-idle', cwd: '/i', host: 'local', status: 'idle', kind: 'agent', agent: 'claude', workspace_id: 'ws-new', last_active: 100 },
        ],
      },
    })

    const order = wrapper.findAll('.space-card .sc-id').map((n) => n.text())
    expect(order).toEqual(['ws-new', 'ws-old'])
  })
})
