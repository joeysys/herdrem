// useSaveFeedback.ts — prefs save-button state driven by the relay ack:
// save → 'saving' while the prefs_set is in flight → 'saved' when the
// prefs_updated broadcast confirms it, or 'error' when the relay rejects it
// or the socket is down. Reverts to 'idle' after a short flash.
import { computed, onUnmounted, ref, watch } from 'vue'
import { usePrefsStore } from '../stores/prefs'

export type SaveState = 'idle' | 'saving' | 'saved' | 'error'

const FLASH_MS = 1800
const ACK_TIMEOUT_MS = 5000

export function useSaveFeedback(...sections: string[]) {
  const prefs = usePrefsStore()
  const state = ref<SaveState>('idle')
  let timer: ReturnType<typeof setTimeout> | undefined

  function flash(s: SaveState, ms = FLASH_MS) {
    clearTimeout(timer)
    state.value = s
    timer = setTimeout(() => (state.value = 'idle'), ms)
  }

  const stop = watch(
    () => sections.some((s) => prefs.pendingSaves.has(s)),
    (pending, wasPending) => {
      if (!wasPending || pending || state.value !== 'saving') return
      flash(prefs.saveError ? 'error' : 'saved')
    },
  )
  onUnmounted(() => {
    stop()
    clearTimeout(timer)
  })

  /** Run a prefs save and drive the button state. Returns send success. */
  function save(fn: () => boolean) {
    clearTimeout(timer)
    if (!fn()) {
      flash('error', 2500)
      return false
    }
    state.value = 'saving'
    // The ack may never arrive if the socket dropped mid-flight.
    timer = setTimeout(() => {
      if (state.value === 'saving') flash('error', 2500)
    }, ACK_TIMEOUT_MS)
    return true
  }

  const label = computed(
    () => ({ idle: 'save', saving: 'saving…', saved: 'saved ✓', error: 'failed' })[state.value],
  )

  return { state, label, save }
}
