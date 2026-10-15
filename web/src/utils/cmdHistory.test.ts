import { describe, it, expect } from 'vitest'
import {
  addEntry, capEntries, collapseByText, kindOf, mergeEntries, sanitizeEntries, search, shouldRecord, suggest, whereOf,
  LIMITS, type HistoryEntry, type RecordMeta,
} from './cmdHistory'

const SH: RecordMeta = { kind: 'sh' }
const AG: RecordMeta = { kind: 'ag' }
const e = (c: string, t: number, extra: Partial<HistoryEntry> = {}): HistoryEntry => ({ c, t, n: 1, ...extra })
const ag = (c: string, t: number, extra: Partial<HistoryEntry> = {}): HistoryEntry => e(c, t, { k: 'ag', ...extra })

describe('shouldRecord', () => {
  it('records ordinary commands and prompts', () => {
    expect(shouldRecord('git status')).toBe(true)
    expect(shouldRecord('ls -la /tmp', 'sh')).toBe(true)
    expect(shouldRecord('refactor the auth module to use sessions', 'ag')).toBe(true)
  })

  it('skips blank input and leading-space lines (ignorespace)', () => {
    expect(shouldRecord('')).toBe(false)
    expect(shouldRecord('   ')).toBe(false)
    expect(shouldRecord(' git status')).toBe(false)
    expect(shouldRecord(' secret prompt', 'ag')).toBe(false)
  })

  it('keeps shell commands single-line and short, but allows long multi-line prompts', () => {
    expect(shouldRecord('echo a\necho b', 'sh')).toBe(false)
    expect(shouldRecord('x'.repeat(LIMITS.sh.maxLen + 1), 'sh')).toBe(false)
    expect(shouldRecord('line one\nline two', 'ag')).toBe(true)
    expect(shouldRecord('x'.repeat(LIMITS.sh.maxLen + 1), 'ag')).toBe(true)
    expect(shouldRecord('x'.repeat(LIMITS.ag.maxLen + 1), 'ag')).toBe(false)
  })

  it.each([
    'export API_TOKEN=abc123',
    'mysql --password hunter2',
    'curl -H "Authorization: Bearer abc" https://x',
    'curl -H "authorization: token x" https://x',
    'git clone https://user:pw@github.com/a/b.git',
    'PASSWORD=foo ./run.sh',
    '-----BEGIN RSA PRIVATE KEY-----',
  ])('never records credential-looking text: %s', (cmd) => {
    expect(shouldRecord(cmd, 'sh')).toBe(false)
    expect(shouldRecord(cmd, 'ag')).toBe(false)
  })

  it('does not over-match words that merely contain "pass"', () => {
    expect(shouldRecord('cat bypass.txt')).toBe(true)
    expect(shouldRecord('echo passenger')).toBe(true)
  })
})

describe('addEntry', () => {
  it('puts the newest first and dedupes within a kind, counting repeats', () => {
    let l: HistoryEntry[] = []
    l = addEntry(l, 'ls', { kind: 'sh', paneId: 'p1' }, 1)
    l = addEntry(l, 'pwd', { kind: 'sh', paneId: 'p1' }, 2)
    l = addEntry(l, 'ls', { kind: 'sh', paneId: 'p2' }, 3)
    expect(l.map((x) => x.c)).toEqual(['ls', 'pwd'])
    expect(l[0]).toMatchObject({ n: 2, t: 3, p: 'p2' })
  })

  it('keeps the same text as separate entries for different kinds', () => {
    let l = addEntry([], 'status', SH, 1)
    l = addEntry(l, 'status', AG, 2)
    expect(l).toHaveLength(2)
    expect(l.map(kindOf).sort()).toEqual(['ag', 'sh'])
  })

  it('stores agent type and pane title only on agent entries, clipped', () => {
    const l = addEntry([], 'fix it', { kind: 'ag', paneId: 'p1', agent: 'claude', label: 'x'.repeat(200) }, 1)
    expect(l[0].k).toBe('ag')
    expect(l[0].a).toBe('claude')
    expect(l[0].w).toHaveLength(80)
    const sh = addEntry([], 'ls', { kind: 'sh', agent: 'claude', label: 'nope' }, 1)
    expect(sh[0].a).toBeUndefined()
    expect(sh[0].k).toBeUndefined()
  })

  it('trims and keeps the previous pane / agent when none is given', () => {
    let l = addEntry([], '  fix it  ', { kind: 'ag', paneId: 'p1', agent: 'claude', label: 'proj' }, 1)
    l = addEntry(l, 'fix it', AG, 2)
    expect(l[0]).toMatchObject({ c: 'fix it', p: 'p1', a: 'claude', w: 'proj', n: 2 })
  })

  it('caps each kind on its own, dropping the oldest', () => {
    let l: HistoryEntry[] = []
    for (let i = 0; i < 12; i++) l = addEntry(l, `cmd${i}`, SH, i, 10)
    expect(l).toHaveLength(10)
    expect(l[0].c).toBe('cmd11')
    expect(l.some((x) => x.c === 'cmd0')).toBe(false)

    // a flood of prompts never evicts shell commands
    let m: HistoryEntry[] = [e('keep me', 1)]
    for (let i = 0; i < LIMITS.ag.maxEntries + 20; i++) m = addEntry(m, `prompt ${i}`, AG, 10 + i)
    expect(m.filter((x) => kindOf(x) === 'ag')).toHaveLength(LIMITS.ag.maxEntries)
    expect(m.some((x) => x.c === 'keep me')).toBe(true)
  })
})

describe('suggest', () => {
  const list = [e('git status', 5), e('git stash', 4), e('npm test', 3), e('echo git st', 2), e('git push', 1)]
  const sh = { kind: 'sh' as const }

  it('returns nothing for blank input', () => {
    expect(suggest(list, '', sh)).toEqual([])
    expect(suggest(list, '  ', sh)).toEqual([])
  })

  it('ranks prefix matches before substring matches, newest first', () => {
    expect(suggest(list, 'git st', sh)).toEqual(['git status', 'git stash', 'echo git st'])
  })

  it('never suggests exactly what is already typed', () => {
    expect(suggest(list, 'git status', sh)).toEqual([])
  })

  it('prefers the current pane inside each group', () => {
    const l = [e('git status', 9, { p: 'a' }), e('git stash', 1, { p: 'b' })]
    expect(suggest(l, 'git st', { kind: 'sh', paneId: 'b' })).toEqual(['git stash', 'git status'])
  })

  it('honours the limit and is case-insensitive for substring matches', () => {
    expect(suggest(list, 'git', sh, 2)).toHaveLength(2)
    expect(suggest([e('Docker ps', 1)], 'docker', sh)).toEqual(['Docker ps'])
  })

  it('only offers entries of the same kind', () => {
    const l = [e('git status', 5), ag('git status of the repo please', 6), ag('summarise git log', 7)]
    expect(suggest(l, 'git', { kind: 'sh' })).toEqual(['git status'])
    expect(suggest(l, 'git', { kind: 'ag' })).toEqual(['git status of the repo please', 'summarise git log'])
  })

  it('does a substring match only from two characters on (one letter is noise)', () => {
    const l = [ag('add a test', 2), ag('refactor', 1)]
    expect(suggest(l, 'a', { kind: 'ag' })).toEqual(['add a test']) // prefix only
    expect(suggest(l, 'te', { kind: 'ag' })).toEqual(['add a test'])
  })
})

describe('search', () => {
  const list = [
    e('docker compose up -d', 5),
    e('git commit -m fix', 4),
    ag('review the docker setup', 3, { a: 'claude', p: 'p1', w: 'infra' }),
    ag('explain the auth flow', 2, { a: 'codex', p: 'p2', w: 'api' }),
    e('cd ~/proj/herdrem', 1),
  ]
  const texts = (r: HistoryEntry[]) => r.map((x) => x.c)

  it('returns everything by recency for an empty query', () => {
    expect(texts(search(list, ''))).toEqual(list.map((x) => x.c))
  })

  it('matches all tokens in any order, across kinds by default', () => {
    expect(texts(search(list, 'docker'))).toEqual(['docker compose up -d', 'review the docker setup'])
    expect(texts(search(list, 'compose docker'))).toEqual(['docker compose up -d'])
  })

  it('filters by kind', () => {
    expect(texts(search(list, '', { kind: 'ag' }))).toEqual(['review the docker setup', 'explain the auth flow'])
    expect(texts(search(list, 'docker', { kind: 'sh' }))).toEqual(['docker compose up -d'])
  })

  it('filters prompts by agent type (and drops shell entries when set)', () => {
    expect(texts(search(list, '', { agent: 'codex' }))).toEqual(['explain the auth flow'])
    expect(texts(search(list, '', { kind: 'all', agent: 'claude' }))).toEqual(['review the docker setup'])
  })

  it('can be limited to the current pane', () => {
    expect(texts(search(list, '', { paneId: 'p2', scope: 'pane' }))).toEqual(['explain the auth flow'])
  })

  it('ignores a pane scope when there is no current pane', () => {
    expect(search(list, '', { scope: 'pane' })).toHaveLength(list.length)
  })

  it('ranks whole-query prefix above plain token matches', () => {
    const l = [e('run docker ps', 9), e('docker ps', 1)]
    expect(texts(search(l, 'docker ps'))).toEqual(['docker ps', 'run docker ps'])
  })

  it('falls back to a loose subsequence match', () => {
    expect(texts(search(list, 'gcm'))).toEqual(['git commit -m fix'])
  })

  it('returns nothing when no entry matches', () => {
    expect(search(list, 'zzzz')).toEqual([])
  })

  it('floats the current pane to the top of an empty-query list', () => {
    const l = [e('a', 9), e('b', 1, { p: 'x' })]
    expect(texts(search(l, '', { paneId: 'x' }))).toEqual(['b', 'a'])
  })
})

describe('sanitizeEntries', () => {
  it('drops malformed rows and repairs counts', () => {
    const out = sanitizeEntries([
      { c: 'ok', t: 2, n: 3, p: 'p' },
      { c: '', t: 1, n: 1 },
      { c: 'no time', n: 1 },
      { c: 'bad count', t: 1, n: -4 },
      'junk',
      null,
    ])
    expect(out).toEqual([{ c: 'ok', t: 2, n: 3, p: 'p' }, { c: 'bad count', t: 1, n: 1 }])
  })

  it('treats rows without a kind as shell commands (data from before agent prompts)', () => {
    const out = sanitizeEntries([{ c: 'ls', t: 1, n: 1 }])
    expect(kindOf(out[0])).toBe('sh')
  })

  it('keeps agent metadata, enforces per-kind length limits, ignores meta on shell rows', () => {
    const out = sanitizeEntries([
      { c: 'fix it', t: 3, n: 1, k: 'ag', a: 'claude', w: 'proj' },
      { c: 'x'.repeat(LIMITS.sh.maxLen + 1), t: 2, n: 1 },
      { c: 'y'.repeat(LIMITS.sh.maxLen + 1), t: 1, n: 1, k: 'ag' },
      { c: 'ls', t: 0, n: 1, a: 'claude' },
    ])
    expect(out.map((x) => x.c)).toEqual(['fix it', 'y'.repeat(LIMITS.sh.maxLen + 1), 'ls'])
    expect(out[0]).toMatchObject({ k: 'ag', a: 'claude', w: 'proj' })
    expect(out[2].a).toBeUndefined()
  })

  it('drops credential-looking rows even if something stored them', () => {
    const out = sanitizeEntries([{ c: 'export API_TOKEN=abc', t: 2, n: 1 }, { c: 'ls', t: 1, n: 1 }])
    expect(out.map((x) => x.c)).toEqual(['ls'])
  })

  it('rejects non-arrays', () => {
    expect(sanitizeEntries({})).toEqual([])
    expect(sanitizeEntries(undefined)).toEqual([])
  })
})

describe('workspaces', () => {
  const W = { host: 'local', workspaceId: 'w1' }
  const inW = (c: string, t: number, extra: Partial<HistoryEntry> = {}) => e(c, t, { h: 'local', s: 'w1', ...extra })
  const elsewhere = (c: string, t: number, extra: Partial<HistoryEntry> = {}) => e(c, t, { h: 'local', s: 'w2', ...extra })
  const texts = (r: HistoryEntry[]) => r.map((x) => x.c)

  it('addEntry keeps the same text in two workspaces as two entries, and counts within one', () => {
    let l = addEntry([], 'make test', { kind: 'sh', host: 'local', workspaceId: 'w1', workspaceLabel: 'api' }, 1)
    l = addEntry(l, 'make test', { kind: 'sh', host: 'local', workspaceId: 'w2' }, 2)
    l = addEntry(l, 'make test', { kind: 'sh', host: 'local', workspaceId: 'w1' }, 3)
    expect(l).toHaveLength(2)
    const w1 = l.find((x) => x.s === 'w1')!
    expect(w1).toMatchObject({ n: 2, t: 3, h: 'local', l: 'api' }) // label kept from the first time
    expect(l.find((x) => x.s === 'w2')!.n).toBe(1)
  })

  it('a different host with the same workspace id is a different workspace', () => {
    let l = addEntry([], 'ls', { kind: 'sh', host: 'a', workspaceId: 'w' }, 1)
    l = addEntry(l, 'ls', { kind: 'sh', host: 'b', workspaceId: 'w' }, 2)
    expect(l).toHaveLength(2)
  })

  it('search scope "workspace" keeps only that workspace, and not entries with no workspace', () => {
    const l = [inW('here', 5), elsewhere('there', 4), e('legacy', 3)]
    expect(texts(search(l, '', { ...W, scope: 'workspace' }))).toEqual(['here'])
    expect(texts(search(l, '', { ...W, scope: 'relay' }))).toEqual(['here', 'there', 'legacy'])
  })

  it('"workspace" scope without a known workspace falls back to everything', () => {
    const l = [inW('here', 5), elsewhere('there', 4)]
    expect(search(l, '', { scope: 'workspace' })).toHaveLength(2)
  })

  it('relay scope shows each text once, newest copy, counts added up', () => {
    const l = [inW('make test', 9, { n: 2 }), elsewhere('make test', 5, { n: 3 }), elsewhere('make lint', 1)]
    const out = search(l, '', { ...W, scope: 'relay' })
    expect(texts(out)).toEqual(['make test', 'make lint'])
    expect(out[0]).toMatchObject({ n: 5, t: 9, s: 'w1' })
  })

  it('workspace scope does not collapse (each entry is already one per workspace)', () => {
    const l = [inW('x', 2), inW('y', 1)]
    expect(search(l, '', { ...W, scope: 'workspace' })).toHaveLength(2)
  })

  it('collapseByText keeps shell and agent text apart', () => {
    const out = collapseByText([e('same', 1), ag('same', 2)])
    expect(out).toHaveLength(2)
  })

  it('ranks pane, then workspace, then the rest of the relay', () => {
    const l = [
      elsewhere('git a-relay', 9),
      inW('git b-workspace', 5),
      inW('git c-pane', 1, { p: 'pX' }),
    ]
    expect(suggest(l, 'git', { kind: 'sh', paneId: 'pX', ...W })).toEqual(['git c-pane', 'git b-workspace', 'git a-relay'])
    expect(texts(search(l, '', { paneId: 'pX', ...W, scope: 'relay' }))).toEqual(['git c-pane', 'git b-workspace', 'git a-relay'])
  })

  it('suggestions offer a text once even if several workspaces have it', () => {
    const l = [inW('npm test', 1), elsewhere('npm test', 9)]
    expect(suggest(l, 'npm', { kind: 'sh', ...W })).toEqual(['npm test'])
  })

  it('whereOf reads the pane the way the todo binding does', () => {
    expect(whereOf(undefined)).toEqual({})
    expect(whereOf({ pane_id: 'p1' } as never)).toEqual({ paneId: 'p1', host: 'local', workspaceId: 'default', workspaceLabel: undefined })
    expect(whereOf({ pane_id: 'p1', host: 'box', workspace_id: 'w9', workspace_label: 'api' } as never))
      .toEqual({ paneId: 'p1', host: 'box', workspaceId: 'w9', workspaceLabel: 'api' })
  })

  it('sanitizeEntries keeps and clips workspace fields', () => {
    const out = sanitizeEntries([{ c: 'ls', t: 1, n: 1, h: 'local', s: 'w1', l: 'x'.repeat(500) }])
    expect(out[0]).toMatchObject({ h: 'local', s: 'w1' })
    expect(out[0].l).toHaveLength(256)
  })
})

describe('mergeEntries / capEntries', () => {
  it('folds incoming slot by slot without losing a newer time or higher count', () => {
    const base = [e('ls', 600, { n: 2, h: 'local', s: 'w' })]
    const out = mergeEntries(base, [
      e('ls', 100, { n: 1, h: 'local', s: 'w' }), // older, lower: no regression
      e('old', 50, { n: 3 }),
    ])
    expect(out.find((x) => x.c === 'ls')).toMatchObject({ n: 2, t: 600 })
    expect(out.find((x) => x.c === 'old')).toMatchObject({ n: 3 })
  })

  it('takes the incoming copy when it is newer, and the larger count', () => {
    const out = mergeEntries([e('ls', 1, { n: 5, p: 'old' })], [e('ls', 9, { n: 2, p: 'new' })])
    expect(out[0]).toMatchObject({ t: 9, n: 5, p: 'new' })
  })

  it('capEntries sorts newest first and caps each kind', () => {
    const many = Array.from({ length: LIMITS.ag.maxEntries + 5 }, (_, i) => ag(`p${i}`, i))
    const out = capEntries([e('sh', 1), ...many])
    expect(out.filter((x) => kindOf(x) === 'ag')).toHaveLength(LIMITS.ag.maxEntries)
    expect(out.some((x) => x.c === 'sh')).toBe(true)
    expect(out[0].t).toBeGreaterThanOrEqual(out[1].t)
  })

  it('keeps a favorite across another use, a merge, and the cap', () => {
    let l = addEntry([], 'ls', SH, 1)
    l = [{ ...l[0], f: true }]
    l = addEntry(l, 'ls', SH, 2)
    expect(l[0]).toMatchObject({ f: true, n: 2, t: 2 })
    const merged = mergeEntries([e('old', 1, { n: 1, f: true })], [e('old', 5, { n: 2 })])
    expect(merged[0].f).toBe(true)
    const capped = capEntries([e('fav', 1, { f: true }), ...Array.from({ length: 12 }, (_, i) => e(`c${i}`, 10 + i))], 10)
    expect(capped.some((x) => x.c === 'fav')).toBe(true)
    expect(capped.filter((x) => !x.f)).toHaveLength(10)
  })

  it('sorts by last used unless asked for frequency', () => {
    const list = [e('new', 10, { n: 1 }), e('old', 1, { n: 5 })]
    expect(search(list, '', {}).map((x) => x.c)).toEqual(['new', 'old'])
    expect(search(list, '', { sort: 'often' }).map((x) => x.c)).toEqual(['old', 'new'])
  })
})
