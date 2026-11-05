// tunnel req-echo: same pending-op semantics as todo — busy scopes refuse,
// send failures refuse, only a negotiated req-echo parks the op. The PIN is
// snapshotted per op so a failure can hand it back to the form.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useTunnelsStore, portScope, fileScope } from './tunnels'
import { useConnectionStore } from './connection'
import { notePendingDisconnect } from './pendingOps'
import { ws } from '../api/ws'
import type { TunnelInfo } from '../api/types'

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

const lastSent = () => vi.mocked(ws.send).mock.lastCall?.[0] as { req_id?: string; type: string }
const errTunnel = (id: string, path: string): TunnelInfo => ({
  id,
  file_path: path,
  host: 'local',
  public_url: '',
  status: 'error',
  auth_mode: 'token_gate',
  error: 'old failure',
  created_at: 1,
})

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear().mockReturnValue(true)
  useConnectionStore().setHello('0.4.0', ['req-echo'])
})

afterEach(() => {
  vi.useRealTimers()
})

describe('tunnel req-echo pending ops', () => {
  it('port create sends req_id and parks under the port scope', () => {
    const tunnels = useTunnelsStore()
    const res = tunnels.createTunnel(5173, 'token_gate', 'pin7')
    expect(res.ok).toBe(true)
    if (!res.ok) return
    expect(lastSent()).toMatchObject({ type: 'tunnel_create', port: 5173, req_id: res.reqId })
    expect(tunnels.pendingFor(portScope)?.outcome).toBe('pending')
    expect(tunnels.pendingFor(portScope)?.snapshot).toBe('pin7')
  })

  it('a second port create is refused while one is pending', () => {
    const tunnels = useTunnelsStore()
    tunnels.createTunnel(5173)
    expect(tunnels.createTunnel(3000)).toEqual({ ok: false, reason: 'busy' })
    expect(ws.send).toHaveBeenCalledTimes(1)
  })

  it('file create scopes by host+path and purges a stale error entry', () => {
    const tunnels = useTunnelsStore()
    tunnels.setTunnels([errTunnel('e1', '/f/a.txt')])
    const res = tunnels.createFileTunnel('/f/a.txt', 'local', 'token_gate', 'pin9')
    expect(res.ok).toBe(true)
    expect(tunnels.tunnels.find((t) => t.id === 'e1')).toBeUndefined()
    expect(tunnels.pendingFor(fileScope('local', '/f/a.txt'))?.outcome).toBe('pending')
    expect(tunnels.pendingFor(fileScope('local', '/f/a.txt'))?.snapshot).toBe('pin9')
  })

  it('a busy file scope refuses without purging the existing error entry', () => {
    const tunnels = useTunnelsStore()
    tunnels.createFileTunnel('/f/a.txt', 'local')
    tunnels.setTunnels([errTunnel('e1', '/f/a.txt')])
    const res = tunnels.createFileTunnel('/f/a.txt', 'local')
    expect(res).toEqual({ ok: false, reason: 'busy' })
    expect(tunnels.tunnels.find((t) => t.id === 'e1')).toBeDefined()
    expect(ws.send).toHaveBeenCalledTimes(1)
  })

  it('confirm and fail resolve the op by req_id', () => {
    const tunnels = useTunnelsStore()
    const res = tunnels.createTunnel(5173)
    if (!res.ok || !res.reqId) return
    expect(tunnels.fail('wrong-id', 'nope')).toBeUndefined()
    expect(tunnels.confirm(res.reqId)?.outcome).toBe('confirmed')
    tunnels.ack(portScope)
    expect(tunnels.pendingFor(portScope)).toBeUndefined()
  })

  it('an unconfirmed create says the tunnel may already be starting', () => {
    vi.useFakeTimers()
    const tunnels = useTunnelsStore()
    tunnels.createTunnel(5173)
    vi.advanceTimersByTime(10_000)
    const op = tunnels.pendingFor(portScope)
    expect(op?.outcome).toBe('unconfirmed')
    expect(op?.message).toContain('may already be starting')
  })

  it('a socket drop marks pending creates unconfirmed', () => {
    const tunnels = useTunnelsStore()
    tunnels.createTunnel(5173)
    notePendingDisconnect()
    expect(tunnels.pendingFor(portScope)?.outcome).toBe('unconfirmed')
  })

  it('without the feature, sends go out bare and nothing parks', () => {
    useConnectionStore().setHello('0.4.0', [])
    const tunnels = useTunnelsStore()
    expect(tunnels.createTunnel(5173)).toEqual({ ok: true, echoed: false })
    expect(lastSent().req_id).toBeUndefined()
    expect(tunnels.pendingFor(portScope)).toBeUndefined()
  })
})
