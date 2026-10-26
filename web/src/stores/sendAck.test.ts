import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { applyServerMessage, approvalNotes, noteDisconnect, resetSendAck, syncApprovalNotes, waitAck } from './sendAck'

beforeEach(() => {
  resetSendAck()
  vi.useFakeTimers()
})

afterEach(() => {
  resetSendAck()
  vi.useRealTimers()
})

describe('sendAck', () => {
  it('resolves sent and error frames that carry the same request id', async () => {
    const sent = waitAck('r1', 'p1', 'input')
    applyServerMessage({ type: 'sent', request_id: 'r1' })
    await expect(sent).resolves.toEqual({ ok: true })

    const failed = waitAck('r2', 'p1', 'respond')
    applyServerMessage({ type: 'error', request_id: 'r2', message: 'Prompt changed, please confirm again' })
    await expect(failed).resolves.toEqual({ ok: false, reason: 'error', message: 'Prompt changed, please confirm again' })
    expect(approvalNotes.value.p1).toBe('Prompt changed, please confirm again')
  })

  it('marks an unanswered send unconfirmed after 10 seconds and does not retry', async () => {
    const pending = waitAck('r3', 'p1', 'input')
    await vi.advanceTimersByTimeAsync(9999)
    let settled = false
    void pending.then(() => {
      settled = true
    })
    await vi.advanceTimersByTimeAsync(0)
    expect(settled).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    await expect(pending).resolves.toEqual({ ok: false, reason: 'unconfirmed' })
  })

  it('treats disconnect as unconfirmed and ignores a disconnect with nothing in flight', async () => {
    noteDisconnect()
    const pending = waitAck('r4', 'p9', 'respond')
    noteDisconnect()
    await expect(pending).resolves.toEqual({ ok: false, reason: 'unconfirmed' })
    expect(approvalNotes.value.p9).toBe('Unconfirmed')
    noteDisconnect()
  })

  it('keeps a respond note across the refresh that follows it, then clears on the next prompt change', async () => {
    const pending = waitAck('r5', 'p1', 'respond')
    applyServerMessage({ type: 'error', request_id: 'r5', message: 'Prompt changed, please confirm again' })
    await pending
    syncApprovalNotes([{ pane_id: 'p1', prompt_id: 'a' }])
    expect(approvalNotes.value.p1).toBe('Prompt changed, please confirm again')
    // The stale response itself triggers one poll. That prompt_id change
    // must not wipe the note the user was just shown.
    syncApprovalNotes([{ pane_id: 'p1', prompt_id: 'b' }])
    expect(approvalNotes.value.p1).toBe('Prompt changed, please confirm again')
    syncApprovalNotes([{ pane_id: 'p1', prompt_id: 'c' }])
    expect(approvalNotes.value.p1).toBeUndefined()
  })
})
