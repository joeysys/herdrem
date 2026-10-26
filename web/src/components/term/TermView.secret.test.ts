import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import TermView from './TermView.vue'
import { useAgentsStore, useConnectionStore } from '../../stores/connection'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { useTerminalStore } from '../../stores/terminal'
import { applyServerMessage, noteDisconnect } from '../../stores/sendAck'
import { ws } from '../../api/ws'
import { safeStorage } from '../../utils/storage'

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

beforeEach(() => {
  setActivePinia(createPinia())
  safeStorage.clear()
  vi.mocked(ws.send).mockClear()
})

afterEach(() => {
  noteDisconnect()
  wrapper?.unmount()
  wrapper = null
})

function mountShell() {
  useAgentsStore().setAgents([
    { pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'shell' },
  ])
  wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
  return wrapper
}

describe('TermView shell command send-ack', () => {
  it('clears and records immediately when the relay has no send-ack', async () => {
    const history = useCmdHistoryStore()
    const w = mountShell()
    const input = w.get('input[aria-label="Command input"]')
    await input.setValue('ls -la')
    await input.trigger('keydown', { key: 'Enter' })
    await nextTick()
    expect(ws.send).toHaveBeenCalledWith(expect.objectContaining({
      type: 'send_text', text: 'ls -la', submit: true,
    }))
    const sent = vi.mocked(ws.send).mock.calls.map((c) => c[0] as { request_id?: string })
    expect(sent.some((m) => m.request_id)).toBe(false)
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(history.entries.map((e) => e.c)).toContain('ls -la')
  })

  it('keeps the draft until sent, and records history only then', async () => {
    const conn = useConnectionStore()
    conn.setHello('t', ['send-ack'])
    conn.status = 'connected'
    const history = useCmdHistoryStore()
    const w = mountShell()
    const input = w.get('input[aria-label="Command input"]')
    await input.setValue('ls -la')
    await input.trigger('keydown', { key: 'Enter' })
    await nextTick()
    const call = vi.mocked(ws.send).mock.calls.find((c) => (c[0] as { type?: string }).type === 'send_text')
    expect(call?.[0]).toEqual(expect.objectContaining({
      type: 'send_text', text: 'ls -la', submit: true, request_id: expect.any(String),
    }))
    expect((input.element as HTMLInputElement).value).toBe('ls -la')
    expect(input.attributes('readonly')).toBeDefined()
    expect(w.get('.shell-note').text()).toBe('Sending')
    expect(history.entries.map((e) => e.c)).not.toContain('ls -la')

    applyServerMessage({ type: 'error', request_id: (call?.[0] as { request_id: string }).request_id, message: 'Send failed' })
    await flushPromises()
    expect((input.element as HTMLInputElement).value).toBe('ls -la')
    expect(w.get('.shell-note').text()).toBe('Send failed')
    expect(history.entries.map((e) => e.c)).not.toContain('ls -la')

    await input.trigger('keydown', { key: 'Enter' })
    await nextTick()
    const again = vi.mocked(ws.send).mock.calls.filter((c) => (c[0] as { type?: string }).type === 'send_text').at(-1)
    applyServerMessage({ type: 'sent', request_id: (again?.[0] as { request_id: string }).request_id })
    await flushPromises()
    expect((w.get('input[aria-label="Command input"]').element as HTMLInputElement).value).toBe('')
    expect(history.entries.map((e) => e.c)).toContain('ls -la')
    expect(w.find('.shell-note').exists()).toBe(false)
  })
})

describe('TermView shell secret input', () => {
  it('keeps a plain shell input when secret-input is off', async () => {
    const w = mountShell()
    useTerminalStore().setContent('p1', '[sudo] password for testuser:\n')
    await nextTick()
    expect(w.get('input[aria-label="Command input"]').attributes('type')).toBe('text')
    expect(w.find('button[aria-label="Cancel"]').exists()).toBe(false)
  })

  it('hides a shell password, skips history and drafts, and sends expect_tail', async () => {
    const conn = useConnectionStore()
    conn.setHello('t', ['secret-input', 'send-ack'])
    conn.status = 'connected'
    const term = useTerminalStore()
    term.setInputDraft('p1', 'keep me')
    const history = useCmdHistoryStore()
    const w = mountShell()
    term.setContent('p1', '[sudo] password for testuser:\n')
    await nextTick()

    const input = w.get('input[aria-label="Password"]')
    expect(input.attributes('type')).toBe('password')
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(term.getInputDraft('p1')).toBe('keep me')
    expect(w.find('.suggest-float').exists()).toBe(false)

    vi.mocked(ws.send).mockClear()
    await input.setValue('s3cret')
    expect(w.get('.inline-text').text()).not.toContain('s3cret')
    expect(w.get('.inline-text').text()).toContain('••••••')
    await input.trigger('keydown', { key: 'Enter' })
    await nextTick()

    expect(ws.send).toHaveBeenCalledWith(expect.objectContaining({
      type: 'send_text',
      pane_id: 'p1',
      text: 's3cret',
      submit: true,
      expect_tail: '[sudo] password for testuser:',
      request_id: expect.any(String),
    }))
    expect(history.entries.map((e) => e.c)).not.toContain('s3cret')
    expect(term.getInputDraft('p1')).toBe('keep me')
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(w.get('.shell-note').text()).toBe('Sending')

    noteDisconnect()
    await flushPromises()

    term.setContent('p1', "testuser@box's password:\n")
    await nextTick()
    const again = w.get('input[aria-label="Password"]')
    expect(again.attributes('type')).toBe('password')
    await again.setValue('later')
    await w.get('button[aria-label="Cancel"]').trigger('click')
    expect(ws.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'send_keys', keys: 'Ctrl+c' }))
    expect((w.get('input[aria-label="Password"]').element as HTMLInputElement).value).toBe('')
    expect(term.getInputDraft('p1')).toBe('keep me')

    term.setContent('p1', '[sudo] password for testuser:\n')
    await nextTick()
    await w.get('input[aria-label="Password"]').setValue('hidden')
    conn.status = 'disconnected'
    await nextTick()
    expect((w.get('input[aria-label="Password"]').element as HTMLInputElement).value).toBe('')
    expect(term.getInputDraft('p1')).toBe('keep me')
  })
})
