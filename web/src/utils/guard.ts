import { ws } from '../api/ws'
import type { Respond } from '../api/types'
import { useConnectionStore } from '../stores/connection'
import { approvalNotes, waitAck } from '../stores/sendAck'
import { uid } from './id'

export function promptChecked(pane: { prompt_id?: string } | null | undefined): boolean {
  if (!pane?.prompt_id) return false
  return useConnectionStore().hasFeature('prompt-guard')
}

export interface RespondOpts {
  submit?: boolean
  optIndex?: number
  promptId?: string
}

// One respond path for the approval cards and the terminal store.
// prompt_id and request_id are added only when the relay advertised them.
export function dispatchRespond(paneId: string, text: string, opts?: RespondOpts): boolean {
  const conn = useConnectionStore()
  const msg: Respond = { type: 'respond', pane_id: paneId, text }
  if (opts?.optIndex !== undefined) msg.opt_index = opts.optIndex
  if (opts?.submit !== undefined) msg.submit = opts.submit
  if (opts?.promptId && conn.hasFeature('prompt-guard')) msg.prompt_id = opts.promptId
  let requestId = ''
  if (conn.hasFeature('send-ack')) {
    requestId = uid()
    msg.request_id = requestId
  }
  const ok = ws.send(msg)
  if (requestId) {
    if (!ok) {
      // No frame left the browser. Mark it unconfirmed without waiting.
      approvalNotes.value = { ...approvalNotes.value, [paneId]: 'Unconfirmed' }
    } else {
      void waitAck(requestId, paneId, 'respond')
    }
  }
  return ok
}
