import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import FilePreview from './FilePreview.vue'
import { useFilesStore } from '../../stores/files'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn() } }))

let wrapper: VueWrapper | null = null

function open(path: string, content: string, extra: Record<string, unknown> = {}) {
  const files = useFilesStore()
  files.openPath(path, '/workspace', 'local', null)
  files.setContent({
    type: 'file_content',
    path,
    content,
    encoding: 'utf-8',
    mime_type: 'text/plain',
    is_text: true,
    is_image: false,
    size: content.length,
    ...extra,
  } as never)
  wrapper = mount(FilePreview, { props: { directoryOpen: false }, attachTo: document.body })
  return wrapper
}

async function flush() {
  await nextTick()
  await nextTick()
}

async function search(w: VueWrapper, q: string) {
  await w.get('button[aria-label="Search file"]').trigger('click')
  await flush()
  await w.get('input[aria-label="Search file"]').setValue(q)
  await vi.advanceTimersByTimeAsync(150)
  await flush()
}

const marks = (w: VueWrapper) => w.findAll('mark.term-hit')

beforeEach(() => {
  vi.useFakeTimers()
  setActivePinia(createPinia())
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  vi.useRealTimers()
})

describe('FilePreview find', () => {
  it('offers search for text files only', async () => {
    const text = open('/workspace/a.ts', 'const a = 1')
    expect(text.find('button[aria-label="Search file"]').exists()).toBe(true)
    text.unmount()

    const img = open('/workspace/p.png', '', { is_image: true, mime_type: 'image/png', is_text: false })
    expect(img.find('button[aria-label="Search file"]').exists()).toBe(false)
    img.unmount()

    useFilesStore().openPath('/workspace/loading.ts', '/workspace', 'local', null) // still reading
    wrapper = mount(FilePreview, { props: { directoryOpen: false } })
    expect(wrapper.find('button[aria-label="Search file"]').exists()).toBe(false)
  })

  it('highlights matches in the source, counts them and starts at the first', async () => {
    const w = open('/workspace/a.ts', 'let foo = 1\nlet bar = 2\nfoo(bar)\n')
    await search(w, 'foo')
    expect(marks(w)).toHaveLength(2)
    expect(w.get('.find-bar .count').text()).toBe('1/2')
    expect(marks(w)[0].classes()).toContain('active')
  })

  it('never matches the line-number gutter', async () => {
    const w = open('/workspace/a.txt', 'alpha\nbeta\ngamma\n')
    await search(w, '2')
    expect(marks(w)).toHaveLength(0)
    expect(w.get('.find-bar .count').text()).toBe('no match')
  })

  it('finds text that syntax highlighting splits across tokens', async () => {
    const w = open('/workspace/a.ts', 'const answer = 42;\n')
    await search(w, 'answer = 42')
    expect(w.get('.find-bar .count').text()).toBe('1/1')
    expect(marks(w).map((m) => m.element.textContent).join('')).toBe('answer = 42')
  })

  it('Enter / Shift+Enter step through hits and wrap', async () => {
    const w = open('/workspace/a.txt', 'x\nx\nx\n')
    await search(w, 'x')
    const input = w.get('input[aria-label="Search file"]')
    expect(w.get('.count').text()).toBe('1/3')
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.get('.count').text()).toBe('2/3')
    await input.trigger('keydown', { key: 'Enter', shiftKey: true })
    await input.trigger('keydown', { key: 'Enter', shiftKey: true })
    expect(w.get('.count').text()).toBe('3/3')
    expect(marks(w).map((m) => m.classes().includes('active'))).toEqual([false, false, true])
  })

  it('uses smart case', async () => {
    const w = open('/workspace/a.txt', 'Error error ERROR\n')
    await search(w, 'error')
    expect(marks(w)).toHaveLength(3)
    await w.get('input[aria-label="Search file"]').setValue('Error')
    await vi.advanceTimersByTimeAsync(150)
    await flush()
    expect(marks(w)).toHaveLength(1)
  })

  it('searches rendered markdown and re-highlights when switching to source', async () => {
    const w = open('/workspace/doc.md', '# Title\n\nsome *emphasised* words and emphasised ones\n')
    await search(w, 'emphasised')
    expect(w.find('.fp-markdown').exists()).toBe(true)
    expect(w.findAll('.fp-markdown mark.term-hit')).toHaveLength(2)
    await w.get('.fp-btn.mode').trigger('click') // → source view
    await flush()
    expect(w.find('.fp-markdown').exists()).toBe(false)
    expect(w.findAll('.fp-code mark.term-hit')).toHaveLength(2)
  })

  it('switches a rendered HTML preview to source to search it, and restores it on close', async () => {
    const w = open('/workspace/page.html', '<h1>Hello</h1>\n<p>hello again</p>\n')
    const modeBtn = () => w.get('.fp-btn.mode')
    expect(modeBtn().text()).toBe('source') // currently rendered; the button offers the source
    await search(w, 'hello')
    expect(modeBtn().text()).toBe('preview') // now showing source
    expect(marks(w)).toHaveLength(2)
    await w.get('button[aria-label="Close search"]').trigger('click')
    await flush()
    expect(modeBtn().text()).toBe('source') // rendered again
    expect(marks(w)).toHaveLength(0)
  })

  it('does not restore the HTML preview when the user chose source themselves', async () => {
    const w = open('/workspace/page.html', '<p>hello</p>\n')
    await w.get('.fp-btn.mode').trigger('click') // user → source
    await search(w, 'hello')
    await w.get('button[aria-label="Close search"]').trigger('click')
    await flush()
    expect(w.get('.fp-btn.mode').text()).toBe('preview') // still source
  })

  it('closes the search if the user switches back to the rendered HTML', async () => {
    const w = open('/workspace/page.html', '<p>hello</p>\n')
    await search(w, 'hello')
    await w.get('.fp-btn.mode').trigger('click') // → rendered
    await flush()
    expect(w.find('.find-bar').exists()).toBe(false)
  })

  it('warns that only the loaded part is searched when the file was truncated', async () => {
    const w = open('/workspace/big.log', 'line\n'.repeat(5), { truncated: true })
    await w.get('button[aria-label="Search file"]').trigger('click')
    await flush()
    expect(w.get('.find-bar .note').attributes('title')).toMatch(/truncated/i)
  })

  it('Escape closes and removes every highlight', async () => {
    const w = open('/workspace/a.txt', 'find me\n')
    await search(w, 'find')
    await w.get('input[aria-label="Search file"]').trigger('keydown', { key: 'Escape' })
    await flush()
    expect(w.find('.find-bar').exists()).toBe(false)
    expect(marks(w)).toHaveLength(0)
    expect(w.get('.fp-code').text()).toContain('find me')
  })

  it('opening a different file starts a fresh search', async () => {
    const w = open('/workspace/a.txt', 'find me\n')
    await search(w, 'find')
    const files = useFilesStore()
    files.openPath('/workspace/b.txt', '/workspace', 'local', null)
    files.setContent({ type: 'file_content', path: '/workspace/b.txt', content: 'find you\n', encoding: 'utf-8', mime_type: 'text/plain', is_text: true, is_image: false, size: 9 } as never)
    await flush()
    expect(w.find('.find-bar').exists()).toBe(false)
    expect(marks(w)).toHaveLength(0)
  })

  it('Ctrl+F opens the find bar instead of the browser find', async () => {
    const w = open('/workspace/a.txt', 'hello\n')
    const ev = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(ev)
    await flush()
    expect(ev.defaultPrevented).toBe(true)
    expect(w.find('.find-bar').exists()).toBe(true)
  })

  it('leaves Ctrl+F to the browser for images and while another panel has focus', async () => {
    const img = open('/workspace/p.png', '', { is_image: true, mime_type: 'image/png', is_text: false })
    const ev = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
    expect(img.find('.find-bar').exists()).toBe(false)
    img.unmount()

    const w = open('/workspace/a.txt', 'hello\n')
    const other = document.createElement('input')
    document.body.appendChild(other)
    other.focus()
    const ev2 = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(ev2)
    expect(ev2.defaultPrevented).toBe(false)
    expect(w.find('.find-bar').exists()).toBe(false)
    other.remove()
  })

  it('removes its document listener on unmount', async () => {
    const w = open('/workspace/a.txt', 'hello\n')
    w.unmount()
    wrapper = null
    const ev = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
  })
})
