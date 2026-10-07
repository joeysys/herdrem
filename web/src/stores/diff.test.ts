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

  it('range mode waits for both commits, then sends git diff A B', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local', 'commit')
    diff.setCommit('aaa')
    vi.mocked(ws.send).mockClear()

    diff.setMode('range')
    expect(diff.state).toMatchObject({ mode: 'range', commit: '', commit2: 'aaa', loading: false })
    expect(vi.mocked(ws.send).mock.calls.map((c) => (c[0] as { type: string }).type)).toEqual(['git_log'])

    diff.setRangeCommit('from', 'bbb')
    const sent = vi.mocked(ws.send).mock.lastCall?.[0] as { type: string; mode: string; commit: string; commit2: string }
    expect(sent).toMatchObject({ type: 'git_diff', mode: 'range', commit: 'bbb', commit2: 'aaa' })

    diff.swapRange()
    const swapped = vi.mocked(ws.send).mock.lastCall?.[0] as { commit: string; commit2: string }
    expect(swapped).toMatchObject({ commit: 'aaa', commit2: 'bbb' })
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

describe('git stage/commit send guards', () => {
  it('clears the staging flag when the socket cannot take the message', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local', 'status')
    vi.mocked(ws.send).mockReturnValue(false)
    diff.stage(['a.txt'])
    expect(diff.staging).toBe(false)
    expect(diff.statusError).toBe('not connected')

    // the flag being cleared is what lets a retry go through at all
    vi.mocked(ws.send).mockReturnValue(true)
    diff.stage(['a.txt'])
    expect(diff.staging).toBe(true)
    expect(vi.mocked(ws.send)).toHaveBeenLastCalledWith(
      expect.objectContaining({ type: 'git_stage', paths: ['a.txt'] }),
    )
  })

  it('rejects a stage selection whose frame would exceed the relay read limit', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local', 'status')
    vi.mocked(ws.send).mockClear()
    // ~700 paths of ~45 bytes each overflow the relay's 32 KiB read limit —
    // without the guard the frame kills the whole connection
    const paths = Array.from({ length: 700 }, (_, i) => `src/some/long/path/file-number-${i}.vue`)
    diff.stage(paths)
    expect(ws.send).not.toHaveBeenCalled()
    expect(diff.staging).toBe(false)
    expect(diff.statusError).toContain('too large')
  })

  it('rejects a commit message that would overflow the frame before the server sees it', () => {
    const diff = useDiffStore()
    diff.openDiff('/repo', 'local', 'status')
    vi.mocked(ws.send).mockClear()
    diff.commit({ message: 'x'.repeat(17 * 1024) })
    expect(ws.send).not.toHaveBeenCalled()
    expect(diff.committing).toBe(false)
    expect(diff.commitOutcome).toMatchObject({ ok: false, error: expect.stringContaining('too long') })
  })
})
