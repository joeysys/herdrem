import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import TermInput from './TermInput.vue'
import { useTerminalStore } from '../../stores/terminal'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { safeStorage } from '../../utils/storage'
import { nextTick } from 'vue'

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

const pane = { pane_id: 'p1', status: 'idle', agent: 'codex' }
const blockedPane = {
  ...pane,
  status: 'blocked',
  options: ['y', 'n'],
  option_labels: ['approve', 'deny'],
}

beforeEach(() => {
  safeStorage.removeItem('herdrem_cmd_history')
  safeStorage.removeItem('herdrem_history_agents')
  setActivePinia(createPinia())
})

describe('TermInput', () => {
  it('disables fill when the input is empty', () => {
    const wrapper = mount(TermInput, { props: { pane } })
    const send = wrapper.get('button.send-btn')
    expect(send.attributes('disabled')).toBeDefined()
    expect(send.attributes('aria-label')).toBe('Fill command into terminal')
  })

  it('fills then sends on two-phase submit', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendText = vi.spyOn(term, 'sendText').mockReturnValue(true)
    const sendKey = vi.spyOn(term, 'sendKey').mockReturnValue(true)

    const wrapper = mount(TermInput, { props: { pane } })
    await wrapper.get('input.cmd').setValue('echo hi')
    await wrapper.get('form.input-row').trigger('submit')
    expect(sendText).toHaveBeenCalledWith('echo hi', false)
    expect(wrapper.get('button.send-btn').attributes('aria-label')).toBe('Send command to pane')

    await wrapper.get('form.input-row').trigger('submit')
    expect(sendKey).toHaveBeenCalledWith('Enter')
  })

  it('quick-sends on Ctrl+Enter', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendText = vi.spyOn(term, 'sendText').mockReturnValue(true)

    const wrapper = mount(TermInput, { props: { pane } })
    await wrapper.get('input.cmd').setValue('pwd')
    await wrapper.get('input.cmd').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(sendText).toHaveBeenCalledWith('pwd', true)
  })

  it('does not submit a filled command after approving an already-blocked pane', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendText = vi.spyOn(term, 'sendText').mockReturnValue(true)
    const sendKey = vi.spyOn(term, 'sendKey').mockReturnValue(true)
    const respond = vi.spyOn(term, 'respond').mockReturnValue(true)

    const wrapper = mount(TermInput, { props: { pane: blockedPane } })
    await wrapper.get('input.cmd').setValue('echo hi')
    await wrapper.get('form.input-row').trigger('submit')
    await wrapper.get('button.appr-btn').trigger('click')
    await wrapper.get('form.input-row').trigger('submit')

    expect(respond).toHaveBeenCalledWith('y', 0)
    expect(sendKey).not.toHaveBeenCalled()
    expect(sendText).toHaveBeenCalledTimes(1)
  })

  it('exposes keyboard-accessible special keys', () => {
    const wrapper = mount(TermInput, { props: { pane } })
    expect(wrapper.get('input.cmd').attributes('aria-label')).toBe('Command to send to pane')
    expect(wrapper.find('[aria-label="Enter"]').exists()).toBe(true)
    expect(wrapper.find('[aria-label="Ctrl modifier"]').exists()).toBe(true)
  })
})

describe('TermInput prompt history', () => {
  const hpane = { pane_id: 'p1', status: 'idle', agent: 'codex', project: 'demo' }

  function setup() {
    const term = useTerminalStore()
    term.activePane = 'p1'
    vi.spyOn(term, 'sendText').mockReturnValue(true)
    vi.spyOn(term, 'sendKey').mockReturnValue(true)
    return { term, history: useCmdHistoryStore() }
  }

  it('records a prompt when it is submitted, not when it is merely filled', async () => {
    const { history } = setup()
    const w = mount(TermInput, { props: { pane: hpane } })
    await w.get('input.cmd').setValue('write a test for the parser')
    await w.get('form.input-row').trigger('submit') // fill only
    expect(history.entries).toEqual([])
    await w.get('form.input-row').trigger('submit') // Enter
    expect(history.entries).toHaveLength(1)
    expect(history.entries[0]).toMatchObject({ c: 'write a test for the parser', k: 'ag', p: 'p1', a: 'codex' })
    expect(history.entries[0].w).toBeTruthy()
  })

  it('also records when the Enter key button submits a filled prompt', async () => {
    const { history } = setup()
    const w = mount(TermInput, { props: { pane: hpane } })
    await w.get('input.cmd').setValue('explain this stack trace')
    await w.get('form.input-row').trigger('submit')
    await w.get('[aria-label="Enter"]').trigger('pointerdown')
    expect(history.entries.map((e) => e.c)).toEqual(['explain this stack trace'])
    // a second Enter has nothing filled in: no duplicate
    await w.get('[aria-label="Enter"]').trigger('pointerdown')
    expect(history.entries[0].n).toBe(1)
  })

  it('records a Ctrl+Enter quick send', async () => {
    const { history } = setup()
    const w = mount(TermInput, { props: { pane: hpane } })
    await w.get('input.cmd').setValue('run the linter')
    await w.get('input.cmd').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(history.entries.map((e) => e.c)).toEqual(['run the linter'])
  })

  it('counts a repeated prompt instead of duplicating it', async () => {
    const { history } = setup()
    const w = mount(TermInput, { props: { pane: hpane } })
    for (let i = 0; i < 2; i++) {
      await w.get('input.cmd').setValue('run the linter')
      await w.get('input.cmd').trigger('keydown', { key: 'Enter', ctrlKey: true })
    }
    expect(history.entries).toHaveLength(1)
    expect(history.entries[0].n).toBe(2)
  })

  it('does not record approvals, !tunnel, secrets, or when recording is off', async () => {
    const { history } = setup()
    const w = mount(TermInput, { props: { pane: blockedPane } })
    await w.get('input.cmd').setValue('echo hi')
    await w.get('form.input-row').trigger('submit')
    await w.get('button.appr-btn').trigger('click') // approval disarms; nothing was submitted
    await w.get('input.cmd').setValue('!tunnel 3000')
    await w.get('form.input-row').trigger('submit')
    await w.get('input.cmd').setValue('use password: hunter2 for the db')
    await w.get('input.cmd').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(history.entries).toEqual([])

    history.setRecordAgents(false)
    await w.get('input.cmd').setValue('a normal prompt')
    await w.get('input.cmd').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(history.entries).toEqual([])
  })

  it('suggests earlier prompts while the input is focused and accepts one with Tab', async () => {
    const { history } = setup()
    history.record('write a test for the parser', { kind: 'ag', paneId: 'p1', agent: 'codex' })
    history.record('write docs for the parser', { kind: 'ag', paneId: 'p1', agent: 'codex' })
    history.record('write a shell script', { kind: 'sh' }) // other kind: never offered
    const w = mount(TermInput, { props: { pane: hpane } })
    const input = w.get('input.cmd')
    await input.setValue('write')
    expect(w.find('[role="listbox"]').exists()).toBe(false) // not focused
    await input.trigger('focus')
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual([
      'write docs for the parser',
      'write a test for the parser',
    ])
    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'Tab' })
    await nextTick()
    expect((input.element as HTMLInputElement).value).toBe('write a test for the parser')
  })

  it('tapping a suggestion fills the box without sending, and Escape dismisses', async () => {
    const { history, term } = setup()
    history.record('refactor the cache layer', { kind: 'ag', paneId: 'p1' })
    const w = mount(TermInput, { props: { pane: hpane } })
    const input = w.get('input.cmd')
    await input.trigger('focus')
    await input.setValue('refac')
    await input.trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="listbox"]').exists()).toBe(false)
    await input.setValue('refact')
    await w.get('[role="option"]').trigger('click')
    await nextTick()
    expect((input.element as HTMLInputElement).value).toBe('refactor the cache layer')
    expect(term.sendText).not.toHaveBeenCalled()
  })

  it('accepting a suggestion after a fill disarms the pending Enter', async () => {
    const { history } = setup()
    history.record('second prompt', { kind: 'ag', paneId: 'p1' })
    const w = mount(TermInput, { props: { pane: hpane } })
    const input = w.get('input.cmd')
    await input.setValue('first prompt')
    await w.get('form.input-row').trigger('submit') // armed
    expect(w.get('button.send-btn').attributes('aria-label')).toBe('Send command to pane')
    await input.trigger('focus')
    await input.setValue('second')
    await w.get('[role="option"]').trigger('click')
    await nextTick()
    expect(w.get('button.send-btn').attributes('aria-label')).toBe('Fill command into terminal')
  })

  it('emits history from the ⏱ key and from Ctrl+R (which no longer reloads the page)', async () => {
    setup()
    const w = mount(TermInput, { props: { pane: hpane } })
    await w.get('[aria-label="Input history"]').trigger('click')
    const ev = new KeyboardEvent('keydown', { key: 'r', ctrlKey: true, cancelable: true, bubbles: true })
    w.get('input.cmd').element.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    expect(w.emitted('history')).toHaveLength(2)
  })

  it('a replace fill swaps the whole input (history pick)', async () => {
    const { term } = setup()
    const w = mount(TermInput, { props: { pane: hpane } })
    await w.get('input.cmd').setValue('typed so far')
    term.requestFill('from history', 'replace')
    await nextTick()
    expect((w.get('input.cmd').element as HTMLInputElement).value).toBe('from history')
  })
})
