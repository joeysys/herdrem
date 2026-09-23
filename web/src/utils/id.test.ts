import { afterEach, describe, expect, it } from 'vitest'
import { uid } from './id'

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

const realCrypto = globalThis.crypto

function stubCrypto(value: unknown) {
  Object.defineProperty(globalThis, 'crypto', { value, configurable: true, writable: true })
}

afterEach(() => {
  stubCrypto(realCrypto)
})

describe('uid', () => {
  it('returns a UUIDv4 via crypto.randomUUID when available', () => {
    expect(uid()).toMatch(UUID_RE)
    // jsdom may or may not expose randomUUID — either way the shape holds
  })

  it('falls back to getRandomValues on insecure origins (randomUUID missing)', () => {
    // a plain-HTTP context: getRandomValues exists, randomUUID does not
    stubCrypto({
      getRandomValues(arr: Uint8Array) {
        arr.fill(0xab)
        return arr
      },
    })
    const id = uid()
    expect(id).toMatch(UUID_RE)
    expect(id).toBe('abababab-abab-4bab-abab-abababababab')
  })

  it('still produces a unique id when no crypto API exists at all', () => {
    stubCrypto(undefined)
    const a = uid()
    const b = uid()
    expect(a).toMatch(/^id-/)
    expect(a).not.toBe(b)
  })
})
