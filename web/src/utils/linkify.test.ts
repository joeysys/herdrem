import { describe, it, expect } from 'vitest'
import { linkifyFilePaths } from './linkify'

function hrefs(html: string): string[] {
  return [...html.matchAll(/data-fp="([^"]+)"/g)].map((m) => decodeURIComponent(m[1]))
}

describe('linkifyFilePaths', () => {
  it('links an absolute path', () => {
    const html = linkifyFilePaths('open /home/user/app.ts now')
    expect(hrefs(html)).toContain('/home/user/app.ts')
  })

  it('links a relative path and strips ./', () => {
    const html = linkifyFilePaths('see ./src/main.go')
    expect(hrefs(html)).toContain('src/main.go')
  })

  it('links a home-relative path', () => {
    const html = linkifyFilePaths('cat ~/notes.md')
    expect(hrefs(html)).toContain('~/notes.md')
  })

  it('links an @-alias path', () => {
    const html = linkifyFilePaths('import @/components/Foo.vue')
    expect(hrefs(html)).toContain('@/components/Foo.vue')
  })

  it('links a bare filename with a known extension', () => {
    const html = linkifyFilePaths('edit config.yaml please')
    expect(hrefs(html)).toContain('config.yaml')
  })

  it('captures a :line suffix', () => {
    const html = linkifyFilePaths('error in src/app.ts:42')
    expect(html).toContain('data-line="42"')
    expect(hrefs(html)).toContain('src/app.ts') // :line stripped from data-fp
  })

  it('does not link unknown extensions', () => {
    const html = linkifyFilePaths('run setup.exe now')
    expect(html).not.toContain('term-file-link')
  })

  it('does not link plain words without extensions', () => {
    const html = linkifyFilePaths('the quick brown fox')
    expect(html).not.toContain('term-file-link')
  })

  it('handles CJK punctuation as boundary', () => {
    // Full-width parens are CJK punctuation boundaries.
    const html = linkifyFilePaths('（src/util.ts）')
    expect(hrefs(html)).toContain('src/util.ts')
  })

  it('links multiple paths on one line', () => {
    const html = linkifyFilePaths('diff a.go b.go')
    const found = hrefs(html)
    expect(found).toContain('a.go')
    expect(found).toContain('b.go')
  })

  it('preserves escaped entities around paths', () => {
    const html = linkifyFilePaths('if (x &lt; y) see src/a.ts')
    expect(html).toContain('&lt;')
    expect(hrefs(html)).toContain('src/a.ts')
  })

  it('links path at start of string', () => {
    const html = linkifyFilePaths('src/index.ts is here')
    expect(hrefs(html)).toContain('src/index.ts')
  })

  it('does not link unknown extensions like .deny', () => {
    const html = linkifyFilePaths('/etc/hosts.deny note')
    expect(html).not.toContain('term-file-link')
  })

  it('filters out paths not in validPaths set', () => {
    const valid = new Set(['/home/user/app.ts'])
    const html = linkifyFilePaths('see /home/user/app.ts and /home/user/missing.py', valid)
    const found = hrefs(html)
    expect(found).toContain('/home/user/app.ts')
    expect(found).not.toContain('/home/user/missing.py')
  })

  it('disables all links when validPaths is empty set', () => {
    const valid = new Set<string>()
    const html = linkifyFilePaths('see /home/user/app.ts and /home/user/missing.py', valid)
    expect(html).not.toContain('term-file-link')
  })

  it('handles CJK punctuation as path boundaries', () => {
    const html = linkifyFilePaths('「Output docs：docs/agents/reviews/review-mdm-20260803.md」')
    expect(hrefs(html)).toContain('docs/agents/reviews/review-mdm-20260803.md')

    const html2 = linkifyFilePaths('（src/main.go）')
    expect(hrefs(html2)).toContain('src/main.go')
  })

  it('links bare filenames and maps them to full relative target paths in validPaths', () => {
    const valid = new Set([
      'docs/agents/reviews/turn-fix-mdm-20260807.md',
      'turn-fix-mdm-20260807.md',
      'docs/agents/reviews/turn-claim-mdm-20260807.md',
      'turn-claim-mdm-20260807.md',
    ])
    const input = 'Consumed docs: docs/agents/reviews/turn-fix-mdm-20260807.md、turn-claim-mdm-20260807.md'
    const html = linkifyFilePaths(input, valid)
    const found = hrefs(html)
    expect(found).toContain('docs/agents/reviews/turn-fix-mdm-20260807.md')
    expect(found).toContain('docs/agents/reviews/turn-claim-mdm-20260807.md')
  })

  it('links a path fragment split before its extension to the validated full path', () => {
    const valid = new Set(['src/store/menus.store.ts'])
    const html = linkifyFilePaths('src/store/menus.store', valid)

    expect(hrefs(html)).toContain('src/store/menus.store.ts')
  })
})
