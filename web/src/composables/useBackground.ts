// useBackground.ts — custom background image per device (PC/mobile not shared).
// Supports URL input or local file upload (stored as data URL in localStorage).
import { ref, watch } from 'vue'
import { safeStorage } from '../utils/storage'

// Device-specific key: PC and mobile get separate storage slots
const isMobile = typeof window !== 'undefined' &&
  (window.matchMedia('(max-width: 768px)').matches || /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent))
const STORAGE_KEY = isMobile ? 'herdrem_bg_mobile' : 'herdrem_bg_desktop'
const NAME_KEY = isMobile ? 'herdrem_bg_name_mobile' : 'herdrem_bg_name_desktop'

const stored = safeStorage.getItem(STORAGE_KEY) || ''
const storedName = safeStorage.getItem(NAME_KEY) || ''
const bgImage = ref(stored)
const bgName = ref(storedName)

function applyBg(url: string) {
  document.documentElement.style.setProperty('--bg-image', url ? `url("${url}")` : 'none')
}

watch(bgImage, (url) => {
  // Apply first so it works even if localStorage save fails
  applyBg(url)
  try {
    if (url) safeStorage.setItem(STORAGE_KEY, url)
    else safeStorage.removeItem(STORAGE_KEY)
  } catch {
    // Quota exceeded — bg still applies for this session, just won't persist
  }
})

watch(bgName, (name) => {
  try {
    if (name) safeStorage.setItem(NAME_KEY, name)
    else safeStorage.removeItem(NAME_KEY)
  } catch { /* ignore */ }
})

// Resize/compress image to fit within localStorage quota (mobile ~5MB)
function compressImage(dataUrl: string, maxDim: number, quality: number): Promise<string> {
  return new Promise((resolve) => {
    const img = new Image()
    img.onload = () => {
      let { width, height } = img
      if (width > maxDim || height > maxDim) {
        const scale = maxDim / Math.max(width, height)
        width = Math.round(width * scale)
        height = Math.round(height * scale)
      }
      const canvas = document.createElement('canvas')
      canvas.width = width
      canvas.height = height
      const ctx = canvas.getContext('2d')
      if (!ctx) { resolve(dataUrl); return }
      ctx.drawImage(img, 0, 0, width, height)
      // Try JPEG at given quality; if still too large, reduce quality progressively
      let result = canvas.toDataURL('image/jpeg', quality)
      resolve(result)
    }
    img.onerror = () => resolve(dataUrl)
    img.src = dataUrl
  })
}

export function useBackground() {
  function setBgUrl(url: string) {
    bgImage.value = url
    bgName.value = url
  }

  async function setBgFile(file: File) {
    const reader = new FileReader()
    reader.onload = async () => {
      let dataUrl = reader.result as string
      // Compress to keep localStorage happy (especially on mobile)
      if (dataUrl.length > 1_500_000) {
        const maxDim = isMobile ? 1280 : 1920
        dataUrl = await compressImage(dataUrl, maxDim, 0.8)
      }
      bgImage.value = dataUrl
      bgName.value = file.name
    }
    reader.readAsDataURL(file)
  }

  function clearBg() {
    bgImage.value = ''
    bgName.value = ''
  }

  function init() {
    applyBg(bgImage.value)
  }

  return { bgImage, bgName, setBgUrl, setBgFile, clearBg, init }
}
