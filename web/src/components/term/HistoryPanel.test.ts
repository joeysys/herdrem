import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import HistoryPanel from './HistoryPanel.vue'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { safeStorage } from '../../utils/storage'

vi.mock('../../api/ws', () => ({
  ws: { send: vi.fn(() => true), connect: vi.fn(), disconnect: vi.fn(), onMessage: vi.fn(() => () => {}), onStatus: vi.fn(() => () => {}), status: 'connected' },
}))

const W1 = { host: 'local', workspaceId: 'w1', workspaceLabel: 'herdrem' }
const W2 = { host: 'local', workspaceId: 'w2', workspaceLabel: 'billing' }
const sh = (paneId: string, w: typeof W1) => ({ kind: 'sh' as const, paneId, ...w })
const ag = (paneId: string, agent: string, label: string, w: typeof W1) => ({ kind: 'ag' as const, paneId, agent, label, ...w })

// Workspace herdrem (w1): 3 commands + 1 prompt. Workspace billing (w2): 1 command + 1 prompt.
function seeded() {
  const h = useCmdHistoryStore()
  h.record('docker ps', sh('p1', W1))
  h.record('git status', sh('p1', W1))
  h.record('review the docker setup', ag('p1', 'claude', 'infra', W1))
  h.record('explain the auth flow', ag('p2', 'codex', 'api', W2))
  h.record('git stash', sh('p1', W1))
  h.record('make invoices', sh('p3', W2))
  return h
}

const HERE = { paneId: 'p1', ...W1 }
const texts = (w: ReturnType<typeof mount>) => w.findAll('.hp-cmd .text').map((t) => t.text())
const kindChips = (w: ReturnType<typeof mount>) => w.findAll('.hp-chip:not(.scope):not(.sort)')
const scopeChips = (w: ReturnType<typeof mount>) => w.findAll('.hp-chip.scope')

beforeEach(() => {
  safeStorage.removeItem('herdrem_cmd_history')
  safeStorage.removeItem('herdrem_history_pending')
  safeStorage.removeItem('herdrem_history_seeded')
  setActivePinia(createPinia())
})

describe('HistoryPanel', () => {
  it('opens on this workspace and the current pane kind, inside a labelled dialog', () => {
    seeded()
    const shell = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    expect(shell.get('[role="dialog"]').attributes('aria-label')).toBe('Input history')
    expect(texts(shell)).toEqual(['git stash', 'git status', 'docker ps']) // not billing's "make invoices"
    const agent = mount(HistoryPanel, { props: { defaultKind: 'ag', where: HERE } })
    expect(texts(agent)).toEqual(['review the docker setup']) // not billing's prompt
  })

  it('offers pane / workspace / relay, defaulting to workspace', () => {
    seeded()
    const w = mount(HistoryPanel, { props: { where: HERE } })
    expect(scopeChips(w).map((c) => c.text())).toEqual(['pane', 'workspace', 'relay'])
    expect(scopeChips(w).map((c) => c.attributes('aria-pressed'))).toEqual(['false', 'true', 'false'])
    expect(scopeChips(w)[1].attributes('title')).toBe('Workspace: herdrem')
  })

  it('without a current pane it can only look relay-wide, and starts there', () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh' } })
    expect(scopeChips(w).map((c) => c.text())).toEqual(['relay'])
    expect(scopeChips(w)[0].attributes('aria-pressed')).toBe('true')
    expect(texts(w)).toContain('make invoices')
  })

  it('widening to the relay brings in other workspaces and says where each came from', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    await scopeChips(w)[2].trigger('click')
    expect(texts(w)).toContain('make invoices')
    const row = w.findAll('.hp-cmd').find((r) => r.text().includes('make invoices'))!
    expect(row.get('.meta').text()).toMatch(/billing/)
    // this workspace's own rows are not labelled with their own workspace
    await scopeChips(w)[1].trigger('click')
    expect(w.findAll('.hp-cmd .meta').some((m) => m.text().includes('herdrem'))).toBe(false)
  })

  it('shows each text once on the relay scope even when several workspaces ran it', async () => {
    const h = seeded()
    h.record('git status', sh('p3', W2))
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    await scopeChips(w)[2].trigger('click')
    expect(texts(w).filter((t) => t === 'git status')).toHaveLength(1)
    expect(w.findAll('.hp-cmd').find((r) => r.text().startsWith('git status'))!.get('.meta').text()).toMatch(/×2/)
  })

  it('the pane scope keeps only what was entered in this pane', async () => {
    const h = seeded()
    h.record('only in p9', sh('p9', W1))
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: { paneId: 'p9', ...W1 } } })
    await scopeChips(w)[0].trigger('click')
    expect(texts(w)).toEqual(['only in p9'])
  })

  it('kind chips switch between all / agents / shells and report their state', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    expect(kindChips(w).map((c) => c.text())).toEqual(['all', 'agents', 'shells'])
    expect(kindChips(w).map((c) => c.attributes('aria-pressed'))).toEqual(['false', 'false', 'true'])
    await kindChips(w)[0].trigger('click')
    expect(texts(w)).toHaveLength(4) // 3 commands + 1 prompt of this workspace
    expect(kindChips(w)[0].attributes('aria-pressed')).toBe('true')
  })

  it('shows which agent and pane a prompt went to', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'ag', where: HERE } })
    await scopeChips(w)[2].trigger('click')
    const metas = w.findAll('.hp-cmd .meta').map((m) => m.text())
    // this workspace's prompt ranks above the (newer) one from another workspace
    expect(metas[0]).toMatch(/^claude · infra · herdrem · /)
    expect(metas[1]).toMatch(/^codex · api · billing · /)
  })

  it('filters prompts by agent type; the select only exists while prompts are in view', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'ag', where: HERE } })
    await scopeChips(w)[2].trigger('click')
    const select = w.get('select[aria-label="Filter by agent type"]')
    expect(select.findAll('option').map((o) => o.text())).toEqual(['all agent types', 'claude', 'codex'])
    await select.setValue('codex')
    expect(texts(w)).toEqual(['explain the auth flow'])
    await kindChips(w)[2].trigger('click') // shells
    expect(w.find('select[aria-label="Filter by agent type"]').exists()).toBe(false)
    expect(texts(w)).toContain('git stash')
  })

  it('ranks the current pane first', () => {
    const h = useCmdHistoryStore()
    h.record('older in this pane', sh('p1', W1))
    h.record('newer in the workspace', sh('p2', W1))
    const w = mount(HistoryPanel, { props: { where: HERE } })
    expect(texts(w)[0]).toBe('older in this pane')
  })

  it('shows an empty state, and says when the filters hide everything', async () => {
    const empty = mount(HistoryPanel)
    expect(empty.text()).toContain('nothing yet')
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    await w.get('input[type="search"]').setValue('zzz')
    expect(w.text()).toContain('nothing matches')
  })

  it('searches as you type within the scope', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    await kindChips(w)[0].trigger('click')
    await w.get('input[type="search"]').setValue('docker')
    expect(texts(w)).toEqual(['docker ps', 'review the docker setup'])
  })

  it('picks on click without sending anything', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    await w.findAll('.hp-cmd')[1].trigger('click')
    expect(w.emitted('pick')![0]).toEqual(['git status'])
  })

  it('arrow keys move the selection and Enter picks it; Escape closes', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    const dialog = w.get('[role="dialog"]')
    await dialog.trigger('keydown', { key: 'ArrowDown' })
    await dialog.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('pick')![0]).toEqual(['git status'])
    await dialog.trigger('keydown', { key: 'ArrowUp' })
    await dialog.trigger('keydown', { key: 'ArrowUp' }) // wraps to the last row
    await dialog.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('pick')![1]).toEqual(['docker ps'])
    await dialog.trigger('keydown', { key: 'Escape' })
    expect(w.emitted('close')).toHaveLength(1)
  })

  it('leaves arrows/Enter alone on the filter controls', async () => {
    seeded()
    const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
    await w.get('.hp-chip').trigger('keydown', { key: 'Enter' })
    await w.get('.hp-chip').trigger('keydown', { key: 'ArrowDown' })
    await scopeChips(w)[0].trigger('keydown', { key: 'Enter' })
    expect(w.emitted('pick')).toBeUndefined()
  })

  it('removes only the entry of that kind', async () => {
    const h = seeded()
    h.record('review the docker setup', sh('p1', W1)) // same text, shell kind
    const w = mount(HistoryPanel, { props: { defaultKind: 'ag', where: HERE } })
    await w.get('[aria-label="Remove review the docker setup from history"]').trigger('click')
    expect(h.entries.map((e) => `${e.k ?? 'sh'}:${e.c}`)).toContain('sh:review the docker setup')
    expect(h.entries.map((e) => `${e.k ?? 'sh'}:${e.c}`)).not.toContain('ag:review the docker setup')
  })

  describe('clearing follows the scope', () => {
    it('workspace scope clears only this workspace', async () => {
      const h = seeded()
      const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
      const btn = w.get('.hp-clear')
      expect(btn.text()).toBe('clear commands here')
      await btn.trigger('click')
      expect(btn.text()).toBe('tap again to clear')
      expect(h.counts.sh).toBe(4) // nothing yet
      await btn.trigger('click')
      expect(h.entries.map((e) => e.c).sort()).toEqual(['explain the auth flow', 'make invoices', 'review the docker setup'])
    })

    it('relay scope clears the kind everywhere', async () => {
      const h = seeded()
      const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
      await scopeChips(w)[2].trigger('click')
      const btn = w.get('.hp-clear')
      expect(btn.text()).toBe('clear commands')
      expect(btn.attributes('title')).toMatch(/every device/)
      await btn.trigger('click')
      await btn.trigger('click')
      expect(h.counts).toEqual({ sh: 0, ag: 2 })
    })

    it('without a workspace it can only clear relay-wide', async () => {
      const h = seeded()
      const w = mount(HistoryPanel, { props: { defaultKind: 'ag' } })
      const btn = w.get('.hp-clear')
      expect(btn.text()).toBe('clear prompts')
      await btn.trigger('click')
      await btn.trigger('click')
      expect(h.counts.ag).toBe(0)
      expect(h.counts.sh).toBe(4)
    })

    it('keeps a favorite and can list the most-used first', async () => {
      const h = useCmdHistoryStore()
      h.applySnapshot([
        { c: 'rare new', t: 300, n: 1, h: 'local', s: 'w1', p: 'p1' },
        { c: 'often old', t: 100, n: 9, h: 'local', s: 'w1', p: 'p1' },
      ])
      const w = mount(HistoryPanel, { props: { defaultKind: 'sh', where: HERE } })
      expect(texts(w)).toEqual(['rare new', 'often old'])
      await w.get('.hp-chip.sort:last-child').trigger('click')
      expect(texts(w)).toEqual(['often old', 'rare new'])
      await w.get('[aria-label="Favorite rare new"]').trigger('click')
      const btn = w.get('.hp-clear')
      await btn.trigger('click')
      await btn.trigger('click')
      expect(h.entries.map((e) => e.c)).toEqual(['rare new'])
      expect(w.get('.hp-clear').attributes('disabled')).toBeDefined()
    })

    it('is disabled when there is nothing to clear in view', () => {
      const h = useCmdHistoryStore()
      h.record('only a command', sh('p1', W1))
      const w = mount(HistoryPanel, { props: { defaultKind: 'ag', where: HERE } })
      expect(w.get('.hp-clear').attributes('disabled')).toBeDefined()
    })
  })
})
