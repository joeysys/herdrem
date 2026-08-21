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
