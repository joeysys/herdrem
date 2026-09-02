import { computed, ref, watch } from 'vue'
import { safeStorage } from '../utils/storage'

export interface ThemeEntry {
  id: string
  label: string
  preview?: string
}

export const THEMES: readonly ThemeEntry[] = [
  { id: 'default', label: 'default', preview: 'dark' },
  { id: 'modern', label: 'modern', preview: 'modern-dark' },
  { id: 'herdr', label: 'herdr' },
  { id: 'e-ink', label: 'e-ink' },
  { id: 'sand', label: 'sand' },
  { id: 'clay', label: 'clay' },
  { id: 'dune', label: 'dune' },
  { id: 'nord', label: 'nord' },
  { id: 'rose', label: 'rose' },
  { id: 'dracula', label: 'dracula' },
  { id: 'kanagawa', label: 'kanagawa' },
  { id: 'midnight', label: 'midnight' },
  { id: 'dark', label: 'dark' },
  { id: 'light', label: 'light' },
  { id: 'modern-dark', label: 'modern dark' },
  { id: 'modern-light', label: 'modern light' },
]

const KEY = 'herdrem_theme'
const urlTheme = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('theme') : null
const isValidTheme = urlTheme && THEMES.some((t) => t.id === urlTheme)
const stored = safeStorage.getItem(KEY)
let initial = isValidTheme ? urlTheme! : stored || 'dark'
if (isValidTheme) {
  safeStorage.setItem(KEY, initial)
}
const theme = ref<string>(initial)

/** Meta-themes ('default', 'modern') resolve to a concrete theme based on the
 *  system prefers-color-scheme media query. */
const prefersDark = typeof window !== 'undefined'
  ? window.matchMedia('(prefers-color-scheme: dark)')
  : null

function resolveTheme(t: string): string {
  if (t === 'default') return prefersDark?.matches ? 'dark' : 'light'
  if (t === 'modern') return prefersDark?.matches ? 'modern-dark' : 'modern-light'
  return t
}

/** The concrete theme currently applied to <html> (meta-theme resolved). */
const resolvedTheme = computed(() => resolveTheme(theme.value))

function apply(t: string) {
  document.documentElement.dataset.theme = resolveTheme(t)
  const meta = document.getElementById('metaThemeColor')
  if (meta) {
    const bg = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()
    if (bg) meta.setAttribute('content', bg)
  }
}

// Re-apply when system color scheme changes (only affects meta-themes)
prefersDark?.addEventListener('change', () => apply(theme.value))

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
  return { theme, resolvedTheme, themes: THEMES, setTheme, init }
}
