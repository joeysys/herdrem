import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import TermView from './TermView.vue'
import { useAgentsStore } from '../../stores/connection'
import { useTerminalStore } from '../../stores/terminal'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { safeStorage } from '../../utils/storage'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'connected',
  },
}))

let wrapper: VueWrapper | null = null
// The panes under test have no workspace_id, so the UI treats them as workspace "default" on host "local".
const HERE = { host: 'local', workspaceId: 'default' }
const sh = (paneId?: string, w: { host: string; workspaceId: string } = HERE) => ({ kind: 'sh' as const, paneId, ...w })
const ag = (paneId?: string, w: { host: string; workspaceId: string } = HERE) => ({ kind: 'ag' as const, paneId, agent: 'claude', label: 'demo', ...w })

function mountView(kind: 'agent' | 'shell') {
  const agents = useAgentsStore()
  agents.setAgents([{ pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind }])
  wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
  return wrapper
}

async function flush() {
  await nextTick()
  await nextTick()
}

beforeEach(() => {
  safeStorage.removeItem('herdrem_cmd_history')
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear()
  vi.useFakeTimers()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  vi.useRealTimers()
})

describe('TermView output search', () => {
  async function openAndType(w: VueWrapper, query: string) {
    await w.get('button[aria-label="Search output"]').trigger('click')
    await flush()
    await w.get('input[aria-label="Search output"]').setValue(query)
    await vi.advanceTimersByTimeAsync(150)
    await flush()
  }

  it('opens from the header, highlights matches and shows the count', async () => {
    const w = mountView('agent')
    useTerminalStore().content = 'build ok\nerror one\nfine\nerror two\n'
    await flush()
    await openAndType(w, 'error')
    expect(w.findAll('mark.term-hit')).toHaveLength(2)
    expect(w.get('.find-bar .count').text()).toBe('2/2') // newest match is active first
    expect(w.findAll('mark.term-hit')[1].classes()).toContain('active')
  })

  it('asks the relay for a deeper scrollback exactly once per pane', async () => {
    const w = mountView('agent')
    vi.mocked(ws.send).mockClear()
    await w.get('button[aria-label="Search output"]').trigger('click')
    await flush()
    const reads = vi.mocked(ws.send).mock.calls.map((c) => c[0] as { type: string; lines?: number; refresh?: boolean })
      .filter((m) => m.type === 'read_pane' && (m.lines ?? 0) >= 3000)
    expect(reads).toHaveLength(1)
    expect(reads[0].refresh).toBe(true)

    await w.get('button[aria-label="Close search"]').trigger('click')
    vi.mocked(ws.send).mockClear()
    await w.get('button[aria-label="Search output"]').trigger('click')
    await flush()
    const again = vi.mocked(ws.send).mock.calls.map((c) => c[0] as { type: string; lines?: number })
      .filter((m) => m.type === 'read_pane' && (m.lines ?? 0) >= 3000)
    expect(again).toHaveLength(0)
  })

  it('searches the whole retained output, not just the rendered tail window', async () => {
    const w = mountView('agent')
    const lines = Array.from({ length: 600 }, (_, i) => (i === 3 ? 'needle at the very top' : `line ${i}`))
    useTerminalStore().content = lines.join('\n')
    await flush()
    expect(w.text()).not.toContain('needle') // outside the 200-line window
    await openAndType(w, 'needle')
    expect(w.findAll('mark.term-hit')).toHaveLength(1)
    expect(w.get('.find-bar .count').text()).toBe('1/1')
  })

  it('Enter / Shift+Enter step through hits and wrap around', async () => {
    const w = mountView('agent')
    useTerminalStore().content = 'x a\nx b\nx c\n'
    await flush()
    await openAndType(w, 'x')
    const input = w.get('input[aria-label="Search output"]')
    expect(w.get('.count').text()).toBe('3/3')
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.get('.count').text()).toBe('1/3')
    await input.trigger('keydown', { key: 'Enter', shiftKey: true })
    expect(w.get('.count').text()).toBe('3/3')
    const active = w.findAll('mark.term-hit').map((m) => m.classes().includes('active'))
    expect(active).toEqual([false, false, true])
  })

  it('reports no match and disables navigation', async () => {
    const w = mountView('agent')
    useTerminalStore().content = 'hello\n'
    await flush()
    await openAndType(w, 'zzz')
    expect(w.get('.count').text()).toBe('no match')
    expect(w.get('button[aria-label="Next match"]').attributes('disabled')).toBeDefined()
  })

  it('Escape closes the bar and removes every highlight', async () => {
    const w = mountView('agent')
    useTerminalStore().content = 'error\n'
    await flush()
    await openAndType(w, 'error')
    await w.get('input[aria-label="Search output"]').trigger('keydown', { key: 'Escape' })
    await flush()
    expect(w.find('.find-bar').exists()).toBe(false)
    expect(w.findAll('mark.term-hit')).toHaveLength(0)
  })

  it('Ctrl+F opens the search instead of the browser find', async () => {
    const w = mountView('agent')
    const ev = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(ev)
    await flush()
    expect(ev.defaultPrevented).toBe(true)
    expect(w.find('.find-bar').exists()).toBe(true)
  })

  it('leaves Ctrl+F alone while another panel owns focus', async () => {
    const w = mountView('agent')
    const other = document.createElement('input')
    document.body.appendChild(other)
    other.focus()
    const ev = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(ev)
    await flush()
    expect(ev.defaultPrevented).toBe(false)
    expect(w.find('.find-bar').exists()).toBe(false)
    other.remove()
  })
})

describe('TermView shell history', () => {
  function inlineInput(w: VueWrapper) {
    return w.get('input[aria-label="Command input"]')
  }

  it('records a submitted command and never the secret ones', async () => {
    const w = mountView('shell')
    const input = inlineInput(w)
    await input.setValue('git status')
    await input.trigger('keydown', { key: 'Enter' })
    await input.setValue('export API_TOKEN=abc')
    await input.trigger('keydown', { key: 'Enter' })
    expect(useCmdHistoryStore().entries.map((e) => e.c)).toEqual(['git status'])
  })

  it('suggests matching history only while the prompt is focused', async () => {
    const w = mountView('shell')
    const h = useCmdHistoryStore()
    h.record('git status', sh('p1'))
    h.record('git stash', sh('p1'))
    const input = inlineInput(w)
    await flush() // a shell pane auto-focuses its prompt on mount (nextTick)
    await input.trigger('blur')
    await input.setValue('git st')
    expect(w.find('[role="listbox"]').exists()).toBe(false) // not focused
    await input.trigger('focus')
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual(['git stash', 'git status'])
    await input.trigger('blur')
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('Tab accepts the highlighted suggestion; arrows change it; text updates', async () => {
    const w = mountView('shell')
    const h = useCmdHistoryStore()
    h.record('git status', sh('p1'))
    h.record('git stash', sh('p1'))
    const input = inlineInput(w)
    await input.trigger('focus')
    await input.setValue('git st')
    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'Tab' })
    await flush()
    expect((input.element as HTMLInputElement).value).toBe('git status')
    expect(useTerminalStore().getInputDraft('p1')).toBe('git status')
  })

  it('Escape dismisses suggestions until the text changes', async () => {
    const w = mountView('shell')
    useCmdHistoryStore().record('git status', sh('p1'))
    const input = inlineInput(w)
    await input.trigger('focus')
    await input.setValue('git')
    expect(w.find('[role="listbox"]').exists()).toBe(true)
    await input.trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="listbox"]').exists()).toBe(false)
    await input.setValue('git s')
    expect(w.find('[role="listbox"]').exists()).toBe(true)
  })

  it('tapping a suggestion fills the prompt without submitting', async () => {
    const w = mountView('shell')
    useCmdHistoryStore().record('docker compose up -d', sh('p1'))
    const input = inlineInput(w)
    await input.trigger('focus')
    await input.setValue('docker')
    vi.mocked(ws.send).mockClear()
    await w.get('[role="option"]').trigger('click')
    await flush()
    expect((input.element as HTMLInputElement).value).toBe('docker compose up -d')
    expect(vi.mocked(ws.send).mock.calls.some((c) => (c[0] as { type: string }).type === 'send_text')).toBe(false)
  })

  it('Ctrl+R opens the history panel and a pick fills the prompt', async () => {
    const w = mountView('shell')
    useCmdHistoryStore().record('kubectl get pods', sh('p1'))
    const input = inlineInput(w)
    const ev = new KeyboardEvent('keydown', { key: 'r', ctrlKey: true, cancelable: true, bubbles: true })
    input.element.dispatchEvent(ev)
    await flush()
    expect(ev.defaultPrevented).toBe(true)
    expect(w.find('[aria-label="Input history"][role="dialog"]').exists()).toBe(true)
    await w.get('.hp-cmd').trigger('click')
    await flush()
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect((input.element as HTMLInputElement).value).toBe('kubectl get pods')
  })

  it('does not show shell suggestions in an agent pane (the agent input has its own)', async () => {
    const w = mountView('agent')
    expect(w.find('input[aria-label="Command input"]').exists()).toBe(false)
    expect(w.find('[aria-label="Command suggestions"]').exists()).toBe(false)
  })
})

describe('TermView agent prompt history', () => {
  const agentInput = (w: VueWrapper) => w.get('input.cmd')

  it('opens on prompts for an agent pane and shows both header and key-row buttons', async () => {
    const w = mountView('agent')
    useCmdHistoryStore().record('refactor the parser', ag('p1'))
    useCmdHistoryStore().record('ls -la', sh('p1'))
    expect(w.findAll('button[aria-label="Input history"]').length).toBeGreaterThanOrEqual(2)
    await w.get('.head-actions button[aria-label="Input history"]').trigger('click')
    await flush()
    const rows = w.findAll('.hp-cmd .text').map((t) => t.text())
    expect(rows).toEqual(['refactor the parser'])
  })

  it('the key-row ⏱ button opens the same panel', async () => {
    const w = mountView('agent')
    await w.get('.key-row button[aria-label="Input history"]').trigger('click')
    await flush()
    expect(w.find('[role="dialog"][aria-label="Input history"]').exists()).toBe(true)
  })

  it('picking a prompt replaces the agent input without sending it', async () => {
    const w = mountView('agent')
    useCmdHistoryStore().record('add retries to the uploader', ag('p1'))
    await agentInput(w).setValue('half typed')
    await w.get('.head-actions button[aria-label="Input history"]').trigger('click')
    await flush()
    vi.mocked(ws.send).mockClear()
    await w.get('.hp-cmd').trigger('click')
    await flush()
    expect((agentInput(w).element as HTMLInputElement).value).toBe('add retries to the uploader')
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect(vi.mocked(ws.send).mock.calls.some((c) => ['send_text', 'send_keys'].includes((c[0] as { type: string }).type))).toBe(false)
  })

  it('a prompt can be reused in a shell pane', async () => {
    const w = mountView('shell')
    useCmdHistoryStore().record('summarise the open PRs', ag('p9'))
    await w.get('.head-actions button[aria-label="Input history"]').trigger('click')
    await flush()
    await w.findAll('.hp-chip')[0].trigger('click') // all
    await w.get('.hp-cmd').trigger('click')
    await flush()
    expect((w.get('input[aria-label="Command input"]').element as HTMLInputElement).value).toBe('summarise the open PRs')
  })

  it('Ctrl+R in the agent input opens the panel instead of reloading', async () => {
    const w = mountView('agent')
    const ev = new KeyboardEvent('keydown', { key: 'r', ctrlKey: true, cancelable: true, bubbles: true })
    agentInput(w).element.dispatchEvent(ev)
    await flush()
    expect(ev.defaultPrevented).toBe(true)
    expect(w.find('[role="dialog"][aria-label="Input history"]').exists()).toBe(true)
  })
})

describe('TermView history scope and workspace', () => {
  function mountIn(kind: 'agent' | 'shell', ws: { id: string; label: string }) {
    const agents = useAgentsStore()
    agents.setAgents([{ pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'box', status: 'idle', kind, workspace_id: ws.id, workspace_label: ws.label } as never])
    wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
    return wrapper
  }

  it('records a shell command with the pane\'s host and workspace', async () => {
    const w = mountIn('shell', { id: 'w7', label: 'billing' })
    const input = w.get('input[aria-label="Command input"]')
    await input.setValue('make invoices')
    await input.trigger('keydown', { key: 'Enter' })
    expect(useCmdHistoryStore().entries[0]).toMatchObject({ c: 'make invoices', p: 'p1', h: 'box', s: 'w7', l: 'billing' })
  })

  it('records an agent prompt with the pane\'s host and workspace', async () => {
    const w = mountIn('agent', { id: 'w7', label: 'billing' })
    await w.get('input.cmd').setValue('summarise the failing invoices')
    await w.get('input.cmd').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(useCmdHistoryStore().entries[0]).toMatchObject({ c: 'summarise the failing invoices', k: 'ag', h: 'box', s: 'w7', l: 'billing' })
  })

  it('the panel opens on this workspace and the relay chip widens it', async () => {
    const w = mountIn('shell', { id: 'w7', label: 'billing' })
    const h = useCmdHistoryStore()
    h.record('here cmd', sh('p1', { host: 'box', workspaceId: 'w7' }))
    h.record('elsewhere cmd', sh('p9', { host: 'box', workspaceId: 'w8' }))
    await w.get('.head-actions button[aria-label="Input history"]').trigger('click')
    await flush()
    expect(w.findAll('.hp-cmd .text').map((t) => t.text())).toEqual(['here cmd'])
    await w.findAll('.hp-chip.scope')[2].trigger('click') // relay
    expect(w.findAll('.hp-cmd .text').map((t) => t.text()).sort()).toEqual(['elsewhere cmd', 'here cmd'])
  })

  it('suggestions rank this workspace first but still offer the rest of the relay', async () => {
    const w = mountIn('shell', { id: 'w7', label: 'billing' })
    const h = useCmdHistoryStore()
    h.record('git rebase main', sh('p9', { host: 'box', workspaceId: 'w8' })) // newer, elsewhere
    h.record('git status', sh('p1', { host: 'box', workspaceId: 'w7' }))
    await flush()
    const input = w.get('input[aria-label="Command input"]')
    await input.trigger('focus')
    await input.setValue('git')
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual(['git status', 'git rebase main'])
  })
})
