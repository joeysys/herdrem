import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import FileBrowser from './FileBrowser.vue'
import { useFilesStore } from '../../stores/files'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn() } }))

describe('FileBrowser actions', () => {
  beforeEach(() => {
    localStorage.clear()
    document.body.innerHTML = ''
    setActivePinia(createPinia())
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
    ])
    actions[2].click()
    await nextTick()

    expect(wrapper.findAll('.fb-entry.file')[0].attributes('data-name')).toBe('a.txt')
    expect(wrapper.find('[data-name="a.txt"] .fe-star').exists()).toBe(true)
    expect(localStorage.getItem('herdrem_file_stars')).toContain('test-star-host:/workspace/a.txt')

    wrapper.unmount()
  })
})
