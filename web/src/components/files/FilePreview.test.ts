import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import FilePreview from './FilePreview.vue'
import { useFilesStore } from '../../stores/files'
import { usePreviewWidth } from '../../composables/usePreviewWidth'

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

describe('FilePreview line width setting', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    usePreviewWidth().setPreviewWidth('screen')
  })

  function mountWithCode() {
    const files = useFilesStore()
    files.openPath('/workspace/code.ts', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/code.ts',
      // the long line is the contract: at a fixed width it must wrap at Nch,
      // not stretch the column to the unwrapped line length
      content: 'const x = 1\nconst long = "' + 'a'.repeat(300) + '"\n',
      encoding: 'utf-8',
      mime_type: 'text/plain',
      is_text: true,
      is_image: false,
      size: 320,
    })
    return mount(FilePreview, { props: { directoryOpen: false } })
  }

  it('wraps at the screen edge by default, switches to a fixed char column, and can disable wrapping', async () => {
    const { previewWidth, setPreviewWidth } = usePreviewWidth()
    const wrapper = mountWithCode()
    const table = wrapper.find('.fp-code')

    expect(previewWidth.value).toBe('screen')
    expect(table.classes()).not.toContain('w-num')
    expect(table.classes()).not.toContain('w-none')

    // A numeric width pins the wrap boundary on an inner block inside each
    // code cell — in auto table layout a td's own width is only a floor, so
    // the constraint has to live on .lcw for a 300-char line to wrap at 120ch.
    setPreviewWidth('120')
    await nextTick()
    expect(table.classes()).toContain('w-num')
    const inners = wrapper.findAll('.fp-code .lc .lcw')
    expect(inners.length).toBe(3) // trailing newline → third (empty) line
    for (const inner of inners) {
      expect(inner.attributes('style')).toContain('width: 120ch')
    }

    // 'none' keeps one real line on one visual line — no width constraint
    setPreviewWidth('none')
    await nextTick()
    expect(table.classes()).toContain('w-none')
    expect(table.classes()).not.toContain('w-num')
    expect(wrapper.find('.fp-code .lc .lcw').attributes('style') ?? '').not.toContain('width')

    // back to the default screen-edge wrapping
    setPreviewWidth('screen')
    await nextTick()
    expect(table.classes()).not.toContain('w-num')
    expect(table.classes()).not.toContain('w-none')

    wrapper.unmount()
  })
})

describe('FilePreview HTML sandbox notice and tunnel preview', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.spyOn(globalThis, 'fetch').mockImplementation(() =>
      Promise.resolve(new Response(null, { status: 204 }))
    )
  })

  it('displays sandbox tip, open-in-new-tab link, and tunnel preview button for HTML', async () => {
    const files = useFilesStore()
    const conn = (await import('../../stores/connection')).useConnectionStore()
    conn.features = ['tunnels', 'preview-run']
    const { ws } = await import('../../api/ws')
    files.openPath('/workspace/test.html', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/test.html',
      content: '<h1>Hello</h1><script>console.log("script");</script>',
      encoding: 'utf-8',
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 53,
    })

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })
    await nextTick()

    // Sandbox tip should be visible in HTML preview mode
    const tip = wrapper.find('.fp-sandbox-tip')
    expect(tip.exists()).toBe(true)
    expect(tip.text()).toContain('sandboxed')
    expect(tip.classes()).toContain('has-scripts')

    // Open in new tab — runs scripts too, in the same opaque origin as the
    // run frame (server sends `sandbox allow-scripts` CSP): no relay
    // cookies or parent DOM, though it can still issue network requests.
    const newTabLink = tip.find('a[target="_blank"]')
    expect(newTabLink.exists()).toBe(true)
    expect(newTabLink.text()).toContain('Open in new tab')
    expect(newTabLink.attributes('href')).toContain('run=1')
    expect(newTabLink.attributes('title')).toContain('isolated origin')
    // The tip must explain both run paths share the same isolation boundary
    expect(tip.text()).toContain('isolated origin')

    // ▶ switches the iframe sandbox to allow-scripts and adds ?run=1; the
    // server then sends CSP `sandbox allow-scripts` so scripts execute in
    // an opaque origin. src keeps the capability-scoped preview URL.
    const runBtn = wrapper.find('button[aria-label="Run scripts"]')
    expect(runBtn.exists()).toBe(true)
    expect(wrapper.find('iframe').attributes('sandbox')).toBe('')
    await runBtn.trigger('click')
    const runFrame = wrapper.find('iframe')
    expect(runFrame.attributes('sandbox')).toBe('allow-scripts')
    expect(runFrame.attributes('src')).toContain('/preview/')
    expect(runFrame.attributes('src')).toContain('run=1') // server relaxes CSP for any load carrying run=1, iframe or new tab
    await runBtn.trigger('click')
    expect(wrapper.find('iframe').attributes('sandbox')).toBe('')

    // Tunnel preview button
    const tunnelBtn = tip.find('.fp-tip-tunnel-btn, .fp-tip-btn.primary')
    expect(tunnelBtn.exists()).toBe(true)
    expect(tunnelBtn.text()).toContain('Public tunnel preview')

    // Trigger tunnel creation
    await tunnelBtn.trigger('click')
    expect(ws.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'tunnel_create',
        file_path: '/workspace/test.html',
        auth_mode: 'token_gate',
      })
    )

    wrapper.unmount()
  })

  it('resets run mode when switching to another file', async () => {
    const files = useFilesStore()
    const conn = (await import('../../stores/connection')).useConnectionStore()
    conn.features = ['preview-run']
    files.openPath('/workspace/one.html', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/one.html',
      content: '<script>console.log("a");</script>',
      encoding: 'utf-8',
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 30,
    })
    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })
    await nextTick()
    await wrapper.find('button[aria-label="Run scripts"]').trigger('click')
    expect(wrapper.find('iframe').attributes('sandbox')).toBe('allow-scripts')

    // Switching files must not inherit run mode — script execution is a
    // per-document choice, so the next HTML loads fully sandboxed again.
    files.openPath('/workspace/two.html', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/two.html',
      content: '<script>console.log("b");</script>',
      encoding: 'utf-8',
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 30,
    })
    await nextTick()
    await nextTick() // watch is async (capability mint)
    const frame = wrapper.find('iframe')
    expect(frame.attributes('sandbox')).toBe('')
    expect(frame.attributes('src')).not.toContain('run=1')
    wrapper.unmount()
  })

  it('ignores a preview capability that arrives after switching files', async () => {
    const files = useFilesStore()
    const conn = (await import('../../stores/connection')).useConnectionStore()
    conn.features = ['preview-run']
    // Hold each /preview-auth response so the test decides the arrival order.
    const pending: Array<(cap: string) => void> = []
    vi.mocked(globalThis.fetch).mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          pending.push((cap) =>
            resolve(new Response(JSON.stringify({ capability: cap, run_capability: `run-${cap}` }), { status: 200 })),
          )
        }),
    )
    const html = (path: string) => ({
      type: 'file_content' as const,
      path,
      content: '<script>1</script>',
      encoding: 'utf-8' as const,
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 18,
    })
    files.openPath('/proj/index.html', '/proj', 'local', null)
    files.setContent(html('/proj/index.html'))
    const wrapper = mount(FilePreview, { props: { directoryOpen: false } })
    await nextTick()
    files.openPath('/proj/admin/page.html', '/proj/admin', 'local', null)
    files.setContent(html('/proj/admin/page.html'))
    await nextTick()
    expect(pending).toHaveLength(2)

    // The newer file's mint answers first, then the stale wider-dir mint.
    pending[1]('cap-admin')
    await flushPromises()
    pending[0]('cap-proj')
    await flushPromises()

    const src = wrapper.find('iframe').attributes('src')
    expect(src).toContain('/preview/cap-admin/')
    expect(src).not.toContain('cap-proj')
    expect(wrapper.find('a[target="_blank"]').attributes('href')).toContain('/preview/run-cap-admin/')
    wrapper.unmount()
  })

  it('hides run affordances when the relay lacks the preview-run feature', async () => {
    const files = useFilesStore()
    const conn = (await import('../../stores/connection')).useConnectionStore()
    conn.features = ['tunnels'] // no preview-run (old relay)
    files.openPath('/workspace/legacy.html', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/legacy.html',
      content: '<script>console.log("x");</script>',
      encoding: 'utf-8',
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 30,
    })
    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })
    await nextTick()
    await nextTick()

    // ▶ stays hidden — clicking it on an old relay would look enabled but
    // the response CSP would still block scripts.
    expect(wrapper.find('button[aria-label="Run scripts"]').exists()).toBe(false)
    // The new-tab link falls back to the sandboxed static URL.
    const link = wrapper.find('a[target="_blank"]')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).not.toContain('run=1')
    expect(link.attributes('title')).toContain('sandboxed')
    wrapper.unmount()
  })

  it('does not render sandboxed/js warning when HTML contains no scripts', async () => {
    const files = useFilesStore()
    const conn = (await import('../../stores/connection')).useConnectionStore()
    conn.features = ['tunnels', 'preview-run']

    files.openPath('/workspace/static.html', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/static.html',
      content: '<h1>Pure HTML</h1><p>No scripts here.</p>',
      encoding: 'utf-8',
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 41,
    })

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })
    await nextTick()

    const tip = wrapper.find('.fp-sandbox-tip')
    expect(tip.exists()).toBe(true)
    expect(tip.classes()).not.toContain('has-scripts')
    expect(wrapper.find('.fp-tip-msg').exists()).toBe(false)
    expect(tip.text()).not.toContain('sandboxed')
    expect(tip.text()).not.toContain('JavaScript')

    // Actions are still accessible
    expect(tip.find('a[target="_blank"]').exists()).toBe(true)
    expect(tip.find('.fp-tip-btn.primary').exists()).toBe(true)

    wrapper.unmount()
  })

  it('renders active tunnel actions when tunnel is established', async () => {
    const files = useFilesStore()
    const conn = (await import('../../stores/connection')).useConnectionStore()
    const { useTunnelsStore } = await import('../../stores/tunnels')
    const tunnels = useTunnelsStore()
    conn.features = ['tunnels', 'preview-run']

    files.openPath('/workspace/flow.html', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/flow.html',
      content: '<h1>Flow</h1>',
      encoding: 'utf-8',
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 13,
    })

    tunnels.setTunnels([
      {
        id: 'tun-file-flow',
        file_path: '/workspace/flow.html',
        host: 'local',
        public_url: 'https://test-flow.trycloudflare.com',
        status: 'active',
        auth_mode: 'token_gate',
        token: 'secret123',
        created_at: Date.now(),
      },
    ])

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })
    await nextTick()

    const tip = wrapper.find('.fp-sandbox-tip')
    expect(tip.exists()).toBe(true)
    expect(tip.text()).toContain('Public URL ready')

    const publicLink = tip.find('a[href*="test-flow.trycloudflare.com"]')
    expect(publicLink.exists()).toBe(true)
    expect(publicLink.attributes('href')).toBe('https://test-flow.trycloudflare.com?token=secret123')

    wrapper.unmount()
  })

  it('handles tunnel create error and allows dismiss / retry', async () => {
    const files = useFilesStore()
    const conn = (await import('../../stores/connection')).useConnectionStore()
    const { useTunnelsStore } = await import('../../stores/tunnels')
    const tunnels = useTunnelsStore()
    conn.features = ['tunnels', 'preview-run']

    files.openPath('/workspace/error.html', '/workspace', 'local', null)
    files.setContent({
      type: 'file_content',
      path: '/workspace/error.html',
      content: '<h1>Error</h1><script>console.log(1)</script>',
      encoding: 'utf-8',
      mime_type: 'text/html',
      is_text: true,
      is_image: false,
      size: 44,
    })

    const wrapper = mount(FilePreview, {
      props: { directoryOpen: false },
    })
    await nextTick()

    const startBtn = wrapper.find('.fp-tip-btn.primary')
    await startBtn.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('Starting…')

    // Simulate backend rejection
    tunnels.handleCreateError('file not found or outside sandbox roots')
    await nextTick()

    expect(wrapper.text()).not.toContain('Starting…')
    expect(wrapper.text()).toContain('Failed to start: file not found or outside sandbox roots')
    expect(wrapper.find('button.danger').text()).toBe('Dismiss')

    // Dismiss the error
    await wrapper.find('button.danger').trigger('click')
    await nextTick()

    expect(wrapper.text()).not.toContain('Failed to start')
    expect(wrapper.find('.fp-tip-btn.primary').text()).toContain('Public tunnel preview')

    wrapper.unmount()
  })
})
