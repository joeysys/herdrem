import { describe, it, expect, vi, beforeEach } from 'vitest'
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
  vi.mocked(ws.send).mockClear()
  localStorage.clear()
})

function seedRelay(name: string, url: string, token: string) {
  const relays = useRelaysStore()
  return relays.add(name, url, token)
}

describe('useRelayConnection', () => {
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
