// todo req-echo: a sent mutation parks in the pending table until its req_id
// echoes back — confirm resolves it, fail carries the server's message, and a
// timeout or socket drop marks it unconfirmed (may have landed; never a
// silent retry). Without the negotiated feature nothing parks at all.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useTodoStore, addScope, itemScope } from './todo'
import { useConnectionStore } from './connection'
import { notePendingDisconnect } from './pendingOps'
import { ws } from '../api/ws'

vi.mock('../api/ws', () => ({
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
const lastSent = () => vi.mocked(ws.send).mock.lastCall?.[0] as { req_id?: string; type: string }

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear().mockReturnValue(true)
  useConnectionStore().setHello('0.4.0', ['req-echo'])
})

afterEach(() => {
  vi.useRealTimers()
})

describe('todo req-echo pending ops', () => {
  it('sends req_id and parks the op until the echo lands', () => {
    const todo = useTodoStore()
    const res = todo.addItem(binding, 'first note')
    expect(res.ok).toBe(true)
    if (!res.ok) return
    expect(lastSent()).toMatchObject({ type: 'todo_set', body: 'first note', req_id: res.reqId })
    const op = todo.pendingFor(addScope(binding))
    expect(op?.outcome).toBe('pending')
    expect(op?.snapshot).toBe('first note')
  })

  it('refuses a second op on the same scope while one is pending', () => {
    const todo = useTodoStore()
    todo.addItem(binding, 'first')
    expect(todo.addItem(binding, 'second')).toEqual({ ok: false, reason: 'busy' })
    expect(ws.send).toHaveBeenCalledTimes(1)
  })

  it('a different scope can still submit while the add scope is busy', () => {
    const todo = useTodoStore()
    todo.addItem(binding, 'first')
    const res = todo.setItem({ id: 't1', body: 'edit', binding })
    expect(res.ok).toBe(true)
    expect(ws.send).toHaveBeenCalledTimes(2)
  })

  it('confirm resolves the op; unknown ids are ignored', () => {
    const todo = useTodoStore()
    const res = todo.addItem(binding, 'first')
    if (!res.ok || !res.reqId) return
    expect(todo.confirm('someone-elses-id')).toBeUndefined()
    expect(todo.pendingFor(addScope(binding))?.outcome).toBe('pending')
    expect(todo.confirm(res.reqId)?.outcome).toBe('confirmed')
    todo.ack(addScope(binding))
    expect(todo.pendingFor(addScope(binding))).toBeUndefined()
  })

  it('fail marks the op failed with the server message', () => {
    const todo = useTodoStore()
    const res = todo.addItem(binding, 'first')
    if (!res.ok || !res.reqId) return
    const op = todo.fail(res.reqId, 'body too large')
    expect(op?.outcome).toBe('failed')
    expect(op?.message).toBe('body too large')
  })

  it('times out into unconfirmed after 10s', () => {
    vi.useFakeTimers()
    const todo = useTodoStore()
    todo.addItem(binding, 'first')
    vi.advanceTimersByTime(10_000)
    const op = todo.pendingFor(addScope(binding))
    expect(op?.outcome).toBe('unconfirmed')
    expect(op?.message).toContain('may already be in the list')
  })

  it('a socket drop marks every pending op unconfirmed', () => {
    const todo = useTodoStore()
    todo.addItem(binding, 'first')
    todo.setItem({ id: 't1', body: 'edit', binding })
    notePendingDisconnect()
    expect(todo.pendingFor(addScope(binding))?.outcome).toBe('unconfirmed')
    expect(todo.pendingFor(itemScope('t1'))?.outcome).toBe('unconfirmed')
  })

  it('without the feature, sends go out bare and nothing parks', () => {
    useConnectionStore().setHello('0.4.0', [])
    const todo = useTodoStore()
    const res = todo.addItem(binding, 'first')
    expect(res).toEqual({ ok: true, echoed: false })
    expect(lastSent().req_id).toBeUndefined()
    expect(todo.pendingFor(addScope(binding))).toBeUndefined()
  })

  it('a failed send records an immediate failure so the form can show it', () => {
    vi.mocked(ws.send).mockReturnValue(false)
    const todo = useTodoStore()
    expect(todo.addItem(binding, 'first')).toEqual({ ok: false, reason: 'not connected' })
    const op = todo.pendingFor(addScope(binding))
    expect(op).toMatchObject({ outcome: 'failed', kind: 'add', snapshot: 'first' })
    expect(op!.message).toContain('not connected')
  })
})
