import { ref } from 'vue'

// Send receipts for send-ack. This module must not import the connection
// store: connection.ts calls in on each server frame.

export type AckKind = 'input' | 'respond'

export type AckResult =
  | { ok: true }
  | { ok: false; reason: 'error'; message: string; key?: string }
  | { ok: false; reason: 'unconfirmed' }

const ACK_MS = 10_000

interface Waiter {
  paneId: string
  kind: AckKind
  timer: ReturnType<typeof setTimeout>
  resolve: (result: AckResult) => void
}

const waiters = new Map<string, Waiter>()
const seenPrompt = new Map<string, string>()
// A respond note survives the prompt_id change caused by the same stale
// refresh. The next change, or an explicit clear, drops it.
const noteHold = new Set<string>()

export const approvalNotes = ref<Record<string, string>>({})

export function waitAck(id: string, paneId: string, kind: AckKind): Promise<AckResult> {
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      waiters.delete(id)
      if (kind === 'respond') setApprovalNote(paneId, 'Unconfirmed')
      resolve({ ok: false, reason: 'unconfirmed' })
    }, ACK_MS)
    waiters.set(id, { paneId, kind, timer, resolve })
  })
}

export function applyServerMessage(msg: { type: string; request_id?: string; message?: string; key?: string }) {
  const id = msg.request_id
  if (!id) return
  const waiter = waiters.get(id)
  if (!waiter) return
  clearTimeout(waiter.timer)
  waiters.delete(id)
  if (msg.type === 'sent') {
    if (waiter.kind === 'respond') clearApprovalNote(waiter.paneId)
    waiter.resolve({ ok: true })
    return
  }
  if (msg.type === 'error') {
    const message = msg.message || 'Send failed'
    if (waiter.kind === 'respond') setApprovalNote(waiter.paneId, message)
    const result: AckResult = { ok: false, reason: 'error', message }
    if (msg.key) result.key = msg.key
    waiter.resolve(result)
  }
}

// Drop every in-flight receipt. A disconnect with no waiter is a no-op, so
// the status callback can run on the initial disconnected state.
export function noteDisconnect() {
  if (waiters.size === 0) return
  for (const waiter of waiters.values()) {
    clearTimeout(waiter.timer)
    if (waiter.kind === 'respond') setApprovalNote(waiter.paneId, 'Unconfirmed')
    waiter.resolve({ ok: false, reason: 'unconfirmed' })
  }
  waiters.clear()
}

export function syncApprovalNotes(panes: { pane_id: string; prompt_id?: string }[]) {
  const alive = new Set<string>()
  const next = { ...approvalNotes.value }
  let changed = false
  for (const pane of panes) {
    alive.add(pane.pane_id)
    const id = pane.prompt_id || ''
    const prev = seenPrompt.get(pane.pane_id)
    if (prev !== undefined && prev !== id && next[pane.pane_id]) {
      if (noteHold.has(pane.pane_id)) noteHold.delete(pane.pane_id)
      else {
        delete next[pane.pane_id]
        changed = true
      }
    }
    seenPrompt.set(pane.pane_id, id)
  }
  for (const id of Object.keys(next)) {
    if (!alive.has(id)) {
      delete next[id]
      noteHold.delete(id)
      changed = true
    }
  }
  for (const id of seenPrompt.keys()) {
    if (!alive.has(id)) seenPrompt.delete(id)
  }
  for (const id of noteHold) {
    if (!alive.has(id)) noteHold.delete(id)
  }
  if (changed) approvalNotes.value = next
}

export function resetSendAck() {
  for (const waiter of waiters.values()) clearTimeout(waiter.timer)
  waiters.clear()
  seenPrompt.clear()
  noteHold.clear()
  approvalNotes.value = {}
}

function setApprovalNote(paneId: string, message: string) {
  if (!paneId) return
  noteHold.add(paneId)
  approvalNotes.value = { ...approvalNotes.value, [paneId]: message }
}

function clearApprovalNote(paneId: string) {
  noteHold.delete(paneId)
  if (!approvalNotes.value[paneId]) return
  const next = { ...approvalNotes.value }
  delete next[paneId]
  approvalNotes.value = next
}
