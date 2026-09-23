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
import { canCookieReconnect, relayNeedsToken, sessionTokenFor } from '../utils/relay'
import { safeStorage } from '../utils/storage'

function canConnect(url: string, token: string): boolean {
  // A token parked in sessionStorage for this URL is a usable credential —
  // checking only entry.token/args would strand cross-origin relays on the
  // boot splash (their metadata never carries a token on disk).
  if (token || sessionTokenFor(url) || !relayNeedsToken(url)) return true
  return canCookieReconnect(url)
}

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

  function resetSessionState() {
    term.closePane()
    term.clearAllCache()
    agents.setAgents([])
    agents.resetSnapshotKey()
    todo.setAll([])
    tunnels.clear()
    files.reset()
    diff.reset()
    previews.clear()
  }

  /** connect to a relay entry, clearing pane/agent/tunnel state to avoid cross-talk */
  function connectRelay(r: RelayEntry): boolean {
    relays.setCurrent(r.id)
    resetSessionState()
    if (!canConnect(r.url, r.token || '')) {
      conn.prepareAuth(r.url)
      return false
    }
    const stop = watch(() => conn.status, (s) => {
      if (s === 'connected') {
        tunnels.fetchTunnels()
        stop()
      }
    })
    conn.connect(r.url, r.token ?? '')
    return true
  }

  /** switch to a relay by id */
  function switchTo(id: string) {
    const r = relays.relays.find((x) => x.id === id)
    if (r) connectRelay(r)
  }

  /**
   * initial boot: connect to the current relay if
   * one is set, otherwise default to same-origin /ws.
   * Tokens stay in memory. Same-origin reload reconnects via the HttpOnly
   * session cookie (no query token) when a prior login left a session hint.
   */
  function boot(token = '') {
    safeStorage.removeItem('herdrem_token')
    const cur = relays.current()
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const sameOrigin = `${proto}//${location.host}`
    if (cur) {
      const targetToken = token || cur.token || ''
      if (!canConnect(cur.url, targetToken)) {
        conn.prepareAuth(cur.url)
        return
      }
      // Keep the token on the in-memory entry for this tab; persistRelays strips disk.
      if (token) relays.update(cur.id, { token })
      conn.connect(cur.url, targetToken)
      return
    }
    if (!canConnect(sameOrigin, token)) return
    conn.connect(sameOrigin, token)
  }

  return { connectRelay, switchTo, boot }
}
