import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import DiffPreview from './DiffPreview.vue'
import { useDiffStore } from '../../stores/diff'
import { useConnectionStore } from '../../stores/connection'
import { safeStorage } from '../../utils/storage'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn(() => true), setReconnectDelay: vi.fn(), onStatus: vi.fn(() => () => {}), onMessage: vi.fn(() => () => {}), connect: vi.fn(), disconnect: vi.fn() } }))

const sent = () => vi.mocked(ws.send).mock.calls.map((c) => c[0] as unknown as Record<string, unknown>)
const last = () => sent().at(-1)!
const H1 = 'e277d858999bd071b6b0cdd19fbfc737490b09f1'
const H2 = 'ab3096838acf28c8309e6c87635138f1b72396e6'
const LOG = `*   ${H1} (HEAD -> main) merge side\n|\\  \n| * ${H2} (side) side work\n|/  \n`

const modes = (w: VueWrapper) => w.get('[aria-label="Diff mode"]').findAll('.dp-mode')
const switches = (w: VueWrapper) => w.find('[aria-label="Log options"]')
const switchBtn = (w: VueWrapper, label: string) => switches(w).findAll('.dp-mode').find((b) => b.text() === label)!

function reply(content: string, extra: Record<string, unknown> = {}) {
  useDiffStore().setLogViewResult({ type: 'git_log_view_result', cwd: '/repo', req_id: last().req_id as string, content, limit: 300, ...extra })
}

beforeEach(() => {
  safeStorage.removeItem('herdrem_git_log_opts')
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear()
  useConnectionStore().setHello('t', ['git-diff', 'git-name-status', 'git-log-view'])
})

describe('DiffPreview log tab', () => {
  it('appears only when the relay supports it', () => {
    useConnectionStore().setHello('t', ['git-diff'])
    useDiffStore().openDiff('/repo', 'local')
    const w = mount(DiffPreview)
    expect(modes(w).map((b) => b.text())).toEqual(['unstaged', 'cached', 'commit'])
    w.unmount()
    useConnectionStore().setHello('t', ['git-diff', 'git-log-view'])
    expect(modes(mount(DiffPreview)).map((b) => b.text())).toEqual(['unstaged', 'cached', 'commit', 'log'])
  })

  it('asks for oneline --graph by default and renders the history', async () => {
    useDiffStore().openDiff('/repo', 'local')
    const w = mount(DiffPreview)
    await modes(w).find((b) => b.text() === 'log')!.trigger('click')
    expect(last()).toEqual({ type: 'git_log_view', cwd: '/repo', host: 'local', req_id: expect.any(String), oneline: true, graph: true })
    // the name-status switch is a diff thing
    expect(w.find('[aria-label="name-status"]').exists()).toBe(false)
    expect(switches(w).findAll('.dp-mode').map((b) => [b.text(), b.attributes('aria-pressed')])).toEqual([
      ['oneline', 'true'], ['graph', 'true'], ['all', 'false'],
    ])

    reply(LOG)
    await nextTick()
    const hashes = w.findAll('.dp-log-hash')
    expect(hashes.map((h) => h.text())).toEqual([H1.slice(0, 7), H2.slice(0, 7)])
    expect(w.findAll('.dp-log-line')[0].text()).toBe(`*   ${H1.slice(0, 7)} (HEAD -> main) merge side`)
    expect(w.get('.dp-log-decor').text()).toBe('(HEAD -> main)')
    expect(w.get('.dp-log-foot').text()).toBe('showing up to 300 commits')
  })

  it('remembers the switches across reloads', async () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'log')
    const w = mount(DiffPreview)
    await switchBtn(w, 'all').trigger('click')
    await switchBtn(w, 'oneline').trigger('click')
    expect(last()).toMatchObject({ type: 'git_log_view', all: true, graph: true })
    expect(last().oneline).toBeUndefined()
    expect(JSON.parse(safeStorage.getItem('herdrem_git_log_opts')!)).toMatchObject({ all: true, oneline: false, graph: true })
    w.unmount()

    // a fresh page load
    setActivePinia(createPinia())
    useConnectionStore().setHello('t', ['git-diff', 'git-log-view'])
    vi.mocked(ws.send).mockClear()
    useDiffStore().openDiff('/repo', 'local', 'log')
    expect(last()).toEqual({ type: 'git_log_view', cwd: '/repo', host: 'local', req_id: expect.any(String), graph: true, all: true })
    const again = mount(DiffPreview)
    expect(switchBtn(again, 'all').attributes('aria-pressed')).toBe('true')
    expect(switchBtn(again, 'oneline').attributes('aria-pressed')).toBe('false')
  })

  it('ignores a corrupt saved setting', () => {
    safeStorage.setItem('herdrem_git_log_opts', '{not json')
    useDiffStore().openDiff('/repo', 'local', 'log')
    expect(last()).toMatchObject({ oneline: true, graph: true })
  })

  it('full format links the "commit <hash>" lines; a hash opens git show', async () => {
    safeStorage.setItem('herdrem_git_log_opts', JSON.stringify({ oneline: false, graph: false }))
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'log')
    const w = mount(DiffPreview)
    reply(`commit ${H1} (HEAD -> main)\nAuthor: A <a@x>\n\n    revert ${H2}\n`)
    await nextTick()
    const hashes = w.findAll('.dp-log-hash')
    expect(hashes.map((h) => h.text())).toEqual([H1]) // the hash in the message is plain text

    vi.mocked(ws.send).mockClear()
    await hashes[0].trigger('click')
    expect(d.state?.mode).toBe('commit')
    expect(sent().map((m) => m.type)).toEqual(['git_diff', 'git_log'])
    expect(sent()[0]).toMatchObject({ mode: 'commit', commit: H1 })
  })

  it('a late diff reply does not leave the log tab, and errors show', async () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local')
    const diffReq = last().req_id as string
    d.setMode('log')
    d.setDiffContent({ type: 'git_diff_content', cwd: '/repo', mode: 'unstaged', req_id: diffReq, content: 'diff --git a/x b/x\n' })
    d.setDiffError(diffReq)
    expect(d.state).toMatchObject({ mode: 'log', loading: true, error: undefined })

    const w = mount(DiffPreview)
    d.setLogViewError(last().req_id as string)
    await nextTick()
    expect(w.get('.dp-error-text').text()).toContain('git log failed')
  })

  it('says when the output was cut', async () => {
    useDiffStore().openDiff('/repo', 'local', 'log')
    const w = mount(DiffPreview)
    reply(LOG, { truncated: true })
    await nextTick()
    expect(w.get('.dp-log-foot').text()).toBe('output cut at 1 MiB — showing up to 300 commits')
  })
})
