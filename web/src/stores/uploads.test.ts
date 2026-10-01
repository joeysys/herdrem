import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useUploadsStore } from './uploads'
import { useConnectionStore } from './connection'
import { useFilesStore } from './files'
import type { UploadOutcome, UploadParams } from '../utils/upload'

vi.mock('../api/ws', () => ({ ws: { send: vi.fn(() => true), onStatus: vi.fn(() => () => {}), onMessage: vi.fn(() => () => {}), connect: vi.fn(), disconnect: vi.fn() } }))

// Each startUpload call is recorded with a resolver the test controls.
const calls: Array<{ p: UploadParams; resolve: (o: UploadOutcome) => void; abort: ReturnType<typeof vi.fn> }> = []
vi.mock('../utils/upload', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../utils/upload')>()
  return {
    ...actual,
    startUpload: (p: UploadParams) => {
      let resolve!: (o: UploadOutcome) => void
      const promise = new Promise<UploadOutcome>((r) => { resolve = r })
      const abort = vi.fn(() => resolve({ ok: false, status: 0, message: 'cancelled', aborted: true }))
      calls.push({ p, resolve, abort })
      return { promise, abort }
    },
  }
})

const flush = () => new Promise((r) => setTimeout(r, 0))
const f = (name: string, size = 3) => new File(['x'.repeat(size)], name)
const T = { host: 'local', dir: '/w', root: '/w' }

function ready() {
  const conn = useConnectionStore()
  conn.setHello('t', ['file-upload'])
  return { conn, up: useUploadsStore(), files: useFilesStore() }
}

beforeEach(() => {
  calls.length = 0
  setActivePinia(createPinia())
})

describe('uploads store', () => {
  it('can upload only when the relay offers it and the page is on the relay origin', () => {
    const conn = useConnectionStore()
    const up = useUploadsStore()
    expect(up.canUpload).toBe(false) // no feature yet
    conn.setHello('t', ['file-upload'])
    expect(up.canUpload).toBe(true) // jsdom page origin == default relay origin
    conn.url = 'wss://elsewhere.example/ws'
    conn.token = ''
    expect(up.supported).toBe(true)
    expect(up.sameOrigin).toBe(false)
    expect(up.canUpload).toBe(false) // another origin and no token: nothing to authenticate with
    conn.token = 'tok'
    expect(up.canUpload).toBe(true) // another origin with the token: Bearer upload
  })

  it('sends the token from another origin', () => {
    const { conn, up } = ready()
    conn.url = 'wss://elsewhere.example/ws'
    conn.token = 'tok'
    up.add([f('a')], T)
    expect(calls[0].p).toMatchObject({ base: 'https://elsewhere.example', token: 'tok' })
  })

  it('uploads one file at a time, in order', async () => {
    const { up } = ready()
    up.add([f('a'), f('b')], T)
    expect(calls.map((c) => c.p.name)).toEqual(['a'])
    expect(up.items.map((i) => i.status)).toEqual(['uploading', 'queued'])
    calls[0].resolve({ ok: true, path: '/w/a', size: 3, replaced: false })
    await flush()
    expect(calls.map((c) => c.p.name)).toEqual(['a', 'b'])
    expect(up.items[0]).toMatchObject({ status: 'done', path: '/w/a' })
    expect(up.active).toBe(1)
  })

  it('passes the destination, the token and progress through', async () => {
    const { conn, up } = ready()
    conn.token = 'tok'
    up.add([f('a', 10)], { host: 'box', dir: '/w/sub', root: '/w' })
    expect(calls[0].p).toMatchObject({ host: 'box', dir: '/w/sub', name: 'a', token: 'tok', overwrite: false })
    calls[0].p.onProgress!(4, 10)
    expect(up.items[0]).toMatchObject({ loaded: 4, total: 10 })
  })

  it('refreshes the listing and highlights the file when the browser shows that folder', async () => {
    const { up, files } = ready()
    files.openBrowserAt('/w', '/w', 'local')
    const nav = vi.spyOn(files, 'navigate')
    up.add([f('pic.png')], T)
    calls[0].resolve({ ok: true, path: '/w/pic.png', size: 3, replaced: false })
    await flush()
    expect(nav).toHaveBeenCalledWith('/w')
    expect(files.scrollToFile).toBe('pic.png')
  })

  it('leaves the listing alone when the browser shows another folder', async () => {
    const { up, files } = ready()
    files.openBrowserAt('/other', '/other', 'local')
    const nav = vi.spyOn(files, 'navigate')
    up.add([f('a')], T)
    calls[0].resolve({ ok: true, path: '/w/a', size: 3, replaced: false })
    await flush()
    expect(nav).not.toHaveBeenCalled()
  })

  it('stops on a conflict until the user decides; replace re-sends with overwrite', async () => {
    const { up } = ready()
    up.add([f('a'), f('b')], T)
    calls[0].resolve({ ok: false, status: 409, message: 'file already exists' })
    await flush()
    expect(up.items[0].status).toBe('conflict')
    // the queue moves on to the next file meanwhile
    expect(calls.map((c) => c.p.name)).toEqual(['a', 'b'])
    calls[1].resolve({ ok: true, path: '/w/b', size: 3, replaced: false })
    await flush()
    up.replace(up.items[0].id)
    expect(calls[2].p).toMatchObject({ name: 'a', overwrite: true })
    calls[2].resolve({ ok: true, path: '/w/a', size: 3, replaced: true })
    await flush()
    expect(up.items[0]).toMatchObject({ status: 'done', replaced: true })
  })

  it('keep both picks the next free name, avoiding listed files and other uploads', async () => {
    const { up, files } = ready()
    files.openBrowserAt('/w', '/w', 'local')
    files.setListing('/w', [
      { name: 'r.pdf', type: 'file', size: 1, mtime: 1 },
      { name: 'r (1).pdf', type: 'file', size: 1, mtime: 1 },
    ])
    up.add([f('r.pdf')], T)
    calls[0].resolve({ ok: false, status: 409, message: '' })
    await flush()
    up.keepBoth(up.items[0].id)
    expect(up.items[0].name).toBe('r (2).pdf')
    expect(calls[1].p).toMatchObject({ name: 'r (2).pdf', overwrite: false })
  })

  it('cancel aborts the running upload, or drops a queued / conflicting one', async () => {
    const { up } = ready()
    up.add([f('a'), f('b')], T)
    up.cancel(up.items[1].id) // queued
    expect(up.items[1].status).toBe('cancelled')
    up.cancel(up.items[0].id) // uploading
    expect(calls[0].abort).toHaveBeenCalled()
    await flush()
    expect(up.items[0].status).toBe('cancelled')
    expect(calls).toHaveLength(1) // the cancelled queued file was never sent
  })

  it('rejects an oversized file without sending it', () => {
    const { up } = ready()
    const big = f('big.iso')
    Object.defineProperty(big, 'size', { value: 100 * 1024 * 1024 + 1 })
    up.add([big], T)
    expect(calls).toHaveLength(0)
    expect(up.items[0]).toMatchObject({ status: 'error', error: 'too large (max 100 MB)' })
    up.retry(up.items[0].id)
    expect(calls).toHaveLength(0)
  })

  it('explains failures and can retry them', async () => {
    const { up } = ready()
    up.add([f('a')], T)
    calls[0].resolve({ ok: false, status: 404, message: 'destination directory not found' })
    await flush()
    expect(up.items[0]).toMatchObject({ status: 'error', error: 'folder no longer exists' })
    up.retry(up.items[0].id)
    expect(calls).toHaveLength(2)
    calls[1].resolve({ ok: false, status: 0, message: 'network error' })
    await flush()
    expect(up.items[0].error).toBe('network error')
  })

  it('clear removes finished rows; dismiss removes a failed one; neither touches live uploads', async () => {
    const { up } = ready()
    up.add([f('done'), f('bad'), f('live')], T)
    calls[0].resolve({ ok: true, path: '/w/done', size: 3, replaced: false })
    await flush()
    calls[1].resolve({ ok: false, status: 403, message: '' })
    await flush()
    up.clearFinished()
    expect(up.items.map((i) => i.name)).toEqual(['bad', 'live'])
    up.dismiss(up.items[1].id) // still uploading: ignored
    up.dismiss(up.items[0].id)
    expect(up.items.map((i) => i.name)).toEqual(['live'])
  })
})
