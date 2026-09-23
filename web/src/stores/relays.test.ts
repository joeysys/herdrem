import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useRelaysStore } from './relays'
import { safeStorage } from '../utils/storage'
import { saveSessionToken, sessionTokenFor } from '../utils/relay'

const STORAGE_KEY = 'herdrem_relays'

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
  safeStorage.removeItem(STORAGE_KEY)
  setActivePinia(createPinia())
})

describe('useRelaysStore credentials', () => {
  it('seeds a same-origin default entry when the list is empty', () => {
    const relays = useRelaysStore()
    expect(relays.relays).toHaveLength(1)
    expect(relays.relays[0].name).toBe('this device')
    expect(relays.relays[0].url).toBe(`ws://${location.host}`)
    expect(relays.currentId).toBe(relays.relays[0].id)
    // The seeded entry persists like any relay — editable, listed, current.
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]') as Array<{ name: string }>
    expect(stored).toHaveLength(1)
  })

  it('does not seed a default entry when relays already exist', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify([
      { id: 'r1', name: 'old', url: 'ws://old.example' },
    ]))
    const relays = useRelaysStore()
    expect(relays.relays).toHaveLength(1)
    expect(relays.relays[0].name).toBe('old')
  })

  it('persists relay metadata without tokens', () => {
    const relays = useRelaysStore()
    relays.add('prod', 'wss://relay.example', 'super-secret')
    const raw = localStorage.getItem(STORAGE_KEY) || ''
    expect(raw).not.toContain('super-secret')
    const stored = JSON.parse(raw) as Array<{ token?: string; name: string }>
    const prod = stored.find((r) => r.name === 'prod')
    expect(prod).toBeTruthy()
    expect(prod!.token).toBeUndefined()
    expect(relays.relays.find((r) => r.name === 'prod')?.token).toBe('super-secret')
    expect(sessionTokenFor('wss://relay.example')).toBe('super-secret')
  })

  it('restores a settings token from sessionStorage after a page reload', () => {
    const relays = useRelaysStore()
    const entry = relays.add('prod', 'wss://relay.example', 'saved-token')
    setActivePinia(createPinia())
    const restored = useRelaysStore()
    expect(restored.relays.find((relay) => relay.id === entry.id)?.token).toBe('saved-token')
  })

  it('lifts a token query into memory and never persists it in the URL', () => {
    const relays = useRelaysStore()
    relays.add('prod', 'wss://relay.example/ws?token=super-secret&keep=1')
    const prod = relays.relays.find((r) => r.name === 'prod')
    expect(prod?.url).toBe('wss://relay.example?keep=1')
    expect(prod?.token).toBe('super-secret')
    const raw = localStorage.getItem(STORAGE_KEY) || ''
    expect(raw).not.toContain('super-secret')
    const stored = JSON.parse(raw) as Array<{ url: string; token?: string; name: string }>
    const storedProd = stored.find((r) => r.name === 'prod')
    expect(storedProd?.url).toBe('wss://relay.example?keep=1')
    expect(storedProd?.token).toBeUndefined()
  })

  it('drops the session-stored token when its relay is removed', () => {
    const relays = useRelaysStore()
    const relay = relays.add('prod', 'wss://relay.example')
    saveSessionToken('wss://relay.example', 'sess-token')
    expect(sessionTokenFor('wss://relay.example')).toBe('sess-token')
    relays.remove(relay.id)
    expect(sessionTokenFor('wss://relay.example')).toBe('')
  })

  it('drops the old-URL session token when a relay URL changes', () => {
    const relays = useRelaysStore()
    const relay = relays.add('prod', 'wss://old.example')
    saveSessionToken('wss://old.example', 'sess-token')
    relays.update(relay.id, { url: 'wss://new.example' })
    expect(sessionTokenFor('wss://old.example')).toBe('')
  })

  it('adopts the new URL\'s own session credential instead of migrating the old token', () => {
    const relays = useRelaysStore()
    const relay = relays.add('prod', 'wss://old.example', 'old-token')
    saveSessionToken('wss://new.example', 'new-token')
    relays.update(relay.id, { url: 'wss://new.example' })
    const updated = relays.relays.find((r) => r.id === relay.id)
    // no token in the patch + URL changed → new URL's credential, never the old one
    expect(updated?.token).toBe('new-token')
    expect(sessionTokenFor('wss://new.example')).toBe('new-token')
    expect(sessionTokenFor('wss://old.example')).toBe('')
  })

  it('drops the entry token when the new URL has no credential of its own', () => {
    const relays = useRelaysStore()
    const relay = relays.add('prod', 'wss://old.example', 'old-token')
    relays.update(relay.id, { url: 'wss://new.example' })
    const updated = relays.relays.find((r) => r.id === relay.id)
    expect(updated?.token).toBeUndefined()
    expect(sessionTokenFor('wss://new.example')).toBe('')
  })

  it('keeps the session token while another entry still uses the URL', () => {
    const relays = useRelaysStore()
    const a = relays.add('a', 'wss://relay.example')
    relays.add('b', 'wss://relay.example')
    saveSessionToken('wss://relay.example', 'sess-token')
    relays.remove(a.id)
    expect(sessionTokenFor('wss://relay.example')).toBe('sess-token')
  })

  it('strips a token query from previously persisted relay URLs on load', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify([
      { id: 'r2', name: 'old', url: 'ws://old.example/ws?token=url-secret' },
    ]))
    const relays = useRelaysStore()
    expect(relays.relays[0].token).toBe('url-secret')
    expect(relays.relays[0].url).toBe('ws://old.example')
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]') as Array<{ url: string; token?: string }>
    expect(stored[0].url).toBe('ws://old.example')
    expect(JSON.stringify(stored)).not.toContain('url-secret')
  })

  it('strips tokens from a previously persisted list on load', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify([
      { id: 'r1', name: 'old', url: 'ws://old.example', token: 'legacy-token' },
    ]))
    const relays = useRelaysStore()
    expect(relays.relays[0].token).toBe('legacy-token')
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]') as Array<{ token?: string }>
    expect(stored[0].token).toBeUndefined()
    expect(JSON.stringify(stored)).not.toContain('legacy-token')
  })
})
