import { afterEach, describe, expect, it } from 'vitest'
import {
  formatChord,
  isShortcutActive,
  onShortcutKeydown,
  register,
  resetShortcutsForTests,
  shortcutBlock,
  setShortcutContext,
  registerTopLayer,
  closeTopRegisteredLayer,
  overlayNow,
  type ShortcutDef,
} from './useShortcuts'

const flags = window as unknown as {
  __setStandalone: (v: boolean) => void
  __setCoarsePointer: (v: boolean) => void
}

function key(init: KeyboardEventInit): KeyboardEvent {
  return new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
}

function bind(partial: Partial<ShortcutDef> & Pick<ShortcutDef, 'id' | 'run'>): ShortcutDef {
  return { label: partial.id, scope: 'global', ...partial }
}

afterEach(() => {
  resetShortcutsForTests()
  flags.__setStandalone(false)
  flags.__setCoarsePointer(false)
  Object.defineProperty(navigator, 'platform', { value: 'Linux x86_64', configurable: true })
  document.body.replaceChildren()
})

describe('useShortcuts', () => {
  it('labels Ctrl on Linux and ⌘ on Mac', () => {
    expect(formatChord(bind({ id: 'find', key: 'f', mod: true, run: () => {} }))).toBe('Ctrl+F')
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true })
    expect(formatChord(bind({ id: 'find', key: 'f', mod: true, run: () => {} }))).toBe('⌘+F')
  })

  it('leaves Alt+digit to a Linux browser tab, which uses it to pick a tab', () => {
    const seen: string[] = []
    register(bind({ id: 'jump', code: 'Digit1', alt: true, browserTab: 'linux-blocked', run: () => { seen.push('1') } }))
    const alt1 = () => key({ key: '1', code: 'Digit1', altKey: true })

    Object.defineProperty(navigator, 'platform', { value: 'Linux x86_64', configurable: true })
    expect(isShortcutActive('jump')).toBe(false)
    const ev = alt1()
    expect(onShortcutKeydown(ev)).toBe(false)
    expect(ev.defaultPrevented).toBe(false)

    flags.__setStandalone(true) // installed window: the browser has no tabs to pick
    expect(onShortcutKeydown(alt1())).toBe(true)
    flags.__setStandalone(false)

    Object.defineProperty(navigator, 'platform', { value: 'Win32', configurable: true })
    expect(onShortcutKeydown(alt1())).toBe(true)
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true })
    expect(onShortcutKeydown(alt1())).toBe(true)
    expect(seen).toEqual(['1', '1', '1'])
  })

  it('Android is not a Linux tab: its browser does not take Alt+digit', () => {
    const jump = bind({ id: 'jump', code: 'Digit1', alt: true, browserTab: 'linux-blocked', run: () => {} })
    register(jump)
    const ua = navigator.userAgent
    Object.defineProperty(navigator, 'userAgent', {
      value: 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Mobile Safari/537.36',
      configurable: true,
    })
    try {
      for (const platform of ['Linux aarch64', '']) {
        Object.defineProperty(navigator, 'platform', { value: platform, configurable: true })
        expect(shortcutBlock(jump)).toBeNull()
        expect(isShortcutActive('jump')).toBe(true)
      }
    } finally {
      Object.defineProperty(navigator, 'userAgent', { value: ua, configurable: true })
    }
  })

  it('matches Alt by e.code when Option rewrites e.key', () => {
    const seen: string[] = []
    register(bind({ id: 'next', code: 'BracketRight', alt: true, run: () => { seen.push('next') } }))
    register(bind({ id: 'next.mac', code: 'BracketRight', meta: true, shift: true, run: () => { seen.push('next') } }))
    const ev = key({ key: '”', code: 'BracketRight', altKey: true })
    expect(onShortcutKeydown(ev)).toBe(true)
    expect(ev.defaultPrevented).toBe(true)
    expect(seen).toEqual(['next'])
    const shifted = key({ key: '}', code: 'BracketRight', altKey: true, shiftKey: true })
    expect(onShortcutKeydown(shifted)).toBe(false)
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true })
    const mac = key({ key: '}', code: 'BracketRight', metaKey: true, shiftKey: true })
    expect(onShortcutKeydown(mac)).toBe(true)
    expect(seen).toEqual(['next', 'next'])
  })

  it('does not bind browser-reserved chords outside a standalone window', () => {
    const seen: string[] = []
    register(bind({
      id: 'shell.clear',
      key: 'l',
      ctrl: true,
      scope: 'input',
      browserTab: 'blocked',
      run: () => { seen.push('clear') },
    }))
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    const blocked = key({ key: 'l', ctrlKey: true })
    Object.defineProperty(blocked, 'target', { value: input })
    expect(isShortcutActive('shell.clear')).toBe(false)
    expect(onShortcutKeydown(blocked)).toBe(false)
    expect(blocked.defaultPrevented).toBe(false)

    register(bind({
      id: 'shell.eof',
      key: 'd',
      ctrl: true,
      scope: 'input',
      browserTab: 'blocked',
      run: () => { seen.push('eof') },
    }))
    register(bind({
      id: 'app.settings',
      key: ',',
      mod: true,
      browserTab: 'mac-blocked',
      run: () => { seen.push('settings') },
    }))
    expect(isShortcutActive('shell.eof')).toBe(false)
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true })
    expect(isShortcutActive('app.settings')).toBe(false)
    Object.defineProperty(navigator, 'platform', { value: 'Linux x86_64', configurable: true })
    flags.__setStandalone(true)
    expect(isShortcutActive('shell.clear')).toBe(true)
    const live = key({ key: 'l', ctrlKey: true })
    Object.defineProperty(live, 'target', { value: input })
    expect(onShortcutKeydown(live)).toBe(true)
    expect(seen).toEqual(['clear'])
  })

  it('drops bare character keys on a coarse pointer but keeps Esc and modified chords', () => {
    const seen: string[] = []
    register(bind({ id: 'down', key: 'j', run: () => { seen.push('j') } }))
    register(bind({ id: 'back', key: 'Escape', run: () => { seen.push('esc') } }))
    register(bind({ id: 'submit', key: 'Enter', mod: true, scope: 'input', run: () => { seen.push('submit') } }))
    flags.__setCoarsePointer(true)
    expect(isShortcutActive('down')).toBe(false)
    expect(isShortcutActive('back')).toBe(true)
    expect(isShortcutActive('submit')).toBe(true)
    expect(onShortcutKeydown(key({ key: 'j' }))).toBe(false)
    expect(onShortcutKeydown(key({ key: 'Escape' }))).toBe(true)
    const input = document.createElement('textarea')
    document.body.appendChild(input)
    const submit = key({ key: 'Enter', ctrlKey: true })
    Object.defineProperty(submit, 'target', { value: input })
    expect(onShortcutKeydown(submit)).toBe(true)
    expect(seen).toEqual(['esc', 'submit'])
  })

  it('lets an overlay swallow global keys, while Esc still closes it', () => {
    const seen: string[] = []
    setShortcutContext({ overlay: () => true, terminal: () => false })
    register(bind({ id: 'down', key: 'j', run: () => { seen.push('j') } }))
    register(bind({ id: 'view', key: 's', run: () => { seen.push('s') } }))
    register(bind({ id: 'jump', code: 'Digit1', alt: true, run: () => { seen.push('1') } }))
    register(bind({ id: 'back', key: 'Escape', run: () => { seen.push('esc') } }))
    register(bind({ id: 'find', key: 'f', mod: true, scope: 'overlay', run: () => { seen.push('find') } }))
    expect(onShortcutKeydown(key({ key: 'j' }))).toBe(false)
    expect(onShortcutKeydown(key({ key: 's' }))).toBe(false)
    expect(onShortcutKeydown(key({ key: '1', code: 'Digit1', altKey: true }))).toBe(false)
    expect(onShortcutKeydown(key({ key: 'f', ctrlKey: true }))).toBe(true)
    expect(onShortcutKeydown(key({ key: 'Escape' }))).toBe(true)
    expect(seen).toEqual(['find', 'esc'])
  })

  it('blurs a text field before a global Esc and ignores bare keys while typing', () => {
    const seen: string[] = []
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    register(bind({ id: 'back', key: 'Escape', run: () => { seen.push('esc') } }))
    register(bind({ id: 'blur', key: 'Escape', scope: 'input', run: (e) => { (e.target as HTMLElement).blur() } }))
    register(bind({ id: 'down', key: 'j', run: () => { seen.push('j') } }))
    const typing = key({ key: 'j' })
    Object.defineProperty(typing, 'target', { value: input })
    expect(onShortcutKeydown(typing)).toBe(false)
    const esc = key({ key: 'Escape' })
    Object.defineProperty(esc, 'target', { value: input })
    expect(onShortcutKeydown(esc)).toBe(true)
    expect(document.activeElement).not.toBe(input)
    expect(seen).toEqual([])
    expect(onShortcutKeydown(key({ key: 'Escape' }))).toBe(true)
    expect(seen).toEqual(['esc'])
  })

  it('isolates unmodified keys in a text input — arrows included, Esc excepted', () => {
    const seen: string[] = []
    register(bind({ id: 'down', key: 'ArrowDown', run: () => { seen.push('down') } }))
    register(bind({ id: 'home', key: 'Home', run: () => { seen.push('home') } }))
    register(bind({ id: 'back', key: 'Escape', run: () => { seen.push('esc') } }))
    register(bind({ id: 'blur', key: 'Escape', scope: 'input', run: (e) => { (e.target as HTMLElement).blur() } }))
    register(bind({ id: 'find', key: 'f', mod: true, run: () => { seen.push('find') } }))
    const input = document.createElement('textarea')
    document.body.appendChild(input)
    input.focus()
    const at = (init: KeyboardEventInit, target: EventTarget = input) => {
      const ev = key(init)
      Object.defineProperty(ev, 'target', { value: target })
      return ev
    }
    expect(onShortcutKeydown(at({ key: 'ArrowDown' }))).toBe(false)
    expect(onShortcutKeydown(at({ key: 'ArrowUp' }))).toBe(false)
    expect(onShortcutKeydown(at({ key: 'Home' }))).toBe(false)
    expect(onShortcutKeydown(at({ key: 'f', ctrlKey: true }))).toBe(true)
    expect(seen).toEqual(['find'])
    // Esc still blurs the field first
    expect(onShortcutKeydown(at({ key: 'Escape' }))).toBe(true)
    expect(document.activeElement).not.toBe(input)
    // With focus outside inputs the same keys dispatch again
    expect(onShortcutKeydown(key({ key: 'ArrowDown' }))).toBe(true)
    expect(onShortcutKeydown(key({ key: 'Home' }))).toBe(true)
    expect(seen).toEqual(['find', 'down', 'home'])
  })

  it('isolates unmodified keys in selects and arrow-driven inputs too', () => {
    const seen: string[] = []
    register(bind({ id: 'down', key: 'ArrowDown', run: () => { seen.push('down') } }))
    const select = document.createElement('select')
    const range = document.createElement('input')
    range.type = 'range'
    const radio = document.createElement('input')
    radio.type = 'radio'
    const box = document.createElement('input')
    box.type = 'checkbox' // arrows are not native to a checkbox: nav still fires
    document.body.append(select, range, radio, box)
    const at = (t: EventTarget) => {
      const ev = key({ key: 'ArrowDown' })
      Object.defineProperty(ev, 'target', { value: t })
      return ev
    }
    expect(onShortcutKeydown(at(select))).toBe(false)
    expect(onShortcutKeydown(at(range))).toBe(false)
    expect(onShortcutKeydown(at(radio))).toBe(false)
    expect(onShortcutKeydown(at(box))).toBe(true)
    expect(seen).toEqual(['down'])
  })

  it('ignores a key that is still composing', () => {
    const seen: string[] = []
    register(bind({ id: 'down', key: 'j', run: () => { seen.push('j') } }))
    const ev = key({ key: 'j', isComposing: true })
    expect(onShortcutKeydown(ev)).toBe(false)
    expect(ev.defaultPrevented).toBe(false)
    expect(seen).toEqual([])
  })

  it('does not claim a key whose handler returns false', () => {
    register(bind({ id: 'copy', key: 'c', ctrl: true, scope: 'input', run: () => false }))
    const input = document.createElement('input')
    document.body.appendChild(input)
    const ev = key({ key: 'c', ctrlKey: true })
    Object.defineProperty(ev, 'target', { value: input })
    expect(onShortcutKeydown(ev)).toBe(false)
    expect(ev.defaultPrevented).toBe(false)
  })

  it('manages top layer stack and reports overlayNow', () => {
    expect(overlayNow()).toBe(false)
    expect(closeTopRegisteredLayer()).toBe(false)

    const events: string[] = []
    const unregister1 = registerTopLayer(() => {
      events.push('layer1')
      return true
    })
    expect(overlayNow()).toBe(true)

    const unregister2 = registerTopLayer(() => {
      events.push('layer2')
      return true
    })
    expect(overlayNow()).toBe(true)

    // Topmost layer (layer2) closes first
    expect(closeTopRegisteredLayer()).toBe(true)
    expect(events).toEqual(['layer2'])

    unregister2()
    // Now layer1 is topmost
    expect(closeTopRegisteredLayer()).toBe(true)
    expect(events).toEqual(['layer2', 'layer1'])

    unregister1()
    expect(overlayNow()).toBe(false)
    expect(closeTopRegisteredLayer()).toBe(false)
  })
})
