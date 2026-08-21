// useLayoutMode.ts — responsive desktop/mobile layout detection with user override & Android "Request Desktop Site" support.
// Uses module-level singleton state so App.vue and PreferencesSection.vue share the exact same reactive refs.
import { ref, watch } from 'vue'
import { safeStorage } from '../utils/storage'

export type LayoutPreference = 'auto' | 'mobile' | 'desktop'

const KEY = 'herdrem_layout_mode'

// Singleton reactive state shared across all components
const preference = ref<LayoutPreference>((safeStorage.getItem(KEY) as LayoutPreference) || 'auto')

function checkIsDesktop(): boolean {
  if (typeof window === 'undefined') return false

  // Priority 1: Explicit user setting in Preferences (HIGHEST PRIORITY)
  if (preference.value === 'desktop') return true
  if (preference.value === 'mobile') return false

  // Priority 2: Auto-detection based on UA and screen width
  const ua = navigator.userAgent || ''
  const isAndroidUA = /Android/i.test(ua)

  // Android UA (native mode on phones, tablets, E-ink readers):
  // Requires >= 1100px width so 906px / 980px stay in Mobile Mode.
  //
  // When user checks "Request Desktop Site" in Chrome/DuckDuckGo:
  // Browser spoofs desktop UA (e.g. 'X11; Linux x86_64' or 'Windows'), removing 'Android'.
  // isAndroidUA becomes false -> threshold drops to 900px -> 906px switches to Desktop Mode!
  const threshold = isAndroidUA ? 1100 : 900
  return window.innerWidth >= threshold
}

const isDesktop = ref(checkIsDesktop())

function updateIsDesktop() {
  isDesktop.value = checkIsDesktop()
}

function setPreference(pref: LayoutPreference) {
  preference.value = pref
  safeStorage.setItem(KEY, pref)
  updateIsDesktop()
}

watch(preference, () => {
  updateIsDesktop()
})

// Initialize resize event listeners once globally
let listenersInitialized = false
function initListeners() {
  if (listenersInitialized || typeof window === 'undefined') return
  listenersInitialized = true
  window.addEventListener('resize', updateIsDesktop)
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', updateIsDesktop)
  }
}

export function useLayoutMode() {
  initListeners()
  return { preference, setPreference, isDesktop, updateIsDesktop }
}
