import { describe, expect, it } from 'vitest'
import {
  extractRelayCredentials,
  isLocalRelayHost,
  normalizeRelayUrl,
  relayFileUrl,
  relayHttpBase,
  relayMatchesPageOrigin,
  relayNeedsToken,
} from './relay'

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

describe('extractRelayCredentials', () => {
  it('lifts a token query out of the URL and keeps other params', () => {
    const creds = extractRelayCredentials('wss://relay.example/ws?token=super-secret&keep=1')
    expect(creds.token).toBe('super-secret')
    expect(creds.url).toBe('wss://relay.example?keep=1')
    expect(normalizeRelayUrl('wss://relay.example/ws?token=super-secret&keep=1')).toBe('wss://relay.example?keep=1')
  })

  it('returns an empty token when the URL has no credential query', () => {
    expect(extractRelayCredentials('ws://192.168.1.10:8766')).toEqual({
      url: 'ws://192.168.1.10:8766',
      token: '',
    })
  })
})

describe('relay file URL helpers', () => {
  it('normalizes the relay base and encodes one file URL consistently', () => {
    expect(relayHttpBase('ws://relay.example/ws/')).toBe('http://relay.example')
    expect(relayFileUrl('wss://relay.example/ws', '/a b.txt', 'remote', true)).toBe(
      'https://relay.example/file?path=%2Fa+b.txt&host=remote&download=1',
    )
  })

  it('omits local host and credentials when building a public file URL', () => {
    const url = relayFileUrl('ws://localhost:8766/ws', '/a.txt', 'local')
    expect(url).toBe('http://localhost:8766/file?path=%2Fa.txt')
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

describe('relayMatchesPageOrigin', () => {
  it('matches the page origin after mapping ws to http', () => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    expect(relayMatchesPageOrigin(`${proto}//${location.host}`)).toBe(true)
    expect(relayMatchesPageOrigin('ws://192.168.1.10:8766')).toBe(false)
  })
})
