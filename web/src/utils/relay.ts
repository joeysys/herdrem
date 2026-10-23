// relay.ts — relay URL normalization and access/session helpers.

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

export function relayFileUrl(relayUrl: string, path: string, host = '', download = false): string {
  const params = new URLSearchParams({ path })
  if (host && host !== 'local') params.set('host', host)
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

// Session tokens: relay tokens kept in sessionStorage keyed by relay URL.
// Unlike memory-only storage this survives a page reload — including the
// service-restart case where the HttpOnly session cookie is already dead —
// and unlike localStorage it dies with the tab instead of persisting
// long-term. Never write these to localStorage: the goal is a credential
// that outlives a refresh, not one that outlives the user.
const SESSION_TOKENS_KEY = 'herdrem_tokens'

function readSessionTokens(): Record<string, string> {
  try {
    if (typeof sessionStorage === 'undefined') return {}
    const raw = sessionStorage.getItem(SESSION_TOKENS_KEY)
    return raw ? (JSON.parse(raw) as Record<string, string>) : {}
  } catch {
    return {}
  }
}

function writeSessionTokens(all: Record<string, string>): void {
  try {
    if (typeof sessionStorage === 'undefined') return
    sessionStorage.setItem(SESSION_TOKENS_KEY, JSON.stringify(all))
  } catch {
    /* sessionStorage blocked — token stays memory-only for this page */
  }
}

/** sessionTokenFor returns the session-stored relay token for a URL, or ''. */
export function sessionTokenFor(url: string): string {
  return readSessionTokens()[url] ?? ''
}

/** saveSessionToken remembers a relay token for this tab session. */
export function saveSessionToken(url: string, token: string): void {
  if (!url || !token) return
  const all = readSessionTokens()
  all[url] = token
  writeSessionTokens(all)
}

/** dropSessionToken forgets a session-stored relay token (e.g. it 401'd). */
export function dropSessionToken(url: string): void {
  const all = readSessionTokens()
  if (!(url in all)) return
  delete all[url]
  writeSessionTokens(all)
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
