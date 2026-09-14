import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref } from 'vue'
import { useDoneAutoMark } from './useDoneAutoMark'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('useDoneAutoMark', () => {
  it('marks after the delay while viewing a done pane', () => {
    const done = ref(true)
    const mark = vi.fn()
    const scope = effectScope()
    scope.run(() => useDoneAutoMark(done, ref(10000), ref('p1'), mark))

    vi.advanceTimersByTime(9999)
    expect(mark).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(mark).toHaveBeenCalledTimes(1)
    scope.stop()
  })

  it('cancels when the pane leaves done before the delay elapses', () => {
    const done = ref(true)
    const mark = vi.fn()
    const scope = effectScope()
    scope.run(() => useDoneAutoMark(done, ref(10000), ref('p1'), mark))

    vi.advanceTimersByTime(5000)
    done.value = false
    vi.advanceTimersByTime(20000)
    expect(mark).not.toHaveBeenCalled()
    scope.stop()
  })

  it('arms on a fresh done transition and honours a live delay change', () => {
    const done = ref(false)
    const delay = ref(10000)
    const mark = vi.fn()
    const scope = effectScope()
    scope.run(() => useDoneAutoMark(done, delay, ref('p1'), mark))

    vi.advanceTimersByTime(60000)
    expect(mark).not.toHaveBeenCalled()

    done.value = true
    vi.advanceTimersByTime(10000)
    expect(mark).toHaveBeenCalledTimes(1)

    // A shorter delay re-arms the timer for a subsequent done entry.
    done.value = false
    delay.value = 3000
    done.value = true
    vi.advanceTimersByTime(3000)
    expect(mark).toHaveBeenCalledTimes(2)
    scope.stop()
  })

  it('pauses while the page is hidden and resumes on visible', () => {
    const done = ref(true)
    const mark = vi.fn()
    const scope = effectScope()

    const hiddenSpy = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    scope.run(() => useDoneAutoMark(done, ref(10000), ref('p1'), mark))
    vi.advanceTimersByTime(60000)
    expect(mark).not.toHaveBeenCalled()

    hiddenSpy.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    vi.advanceTimersByTime(10000)
    expect(mark).toHaveBeenCalledTimes(1)

    hiddenSpy.mockRestore()
    scope.stop()
  })

  it('does nothing on scope dispose before the delay', () => {
    const mark = vi.fn()
    const scope = effectScope()
    scope.run(() => useDoneAutoMark(ref(true), ref(10000), ref('p1'), mark))
    scope.stop()
    vi.advanceTimersByTime(60000)
    expect(mark).not.toHaveBeenCalled()
  })

  it('restarts the delay when the pane identity changes while done', () => {
    // Mobile/non-split layout reuses one TermView and only swaps paneId — a
    // done→done switch must not fire the timer armed for the previous pane.
    const done = ref(true)
    const paneId = ref('pane-a')
    const marked: string[] = []
    const scope = effectScope()
    scope.run(() => useDoneAutoMark(done, ref(10000), paneId, () => marked.push(paneId.value)))

    vi.advanceTimersByTime(5000)
    paneId.value = 'pane-b'
    vi.advanceTimersByTime(5000)
    expect(marked).toEqual([]) // pane-b has only been open 5s of its 10s
    vi.advanceTimersByTime(5000)
    expect(marked).toEqual(['pane-b'])
    scope.stop()
  })
})
