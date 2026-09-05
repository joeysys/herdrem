import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import FileBrowser from './FileBrowser.vue'
import { useFilesStore } from '../../stores/files'
import { useConnectionStore } from '../../stores/connection'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn() } }))

describe('FileBrowser actions', () => {
  beforeEach(() => {
    localStorage.clear()
    document.body.innerHTML = ''
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('stars an entry from the more menu and sorts it first', async () => {
    const files = useFilesStore()
    files.openBrowserAt('/workspace', '/workspace', 'test-star-host')
    files.setListing('/workspace', [
      { name: 'a.txt', type: 'file', size: 1, mtime: 1 },
      { name: 'b.txt', type: 'file', size: 1, mtime: 2 },
    ])
    const wrapper = mount(FileBrowser, {
      props: { docked: false },
      attachTo: document.body,
    })

    expect(wrapper.find('.crumb-fill').text()).toBe('↙')
    await wrapper.find('[data-name="a.txt"] .fe-more-btn').trigger('click')
    const actions = document.querySelectorAll<HTMLButtonElement>('.fe-menu-item')
    expect([...actions].map((button) => button.textContent?.trim())).toEqual([
      '↗preview',
      '↙fill Terminal',
      '★star',
      '⧉copy path',
      '⤓download',
    ])
    actions[2].click()
    await nextTick()

    expect(wrapper.findAll('.fb-entry.file')[0].attributes('data-name')).toBe('a.txt')
    expect(wrapper.find('[data-name="a.txt"] .fe-star').exists()).toBe(true)
    expect(localStorage.getItem('herdrem_file_stars')).toContain('test-star-host:/workspace/a.txt')

    wrapper.unmount()
  })

  it('shows download option for files and triggers download with correct URL', async () => {
    const files = useFilesStore()
    const conn = useConnectionStore()
    conn.url = 'ws://127.0.0.1:8766/ws'
    conn.token = 'secret-token'

    files.openBrowserAt('/workspace/src', '/workspace', 'remote-host')
    files.setListing('/workspace/src', [
      { name: 'main.go', type: 'file', size: 100, mtime: 1 },
    ])
    const wrapper = mount(FileBrowser, {
      props: { docked: false },
      attachTo: document.body,
    })

    await wrapper.find('[data-name="main.go"] .fe-more-btn').trigger('click')
    const actions = document.querySelectorAll<HTMLButtonElement>('.fe-menu-item')
    const downloadBtn = [...actions].find((btn) => btn.textContent?.includes('download'))
    expect(downloadBtn).toBeDefined()

    // Mock document.createElement('a') click
    let clickedHref = ''
    let clickedDownload = ''
    const origCreateElement = document.createElement.bind(document)
    vi.spyOn(document, 'createElement').mockImplementation((tagName: string, options?: ElementCreationOptions) => {
      const el = origCreateElement(tagName, options)
      if (tagName.toLowerCase() === 'a') {
        el.click = () => {
          clickedHref = (el as HTMLAnchorElement).href
          clickedDownload = (el as HTMLAnchorElement).download
        }
      }
      return el
    })

    downloadBtn?.click()
    await nextTick()

    expect(clickedDownload).toBe('main.go')
    expect(clickedHref).toContain('/file?path=%2Fworkspace%2Fsrc%2Fmain.go')
    expect(clickedHref).toContain('host=remote-host')
    expect(clickedHref).toContain('token=secret-token')
    expect(clickedHref).toContain('download=1')

    // Menu should be closed after clicking download
    expect(document.querySelector('.fe-menu')).toBeNull()

    wrapper.unmount()
  })

  it('does not show download option for directories', async () => {
    const files = useFilesStore()
    files.openBrowserAt('/workspace', '/workspace', 'local')
    files.setListing('/workspace', [
      { name: 'subdir', type: 'dir', size: 0, mtime: 1 },
    ])
    const wrapper = mount(FileBrowser, {
      props: { docked: false },
      attachTo: document.body,
    })

    await wrapper.find('[data-name="subdir"] .fe-more-btn').trigger('click')
    const actions = document.querySelectorAll<HTMLButtonElement>('.fe-menu-item')
    expect([...actions].map((button) => button.textContent?.trim())).toEqual([
      '📁open folder',
      '↙fill Terminal',
      '★star',
      '⧉copy path',
    ])

    wrapper.unmount()
  })
})
