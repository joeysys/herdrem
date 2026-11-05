// TodoManager: the workspace select doubles as the list filter, and the
// manager opens scoped when App hands it the active space's binding.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import TodoManager from './TodoManager.vue'
import TodoPanel from './TodoPanel.vue'
import { useAgentsStore, useConnectionStore } from '../../stores/connection'
import { useTodoStore, addScope, itemScope } from '../../stores/todo'
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
  vi.mocked(ws.send).mockClear().mockReturnValue(true)
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

describe('TodoManager req-echo UI', () => {
  beforeEach(() => {
    useConnectionStore().setHello('0.4.0', ['req-echo'])
  })

  it('keeps the draft in a pending row until the echo confirms it', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoManager)
    await wrapper.find('textarea[aria-label="New todo"]').setValue('queued note')
    await wrapper.find('.tm-add').trigger('submit')
    await nextTick()

    const row = wrapper.find('.tm-pending')
    expect(row.exists()).toBe(true)
    expect(row.text()).toContain('queued note')
    expect(row.text()).toContain('sending…')

    todo.confirm(todo.pendingFor(addScope(undefined))!.reqId)
    await nextTick()
    expect(wrapper.find('.tm-pending').exists()).toBe(false)
    expect((wrapper.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('')
  })

  it('a failed echo shows the message and hands the draft back', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoManager)
    await wrapper.find('textarea[aria-label="New todo"]').setValue('queued note')
    await wrapper.find('.tm-add').trigger('submit')
    await nextTick()

    const op = todo.pendingFor(addScope(undefined))!
    todo.fail(op.reqId, 'body too large')
    await nextTick()

    expect(wrapper.find('.tm-pending').exists()).toBe(false)
    expect(wrapper.find('.tm-msg').text()).toContain('body too large')
    expect((wrapper.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('queued note')
  })

  it('a failed rebind snaps the select back to the old workspace', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoManager)
    const item = wrapper.findAll('.tm-item').find((el) => el.text().includes('alpha note'))!
    const select = item.find('select[aria-label="Rebind todo"]')
    expect((select.element as HTMLSelectElement).value).toBe(wsKey('ws-a'))

    await select.setValue(wsKey('ws-b'))
    await nextTick()
    // The override shows the pick while the op is in flight.
    expect((select.element as HTMLSelectElement).value).toBe(wsKey('ws-b'))

    const op = todo.pendingFor(itemScope('t1'))!
    expect(op.kind).toBe('rebind')
    todo.fail(op.reqId, 'binding too large')
    await nextTick()

    expect((select.element as HTMLSelectElement).value).toBe(wsKey('ws-a'))
    expect(item.find('.tm-item-state').text()).toContain('binding too large')
  })

  it('item controls lock while the op is in flight', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoManager)
    const item = wrapper.findAll('.tm-item').find((el) => el.text().includes('alpha note'))!
    await item.find('button:not(.danger)').trigger('click') // Edit
    await item.find('.tm-actions button').trigger('click') // save
    await nextTick()

    expect(todo.pendingFor(itemScope('t1'))?.outcome).toBe('pending')
    expect(item.find('textarea[aria-label="Edit todo"]').attributes('readonly')).toBeDefined()
    expect(item.find('.tm-item-state').text()).toContain('saving…')
  })

  it('a refused add send reports not connected on the form', async () => {
    vi.mocked(ws.send).mockReturnValue(false)
    const wrapper = mount(TodoManager)
    await wrapper.find('textarea[aria-label="New todo"]').setValue('queued note')
    await wrapper.find('.tm-add').trigger('submit')
    await nextTick()

    expect(wrapper.find('.tm-msg').text()).toContain('not connected')
    expect((wrapper.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('queued note')
  })

  it('a refused save reports not connected on the item and stays editing', async () => {
    vi.mocked(ws.send).mockReturnValue(false)
    const wrapper = mount(TodoManager)
    const item = wrapper.findAll('.tm-item').find((el) => el.text().includes('alpha note'))!
    await item.find('button:not(.danger)').trigger('click') // Edit
    await item.find('textarea[aria-label="Edit todo"]').setValue('edited alpha')
    await item.find('.tm-actions button').trigger('click') // save
    await nextTick()

    expect(item.find('textarea[aria-label="Edit todo"]').exists()).toBe(true)
    expect(item.find('.tm-item-state').text()).toContain('not connected')
  })

  it('a refused delete reports not connected on the item', async () => {
    vi.mocked(ws.send).mockReturnValue(false)
    const wrapper = mount(TodoManager)
    const item = wrapper.findAll('.tm-item').find((el) => el.text().includes('alpha note'))!
    await item.find('button.danger').trigger('click') // Delete
    await nextTick()

    expect(item.find('.tm-item-state').text()).toContain('not connected')
    expect(useTodoStore().todos.t1).toBeDefined()
  })

  it('a refused rebind send snaps the select back and reports not connected', async () => {
    vi.mocked(ws.send).mockReturnValue(false)
    const wrapper = mount(TodoManager)
    const item = wrapper.findAll('.tm-item').find((el) => el.text().includes('alpha note'))!
    const select = item.find('select[aria-label="Rebind todo"]')

    await select.setValue(wsKey('ws-b'))
    await nextTick()

    expect((select.element as HTMLSelectElement).value).toBe(wsKey('ws-a'))
    expect(item.find('.tm-item-state').text()).toContain('not connected')
  })

  it('a shared add scope resolves for both mounted components — neither steals it', async () => {
    const todo = useTodoStore()
    const wsBinding = { host: 'local', workspace_id: 'ws-a' }
    const panel = mount(TodoPanel, { props: { binding: wsBinding } })
    const manager = mount(TodoManager, { props: { initialBinding: wsBinding } })
    await nextTick()

    await manager.find('textarea[aria-label="New todo"]').setValue('shared note')
    await manager.find('.tm-add').trigger('submit')
    await nextTick()
    const op = todo.pendingFor(addScope(wsBinding))!
    expect(op.outcome).toBe('pending')

    todo.confirm(op.reqId)
    await nextTick()
    expect(panel.find('.tp-pending').exists()).toBe(false)
    expect((manager.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('')
  })

  it('a resolved add for another workspace never touches this draft', async () => {
    const todo = useTodoStore()
    const manager = mount(TodoManager, { props: { initialBinding: { host: 'local', workspace_id: 'ws-a' } } })
    await nextTick()
    await manager.find('textarea[aria-label="New todo"]').setValue('my draft')

    vi.mocked(ws.send).mockReturnValue(false)
    todo.addItem({ host: 'local', workspace_id: 'ws-b' }, 'foreign note')
    await nextTick()

    expect((manager.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('my draft')
    expect(manager.find('.tm-msg').exists()).toBe(false)
  })

  it('clears the submitted draft when its echo arrives after the scope filter changes', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoManager, { props: { initialBinding: { host: 'local', workspace_id: 'ws-a' } } })
    await nextTick()
    const select = wrapper.find('select[aria-label="Workspace scope: filters list, binds new todos"]')
    await wrapper.find('textarea[aria-label="New todo"]').setValue('for alpha')
    await wrapper.find('.tm-add').trigger('submit')
    const op = todo.pendingFor(addScope({ host: 'local', workspace_id: 'ws-a' }))!

    await select.setValue(wsKey('ws-b'))
    todo.confirm(op.reqId)
    await nextTick()
    expect((wrapper.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('')

    await select.setValue(wsKey('ws-a'))
    await nextTick()
    expect((wrapper.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('')
    expect(todo.pendingFor(addScope({ host: 'local', workspace_id: 'ws-a' }))?.outcome).toBe('confirmed')
  })

  it('a once-owned scope stops being consumed after its op resolves', async () => {
    const todo = useTodoStore()
    const wsA = { host: 'local', workspace_id: 'ws-a' }
    const manager = mount(TodoManager, { props: { initialBinding: wsA } })
    await nextTick()

    await manager.find('textarea[aria-label="New todo"]').setValue('managed note')
    await manager.find('.tm-add').trigger('submit')
    await nextTick()
    todo.confirm(todo.pendingFor(addScope(wsA))!.reqId)
    await nextTick()
    expect((manager.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('')

    // Filter elsewhere; a foreign panel then fails on the scope we used to own.
    await manager.find('select[aria-label="Workspace scope: filters list, binds new todos"]').setValue(wsKey('ws-b'))
    vi.mocked(ws.send).mockReturnValue(false)
    const panel = mount(TodoPanel, { props: { binding: wsA } })
    await panel.find('.tp-new').setValue('panel draft')
    await panel.find('.tp-add-btn').trigger('click')
    await nextTick()

    expect((manager.find('textarea[aria-label="New todo"]').element as HTMLTextAreaElement).value).toBe('')
    expect(manager.find('.tm-msg').exists()).toBe(false)
  })
})
