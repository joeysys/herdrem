import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { startUpload, uniqueName } from './upload'

// Minimal XMLHttpRequest double: records the request, lets the test resolve it.
class FakeXHR {
  static last: FakeXHR
  method = ''
  url = ''
  headers: Record<string, string> = {}
  body: unknown
  status = 0
  statusText = ''
  responseText = ''
  upload: { onprogress: ((e: { loaded: number; total: number; lengthComputable: boolean }) => void) | null } = { onprogress: null }
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  onabort: (() => void) | null = null
  aborted = false
  constructor() { FakeXHR.last = this }
  open(m: string, u: string) { this.method = m; this.url = u }
  setRequestHeader(k: string, v: string) { this.headers[k] = v }
  send(b: unknown) { this.body = b }
  abort() { this.aborted = true; this.onabort?.() }
  respond(status: number, text: string) { this.status = status; this.responseText = text; this.onload?.() }
}

const orig = globalThis.XMLHttpRequest
beforeEach(() => { (globalThis as unknown as { XMLHttpRequest: unknown }).XMLHttpRequest = FakeXHR })
afterEach(() => { globalThis.XMLHttpRequest = orig })

const file = (text: string, name = 'a.txt') => new File([text], name)

describe('startUpload', () => {
  it('POSTs the raw file to /upload with the destination in the query', () => {
    const f = file('hello')
    startUpload({ base: 'https://relay.example', host: 'box', dir: '/w/my dir', name: 'a b.txt', file: f })
    const x = FakeXHR.last
    expect(x.method).toBe('POST')
    const u = new URL(x.url)
    expect(u.origin + u.pathname).toBe('https://relay.example/upload')
    expect(Object.fromEntries(u.searchParams)).toEqual({ host: 'box', dir: '/w/my dir', name: 'a b.txt', size: '5' })
    expect(x.body).toBe(f)
  })

  it('always sends the upload marker header, and the token only when there is one', () => {
    startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x') })
    expect(FakeXHR.last.headers).toEqual({ 'X-Herdrem-Upload': '1', 'Content-Type': 'application/octet-stream' })
    startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x'), token: 'tok', overwrite: true })
    expect(FakeXHR.last.headers.Authorization).toBe('Bearer tok')
    expect(new URL(FakeXHR.last.url).searchParams.get('overwrite')).toBe('1')
  })

  it('never puts the token in the URL', () => {
    startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x'), token: 'secret-token' })
    expect(FakeXHR.last.url).not.toContain('secret-token')
  })

  it('reports progress', () => {
    const seen: Array<[number, number]> = []
    startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('12345'), onProgress: (l, t) => seen.push([l, t]) })
    FakeXHR.last.upload.onprogress!({ loaded: 2, total: 5, lengthComputable: true })
    FakeXHR.last.upload.onprogress!({ loaded: 4, total: 0, lengthComputable: false })
    expect(seen).toEqual([[2, 5], [4, 5]])
  })

  it('resolves success, HTTP errors, network errors and aborts', async () => {
    const ok = startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x') })
    FakeXHR.last.respond(200, '{"path":"/w/a","size":1,"replaced":true}')
    expect(await ok.promise).toEqual({ ok: true, path: '/w/a', size: 1, replaced: true })

    const conflict = startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x') })
    FakeXHR.last.respond(409, 'file already exists\n')
    expect(await conflict.promise).toEqual({ ok: false, status: 409, message: 'file already exists' })

    const net = startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x') })
    FakeXHR.last.onerror!()
    expect(await net.promise).toMatchObject({ ok: false, status: 0, message: 'network error' })

    const cancelled = startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x') })
    cancelled.abort()
    expect(FakeXHR.last.aborted).toBe(true)
    expect(await cancelled.promise).toMatchObject({ ok: false, aborted: true })
  })

  it('treats an unparsable 200 as a failure', async () => {
    const h = startUpload({ base: 'https://r', host: 'local', dir: '/w', name: 'a', file: file('x') })
    FakeXHR.last.respond(200, 'not json')
    expect(await h.promise).toMatchObject({ ok: false, message: 'unexpected response' })
  })
})

describe('uniqueName', () => {
  it('keeps a free name and numbers a taken one before the extension', () => {
    expect(uniqueName('a.txt', new Set())).toBe('a.txt')
    expect(uniqueName('a.txt', new Set(['a.txt']))).toBe('a (1).txt')
    expect(uniqueName('a.txt', new Set(['a.txt', 'a (1).txt']))).toBe('a (2).txt')
  })

  it('uses the last extension only and handles dotfiles / no extension', () => {
    expect(uniqueName('backup.tar.gz', new Set(['backup.tar.gz']))).toBe('backup.tar (1).gz')
    expect(uniqueName('.env', new Set(['.env']))).toBe('.env (1)')
    expect(uniqueName('Makefile', new Set(['Makefile']))).toBe('Makefile (1)')
    expect(uniqueName('trailing.', new Set(['trailing.']))).toBe('trailing. (1)')
  })
})
