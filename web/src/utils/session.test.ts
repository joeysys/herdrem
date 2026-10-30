import { describe, expect, it } from 'vitest'
import { defaultWorktreeName, insertIndex } from './session'

describe('insertIndex', () => {
  it('uses the pre-removal index and steps past the target when moving down', () => {
    expect(insertIndex(1, 0)).toBe(0)
    expect(insertIndex(0, 1)).toBe(2)
    expect(insertIndex(0, 0)).toBe(0)
  })
})

describe('defaultWorktreeName', () => {
  it('appends a suffix to the current space name', () => {
    expect(defaultWorktreeName('herdrem', 'a3f1')).toBe('herdrem-a3f1')
    expect(defaultWorktreeName('  ', 'a3f1')).toBe('worktree-a3f1')
  })

  it('keeps the suffix when the space name is already long', () => {
    expect(defaultWorktreeName('x'.repeat(200), 'a3f1')).toBe(`${'x'.repeat(195)}-a3f1`)
  })
})
