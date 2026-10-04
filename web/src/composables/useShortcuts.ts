import { ref } from 'vue'
import { hasSoftKeyboard } from '../utils/softKeyboard'
import { isStandalone } from './useHistoryTrap'

// One document keydown listener, every shortcut registered here. Dispatch
// rules live in docs/plans/desktop-keyboard-shortcuts.md §4.1.

export type ShortcutScope = 'global' | 'terminal' | 'overlay' | 'input'

export interface ShortcutDef {
  id: string
  label: string
  scope: ShortcutScope
  /** character or named key, compared to e.key */
  key?: string
  /** physical key, compared to e.code (Alt chords — macOS Option rewrites e.key) */
  code?: string
  /** Ctrl on other platforms, ⌘ on Mac */
  mod?: boolean
  ctrl?: boolean
  alt?: boolean
  shift?: boolean
  meta?: boolean
  when?: () => boolean
  /**
   * blocked: not bound in a browser tab (the browser keeps the key).
   * mac-blocked: same, but only on Mac.
   * linux-blocked: same, but only on Linux / ChromeOS, where Firefox and
   * Chrome use Alt+1…9 to pick a tab.
   * The entry stays in the help panel with blockedNote.
   */
  browserTab?: 'blocked' | 'mac-blocked' | 'linux-blocked'
  blockedNote?: string
  /** shown in help, never dispatched (the component already handles the key) */
  helpOnly?: boolean
  /** return false to let the key through (and keep looking) */
  run: (e: KeyboardEvent) => boolean | void
}

export interface ShortcutView {
  def: ShortcutDef
  chord: string
  /** set when the binding is registered but not active in this environment */
  unavailable: string | null
}

const bindings = new Map<string, ShortcutDef>()
export const shortcutVersion = ref(0)

let overlayOpen = () => false
let terminalActive = () => false
let listening = false
const overlaySources = new Set<() => boolean>()

/** A mounted surface (file preview, …) that should count as an overlay even
 *  when App has not installed its own context — unit tests mount it alone. */
export function addOverlaySource(get: () => boolean): () => void {
  overlaySources.add(get)
  return () => { overlaySources.delete(get) }
}

function overlayNow(): boolean {
  if (overlayOpen()) return true
  for (const get of overlaySources) if (get()) return true
  return false
}

/** Shell input actions owned by TermView, registered once from App. */
export const shellActions = {
  active: (): boolean => false,
  interrupt: (_e: KeyboardEvent): boolean => false,
  eof: (_e: KeyboardEvent): boolean => false,
  clear: (_e: KeyboardEvent): boolean => false,
}

export function setShortcutContext(ctx: { overlay: () => boolean; terminal: () => boolean }) {
  overlayOpen = ctx.overlay
  terminalActive = ctx.terminal
}

export function isMacPlatform(): boolean {
  if (typeof navigator === 'undefined') return false
  const p = navigator.platform || ''
  const ua = navigator.userAgent || ''
  return /Mac|iPhone|iPad|iPod/.test(p) || /Mac OS X/.test(ua)
}

export function isLinuxPlatform(): boolean {
  if (typeof navigator === 'undefined' || isMacPlatform()) return false
  const p = navigator.platform || ''
  const ua = navigator.userAgent || ''
  return /Linux|X11|CrOS/.test(p) || /Linux|X11|CrOS/.test(ua)
}

export function register(def: ShortcutDef): () => void {
  bindings.set(def.id, def)
  shortcutVersion.value++
  installShortcutListener()
  return () => {
    if (bindings.get(def.id) === def) {
      bindings.delete(def.id)
      shortcutVersion.value++
    }
  }
}

/** Drops every binding. Tests only — the listener itself stays installed. */
export function resetShortcutsForTests() {
  bindings.clear()
  overlaySources.clear()
  shellEnsured = false
  helpEnsured = false
  overlayOpen = () => false
  terminalActive = () => false
  shellActions.active = () => false
  shellActions.interrupt = () => false
  shellActions.eof = () => false
  shellActions.clear = () => false
  shortcutVersion.value++
}

export function installShortcutListener() {
  if (listening || typeof document === 'undefined') return
  listening = true
  document.addEventListener('keydown', onShortcutKeydown)
}

function isBareCharacter(def: ShortcutDef): boolean {
  if (def.mod || def.ctrl || def.alt || def.meta) return false
  return !!def.key && def.key.length === 1
}

/** Why this binding must not fire right now, or null when it may. */
export function shortcutBlock(def: ShortcutDef): string | null {
  if (def.browserTab === 'blocked' && !isStandalone()) {
    return def.blockedNote || 'A normal browser tab will not let this key through'
  }
  if (def.browserTab === 'mac-blocked' && isMacPlatform() && !isStandalone()) {
    return def.blockedNote || 'A normal browser tab on Mac will not let this key through'
  }
  if (def.browserTab === 'linux-blocked' && isLinuxPlatform() && !isStandalone()) {
    return def.blockedNote || 'In a normal browser tab on Linux this key belongs to the browser'
  }
  if (isBareCharacter(def) && hasSoftKeyboard()) return 'Bare character keys are disabled on touch screens'
  return null
}

export function isShortcutActive(id: string): boolean {
  const def = bindings.get(id)
  return !!def && !def.helpOnly && !shortcutBlock(def)
}

export function shortcutChord(id: string): string {
  const def = bindings.get(id)
  return def ? formatChord(def) : ''
}

export function formatChord(def: ShortcutDef): string {
  const parts: string[] = []
  if (def.mod) parts.push(isMacPlatform() ? '⌘' : 'Ctrl')
  if (def.ctrl) parts.push('Ctrl')
  if (def.alt) parts.push(isMacPlatform() ? '⌥' : 'Alt')
  if (def.meta) parts.push(isMacPlatform() ? '⌘' : 'Win')
  if (def.shift) parts.push('Shift')
  parts.push(chordKey(def))
  return parts.join('+')
}

function chordKey(def: ShortcutDef): string {
  if (def.key === 'Escape') return 'Esc'
  if (def.key === 'ArrowUp') return '↑'
  if (def.key === 'ArrowDown') return '↓'
  if (def.key === 'ArrowLeft') return '←'
  if (def.key === 'ArrowRight') return '→'
  if (def.key === 'Enter') return 'Enter'
  if (def.key) return def.key.length === 1 ? def.key.toUpperCase() : def.key
  switch (def.code) {
    case 'BracketLeft': return '['
    case 'BracketRight': return ']'
    case 'Comma': return ','
    default:
      if (def.code?.startsWith('Digit')) return def.code.slice(5)
      return def.code || ''
  }
}

export function listShortcuts(): ShortcutView[] {
  // touch shortcutVersion so computed help panels re-read after register()
  void shortcutVersion.value
  return [...bindings.values()].map((def) => ({
    def,
    chord: formatChord(def),
    unavailable: def.helpOnly ? null : shortcutBlock(def),
  }))
}

export function isTextInput(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false
  if (el instanceof HTMLTextAreaElement) return !el.readOnly
  if (el instanceof HTMLInputElement) {
    return !el.readOnly && !['button', 'checkbox', 'radio', 'submit', 'reset', 'range', 'file'].includes(el.type)
  }
  return el.isContentEditable
}

function modifiersMatch(e: KeyboardEvent, def: ShortcutDef): boolean {
  const wantMod = !!def.mod
  if (wantMod) {
    const ok = isMacPlatform() ? e.metaKey : e.ctrlKey
    if (!ok) return false
    if (isMacPlatform() && e.ctrlKey && !def.ctrl) return false
    if (!isMacPlatform() && e.metaKey && !def.meta) return false
  } else if ((e.ctrlKey && !def.ctrl) || (e.metaKey && !def.meta)) {
    return false
  }
  if (!!def.ctrl !== e.ctrlKey && !(wantMod && !isMacPlatform())) return false
  if (!!def.meta !== e.metaKey && !(wantMod && isMacPlatform())) return false
  if (!!def.alt !== e.altKey) return false
  // Letters and named keys treat Shift as its own modifier. A symbol key
  // (`?`, `/`) already is the shifted or unshifted character.
  const letter = !!def.key && def.key.length === 1 && def.key.toLowerCase() !== def.key.toUpperCase()
  if (def.code || def.shift || !def.key || def.key.length > 1 || letter) {
    if (!!def.shift !== e.shiftKey) return false
  }
  return true
}

function keyMatch(e: KeyboardEvent, def: ShortcutDef): boolean {
  if (def.code) return e.code === def.code
  if (!def.key) return false
  if (def.key.length === 1) return e.key.toLowerCase() === def.key.toLowerCase()
  return e.key === def.key
}

function scopesFor(e: KeyboardEvent): ShortcutScope[] {
  const input = isTextInput(e.target)
  if (overlayNow()) return input ? ['input', 'overlay'] : ['overlay']
  if (input) return ['input', 'terminal', 'global']
  if (terminalActive()) return ['terminal', 'global']
  return ['global']
}

function allowed(def: ShortcutDef, scopes: ShortcutScope[], e: KeyboardEvent): boolean {
  if (def.helpOnly || shortcutBlock(def)) return false
  if (def.scope === 'global' && def.key === 'Escape' && !def.mod && !def.ctrl && !def.alt && !def.meta) {
    // Esc still closes an overlay, or blurs an input before that.
    if (scopes.includes('overlay') || scopes.includes('global') || scopes.includes('input')) return true
  }
  if (!scopes.includes(def.scope)) return false
  if (isTextInput(e.target) && isBareCharacter(def)) return false
  return true
}

let shellEnsured = false

/** Shell chords stay registered for the help panel; TermView fills in shellActions. */
export function ensureShellShortcuts() {
  if (shellEnsured) return
  shellEnsured = true
  register({
    id: 'shell.interrupt',
    label: 'Send interrupt and clear the draft',
    scope: 'input',
    key: 'c',
    ctrl: true,
    when: () => shellActions.active(),
    run: (e) => shellActions.interrupt(e),
  })
  register({
    id: 'shell.eof',
    label: 'Send Ctrl+D',
    scope: 'input',
    key: 'd',
    ctrl: true,
    browserTab: 'blocked',
    blockedNote: 'In a normal browser tab, Ctrl/⌘+D bookmarks the page. Use ShellKeys',
    when: () => shellActions.active(),
    run: (e) => shellActions.eof(e),
  })
  register({
    id: 'shell.clear',
    label: 'Send Ctrl+L',
    scope: 'input',
    key: 'l',
    ctrl: true,
    browserTab: 'blocked',
    blockedNote: 'In a normal browser tab, Ctrl/⌘+L focuses the address bar. Use ShellKeys',
    when: () => shellActions.active(),
    run: (e) => shellActions.clear(e),
  })
}

let helpEnsured = false

/** Chords whose keydown already lives on the control; help still lists them. */
export function ensureChordHelp() {
  if (helpEnsured) return
  helpEnsured = true
  const submit = (id: string, label: string, scope: ShortcutDef['scope']) => register({
    id, label, scope, key: 'Enter', mod: true, helpOnly: true, run: () => {},
  })
  submit('input.submit', 'Send', 'input')
  register({ id: 'input.newline', label: 'New line in the input', scope: 'input', key: 'Enter', shift: true, helpOnly: true, run: () => {} })
  submit('todo.add', 'Add todo', 'input')
  submit('git.commit', 'Commit', 'overlay')
}

/** Dispatch one keydown. Returns true when a shortcut handled it. */
export function onShortcutKeydown(e: KeyboardEvent): boolean {
  if (e.isComposing || e.key === 'Process') return false
  if (e.defaultPrevented) return false
  const scopes = scopesFor(e)
  for (const scope of scopes) {
    for (const def of bindings.values()) {
      if (def.scope !== scope && !(scope === 'overlay' && def.key === 'Escape' && def.scope === 'global')) continue
      if (!allowed(def, scopes, e)) continue
      if (!modifiersMatch(e, def) || !keyMatch(e, def)) continue
      if (def.when && !def.when()) continue
      const taken = def.run(e)
      if (taken === false) continue
      e.preventDefault()
      return true
    }
  }
  return false
}
