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
    el.dispatchEvent(new Event('wheel'))
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

// Bug: while the user reads back, an agent TUI that keeps redrawing its status
// area makes the relay send full snapshots of a rolling window instead of
// append deltas. Each one drops the lines that scrolled off the top, so the
// same scrollTop now shows later text — the view "jumps" by however many
// lines were just printed, and only while the agent is busy.
describe('TermView while reading back', () => {
  const lineAt = (el: HTMLElement) => {
    const text = el.querySelector('.ansi-stream')?.textContent ?? ''
    return text.split('\n')[Math.floor(el.scrollTop / 20)]
  }

  async function scrolledUp() {
    useAgentsStore().setAgents([{ pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'agent' }])
    const term = useTerminalStore()
    term.activePane = 'p1'
    const wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
    const el = wrapper.get('.term-scroll').element as HTMLElement
    fakeLayout(el)
    const win = (from: number) => Array.from({ length: 500 }, (_, i) => `line ${from + i}`).join('\n')
    term.setContent('p1', win(0))
    await settle()
    el.dispatchEvent(new Event('wheel')) // the user scrolls…
    el.scrollTop = 3000 // …well above the bottom
    el.dispatchEvent(new Event('scroll'))
    await settle()
    expect(term.stickBottom).toBe(false)
    return { term, wrapper, el, win }
  }

  it('a full snapshot of a shifted window does not move the text being read', async () => {
    const { term, wrapper, el, win } = await scrolledUp()
    const before = lineAt(el)
    expect(before).toBe('line 450') // 3000px into the 200-line tail window

    term.setContent('p1', win(30)) // 30 new lines printed, 30 dropped at the top
    await settle()
    expect(lineAt(el)).toBe(before)
    wrapper.unmount()
  })

  it('offers the new output, and going there shows it', async () => {
    const { term, wrapper, el, win } = await scrolledUp()
    expect(wrapper.find('.new-output').exists()).toBe(false)
    term.setContent('p1', win(30))
    await settle()
    const btn = wrapper.get('.new-output')
    expect(btn.attributes('aria-label')).toBe('Jump to new output')

    await btn.trigger('click')
    await settle()
    expect(term.stickBottom).toBe(true)
    expect(wrapper.find('.new-output').exists()).toBe(false)
    expect(el.textContent).toContain('line 529')
    expect(el.scrollTop).toBe(el.scrollHeight - CLIENT_H)
  })

  it('search stays live; closing it while scrolled up holds what it showed', async () => {
    const { term, wrapper, el, win } = await scrolledUp()
    await wrapper.get('.desktop-only [aria-label="Search output"]').trigger('click')
    await settle()
    term.setContent('p1', win(30))
    await settle()
    expect(el.textContent).toContain('line 529') // live while searching
    expect(wrapper.find('.new-output').exists()).toBe(false)

    term.stickBottom = false // reading back inside the search results
    await settle()
    await wrapper.get('[aria-label="Close search"]').trigger('click')
    await settle()
    const shown = el.textContent
    term.setContent('p1', win(60))
    await settle()
    expect(el.textContent).toBe(shown) // held, not jumped
    expect(wrapper.find('.new-output').exists()).toBe(true)
    wrapper.unmount()
  })

  it('scrolling back down to the bottom catches up with the output', async () => {
    const { term, wrapper, el, win } = await scrolledUp()
    term.setContent('p1', win(30))
    await settle()
    expect(el.textContent).not.toContain('line 529') // still the frozen view
    el.scrollTop = el.scrollHeight
    el.dispatchEvent(new Event('scroll'))
    await settle()
    expect(term.stickBottom).toBe(true)
    expect(el.textContent).toContain('line 529')
    wrapper.unmount()
  })
})

// Bug: a scroll event that layout caused — the soft keyboard opening, the
// input box growing with a paste — was taken as the user scrolling up. The
// view unpinned at the bottom, stopped following output, and switched to the
// full render, which could land far up once the history above changed.
describe('TermView scroll events the user did not make', () => {
  it('keep the view pinned and at the bottom', async () => {
    useAgentsStore().setAgents([{ pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'agent' }])
    const term = useTerminalStore()
    term.activePane = 'p1'
    const wrapper = mount(TermView, { props: { paneId: 'p1' }, attachTo: document.body })
    const el = wrapper.get('.term-scroll').element as HTMLElement
    fakeLayout(el)
    term.setContent('p1', Array.from({ length: 500 }, (_, i) => `line ${i}`).join('\n'))
    await settle()
    const windowed = el.scrollHeight

    // layout leaves the view 150px above the bottom and the browser fires scroll
    el.scrollTop = el.scrollHeight - CLIENT_H - 150
    el.dispatchEvent(new Event('scroll'))
    await settle()
    expect(term.stickBottom).toBe(true)
    expect(el.scrollTop).toBe(el.scrollHeight - CLIENT_H)
    expect(el.scrollHeight).toBe(windowed) // still the tail window, no full re-render

    // the same position reached by the user does unpin
    el.dispatchEvent(new Event('touchstart'))
    el.scrollTop = el.scrollHeight - CLIENT_H - 150
    el.dispatchEvent(new Event('scroll'))
    await settle()
    expect(term.stickBottom).toBe(false)
    wrapper.unmount()
  })
})
