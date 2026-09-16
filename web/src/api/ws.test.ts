import { describe, expect, it } from 'vitest'
import { wsAuthSubprotocol } from './ws'

// The token is carried as a WebSocket subprotocol (`herdrem.<base64url>`)
// rather than ?token=, so the value must be tchar-safe and round-trip
// through the server's base64url decode for arbitrary user tokens.
describe('wsAuthSubprotocol', () => {
  it('encodes a simple token', () => {
    expect(wsAuthSubprotocol('secret')).toBe('herdrem.c2VjcmV0')
  })

  it('produces a tchar-safe subprotocol for tokens with specials', () => {
    const proto = wsAuthSubprotocol('s3cret token/with=specials?')
    expect(proto.startsWith('herdrem.')).toBe(true)
    // RFC 7230 tchar only — no space, '=', '/', '?' may appear.
    expect(proto).toMatch(/^herdrem\.[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/)
  })

  it('round-trips UTF-8 tokens', () => {
    const token = 'トークン-🔑-token'
    const b64 = wsAuthSubprotocol(token).slice('herdrem.'.length)
    const bin = atob(b64.replace(/-/g, '+').replace(/_/g, '/'))
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
    expect(new TextDecoder().decode(bytes)).toBe(token)
  })
})
