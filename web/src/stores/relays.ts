import { defineStore } from 'pinia'
import { ref } from 'vue'
import { extractRelayCredentials, dropSessionToken, saveSessionToken, sessionTokenFor } from '../utils/relay'
import { safeStorage } from '../utils/storage'
import { uid } from '../utils/id'

export interface RelayEntry {
  id: string
  name: string
  url: string
  token?: string
}

const STORAGE_KEY = 'herdrem_relays'
const CURRENT_KEY = 'herdrem_relays_current'

function persistRelays(list: RelayEntry[]) {
  // Never write tokens to localStorage. Credentials live in sessionStorage
  // for this tab; same-origin connections can also use the HttpOnly cookie.
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
        token: r.token || creds.token || sessionTokenFor(creds.url) || undefined,
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

  // Seed a default same-origin entry so the built-in fallback shows up in
  // the relays list and can be edited/renamed/tokened — before this it was
  // an invisible boot() fallback with no settings representation.
  if (relays.value.length === 0 && typeof location !== 'undefined') {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const entry = add('this device', `${proto}//${location.host}`)
    if (!currentId.value) setCurrent(entry.id)
  }

  function save() {
    persistRelays(relays.value)
  }

  function add(name: string, url: string, token?: string): RelayEntry {
    const creds = extractRelayCredentials(url)
    const entry: RelayEntry = {
      id: uid(),
      name,
      url: creds.url,
      token: token || creds.token || undefined,
    }
    if (entry.token) saveSessionToken(entry.url, entry.token)
    relays.value.push(entry)
    save()
    return entry
  }

  function update(id: string, patch: Partial<Omit<RelayEntry, 'id'>>) {
    const idx = relays.value.findIndex((r) => r.id === id)
    if (idx === -1) return
    const oldUrl = relays.value[idx].url
    if (patch.url) {
      const creds = extractRelayCredentials(patch.url)
      patch.url = creds.url
      if (creds.token && patch.token === undefined) patch.token = creds.token
      // A URL change must not migrate the old address's credential: a caller
      // that supplied no token gets the new URL's own session credential (or
      // none), not the old entry's token via the spread below.
      if (patch.url !== oldUrl && patch.token === undefined) {
        patch.token = sessionTokenFor(patch.url) || undefined
      }
    }
    relays.value[idx] = { ...relays.value[idx], ...patch }
    if (relays.value[idx].token) saveSessionToken(relays.value[idx].url, relays.value[idx].token)
    // A URL change orphans the session-stored credential keyed by the old
    // URL — re-adding that URL later must not silently reuse it.
    if (patch.url && patch.url !== oldUrl) dropSessionCredential(oldUrl)
    save()
  }

  function upsert(input: { name: string; url: string; token?: string }): RelayEntry {
    const creds = extractRelayCredentials(input.url)
    const token = input.token || creds.token || sessionTokenFor(creds.url) || undefined
    const existing = relays.value.find((r) => r.url === creds.url)
    if (existing) {
      update(existing.id, { name: input.name, token })
      return { ...existing, name: input.name, token }
    }
    return add(input.name, creds.url, token)
  }

  function remove(id: string) {
    const removed = relays.value.find((r) => r.id === id)
    relays.value = relays.value.filter((r) => r.id !== id)
    if (currentId.value === id) {
      currentId.value = ''
      safeStorage.removeItem(CURRENT_KEY)
    }
    if (removed) dropSessionCredential(removed.url)
    save()
  }

  // dropSessionCredential forgets the sessionStorage token parked under a
  // URL once no remaining entry points at it — deleting a relay must mean
  // forgetting its credential, not just its metadata row.
  function dropSessionCredential(url: string) {
    if (url && !relays.value.some((r) => r.url === url)) dropSessionToken(url)
  }

  function forgetToken(url: string) {
    dropSessionToken(url)
    for (const relay of relays.value) {
      if (relay.url === url) relay.token = undefined
    }
  }

  function setCurrent(id: string) {
    currentId.value = id
    safeStorage.setItem(CURRENT_KEY, id)
  }

  function current(): RelayEntry | null {
    return relays.value.find((r) => r.id === currentId.value) || null
  }

  return { relays, currentId, add, update, upsert, remove, setCurrent, current, save, forgetToken }
})
