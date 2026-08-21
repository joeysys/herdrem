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
