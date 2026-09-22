// TodoManager: the workspace select doubles as the list filter, and the
// manager opens scoped when App hands it the active space's binding.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import TodoManager from './TodoManager.vue'
import { useAgentsStore } from '../../stores/connection'
import { useTodoStore } from '../../stores/todo'
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

function seed() {
  useAgentsStore().setAgents([
    { pane_id: 'a1', kind: 'agent', agent: 'claude', status: 'idle', host: 'local', workspace_id: 'ws-a', workspace_label: 'Alpha' },
    { pane_id: 'b1', kind: 'agent', agent: 'claude', status: 'idle', host: 'local', workspace_id: 'ws-b', workspace_label: 'Beta' },
  ])
  useTodoStore().setAll([
    { id: 't1', body: 'alpha note', binding: { host: 'local', workspace_id: 'ws-a' }, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    { id: 't2', body: 'beta note', binding: { host: 'local', workspace_id: 'ws-b' }, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    { id: 't3', body: 'loose note', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
  ])
}

const wsKey = (id: string) => `local\u0000${id}`

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.mocked(ws.send).mockClear()
  seed()
})

describe('TodoManager workspace scoping', () => {
  it('shows every section when no scope is given', () => {
    const wrapper = mount(TodoManager)
    const text = wrapper.find('.tm-list').text()
    expect(text).toContain('alpha note')
    expect(text).toContain('beta note')
    expect(text).toContain('loose note')
  })

  it('defaults to the initialBinding workspace and shows only its items', async () => {
    const wrapper = mount(TodoManager, {
      props: { initialBinding: { host: 'local', workspace_id: 'ws-a' } },
    })
    // initialBinding is applied in onMounted, so the select settles one tick later.
    await nextTick()

    const select = wrapper.find('select[aria-label="Workspace scope: filters list, binds new todos"]')
    expect((select.element as HTMLSelectElement).value).toBe(wsKey('ws-a'))

    const text = wrapper.find('.tm-list').text()
    expect(text).toContain('alpha note')
    expect(text).not.toContain('beta note')
    expect(text).not.toContain('loose note')
  })

  it('re-scopes the list when the select changes', async () => {
    const wrapper = mount(TodoManager, {
      props: { initialBinding: { host: 'local', workspace_id: 'ws-a' } },
    })
    const select = wrapper.find('select[aria-label="Workspace scope: filters list, binds new todos"]')

    await select.setValue(wsKey('ws-b'))
    expect(wrapper.find('.tm-list').text()).toContain('beta note')
    expect(wrapper.find('.tm-list').text()).not.toContain('alpha note')

    await select.setValue('')
    const text = wrapper.find('.tm-list').text()
    expect(text).toContain('alpha note')
    expect(text).toContain('beta note')
    expect(text).toContain('loose note')
  })

  it('binds new todos to the scoped workspace', async () => {
    const wrapper = mount(TodoManager, {
      props: { initialBinding: { host: 'local', workspace_id: 'ws-a' } },
    })

    await wrapper.find('textarea[aria-label="New todo"]').setValue('scoped item')
    await wrapper.find('.tm-add').trigger('submit')

    expect(ws.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'todo_set',
        body: 'scoped item',
        binding: expect.objectContaining({ host: 'local', workspace_id: 'ws-a' }),
      }),
    )
  })

  it('Ctrl+Enter adds an unbound todo under the All workspaces scope', async () => {
    const wrapper = mount(TodoManager)
    const textarea = wrapper.find('textarea[aria-label="New todo"]')
    await textarea.setValue('loose item')
    await textarea.trigger('keydown', { key: 'Enter', ctrlKey: true })
    const sent = vi.mocked(ws.send).mock.lastCall?.[0] as { type: string; body: string; binding?: unknown }
    expect(sent).toMatchObject({ type: 'todo_set', body: 'loose item' })
    expect(sent.binding).toBeUndefined()

    // The empty option's label must disclose the unbound consequence.
    expect(wrapper.find('option[value=""]').text()).toContain('unbound')
  })

  it('ignores an initialBinding for a workspace that has no panes', () => {
    const wrapper = mount(TodoManager, {
      props: { initialBinding: { host: 'local', workspace_id: 'ws-gone' } },
    })
    const select = wrapper.find('select[aria-label="Workspace scope: filters list, binds new todos"]')
    expect((select.element as HTMLSelectElement).value).toBe('')
  })
})
