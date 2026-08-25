import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useRelaysStore } from './relays'

const STORAGE_KEY = 'herdrem_relays'

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
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
