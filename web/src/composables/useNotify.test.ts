// Android Chrome (browser and installed PWA) throws on `new Notification()`;
// notifications must go through the service worker registration there.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useNotify } from './useNotify'
import type { Pane } from '../api/types'

const showNotification = vi.fn((_title: string, _opts?: NotificationOptions) => Promise.resolve())
const ctor = vi.fn()
const requestPermission = vi.fn(() => Promise.resolve('granted'))
let permission: NotificationPermission = 'granted'
let reg: { showNotification: typeof showNotification } | undefined
let hidden = false

beforeEach(() => {
  showNotification.mockClear()
  ctor.mockClear()
  requestPermission.mockClear()
  permission = 'granted'
  reg = { showNotification }
  hidden = false
  class FakeNotification {
    static get permission() { return permission }
    static requestPermission = requestPermission
    onclick: (() => void) | null = null
    constructor(title: string, opts: NotificationOptions) { ctor(title, opts) }
  }
  vi.stubGlobal('Notification', FakeNotification)
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: { getRegistration: () => Promise.resolve(reg) },
  })
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden })
})
afterEach(() => {
  vi.unstubAllGlobals()
  delete (navigator as unknown as { serviceWorker?: unknown }).serviceWorker
  delete (document as unknown as { hidden?: unknown }).hidden
})

const pane = (status: string): Pane =>
  ({ pane_id: 'p1', project: 'demo', status, kind: 'agent', last_active: 1 }) as unknown as Pane

const flush = () => new Promise((r) => setTimeout(r, 0))

describe('useNotify system notifications', () => {
  it('shows blocked through the service worker, not the constructor', async () => {
    const n = useNotify()
    n.processSnapshot([pane('working')])
    n.processSnapshot([pane('blocked')])
    await flush()
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0]).toEqual([
      '🔒 demo',
      expect.objectContaining({ tag: 'p1', data: { paneId: 'p1' } }),
    ])
    expect(ctor).not.toHaveBeenCalled()
  })

  it('falls back to the constructor without a service worker', async () => {
    reg = undefined
    const n = useNotify()
    n.processSnapshot([pane('working')])
    n.processSnapshot([pane('blocked')])
    await flush()
    expect(ctor).toHaveBeenCalledTimes(1)
  })

  it('notifies done only while the app is in the background', async () => {
    const n = useNotify()
    n.processSnapshot([pane('working')])
    n.processSnapshot([pane('done')])
    await flush()
    expect(showNotification).not.toHaveBeenCalled()

    hidden = true
    n.processSnapshot([pane('working')])
    n.processSnapshot([pane('done')])
    await flush()
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0]).toBe('✅ demo')
  })

  it('stays silent without permission', async () => {
    permission = 'denied'
    const n = useNotify()
    n.processSnapshot([pane('working')])
    n.processSnapshot([pane('blocked')])
    await flush()
    expect(showNotification).not.toHaveBeenCalled()
    expect(ctor).not.toHaveBeenCalled()
  })

  it('asks for permission on the first tap, not on load', () => {
    permission = 'default'
    useNotify().ensurePermission()
    expect(requestPermission).not.toHaveBeenCalled()
    document.dispatchEvent(new Event('pointerdown'))
    expect(requestPermission).toHaveBeenCalledTimes(1)
    document.dispatchEvent(new Event('pointerdown'))
    expect(requestPermission).toHaveBeenCalledTimes(1)
  })
})
