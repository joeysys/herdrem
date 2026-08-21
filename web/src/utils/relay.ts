// relay.ts — relay URL normalization and access/session helpers.

/** normalizeRelayUrl coerces a user-typed relay address into a ws(s):// URL. */
export function normalizeRelayUrl(raw: string): string {
  let u = (raw || '').trim()
  if (!u) return u
  // add scheme based on page protocol if missing
  if (!/^wss?:\/\//i.test(u)) {
    u = u.replace(/^https?:\/\//i, '')
    const proto = location.protocol === 'https:' ? 'wss://' : 'ws://'
    u = proto + u
  }
  // strip trailing slashes and a trailing /ws path
  u = u.replace(/\/+$/, '')
  u = u.replace(/\/ws$/i, '')
  return u
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
