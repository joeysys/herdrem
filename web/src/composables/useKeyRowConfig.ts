import { computed, ref } from 'vue'
import { safeStorage } from '../utils/storage'

export interface KeyItemDef {
  id: string
  label: string
  name: string
  description?: string
  danger?: boolean
  repeat?: boolean
  key?: string
}

export interface KeyItemConfig {
  id: string
  visible: boolean
}

export type KeyRowPaneType = 'agent' | 'shell'

export const DEFAULT_AGENT_KEY_DEFS: KeyItemDef[] = [
  { id: 'skills', label: '⚡', name: 'Skills', description: 'Agent skills menu' },
  { id: 'agentCmds', label: '▶', name: 'Commands', description: 'Agent quick commands' },
  { id: 'arrows', label: '⌖', name: 'Arrows', description: 'Arrow keys pad' },
  { id: 'Enter', label: 'Enter', name: 'Enter', key: 'Enter' },
  { id: 'Escape', label: 'Esc', name: 'Esc', key: 'Escape' },
  { id: 'Tab', label: 'Tab', name: 'Tab', key: 'Tab' },
  { id: 'Ctrl+c', label: '^C', name: '^C', danger: true, repeat: true, key: 'Ctrl+c' },
  { id: 'Ctrl+d', label: '^D', name: '^D', danger: true, repeat: true, key: 'Ctrl+d' },
  { id: 'ctrl', label: 'ctrl', name: 'Ctrl modifier', description: 'Next key sent as Ctrl+key' },
]

export const DEFAULT_SHELL_KEY_DEFS: KeyItemDef[] = [
  { id: 'input', label: '❯', name: 'Input box', description: 'Toggle input box' },
  { id: 'shellCmds', label: '▶', name: 'Commands', description: 'Shell quick commands' },
  { id: 'arrows', label: '⌖', name: 'Arrows', description: 'Arrow keys pad' },
  { id: 'Enter', label: 'Enter', name: 'Enter', key: 'Enter' },
  { id: 'Escape', label: 'Esc', name: 'Esc', key: 'Escape' },
  { id: 'Tab', label: 'Tab', name: 'Tab', key: 'Tab' },
  { id: 'Ctrl+c', label: '^C', name: '^C', danger: true, repeat: true, key: 'Ctrl+c' },
  { id: 'Ctrl+d', label: '^D', name: '^D', key: 'Ctrl+d' },
  { id: 'Ctrl+z', label: '^Z', name: '^Z', key: 'Ctrl+z' },
  { id: 'Ctrl+l', label: '^L', name: '^L', key: 'Ctrl+l' },
  { id: 'Ctrl+r', label: '^R', name: '^R', key: 'Ctrl+r' },
  { id: 'Ctrl+a', label: '^A', name: '^A', key: 'Ctrl+a' },
  { id: 'Ctrl+e', label: '^E', name: '^E', key: 'Ctrl+e' },
  { id: 'Ctrl+w', label: '^W', name: '^W', key: 'Ctrl+w' },
  { id: 'ctrl', label: 'ctrl', name: 'Ctrl modifier', description: 'Next key sent as Ctrl+key' },
]

export function storageKeyFor(type: KeyRowPaneType): string {
  return `herdrem_key_row_${type}`
}

export function defaultDefsFor(type: KeyRowPaneType): KeyItemDef[] {
  return type === 'agent' ? DEFAULT_AGENT_KEY_DEFS : DEFAULT_SHELL_KEY_DEFS
}

export function loadKeyRowConfig(type: KeyRowPaneType): KeyItemConfig[] {
  const defs = defaultDefsFor(type)
  const key = storageKeyFor(type)
  const raw = safeStorage.getItem(key)
  if (!raw) {
    return defs.map((d) => ({ id: d.id, visible: true }))
  }

  try {
    const parsed = JSON.parse(raw) as Array<{ id: string; visible?: boolean }>
    if (!Array.isArray(parsed)) throw new Error('invalid config')
    const validIds = new Set(defs.map((d) => d.id))
    const result: KeyItemConfig[] = []
    const seen = new Set<string>()

    for (const item of parsed) {
      if (item && typeof item.id === 'string' && validIds.has(item.id) && !seen.has(item.id)) {
        result.push({ id: item.id, visible: item.visible !== false })
        seen.add(item.id)
      }
    }

    // Append any newly added default items not present in saved config
    for (const d of defs) {
      if (!seen.has(d.id)) {
        result.push({ id: d.id, visible: true })
      }
    }

    return result
  } catch {
    return defs.map((d) => ({ id: d.id, visible: true }))
  }
}

// Module-level reactive refs so any view / modal sharing the pane type is synchronized
const agentConfig = ref<KeyItemConfig[]>(loadKeyRowConfig('agent'))
const shellConfig = ref<KeyItemConfig[]>(loadKeyRowConfig('shell'))

export function resetKeyRowState(type?: KeyRowPaneType) {
  if (!type || type === 'agent') {
    agentConfig.value = loadKeyRowConfig('agent')
  }
  if (!type || type === 'shell') {
    shellConfig.value = loadKeyRowConfig('shell')
  }
}

export function useKeyRowConfig(type: KeyRowPaneType) {
  const configRef = type === 'agent' ? agentConfig : shellConfig
  const defs = defaultDefsFor(type)
  const defsMap = new Map(defs.map((d) => [d.id, d]))
  const storageKey = storageKeyFor(type)

  function save() {
    safeStorage.setItem(storageKey, JSON.stringify(configRef.value))
  }

  const items = computed(() => {
    return configRef.value.map((c) => {
      const def = defsMap.get(c.id) || { id: c.id, label: c.id, name: c.id }
      return {
        ...def,
        visible: c.visible,
      }
    })
  })

  const visibleItems = computed(() => {
    return items.value.filter((item) => item.visible)
  })

  function moveUp(index: number) {
    if (index <= 0 || index >= configRef.value.length) return
    const arr = [...configRef.value]
    const temp = arr[index - 1]
    arr[index - 1] = arr[index]
    arr[index] = temp
    configRef.value = arr
    save()
  }

  function moveDown(index: number) {
    if (index < 0 || index >= configRef.value.length - 1) return
    const arr = [...configRef.value]
    const temp = arr[index + 1]
    arr[index + 1] = arr[index]
    arr[index] = temp
    configRef.value = arr
    save()
  }

  function setVisible(id: string, visible: boolean) {
    const idx = configRef.value.findIndex((c) => c.id === id)
    if (idx >= 0) {
      const arr = [...configRef.value]
      arr[idx] = { ...arr[idx], visible }
      configRef.value = arr
      save()
    }
  }

  function toggleVisible(id: string) {
    const item = configRef.value.find((c) => c.id === id)
    if (item) {
      setVisible(id, !item.visible)
    }
  }

  function reset() {
    configRef.value = defs.map((d) => ({ id: d.id, visible: true }))
    safeStorage.removeItem(storageKey)
  }

  return {
    type,
    items,
    visibleItems,
    moveUp,
    moveDown,
    setVisible,
    toggleVisible,
    reset,
  }
}
