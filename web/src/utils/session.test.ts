import { describe, expect, it } from 'vitest'
import { insertIndex } from './session'

describe('insertIndex', () => {
  it('uses the pre-removal index and steps past the target when moving down', () => {
    expect(insertIndex(1, 0)).toBe(0)
    expect(insertIndex(0, 1)).toBe(2)
    expect(insertIndex(0, 0)).toBe(0)
  })
})
