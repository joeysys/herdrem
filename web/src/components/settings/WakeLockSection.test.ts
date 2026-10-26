import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import WakeLockSection from './WakeLockSection.vue'
import { resetWakeLockForTests } from '../../composables/useWakeLock'
import { safeStorage } from '../../utils/storage'

beforeEach(() => {
  safeStorage.removeItem('herdrem_wake_lock')
  resetWakeLockForTests()
})

describe('WakeLockSection', () => {
  it('defaults off and stores the choice on this device', async () => {
    const wrapper = mount(WakeLockSection)
    const box = wrapper.get('input[type="checkbox"]')
    expect((box.element as HTMLInputElement).checked).toBe(false)
    expect(box.attributes('aria-label')).toBe('keep screen on')

    await box.setValue(true)
    expect(safeStorage.getItem('herdrem_wake_lock')).toBe('1')
    expect((box.element as HTMLInputElement).checked).toBe(true)

    await box.setValue(false)
    expect(safeStorage.getItem('herdrem_wake_lock')).toBeNull()
  })

  it('shows a stored on switch', () => {
    safeStorage.setItem('herdrem_wake_lock', '1')
    resetWakeLockForTests()
    const wrapper = mount(WakeLockSection)
    expect((wrapper.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true)
  })
})
