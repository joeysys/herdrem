import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useDiffStore } from './diff'
import { ws } from '../api/ws'
import type { GitDiffContent, GitLogResult } from '../api/types'

vi.mock('../api/ws', () => ({ ws: { send: vi.fn() } }))

describe('useDiffStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('opens in unstaged mode by default and sends a git_diff request', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')

    expect(diff.open).toBe(true)
    expect(diff.state).toMatchObject({ cwd: '/repo', host: 'local', mode: 'unstaged', loading: true })
    const sent = vi.mocked(ws.send).mock.lastCall?.[0] as { type: string; cwd: string; mode: string }
    expect(sent).toMatchObject({ type: 'git_diff', cwd: '/repo', mode: 'unstaged' })
  })

  it('switching to commit mode with no commit fetches the log and waits for a pick', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')
    vi.mocked(ws.send).mockClear()

    diff.setMode('commit')

    expect(diff.state?.mode).toBe('commit')
    expect(diff.state?.loading).toBe(false)
    // only a git_log should have been sent, not a git_diff
    const sent = vi.mocked(ws.send).mock.lastCall?.[0] as { type: string }
    expect(sent.type).toBe('git_log')
  })

  it('applies diff content matching the in-flight request id', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')
    // capture the actual req_id sent
    const sent = vi.mocked(ws.send).mock.calls[0][0] as { req_id: string }
    const msg: GitDiffContent = {
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      content: 'diff --git a/x b/x\n',
      req_id: sent.req_id,
    }
    diff.setDiffContent(msg)
    expect(diff.state?.content).toContain('diff --git')
    expect(diff.state?.loading).toBe(false)
  })

  it('ignores stale diff responses (mismatched req_id)', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')
    const stale: GitDiffContent = {
      type: 'git_diff_content',
      cwd: '/repo',
      mode: 'unstaged',
      content: 'stale content',
      req_id: 'not-the-current-id',
    }
    diff.setDiffContent(stale)
    expect(diff.state?.content).toBe('')
  })

  it('ignores diff responses for a different cwd', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')
    const sent = vi.mocked(ws.send).mock.calls[0][0] as { req_id: string }
    const msg: GitDiffContent = {
      type: 'git_diff_content',
      cwd: '/other',
      mode: 'unstaged',
      content: 'wrong repo',
      req_id: sent.req_id,
    }
    diff.setDiffContent(msg)
    expect(diff.state?.content).toBe('')
  })

  it('log result auto-selects the newest commit when in commit mode with none chosen', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')
    diff.setMode('commit')
    const logSent = vi.mocked(ws.send).mock.lastCall?.[0] as { req_id: string }

    const logMsg: GitLogResult = {
      type: 'git_log_result',
      cwd: '/repo',
      commits: [
        { hash: 'aaa', author: 'A', date: '2026-08-26', subject: 'newest' },
        { hash: 'bbb', author: 'B', date: '2026-08-25', subject: 'older' },
      ],
      req_id: logSent.req_id,
    }
    diff.setLogResult(logMsg)
    expect(diff.state?.commits.length).toBe(2)
    // auto-pick should have triggered a git_diff for the newest commit
    const lastSent = vi.mocked(ws.send).mock.lastCall?.[0] as { type: string; commit: string }
    expect(lastSent.type).toBe('git_diff')
    expect(lastSent.commit).toBe('aaa')
  })

  it('setDiffError marks loading done and stores an error message', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')
    const sent = vi.mocked(ws.send).mock.calls[0][0] as { req_id: string }
    diff.setDiffError(sent.req_id)
    expect(diff.state?.loading).toBe(false)
    expect(diff.state?.error).toBeTruthy()
    expect(diff.state?.content).toBe('')
  })

  it('close clears state', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local')
    diff.close()
    expect(diff.open).toBe(false)
    expect(diff.state).toBeNull()
  })
})
