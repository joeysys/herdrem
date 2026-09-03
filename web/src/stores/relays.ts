import { defineStore } from 'pinia'
import { ref } from 'vue'
import { extractRelayCredentials } from '../utils/relay'
import { safeStorage } from '../utils/storage'

export interface RelayEntry {
  id: string
  name: string
  url: string
  token?: string
}

const STORAGE_KEY = 'herdrem_relays'
const CURRENT_KEY = 'herdrem_relays_current'

function persistRelays(list: RelayEntry[]) {
  // Never write tokens to localStorage. Keep them on the in-memory
  // entries for this session only. Same-origin reconnect uses the HttpOnly
  // session cookie, not this list.
  const meta = list.map(({ id, name, url }) => ({ id, name, url }))
  safeStorage.setItem(STORAGE_KEY, JSON.stringify(meta))
}

function loadRelays(): RelayEntry[] {
  try {
    const raw = safeStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as RelayEntry[]
    if (!Array.isArray(parsed)) return []
    // One-session migration: keep any previously stored token in memory so
    // this tab still connects, then immediately strip credentials from disk.
    const list = parsed.map((r) => {
      const creds = extractRelayCredentials(r.url || '')
      return {
        id: r.id,
        name: r.name,
        url: creds.url,
        token: r.token || creds.token || undefined,
      }
    })
    persistRelays(list)
    return list
  } catch {
    return []
  }
}

export const useRelaysStore = defineStore('relays', () => {
  const relays = ref<RelayEntry[]>(loadRelays())
  const currentId = ref<string>(safeStorage.getItem(CURRENT_KEY) || '')

  function save() {
    persistRelays(relays.value)
  }

  function add(name: string, url: string, token?: string): RelayEntry {
    const creds = extractRelayCredentials(url)
    const entry: RelayEntry = {
      id: crypto.randomUUID(),
      name,
      url: creds.url,
      token: token || creds.token || undefined,
    }
    relays.value.push(entry)
    save()
    return entry
  }

  function update(id: string, patch: Partial<Omit<RelayEntry, 'id'>>) {
    const idx = relays.value.findIndex((r) => r.id === id)
    if (idx === -1) return
    if (patch.url) {
      const creds = extractRelayCredentials(patch.url)
      patch.url = creds.url
      if (creds.token && patch.token === undefined) patch.token = creds.token
    }
    relays.value[idx] = { ...relays.value[idx], ...patch }
    save()
  }

  function upsert(input: { name: string; url: string; token?: string }): RelayEntry {
    const creds = extractRelayCredentials(input.url)
    const token = input.token || creds.token || undefined
    const existing = relays.value.find((r) => r.url === creds.url)
    if (existing) {
      update(existing.id, { name: input.name, token })
      return { ...existing, name: input.name, token }
    }
    return add(input.name, creds.url, token)
  }

  function remove(id: string) {
    relays.value = relays.value.filter((r) => r.id !== id)
    if (currentId.value === id) {
      currentId.value = ''
      safeStorage.removeItem(CURRENT_KEY)
    }
    save()
  }

  function setCurrent(id: string) {
    currentId.value = id
    safeStorage.setItem(CURRENT_KEY, id)
  }

  function current(): RelayEntry | null {
    return relays.value.find((r) => r.id === currentId.value) || null
  }

  return { relays, currentId, add, update, upsert, remove, setCurrent, current, save }
})
