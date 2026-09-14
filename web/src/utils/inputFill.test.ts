import { describe, expect, it } from 'vitest'
import { insertSnippet, prependSnippet } from './inputFill'

describe('insertSnippet (append/cursor fill)', () => {
  it('fills an empty input without padding', () => {
    expect(insertSnippet('', null, 'fix the bug')).toEqual({ text: 'fix the bug', cursor: 11 })
  })

  it('appends at end with a space when cursor is unknown', () => {
    expect(insertSnippet('ls -la', null, 'src/foo.ts')).toEqual({ text: 'ls -la src/foo.ts', cursor: 17 })
  })

  it('does not double a space already present at the tail', () => {
    expect(insertSnippet('ls ', null, 'foo')).toEqual({ text: 'ls foo', cursor: 6 })
  })

  it('inserts at the cursor position with both-side padding', () => {
    expect(insertSnippet('abc ghi', 4, 'def')).toEqual({ text: 'abc def ghi', cursor: 8 })
  })

  it('pads only the missing side at a boundary', () => {
    expect(insertSnippet('abc  ghi', 4, 'def')).toEqual({ text: 'abc def ghi', cursor: 7 })
  })

  it('respects an explicit cursor at position 0 (cursor lands after the snippet + padding)', () => {
    expect(insertSnippet('tail', 0, 'head')).toEqual({ text: 'head tail', cursor: 5 })
  })

  it('clamps an out-of-range cursor to the end', () => {
    expect(insertSnippet('ab', 99, 'cd')).toEqual({ text: 'ab cd', cursor: 5 })
  })
})

describe('prependSnippet (command chip fill)', () => {
  it('prepends a command before existing text with one space', () => {
    expect(prependSnippet('fix this', '/review')).toEqual({ text: '/review fix this', cursor: 8 })
  })

  it('always leaves a trailing space on an empty input', () => {
    expect(prependSnippet('', 'git status')).toEqual({ text: 'git status ', cursor: 11 })
  })

  it('trims snippet whitespace instead of doubling separators', () => {
    expect(prependSnippet('x', ' /commit  ')).toEqual({ text: '/commit x', cursor: 8 })
  })
})
