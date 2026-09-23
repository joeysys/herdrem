import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { computed, ref } from 'vue'
import { relTime, useAgentsStore, useConnectionStore } from './connection'
import { useTodoStore } from './todo'
import { useRelaysStore } from './relays'
import { clearSessionCookieHint, markSessionCookieHint, sessionTokenFor } from '../utils/relay'
import { ws } from '../api/ws'

vi.mock('../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'disconnected',
  },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  sessionStorage.clear()
  clearSessionCookieHint()
})

describe('useAgentsStore', () => {
  it('updates panes when valid_paths changes without changing its count', () => {
    const store = useAgentsStore()
    const base = { pane_id: '%1', cwd: '/workspace', valid_paths: ['docs/old.md'] }

    store.setAgents([base])
    store.setAgents([{ ...base, valid_paths: ['docs/new.md'] }])

    expect(store.agents[0].valid_paths).toEqual(['docs/new.md'])
  })

  it('applies a follow-up snapshot that only enriches prompt and options', () => {
    // Regression: the first broadcast of a blocked pane can precede attachMenu
    // enrichment; the dedup key must not swallow the later identical-status
    // snapshot that carries prompt/options/option_labels.
    const store = useAgentsStore()
    const base = { pane_id: '%1', kind: 'agent', agent: 'claude', status: 'blocked', host: 'local', workspace_id: 'w1' }
    store.setAgents([{ ...base }])
    const first = store.agents[0]

    store.setAgents([{ ...base, prompt: 'allow edit?', options: ['y', 'n'], option_labels: ['Yes', 'No'] }])

    expect(store.agents[0]).not.toBe(first)
    expect(store.agents[0].prompt).toBe('allow edit?')
    expect(store.agents[0].options).toEqual(['y', 'n'])
  })

  it('pins blocked agents first while keeping last_active order inside groups', () => {
    const agents = useAgentsStore()
    agents.setAgents([
      { pane_id: 'w1', kind: 'agent', agent: 'claude', status: 'working', last_active: 100 },
      { pane_id: 'b1', kind: 'agent', agent: 'claude', status: 'blocked', last_active: 10 },
      { pane_id: 'w2', kind: 'agent', agent: 'claude', status: 'working', last_active: 50 },
      { pane_id: 'b2', kind: 'agent', agent: 'claude', status: 'blocked', last_active: 90 },
    ])

    expect(agents.filtered.map((p) => p.pane_id)).toEqual(['b2', 'b1', 'w1', 'w2'])
  })

  it('treats waiting/confirm statuses as approval-blocked like the relay', () => {
    // The relay's isBlocked covers block/wait/confirm and attaches prompt +
    // options for all of them; the frontend must classify them identically.
    const agents = useAgentsStore()
    agents.setAgents([
      { pane_id: 'w1', kind: 'agent', agent: 'claude', status: 'waiting', last_active: 1 },
      { pane_id: 'x1', kind: 'agent', agent: 'claude', status: 'working', last_active: 99 },
      { pane_id: 'c1', kind: 'agent', agent: 'claude', status: 'confirm', last_active: 2 },
    ])

    expect(agents.filtered.map((p) => p.pane_id)).toEqual(['c1', 'w1', 'x1'])
  })

  it('does not pin shells whose status reads blocked', () => {
    const agents = useAgentsStore()
    agents.setAgents([
      { pane_id: 's1', kind: 'shell', status: 'blocked', last_active: 10 },
      { pane_id: 'd1', kind: 'agent', agent: 'claude', status: 'done', last_active: 20 },
      { pane_id: 'b1', kind: 'agent', agent: 'claude', status: 'blocked', last_active: 1 },
    ])

    // s1 is a shell: it sorts by recency like any other non-blocked pane and
    // loses to the newer done agent; only the blocked agent is pinned.
    expect(agents.filtered.map((p) => p.pane_id)).toEqual(['b1', 'd1', 's1'])
  })

  it('updates relative time when the display clock advances', () => {
    const now = ref(1_060)
    const label = computed(() => relTime(1_000, now.value))

    expect(label.value).toBe('1m')
    now.value = 4_600
    expect(label.value).toBe('1h')
  })

  it('collapses "name•model" display names to the bare agent type', () => {
    const agents = useAgentsStore()
    agents.setAgents([
      { pane_id: '%1', agent: 'cmd', display_agent: 'cmd•sonnet', kind: 'agent' },
      { pane_id: '%2', agent: 'cmd', display_agent: 'cmd•gpt-5', kind: 'agent' },
      { pane_id: '%3', agent: 'claude', display_agent: 'claude•sonnet', kind: 'agent' },
      { pane_id: '%4', agent: 'custom', display_agent: 'custom•label', kind: 'agent' },
    ])

    expect(agents.agentTypes).toContain('cmd')
    expect(agents.agentTypes).toContain('claude•sonnet')
    expect(agents.agentTypes).toContain('custom•label')
    expect(agents.agentTypes).not.toContain('claude')
    expect(agents.agentTypes).not.toContain('custom')

    agents.typeFilter = 'cmd'
    expect(agents.filtered.map((p) => p.pane_id)).toEqual(['%1', '%2'])

    agents.typeFilter = 'claude•sonnet'
    expect(agents.filtered.map((p) => p.pane_id)).toEqual(['%3'])

    agents.typeFilter = 'custom•label'
    expect(agents.filtered.map((p) => p.pane_id)).toEqual(['%4'])
  })

  it('binds todos to a workspace instead of individual panes', () => {
    const agents = useAgentsStore()
    const todo = useTodoStore()
    agents.setAgents([
      { pane_id: '%1', host: 'local', workspace_id: 'ws1' },
      { pane_id: '%2', host: 'local', workspace_id: 'ws1' },
    ])
    todo.setAll([{
      id: '1', body: 'shared', binding: { host: 'local', workspace_id: 'ws1' },
      created_at: '2026-08-10T00:00:00Z', updated_at: '2026-08-10T00:00:00Z',
    }])

    agents.toggleStatusFilter('todo')
    expect(agents.filtered.map((pane) => pane.pane_id)).toEqual(['%1', '%2'])
    expect(agents.counts.todo).toBe(1)
  })
})

describe('useConnectionStore token storage', () => {
  it('keeps the relay token in memory and scrubs localStorage copies', () => {
    localStorage.setItem('herdrem_token', 'stale')
    const conn = useConnectionStore()
    expect(conn.token).toBe('')
    expect(localStorage.getItem('herdrem_token')).toBeNull()

    conn.connect('ws://relay.example', 'live-token')
    expect(conn.token).toBe('live-token')
    expect(localStorage.getItem('herdrem_token')).toBeNull()
  })

  it('persists a relay URL without a token query', () => {
    const conn = useConnectionStore()
    conn.connect('wss://relay.example/ws?token=super-secret&keep=1')
    expect(conn.url).toBe('wss://relay.example?keep=1')
    expect(localStorage.getItem('herdrem_url')).toBe('wss://relay.example?keep=1')
    expect(localStorage.getItem('herdrem_url')).not.toContain('super-secret')
  })

  it('lifts a token query into memory when connect is not given an explicit token', () => {
    const conn = useConnectionStore()
    conn.connect('wss://relay.example/ws?token=query-secret')
    expect(conn.token).toBe('query-secret')
    expect(conn.url).toBe('wss://relay.example')
    expect(localStorage.getItem('herdrem_url')).not.toContain('query-secret')
  })

  it('marks a session cookie hint after a same-origin mint', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ ok: true, status: 204 } as Response)
    const conn = useConnectionStore()
    conn.connect(`ws://${location.host}`, 'lan-token')
    expect(fetchMock).toHaveBeenCalled()
    // Explicit re-auth revokes the old session first, then mints — flush the
    // promise chain with a macrotask.
    await new Promise((r) => setTimeout(r, 0))
    const urls = fetchMock.mock.calls.map((c) => String(c[0]))
    expect(urls[0]).toContain('/session-logout')
    expect(urls[1]).toContain('/session-auth')
    // The relay token rides the Authorization header, not the URL.
    expect(urls[1]).not.toContain('token=')
    const mintOpts = fetchMock.mock.calls[1][1] as RequestInit
    expect((mintOpts.headers as Record<string, string>).Authorization).toBe('Bearer lan-token')
    expect(localStorage.getItem('herdrem_session')).toBe('1')
    fetchMock.mockRestore()
  })
})

describe('useConnectionStore boot timeout', () => {
  it('sets booted=true after 5s even if hello never arrives', () => {
    vi.useFakeTimers()
    const conn = useConnectionStore()
    expect(conn.booted).toBe(false)
    conn.connect('ws://relay.example', '')
    // Before timeout: still not booted
    vi.advanceTimersByTime(4999)
    expect(conn.booted).toBe(false)
    // After 5s: booted becomes true so the UI is not permanently trapped
    vi.advanceTimersByTime(1)
    expect(conn.booted).toBe(true)
    vi.useRealTimers()
  })

  it('marks a timed-out boot without clearing its token', () => {
    vi.useFakeTimers()
    const conn = useConnectionStore()
    conn.connect('ws://relay.example', 'saved-token')

    vi.advanceTimersByTime(5000)
    expect(conn.bootTimedOut).toBe(true)
    expect(conn.token).toBe('saved-token')

    conn.setHello('v1', ['agents'])
    expect(conn.bootTimedOut).toBe(false)
    vi.useRealTimers()
  })

  it('clears the session cookie hint after a timed-out cookieless boot', () => {
    vi.useFakeTimers()
    markSessionCookieHint()
    const conn = useConnectionStore()
    conn.connect('ws://192.168.1.10:8766', '')
    vi.advanceTimersByTime(5000)
    expect(conn.bootTimedOut).toBe(true)
    expect(localStorage.getItem('herdrem_session')).toBeNull()
    vi.useRealTimers()
  })

  it('keeps the session cookie hint after a timed-out boot that had a token', () => {
    vi.useFakeTimers()
    markSessionCookieHint()
    const conn = useConnectionStore()
    conn.connect('ws://192.168.1.10:8766', 'saved-token')
    vi.advanceTimersByTime(5000)
    expect(conn.bootTimedOut).toBe(true)
    expect(localStorage.getItem('herdrem_session')).toBe('1')
    vi.useRealTimers()
  })

  it('clears the boot timer when hello arrives', () => {
    vi.useFakeTimers()
    const conn = useConnectionStore()
    conn.connect('ws://relay.example', '')
    conn.setHello('v1', ['agents'])
    expect(conn.booted).toBe(true)
    // Advancing past 5s should not cause any issues (timer already cleared)
    vi.advanceTimersByTime(6000)
    expect(conn.booted).toBe(true)
    vi.useRealTimers()
  })
})

describe('useConnectionStore session probe', () => {
  it('fails fast to auth when a cookieless same-origin connect meets a dead session', async () => {
    markSessionCookieHint()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ status: 401 } as Response)
    const conn = useConnectionStore()
    conn.connect(`ws://${location.host}`)
    await new Promise((r) => setTimeout(r, 0))
    const urls = fetchMock.mock.calls.map((c) => String(c[0]))
    expect(urls).toHaveLength(1)
    expect(urls[0]).toContain('/session-auth')
    // No Bearer header — the probe verifies the HttpOnly session cookie.
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).toBeUndefined()
    expect(localStorage.getItem('herdrem_session')).toBeNull()
    expect(conn.bootTimedOut).toBe(true)
    // Splash stays up while App.vue's bootTimedOut watcher re-probes.
    expect(conn.booted).toBe(false)
    fetchMock.mockRestore()
  })

  it('leaves the connect alone when the session cookie is still valid', async () => {
    markSessionCookieHint()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ status: 204 } as Response)
    const conn = useConnectionStore()
    conn.connect(`ws://${location.host}`)
    await new Promise((r) => setTimeout(r, 0))
    expect(conn.bootTimedOut).toBe(false)
    expect(localStorage.getItem('herdrem_session')).toBe('1')
    fetchMock.mockRestore()
  })

  it('ignores a stale probe result after a newer connect supersedes it', async () => {
    let resolveProbe: (r: Response) => void = () => {}
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      () => new Promise<Response>((r) => { resolveProbe = r }),
    )
    const conn = useConnectionStore()
    conn.connect(`ws://${location.host}`)
    conn.connect('ws://relay.example', '')
    resolveProbe({ status: 401 } as Response)
    await new Promise((r) => setTimeout(r, 0))
    expect(conn.bootTimedOut).toBe(false)
    fetchMock.mockRestore()
  })

  it('fails fast when the mint probe rejects an explicit token', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ status: 401 } as Response)
    const conn = useConnectionStore()
    conn.connect(`ws://${location.host}`, 'wrong-token')
    await new Promise((r) => setTimeout(r, 0))
    expect(conn.bootTimedOut).toBe(true)
    fetchMock.mockRestore()
  })
})

describe('useConnectionStore session tokens', () => {
  it('persists a connect token in sessionStorage keyed by relay URL', () => {
    const conn = useConnectionStore()
    conn.connect('wss://relay.example/ws', 'live-token')
    const stored = JSON.parse(sessionStorage.getItem('herdrem_tokens') || '{}')
    expect(stored['wss://relay.example']).toBe('live-token')
    // Never touches localStorage — the credential dies with the tab.
    expect(localStorage.getItem('herdrem_tokens')).toBeNull()
    expect(localStorage.getItem('herdrem_token')).toBeNull()
  })

  it('seeds the token ref from sessionStorage on init', () => {
    localStorage.setItem('herdrem_url', 'ws://relay.example')
    sessionStorage.setItem('herdrem_tokens', JSON.stringify({ 'ws://relay.example': 'init-token' }))
    const conn = useConnectionStore()
    expect(conn.token).toBe('init-token')
  })

  it('restores a session-stored token on a tokenless connect (restart reload)', () => {
    sessionStorage.setItem('herdrem_tokens', JSON.stringify({ 'wss://relay.example': 'saved-token' }))
    const conn = useConnectionStore()
    conn.connect('wss://relay.example', '')
    expect(conn.token).toBe('saved-token')
    // The WS dial itself gets the restored credential — no token page needed.
    expect(ws.connect).toHaveBeenCalledWith('wss://relay.example', 'saved-token')
  })

  it('drops a session-stored token when the relay rejects it', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ status: 401 } as Response)
    const conn = useConnectionStore()
    conn.connect(`ws://${location.host}`, 'stale-token')
    expect(JSON.parse(sessionStorage.getItem('herdrem_tokens') || '{}')[`ws://${location.host}`]).toBe('stale-token')
    await new Promise((r) => setTimeout(r, 0))
    expect(conn.bootTimedOut).toBe(true)
    expect(JSON.parse(sessionStorage.getItem('herdrem_tokens') || '{}')).not.toHaveProperty(`ws://${location.host}`)
    fetchMock.mockRestore()
  })

  it('times out a failed relay switch even after a previous hello', () => {
    vi.useFakeTimers()
    const conn = useConnectionStore()
    conn.connect('wss://first.example', 'first-token')
    conn.setHello('1', [])
    conn.connect('wss://second.example', 'invalid-token')
    vi.advanceTimersByTime(5000)
    expect(conn.bootTimedOut).toBe(true)
    vi.useRealTimers()
  })

  it('forgets a rejected settings token so switching back does not retry it', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ status: 401 } as Response)
    const relays = useRelaysStore()
    const relay = relays.add('local', `ws://${location.host}`, 'bad-token')
    relays.setCurrent(relay.id)
    const conn = useConnectionStore()
    conn.connect(relay.url, relay.token)
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(relays.current()?.token).toBeUndefined()
    expect(conn.token).toBe('')
    expect(sessionTokenFor(relay.url)).toBe('')
    fetchMock.mockRestore()
  })

  it('slides the session cookie while connected and stops on disconnect', () => {
    vi.useFakeTimers()
    let statusCb: (s: string) => void = () => {}
    vi.mocked(ws.onStatus).mockImplementation((cb) => {
      statusCb = cb
      return () => {}
    })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ status: 204 } as Response)
    const conn = useConnectionStore()
    conn.connect(`ws://${location.host}`, 'tok')
    fetchMock.mockClear()

    statusCb('connected')
    vi.advanceTimersByTime(30 * 60 * 1000)
    expect(fetchMock).toHaveBeenCalled()
    expect(String(fetchMock.mock.calls[0][0])).toContain('/session-auth')
    expect((fetchMock.mock.calls[0][1] as RequestInit).credentials).toBe('include')

    statusCb('disconnected')
    fetchMock.mockClear()
    vi.advanceTimersByTime(60 * 60 * 1000)
    expect(fetchMock).not.toHaveBeenCalled()

    fetchMock.mockRestore()
    vi.useRealTimers()
  })
})
