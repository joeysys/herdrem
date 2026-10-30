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

  it('selects lines in diff on click, extends with Shift+click, and ignores deleted lines', async () => {
    useConnectionStore().setHello('t', ['git-diff'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'unstaged', '', '', 'pane-diff-1')
    const w = mount(DiffPreview, { attachTo: document.body })
    const req = last()
    d.setDiffContent({
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      req_id: req.req_id as string,
      content: 'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n-old line 1\n+new line 1\n context 2\n+new line 3\n',
    } as never)
    await nextTick()
    await nextTick()

    const rows = w.findAll('.dp-code tr')
    const delRow = rows.find((r) => r.classes().includes('del'))!
    expect(delRow.find('.ln').classes()).not.toContain('pick')
    expect(delRow.find('.lnb').exists()).toBe(false)

    const addRows = rows.filter((r) => r.classes().includes('add'))
    expect(addRows[0].find('.ln').classes()).toContain('pick')
    await addRows[0].get('.lnb').trigger('click')
    expect(addRows[0].classes()).toContain('sel')
    expect(w.findComponent({ name: 'LineCommentBar' }).exists()).toBe(true)

    await addRows[1].get('.lnb').trigger('click', { shiftKey: true })
    expect(delRow.classes()).not.toContain('sel')
    expect(addRows[0].classes()).toContain('sel')
    expect(addRows[1].classes()).toContain('sel')
  })

  it('keeps in-hunk ---/+++ content lines as del/add, not meta (F3 regression)', async () => {
    useConnectionStore().setHello('t', ['git-diff'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'unstaged')
    const w = mount(DiffPreview, { attachTo: document.body })
    const req = last()
    d.setDiffContent({
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      req_id: req.req_id as string,
      content: 'diff --git a/x.sql b/x.sql\n--- a/x.sql\n+++ b/x.sql\n@@ -1,3 +1,3 @@\n a\n--- sql comment\n c\n+++i;\n',
    } as never)
    await nextTick()
    await nextTick()

    const rows = w.findAll('.dp-code tr')
    const meta = rows.filter((r) => r.classes().includes('meta'))
    // only the real file headers are meta; hunk content stays del/add
    expect(meta.map((r) => r.text())).toEqual(['diff --git a/x.sql b/x.sql', '--- a/x.sql', '+++ b/x.sql'])

    const delLine = rows.find((r) => r.classes().includes('del'))!
    expect(delLine.find('.ln').text()).toBe('2') // old-side line number, not selectable
    expect(delLine.find('.lnb').exists()).toBe(false)

    const addLine = rows.find((r) => r.classes().includes('add') && r.text().includes('+++i;'))!
    expect(addLine.find('.ln').text()).toBe('3') // new-side counter kept counting
    expect(addLine.find('.lnb').exists()).toBe(true)
    w.unmount()
  })

  it('does not extend a selection across file sections and clears it on new content', async () => {
    useConnectionStore().setHello('t', ['git-diff'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'unstaged')
    const w = mount(DiffPreview, { attachTo: document.body })
    const req = last()
    d.setDiffContent({
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      req_id: req.req_id as string,
      content: 'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n x\n+p\ndiff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1,2 @@\n y\n+q\n',
    } as never)
    await nextTick()
    await nextTick()

    const secA = w.get('[data-path="a.go"]')
    const secB = w.get('[data-path="b.go"]')
    const bar = () => w.findComponent({ name: 'LineCommentBar' })

    await secA.get('.lnb').trigger('click')
    expect(bar().exists()).toBe(true)
    expect(bar().props('path')).toBe('a.go')

    // Shift+click in another file's section does not extend — it re-anchors there
    await secB.get('.lnb').trigger('click', { shiftKey: true })
    expect(bar().props('path')).toBe('b.go')
    expect(bar().props('start')).toBe(1)
    expect(bar().props('end')).toBe(1)

    // New diff content renumbers every row — the selection must be cleared
    const req2 = last()
    d.setDiffContent({
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      req_id: req2.req_id as string,
      content: 'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n x\n+z\n',
    } as never)
    await nextTick()
    await nextTick()
    expect(bar().exists()).toBe(false)
    w.unmount()
  })

  it('does not count \\ No newline marker lines as file lines', async () => {
    useConnectionStore().setHello('t', ['git-diff'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'unstaged')
    const w = mount(DiffPreview, { attachTo: document.body })
    const req = last()
    d.setDiffContent({
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      req_id: req.req_id as string,
      content: 'diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n\\ No newline at end of file\n',
    } as never)
    await nextTick()
    await nextTick()

    const rows = w.findAll('.dp-code tr')
    const marker = rows.find((r) => r.text().includes('No newline at end of file'))!
    expect(marker.classes()).toContain('meta')
    expect(marker.find('.lnb').exists()).toBe(false)
    expect(marker.find('.ln').text().trim()).toBe('')

    const addLine = rows.find((r) => r.classes().includes('add'))!
    // without the fix the marker row ate a context slot: +b would show 3
    expect(addLine.find('.ln').text()).toBe('2')
    expect(addLine.find('.lnb').exists()).toBe(true)
    w.unmount()
  })

  it('keeps a single roving tab stop per file section', async () => {
    useConnectionStore().setHello('t', ['git-diff'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'unstaged')
    const w = mount(DiffPreview, { attachTo: document.body })
    const req = last()
    d.setDiffContent({
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      req_id: req.req_id as string,
      content: 'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,3 @@\n x\n+p\n+q\n',
    } as never)
    await nextTick()
    await nextTick()

    const secA = w.get('[data-path="a.go"]')
    const tabbable = () => secA.findAll('button.lnb[tabindex="0"]')
    expect(tabbable()).toHaveLength(1)
    await tabbable()[0].trigger('keydown', { key: 'ArrowDown' })
    await nextTick()
    expect(tabbable()).toHaveLength(1)
    expect(tabbable()[0].text()).toBe('2')
    // the number cell itself is not a keyboard target
    expect(secA.find('.ln').attributes('tabindex')).toBeUndefined()
    expect(secA.find('.ln').attributes('role')).toBeUndefined()
    w.unmount()
  })

  it('does not produce a trailing phantom context line when diff ends with a newline', async () => {
    useConnectionStore().setHello('t', ['git-diff'])
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'unstaged')
    const w = mount(DiffPreview, { attachTo: document.body })
    const req = last()
    d.setDiffContent({
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      req_id: req.req_id as string,
      content: 'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,4 +1,4 @@\n line 1\n line 2\n line 3\n line 4\n',
    } as never)
    await nextTick()
    await nextTick()

    const secA = w.get('[data-path="a.go"]')
    const buttons = secA.findAll('button.lnb')
    expect(buttons.map((b) => b.text())).toEqual(['1', '2', '3', '4'])
    w.unmount()
  })
})
