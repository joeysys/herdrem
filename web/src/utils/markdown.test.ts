import { describe, it, expect } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  it('renders headings', () => {
    expect(renderMarkdown('# Title')).toContain('<h1')
    expect(renderMarkdown('## Sub')).toContain('<h2')
  })
  it('escapes html', () => {
    expect(renderMarkdown('a <b> c')).toContain('&lt;b&gt;')
  })
  it('renders bold/italic/code', () => {
    const html = renderMarkdown('**b** *i* `c`')
    expect(html).toContain('<strong>b</strong>')
    expect(html).toContain('<em>i</em>')
    expect(html).toContain('<code>c</code>')
  })
  it('renders links', () => {
    expect(renderMarkdown('[x](https://a.b)')).toContain('href="https://a.b"')
  })
  it('escapes quotes so link/image URLs cannot break out of attributes', () => {
    const html = renderMarkdown('![a](x"onerror="alert(1))')
    expect(html).toContain('src="x&quot;onerror=&quot;alert(1"')
    expect(html).not.toContain('onerror="')
  })
  it('drops javascript: link URLs', () => {
    const html = renderMarkdown('[x](javascript:alert(1))')
    expect(html).not.toContain('<a')
    expect(html).not.toContain('javascript:')
  })
  it('drops control-char-obfuscated javascript: URLs', () => {
    // WHATWG URL parsing strips leading C0 controls and removes tab/LF/CR
    // anywhere, so "\u0001javascript:" re-parses as javascript: in the browser.
    for (const u of [
      '\u0001javascript:alert(1)',
      '\u0000javascript:alert(1)',
      'java\u0001script:alert(1)',
      '\u007fjavascript:alert(1)',
      'data:text/html,<b>',
    ]) {
      const out = renderMarkdown(`[x](${u})`)
      expect(out, JSON.stringify(u)).not.toContain('<a')
      const img = renderMarkdown(`![x](${u})`)
      expect(img, JSON.stringify(u)).not.toContain('<img')
    }
  })
  it('emits the control-stripped URL, not the raw text', () => {
    expect(renderMarkdown('[x](a\u0001b)')).toContain('href="ab"')
  })
  it('drops non-image data: and javascript: image URLs', () => {
    expect(renderMarkdown('![a](javascript:alert(1))')).not.toContain('<img')
    expect(renderMarkdown('![a](data:text/html;base64,PGI+)')).not.toContain('<img')
    expect(renderMarkdown('![a](data:image/png;base64,iVBOR)')).toContain('src="data:image/png;base64,iVBOR"')
  })
  it('keeps scheme-less and whitelisted URLs', () => {
    expect(renderMarkdown('[r](./docs/a.md)')).toContain('href="./docs/a.md"')
    expect(renderMarkdown('[m](mailto:a@b.c)')).toContain('href="mailto:a@b.c"')
    expect(renderMarkdown('![i](//cdn.example/x.png)')).toContain('src="//cdn.example/x.png"')
  })
  it('renders unordered list', () => {
    const html = renderMarkdown('- one\n- two')
    expect(html).toContain('<ul>')
    expect(html).toContain('<li>one</li>')
  })
  it('renders task list', () => {
    const html = renderMarkdown('- [x] done\n- [ ] todo')
    expect(html).toContain('checked')
    expect(html).toContain('md-task')
  })
  it('renders fenced code', () => {
    const html = renderMarkdown('```go\npackage main\n```')
    expect(html).toContain('md-code')
    expect(html).toContain('data-lang="go"')
    expect(html).toContain('package main')
  })
  it('renders a table', () => {
    const html = renderMarkdown('| a | b |\n|---|---|\n| 1 | 2 |')
    expect(html).toContain('md-table')
    expect(html).toContain('<th>a</th>')
    expect(html).toContain('<td>1</td>')
  })
  it('renders frontmatter', () => {
    const html = renderMarkdown('---\ntitle: Hello\nseverity: P1\n---\n\nbody')
    expect(html).toContain('md-frontmatter')
    expect(html).toContain('Hello')
  })
  it('renders hr', () => {
    expect(renderMarkdown('---')).toContain('md-hr')
  })
})
