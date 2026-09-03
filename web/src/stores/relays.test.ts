import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useRelaysStore } from './relays'
import { safeStorage } from '../utils/storage'

const STORAGE_KEY = 'herdrem_relays'

beforeEach(() => {
  localStorage.clear()
  safeStorage.removeItem(STORAGE_KEY)
  setActivePinia(createPinia())
})

describe('useRelaysStore credentials', () => {
  it('persists relay metadata without tokens', () => {
    const relays = useRelaysStore()
    relays.add('prod', 'wss://relay.example', 'super-secret')
    const raw = localStorage.getItem(STORAGE_KEY) || ''
    expect(raw).not.toContain('super-secret')
    const stored = JSON.parse(raw) as Array<{ token?: string; name: string }>
    expect(stored).toHaveLength(1)
    expect(stored[0].name).toBe('prod')
    expect(stored[0].token).toBeUndefined()
    expect(relays.relays[0].token).toBe('super-secret')
  })

  it('lifts a token query into memory and never persists it in the URL', () => {
    const relays = useRelaysStore()
    relays.add('prod', 'wss://relay.example/ws?token=super-secret&keep=1')
    expect(relays.relays[0].url).toBe('wss://relay.example?keep=1')
    expect(relays.relays[0].token).toBe('super-secret')
    const raw = localStorage.getItem(STORAGE_KEY) || ''
    expect(raw).not.toContain('super-secret')
    const stored = JSON.parse(raw) as Array<{ url: string; token?: string }>
    expect(stored[0].url).toBe('wss://relay.example?keep=1')
    expect(stored[0].token).toBeUndefined()
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
