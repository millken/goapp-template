import { beforeEach, describe, expect, it } from 'vitest'
import { createScroll, type ScrollHost } from './scroll'

/** A ScrollHost with writable state, so a test can arrange a history entry. */
interface FakeHost extends ScrollHost {
  scrollY: number
}

function fakeWindow(): FakeHost {
  const w: FakeHost = {
    scrollY: 0,
    history: {
      state: null,
      replaceState: (s: unknown) => {
        w.history.state = s
      },
    },
    scrollTo: (_x: number, y: number) => {
      w.scrollY = y
    },
  }
  return w
}

beforeEach(() => {
  document.body.innerHTML = ''
})

describe('scroll', () => {
  it('records the current offset into the history entry being left', () => {
    const w = fakeWindow()
    w.scrollTo(0, 420)
    const s = createScroll(w)

    s.save()

    expect((w.history.state as { scrollY: number }).scrollY).toBe(420)
  })

  it('restores the offset stored on the entry being returned to', () => {
    const w = fakeWindow()
    w.history.state = { pjax: true, scrollY: 250 }
    const s = createScroll(w)

    s.restore()

    expect(w.scrollY).toBe(250)
  })

  it('goes to the top when the entry carries no offset', () => {
    const w = fakeWindow()
    w.scrollTo(0, 999)
    w.history.state = { pjax: true }
    const s = createScroll(w)

    s.restore()

    expect(w.scrollY).toBe(0)
  })

  // save() must not destroy the marker the navigator relies on.
  it('preserves the rest of the history state when saving', () => {
    const w = fakeWindow()
    w.history.state = { pjax: true }
    w.scrollTo(0, 10)
    const s = createScroll(w)

    s.save()

    expect(w.history.state).toEqual({ pjax: true, scrollY: 10 })
  })

  it('tolerates a null history state', () => {
    const w = fakeWindow()
    const s = createScroll(w)
    expect(() => s.restore()).not.toThrow()
    expect(w.scrollY).toBe(0)
  })
})
