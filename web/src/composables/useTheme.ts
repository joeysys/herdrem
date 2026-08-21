import { ref, watch } from 'vue'
import { safeStorage } from '../utils/storage'

export const THEMES = [
  { id: 'dark', label: 'dark' },
  { id: 'modern-dark', label: 'modern dark' },
  { id: 'modern-light', label: 'modern light' },
  { id: 'herdr', label: 'herdr' },
  { id: 'light', label: 'light' },
  { id: 'e-ink', label: 'e-ink' },
  { id: 'sand', label: 'sand' },
  { id: 'clay', label: 'clay' },
  { id: 'dune', label: 'dune' },
  { id: 'nord', label: 'nord' },
  { id: 'rose', label: 'rose' },
  { id: 'dracula', label: 'dracula' },
  { id: 'kanagawa', label: 'kanagawa' },
  { id: 'midnight', label: 'midnight' },
] as const

const KEY = 'herdrem_theme'
const urlTheme = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('theme') : null
const isValidTheme = urlTheme && THEMES.some((t) => t.id === urlTheme)
const stored = safeStorage.getItem(KEY)
let initial = isValidTheme ? urlTheme! : stored || 'dark'
if (isValidTheme) {
  safeStorage.setItem(KEY, initial)
}
const theme = ref<string>(initial)

function apply(t: string) {
  document.documentElement.dataset.theme = t
  const meta = document.getElementById('metaThemeColor')
  if (meta) {
    const bg = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()
    if (bg) meta.setAttribute('content', bg)
  }
}

watch(theme, (t) => {
  safeStorage.setItem(KEY, t)
  apply(t)
})

export function useTheme() {
  function setTheme(t: string) {
    theme.value = t
  }
  function init() {
    apply(theme.value)
  }
  return { theme, themes: THEMES, setTheme, init }
}
