import { describe, expect, it } from 'vitest'
import { isLocalRelayHost, relayNeedsToken } from './relay'

describe('isLocalRelayHost', () => {
  it('matches only the token-free host names', () => {
    expect(isLocalRelayHost('localhost')).toBe(true)
    expect(isLocalRelayHost('LOCALHOST')).toBe(true)
    expect(isLocalRelayHost('127.0.0.1')).toBe(true)
    expect(isLocalRelayHost('::1')).toBe(true)
    expect(isLocalRelayHost('[::1]')).toBe(true)
    expect(isLocalRelayHost('192.168.1.10')).toBe(false)
  })
})

describe('relayNeedsToken', () => {
  it('exempts only a local relay with the page origin', () => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    expect(relayNeedsToken(`${proto}//${location.host}`)).toBe(false)
    expect(relayNeedsToken(`${proto}//127.0.0.1:8766`)).toBe(true)
    expect(relayNeedsToken(`${proto}//192.168.1.10:8766`)).toBe(true)
  })
})
