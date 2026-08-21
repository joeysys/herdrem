import { defineStore } from 'pinia'
import { ref } from 'vue'
import { normalizeRelayUrl } from '../utils/relay'
import { safeStorage } from '../utils/storage'

export interface RelayEntry {
  id: string
  name: string
  url: string
  token?: string
}

const STORAGE_KEY = 'herdrem_relays'
const CURRENT_KEY = 'herdrem_relays_current'

function loadRelays(): RelayEntry[] {
  try {
    const raw = safeStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    return JSON.parse(raw) as RelayEntry[]
  } catch {
    return []
  }
}

function saveRelays(list: RelayEntry[]) {
  safeStorage.setItem(STORAGE_KEY, JSON.stringify(list))
}

export const useRelaysStore = defineStore('relays', () => {
  const relays = ref<RelayEntry[]>(loadRelays())
  const currentId = ref<string>(safeStorage.getItem(CURRENT_KEY) || '')

  function save() {
    saveRelays(relays.value)
  }

  function add(name: string, url: string, token?: string): RelayEntry {
    const entry: RelayEntry = {
      id: crypto.randomUUID(),
      name,
      url: normalizeRelayUrl(url),
      token,
    }
    relays.value.push(entry)
    save()
    return entry
  }

  function update(id: string, patch: Partial<Omit<RelayEntry, 'id'>>) {
    const idx = relays.value.findIndex((r) => r.id === id)
    if (idx === -1) return
    if (patch.url) patch.url = normalizeRelayUrl(patch.url)
    relays.value[idx] = { ...relays.value[idx], ...patch }
    save()
  }

  function upsert(input: { name: string; url: string; token?: string }): RelayEntry {
    const norm = normalizeRelayUrl(input.url)
    const existing = relays.value.find((r) => r.url === norm)
    if (existing) {
      update(existing.id, { name: input.name, token: input.token })
      return { ...existing, name: input.name, token: input.token }
    }
    return add(input.name, norm, input.token)
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
