import { describe, expect, it, vi } from 'vitest'

// The preview-width ref is a module-level singleton — resetModules + dynamic
// import gives each test a fresh module that re-reads localStorage.
describe('usePreviewWidth', () => {
  it('defaults to screen and persists choices to localStorage', async () => {
    localStorage.clear()
    vi.resetModules()
    const { usePreviewWidth } = await import('./usePreviewWidth')
    const { previewWidth, setPreviewWidth } = usePreviewWidth()

    expect(previewWidth.value).toBe('screen')

    setPreviewWidth('120')
    expect(previewWidth.value).toBe('120')
    expect(localStorage.getItem('herdrem_preview_width')).toBe('120')
  })

  it('restores a stored width on reload', async () => {
    localStorage.setItem('herdrem_preview_width', '80')
    vi.resetModules()
    const { usePreviewWidth } = await import('./usePreviewWidth')

    expect(usePreviewWidth().previewWidth.value).toBe('80')
  })

  it('falls back to screen for an unknown stored value', async () => {
    localStorage.setItem('herdrem_preview_width', '9999')
    vi.resetModules()
    const { usePreviewWidth } = await import('./usePreviewWidth')

    expect(usePreviewWidth().previewWidth.value).toBe('screen')
  })
})
