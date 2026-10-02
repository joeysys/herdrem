// Scrolling up from the bottom switches the terminal from the tail window to
// the full content. The viewport must stay on the same lines: the window is the
// tail of the full content, so the distance from the bottom is invariant.
//
// Bug: the switch estimated the added height as dropped lines × one line's
// height. Lines holding fallback-font glyphs (box drawing, emoji, CJK) are
// taller than that, so the estimate fell short and the view jumped far up.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import TermView from './TermView.vue'
import { useAgentsStore } from '../../stores/connection'
import { useTerminalStore } from '../../stores/terminal'

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

beforeEach(() => {
  setActivePinia(createPinia())
})

const CLIENT_H = 400

/** fakeLayout gives the scroller a height computed from its rendered lines:
 *  plain lines 20px, lines with a box-drawing glyph 24px (taller, as with a
 *  fallback font). jsdom has no layout, so scrollHeight/scrollTop are stubbed. */
function fakeLayout(el: HTMLElement) {
  let top = 0
  const height = () => {
    const text = el.querySelector('.ansi-stream')?.textContent ?? ''
    return text.split('\n').reduce((h, line) => h + (line.includes('│') ? 24 : 20), 0)
  }
  Object.defineProperty(el, 'scrollHeight', { configurable: true, get: height })
  Object.defineProperty(el, 'clientHeight', { configurable: true, get: () => CLIENT_H })
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get: () => top,
    set: (v: number) => { top = Math.max(0, Math.min(v, height() - CLIENT_H)) },
  })
}

async function settle() {
  await nextTick()
  await nextTick()
  await new Promise((r) => setTimeout(r, 0))
}

describe('TermView scroll-up from the tail window', () => {
  it('keeps the same lines in view when the full content replaces the window', async () => {
    useAgentsStore().setAgents([{ pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'agent' }])
    const term = useTerminalStore()
    const wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
    const el = wrapper.get('.term-scroll').element as HTMLElement
    fakeLayout(el)

    // 600 lines: the 400 that fall outside the 200-line window are tall.
    const lines = Array.from({ length: 600 }, (_, i) => (i < 400 ? `│ old ${i}` : `line ${i}`))
    term.content = lines.join('\n')
    await settle()
    expect(term.stickBottom).toBe(true)
    expect(el.textContent).not.toContain('│ old 0') // windowed

    // user scrolls 100px up from the bottom
    el.scrollTop = el.scrollHeight - CLIENT_H - 100
    const fromBottom = el.scrollHeight - el.scrollTop
    el.dispatchEvent(new Event('scroll'))
    await settle()

    expect(term.stickBottom).toBe(false)
    expect(el.textContent).toContain('│ old 0') // full content rendered
    expect(el.scrollHeight - el.scrollTop).toBe(fromBottom)

    wrapper.unmount()
  })
})
