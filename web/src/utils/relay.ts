// relay.ts — relay URL normalization and access/session helpers.
import { safeStorage } from './storage'

/** isLocalRelayHost matches the only host names exempt from relay tokens. */
export function isLocalRelayHost(hostname: string): boolean {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, '')
  return host === 'localhost' || host === '127.0.0.1' || host === '::1'
}

/** extractRelayCredentials normalizes a relay address and lifts `token` out of the query. */
export function extractRelayCredentials(raw: string): { url: string; token: string } {
  let u = (raw || '').trim()
  if (!u) return { url: '', token: '' }
  if (!/^wss?:\/\//i.test(u)) {
    u = u.replace(/^https?:\/\//i, '')
    const proto = location.protocol === 'https:' ? 'wss://' : 'ws://'
    u = proto + u
  }
  try {
    const parsed = new URL(u)
    const token = parsed.searchParams.get('token') || ''
    parsed.searchParams.delete('token')
    let path = parsed.pathname.replace(/\/+$/, '').replace(/\/ws$/i, '')
    if (!path || path === '/') path = ''
    const search = parsed.searchParams.toString()
    const url = `${parsed.protocol}//${parsed.host}${path}${search ? `?${search}` : ''}${parsed.hash}`
    return { url, token }
  } catch {
    u = u.replace(/\/+$/, '').replace(/\/ws$/i, '')
    return { url: u, token: '' }
  }
}

/** normalizeRelayUrl coerces a user-typed relay address into a ws(s):// URL. */
export function normalizeRelayUrl(raw: string): string {
  return extractRelayCredentials(raw).url
}

export function relayHttpBase(raw: string): string {
  return raw.replace(/^ws/i, 'http').replace(/\/ws\/?$/i, '').replace(/\/$/, '')
}

export function relayFileUrl(relayUrl: string, path: string, host = '', token = '', download = false): string {
  const params = new URLSearchParams({ path })
  if (host && host !== 'local') params.set('host', host)
  if (token) params.set('token', token)
  if (download) params.set('download', '1')
  return `${relayHttpBase(relayUrl)}/file?${params}`
}

/** relayNeedsToken requires auth unless the relay is a local same-origin target. */
export function relayNeedsToken(raw: string): boolean {
  try {
    const relay = new URL(normalizeRelayUrl(raw))
    const relayProtocol = relay.protocol === 'wss:' ? 'https:' : 'http:'
    return !isLocalRelayHost(relay.hostname) || `${relayProtocol}//${relay.host}` !== location.origin
  } catch {
    return true
  }
}

/** relayMatchesPageOrigin is true when the relay is the origin serving this PWA. */
export function relayMatchesPageOrigin(raw: string): boolean {
  try {
    const relay = new URL(normalizeRelayUrl(raw))
    const httpOrigin = `${relay.protocol === 'wss:' ? 'https:' : 'http:'}//${relay.host}`
    return httpOrigin === location.origin
  } catch {
    return false
  }
}

const SESSION_HINT_KEY = 'herdrem_session'

/** hasSessionCookieHint is a non-secret flag that a same-origin HttpOnly cookie was minted. */
export function hasSessionCookieHint(): boolean {
  return safeStorage.getItem(SESSION_HINT_KEY) === '1'
}

export function markSessionCookieHint(): void {
  safeStorage.setItem(SESSION_HINT_KEY, '1')
}

export function clearSessionCookieHint(): void {
  safeStorage.removeItem(SESSION_HINT_KEY)
}

/** canCookieReconnect allows a cookieless WS dial on this origin after a prior login. */
export function canCookieReconnect(raw: string): boolean {
  return relayMatchesPageOrigin(raw) && hasSessionCookieHint()
}

/** relayHostLabel returns a short host label for display.
 *  For domains, shows only the first subdomain level (e.g. "myapp.example.com" → "myapp").
 *  For IP:port or localhost:port, shows the full host:port. */
export function relayHostLabel(url: string): string {
  const host = normalizeRelayUrl(url).replace(/^wss?:\/\//i, '')
  // If it's an IP address or localhost, show as-is
  if (/^(\d+\.|localhost)/i.test(host)) return host
  // For domains, show only the first subdomain part
  const parts = host.split('.')
  if (parts.length > 2) return parts[0]
  return host
}
