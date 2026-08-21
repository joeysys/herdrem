import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTerminalStore } from './terminal'

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('useTerminalStore setContent', () => {
  it('appends incremental delta when a baseline exists', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.content = 'line1\nline2\n'
    term.setContent('p1', 'line3\n', true)
    expect(term.content).toBe('line1\nline2\nline3\n')
  })

  it('drops append delta when no baseline yet (raced ahead of full snapshot)', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.content = ''
    term.setContent('p1', 'line3\n', true)
    expect(term.content).toBe('')
  })

  it('replaces content on full snapshot', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.content = 'stale'
    term.setContent('p1', 'fresh\ncontent\n')
    expect(term.content).toBe('fresh\ncontent\n')
  })

  it('ignores messages for non-active panes', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.content = 'base'
    term.setContent('p2', 'other', true)
    expect(term.content).toBe('base')
  })

  it('appends delta when base_rev matches the accepted full snapshot rev', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'base\n', false, 10) // full snapshot, rev=10
    term.setContent('p1', 'next\n', true, 11, 10) // delta base_rev=10
    expect(term.content).toBe('base\nnext\n')
  })

  it('drops delta whose base_rev does not match the held baseline (stale/foreign delta)', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'base\n', false, 10) // we hold rev=10
    term.setContent('p1', 'foreign\n', true, 12, 11) // delta based on rev=11
    expect(term.content).toBe('base\n') // unchanged — corrupted delta rejected
  })

  it('drops delta when the full snapshot carried no rev and delta has base_rev', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'base\n', false) // rev undefined → unknown baseline
    term.setContent('p1', 'delta\n', true, 5, 4)
    expect(term.content).toBe('base\n')
  })

  it('accepts delta with base_rev when full snapshot had no rev but delta continues it', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'base\n', false) // no rev on full
    // after the first delta we learn the rev; subsequent deltas validate
    term.setContent('p1', 'd1\n', true, 6) // no base_rev → accepted (unknown baseline tolerated)
    term.setContent('p1', 'd2\n', true, 7, 6) // base_rev=6 == last accepted 6
    expect(term.content).toBe('base\nd1\nd2\n')
  })
})

describe('useTerminalStore content bounds (F3)', () => {
  it('caps in-memory content at MAX_CONTENT_CHARS', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    // Push a full snapshot larger than the cap (1MB)
    const big = 'x'.repeat(1_100_000)
    term.setContent('p1', big)
    expect(term.content.length).toBeLessThanOrEqual(1_000_000)
    // tail preserved
    expect(term.content.endsWith('x')).toBe(true)
  })

  it('persists only the capped tail to the cache', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    const big = 'y'.repeat(300_000)
    term.setContent('p1', big)
    // saveCache wrote a capped tail to localStorage
    const raw = localStorage.getItem('herdrem_term_cache') || ''
    const parsed = JSON.parse(raw)
    const cached = parsed['p1']
    expect(cached).toBeDefined()
    expect(cached.text.length).toBeLessThanOrEqual(200_000)
    expect(cached.text.endsWith('y')).toBe(true)
  })
})

describe('useTerminalStore cache clearing', () => {
  it('clearPaneCache removes one pane from the cache and storage', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'cached content', false, 5)
    expect(term.content).toBe('cached content')
    term.clearPaneCache('p1')
    // content reset + cache entry gone from localStorage
    expect(term.content).toBe('')
    const raw = localStorage.getItem('herdrem_term_cache') || '{}'
    expect(JSON.parse(raw)['p1']).toBeUndefined()
  })

  it('clearPaneCache only affects the named pane', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'content1', false, 1)
    // setContent only writes active pane; simulate p2 cache via storage
    term.clearPaneCache('p2') // non-active pane: no content reset
    expect(term.content).toBe('content1')
  })

  it('clearAllCache wipes memory and storage', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'content1', false, 1)
    term.clearAllCache()
    expect(term.content).toBe('')
    expect(localStorage.getItem('herdrem_term_cache')).toBeNull()
    // next setContent still works
    term.setContent('p1', 'fresh', false, 2)
    expect(term.content).toBe('fresh')
  })
})

describe('setContent poll mode (N2)', () => {
  it('poll full response with matching rev does not replace delta-grown content', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    // baseline from a full read at rev 10
    term.setContent('p1', 'line1\nline2\n', false, 10)
    // delta grows the content to 4 lines
    term.setContent('p1', 'line3\nline4\n', true, 11, 10)
    expect(term.content).toBe('line1\nline2\nline3\nline4\n')
    // poll full response (truncated tail, rev 11 matches) → content unchanged
    term.setContent('p1', 'line4\n', false, 11, undefined, true)
    expect(term.content).toBe('line1\nline2\nline3\nline4\n')
  })

  it('poll full response with mismatched rev clears the baseline (does not adopt it)', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    term.setContent('p1', 'content-a\n', false, 5)
    // poll reports rev 9 (server baseline moved on) → baseline must be cleared,
    // NOT adopted — the client does not hold rev 9's content.
    term.setContent('p1', 'new-tail\n', false, 9, undefined, true)
    expect(term.content).toBe('content-a\n') // not replaced by the poll tail
    // Baseline is now unknown: a delta claiming base_rev=9 must be rejected
    // (if the baseline had been adopted, it would splice and lose content).
    term.setContent('p1', 'ccc\n', true, 10, 9)
    expect(term.content).toBe('content-a\n')
  })

  it('N2: delta for the poll-reported rev is rejected while baseline is unknown (no silent content loss)', () => {
    const term = useTerminalStore()
    term.activePane = 'p1' as any
    // client holds rev 10 content
    term.setContent('p1', 'AAA\n', false, 10)
    // poll reports rev 11 (client missed R10→R11) → baseline cleared, resync
    term.setContent('p1', 'tail\n', false, 11, undefined, true)
    // a delta claiming base_rev=11 arrives before the resync response
    term.setContent('p1', 'CCC\n', true, 12, 11)
    // must NOT splice onto the stale content — rev 11's content would be lost
    expect(term.content).toBe('AAA\n')
  })
})
