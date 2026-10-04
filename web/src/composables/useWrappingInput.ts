import { nextTick, onMounted, watch, type Ref } from 'vue'

// A <textarea rows="1"> for sending to a pane: Enter submits, long text wraps
// and grows (CSS max-height caps it; past that it scrolls), and — where the
// relay can paste multi-line text — Shift+Enter inserts a new line.
//
// Enter is caught as `beforeinput` insertLineBreak / insertParagraph rather
// than keydown, so confirming an IME candidate with Enter (a composition
// event, not a line break) never submits, and mobile keyboards that report
// keyCode 229 still do. Ctrl/⌘+Enter handlers that preventDefault on keydown
// stop the line break before it gets here.
export function useWrappingInput(
  el: Ref<HTMLTextAreaElement | null>,
  text: Ref<string>,
  submit: () => void,
  /** whether line breaks may stay in the text (the relay pastes them) */
  multiline: () => boolean,
) {
  function fit() {
    const t = el.value
    if (!t) return
    // Measure without a scrollbar: at height:auto the one-row box overflows,
    // the scrollbar narrows it, the text wraps one line more, and the box
    // ends up a line too tall. Past max-height the scrollbar comes back.
    t.style.overflowY = 'hidden'
    t.style.height = 'auto'
    t.style.height = `${t.scrollHeight}px`
    t.style.overflowY = ''
  }
  watch(text, () => nextTick(fit))
  onMounted(fit)

  // Set by the keydown that precedes a line break; any other key clears it.
  let newLineWanted = false

  function onKeydown(e: KeyboardEvent) {
    newLineWanted =
      e.key === 'Enter' && e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && !e.isComposing && multiline()
  }

  function onBeforeInput(e: Event) {
    const type = (e as InputEvent).inputType
    if (type !== 'insertLineBreak' && type !== 'insertParagraph') return
    if (newLineWanted) {
      newLineWanted = false
      return // Shift+Enter: let the textarea insert it
    }
    e.preventDefault()
    submit()
  }

  // Without multi-line support, drop line breaks from a paste like
  // <input type="text"> does, so it never reaches the pane as several Enters.
  function onInput() {
    const t = el.value
    if (!t || multiline() || !/[\r\n]/.test(t.value)) return
    const caret = t.selectionStart ?? t.value.length
    const removedBefore = (t.value.slice(0, caret).match(/[\r\n]/g) || []).length
    const clean = t.value.replace(/[\r\n]/g, '')
    t.value = clean
    text.value = clean
    const at = caret - removedBefore
    t.setSelectionRange(at, at)
  }

  return { onKeydown, onBeforeInput, onInput, fit }
}

/** Whether the caret sits on the first (up) or last (down) line of the text,
 *  so ↑ / ↓ there may recall history instead of moving between lines. */
export function caretOnEdgeLine(el: EventTarget | null, dir: 'up' | 'down'): boolean {
  if (!(el instanceof HTMLTextAreaElement)) return true
  const pos = el.selectionStart ?? 0
  return dir === 'up' ? !el.value.slice(0, pos).includes('\n') : !el.value.slice(el.selectionEnd ?? pos).includes('\n')
}
