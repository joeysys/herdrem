import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useFilesStore } from './files'
import { ws } from '../api/ws'

vi.mock('../api/ws', () => ({ ws: { send: vi.fn() } }))

describe('useFilesStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('opens a terminal path in preview without opening or moving the directory', () => {
    const files = useFilesStore()
    files.openBrowserAt('/workspace/src', '/workspace', 'remote')

    files.openPath('/other/readme.md', '/other', 'local', 12)

    expect(files.browserOpen).toBe(true)
    expect(files.path).toBe('/workspace/src')
    expect(files.host).toBe('remote')
    expect(files.preview).toMatchObject({
      path: '/other/readme.md',
      root: '/other',
      host: 'local',
      targetLine: 12,
    })
    const sent = vi.mocked(ws.send).mock.lastCall?.[0] as { type: string; path: string; host: string; req_id: string }
    expect(sent).toMatchObject({ type: 'file_read', path: '/other/readme.md', host: 'local' })
    expect(sent.req_id).toBeTruthy()
    expect(files.preview?.reqId).toBe(sent.req_id)
  })

  it('opens a file from the directory with that directory context', () => {
    const files = useFilesStore()
    files.openBrowserAt('/workspace/src', '/workspace', 'remote')

    files.openFile('/workspace/src/App.vue')

    expect(files.preview).toMatchObject({
      path: '/workspace/src/App.vue',
      root: '/workspace',
      host: 'remote',
    })
    expect(files.path).toBe('/workspace/src')
  })

  it('closes directory and preview independently', () => {
    const files = useFilesStore()
    files.openBrowser('/workspace', 'local')
    files.openPath('/workspace/a.txt', '/workspace', 'local', null)

    files.closePreview()
    expect(files.preview).toBeNull()
    expect(files.browserOpen).toBe(true)

    files.openPath('/workspace/a.txt', '/workspace', 'local', null)
    files.closeBrowser()
    expect(files.browserOpen).toBe(false)
    expect(files.preview?.path).toBe('/workspace/a.txt')
  })

  it('switches the directory between docked and floating modes', () => {
    const files = useFilesStore()
    expect(files.browserDocked).toBe(true)
    files.toggleDocked()
    expect(files.browserDocked).toBe(false)
  })

  it('setListError only clears directory loading, not the preview', () => {
    const files = useFilesStore()
    files.openBrowserAt('/workspace', '/workspace', 'local')
    files.openPath('/workspace/a.txt', '/workspace', 'local', null)
    expect(files.loading).toBe(true)
    expect(files.preview).not.toBeNull()

    files.setListError()

    expect(files.loading).toBe(false)
    expect(files.preview).not.toBeNull()
    expect(files.preview?.error).toBeUndefined()
  })

  it('setPreviewError only marks the preview as errored, not the directory', () => {
    const files = useFilesStore()
    files.openBrowserAt('/workspace', '/workspace', 'local')
    files.openPath('/workspace/a.txt', '/workspace', 'local', null)
    expect(files.loading).toBe(true)

    files.setPreviewError()

    expect(files.preview?.error).toBe('file not found or unreadable')
    // directory loading state is untouched by a preview error
    expect(files.loading).toBe(true)
  })

  it('rejects a stale file_content response with a mismatched req_id', () => {
    const files = useFilesStore()
    files.openPath('/workspace/a.txt', '/workspace', 'local', null)
    const originalReqId = files.preview!.reqId

    // Simulate a second read of the same path (new req_id)
    files.openPath('/workspace/a.txt', '/workspace', 'local', null)
    expect(files.preview!.reqId).not.toBe(originalReqId)

    // Stale response arrives with the old req_id — must be ignored
    files.setContent({
      type: 'file_content',
      path: '/workspace/a.txt',
      content: 'stale',
      req_id: originalReqId,
    })
    expect(files.preview?.content).toBe('')

    // Current response arrives — accepted
    files.setContent({
      type: 'file_content',
      path: '/workspace/a.txt',
      content: 'fresh',
      req_id: files.preview!.reqId,
    })
    expect(files.preview?.content).toBe('fresh')
  })

  it('rejects a stale file_listing response with a mismatched req_id', () => {
    const files = useFilesStore()
    files.openBrowserAt('/workspace', '/workspace', 'local')
    const firstReqId = files.listReqId

    // Navigate to the same path again — new req_id
    files.navigate('/workspace')
    expect(files.listReqId).not.toBe(firstReqId)

    // Stale listing arrives — must be ignored
    files.setListing('/workspace', [{ name: 'stale.txt', type: 'file', size: 0 }], firstReqId)
    expect(files.entries).toHaveLength(0)

    // Current listing arrives — accepted
    files.setListing('/workspace', [{ name: 'fresh.txt', type: 'file', size: 0 }], files.listReqId)
    expect(files.entries[0]?.name).toBe('fresh.txt')
  })
})
