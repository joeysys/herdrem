import { describe, it, expect } from 'vitest'
import { tailWindow, computeTailWindow } from '../../utils/tailWindow'

describe('tailWindow', () => {
  it('returns full content when under the window', () => {
    expect(tailWindow('a\nb\nc', 200)).toBe('a\nb\nc')
  })
  it('keeps only the tail when over the window', () => {
    const raw = Array.from({ length: 300 }, (_, i) => `line${i}`).join('\n')
    const out = tailWindow(raw, 200)
    // 300 lines → keep the last 200: lines 100..299
    expect(out).toBe(Array.from({ length: 200 }, (_, i) => `line${i + 100}`).join('\n'))
  })
  it('preserves ANSI escapes in the tail', () => {
    // 250 lines (last line has no trailing newline) → keep last 200
    const lines = Array.from({ length: 250 }, (_, i) => `\x1b[31mred${i}\x1b[0m`)
    const raw = lines.join('\n')
    const out = tailWindow(raw, 200)
    const outLines = out.split('\n')
    expect(outLines.length).toBe(200)
    expect(outLines[0]).toBe('\x1b[31mred50\x1b[0m')
    expect(outLines[199]).toBe('\x1b[31mred249\x1b[0m')
  })
  it('returns full content for zero/negative maxLines', () => {
    const raw = 'a\nb\nc'
    expect(tailWindow(raw, 0)).toBe('a\nb\nc')
    expect(tailWindow(raw, -1)).toBe('a\nb\nc')
  })
  it('handles empty content', () => {
    expect(tailWindow('', 200)).toBe('')
  })
  it('handles content with trailing newline', () => {
    // 3 lines, last ends with \n → window of 2 keeps the last 2 lines + trailing \n
    const raw = 'a\nb\nc\n'
    expect(tailWindow(raw, 2)).toBe('b\nc\n')
  })
  it('replays active ANSI style at the cutoff boundary (F4)', () => {
    // A long-lived red style opens before the cutoff and resets after it.
    // The tail must re-establish the red state so rendering stays faithful.
    const raw = 'x'.repeat(50) + '\n' + '\x1b[31m' + Array.from({ length: 30 }, (_, i) => `red${i}`).join('\n') + '\x1b[0m\n' + 'end'
    // visual lines: 1 (xxx) + 30 (redN) + 1 (end) = 32
    const out = tailWindow(raw, 30)
    // 32 lines → keep last 30: drops the first 'xxx' line. The cut lands right
    // after 'xxx\n', i.e. at the ESC[31m start — no active style to replay.
    expect(out.startsWith('\x1b[31mred1\n')).toBe(true)
  })
  it('replays active style when the cutoff is mid-style (F4)', () => {
    // Style opens before the first (dropped) line and continues into the tail.
    const raw = '\x1b[32m' + Array.from({ length: 30 }, (_, i) => `green${i}`).join('\n') + '\x1b[0m'
    // 30 lines, window of 10 → drop first 20, keep green20..green29
    const out = tailWindow(raw, 10)
    expect(out.startsWith('\x1b[32mgreen20\n')).toBe(true)
  })
  it('replays long-lived 256-color and truecolor styles', () => {
    const raw256 = '\x1b[38;5;196m' + Array.from({ length: 30 }, (_, i) => `red${i}`).join('\n')
    const rawTrue = '\x1b[38;2;1;2;3m' + Array.from({ length: 30 }, (_, i) => `rgb${i}`).join('\n')
    expect(tailWindow(raw256, 10).startsWith('\x1b[38;5;196mred20\n')).toBe(true)
    expect(tailWindow(rawTrue, 10).startsWith('\x1b[38;2;1;2;3mrgb20\n')).toBe(true)
  })
})

describe('computeTailWindow offset (scroll compensation)', () => {
  it('reports dropped lines when windowing cuts', () => {
    const raw = Array.from({ length: 300 }, (_, i) => `line${i}`).join('\n')
    const { text, droppedLines } = computeTailWindow(raw, 200)
    expect(droppedLines).toBe(100)
    expect(text).toBe(Array.from({ length: 200 }, (_, i) => `line${i + 100}`).join('\n'))
  })
  it('reports zero offset when content fits the window', () => {
    const raw = 'a\nb\nc'
    const { text, droppedLines, droppedBytes } = computeTailWindow(raw, 200)
    expect(droppedLines).toBe(0)
    expect(droppedBytes).toBe(0)
    expect(text).toBe(raw)
  })
  it('reports zero offset for empty content', () => {
    const { droppedLines } = computeTailWindow('', 200)
    expect(droppedLines).toBe(0)
  })
})
