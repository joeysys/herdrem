import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useDiffStore } from './diff'
import { ws } from '../api/ws'
import type { GitStatusResultMsg } from '../api/types'

vi.mock('../api/ws', () => ({ ws: { send: vi.fn(() => true) } }))

type Sent = Record<string, unknown>
const sent = () => vi.mocked(ws.send).mock.calls.map((c) => c[0] as unknown as Sent)
const last = () => sent().at(-1)!

function statusMsg(reqId: string, extra: Partial<GitStatusResultMsg> = {}): GitStatusResultMsg {
  return {
    type: 'git_status_result', cwd: '/repo', req_id: reqId, branch: 'main', ahead: 0, behind: 0,
    staged: [], unstaged: [{ path: 'a.go', status: 'M' }], untracked: [], conflicted: [], staged_outside: 0, ...extra,
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockReset()
  vi.mocked(ws.send).mockReturnValue(true)
})

describe('diff store — git status', () => {
  it('opens straight into status and asks for it (no diff request)', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'box', 'status')
    expect(d.state?.mode).toBe('status')
    expect(sent().map((m) => m.type)).toEqual(['git_status'])
    expect(last()).toMatchObject({ cwd: '/repo', host: 'box' })
    expect(d.statusLoading).toBe(true)
  })

  it('applies the matching status reply and ignores stale ones', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    const id = last().req_id as string
    d.setStatusResult(statusMsg('old-request', { branch: 'stale' }))
    expect(d.status).toBeNull()
    d.setStatusResult(statusMsg(id))
    expect(d.status?.branch).toBe('main')
    expect(d.status?.unstaged).toHaveLength(1)
    expect(d.statusLoading).toBe(false)
    expect((d.status as unknown as Record<string, unknown>).type).toBeUndefined()
  })

  it('ignores a status for another folder', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.setStatusResult({ ...statusMsg(last().req_id as string), cwd: '/elsewhere' })
    expect(d.status).toBeNull()
  })

  it('stages selected paths, everything, or unstages, and takes the reply as the new status', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.stage(['a.go', 'b c.go'])
    expect(last()).toMatchObject({ type: 'git_stage', cwd: '/repo', paths: ['a.go', 'b c.go'], unstage: false })
    expect(d.staging).toBe(true)
    d.stage(['x']) // ignored while one is in flight
    expect(sent().filter((m) => m.type === 'git_stage')).toHaveLength(1)
    d.setStatusResult(statusMsg(last().req_id as string, { staged: [{ path: 'a.go', status: 'M' }] }))
    expect(d.staging).toBe(false)
    expect(d.status?.staged[0].path).toBe('a.go')

    d.stageAll()
    expect(last()).toMatchObject({ type: 'git_stage', all: true, unstage: false })
    d.setStatusResult(statusMsg(last().req_id as string))
    d.stageAll(true)
    expect(last()).toMatchObject({ all: true, unstage: true })
    d.setStatusResult(statusMsg(last().req_id as string))
    d.stage(['a.go'], true)
    expect(last()).toMatchObject({ paths: ['a.go'], unstage: true })
  })

  it('surfaces a stage / status error and clears the busy state', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.stage(['../x'])
    d.setStatusError(last().req_id as string, 'invalid git request')
    expect(d.staging).toBe(false)
    expect(d.statusError).toBe('invalid git request')
  })

  it('commits with a message or with the configured command, one at a time', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.commit({ message: 'fix it' })
    expect(last()).toMatchObject({ type: 'git_commit', cwd: '/repo', message: 'fix it' })
    expect(last().use_command).toBeUndefined()
    d.commit({ message: 'again' })
    expect(sent().filter((m) => m.type === 'git_commit')).toHaveLength(1)
    d.setCommitResult({ type: 'git_commit_result', cwd: '/repo', req_id: last().req_id as string, ok: true, hash: 'abc', subject: 'fix it' })
    expect(d.committing).toBe(false)
    expect(d.commitOutcome).toMatchObject({ ok: true, hash: 'abc' })
    expect(last().type).toBe('git_status') // refreshed after the commit

    d.commit({ useCommand: true })
    expect(last()).toMatchObject({ type: 'git_commit', use_command: true })
    expect(last().message).toBeUndefined()
  })

  it('reports a commit it could not send', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    vi.mocked(ws.send).mockReturnValue(false)
    d.commit({ message: 'x' })
    expect(d.committing).toBe(false)
    expect(d.commitOutcome).toMatchObject({ ok: false, error: 'not connected' })
  })

  it('keeps a failed commit\'s output for display', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.commit({ message: 'x' })
    d.setCommitResult({ type: 'git_commit_result', cwd: '/repo', req_id: last().req_id as string, ok: false, error: 'git commit: exit status 1', output: 'lint failed' })
    expect(d.commitOutcome).toMatchObject({ ok: false, output: 'lint failed' })
  })

  it('jumps from a status entry to its diff in the right view', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.showFileDiff('src/a.go', true)
    expect(d.state?.mode).toBe('cached')
    expect(d.focusPath).toBe('src/a.go')
    expect(last()).toMatchObject({ type: 'git_diff', mode: 'cached' })
    d.showFileDiff('src/b.go', false)
    expect(d.state?.mode).toBe('unstaged')
  })

  it('refresh in status mode re-reads the status, not a diff', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'unstaged')
    d.setMode('status')
    vi.mocked(ws.send).mockClear()
    d.refresh()
    expect(sent().map((m) => m.type)).toEqual(['git_status'])
  })

  it('closing forgets the status state', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.setStatusResult(statusMsg(last().req_id as string))
    d.close()
    expect(d.status).toBeNull()
    expect(d.commitOutcome).toBeNull()
  })
})
