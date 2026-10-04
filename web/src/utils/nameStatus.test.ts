import { describe, expect, it } from 'vitest'
import { parseNameStatus, unquotePath } from './nameStatus'

describe('parseNameStatus', () => {
  it('reads plain, rename and copy lines', () => {
    const v = parseNameStatus('M\tsrc/a.ts\nA\tnew.txt\nD\told.txt\nR087\tx/ふるい.txt\tx/あたらしい.txt\nC100\ta.ts\tb.ts\n')
    expect(v.header).toBe('')
    expect(v.entries).toEqual([
      { status: 'M', score: '', path: 'src/a.ts' },
      { status: 'A', score: '', path: 'new.txt' },
      { status: 'D', score: '', path: 'old.txt' },
      { status: 'R', score: '087', orig: 'x/ふるい.txt', path: 'x/あたらしい.txt' },
      { status: 'C', score: '100', orig: 'a.ts', path: 'b.ts' },
    ])
  })

  it('keeps the git show header, including indented message lines', () => {
    const out = [
      'commit 0123456789abcdef',
      'Author:     A <a@x>',
      '',
      '    fix: the thing',
      '',
      '    M\tlooks like an entry but is indented',
      '',
      'M\tweb/src/App.vue',
      'MM\tmerged.txt',
      '',
    ].join('\n')
    const v = parseNameStatus(out)
    expect(v.header).toContain('fix: the thing')
    expect(v.header).toContain('looks like an entry')
    expect(v.entries.map((e) => [e.status, e.path])).toEqual([['M', 'web/src/App.vue'], ['MM', 'merged.txt']])
  })
})

describe('unquotePath', () => {
  it('passes unquoted paths through', () => {
    expect(unquotePath('plain/path.txt')).toBe('plain/path.txt')
    expect(unquotePath('にほんご.txt')).toBe('にほんご.txt')
  })

  it('undoes C escapes and octal UTF-8 bytes', () => {
    expect(unquotePath('"a\\tb.txt"')).toBe('a\tb.txt')
    expect(unquotePath('"say \\"hi\\".txt"')).toBe('say "hi".txt')
    expect(unquotePath('"back\\\\slash"')).toBe('back\\slash')
    expect(unquotePath('"caf\\303\\251.txt"')).toBe('café.txt')
  })

  it('splits fields on real tabs only, so a quoted tab stays in its path', () => {
    const v = parseNameStatus('R100\t"a\\tb.txt"\tc.txt\n')
    expect(v.entries[0]).toMatchObject({ orig: 'a\tb.txt', path: 'c.txt' })
  })
})
