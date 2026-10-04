import { beforeEach, describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { useTermZoom } from './useTermZoom'
import { safeStorage } from '../utils/storage'

beforeEach(() => safeStorage.removeItem('herdrem_term_zoom'))

describe('useTermZoom', () => {
  it('drives the --term-zoom variable the terminal font sizes multiply', () => {
    const el = document.createElement('div')
    const z = useTermZoom(ref(el))
    z.bind(el)
    expect(el.style.getPropertyValue('--term-zoom')).toBe('1.000')
    z.setZoom(1.5)
    expect(el.style.getPropertyValue('--term-zoom')).toBe('1.500')
    // the old approach set font-size on this box, which the text never saw
    expect(el.style.fontSize).toBe('')
  })

  it('clamps and remembers the zoom', () => {
    const el = document.createElement('div')
    const z = useTermZoom(ref(el))
    z.setZoom(10)
    expect(z.zoom.value).toBe(2.5)
    z.setZoom(0.1)
    expect(z.zoom.value).toBe(0.6)
    z.setZoom(1.25)
    const again = useTermZoom(ref(document.createElement('div')))
    expect(again.zoom.value).toBe(1.25)
  })

  it('pinch-zooms with two fingers', () => {
    const el = document.createElement('div')
    const z = useTermZoom(ref(el))
    z.bind(el)
    const touches = (d: number) => [{ clientX: 0, clientY: 0 }, { clientX: d, clientY: 0 }]
    const fire = (type: string, list: unknown[]) => {
      const e = new Event(type, { cancelable: true }) as Event & { touches: unknown[] }
      Object.defineProperty(e, 'touches', { value: list })
      el.dispatchEvent(e)
    }
    fire('touchstart', touches(100))
    fire('touchmove', touches(150))
    expect(z.zoom.value).toBeCloseTo(1.5)
    expect(el.style.getPropertyValue('--term-zoom')).toBe('1.500')
  })

  it('a slow pinch still leaves 100%, though small values near it snap', () => {
    const el = document.createElement('div')
    const z = useTermZoom(ref(el))
    z.setZoom(1)
    z.bind(el)
    const touches = (d: number) => [{ clientX: 0, clientY: 0 }, { clientX: d, clientY: 0 }]
    const fire = (type: string, list: unknown[]) => {
      const e = new Event(type, { cancelable: true }) as Event & { touches: unknown[] }
      Object.defineProperty(e, 'touches', { value: list })
      el.dispatchEvent(e)
    }
    fire('touchstart', touches(200))
    fire('touchmove', touches(205)) // +2.5%: snaps to 100%
    expect(z.percent.value).toBe(100)
    for (let d = 210; d <= 240; d += 5) fire('touchmove', touches(d))
    expect(z.percent.value).toBe(120)
  })

  it('wheel steps land back on 100% and report a whole percentage', () => {
    const el = document.createElement('div')
    const z = useTermZoom(ref(el))
    z.setZoom(1)
    z.bind(el)
    const wheel = (deltaY: number) => el.dispatchEvent(new WheelEvent('wheel', { deltaY, ctrlKey: true, cancelable: true }))
    wheel(-1)
    expect(z.percent.value).toBe(110)
    wheel(1) // 1.1 × 0.9 = 0.99
    expect(z.zoom.value).toBe(1)
    expect(z.percent.value).toBe(100)
    wheel(1)
    expect(z.percent.value).toBe(90)
  })
})
