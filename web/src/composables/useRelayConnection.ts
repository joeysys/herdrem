// useRelayConnection.ts — switch/connect relays with proper state cleanup.
import { watch } from 'vue'
import { useConnectionStore, useAgentsStore } from '../stores/connection'
import { useRelaysStore, type RelayEntry } from '../stores/relays'
import { useTerminalStore } from '../stores/terminal'
import { useTunnelsStore } from '../stores/tunnels'
import { useTodoStore } from '../stores/todo'
import { usePreviewsStore } from '../stores/preview'
import { useFilesStore } from '../stores/files'
import { useDiffStore } from '../stores/diff'
import { relayNeedsToken } from '../utils/relay'
import { safeStorage } from '../utils/storage'

export function useRelayConnection() {
  const conn = useConnectionStore()
  const relays = useRelaysStore()
  const agents = useAgentsStore()
  const term = useTerminalStore()
  const tunnels = useTunnelsStore()
  const todo = useTodoStore()
  const previews = usePreviewsStore()
  const files = useFilesStore()
  const diff = useDiffStore()

  /** connect to a relay entry, clearing pane/agent/tunnel state to avoid cross-talk */
  function connectRelay(r: RelayEntry) {
    if (!r.token && relayNeedsToken(r.url)) return
    relays.setCurrent(r.id)
    term.closePane()
    term.clearAllCache()
    agents.setAgents([])
    agents.resetSnapshotKey()
    todo.setAll([])
    tunnels.clear()
    files.reset()
    diff.reset()
    previews.clear()
    const stop = watch(() => conn.status, (s) => {
      if (s === 'connected') {
        tunnels.fetchTunnels()
        stop()
      }
    })
    conn.connect(r.url, r.token ?? '')
  }

  /** switch to a relay by id */
  function switchTo(id: string) {
    const r = relays.relays.find((x) => x.id === id)
    if (r) connectRelay(r)
  }

  /**
   * initial boot: connect to the current relay if
   * one is set, otherwise default to same-origin /ws.
   */
  function boot(token = '') {
    safeStorage.removeItem('herdrem_token')
    const cur = relays.current()
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const sameOrigin = `${proto}//${location.host}`
    if (cur) {
      const targetToken = cur.token || token
      if (!targetToken && relayNeedsToken(cur.url)) return
      if (token && relayNeedsToken(cur.url) && !cur.token) {
        relays.update(cur.id, { token })
      }
      conn.connect(cur.url, targetToken)
    } else {
      // No saved relay — always default to same-origin /ws
      if (!token && relayNeedsToken(sameOrigin)) return
      if (token && relayNeedsToken(sameOrigin)) {
        const entry = relays.upsert({ name: 'default', url: sameOrigin, token })
        relays.setCurrent(entry.id)
      }
      conn.connect(sameOrigin, token)
    }
  }

  return { connectRelay, switchTo, boot }
}
