import { ref } from 'vue'

// Mutation receipts for req-echo (todo_set / tunnel_create). A sent op waits
// for the echo carrying its req_id; 10s without an echo — or a socket drop
// first — marks it unconfirmed. Unconfirmed means "may have landed", never
// "just retry", which is why each store supplies its own message text.

export type PendingOutcome = 'pending' | 'confirmed' | 'failed' | 'unconfirmed'

export interface PendingOp {
  reqId: string
  scope: string // caller-chosen key a component subscribes to
  kind: string // 'add' | 'set' | 'delete' | 'rebind' | 'tunnel'
  snapshot: string // submitted body / pin / binding key — not the input box
  outcome: PendingOutcome
  message: string // why, for failed / unconfirmed
}

export const PENDING_ECHO_MS = 10_000

const disconnectListeners = new Set<() => void>()

// Registered once per store instance; the connection layer fires it on a
// socket drop, alongside sendAck.noteDisconnect.
export function onPendingDisconnect(fn: () => void): () => void {
  disconnectListeners.add(fn)
  return () => disconnectListeners.delete(fn)
}

export function notePendingDisconnect() {
  for (const fn of disconnectListeners) fn()
}

export interface PendingOpsOptions {
  unconfirmedMessage: (op: PendingOp) => string
  onUnconfirmed?: (op: PendingOp) => void
  timeoutMs?: number
}

export function usePendingOps(opts: PendingOpsOptions) {
  const pending = ref<Record<string, PendingOp>>({})
  const timers = new Map<string, ReturnType<typeof setTimeout>>()
  let seq = 0

  function newReqId(): string {
    return `req-${Date.now().toString(36)}-${++seq}`
  }

  function forScope(scope: string): PendingOp | undefined {
    return pending.value[scope]
  }

  function isBusy(scope: string): boolean {
    return pending.value[scope]?.outcome === 'pending'
  }

  function setOutcome(scope: string, outcome: PendingOutcome, message = '') {
    const cur = pending.value[scope]
    if (!cur) return
    pending.value = { ...pending.value, [scope]: { ...cur, outcome, message } }
  }

  function unconfirm(scope: string, reqId: string) {
    const cur = pending.value[scope]
    if (cur?.reqId !== reqId || cur.outcome !== 'pending') return
    setOutcome(scope, 'unconfirmed', opts.unconfirmedMessage(cur))
    if (cur) opts.onUnconfirmed?.(pending.value[scope])
  }

  /** Record a successfully sent op and start its echo timer. */
  function track(scope: string, kind: string, snapshot: string, reqId: string): PendingOp {
    const op: PendingOp = { reqId, scope, kind, snapshot, outcome: 'pending', message: '' }
    pending.value = { ...pending.value, [scope]: op }
    timers.set(
      reqId,
      setTimeout(() => {
        timers.delete(reqId)
        unconfirm(scope, reqId)
      }, opts.timeoutMs ?? PENDING_ECHO_MS),
    )
    return op
  }

  /** Record an op that never reached the wire, so the form can show why. */
  function trackFailed(scope: string, kind: string, snapshot: string, message: string): PendingOp {
    const op: PendingOp = {
      reqId: `local-${Date.now().toString(36)}-${++seq}`,
      scope,
      kind,
      snapshot,
      outcome: 'failed',
      message,
    }
    pending.value = { ...pending.value, [scope]: op }
    return op
  }

  function clearTimer(reqId: string) {
    const t = timers.get(reqId)
    if (t) {
      clearTimeout(t)
      timers.delete(reqId)
    }
  }

  /** An echo carrying req_id confirmed the op — including one that already
   * timed out into unconfirmed, so a late echo still lands. Terminal states
   * (confirmed, failed) never move. Unknown/absent id: no-op. */
  function confirm(reqId?: string): PendingOp | undefined {
    if (!reqId) return undefined
    const op = Object.values(pending.value).find((o) => o.reqId === reqId)
    if (!op || op.outcome === 'confirmed' || op.outcome === 'failed') return undefined
    clearTimer(reqId)
    setOutcome(op.scope, 'confirmed')
    return pending.value[op.scope]
  }

  /** The server rejected an op — only a matching req_id may fail it. A late
   * error may upgrade an unconfirmed op; confirmed/failed never move. */
  function fail(reqId: string | undefined, message: string): PendingOp | undefined {
    if (!reqId) return undefined
    const op = Object.values(pending.value).find((o) => o.reqId === reqId)
    if (!op || op.outcome === 'confirmed' || op.outcome === 'failed') return undefined
    clearTimer(reqId)
    setOutcome(op.scope, 'failed', message)
    return pending.value[op.scope]
  }

  /** A socket drop before the echo: every pending op becomes unconfirmed. */
  function noteDisconnect() {
    for (const op of Object.values(pending.value)) {
      if (op.outcome !== 'pending') continue
      clearTimer(op.reqId)
      unconfirm(op.scope, op.reqId)
    }
  }

  /** Drop a finished entry once the component has consumed its outcome. */
  function ack(scope: string) {
    const cur = pending.value[scope]
    if (!cur || cur.outcome === 'pending') return
    clearTimer(cur.reqId)
    const { [scope]: _dropped, ...rest } = pending.value
    pending.value = rest
  }

  const unregister = onPendingDisconnect(noteDisconnect)

  function reset() {
    for (const t of timers.values()) clearTimeout(t)
    timers.clear()
    pending.value = {}
    unregister()
  }

  return { pending, newReqId, forScope, isBusy, track, trackFailed, confirm, fail, noteDisconnect, ack, reset }
}
