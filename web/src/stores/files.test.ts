import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useFilesStore } from './files'

vi.mock('../api/ws', () => ({
  ws: { send: vi.fn() },
}))

describe('useFilesStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  describe('closePreview', () => {
    it('clears preview state', () => {
      const files = useFilesStore()
      files.openPath('/some/dir/file.txt', '/some/dir', 'local', null)
      expect(files.preview).not.toBeNull()
      expect(files.previewMax).toBe(true)

      files.closePreview()
      expect(files.preview).toBeNull()
      expect(files.previewMax).toBe(false)
    })

    it('preserves browserOpen state', () => {
      const files = useFilesStore()
      files.openPath('/some/dir/file.txt', '/some/dir', 'local', null)
      expect(files.browserOpen).toBe(true)

      files.closePreview()
      expect(files.browserOpen).toBe(true)
    })

    it('is safe to call when preview is already null', () => {
      const files = useFilesStore()
      expect(files.preview).toBeNull()
      files.closePreview()
      expect(files.preview).toBeNull()
      expect(files.previewMax).toBe(false)
    })
  })

  describe('closeBrowser and openBrowser state preservation', () => {
    it('closes browser and preserves preview and path state', () => {
      const files = useFilesStore()
      files.openPath('/some/dir/sub/file.txt', '/some/dir', 'local', null)
      expect(files.browserOpen).toBe(true)
      expect(files.path).toBe('/some/dir/sub')
      expect(files.preview?.path).toBe('/some/dir/sub/file.txt')

      files.closeBrowser()
      expect(files.browserOpen).toBe(false)
      expect(files.path).toBe('/some/dir/sub')
      expect(files.preview?.path).toBe('/some/dir/sub/file.txt')
    })

    it('restores previous directory path and preview when re-opening in same root', () => {
      const files = useFilesStore()
      files.openPath('/some/dir/sub/file.txt', '/some/dir', 'local', null)
      files.closeBrowser()

      files.openBrowser('/some/dir', 'local')
      expect(files.browserOpen).toBe(true)
      expect(files.path).toBe('/some/dir/sub')
      expect(files.preview?.path).toBe('/some/dir/sub/file.txt')
    })

    it('resets path and preview when re-opening in a different root', () => {
      const files = useFilesStore()
      files.openPath('/some/dir/sub/file.txt', '/some/dir', 'local', null)
      files.closeBrowser()

      files.openBrowser('/other/dir', 'local')
      expect(files.browserOpen).toBe(true)
      expect(files.path).toBe('/other/dir')
      expect(files.preview).toBeNull()
    })
  })
})
