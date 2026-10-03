import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import App from './App.vue'
import { useAgentsStore, useConnectionStore } from './stores/connection'
import { isShortcutActive } from './composables/useShortcuts'
import { useLayoutMode } from './composables/useLayoutMode'
import { ws } from './api/ws'

vi.mock('./api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'connected',
  },
}))

const flags = window as unknown as { __setCoarsePointer: (v: boolean) => void; __setStandalone: (v: boolean) => void }

let wrapper: VueWrapper | null = null

function key(init: KeyboardEventInit) {
  const ev = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
  document.dispatchEvent(ev)
  return ev
}

async function flush() {
  await nextTick()
  await nextTick()
}

beforeEach(() => {
  setActivePinia(createPinia())
  flags.__setCoarsePointer(false)
  vi.mocked(ws.send).mockClear()
})

const platform = Object.getOwnPropertyDescriptor(navigator, 'platform')

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  flags.__setCoarsePointer(false)
  flags.__setStandalone(false)
  if (platform) Object.defineProperty(navigator, 'platform', platform)
})

async function seed() {
  useConnectionStore().booted = true
  wrapper = mount(App, { attachTo: document.body })
  // boot() during mount wipes whatever was seeded before it
  useAgentsStore().setAgents([
    { pane_id: 'p1', project: 'one', cwd: '/tmp/one', host: 'local', status: 'blocked', kind: 'agent', last_active: 2 },
    { pane_id: 'p2', project: 'two', cwd: '/tmp/two', host: 'local', status: 'idle', kind: 'agent', last_active: 1 },
  ])
  await flush()
  return wrapper
}

describe('desktop shortcuts on the list', () => {
  it('Esc leaves the terminal and stays in the app', async () => {
    const w = await seed()
    await w.get('article.agent-card').trigger('click')
    await flush()
    expect(w.find('[aria-label="Terminal"]').exists()).toBe(true)
    const focused = document.activeElement
    if (focused instanceof HTMLInputElement || focused instanceof HTMLTextAreaElement) {
      focused.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
      await flush()
      expect(w.find('[aria-label="Terminal"]').exists()).toBe(true)
      expect(document.activeElement).not.toBe(focused)
    }
    key({ key: 'Escape' })
    await flush()
    expect(w.find('[aria-label="Terminal"]').exists()).toBe(false)
    expect(w.find('article.agent-card').exists()).toBe(true)
    expect(w.find('.boot-splash').exists()).toBe(false)
  })

  it('Esc closes settings and does not leave the list', async () => {
    const w = await seed()
    await w.get('button[aria-label="Settings"]').trigger('click')
    await flush()
    expect(w.find('[aria-label="Settings"][role="dialog"]').exists()).toBe(true)
    key({ key: 'Escape' })
    await flush()
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect(w.find('article.agent-card').exists()).toBe(true)
    expect(w.find('[aria-label="Terminal"]').exists()).toBe(false)
  })

  it('j/k move DOM focus and y approves only the focused blocked card', async () => {
    const w = await seed()
    const cards = () => [...document.querySelectorAll<HTMLElement>('article.agent-card')]
    key({ key: 'j' })
    await flush()
    expect(document.activeElement).toBe(cards()[0])
    key({ key: 'j' })
    expect(document.activeElement).toBe(cards()[1])
    key({ key: 'y' })
    expect(ws.send).not.toHaveBeenCalled()
    cards()[0].focus()
    key({ key: 'y' })
    expect(ws.send).toHaveBeenCalledWith({ type: 'respond', pane_id: 'p1', text: 'y', submit: false })
    expect(w.find('[aria-label="Terminal"]').exists()).toBe(false)
  })

  it('Alt+] follows the pane list even when the cards are not on screen', async () => {
    const w = await seed()
    await w.get('article.agent-card').trigger('click')
    await flush()
    expect(w.find('[aria-label="Terminal"]').exists()).toBe(true)
    key({ key: '”', code: 'BracketRight', altKey: true })
    await flush()
    expect(document.body.textContent).toContain('two')
  })

  it('does not let global keys through an open overlay', async () => {
    const w = await seed()
    await w.get('button[aria-label="Settings"]').trigger('click')
    await flush()
    const before = document.activeElement
    key({ key: 'j' })
    key({ key: '”', code: 'BracketRight', altKey: true })
    await flush()
    expect(document.activeElement).toBe(before)
    expect(w.find('[aria-label="Terminal"]').exists()).toBe(false)
  })

  it('ignores bare keys on a coarse pointer but still closes on Esc', async () => {
    flags.__setCoarsePointer(true)
    const w = await seed()
    await w.get('button[aria-label="Settings"]').trigger('click')
    await flush()
    key({ key: 'j' })
    expect(document.activeElement?.classList.contains('agent-card')).toBe(false)
    key({ key: 'Escape' })
    await flush()
    expect(w.find('[role="dialog"]').exists()).toBe(false)
  })

  it('opens and closes the shortcut help', async () => {
    const w = await seed()
    key({ key: '?' })
    await flush()
    expect(w.get('#shortcut-help-title').text()).toBe('Shortcuts')
    key({ key: 'Escape' })
    await flush()
    expect(w.find('#shortcut-help-title').exists()).toBe(false)
  })

  it('opens the shortcut help from the header button on desktop only', async () => {
    const layout = useLayoutMode()
    layout.setPreference('desktop')
    try {
      const w = await seed()
      await w.get('header [aria-label="Keyboard shortcuts"]').trigger('click')
      await flush()
      expect(w.get('#shortcut-help-title').text()).toBe('Shortcuts')
      await w.get('[aria-label="Close shortcuts"]').trigger('click')
      await flush()
      layout.setPreference('mobile')
      await flush()
      expect(w.find('header [aria-label="Keyboard shortcuts"]').exists()).toBe(false)
    } finally {
      layout.setPreference('auto')
    }
  })

  it('marks ⌘⇧[ / ] unavailable in a Mac browser tab', async () => {
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true })
    await seed()
    expect(isShortcutActive('nav.prev.mac')).toBe(false)
    expect(isShortcutActive('nav.next.mac')).toBe(false)
    flags.__setStandalone(true)
    expect(isShortcutActive('nav.next.mac')).toBe(true)
  })
})
