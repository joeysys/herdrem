import { ws } from '../api/ws'
import type { Respond } from '../api/types'
import { useConnectionStore } from '../stores/connection'
import { approvalNotes, waitAck } from '../stores/sendAck'
import { uid } from './id'
import { operationFields } from './session'

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
  Object.assign(msg, operationFields())
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
      void waitAck(requestId, paneId, 'respond').then((result) => {
        // A timeout or a restarted relay is not retried. Refresh the pane
        // and leave the next click to mint a new operation_id.
        if (!result.ok && (result.reason === 'unconfirmed' || result.key === 'stale_boot')) {
          ws.send({ type: 'read_pane', pane_id: paneId, lines: 30, purpose: 'term' })
        }
      })
    }
  }
  return ok
}
