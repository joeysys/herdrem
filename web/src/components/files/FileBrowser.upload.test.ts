import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import FileBrowser from './FileBrowser.vue'
import { useFilesStore } from '../../stores/files'
import { useConnectionStore } from '../../stores/connection'
import { useUploadsStore } from '../../stores/uploads'
import { useTerminalStore } from '../../stores/terminal'
import type { UploadOutcome, UploadParams } from '../../utils/upload'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn(), onStatus: vi.fn(() => () => {}), onMessage: vi.fn(() => () => {}), connect: vi.fn(), disconnect: vi.fn() } }))

const calls: Array<{ p: UploadParams; resolve: (o: UploadOutcome) => void }> = []
vi.mock('../../utils/upload', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../utils/upload')>()
  return {
    ...actual,
    startUpload: (p: UploadParams) => {
      let resolve!: (o: UploadOutcome) => void
      const promise = new Promise<UploadOutcome>((r) => { resolve = r })
      calls.push({ p, resolve })
      return { promise, abort: () => resolve({ ok: false, status: 0, message: 'cancelled', aborted: true }) }
    },
  }
})

// jsdom has no CSS.escape (every browser does); FileBrowser uses it to scroll
// to a freshly uploaded file.
const g = globalThis as unknown as { CSS?: { escape?: (s: string) => string } }
if (!g.CSS?.escape) g.CSS = { ...(g.CSS ?? {}), escape: (s: string) => s.replace(/["\\]/g, '\\$&') }

let w: VueWrapper | null = null
const flush = async () => { await new Promise((r) => setTimeout(r, 0)); await nextTick() }

function open(features: string[] = ['file-upload']) {
  useConnectionStore().setHello('t', features)
  const files = useFilesStore()
  files.openBrowserAt('/w/sub', '/w', 'local')
  files.setListing('/w/sub', [{ name: 'old.txt', type: 'file', size: 1, mtime: 1 }])
  w = mount(FileBrowser, { props: { docked: false }, attachTo: document.body })
  return w
}

function pick(wrapper: VueWrapper, list: File[]) {
  const input = wrapper.get('input[type="file"]')
  Object.defineProperty(input.element, 'files', { value: list, configurable: true })
  return input.trigger('change')
}

const dt = (files: File[] = []) => ({ types: ['Files'], files, dropEffect: 'none' })

beforeEach(() => {
  calls.length = 0
  localStorage.clear()
  document.body.innerHTML = ''
  setActivePinia(createPinia())
})
afterEach(() => {
  w?.unmount()
  w = null
  vi.restoreAllMocks()
})

describe('FileBrowser upload', () => {
  it('has no upload button when the relay cannot take uploads', () => {
    const wrapper = open([])
    expect(wrapper.find('button[aria-label="Upload files"]').exists()).toBe(false)
  })

  it('opens the file picker from the upload button', async () => {
    const wrapper = open()
    const click = vi.spyOn(HTMLInputElement.prototype, 'click').mockImplementation(() => {})
    const btn = wrapper.get('button[aria-label="Upload files"]')
    expect(btn.attributes('disabled')).toBeUndefined()
    expect(btn.attributes('title')).toBe('Upload files to sub/')
    await btn.trigger('click')
    expect(click).toHaveBeenCalled()
  })

  it('works from another origin when the app holds the relay token', () => {
    const conn = useConnectionStore()
    conn.url = 'wss://elsewhere.example/ws'
    conn.token = 'tok'
    const wrapper = open()
    const btn = wrapper.get('button[aria-label="Upload files"]')
    expect(btn.attributes('disabled')).toBeUndefined()
  })

  it('is disabled, with the reason, from another origin without a token', () => {
    const conn = useConnectionStore()
    conn.url = 'wss://elsewhere.example/ws'
    conn.token = ''
    const wrapper = open()
    const btn = wrapper.get('button[aria-label="Upload files"]')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(btn.attributes('title')).toMatch(/needs the relay token/)
  })

  it('queues picked files for the folder being shown', async () => {
    const wrapper = open()
    await pick(wrapper, [new File(['hi'], 'a.txt'), new File(['yo'], 'b.txt')])
    expect(useUploadsStore().items.map((i) => [i.name, i.dir, i.host])).toEqual([
      ['a.txt', '/w/sub', 'local'],
      ['b.txt', '/w/sub', 'local'],
    ])
    expect(calls[0].p).toMatchObject({ name: 'a.txt', dir: '/w/sub' })
  })

  it('shows a drop target while files are dragged over the list, and uploads what is dropped', async () => {
    const wrapper = open()
    const list = wrapper.get('.fb-list')
    expect(wrapper.find('.fb-drop').exists()).toBe(false)
    await list.trigger('dragenter', { dataTransfer: dt() })
    expect(wrapper.get('.fb-drop').text()).toContain('sub/')
    const file = new File(['x'], 'dropped.png')
    await list.trigger('drop', { dataTransfer: dt([file]) })
    expect(wrapper.find('.fb-drop').exists()).toBe(false)
    expect(useUploadsStore().items.map((i) => i.name)).toEqual(['dropped.png'])
  })

  it('ignores drags that carry no files', async () => {
    const wrapper = open()
    await wrapper.get('.fb-list').trigger('dragenter', { dataTransfer: { types: ['text/plain'], files: [] } })
    expect(wrapper.find('.fb-drop').exists()).toBe(false)
  })

  it('hides the drop target when the drag leaves', async () => {
    const wrapper = open()
    const list = wrapper.get('.fb-list')
    await list.trigger('dragenter', { dataTransfer: dt() })
    await list.trigger('dragleave', { dataTransfer: dt() })
    expect(wrapper.find('.fb-drop').exists()).toBe(false)
  })

  it('shows progress, then offers to fill the uploaded path into the terminal', async () => {
    const wrapper = open()
    const fill = vi.spyOn(useTerminalStore(), 'fillPath')
    await pick(wrapper, [new File(['hello'], 'shot.png')])
    calls[0].p.onProgress!(2, 5)
    await nextTick()
    const bar = wrapper.get('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBe('40')
    calls[0].resolve({ ok: true, path: '/w/sub/shot.png', size: 5, replaced: false })
    await flush()
    expect(wrapper.get('.fu-item').classes()).toContain('done')
    await wrapper.get('button[aria-label="Fill shot.png path into terminal"]').trigger('click')
    expect(fill).toHaveBeenCalledWith('sub/shot.png')
  })

  it('asks what to do when the name is taken', async () => {
    const wrapper = open()
    await pick(wrapper, [new File(['n'], 'old.txt')])
    calls[0].resolve({ ok: false, status: 409, message: 'file already exists' })
    await flush()
    expect(wrapper.get('.fu-status').text()).toBe('already exists')
    const actions = wrapper.findAll('.fu-actions .fu-btn').map((b) => b.text())
    expect(actions).toEqual(['replace', 'keep both', 'skip'])
    await wrapper.findAll('.fu-actions .fu-btn')[1].trigger('click') // keep both
    expect(calls[1].p.name).toBe('old (1).txt')
  })

  it('can clear finished uploads', async () => {
    const wrapper = open()
    await pick(wrapper, [new File(['n'], 'x.txt')])
    calls[0].resolve({ ok: true, path: '/w/sub/x.txt', size: 1, replaced: false })
    await flush()
    await wrapper.get('.fu-clear').trigger('click')
    expect(wrapper.find('.fb-uploads').exists()).toBe(false)
  })
})
