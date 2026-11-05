import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ws } from '../api/ws'
import type { TunnelInfo, TunnelCreate, TunnelClose, TunnelsList } from '../api/types'
import { useConnectionStore } from './connection'
import { usePendingOps } from './pendingOps'
import { useNotify } from '../composables/useNotify'

export type SubmitResult = { ok: true; reqId?: string; echoed?: boolean } | { ok: false; reason: 'busy' | 'not connected' }

export const portScope = 'tunnel:port'
export const fileScope = (host: string, path: string) => `tunnel:file:${host}:${path}`

export const useTunnelsStore = defineStore('tunnels', () => {
  const tunnels = ref<TunnelInfo[]>([])
  const showPanel = ref(false)
  const previewUrl = ref<string | null>(null)
  const previewTitle = ref<string | null>(null)

  const activeTunnels = computed(() => tunnels.value.filter((t) => t.status === 'active' || t.status === 'starting'))
  const activeCount = computed(() => activeTunnels.value.length)

  function setTunnels(list: TunnelInfo[]) {
    tunnels.value = list
  }

  function updateTunnel(item: TunnelInfo) {
    const idx = tunnels.value.findIndex((t) => t.id === item.id)
    if (idx >= 0) {
      if (item.status === 'closed') {
        tunnels.value.splice(idx, 1)
      } else {
        tunnels.value[idx] = item
      }
    } else if (item.status !== 'closed') {
      tunnels.value.push(item)
    }
  }

  function fetchTunnels() {
    const msg: TunnelsList = { type: 'tunnels_list' }
    ws.send(msg)
  }

  const ops = usePendingOps({
    unconfirmedMessage: () => 'Unconfirmed — a tunnel may already be starting. Check the list before retrying.',
    onUnconfirmed: (op) => useNotify().push({ message: op.message, type: 'info' }),
  })
  const pending = ops.pending

  // Same submit rules as todo.ts: busy scopes refuse, send failures refuse,
  // and only a negotiated req-echo parks the op. Without the feature a
  // successful send clears the caller's PIN immediately, like before.
  function submitCreate(msg: Omit<TunnelCreate, 'type' | 'req_id'>, scope: string, snapshot: string): SubmitResult {
    if (ops.isBusy(scope)) return { ok: false, reason: 'busy' }
    const reqId = useConnectionStore().hasFeature('req-echo') ? ops.newReqId() : undefined
    const full: TunnelCreate = { type: 'tunnel_create', ...msg }
    if (reqId) full.req_id = reqId
    if (!ws.send(full)) {
      ops.trackFailed(scope, 'tunnel', snapshot, 'not connected — the tunnel request was not sent')
      return { ok: false, reason: 'not connected' }
    }
    if (!reqId) return { ok: true, echoed: false }
    ops.track(scope, 'tunnel', snapshot, reqId)
    return { ok: true, reqId }
  }

  function createTunnel(port: number, authMode: 'token_gate' | 'basic_auth' | 'direct' = 'token_gate', pin?: string): SubmitResult {
    return submitCreate({ port, auth_mode: authMode, pin }, portScope, pin ?? '')
  }

  const lastCreateError = ref<{ message: string; timestamp: number } | null>(null)

  function handleCreateError(message: string) {
    lastCreateError.value = { message, timestamp: Date.now() }
  }

  function dismissTunnel(id: string) {
    const idx = tunnels.value.findIndex((t) => t.id === id)
    if (idx >= 0) {
      tunnels.value.splice(idx, 1)
    }
  }

  function createFileTunnel(filePath: string, host = 'local', authMode: 'token_gate' | 'basic_auth' | 'direct' = 'token_gate', pin?: string): SubmitResult {
    const scope = fileScope(host, filePath)
    if (ops.isBusy(scope)) return { ok: false, reason: 'busy' }
    // Purge any existing error entry for this file so retry starts clean
    tunnels.value = tunnels.value.filter(
      (t) => !(t.file_path === filePath && (t.host || 'local') === host && t.status === 'error')
    )
    lastCreateError.value = null
    return submitCreate({ file_path: filePath, host, auth_mode: authMode, pin }, scope, pin ?? '')
  }

  function getFileTunnel(filePath: string, host = 'local') {
    const matches = tunnels.value.filter(
      (t) => t.file_path === filePath && (t.host || 'local') === host && t.status !== 'closed'
    )
    if (matches.length === 0) return undefined
    // Prefer active/starting tunnel over older error entries
    const live = matches.find((t) => t.status === 'active' || t.status === 'starting')
    if (live) return live
    // Otherwise return latest entry
    return matches[matches.length - 1]
  }

  function closeTunnel(id: string) {
    dismissTunnel(id)
    const msg: TunnelClose = {
      type: 'tunnel_close',
      id,
    }
    ws.send(msg)
  }

  function togglePanel() {
    showPanel.value = !showPanel.value
    if (showPanel.value) {
      fetchTunnels()
    }
  }

  function openPreview(url: string, title?: string) {
    previewUrl.value = url
    previewTitle.value = title || 'Dev Server Preview'
  }

  function closePreview() {
    previewUrl.value = null
    previewTitle.value = null
  }

  /** Clear all tunnel state — called when switching relays to avoid stale data. */
  function clear() {
    tunnels.value = []
    previewUrl.value = null
    previewTitle.value = null
    showPanel.value = false
  }

  return {
    tunnels,
    showPanel,
    previewUrl,
    previewTitle,
    activeTunnels,
    activeCount,
    setTunnels,
    updateTunnel,
    fetchTunnels,
    createTunnel,
    createFileTunnel,
    getFileTunnel,
    closeTunnel,
    dismissTunnel,
    lastCreateError,
    pending,
    pendingFor: ops.forScope,
    confirm: ops.confirm,
    fail: ops.fail,
    ack: ops.ack,
    resetPending: ops.reset,
    handleCreateError,
    togglePanel,
    openPreview,
    closePreview,
    clear,
  }
})
