import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ws } from '../api/ws'
import type { TunnelInfo, TunnelCreate, TunnelClose, TunnelsList } from '../api/types'

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

  function createTunnel(port: number, authMode: 'token_gate' | 'basic_auth' | 'direct' = 'token_gate', pin?: string) {
    const msg: TunnelCreate = {
      type: 'tunnel_create',
      port,
      auth_mode: authMode,
      pin,
    }
    ws.send(msg)
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

  function createFileTunnel(filePath: string, host = 'local', authMode: 'token_gate' | 'basic_auth' | 'direct' = 'token_gate', pin?: string) {
    // Purge any existing error entry for this file so retry starts clean
    tunnels.value = tunnels.value.filter(
      (t) => !(t.file_path === filePath && (t.host || 'local') === host && t.status === 'error')
    )
    lastCreateError.value = null
    const msg: TunnelCreate = {
      type: 'tunnel_create',
      file_path: filePath,
      host,
      auth_mode: authMode,
      pin,
    }
    ws.send(msg)
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
    handleCreateError,
    togglePanel,
    openPreview,
    closePreview,
    clear,
  }
})
