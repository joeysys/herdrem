import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { applyPwaNameToHtml, DEFAULT_PWA_NAME, MAX_PWA_NAME_LENGTH, resolvePwaName } from './pwa-name.ts'

describe('resolvePwaName', () => {
  it('defaults when unset, blank, or only control characters', () => {
    expect(resolvePwaName({})).toBe(DEFAULT_PWA_NAME)
    expect(resolvePwaName({ HERDREM_PWA_NAME: '   ' })).toBe(DEFAULT_PWA_NAME)
    expect(resolvePwaName({ HERDREM_PWA_NAME: '\n\t' })).toBe(DEFAULT_PWA_NAME)
  })

  it('turns tabs and newlines into spaces and drops other control characters', () => {
    expect(resolvePwaName({ HERDREM_PWA_NAME: '  Acme\nOps\t' })).toBe('Acme Ops')
    expect(resolvePwaName({ HERDREM_PWA_NAME: 'Ac\u0000me' })).toBe('Acme')
  })

  it('caps the name at 48 code points and trims a cut that lands on a space', () => {
    const long = '名'.repeat(MAX_PWA_NAME_LENGTH + 5)
    expect(Array.from(resolvePwaName({ HERDREM_PWA_NAME: long }))).toHaveLength(MAX_PWA_NAME_LENGTH)
    expect(Array.from(resolvePwaName({ HERDREM_PWA_NAME: '🙂'.repeat(60) }))).toHaveLength(MAX_PWA_NAME_LENGTH)
    expect(resolvePwaName({ HERDREM_PWA_NAME: 'x'.repeat(47) + ' tail' })).toBe('x'.repeat(47))
  })
})

describe('applyPwaNameToHtml', () => {
  it('replaces the document title and the apple home-screen title', () => {
    const html = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'index.html'), 'utf8')
    const out = applyPwaNameToHtml(html, `Acme & "Ops" <eu>`)
    expect(out).toContain('<title>Acme &amp; &quot;Ops&quot; &lt;eu&gt;</title>')
    expect(out).toContain('content="Acme &amp; &quot;Ops&quot; &lt;eu&gt;"')
    expect(out).not.toContain('<title>Herdrem</title>')
    expect(out).not.toContain('content="Herdrem"')
  })

  it('inserts $ sequences literally instead of as replace patterns', () => {
    const html = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'index.html'), 'utf8')
    const name = 'Cash$$ Ops $& $` $\' end'
    const escaped = 'Cash$$ Ops $&amp; $` $&#39; end'
    const out = applyPwaNameToHtml(html, name)
    expect(out).toContain(`<title>${escaped}</title>`)
    expect(out).toContain(`content="${escaped}"`)
    expect(out.match(/<title>/g)).toHaveLength(1)
    expect(out).not.toContain('<title><!DOCTYPE')
    expect(out).not.toContain('<title>Herdrem</title>')
    expect(out).not.toContain('content="Herdrem"')
  })

  it('refuses html that lost the markers', () => {
    expect(() => applyPwaNameToHtml('<title>Other</title>', 'Acme')).toThrow(/title markers/)
  })
})
