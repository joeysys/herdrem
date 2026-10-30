import { ref } from 'vue'
import type { ServerMessage, WorktreeEntry } from '../api/types'
import { useConnectionStore } from '../stores/connection'
import { uid } from './id'

const sessionOps = new Set([
  'session_rename',
  'workspace_move',
  'session_close',
  'worktree_list',
  'worktree_create',
  'worktree_open',
  'worktree_remove',
])

export const sessionError = ref<{ op: string; message: string; key?: string } | null>(null)
export const sessionOK = ref<{ op: string; id?: string } | null>(null)
export const worktreeLists = ref<Record<string, WorktreeEntry[]>>({})

/** Fields for one click. Empty until hello has advertised boot_id, so an old
 *  relay keeps the previous request shape. A new click mints a new id. */
export function operationFields(): { operation_id?: string; boot_id?: string } {
  const boot = useConnectionStore().bootId
  if (!boot) return {}
  return { operation_id: uid(), boot_id: boot }
}

/** Default workspace name for a new worktree: the current space plus a short suffix. */
export function defaultWorktreeName(space: string, suffix = worktreeNameSuffix()): string {
  const tail = suffix.trim() || '0000'
  const base = (space.trim() || 'worktree').slice(0, Math.max(1, 200 - tail.length - 1))
  return `${base}-${tail}`
}

function worktreeNameSuffix(): string {
  const bytes = new Uint8Array(2)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/** herdr insert_index is the position in the list before the item is removed.
 *  Moving an item down has to send the index after the target: herdr subtracts
 *  one when the source index is lower than insert_index. The relay forwards
 *  the number unchanged. */
export function insertIndex(from: number, to: number): number {
  return from < to ? to + 1 : to
}

export function applySessionMessage(msg: ServerMessage) {
  if (msg.type === 'worktrees') {
    worktreeLists.value = { ...worktreeLists.value, [msg.workspace_id]: msg.entries || [] }
    return
  }
  if (msg.type === 'session_ok') {
    sessionOK.value = { op: msg.op, id: msg.id }
    return
  }
  if (msg.type === 'worktree_ready') {
    sessionOK.value = { op: msg.op, id: msg.workspace_id }
    return
  }
  if (msg.type === 'error' && sessionOps.has(msg.op)) {
    sessionError.value = { op: msg.op, message: msg.message, key: msg.key }
  }
}

export function resetSessionState() {
  sessionError.value = null
  sessionOK.value = null
  worktreeLists.value = {}
}
