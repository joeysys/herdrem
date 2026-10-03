import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import TermView from './TermView.vue'
import { setShortcutContext } from '../../composables/useShortcuts'
import { useAgentsStore, useConnectionStore } from '../../stores/connection'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { useDiffStore } from '../../stores/diff'
import { useFilesStore } from '../../stores/files'
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

const flags = window as unknown as { __setStandalone: (v: boolean) => void }
let wrapper: VueWrapper | null = null

function mountShell() {
  useAgentsStore().setAgents([
    { pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'shell' },
  ])
  wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
  return wrapper
}

function mountAgent() {
  useAgentsStore().setAgents([
    { pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'agent' },
  ])
  useConnectionStore().setHello('test', ['files', 'git-diff'])
  wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
  return wrapper
}

function press(target: EventTarget, init: KeyboardEventInit) {
  const ev = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
  target.dispatchEvent(ev)
  return ev
}

beforeEach(() => {
  setActivePinia(createPinia())
  flags.__setStandalone(false)
  setShortcutContext({ overlay: () => false, terminal: () => true })
  vi.mocked(ws.send).mockClear()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  flags.__setStandalone(false)
  setShortcutContext({ overlay: () => false, terminal: () => false })
})

describe('TermView shell shortcuts', () => {
  it('sends Ctrl+C and clears the draft when nothing is selected', async () => {
    const w = mountShell()
    const input = w.get('input[aria-label="Command input"]').element as HTMLInputElement
    input.focus()
    await w.get('input[aria-label="Command input"]').setValue('rm -rf')
    const ev = press(input, { key: 'c', ctrlKey: true })
    await nextTick()
    expect(ev.defaultPrevented).toBe(true)
    expect(vi.mocked(ws.send)).toHaveBeenCalledWith({ type: 'send_keys', pane_id: 'p1', keys: 'Ctrl+c' })
    expect(input.value).toBe('')
  })

  it('leaves Ctrl+C alone when the draft has a selection', async () => {
    const w = mountShell()
    const input = w.get('input[aria-label="Command input"]').element as HTMLInputElement
    input.focus()
    await w.get('input[aria-label="Command input"]').setValue('rm -rf')
    input.setSelectionRange(0, 2)
    const ev = press(input, { key: 'c', ctrlKey: true })
    expect(ev.defaultPrevented).toBe(false)
    expect(vi.mocked(ws.send)).not.toHaveBeenCalledWith(expect.objectContaining({ type: 'send_keys' }))
    expect(input.value).toBe('rm -rf')
  })

  it('recalls the previous shell command on ArrowUp', async () => {
    const w = mountShell()
    useCmdHistoryStore().record('git status', { kind: 'sh', paneId: 'p1', host: 'local', workspaceId: 'default' })
    const input = w.get('input[aria-label="Command input"]')
    await input.trigger('focus')
    await input.setValue('')
    await input.trigger('keydown', { key: 'ArrowUp' })
    await nextTick()
    expect((input.element as HTMLInputElement).value).toBe('git status')
  })

  it('does not send Ctrl+L from a browser tab', async () => {
    const w = mountShell()
    const input = w.get('input[aria-label="Command input"]').element as HTMLInputElement
    input.focus()
    const ev = press(input, { key: 'l', ctrlKey: true })
    expect(ev.defaultPrevented).toBe(false)
    expect(vi.mocked(ws.send)).not.toHaveBeenCalledWith(expect.objectContaining({ keys: 'Ctrl+l' }))
    flags.__setStandalone(true)
    const live = press(input, { key: 'l', ctrlKey: true })
    expect(live.defaultPrevented).toBe(true)
    expect(vi.mocked(ws.send)).toHaveBeenCalledWith({ type: 'send_keys', pane_id: 'p1', keys: 'Ctrl+l' })
  })
})

describe('TermView terminal shortcuts', () => {
  it('i focuses the prompt and / inserts a slash without a second character', async () => {
    const w = mountAgent()
    const input = w.get('input[aria-label="Command to send to pane"]').element as HTMLInputElement
    const inserted = press(document, { key: 'i' })
    await nextTick()
    expect(inserted.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(input)
    input.blur()
    const slash = press(document, { key: '/' })
    await nextTick()
    expect(slash.defaultPrevented).toBe(true)
    expect(input.value).toBe('/')
    expect(document.activeElement).toBe(input)
  })

  it('e and g open files and diff only while this terminal is the active scope', async () => {
    mountAgent()
    press(document, { key: 'e' })
    await nextTick()
    expect(useFilesStore().browserOpen).toBe(true)
    press(document, { key: 'g' })
    await nextTick()
    expect(useDiffStore().open).toBe(true)
    wrapper?.unmount()
    wrapper = null
    setShortcutContext({ overlay: () => false, terminal: () => false })
    mountAgent()
    useDiffStore().close?.()
    useFilesStore().closeBrowser?.()
    press(document, { key: 'g' })
    expect(useDiffStore().open).toBe(false)
  })
})
