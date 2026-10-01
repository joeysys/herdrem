import { describe, it, expect } from 'vitest'
import { applyHighlights, clearHighlights, matchesQuery, setActiveGroup, MAX_HITS } from './domHighlight'

function root(html: string): HTMLElement {
  const el = document.createElement('div')
  el.innerHTML = html
  return el
}

describe('applyHighlights', () => {
  it('wraps each match and returns one group per hit', () => {
    const r = root('foo bar foo')
    const groups = applyHighlights(r, 'foo')
    expect(groups).toHaveLength(2)
    expect(r.querySelectorAll('mark.term-hit')).toHaveLength(2)
    expect(r.textContent).toBe('foo bar foo')
  })

  it('finds a hit that straddles ANSI colour spans', () => {
    const r = root('git <span style="color:red">st</span>atus ok')
    const groups = applyHighlights(r, 'git status')
    expect(groups).toHaveLength(1)
    expect(groups[0].length).toBe(3)
    expect(groups[0].map((m) => m.textContent).join('')).toBe('git status')
    expect(r.textContent).toBe('git status ok')
  })

  it('handles adjacent hits inside one text node', () => {
    const r = root('abab')
    const groups = applyHighlights(r, 'ab')
    expect(groups.map((g) => g.map((m) => m.textContent).join(''))).toEqual(['ab', 'ab'])
    expect(r.textContent).toBe('abab')
  })

  it('is case-insensitive unless the query has an uppercase letter', () => {
    expect(applyHighlights(root('Error error ERROR'), 'error')).toHaveLength(3)
    expect(applyHighlights(root('Error error ERROR'), 'Error')).toHaveLength(1)
  })

  it('returns nothing for an empty query or no match', () => {
    expect(applyHighlights(root('abc'), '')).toEqual([])
    expect(applyHighlights(root('abc'), 'zzz')).toEqual([])
  })

  it('does not match across a newline', () => {
    expect(applyHighlights(root('foo\nbar'), 'foo bar')).toEqual([])
  })

  it('caps the number of highlighted hits', () => {
    const r = root('a'.repeat(MAX_HITS + 50))
    expect(applyHighlights(r, 'a')).toHaveLength(MAX_HITS)
  })

  it('keeps matches inside links', () => {
    const r = root('see <a href="#">src/main.go</a> now')
    const groups = applyHighlights(r, 'main')
    expect(groups).toHaveLength(1)
    expect(r.querySelector('a mark.term-hit')?.textContent).toBe('main')
  })
})

describe('clearHighlights / setActiveGroup', () => {
  it('restores the original DOM text and structure', () => {
    const r = root('git <span>st</span>atus ok')
    const before = r.innerHTML
    applyHighlights(r, 'git status')
    clearHighlights(r)
    expect(r.querySelectorAll('mark')).toHaveLength(0)
    expect(r.textContent).toBe('git status ok')
    expect(r.querySelectorAll('span')).toHaveLength(1)
    expect(r.innerHTML).toBe(before)
  })

  it('moves the active class between hits', () => {
    const r = root('x y x')
    const groups = applyHighlights(r, 'x')
    setActiveGroup(groups, 0)
    expect(groups[0][0].classList.contains('active')).toBe(true)
    expect(groups[1][0].classList.contains('active')).toBe(false)
    setActiveGroup(groups, 1)
    expect(groups[0][0].classList.contains('active')).toBe(false)
    expect(groups[1][0].classList.contains('active')).toBe(true)
  })

  it('returns null for an out-of-range active index', () => {
    expect(setActiveGroup([], 0)).toBeNull()
  })
})

describe('limit / matchesQuery / scoped clearing', () => {
  it('honours an explicit hit limit', () => {
    expect(applyHighlights(root('a a a a a'), 'a', 3)).toHaveLength(3)
    expect(applyHighlights(root('a a a'), 'a', 0)).toEqual([])
  })

  it('matchesQuery uses the same smart-case rule as the highlighter', () => {
    expect(matchesQuery('Hello World', 'hello')).toBe(true)
    expect(matchesQuery('Hello World', 'Hello')).toBe(true)
    expect(matchesQuery('hello world', 'Hello')).toBe(false)
    expect(matchesQuery('anything', '')).toBe(false)
  })

  it('only normalises text nodes next to removed marks', () => {
    const r = root('<p>keep</p><p>find me</p>')
    const p1 = r.querySelector('p')!
    p1.appendChild(document.createTextNode(' split')) // two adjacent text nodes, no mark here
    expect(p1.childNodes.length).toBe(2)
    applyHighlights(r, 'find')
    clearHighlights(r)
    expect(p1.childNodes.length).toBe(2) // untouched: no mark was ever in this element
    expect(r.querySelectorAll('mark')).toHaveLength(0)
    expect(r.textContent).toBe('keep splitfind me')
  })
})
