import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ws } from '../api/ws'
import { useConnectionStore } from '../stores/connection'
import { applyServerMessage, approvalNotes, resetSendAck } from '../stores/sendAck'
import { dispatchRespond, promptChecked } from './guard'

vi.mock('../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'disconnected',
  },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  resetSendAck()
  vi.mocked(ws.send).mockReset()
  vi.mocked(ws.send).mockReturnValue(true)
})

describe('dispatchRespond', () => {
  it('omits the new fields when the relay did not advertise them', () => {
    dispatchRespond('p1', 'y', { submit: false, promptId: 'abc' })
    expect(ws.send).toHaveBeenCalledWith({ type: 'respond', pane_id: 'p1', text: 'y', submit: false })
    expect(vi.mocked(ws.send).mock.calls[0][0]).not.toHaveProperty('operation_id')
    expect(promptChecked({ prompt_id: 'abc' })).toBe(false)
  })

  it('sends prompt_id and request_id only for the advertised features', () => {
    useConnectionStore().setHello('t', ['prompt-guard', 'send-ack'])
    expect(promptChecked({ prompt_id: 'abc' })).toBe(true)
    expect(promptChecked({ prompt_id: '' })).toBe(false)
    dispatchRespond('p1', 'y', { submit: false, promptId: 'abc' })
    const msg = vi.mocked(ws.send).mock.calls[0][0]
    expect(msg).toMatchObject({
      type: 'respond',
      pane_id: 'p1',
      text: 'y',
      submit: false,
      prompt_id: 'abc',
    })
    expect(msg).toHaveProperty('request_id')
    expect(typeof (msg as { request_id?: string }).request_id).toBe('string')
    expect(msg).not.toHaveProperty('operation_id')
  })

  it('sends operation_id and boot_id once hello has a boot id', () => {
    useConnectionStore().setHello('t', ['send-ack'], 'boot-1')
    dispatchRespond('p1', 'y', { submit: false })
    const msg = vi.mocked(ws.send).mock.calls[0][0] as { operation_id?: string; boot_id?: string; request_id?: string }
    expect(msg.boot_id).toBe('boot-1')
    expect(typeof msg.operation_id).toBe('string')
    expect(msg.operation_id).not.toBe('')
    expect(typeof msg.request_id).toBe('string')
    vi.mocked(ws.send).mockClear()
    dispatchRespond('p1', 'n', { submit: false })
    const again = vi.mocked(ws.send).mock.calls[0][0] as { operation_id?: string; boot_id?: string }
    expect(again.boot_id).toBe('boot-1')
    expect(again.operation_id).not.toBe(msg.operation_id)
  })

  it('refreshes the pane after a respond timeout and does not resend the click', async () => {
    vi.useFakeTimers()
    try {
      useConnectionStore().setHello('t', ['send-ack'], 'boot-1')
      dispatchRespond('p1', 'y', { submit: false })
      expect(ws.send).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(10_000)
      expect(ws.send).toHaveBeenCalledTimes(2)
      expect(ws.send).toHaveBeenLastCalledWith({ type: 'read_pane', pane_id: 'p1', lines: 30, purpose: 'term' })
    } finally {
      vi.useRealTimers()
    }
  })

  it('refreshes on stale_boot and leaves an ordinary send error alone', async () => {
    useConnectionStore().setHello('t', ['send-ack'], 'boot-1')
    dispatchRespond('p1', 'y', { submit: false })
    const first = vi.mocked(ws.send).mock.calls[0][0] as { request_id: string }
    applyServerMessage({ type: 'error', request_id: first.request_id, message: 'Send failed', key: 'unavailable' })
    await Promise.resolve()
    await Promise.resolve()
    expect(ws.send).toHaveBeenCalledTimes(1)

    dispatchRespond('p1', 'n', { submit: false })
    const second = vi.mocked(ws.send).mock.calls[1][0] as { request_id: string }
    applyServerMessage({
      type: 'error',
      request_id: second.request_id,
      message: 'relay restarted; refresh and try again',
      key: 'stale_boot',
    })
    await Promise.resolve()
    await Promise.resolve()
    expect(ws.send).toHaveBeenLastCalledWith({ type: 'read_pane', pane_id: 'p1', lines: 30, purpose: 'term' })
  })

  it('marks the card unconfirmed when the socket is not open', () => {
    useConnectionStore().setHello('t', ['send-ack'])
    vi.mocked(ws.send).mockReturnValue(false)
    expect(dispatchRespond('p1', 'n', { submit: false })).toBe(false)
    expect(approvalNotes.value.p1).toBe('Unconfirmed')
    expect(ws.send).toHaveBeenCalledTimes(1)
  })
})
