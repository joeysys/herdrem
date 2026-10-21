import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import DiffPreview from './DiffPreview.vue'
import { useDiffStore } from '../../stores/diff'
import { useConnectionStore } from '../../stores/connection'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn(() => true), setReconnectDelay: vi.fn(), onStatus: vi.fn(() => () => {}), onMessage: vi.fn(() => () => {}), connect: vi.fn(), disconnect: vi.fn() } }))

// jsdom lacks CSS.escape (browsers have it)
const g = globalThis as unknown as { CSS?: { escape?: (s: string) => string } }
if (!g.CSS?.escape) g.CSS = { ...(g.CSS ?? {}), escape: (s: string) => s.replace(/["\\]/g, '\\$&') }

const last = () => vi.mocked(ws.send).mock.lastCall?.[0] as unknown as Record<string, unknown>

beforeEach(() => {
  document.body.innerHTML = ''
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear()
})

describe('DiffPreview status tab', () => {
  it('appears only when the relay can stage and commit', () => {
    useDiffStore().openDiff('/repo', 'local')
    const without = mount(DiffPreview)
    expect(without.findAll('.dp-mode').map((b) => b.text())).toEqual(['unstaged', 'cached', 'commit'])
    without.unmount()
    useConnectionStore().setHello('t', ['git-diff', 'git-commit'])
    const w = mount(DiffPreview)
    expect(w.findAll('.dp-mode').map((b) => b.text())).toEqual(['status', 'unstaged', 'cached', 'commit'])
  })

  it('switches to status and shows the status view', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-commit'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local')
    const w = mount(DiffPreview)
    await w.findAll('.dp-mode')[0].trigger('click')
    expect(last()).toMatchObject({ type: 'git_status', cwd: '/repo' })
    expect(w.findAll('.dp-mode')[0].attributes('aria-pressed')).toBe('true')
    expect(w.find('.gs').exists()).toBe(true)
  })

  it('the refresh button re-reads the status in status mode', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-commit'])
    useDiffStore().openDiff('/repo', 'local', 'status')
    const w = mount(DiffPreview)
    vi.mocked(ws.send).mockClear()
    await w.get('button[aria-label="Refresh diff"]').trigger('click')
    expect(last()).toMatchObject({ type: 'git_status' })
  })

  it('the header returns to status after a file was opened from the list', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-commit'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    const w = mount(DiffPreview)
    expect(w.get('button[aria-label="Close diff preview"]').text()).toBe('×')
    d.showFileDiff('a.go', false)
    await nextTick()
    const back = w.get('button[aria-label="Back to status"]')
    expect(back.text()).toBe('‹')
    await back.trigger('click')
    expect(d.open).toBe(true)
    expect(d.state?.mode).toBe('status')
    expect(w.get('button[aria-label="Close diff preview"]').text()).toBe('×')
    expect(w.find('.gs').exists()).toBe(true)
  })

  it('opening a file from status scrolls its section into view and highlights it', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-commit'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    const w = mount(DiffPreview, { attachTo: document.body })
    const scrolled: string[] = []
    Element.prototype.scrollIntoView = function () { scrolled.push((this as HTMLElement).dataset.path || '') }
    d.showFileDiff('b.go', false)
    const req = last()
    d.setDiffContent({
      type: 'git_diff_content', cwd: '/repo', mode: 'unstaged', req_id: req.req_id as string,
      content: 'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\ndiff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-p\n+q\n',
    } as never)
    await nextTick()
    await nextTick()
    expect(scrolled).toEqual(['b.go'])
    expect(w.get('[data-path="b.go"]').classes()).toContain('flash')
    expect(d.focusPath).toBe('')
  })
})
