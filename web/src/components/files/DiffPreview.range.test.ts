import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import DiffPreview from './DiffPreview.vue'
import { useDiffStore } from '../../stores/diff'
import { useConnectionStore } from '../../stores/connection'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    setReconnectDelay: vi.fn(),
    onStatus: vi.fn(() => () => {}),
    onMessage: vi.fn(() => () => {}),
    connect: vi.fn(),
    disconnect: vi.fn(),
  },
}))

const sent = () => vi.mocked(ws.send).mock.calls.map((c) => c[0] as unknown as Record<string, unknown>)
const last = () => sent().at(-1)!
const A = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
const B = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear()
  useConnectionStore().setHello('t', ['git-diff', 'git-range', 'git-name-status'])
})

describe('DiffPreview two-commit range', () => {
  it('is hidden until the relay advertises git-range', () => {
    useConnectionStore().setHello('t', ['git-diff'])
    useDiffStore().openDiff('/repo', 'local')
    const w = mount(DiffPreview)
    expect(w.get('[aria-label="Diff mode"]').text()).not.toContain('range')
    w.unmount()
  })

  it('lays the path and the mode tabs on separate rows, then diffs the two picks', async () => {
    useDiffStore().openDiff('/repo', 'local')
    const w = mount(DiffPreview)
    expect(w.get('.dp-bar').text()).toContain('repo')
    expect(w.get('.dp-bar').find('[aria-label="Refresh diff"]').exists()).toBe(true)
    expect(w.get('[aria-label="Diff mode"]').text()).not.toContain('Refresh')

    await w.get('[aria-label="Diff mode"]').findAll('.dp-mode').find((b) => b.text() === 'range')!.trigger('click')
    expect(w.get('.dp-empty').text()).toBe('pick two commits to compare')
    expect(last().type).toBe('git_log')

    useDiffStore().setLogResult({
      type: 'git_log_result',
      cwd: '/repo',
      req_id: last().req_id as string,
      commits: [
        { hash: B, author: 'B', date: '2026-10-05', subject: 'newer' },
        { hash: A, author: 'A', date: '2026-10-04', subject: 'older' },
      ],
    })
    await nextTick()

    const buttons = w.findAll('.dp-commit-btn')
    expect(buttons.map((b) => [b.get('.dp-slot-name').text(), b.get('.dp-commit-label').text()])).toEqual([
      ['from', 'select…'],
      ['to', 'select…'],
    ])
    await buttons[0].trigger('click')
    await w.get('.dp-commit-list').findAll('.dp-commit-item')[1].trigger('click')
    expect(last().type).toBe('git_log') // still waiting for the other end

    await w.findAll('.dp-commit-btn')[1].trigger('click')
    await w.get('.dp-commit-list').findAll('.dp-commit-item')[0].trigger('click')
    expect(last()).toMatchObject({ type: 'git_diff', mode: 'range', commit: A, commit2: B })

    await w.get('[aria-label="Swap the two commits"]').trigger('click')
    expect(last()).toMatchObject({ commit: B, commit2: A })
  })
})
