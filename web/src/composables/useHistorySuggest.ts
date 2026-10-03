import { computed, ref, watch, type Ref } from 'vue'
import { useCmdHistoryStore } from '../stores/cmdHistory'
import type { HistoryKind, Where } from '../utils/cmdHistory'

export interface HistorySuggestOptions {
  /** the text being edited */
  text: Ref<string>
  kind: HistoryKind
  /** the pane / workspace being typed in, used to rank its own history first */
  where: () => Where
  /** whether suggestions may show right now (input focused, no panel open…) */
  enabled: () => boolean
  /** replace the input with the chosen suggestion */
  accept: (text: string) => void
  /** Ctrl/Cmd+R */
  openHistory: () => void
}

/**
 * Inline history suggestions + keyboard handling shared by the shell prompt
 * and the agent input. Ctrl/Cmd+R opens the history panel (instead of
 * reloading the page); while suggestions are visible Tab / → accept, ↑ ↓
 * choose and Esc dismisses. With no suggestions every key behaves normally.
 */
export function useHistorySuggest(o: HistorySuggestOptions) {
  const history = useCmdHistoryStore()
  const active = ref(0)
  const dismissedFor = ref<string | null>(null)

  const suggestions = computed(() => {
    if (!o.enabled() || dismissedFor.value === o.text.value) return []
    return history.suggest(o.text.value, { kind: o.kind, ...o.where() }, 6)
  })
  watch(o.text, () => { active.value = 0 })

  function onKeydown(e: KeyboardEvent) {
    if (e.isComposing) return
    if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'r') {
      e.preventDefault()
      o.openHistory()
      return
    }
    const list = suggestions.value
    if (!list.length) return
    const chosen = list[active.value] ?? list[0]
    if (e.key === 'Tab' && !e.shiftKey) {
      e.preventDefault()
      o.accept(chosen)
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      active.value = (active.value + 1) % list.length
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      active.value = (active.value - 1 + list.length) % list.length
    } else if (e.key === 'ArrowRight') {
      const t = e.target as HTMLInputElement
      if (t.selectionStart === t.value.length && t.selectionEnd === t.value.length) {
        e.preventDefault()
        o.accept(chosen)
      }
    } else if (e.key === 'Escape') {
      if (!list.length) return
      dismissedFor.value = o.text.value
      e.preventDefault()
      e.stopPropagation()
    }
  }

  return { suggestions, active, onKeydown }
}
