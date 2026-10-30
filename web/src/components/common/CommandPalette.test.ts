import { beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import CommandPalette from './CommandPalette.vue'
import type { Pane } from '../../api/types'

function pane(over: Partial<Pane>): Pane {
  return {
    pane_id: 'w1:p1',
    kind: 'agent',
    agent: 'claude',
    workspace_id: 'ws1',
    workspace_label: 'demo',
    host: 'local',
    status: 'working',
    ...over,
  }
}

const PANES: Pane[] = [
  pane({
    pane_id: 'w1:p1',
    agent: 'claude',
    status: 'blocked',
    workspace_id: 'ws1',
    workspace_label: 'demo',
    project: 'demo',
  }),
  pane({
    pane_id: 'w1:p2',
    agent: 'codex',
    status: 'working',
    workspace_id: 'ws1',
    workspace_label: 'demo',
    project: 'demo',
  }),
  pane({
    pane_id: 'w2:p3',
    kind: 'shell',
    agent: '',
    status: 'done',
    workspace_id: 'ws2',
    workspace_label: 'ops',
    project: 'ops',
  }),
]

function mountPalette(props: Record<string, unknown> = {}) {
  return mount(CommandPalette, {
    props: { panes: PANES, canCreate: true, canWorktree: true, ...props },
    attachTo: document.body,
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('CommandPalette', () => {
  it('lists commands, workspaces, and panes grouped by workspace', () => {
    const wrapper = mountPalette()
    const groups = wrapper.findAll('.pal-group').map((g) => g.text())
    expect(groups).toEqual(['Commands', 'demo', 'ops'])
    expect(wrapper.findAll('.pal-item.pane')).toHaveLength(3)
    expect(wrapper.findAll('.pal-item.workspace')).toHaveLength(2)
    expect(wrapper.findAll('.pal-item.command').length).toBeGreaterThanOrEqual(4); // new-workspace, new-shell, 2 worktrees
    wrapper.unmount()
  })

  it('filters panes by typed query and emits openPane on Enter', async () => {
    const wrapper = mountPalette()
    const input = wrapper.get('.pal-input')
    await input.setValue('codex')
    const items = wrapper.findAll('.pal-item.pane')
    expect(items).toHaveLength(1)
    expect(items[0].text()).toContain('codex')
    await input.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('openPane')).toEqual([['w1:p2']])
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('navigates with arrows, Home, End, and Enter', async () => {
    const wrapper = mountPalette()
    const input = wrapper.get('.pal-input')
    expect(wrapper.findAll('.pal-item')[0].classes()).toContain('active')

    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(wrapper.findAll('.pal-item')[1].classes()).toContain('active')

    await input.trigger('keydown', { key: 'End' })
    const items = wrapper.findAll('.pal-item')
    expect(items[items.length - 1].classes()).toContain('active')

    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(wrapper.findAll('.pal-item')[0].classes()).toContain('active')

    await input.trigger('keydown', { key: 'ArrowUp' })
    expect(items[items.length - 1].classes()).toContain('active')

    await input.trigger('keydown', { key: 'Home' })
    expect(wrapper.findAll('.pal-item')[0].classes()).toContain('active')

    await input.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('command')).toEqual([['new-workspace']])
    wrapper.unmount()
  })

  it('closes on Ctrl+K or Cmd+K', async () => {
    const wrapper = mountPalette()
    const input = wrapper.get('.pal-input')
    await input.trigger('keydown', { key: 'k', ctrlKey: true })
    expect(wrapper.emitted('close')).toHaveLength(1)
    await input.trigger('keydown', { key: 'k', metaKey: true })
    expect(wrapper.emitted('close')).toHaveLength(2)
    wrapper.unmount()
  })

  it('filters panes by status chip', async () => {
    const wrapper = mountPalette()
    const chips = wrapper.findAll('.pal-chip')
    await chips[1].trigger('click'); // blocked
    const items = wrapper.findAll('.pal-item.pane')
    expect(items).toHaveLength(1)
    expect(items[0].text()).toContain('claude')
    expect(items[0].text()).toContain('blocked')
    wrapper.unmount()
  })

  it('keeps Enter/Space on a filter chip to the chip (F5 regression)', async () => {
    const wrapper = mountPalette()
    const chips = wrapper.findAll('.pal-chip')
    // Enter/Space/arrows on a focused chip must not pick the highlighted row
    // or close the palette — nav keys only act on the search input.
    await chips[1].trigger('keydown', { key: 'Enter' })
    await chips[1].trigger('keydown', { key: ' ' })
    await chips[1].trigger('keydown', { key: 'ArrowDown' })
    expect(wrapper.emitted('command')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('openPane')).toBeUndefined()
    expect(wrapper.findAll('.pal-item')[0].classes()).toContain('active')
    wrapper.unmount()
  })

  it('workspace rows emit openSpace and worktree rows emit workspaceMenu', async () => {
    const wrapper = mountPalette()
    const wsRows = wrapper.findAll('.pal-item.workspace')
    await wsRows[0].trigger('click')
    expect(wrapper.emitted('openSpace')).toEqual([['ws1']])
    const wt = wrapper.findAll('.pal-item.command').filter((r) => r.text().includes('worktree'))
    await wt[0].trigger('click')
    expect(wrapper.emitted('workspaceMenu')).toEqual([['ws1']])
    wrapper.unmount()
  })

  it('Escape closes; no matches shows the empty state', async () => {
    const wrapper = mountPalette()
    const input = wrapper.get('.pal-input')
    await input.setValue('zzz-no-such-thing')
    expect(wrapper.find('.pal-empty').exists()).toBe(true)
    await input.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
