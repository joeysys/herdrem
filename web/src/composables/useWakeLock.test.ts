import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { resetWakeLockForTests, useWakeLock, useWakeLockSession } from './useWakeLock'
import { safeStorage } from '../utils/storage'

const Harness = defineComponent({
  setup() {
    useWakeLockSession()
    return () => null
  },
})

let hidden = false

function setHidden(value: boolean) {
  hidden = value
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden })
}

function flush() {
  return new Promise((resolve) => setTimeout(resolve, 0))
}

function installLock(request = vi.fn(async () => ({
  release: releaseMock,
  addEventListener: () => {},
}))) {
  Object.defineProperty(navigator, 'wakeLock', { configurable: true, value: { request } })
  return request
}

const releaseMock = vi.fn(async () => {})

beforeEach(() => {
  hidden = false
  setHidden(false)
  releaseMock.mockClear()
  safeStorage.removeItem('herdrem_wake_lock')
  resetWakeLockForTests()
})

afterEach(() => {
  safeStorage.removeItem('herdrem_wake_lock')
  resetWakeLockForTests()
})

describe('useWakeLockSession', () => {
  it('does not request a lock when the switch is off', async () => {
    const request = installLock()
    const wrapper = mount(Harness)
    await flush()
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('requests while a terminal session is open and the switch is on', async () => {
    const request = installLock()
    const wrapper = mount(Harness)
    useWakeLock().setEnabled(true)
    await flush()
    expect(request).toHaveBeenCalledWith('screen')
    wrapper.unmount()
    await flush()
    expect(releaseMock).toHaveBeenCalled()
  })

  it('releases when the page hides and requests again when it is shown', async () => {
    safeStorage.setItem('herdrem_wake_lock', '1')
    resetWakeLockForTests()
    const request = installLock()
    const wrapper = mount(Harness)
    await flush()
    expect(request).toHaveBeenCalledTimes(1)

    setHidden(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await flush()
    expect(releaseMock).toHaveBeenCalledTimes(1)

    setHidden(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await flush()
    expect(request).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('releases immediately when the switch is turned off', async () => {
    safeStorage.setItem('herdrem_wake_lock', '1')
    resetWakeLockForTests()
    installLock()
    const wrapper = mount(Harness)
    await flush()
    useWakeLock().setEnabled(false)
    await flush()
    expect(releaseMock).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('ignores a rejected request and a missing wake lock', async () => {
    safeStorage.setItem('herdrem_wake_lock', '1')
    resetWakeLockForTests()
    const request = vi.fn().mockRejectedValue(new Error('denied'))
    installLock(request)
    const wrapper = mount(Harness)
    await flush()
    expect(request).toHaveBeenCalledTimes(1)
    wrapper.unmount()

    resetWakeLockForTests()
    Object.defineProperty(navigator, 'wakeLock', { configurable: true, value: undefined })
    const again = mount(Harness)
    await flush()
    again.unmount()
  })
})
