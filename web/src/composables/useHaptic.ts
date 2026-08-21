// useHaptic.ts — light vibration feedback on supported devices.
export function useHaptic() {
  function haptic(pattern: number | number[] = 10) {
    if (typeof navigator !== 'undefined' && 'vibrate' in navigator) {
      try {
        navigator.vibrate(pattern)
      } catch {
        /* not supported / not allowed */
      }
    }
  }
  return { haptic }
}
