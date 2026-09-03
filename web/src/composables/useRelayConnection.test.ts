import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { useRelayConnection } from './useRelayConnection'
import { useAgentsStore, useConnectionStore } from '../stores/connection'
import { useRelaysStore } from '../stores/relays'
import { useTunnelsStore } from '../stores/tunnels'
import { usePreviewsStore } from '../stores/preview'
import { useFilesStore } from '../stores/files'
import { useTodoStore } from '../stores/todo'
import { useTerminalStore } from '../stores/terminal'
import { ws } from '../api/ws'
import { clearSessionCookieHint, markSessionCookieHint } from '../utils/relay'

vi.mock('../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn((cb: (s: string) => void) => {
      ;(ws as { _statusCb?: (s: string) => void })._statusCb = cb
      return () => {}
    }),
    status: 'disconnected',
  },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.connect).mockClear()
  vi.mocked(ws.disconnect).mockClear()
  vi.mocked(ws.send).mockClear()
  localStorage.clear()
  clearSessionCookieHint()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function seedRelay(name: string, url: string, token: string) {
  const relays = useRelaysStore()
  return relays.add(name, url, token)
}

describe('useRelayConnection', () => {
  it('passes a boot token to the default same-origin relay', () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({} as Response)
    const { boot } = useRelayConnection()

    try {
      boot('lan-token')

      expect(ws.connect).toHaveBeenCalledWith(`ws://${location.host}`, 'lan-token')
    } finally {
      fetchMock.mockRestore()
    }
  })

  it('does not boot a non-local relay without a token', () => {
    const relay = seedRelay('LAN', 'ws://192.168.1.10:8766', '')
    useRelaysStore().setCurrent(relay.id)
    const { boot } = useRelayConnection()

    boot()

    expect(ws.connect).not.toHaveBeenCalled()
  })

  it('does not switch to a non-local relay without a token', () => {
    const relay = seedRelay('LAN', 'ws://192.168.1.10:8766', '')
    const { connectRelay } = useRelayConnection()

    connectRelay(relay)

    expect(ws.connect).not.toHaveBeenCalled()
  })

  it('prepares auth instead of silently failing when switching to a remote relay without a token', () => {
    const a = seedRelay('A', 'ws://a.example', 'tok-a')
    const b = seedRelay('B', 'ws://b.example', '')
    const conn = useConnectionStore()
    const relays = useRelaysStore()
    const { connectRelay } = useRelayConnection()

    connectRelay(a)
    expect(ws.connect).toHaveBeenCalledWith('ws://a.example', 'tok-a')
    expect(conn.token).toBe('tok-a')

    const ok = connectRelay(b)
    expect(ok).toBe(false)
    expect(ws.connect).toHaveBeenCalledTimes(1)
    expect(ws.disconnect).toHaveBeenCalled()
    expect(relays.currentId).toBe(b.id)
    expect(conn.token).toBe('')
    expect(conn.url).toBe('ws://b.example')
    expect(conn.status).toBe('disconnected')
  })

  it('keeps a boot token in memory and never writes it to localStorage', async () => {
    const relay = seedRelay('LAN', 'ws://192.168.1.10:8766', '')
    const relays = useRelaysStore()
    relays.setCurrent(relay.id)
    const { boot } = useRelayConnection()

    boot('lan-token')

    expect(ws.connect).toHaveBeenCalledWith('ws://192.168.1.10:8766', 'lan-token')
    expect(relays.relays.find((entry) => entry.id === relay.id)?.token).toBe('lan-token')

    const statusCb = (ws as { _statusCb?: (s: string) => void })._statusCb
    statusCb?.('connecting')
    await nextTick()
    statusCb?.('connected')
    await nextTick()

    const stored = JSON.parse(localStorage.getItem('herdrem_relays') || '[]') as Array<{ id: string; token?: string }>
    expect(stored.find((entry) => entry.id === relay.id)?.token).toBeUndefined()
    expect(JSON.stringify(stored)).not.toContain('lan-token')
  })

  it('keeps a memory token after a transient connection failure', async () => {
    const relay = seedRelay('LAN', 'ws://192.168.1.10:8766', 'saved-token')
    const conn = useConnectionStore()
    useRelaysStore().setCurrent(relay.id)
    const { boot } = useRelayConnection()

    boot()

    expect(ws.connect).toHaveBeenCalledWith('ws://192.168.1.10:8766', 'saved-token')
    expect(conn.token).toBe('saved-token')

    const statusCb = (ws as { _statusCb?: (s: string) => void })._statusCb
    statusCb?.('connecting')
    await nextTick()
    statusCb?.('disconnected')
    await nextTick()

    expect(conn.token).toBe('saved-token')
    expect(ws.disconnect).not.toHaveBeenCalled()
    const stored = JSON.parse(localStorage.getItem('herdrem_relays') || '[]') as Array<{ token?: string }>
    expect(JSON.stringify(stored)).not.toContain('saved-token')

    boot('replacement-token')
    expect(ws.connect).toHaveBeenLastCalledWith('ws://192.168.1.10:8766', 'replacement-token')
  })

  it('boots a page-origin LAN relay without a token when a session hint exists', () => {
    vi.stubGlobal('location', {
      protocol: 'http:',
      host: '192.168.1.10:8766',
      hostname: '192.168.1.10',
      origin: 'http://192.168.1.10:8766',
      href: 'http://192.168.1.10:8766/',
    })
    markSessionCookieHint()
    const relay = seedRelay('LAN', 'ws://192.168.1.10:8766', '')
    useRelaysStore().setCurrent(relay.id)
    const { boot } = useRelayConnection()

    boot()

    expect(ws.connect).toHaveBeenCalledWith('ws://192.168.1.10:8766', '')
    vi.unstubAllGlobals()
  })

  it('does not boot a page-origin LAN relay without a token when no session hint exists', () => {
    vi.stubGlobal('location', {
      protocol: 'http:',
      host: '192.168.1.10:8766',
      hostname: '192.168.1.10',
      origin: 'http://192.168.1.10:8766',
      href: 'http://192.168.1.10:8766/',
    })
    const relay = seedRelay('LAN', 'ws://192.168.1.10:8766', '')
    useRelaysStore().setCurrent(relay.id)
    const { boot } = useRelayConnection()

    boot()

    expect(ws.connect).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })

  it('clears tunnels, panes, previews, files and todos when switching A → B', () => {
    const a = seedRelay('A', 'ws://a.example/ws', 'tok-a')
    const b = seedRelay('B', 'ws://b.example/ws', 'tok-b')
    const agents = useAgentsStore()
    const tunnels = useTunnelsStore()
    const previews = usePreviewsStore()
    const files = useFilesStore()
    const todo = useTodoStore()
    const term = useTerminalStore()

    agents.setAgents([{ pane_id: 'p-a', host: 'local', status: 'idle' }])
    tunnels.setTunnels([{ id: 't-a', port: 5173, status: 'active', public_url: 'https://a.trycloudflare.com', auth_mode: 'token_gate', created_at: 1 }])
    tunnels.openPreview('https://a.trycloudflare.com', 'A')
    previews.setPreview('p-a', 'old output')
    files.openBrowser('/a', 'local')
    todo.setAll([{
      id: '1', body: 'a-todo', binding: { host: 'local', workspace_id: 'ws' },
      created_at: '2026-08-10T00:00:00Z', updated_at: '2026-08-10T00:00:00Z',
    }])
    term.activePane = 'p-a'
    term.content = 'stale-a'

    const { connectRelay } = useRelayConnection()
    connectRelay(b)

    expect(agents.agents).toEqual([])
    expect(tunnels.tunnels).toEqual([])
    expect(tunnels.previewUrl).toBeNull()
    expect(previews.hasPreview('p-a')).toBe(false)
    expect(files.browserOpen).toBe(false)
    expect(files.preview).toBeNull()
    expect(todo.todos).toEqual({})
    expect(term.activePane).toBe('')
    expect(ws.connect).toHaveBeenCalledWith('ws://b.example', 'tok-b')

    agents.setAgents([{ pane_id: 'p-b', host: 'local', status: 'idle' }])
    tunnels.setTunnels([{ id: 't-b', port: 3000, status: 'active', public_url: 'https://b.trycloudflare.com', auth_mode: 'token_gate', created_at: 1 }])
    connectRelay(a)
    expect(agents.agents).toEqual([])
    expect(tunnels.tunnels).toEqual([])
    expect(ws.connect).toHaveBeenLastCalledWith('ws://a.example', 'tok-a')
  })

  it('re-requests tunnels after the new relay connects', async () => {
    const a = seedRelay('A', 'ws://a.example/ws', 'tok-a')
    const conn = useConnectionStore()
    const { connectRelay } = useRelayConnection()
    connectRelay(a)
    conn.status = 'connected'
    await nextTick()
    expect(ws.send).toHaveBeenCalledWith({ type: 'tunnels_list' })
  })
})
