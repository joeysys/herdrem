import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { computed, ref } from 'vue'
import { relTime, useAgentsStore, useConnectionStore } from './connection'
import { useTodoStore } from './todo'

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
})

describe('useAgentsStore', () => {
  it('updates panes when valid_paths changes without changing its count', () => {
    const store = useAgentsStore()
    const base = { pane_id: '%1', cwd: '/workspace', valid_paths: ['docs/old.md'] }

    store.setAgents([base])
    store.setAgents([{ ...base, valid_paths: ['docs/new.md'] }])

    expect(store.agents[0].valid_paths).toEqual(['docs/new.md'])
  })

  it('updates relative time when the display clock advances', () => {
    const now = ref(1_060)
    const label = computed(() => relTime(1_000, now.value))

    expect(label.value).toBe('1m')
    now.value = 4_600
    expect(label.value).toBe('1h')
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
})

describe('useConnectionStore boot timeout (F2)', () => {
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
