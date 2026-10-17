import { ref } from 'vue'
import { vi } from 'vitest'

// Stub for 'virtual:pwa-register/vue' — no service worker exists under jsdom.
// Exposes its internals so tests can drive the update banner lifecycle.
export const needRefreshRef = ref(false)
export const offlineReadyRef = ref(false)
export const updateServiceWorker = vi.fn(async () => {})

export interface RegisterSwOptions {
  onNeedRefresh?: () => void
  onOfflineReady?: () => void
  onRegisteredSW?: (swUrl: string, registration?: { update: () => Promise<void> }) => void
}

// The options App.vue passed on the last useRegisterSW() call (there is one
// call per mount).
export let lastOptions: RegisterSwOptions | undefined

export function useRegisterSW(options?: RegisterSwOptions) {
  lastOptions = options
  return {
    needRefresh: needRefreshRef,
    offlineReady: offlineReadyRef,
    updateServiceWorker,
  }
}

export function resetRegisterSwMock() {
  needRefreshRef.value = false
  offlineReadyRef.value = false
  updateServiceWorker.mockClear()
  lastOptions = undefined
}
