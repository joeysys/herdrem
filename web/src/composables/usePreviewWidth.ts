// usePreviewWidth.ts — code line width preference for the file preview.
// Module-level singleton state so PreferencesSection and FilePreview share
// the exact same reactive ref (same pattern as useLayoutMode).
import { ref } from 'vue'
import { safeStorage } from '../utils/storage'

// 'screen' wraps at the panel edge; a number fixes the code column to that
// many characters; 'none' disables wrapping (horizontal scroll).
export type PreviewWidth = 'screen' | '80' | '100' | '120' | '160' | 'none'

export const PREVIEW_WIDTHS: readonly PreviewWidth[] = ['screen', '80', '100', '120', '160', 'none']

const KEY = 'herdrem_preview_width'

function load(): PreviewWidth {
  const v = safeStorage.getItem(KEY)
  return (PREVIEW_WIDTHS as readonly string[]).includes(v || '') ? (v as PreviewWidth) : 'screen'
}

const previewWidth = ref<PreviewWidth>(load())

export function usePreviewWidth() {
  function setPreviewWidth(w: PreviewWidth) {
    previewWidth.value = w
    safeStorage.setItem(KEY, w)
  }
  return { previewWidth, previewWidths: PREVIEW_WIDTHS, setPreviewWidth }
}
