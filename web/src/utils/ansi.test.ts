import { describe, it, expect } from 'vitest'
import { ansiToHtml, ansiToHtmlWith, stripAnsi, ansi256ToCss, STANDARD_COLORS } from './ansi'

describe('stripAnsi', () => {
  it('removes SGR sequences', () => {
    expect(stripAnsi('\x1b[31mred\x1b[0m')).toBe('red')
  })
  it('removes OSC sequences', () => {
    expect(stripAnsi('\x1b]0;title\x07text')).toBe('text')
  })
})

describe('ansi256ToCss', () => {
  it('maps standard colors', () => {
    expect(ansi256ToCss(1)).toBe(STANDARD_COLORS[1])
  })
  it('maps color cube', () => {
    expect(ansi256ToCss(196)).toBe('rgb(255,0,0)') // 16 + 180 => r=255,g=0,b=0
  })
  it('maps grayscale', () => {
    expect(ansi256ToCss(255)).toBe('rgb(238,238,238)') // 8 + 23*10
  })
})

describe('ansiToHtml', () => {
  it('passes plain text through, escaping HTML', () => {
    expect(ansiToHtml('a <b> & c')).toBe('a &lt;b&gt; &amp; c')
  })

  it('renders 16-color foreground', () => {
    expect(ansiToHtml('\x1b[31mred\x1b[0m')).toBe(
      `<span style="color:${STANDARD_COLORS[1]}">red</span>`,
    )
  })

  it('renders bright foreground', () => {
    expect(ansiToHtml('\x1b[91mbright\x1b[0m')).toBe(
      `<span style="color:${STANDARD_COLORS[9]}">bright</span>`,
    )
  })

  it('renders bold', () => {
    expect(ansiToHtml('\x1b[1mbold\x1b[0m')).toBe('<span style="font-weight:700">bold</span>')
  })

  it('renders combined styles in one span', () => {
    const html = ansiToHtml('\x1b[1;32mgreenbold\x1b[0m')
    expect(html).toContain('color:' + STANDARD_COLORS[2])
    expect(html).toContain('font-weight:700')
    expect(html).toContain('greenbold')
  })

  it('renders 256-color', () => {
    expect(ansiToHtml('\x1b[38;5;196mred\x1b[0m')).toBe('<span style="color:rgb(255,0,0)">red</span>')
  })

  it('renders truecolor', () => {
    expect(ansiToHtml('\x1b[38;2;10;20;30mc\x1b[0m')).toBe('<span style="color:rgb(10,20,30)">c</span>')
  })

  it('handles inverse by swapping fg/bg', () => {
    const html = ansiToHtml('\x1b[7minv\x1b[0m')
    expect(html).toContain('background-color:var(--term-fg)')
    expect(html).toContain('color:var(--term-bg)')
  })

  it('marks background spans with the has-bg class', () => {
    const html = ansiToHtml('\x1b[41mred bg\x1b[0m')
    expect(html).toBe(`<span class="has-bg" style="background-color:${STANDARD_COLORS[1]}">red bg</span>`)
  })

  it('marks inverse spans with the has-bg class', () => {
    const html = ansiToHtml('\x1b[7minv\x1b[0m')
    expect(html).toContain('class="has-bg"')
  })

  it('does not mark foreground-only spans', () => {
    const html = ansiToHtml('\x1b[31mred\x1b[0m')
    expect(html).not.toContain('has-bg')
  })

  it('drops cursor-movement and other non-SGR CSI', () => {
    expect(ansiToHtml('a\x1b[2Jb\x1b[Hc')).toBe('abc')
  })

  it('drops OSC sequences', () => {
    expect(ansiToHtml('\x1b]0;title\x07plain')).toBe('plain')
  })

  it('switches spans on style change without nesting', () => {
    const html = ansiToHtml('\x1b[31mred\x1b[32mgreen\x1b[0m')
    expect(html).toBe(
      `<span style="color:${STANDARD_COLORS[1]}">red</span><span style="color:${STANDARD_COLORS[2]}">green</span>`,
    )
  })

  it('keeps underline and strikethrough', () => {
    const html = ansiToHtml('\x1b[4;9mux\x1b[0m')
    expect(html).toContain('text-decoration:underline line-through')
  })
})

describe('ansiToHtmlWith', () => {
  it('decorates escaped text segments', () => {
    const link = (s: string) => s.replace(/foo/g, '<b>foo</b>')
    const html = ansiToHtmlWith('\x1b[31mfoo bar\x1b[0m', link)
    expect(html).toBe(`<span style="color:${STANDARD_COLORS[1]}"><b>foo</b> bar</span>`)
  })
})
