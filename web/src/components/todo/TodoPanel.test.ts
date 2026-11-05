// TodoPanel pending row: an echoed send keeps the draft out of the input box
// and shows it as an in-flight row; the row resolves on confirm/fail and the
// outcome decides whether the draft clears or bounces back for a retry.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import TodoPanel from './TodoPanel.vue'
import { useTodoStore, addScope } from '../../stores/todo'
import { useConnectionStore } from '../../stores/connection'
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

const binding = { host: 'local', workspace_id: 'ws-a', workspace_label: 'A' }

async function submitNote(wrapper: ReturnType<typeof mount>, text: string) {
  await wrapper.find('.tp-new').setValue(text)
  await wrapper.find('.tp-add-btn').trigger('click')
  await nextTick()
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear().mockReturnValue(true)
  useConnectionStore().setHello('0.4.0', ['req-echo'])
})

describe('TodoPanel req-echo UI', () => {
  it('shows the sent body as a pending row and locks the input', async () => {
    const wrapper = mount(TodoPanel, { props: { binding } })
    await submitNote(wrapper, 'draft note')

    const row = wrapper.find('.tp-pending')
    expect(row.exists()).toBe(true)
    expect(row.text()).toContain('draft note')
    expect(row.text()).toContain('sending…')
    expect(wrapper.find('.tp-new').attributes('readonly')).toBeDefined()
    expect(wrapper.find('.tp-add-btn').attributes('disabled')).toBeDefined()
  })

  it('a confirmed echo clears the draft and drops the row', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoPanel, { props: { binding } })
    await submitNote(wrapper, 'draft note')

    const op = todo.pendingFor(addScope(binding))!
    todo.confirm(op.reqId)
    await nextTick()

    expect(wrapper.find('.tp-pending').exists()).toBe(false)
    expect((wrapper.find('.tp-new').element as HTMLTextAreaElement).value).toBe('')
  })

  it('a failed echo shows the message and hands the draft back', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoPanel, { props: { binding } })
    await submitNote(wrapper, 'draft note')

    const op = todo.pendingFor(addScope(binding))!
    todo.fail(op.reqId, 'body too large')
    await nextTick()

    // The placeholder is gone; the message line stays until the next submit.
    expect(wrapper.find('.tp-pending').exists()).toBe(false)
    expect(wrapper.find('.tp-msg').text()).toContain('body too large')
    expect((wrapper.find('.tp-new').element as HTMLTextAreaElement).value).toBe('draft note')
  })

  it('clears the draft immediately when the relay has no req-echo', async () => {
    useConnectionStore().setHello('0.4.0', [])
    const wrapper = mount(TodoPanel, { props: { binding } })
    await submitNote(wrapper, 'draft note')

    expect(wrapper.find('.tp-pending').exists()).toBe(false)
    expect((wrapper.find('.tp-new').element as HTMLTextAreaElement).value).toBe('')
  })

  it('shows not connected on a failed send — with or without req-echo', async () => {
    vi.mocked(ws.send).mockReturnValue(false)
    const wrapper = mount(TodoPanel, { props: { binding } })
    await submitNote(wrapper, 'draft note')
    expect(wrapper.find('.tp-msg').text()).toContain('not connected')
    expect((wrapper.find('.tp-new').element as HTMLTextAreaElement).value).toBe('draft note')
    wrapper.unmount()

    useConnectionStore().setHello('0.4.0', [])
    const legacy = mount(TodoPanel, { props: { binding } })
    await submitNote(legacy, 'legacy draft')
    expect(legacy.find('.tp-msg').text()).toContain('not connected')
    expect((legacy.find('.tp-new').element as HTMLTextAreaElement).value).toBe('legacy draft')
  })

  it('does not clear input that changed after the send when the echo lands', async () => {
    const todo = useTodoStore()
    const wrapper = mount(TodoPanel, { props: { binding } })
    await submitNote(wrapper, 'draft note')
    // readonly blocks typing, not programmatic edits — the confirm must only
    // clear text that is still exactly what was submitted.
    await wrapper.find('.tp-new').setValue('draft note — extended')
    todo.confirm(todo.pendingFor(addScope(binding))!.reqId)
    await nextTick()
    expect((wrapper.find('.tp-new').element as HTMLTextAreaElement).value).toBe('draft note — extended')
  })

  it('refuses a second send while an add is in flight, Ctrl+Enter included', async () => {
    const wrapper = mount(TodoPanel, { props: { binding } })
    await submitNote(wrapper, 'draft note')
    await wrapper.find('.tp-new').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(vi.mocked(ws.send)).toHaveBeenCalledTimes(1)
  })

  it('a reopened panel shows the pending row and blocks another submit', async () => {
    useTodoStore().addItem(binding, 'parked draft')
    const wrapper = mount(TodoPanel, { props: { binding } })
    await nextTick()

    const row = wrapper.find('.tp-pending')
    expect(row.exists()).toBe(true)
    expect(row.text()).toContain('parked draft')
    expect(wrapper.find('.tp-new').attributes('readonly')).toBeDefined()
    expect(wrapper.find('.tp-add-btn').attributes('disabled')).toBeDefined()

    await wrapper.find('.tp-new').setValue('another')
    await wrapper.find('.tp-add-btn').trigger('click')
    expect(vi.mocked(ws.send)).toHaveBeenCalledTimes(1)
  })

  it('a late echo after unconfirmed still clears the draft and message', async () => {
    vi.useFakeTimers()
    try {
      const todo = useTodoStore()
      const wrapper = mount(TodoPanel, { props: { binding } })
      await submitNote(wrapper, 'draft note')
      const op = todo.pendingFor(addScope(binding))!

      vi.advanceTimersByTime(10_000)
      await nextTick()
      expect(wrapper.find('.tp-msg').text()).toContain('Unconfirmed')

      todo.confirm(op.reqId)
      await nextTick()
      expect(wrapper.find('.tp-msg').exists()).toBe(false)
      expect((wrapper.find('.tp-new').element as HTMLTextAreaElement).value).toBe('')
    } finally {
      vi.useRealTimers()
    }
  })
})
