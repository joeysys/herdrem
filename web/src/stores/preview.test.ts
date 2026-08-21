import { describe, it, expect } from 'vitest'
import { formatPreview } from './preview'

describe('formatPreview', () => {
  it('strips ANSI and keeps last lines', () => {
    const raw = '\x1b[31mline1\x1b[0m\nline2\nline3'
    expect(formatPreview(raw)).toBe('line1\nline2\nline3')
  })
  it('drops blank lines and trailing whitespace', () => {
    expect(formatPreview('a  \n\n\nb\n')).toBe('a\nb')
  })
  it('limits to maxLines', () => {
    const raw = Array.from({ length: 20 }, (_, i) => `l${i}`).join('\n')
    const out = formatPreview(raw, 5)
    expect(out.split('\n')).toHaveLength(5)
    expect(out).toContain('l19')
  })
  it('handles empty input', () => {
    expect(formatPreview('')).toBe('')
  })
})
