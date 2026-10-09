// storage.ts — safe wrapper around localStorage for privacy-strict browsers (DuckDuckGo, Firefox, etc.)
// Prevents uncaught SecurityError / DOMException when third-party cookies / storage are blocked.

const memStore = new Map<string, string>()

export const safeStorage = {
  getItem(key: string): string | null {
    try {
      if (typeof localStorage !== 'undefined') {
        const val = localStorage.getItem(key)
        if (val !== null) return val
      }
    } catch {
      /* localStorage blocked / SecurityError */
    }
    return memStore.get(key) ?? null
  },

  setItem(key: string, value: string): void {
    memStore.set(key, value)
    try {
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem(key, value)
      }
    } catch {
      /* localStorage blocked / SecurityError */
    }
  },

  removeItem(key: string): void {
    memStore.delete(key)
    try {
      if (typeof localStorage !== 'undefined') {
        localStorage.removeItem(key)
      }
    } catch {
      /* localStorage blocked / SecurityError */
    }
  },

  clear(): void {
    memStore.clear()
    try {
      if (typeof localStorage !== 'undefined') {
        localStorage.clear()
      }
    } catch {
      /* localStorage blocked / SecurityError */
    }
  },
}
