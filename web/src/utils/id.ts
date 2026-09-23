// id.ts — unique id helper with an insecure-context fallback.
// crypto.randomUUID is gated behind secure contexts (HTTPS / localhost), so a
// phone opening the UI over plain http://<lan-ip> gets "crypto.randomUUID is
// not a function". crypto.getRandomValues predates that gate and stays
// available on insecure origins — same crypto strength, formatted as UUIDv4.
export function uid(): string {
  const c = globalThis.crypto
  if (typeof c?.randomUUID === 'function') return c.randomUUID()
  if (typeof c?.getRandomValues === 'function') {
    const b = c.getRandomValues(new Uint8Array(16))
    b[6] = (b[6] & 0x0f) | 0x40 // version 4
    b[8] = (b[8] & 0x3f) | 0x80 // variant 10
    const h = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
    return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
  }
  return `id-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}
