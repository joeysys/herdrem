import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { usePrefsStore } from './prefs'
import { ws } from '../api/ws'

beforeEach(() => {
  setActivePinia(createPinia())
})

function sendSpy() {
  return vi.spyOn(ws, 'send').mockReturnValue(true)
}

/** req_id of the n-th prefs_set sent through the mocked ws. */
function sentReqId(send: ReturnType<typeof sendSpy>, n = 0): string {
  const msg = send.mock.calls[n]?.[0] as { req_id?: string }
  expect(msg.req_id).toBeTruthy()
  return msg.req_id!
}

describe('usePrefsStore save tracking', () => {
  it('marks a section pending after a successful send and clears it on the matching req_id confirm', () => {
    const send = sendSpy()
    const prefs = usePrefsStore()

    expect(prefs.setShellCmds(['ls'])).toBe(true)
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ type: 'prefs_set', section: 'shell_cmds' }))
    expect(prefs.pendingSaves.has('shell_cmds')).toBe(true)
    expect(prefs.shellCmds).toEqual(['ls']) // optimistic local update

    prefs.confirmSaves(sentReqId(send))
    expect(prefs.pendingSaves.size).toBe(0)
    expect(prefs.saveError).toBe('')
    send.mockRestore()
  })

  it('returns false and does not mark pending when the socket is down', () => {
    const send = vi.spyOn(ws, 'send').mockReturnValue(false)
    const prefs = usePrefsStore()

    expect(prefs.setSkills([{ label: 'a', template: 'b' }])).toBe(false)
    expect(prefs.pendingSaves.size).toBe(0)
    send.mockRestore()
  })

  it('failSaves clears pending and records the server error', () => {
    const send = sendSpy()
    const prefs = usePrefsStore()

    prefs.setTiming({ snapshot_poll_ms: 3000 })
    expect(prefs.pendingSaves.has('timing')).toBe(true)

    prefs.failSaves('timing.snapshot_poll_ms must be 0 or in [500, 60000]', sentReqId(send))
    expect(prefs.pendingSaves.size).toBe(0)
    expect(prefs.saveError).toContain('snapshot_poll_ms')
    send.mockRestore()
  })

  it('a multi-section save stays pending until every req_id is confirmed', () => {
    const send = sendSpy()
    const prefs = usePrefsStore()

    // TimingSection writes terminal then timing — two separate prefs_set.
    prefs.setTermPollMs(6000)
    prefs.setTiming({ snapshot_poll_ms: 3000 })
    expect(prefs.pendingSaves.has('terminal')).toBe(true)
    expect(prefs.pendingSaves.has('timing')).toBe(true)

    // First ack confirms only its own section; the other remains in flight.
    prefs.confirmSaves(sentReqId(send, 0))
    expect(prefs.pendingSaves.has('terminal')).toBe(false)
    expect(prefs.pendingSaves.has('timing')).toBe(true)

    prefs.confirmSaves(sentReqId(send, 1))
    expect(prefs.pendingSaves.size).toBe(0)
    send.mockRestore()
  })

  it('a late failure on one section of a multi-section save still records the error', () => {
    const send = sendSpy()
    const prefs = usePrefsStore()

    prefs.setTermPollMs(6000)
    prefs.setTiming({ snapshot_poll_ms: 3000 })

    // terminal acks fine, then timing is rejected: pending empties with an
    // error recorded so the feedback button shows 'failed', not 'saved'.
    prefs.confirmSaves(sentReqId(send, 0))
    prefs.failSaves('timing.snapshot_poll_ms out of range', sentReqId(send, 1))
    expect(prefs.pendingSaves.size).toBe(0)
    expect(prefs.saveError).toContain('snapshot_poll_ms')
    send.mockRestore()
  })

  it('ignores broadcasts without a req_id or with a foreign req_id', () => {
    const send = sendSpy()
    const prefs = usePrefsStore()

    prefs.setShellCmds(['ls'])
    prefs.confirmSaves() // unattributed broadcast (e.g. kind_override or old relay)
    expect(prefs.pendingSaves.has('shell_cmds')).toBe(true)
    prefs.confirmSaves('save-from-another-client')
    expect(prefs.pendingSaves.has('shell_cmds')).toBe(true)

    prefs.confirmSaves(sentReqId(send))
    expect(prefs.pendingSaves.size).toBe(0)
    send.mockRestore()
  })
})

describe('git commit command pref', () => {
  it('reads it from the relay prefs and saves it to the git section', () => {
    const send = sendSpy()
    const prefs = usePrefsStore()
    expect(prefs.gitCommitCmd).toBe('')
    prefs.setPrefs({ git: { commit_cmd: 'npm run commit' } })
    expect(prefs.gitCommitCmd).toBe('npm run commit')
    prefs.setPrefs({})
    expect(prefs.gitCommitCmd).toBe('') // cleared on the relay → cleared here
    prefs.setGitCommitCmd('  git commit -s  ')
    expect(prefs.gitCommitCmd).toBe('git commit -s')
    const sentMsg = send.mock.lastCall?.[0] as unknown as Record<string, unknown>
    expect(sentMsg).toMatchObject({ type: 'prefs_set', section: 'git', data: { commit_cmd: 'git commit -s' } })
  })
})
