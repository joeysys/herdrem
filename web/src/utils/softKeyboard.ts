// softKeyboard.ts — on touch devices, focusing an input raises the soft
// keyboard and shifts the layout. Convenience focus (opening a pane, tapping
// the output, closing a panel) must not do that: there the keyboard comes up
// only for explicit typing actions (tapping an input or the prompt line,
// opening search / history / the input box, picking a history entry).

/** hasSoftKeyboard reports a touch-first device, where focus means keyboard. */
export function hasSoftKeyboard(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia('(pointer: coarse)').matches
}

/** autoFocus focuses el for convenience: always on desktop; on touch only
 *  when focus is already in an input, so the keyboard state never changes. */
export function autoFocus(el: HTMLInputElement | HTMLTextAreaElement | null | undefined): boolean {
  if (!el) return false
  if (hasSoftKeyboard() && !isTextInput(document.activeElement)) return false
  el.focus()
  return true
}

function isTextInput(el: Element | null): boolean {
  if (!el) return false
  if (el instanceof HTMLTextAreaElement) return !el.readOnly
  if (el instanceof HTMLInputElement) return !el.readOnly && !['button', 'checkbox', 'radio', 'submit', 'reset', 'range', 'file'].includes(el.type)
  return (el as HTMLElement).isContentEditable === true
}
