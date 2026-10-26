import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ws } from '../api/ws'
import { useConnectionStore } from '../stores/connection'
import { approvalNotes, resetSendAck } from '../stores/sendAck'
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
  })

  it('marks the card unconfirmed when the socket is not open', () => {
    useConnectionStore().setHello('t', ['send-ack'])
    vi.mocked(ws.send).mockReturnValue(false)
    expect(dispatchRespond('p1', 'n', { submit: false })).toBe(false)
    expect(approvalNotes.value.p1).toBe('Unconfirmed')
  })
})
