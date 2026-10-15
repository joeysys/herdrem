import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import DiffPreview from './DiffPreview.vue'
import { useDiffStore } from '../../stores/diff'
import { useFilesStore } from '../../stores/files'
import { useConnectionStore } from '../../stores/connection'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn(() => true), setReconnectDelay: vi.fn(), onStatus: vi.fn(() => () => {}), onMessage: vi.fn(() => () => {}), connect: vi.fn(), disconnect: vi.fn() } }))

// jsdom lacks CSS.escape (browsers have it)
const g = globalThis as unknown as { CSS?: { escape?: (s: string) => string } }
if (!g.CSS?.escape) g.CSS = { ...(g.CSS ?? {}), escape: (s: string) => s.replace(/["\\]/g, '\\$&') }

const last = () => vi.mocked(ws.send).mock.lastCall?.[0] as unknown as Record<string, unknown>
const toggle = (w: ReturnType<typeof mount>) => w.find('[aria-label="name-status"]')

function reply(content: string, extra: Record<string, unknown> = {}) {
  const req = last()
  useDiffStore().setDiffContent({
    type: 'git_diff_content', cwd: '/repo', mode: req.mode, commit: req.commit, format: req.format,
    req_id: req.req_id, content, ...extra,
  } as never)
}

const PATCH = 'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\ndiff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-p\n+q\n'

beforeEach(() => {
  document.body.innerHTML = ''
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear()
})

describe('DiffPreview name-status', () => {
  it('offers the toggle only when the relay supports it, and not on the status tab', async () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local')
    const without = mount(DiffPreview)
    expect(toggle(without).exists()).toBe(false)
    without.unmount()

    useConnectionStore().setHello('t', ['git-diff', 'git-commit', 'git-name-status'])
    const w = mount(DiffPreview)
    expect(toggle(w).attributes('aria-pressed')).toBe('false')
    d.setMode('status')
    await nextTick()
    expect(toggle(w).exists()).toBe(false)
  })

  it('asks the relay for --name-status and lists the files, then back to the patch', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-name-status'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local')
    expect(last().format).toBeUndefined()
    const w = mount(DiffPreview)

    await toggle(w).trigger('click')
    expect(last()).toMatchObject({ type: 'git_diff', mode: 'unstaged', format: 'name-status' })
    reply('M\ta.go\nA\tnew.txt\nD\tgone.txt\nR095\told/めい.txt\tnew/めい.txt\n')
    await nextTick()

    const rows = w.findAll('.dp-ns-row')
    expect(rows.map((r) => r.get('.dp-ns-badge').text())).toEqual(['M', 'A', 'D', 'R'])
    expect(rows[3].get('.dp-ns-path').text()).toBe('new/めい.txt ← old/めい.txt')
    expect(rows[3].text()).toContain('95%')
    expect(rows[2].find('.dp-ns-open').exists()).toBe(false) // a deleted file has nothing to open
    expect(w.find('.dp-files').exists()).toBe(false)

    await w.get('[aria-label="Show the patch"]').trigger('click')
    expect(last().format).toBeUndefined()
    reply(PATCH)
    await nextTick()
    expect(w.find('.dp-ns').exists()).toBe(false)
    expect(w.findAll('.dp-file-path').map((p) => p.text())).toEqual(['a.go', 'b.go'])
  })

  it('keeps the git show header above the file list', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-name-status'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'commit', 'abc1234')
    d.setNameStatus(true)
    expect(last()).toMatchObject({ mode: 'commit', commit: 'abc1234', format: 'name-status' })
    const w = mount(DiffPreview)
    reply('commit abc1234\nAuthor: A <a@x>\n\n    fix things\n\nM\tweb/a.ts\n')
    await nextTick()
    expect(w.get('.dp-ns-header').text()).toContain('fix things')
    expect(w.findAll('.dp-ns-row')).toHaveLength(1)
  })

  it('a row opens that file in the patch, scrolled to it; open previews the file', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-name-status'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local')
    d.setNameStatus(true)
    const w = mount(DiffPreview, { attachTo: document.body })
    reply('M\ta.go\nM\tb.go\n')
    await nextTick()

    const opened = vi.spyOn(useFilesStore(), 'openPath').mockImplementation(() => {})
    await w.findAll('.dp-ns-open')[1].trigger('click')
    expect(opened).toHaveBeenCalledWith('/repo/b.go', '/repo', 'local', null)

    const scrolled: string[] = []
    Element.prototype.scrollIntoView = function () { scrolled.push((this as HTMLElement).dataset.path || '') }
    await w.findAll('.dp-ns-path')[1].trigger('click')
    expect(d.nameStatus).toBe(false)
    expect(last().format).toBeUndefined()
    reply(PATCH)
    await nextTick()
    await nextTick()
    expect(scrolled).toEqual(['b.go'])
  })

  it('opening a file from the status tab shows its patch even with name-status on', () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-commit', 'git-name-status'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.setNameStatus(true)
    d.showFileDiff('b.go', false)
    expect(last()).toMatchObject({ type: 'git_diff', mode: 'unstaged' })
    expect(last().format).toBeUndefined()

    // same mode already showing the list: still switches to the patch
    d.setNameStatus(true)
    expect(last().format).toBe('name-status')
    d.showFileDiff('a.go', false)
    expect(last().format).toBeUndefined()
  })

  it('never renders a patch reply as a file list', async () => {
    useConnectionStore().setHello('t', ['git-diff', 'git-name-status'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local')
    d.setNameStatus(true)
    const w = mount(DiffPreview)
    // a relay that ignored the format (no echo) answers with a patch
    reply(PATCH, { format: undefined })
    await nextTick()
    expect(w.find('.dp-ns').exists()).toBe(false)
    expect(w.findAll('.dp-file-path')).toHaveLength(2)
  })
})
