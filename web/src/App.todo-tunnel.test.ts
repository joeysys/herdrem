// App message routing for req-echo: todo_updated / tunnel_updated carrying a
// req_id confirm the parked mutation; error frames with the same id fail it.
// Broadcast copies (no req_id) must never resolve someone else's pending op.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { MockInstance } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import App from './App.vue'
import { useConnectionStore } from './stores/connection'
import { useTodoStore, addScope } from './stores/todo'
import { useTunnelsStore, portScope } from './stores/tunnels'
import { ws } from './api/ws'

vi.mock('./api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'disconnected',
  },
}))

const binding = { host: 'local', workspace_id: 'ws-a', workspace_label: 'A' }
let fetchMock: MockInstance<typeof fetch>

function handler() {
  const cb = vi.mocked(ws.onMessage).mock.calls.at(-1)?.[0]
  if (!cb) throw new Error('App registered no onMessage handler')
  return cb
}

beforeEach(() => {
  setActivePinia(createPinia())
  sessionStorage.clear()
  vi.mocked(ws.send).mockClear().mockReturnValue(true)
  fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise<Response>(() => {}))
})

afterEach(() => {
  fetchMock.mockRestore()
})

describe('App req-echo routing', () => {
  it('todo_updated with req_id confirms the parked op; without it the op stays pending', async () => {
    const wrapper = mount(App)
    useConnectionStore().setHello('0.4.0', ['req-echo'])
    const todo = useTodoStore()

    // A broadcast copy — no req_id — must not confirm the op.
    todo.addItem(binding, 'broadcast bait')
    handler()({ type: 'todo_updated', todo: { id: 't9', body: 'broadcast bait', created_at: 'x', updated_at: 'x' } })
    expect(todo.pendingFor(addScope(binding))?.outcome).toBe('pending')

    // The initiator's copy carries the matching req_id.
    const reqId = todo.pendingFor(addScope(binding))!.reqId
    handler()({ type: 'todo_updated', todo: { id: 't9', body: 'broadcast bait', created_at: 'x', updated_at: 'x' }, req_id: reqId })
    await nextTick()
    expect(todo.pendingFor(addScope(binding))?.outcome).toBe('confirmed')
    wrapper.unmount()
  })

  it('a todo_set error fails the matching op', async () => {
    const wrapper = mount(App)
    useConnectionStore().setHello('0.4.0', ['req-echo'])
    const todo = useTodoStore()
    todo.addItem(binding, 'doomed')
    const reqId = todo.pendingFor(addScope(binding))!.reqId

    handler()({ type: 'error', op: 'todo_set', message: 'body too large', req_id: reqId })
    await nextTick()
    const op = todo.pendingFor(addScope(binding))
    expect(op?.outcome).toBe('failed')
    expect(op?.message).toBe('body too large')
    wrapper.unmount()
  })

  it('a stray todo_set error without req_id fails nothing', async () => {
    const wrapper = mount(App)
    useConnectionStore().setHello('0.4.0', ['req-echo'])
    const todo = useTodoStore()
    todo.addItem(binding, 'safe')

    handler()({ type: 'error', op: 'todo_set', message: 'someone else failed' })
    await nextTick()
    expect(todo.pendingFor(addScope(binding))?.outcome).toBe('pending')
    wrapper.unmount()
  })

  it('tunnel_updated with req_id confirms; tunnel_create errors fail it', async () => {
    const wrapper = mount(App)
    useConnectionStore().setHello('0.4.0', ['req-echo'])
    const tunnels = useTunnelsStore()

    tunnels.createTunnel(5173, 'token_gate')
    const reqId = tunnels.pendingFor(portScope)!.reqId
    handler()({ type: 'tunnel_updated', tunnel: { id: 'u1', port: 5173, public_url: 'https://x.trycloudflare.com', status: 'starting', auth_mode: 'token_gate', created_at: 1 }, req_id: reqId })
    await nextTick()
    expect(tunnels.pendingFor(portScope)?.outcome).toBe('confirmed')
    tunnels.ack(portScope)

    tunnels.createTunnel(3000, 'token_gate')
    const reqId2 = tunnels.pendingFor(portScope)!.reqId
    handler()({ type: 'error', op: 'tunnel_create', message: 'port not allowed', req_id: reqId2 })
    await nextTick()
    const op = tunnels.pendingFor(portScope)
    expect(op?.outcome).toBe('failed')
    expect(op?.message).toBe('port not allowed')
    wrapper.unmount()
  })

  it('a req-scoped tunnel_create error never lands in the legacy lastCreateError slot', async () => {
    const wrapper = mount(App)
    useConnectionStore().setHello('0.4.0', ['req-echo'])
    const tunnels = useTunnelsStore()

    // A stale req_id that matches no parked op must not overwrite the slot an
    // unrelated in-flight create reads.
    handler()({ type: 'error', op: 'tunnel_create', message: 'stale rejection', req_id: 'req-nope' })
    await nextTick()
    expect(tunnels.lastCreateError).toBeNull()

    // Even a matched req-scoped error surfaces through its op, not the slot.
    tunnels.createTunnel(5173, 'token_gate')
    const reqId = tunnels.pendingFor(portScope)!.reqId
    handler()({ type: 'error', op: 'tunnel_create', message: 'port not allowed', req_id: reqId })
    await nextTick()
    expect(tunnels.lastCreateError).toBeNull()
    expect(tunnels.pendingFor(portScope)?.outcome).toBe('failed')

    // Legacy relays send unscoped errors; those still use the slot.
    handler()({ type: 'error', op: 'tunnel_create', message: 'legacy failure' })
    await nextTick()
    expect(tunnels.lastCreateError).not.toBeNull()
    wrapper.unmount()
  })
})
