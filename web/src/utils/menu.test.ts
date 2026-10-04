import { describe, it, expect } from 'vitest'
import {
  parseNumberedMenu,
  detectOptions,
  detectMenu,
  cursorKeySequence,
  isStickyChoice,
  menuLabel,
} from './menu'

describe('parseNumberedMenu', () => {
  it('parses a claude confirm menu', () => {
    const { payloads, labels } = parseNumberedMenu('❯ 1. Yes\n  2. No')
    expect(payloads).toEqual(['1', '2'])
    expect(labels).toEqual(['Yes', 'No'])
  })
  it('rejects numbered prose', () => {
    const { payloads } = parseNumberedMenu('1. first\nprose at margin\n2. second')
    expect(payloads).toEqual([])
  })
  it('handles box borders and wrapped labels', () => {
    const { payloads } = parseNumberedMenu('│ ❯ 1. Yes, allow tmp/\n│      and more\n│   2. No │')
    expect(payloads).toEqual(['1', '2'])
  })
})

describe('detectOptions / detectMenu', () => {
  it('numbered menu wins', () => {
    expect(detectOptions('❯ 1. Yes\n  2. No')).toEqual(['1', '2'])
  })
  it('paren shortcuts', () => {
    expect(detectOptions('proceed? (y)es (n)o')).toEqual(['y', 'n'])
  })
  it('detectMenu returns labels only when matched', () => {
    const m = detectMenu('❯ 1. Yes\n  2. No')
    expect(m.labels).toEqual(['Yes', 'No'])
  })
})

describe('cursor helpers', () => {
  it('builds arrow+enter sequence', () => {
    expect(cursorKeySequence(2)).toEqual(['Down', 'Down', 'Enter'])
    expect(cursorKeySequence(0)).toEqual(['Enter'])
  })
})

describe('isStickyChoice / menuLabel', () => {
  it('detects sticky choices', () => {
    expect(isStickyChoice("Yes, and don't ask again")).toBe(true)
    expect(isStickyChoice('trust, always allow')).toBe(true)
    expect(isStickyChoice('No')).toBe(false)
  })
  it('shortens labels', () => {
    expect(menuLabel('Yes, and allow tmp/')).toBe('Yes')
    expect(menuLabel('No')).toBe('No')
  })
})

// Claude's AskUserQuestion: a rule before the last item used to drop the menu,
// and the (x)es fallback then turned prose letters into buttons.
const ASK_USER_QUESTION = `Some earlier output mentioning (e)dge cases,
a (0)-based index and the (v)erbose flag on separate lines.

 ☐ Conflict policy

❯ 1. Restore only Ctrl+R (Recommended)
     Give Ctrl/⌘+R back to the browser for refresh.
  2. Restore both Ctrl+R and Ctrl+F
     Return both keys to the browser.
  3. Remap Ctrl+R to a conflict-free key
     Use Alt+R for input history.
  4. Type something.
──────────────────────────────────────────────────────────────────
  5. Chat about this

Enter to select · ↑/↓ to navigate · Esc to cancel`

describe('AskUserQuestion menu', () => {
  it('reads all five items across the rule', () => {
    expect(detectMenu(ASK_USER_QUESTION)).toEqual({
      options: ['1', '2', '3', '4', '5'],
      labels: ['Restore only Ctrl+R (Recommended)', 'Restore both Ctrl+R and Ctrl+F', 'Remap Ctrl+R to a conflict-free key', 'Type something.', 'Chat about this'],
    })
  })

  it('takes (x)es shortcuts only from one prompt line', () => {
    expect(detectOptions('mentions (e)dge cases\nand the (v)erbose flag\n')).toEqual([])
    expect(detectOptions('earlier: (a)bc (b)cd\nProceed? (y)es (n)o')).toEqual(['y', 'n'])
  })
})
