import type { ServerMessage, ClientMessage } from './types'

type MessageHandler = (msg: ServerMessage) => void

// The relay token rides the WebSocket handshake as a subprotocol —
// `herdrem.<base64url(token)>` — instead of a ?token= query, so the
// long-term credential stays out of the URL, browser history, and proxy
// logs. base64url (unpadded) is required because subprotocol values must be
// HTTP tchar-safe; the server decodes the same form in auth.Check.
export function wsAuthSubprotocol(token: string): string {
  const bytes = new TextEncoder().encode(token)
  let bin = ''
  bytes.forEach((b) => { bin += String.fromCharCode(b) })
  const b64 = btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  return `herdrem.${b64}`
}

export class WSClient {
  private ws: WebSocket | null = null
  private url = ''
  private token = ''
  private handlers: Set<MessageHandler> = new Set()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private shouldReconnect = true
  private connId = 0 // increments on each connect; callbacks check identity
  public status: 'disconnected' | 'connecting' | 'connected' = 'disconnected'
  /** reconnect delay in ms; set from prefs timing.reconnect_delay_ms */
  public reconnectDelayMs = 2000

  connect(url: string, token?: string) {
    // Close existing connection and clear pending reconnect
    this.shouldReconnect = false
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.ws) {
      this.ws.onclose = null
      this.ws.onerror = null
      this.ws.onmessage = null
      this.ws.onopen = null
      this.ws.close()
      this.ws = null
    }
    this.url = url
    this.token = token || ''
    this.shouldReconnect = true
    this.doConnect()
  }

  private doConnect() {
    this.status = 'connecting'
    this.notifyStatus()
    const myId = ++this.connId
    // Relay's WS endpoint is /ws (root serves the SPA). normalizeRelayUrl
    // strips /ws for storage/display, so re-append it here if missing.
    let base = this.url
    if (!/\/ws$/i.test(base)) base = base.replace(/\/+$/, '') + '/ws'
    const socket = this.token ? new WebSocket(base, [wsAuthSubprotocol(this.token)]) : new WebSocket(base)
    this.ws = socket

    socket.onopen = () => {
      if (myId !== this.connId) return // stale connection
      this.status = 'connected'
      this.notifyStatus()
    }

    socket.onmessage = (ev) => {
      if (myId !== this.connId) return // stale connection
      try {
        const msg = JSON.parse(ev.data) as ServerMessage
        this.handlers.forEach((h) => h(msg))
      } catch (e) {
        console.error('ws parse error:', e)
      }
    }

    socket.onclose = () => {
      if (myId !== this.connId) return // stale connection, ignore
      this.status = 'disconnected'
      this.notifyStatus()
      if (this.shouldReconnect) {
        this.reconnectTimer = setTimeout(() => this.doConnect(), this.reconnectDelayMs)
      }
    }

    socket.onerror = () => {
      if (myId !== this.connId) return
      socket.close()
    }
  }

  disconnect() {
    this.shouldReconnect = false
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.ws?.close()
    this.ws = null
    this.status = 'disconnected'
    this.notifyStatus()
  }

  setReconnectDelay(ms: number) {
    if (ms > 0) this.reconnectDelayMs = ms
  }

  send(msg: ClientMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg))
      return true
    }
    return false
  }

  onMessage(handler: MessageHandler): () => void {
    this.handlers.add(handler)
    return () => this.handlers.delete(handler)
  }

  private statusHandlers: Set<(s: string) => void> = new Set()
  onStatus(handler: (s: string) => void): () => void {
    this.statusHandlers.add(handler)
    handler(this.status)
    return () => this.statusHandlers.delete(handler)
  }

  private notifyStatus() {
    this.statusHandlers.forEach((h) => h(this.status))
  }
}

export const ws = new WSClient()
