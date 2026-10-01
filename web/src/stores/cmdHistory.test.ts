import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { chunkForImport, useCmdHistoryStore } from './cmdHistory'
import { useConnectionStore } from './connection'
import { safeStorage } from '../utils/storage'
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

const sh = (paneId?: string, extra: Record<string, unknown> = {}) => ({ kind: 'sh' as const, paneId, ...extra })
const ag = (paneId?: string, agent = 'claude', label = 'proj', extra: Record<string, unknown> = {}) =>
  ({ kind: 'ag' as const, paneId, agent, label, ...extra })

const RELAY = `http://${location.host}`.toLowerCase()
const KEYS = ['herdrem_cmd_history', 'herdrem_history_pending', 'herdrem_history_seeded', 'herdrem_history_agents']

/** Messages the store has sent to the relay, by type. */
const sent = (type?: string) =>
  vi.mocked(ws.send).mock.calls.map((c) => c[0] as unknown as Record<string, unknown>).filter((m) => !type || m.type === type)

function shared() {
  const conn = useConnectionStore()
  conn.setHello('test', ['input-history'])
  return { conn, h: useCmdHistoryStore() }
}

beforeEach(() => {
  // safeStorage mirrors writes into an in-memory map; clear through it.
  for (const k of KEYS) safeStorage.removeItem(k)
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockReset()
  vi.mocked(ws.send).mockReturnValue(true)
})

describe('cmdHistory store — browser-only (relay without input-history)', () => {
  it('records commands for suggestions and search, and sends nothing', () => {
    const h = useCmdHistoryStore()
    expect(h.shared).toBe(false)
    h.record('git status', sh('p1'))
    h.record('git stash', sh('p1'))
    expect(h.suggest('git st', { kind: 'sh', paneId: 'p1' })).toEqual(['git stash', 'git status'])
    expect(h.search('stat', { paneId: 'p1' }).map((e) => e.c)).toEqual(['git status'])
    expect(sent()).toEqual([])
  })

  it('records agent prompts separately from shell commands', () => {
    const h = useCmdHistoryStore()
    h.record('git status', sh('p1'))
    h.record('git status of the repo', ag('p2', 'codex', 'api'))
    expect(h.suggest('git', { kind: 'sh' })).toEqual(['git status'])
    expect(h.suggest('git', { kind: 'ag' })).toEqual(['git status of the repo'])
    expect(h.counts).toEqual({ sh: 1, ag: 1 })
    expect(h.agentTypes).toEqual(['codex'])
    expect(h.entries.find((e) => e.k === 'ag')).toMatchObject({ a: 'codex', w: 'api', p: 'p2' })
  })

  it('remembers the workspace and keeps the same text in two workspaces apart', () => {
    const h = useCmdHistoryStore()
    h.record('make test', sh('p1', { host: 'local', workspaceId: 'w1', workspaceLabel: 'api' }))
    h.record('make test', sh('p2', { host: 'local', workspaceId: 'w2' }))
    expect(h.entries).toHaveLength(2)
    expect(h.search('', { host: 'local', workspaceId: 'w1', scope: 'workspace' }).map((e) => e.l)).toEqual(['api'])
    expect(h.count({ host: 'local', workspaceId: 'w2', scope: 'workspace' })).toBe(1)
    expect(h.count({ scope: 'relay' })).toBe(1) // one text on the relay
  })

  it('lists the agent types seen, sorted, shell entries excluded', () => {
    const h = useCmdHistoryStore()
    h.record('one', ag('p1', 'codex'))
    h.record('two', ag('p2', 'claude'))
    h.record('three', sh('p3'))
    expect(h.agentTypes).toEqual(['claude', 'codex'])
  })

  it('does not record credential-looking or ignorespace input', () => {
    const h = useCmdHistoryStore()
    h.record('export TOKEN=abc', sh())
    h.record(' secret command', sh())
    h.record('use token: abc123 for the api', ag())
    h.record('ls', sh())
    expect(h.entries.map((e) => e.c)).toEqual(['ls'])
  })

  it('can stop saving agent prompts on this device, and the choice persists', () => {
    const h = useCmdHistoryStore()
    expect(h.recordAgents).toBe(true)
    h.setRecordAgents(false)
    h.record('a prompt', ag())
    h.record('a command', sh())
    expect(h.entries.map((e) => e.c)).toEqual(['a command'])
    setActivePinia(createPinia())
    expect(useCmdHistoryStore().recordAgents).toBe(false)
  })

  it('keeps a separate history per relay', () => {
    const conn = useConnectionStore()
    const h = useCmdHistoryStore()
    conn.url = 'ws://relay-a.example/ws'
    h.record('only on a', sh())
    h.record('prompt on a', ag())
    conn.url = 'ws://relay-b.example/ws'
    expect(h.entries).toEqual([])
    expect(h.agentTypes).toEqual([])
    h.record('only on b', sh())
    expect(h.entries.map((e) => e.c)).toEqual(['only on b'])
    conn.url = 'ws://relay-a.example/ws'
    expect(h.entries.map((e) => e.c).sort()).toEqual(['only on a', 'prompt on a'])
  })

  it('treats a relay URL case-insensitively', () => {
    const conn = useConnectionStore()
    const h = useCmdHistoryStore()
    conn.url = 'ws://relay.example/ws'
    h.record('same relay', sh())
    conn.url = 'ws://RELAY.example/ws'
    expect(h.entries.map((e) => e.c)).toEqual(['same relay'])
  })

  it('persists across a reload, including agent and workspace metadata', () => {
    const conn = useConnectionStore()
    conn.url = 'ws://relay-a.example/ws'
    const h = useCmdHistoryStore()
    h.record('persisted cmd', sh())
    h.record('persisted prompt', ag('p1', 'claude', 'proj', { host: 'local', workspaceId: 'w1', workspaceLabel: 'api' }))
    setActivePinia(createPinia())
    const conn2 = useConnectionStore()
    conn2.url = 'ws://relay-a.example/ws'
    const again = useCmdHistoryStore()
    expect(again.entries.map((e) => e.c)).toEqual(['persisted prompt', 'persisted cmd'])
    expect(again.entries[0]).toMatchObject({ k: 'ag', a: 'claude', w: 'proj', h: 'local', s: 'w1', l: 'api' })
  })

  it('loads history written before agent prompts / workspaces existed', () => {
    safeStorage.setItem('herdrem_cmd_history', JSON.stringify({
      'http://localhost:3000': [{ c: 'old cmd', t: 1, n: 2 }],
    }))
    const conn = useConnectionStore()
    conn.url = 'ws://localhost:3000/ws'
    const h = useCmdHistoryStore()
    expect(h.suggest('old', { kind: 'sh' })).toEqual(['old cmd'])
    expect(h.counts).toEqual({ sh: 1, ag: 0 })
  })

  it('removes one entry (by text and kind) and clears only the current relay', () => {
    const conn = useConnectionStore()
    const h = useCmdHistoryStore()
    conn.url = 'ws://a.example/ws'
    h.record('one', sh())
    h.record('two', sh())
    h.record('one', ag()) // same text, other kind
    conn.url = 'ws://b.example/ws'
    h.record('keep', sh())
    conn.url = 'ws://a.example/ws'
    h.remove({ c: 'one' })
    expect(h.entries.map((e) => `${e.k ?? 'sh'}:${e.c}`).sort()).toEqual(['ag:one', 'sh:two'])
    h.clear()
    expect(h.entries).toEqual([])
    conn.url = 'ws://b.example/ws'
    expect(h.entries.map((e) => e.c)).toEqual(['keep'])
  })

  it('can clear one kind, or one workspace', () => {
    const h = useCmdHistoryStore()
    h.record('a command', sh())
    h.record('a prompt', ag())
    h.clear('ag')
    expect(h.entries.map((e) => e.c)).toEqual(['a command'])

    h.record('in w1', sh('p', { host: 'local', workspaceId: 'w1' }))
    h.record('in w2', sh('p', { host: 'local', workspaceId: 'w2' }))
    h.clear('all', { host: 'local', workspaceId: 'w1' })
    expect(h.entries.map((e) => e.c).sort()).toEqual(['a command', 'in w2'])
  })

  it('survives corrupt stored data', () => {
    safeStorage.setItem('herdrem_cmd_history', '{not json')
    expect(useCmdHistoryStore().entries).toEqual([])
    safeStorage.setItem('herdrem_cmd_history', JSON.stringify({ x: 'nope', y: [{ c: 'ok', t: 1, n: 1 }] }))
    setActivePinia(createPinia())
    expect(useCmdHistoryStore().entries).toEqual([])
  })
})

describe('cmdHistory store — shared through the relay', () => {
  it('is shared only when the relay advertises input-history', () => {
    const conn = useConnectionStore()
    const h = useCmdHistoryStore()
    expect(h.shared).toBe(false)
    conn.setHello('test', ['input-history'])
    expect(h.shared).toBe(true)
  })

  it('applies a recorded entry at once and forwards it with its workspace', () => {
    const { h } = shared()
    h.record('refactor the parser', ag('p1', 'claude', 'api', { host: 'local', workspaceId: 'w1', workspaceLabel: 'herdrem' }))
    expect(h.entries).toHaveLength(1)
    expect(sent('history_record')).toEqual([{
      type: 'history_record',
      text: 'refactor the parser',
      kind: 'ag',
      pane_id: 'p1',
      host: 'local',
      workspace_id: 'w1',
      workspace_label: 'herdrem',
      agent: 'claude',
      title: 'api',
    }])
  })

  it('sends nothing for what is never remembered', () => {
    const { h } = shared()
    h.record('export TOKEN=x', sh())
    h.record(' private', sh())
    h.setRecordAgents(false)
    h.record('a prompt', ag())
    expect(sent()).toEqual([])
  })

  it('forwards removals and clears (relay-wide, per kind, per workspace)', () => {
    const { h } = shared()
    h.record('gone', sh())
    vi.mocked(ws.send).mockClear()
    h.remove({ c: 'gone' })
    h.remove({ c: 'p', k: 'ag' })
    h.clear('ag')
    h.clear('all', { host: 'local', workspaceId: 'w1' })
    expect(sent()).toEqual([
      { type: 'history_remove', kind: 'sh', text: 'gone' },
      { type: 'history_remove', kind: 'ag', text: 'p' },
      { type: 'history_clear', kind: 'ag', host: undefined, workspace_id: undefined },
      { type: 'history_clear', kind: 'all', host: 'local', workspace_id: 'w1' },
    ])
  })

  it('takes the relay snapshot as the truth once the first sync is done', () => {
    safeStorage.setItem('herdrem_history_seeded', JSON.stringify({ [RELAY]: true }))
    const { h } = shared()
    h.record('stale local', sh()) // not on the relay (e.g. deleted from another device)
    h.applySnapshot([{ c: 'from relay', t: 5, n: 2, h: 'local', s: 'w1' }, { c: 'export TOKEN=x', t: 4, n: 1 }, 'junk'])
    expect(h.entries.map((e) => e.c)).toEqual(['from relay']) // snapshot replaces; bad and secret rows dropped
    expect(h.entries[0]).toMatchObject({ n: 2, s: 'w1' })
  })

  it('uploads what the browser cached before the relay kept history — once', () => {
    safeStorage.setItem('herdrem_cmd_history', JSON.stringify({
      [RELAY]: [{ c: 'old cmd', t: 10, n: 3 }, { c: 'old prompt', t: 9, n: 1, k: 'ag', a: 'claude' }],
    }))
    const { h } = shared()
    expect(h.entries).toHaveLength(2)
    h.applySnapshot([{ c: 'on relay', t: 20, n: 1 }])
    const imports = sent('history_import')
    expect(imports).toHaveLength(1)
    expect((imports[0].entries as Array<{ c: string }>).map((e) => e.c).sort()).toEqual(['old cmd', 'old prompt'])
    expect(h.entries.map((e) => e.c).sort()).toEqual(['old cmd', 'old prompt', 'on relay'])

    // next connect: nothing re-uploaded, and what another device deleted stays deleted
    vi.mocked(ws.send).mockClear()
    h.applySnapshot([{ c: 'on relay', t: 20, n: 1 }])
    expect(sent('history_import')).toEqual([])
    expect(h.entries.map((e) => e.c)).toEqual(['on relay'])
  })

  it('does not upload an empty cache', () => {
    const { h } = shared()
    h.applySnapshot([{ c: 'on relay', t: 1, n: 1 }])
    expect(sent('history_import')).toEqual([])
  })

  it('queues entries made while the relay was unreachable and uploads them on the next sync', () => {
    const { h } = shared()
    h.applySnapshot([]) // first sync done
    vi.mocked(ws.send).mockReset()
    vi.mocked(ws.send).mockReturnValue(false) // socket down
    h.record('typed offline', sh('p1', { host: 'local', workspaceId: 'w1' }))
    expect(h.entries.map((e) => e.c)).toEqual(['typed offline'])

    vi.mocked(ws.send).mockReturnValue(true)
    h.applySnapshot([{ c: 'from relay', t: 1, n: 1 }])
    const imports = sent('history_import')
    expect(imports).toHaveLength(1)
    expect((imports[0].entries as Array<{ c: string; s?: string }>)[0]).toMatchObject({ c: 'typed offline', s: 'w1' })
    expect(h.entries.map((e) => e.c).sort()).toEqual(['from relay', 'typed offline'])

    // delivered: not uploaded again
    vi.mocked(ws.send).mockClear()
    h.applySnapshot([{ c: 'from relay', t: 1, n: 1 }, { c: 'typed offline', t: 99, n: 1 }])
    expect(sent('history_import')).toEqual([])
  })

  it('keeps the pending queue across a reload', () => {
    const { h } = shared()
    h.applySnapshot([])
    vi.mocked(ws.send).mockReturnValue(false)
    h.record('survives reload', sh())

    setActivePinia(createPinia())
    vi.mocked(ws.send).mockReturnValue(true)
    const { h: again } = shared()
    again.applySnapshot([])
    expect((sent('history_import')[0].entries as Array<{ c: string }>).map((e) => e.c)).toEqual(['survives reload'])
  })

  it('merges an entry pushed by another device (replacing the same slot)', () => {
    const { h } = shared()
    h.record('npm test', sh('p1', { host: 'local', workspaceId: 'w1' }))
    h.applyAdded({ c: 'npm test', t: Date.now() + 5, n: 4, h: 'local', s: 'w1', p: 'pOther' })
    h.applyAdded({ c: 'npm test', t: Date.now() + 5, n: 1, h: 'local', s: 'w2' })
    expect(h.entries).toHaveLength(2)
    expect(h.entries.find((e) => e.s === 'w1')).toMatchObject({ n: 4, p: 'pOther' })
    h.applyAdded('not an entry')
    h.applyAdded({ c: 'export TOKEN=x', t: 1, n: 1 })
    expect(h.entries.map((e) => e.c).sort()).toEqual(['npm test', 'npm test'])
  })

  it('applies removals and clears made on other devices', () => {
    const { h } = shared()
    h.record('keep', sh('p', { host: 'local', workspaceId: 'w1' }))
    h.record('drop', sh('p', { host: 'local', workspaceId: 'w2' }))
    h.record('a prompt', ag('p', 'claude', 'x', { host: 'local', workspaceId: 'w1' }))
    h.applyRemoved('sh', 'drop')
    expect(h.entries.map((e) => e.c).sort()).toEqual(['a prompt', 'keep'])
    h.applyCleared('ag')
    expect(h.entries.map((e) => e.c)).toEqual(['keep'])
    h.record('other', sh('p', { host: 'local', workspaceId: 'w2' }))
    h.applyCleared('all', 'local', 'w1')
    expect(h.entries.map((e) => e.c)).toEqual(['other'])
    h.applyCleared('all')
    expect(h.entries).toEqual([])
  })

  it('ignores malformed push messages', () => {
    const { h } = shared()
    h.record('safe', sh())
    h.applyRemoved('sh', 42)
    h.applyRemoved('sh', undefined)
    expect(h.entries.map((e) => e.c)).toEqual(['safe'])
  })
})

describe('chunkForImport', () => {
  it('keeps every message under the size limit and loses nothing', () => {
    const entries = Array.from({ length: 200 }, (_, i) => ({ c: `command number ${i} ${'x'.repeat(200)}`, t: i, n: 1 }))
    const chunks = chunkForImport(entries, 5000)
    expect(chunks.length).toBeGreaterThan(1)
    for (const c of chunks) expect(JSON.stringify({ type: 'history_import', entries: c }).length).toBeLessThan(5200)
    expect(chunks.flat()).toHaveLength(200)
  })

  it('puts an oversized single entry in a chunk of its own', () => {
    const big = { c: 'x'.repeat(9000), t: 1, n: 1 }
    const out = chunkForImport([{ c: 'a', t: 1, n: 1 }, big, { c: 'b', t: 1, n: 1 }], 5000)
    expect(out.map((c) => c.length)).toEqual([1, 1, 1])
  })

  it('returns nothing for nothing', () => {
    expect(chunkForImport([])).toEqual([])
  })
})
