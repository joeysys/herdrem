import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ws } from '../api/ws'
import type { Todo, TodoBinding, TodoSet } from '../api/types'
import { useConnectionStore } from './connection'
import { usePendingOps } from './pendingOps'
import { useNotify } from '../composables/useNotify'

export type TodoOpKind = 'add' | 'set' | 'delete' | 'rebind'
export type SubmitResult = { ok: true; reqId?: string; echoed?: boolean } | { ok: false; reason: 'busy' | 'not connected' }

// Scopes — at most one pending op each. TodoPanel and a TodoManager scoped to
// the same workspace share the add scope because they write the same target.
export function bindingScopeKey(binding?: TodoBinding): string {
  return binding ? `${binding.host || 'local'} ${binding.workspace_id || 'default'}` : 'unbound'
}
export const addScope = (binding?: TodoBinding) => `todo:add:${bindingScopeKey(binding)}`
export const itemScope = (id: string) => `todo:item:${id}`

export const useTodoStore = defineStore('todo', () => {
  const todos = ref<Record<string, Todo>>({})

  const ops = usePendingOps({
    unconfirmedMessage: (op) =>
      op.kind === 'add'
        ? 'Unconfirmed — it may already be in the list. Check before adding again.'
        : 'Unconfirmed — check the item, then retry.',
    onUnconfirmed: (op) => useNotify().push({ message: op.message, type: 'info' }),
  })
  const pending = ops.pending

  function setAll(items: Todo[]) {
    todos.value = Object.fromEntries(items.map((item) => [item.id, item]))
  }

  function updateItem(item: Todo, deleted = false) {
    if (deleted) delete todos.value[item.id]
    else todos.value[item.id] = item
  }

  // Shared submit: busy scopes refuse, send failures refuse, and only a
  // negotiated req-echo parks the op in the pending table. Without the
  // feature the caller clears its draft immediately, like before.
  function submit(item: { id?: string; body: string; binding?: TodoBinding }, kind: TodoOpKind, scope: string, snapshot: string): SubmitResult {
    if (ops.isBusy(scope)) return { ok: false, reason: 'busy' }
    const reqId = useConnectionStore().hasFeature('req-echo') ? ops.newReqId() : undefined
    const msg: TodoSet = { type: 'todo_set', id: item.id, body: item.body, binding: item.binding }
    if (reqId) msg.req_id = reqId
    if (!ws.send(msg)) {
      ops.trackFailed(scope, kind, snapshot, 'not connected — the change was not sent')
      return { ok: false, reason: 'not connected' }
    }
    if (!reqId) return { ok: true, echoed: false }
    ops.track(scope, kind, snapshot, reqId)
    return { ok: true, reqId }
  }

  function setItem(item: Pick<Todo, 'body' | 'binding'> & { id?: string }): SubmitResult {
    return submit(item, 'set', itemScope(item.id ?? ''), item.body)
  }

  function deleteItem(id: string): SubmitResult {
    return submit({ id, body: '' }, 'delete', itemScope(id), '')
  }

  function rebindItem(item: Todo, binding: TodoBinding | undefined): SubmitResult {
    return submit({ id: item.id, body: item.body, binding }, 'rebind', itemScope(item.id), bindingScopeKey(binding))
  }

  function addItem(binding: TodoBinding | undefined, body: string): SubmitResult {
    return submit({ body, binding }, 'add', addScope(binding), body)
  }

  function hasItems(binding: TodoBinding): boolean {
    return Object.values(todos.value).some((item) => sameBinding(item.binding, binding))
  }

  function itemsFor(binding: TodoBinding): Todo[] {
    return Object.values(todos.value).filter((item) => sameBinding(item.binding, binding))
  }

  return {
    todos,
    pending,
    setAll,
    updateItem,
    setItem,
    deleteItem,
    rebindItem,
    addItem,
    hasItems,
    itemsFor,
    pendingFor: ops.forScope,
    confirm: ops.confirm,
    fail: ops.fail,
    ack: ops.ack,
    resetPending: ops.reset,
  }
})

export function sameBinding(a?: TodoBinding, b?: TodoBinding): boolean {
  return !!a && !!b && a.host === b.host && a.workspace_id === b.workspace_id
}
