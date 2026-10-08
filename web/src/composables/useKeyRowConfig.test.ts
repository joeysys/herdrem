import { describe, it, expect, beforeEach } from 'vitest'
import {
  useKeyRowConfig,
  resetKeyRowState,
  DEFAULT_AGENT_KEY_DEFS,
  DEFAULT_SHELL_KEY_DEFS,
} from './useKeyRowConfig'
import { safeStorage } from '../utils/storage'

beforeEach(() => {
  safeStorage.removeItem('herdrem_key_row_agent')
  safeStorage.removeItem('herdrem_key_row_shell')
  resetKeyRowState('agent')
  resetKeyRowState('shell')
})

describe('useKeyRowConfig', () => {
  it('loads default agent keys including Tab after Esc', () => {
    const { items, visibleItems } = useKeyRowConfig('agent')
    expect(items.value.map((i) => i.id)).toEqual(DEFAULT_AGENT_KEY_DEFS.map((d) => d.id))
    expect(visibleItems.value.length).toBe(DEFAULT_AGENT_KEY_DEFS.length)

    const escIdx = items.value.findIndex((i) => i.id === 'Escape')
    const tabIdx = items.value.findIndex((i) => i.id === 'Tab')
    expect(tabIdx).toBe(escIdx + 1)
  })

  it('loads default shell keys including Tab after Esc', () => {
    const { items, visibleItems } = useKeyRowConfig('shell')
    expect(items.value.map((i) => i.id)).toEqual(DEFAULT_SHELL_KEY_DEFS.map((d) => d.id))
    expect(visibleItems.value.length).toBe(DEFAULT_SHELL_KEY_DEFS.length)

    const escIdx = items.value.findIndex((i) => i.id === 'Escape')
    const tabIdx = items.value.findIndex((i) => i.id === 'Tab')
    expect(tabIdx).toBe(escIdx + 1)
  })

  it('can reorder buttons up and down and persist', () => {
    const { items, moveUp, moveDown } = useKeyRowConfig('agent')
    const initialFirst = items.value[0].id
    const initialSecond = items.value[1].id

    moveDown(0)
    expect(items.value[0].id).toBe(initialSecond)
    expect(items.value[1].id).toBe(initialFirst)

    // Stored to safeStorage
    const stored = JSON.parse(safeStorage.getItem('herdrem_key_row_agent') || '[]')
    expect(stored[0].id).toBe(initialSecond)
    expect(stored[1].id).toBe(initialFirst)

    moveUp(1)
    expect(items.value[0].id).toBe(initialFirst)
    expect(items.value[1].id).toBe(initialSecond)
  })

  it('can toggle visibility of buttons and persist', () => {
    const { items, visibleItems, toggleVisible, setVisible } = useKeyRowConfig('agent')
    expect(visibleItems.value.some((i) => i.id === 'Tab')).toBe(true)

    toggleVisible('Tab')
    expect(items.value.find((i) => i.id === 'Tab')?.visible).toBe(false)
    expect(visibleItems.value.some((i) => i.id === 'Tab')).toBe(false)

    const stored = JSON.parse(safeStorage.getItem('herdrem_key_row_agent') || '[]')
    expect(stored.find((s: { id: string }) => s.id === 'Tab')?.visible).toBe(false)

    setVisible('Tab', true)
    expect(visibleItems.value.some((i) => i.id === 'Tab')).toBe(true)
  })

  it('resets to defaults cleanly', () => {
    const { items, moveDown, toggleVisible, reset } = useKeyRowConfig('agent')
    moveDown(0)
    toggleVisible('Tab')
    expect(safeStorage.getItem('herdrem_key_row_agent')).not.toBeNull()

    reset()
    expect(safeStorage.getItem('herdrem_key_row_agent')).toBeNull()
    expect(items.value.map((i) => i.id)).toEqual(DEFAULT_AGENT_KEY_DEFS.map((d) => d.id))
    expect(items.value.every((i) => i.visible)).toBe(true)
  })

  it('appends newly added keys to old stored configs', () => {
    // Simulate user having saved a config from before Tab existed
    const oldConfig = [
      { id: 'Enter', visible: true },
      { id: 'Escape', visible: false },
    ]
    safeStorage.setItem('herdrem_key_row_agent', JSON.stringify(oldConfig))
    resetKeyRowState('agent')

    const { items } = useKeyRowConfig('agent')
    expect(items.value[0].id).toBe('Enter')
    expect(items.value[1].id).toBe('Escape')
    expect(items.value[1].visible).toBe(false)

    // Tab must be present and appended as visible
    const tab = items.value.find((i) => i.id === 'Tab')
    expect(tab).toBeDefined()
    expect(tab?.visible).toBe(true)
  })
})
