import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import FilePreview from './FilePreview.vue'
import { useFilesStore } from '../../stores/files'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn() } }))

describe('FilePreview copy button', () => {
  const originalClipboard = navigator.clipboard

  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    Object.defineProperty(navigator, 'clipboard', {
      value: originalClipboard,
      configurable: true,
      writable: true,
    })
  })

  it('renders copy button for text files and copies content via clipboard API', async () => {
    const files = useFilesStore()
    files.openPath('/workspace/hello.ts', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/hello.ts',
      content: 'console.log("hello world");',
      encoding: 'utf-8',
      mime_type: 'text/plain',
      is_text: true,
      is_image: false,
      size: 27,
    })

    const writeTextMock = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: writeTextMock },
      configurable: true,
      writable: true,
    })

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    const copyBtn = wrapper.find('.copy-btn')
    expect(copyBtn.exists()).toBe(true)
    expect(copyBtn.text()).toBe('⧉ copy')
    expect(copyBtn.attributes('title')).toBe('Copy file content')

    await copyBtn.trigger('click')
    await nextTick()

    expect(writeTextMock).toHaveBeenCalledWith('console.log("hello world");')
    expect(wrapper.find('.copy-btn').text()).toBe('✓ copied')
    expect(wrapper.find('.copy-btn').classes()).toContain('copied')
    expect(wrapper.find('.copy-btn').attributes('title')).toBe('Copied to clipboard')

    // After 2s timeout, it reverts to idle
    vi.advanceTimersByTime(2000)
    await nextTick()
    expect(wrapper.find('.copy-btn').text()).toBe('⧉ copy')
    expect(wrapper.find('.copy-btn').classes()).not.toContain('copied')

    wrapper.unmount()
  })

  it('does not render copy button for binary image files', async () => {
    const files = useFilesStore()
    files.openPath('/workspace/photo.png', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/photo.png',
      content: 'base64data...',
      encoding: 'base64',
      mime_type: 'image/png',
      is_text: false,
      is_image: true,
      size: 100,
    })

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    expect(wrapper.find('.copy-btn').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders copy button for SVG files', async () => {
    const files = useFilesStore()
    files.openPath('/workspace/icon.svg', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/icon.svg',
      content: '<svg><circle r="10"/></svg>',
      encoding: 'utf-8',
      mime_type: 'image/svg+xml',
      is_text: true,
      is_image: true,
      size: 30,
    })

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    expect(wrapper.find('.copy-btn').exists()).toBe(true)
    wrapper.unmount()
  })

  it('disables copy button when preview has an error', async () => {
    const files = useFilesStore()
    files.openPath('/workspace/missing.txt', '/workspace', 'local', null)
    files.setPreviewError()

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    const copyBtn = wrapper.find('.copy-btn')
    expect(copyBtn.exists()).toBe(true)
    expect(copyBtn.attributes('disabled')).toBeDefined()
    expect(copyBtn.attributes('title')).toBe('File content unavailable')

    wrapper.unmount()
  })

  it('disables copy button while content is loading', () => {
    const files = useFilesStore()
    files.openPath('/workspace/loading.txt', '/workspace', 'local', null)

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    const copyBtn = wrapper.find('.copy-btn')
    expect(copyBtn.attributes('disabled')).toBeDefined()
    expect(copyBtn.attributes('title')).toBe('Reading file content')
    wrapper.unmount()
  })

  it('disables copy button for truncated content', () => {
    const files = useFilesStore()
    files.openPath('/workspace/large.log', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/large.log',
      content: 'partial',
      encoding: 'utf-8',
      mime_type: 'text/plain',
      is_text: true,
      is_image: false,
      truncated: true,
      size: 1024 * 1024,
    })

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    const copyBtn = wrapper.find('.copy-btn')
    expect(copyBtn.attributes('disabled')).toBeDefined()
    expect(copyBtn.attributes('title')).toBe('File content truncated')
    wrapper.unmount()
  })

  it('falls back to document.execCommand when clipboard API is unavailable', async () => {
    const files = useFilesStore()
    files.openPath('/workspace/doc.md', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/doc.md',
      content: '# Title',
      encoding: 'utf-8',
      mime_type: 'text/markdown',
      is_text: true,
      is_image: false,
      size: 7,
    })

    // Simulate context where navigator.clipboard is undefined
    Object.defineProperty(navigator, 'clipboard', {
      value: undefined,
      configurable: true,
      writable: true,
    })

    const execCommandMock = vi.fn().mockReturnValue(true)
    document.execCommand = execCommandMock

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    const copyBtn = wrapper.find('.copy-btn')
    await copyBtn.trigger('click')
    await nextTick()

    expect(execCommandMock).toHaveBeenCalledWith('copy')
    expect(wrapper.find('.copy-btn').text()).toBe('✓ copied')

    wrapper.unmount()
  })

  it('shows error state when copy fails', async () => {
    const files = useFilesStore()
    files.openPath('/workspace/doc.txt', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/doc.txt',
      content: 'hello',
      encoding: 'utf-8',
      mime_type: 'text/plain',
      is_text: true,
      is_image: false,
      size: 5,
    })

    Object.defineProperty(navigator, 'clipboard', {
      value: {
        writeText: vi.fn().mockRejectedValue(new Error('Permission denied')),
      },
      configurable: true,
      writable: true,
    })
    document.execCommand = vi.fn().mockReturnValue(false)

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })

    const copyBtn = wrapper.find('.copy-btn')
    await copyBtn.trigger('click')
    await nextTick()

    expect(wrapper.find('.copy-btn').text()).toBe('⚠ failed')
    expect(wrapper.find('.copy-btn').classes()).toContain('error')
    expect(wrapper.find('.copy-btn').attributes('title')).toBe('Failed to copy')

    vi.advanceTimersByTime(2000)
    await nextTick()
    expect(wrapper.find('.copy-btn').text()).toBe('⧉ copy')

    wrapper.unmount()
  })
})
