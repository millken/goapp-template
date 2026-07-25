import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createProgress, PROGRESS_DELAY_MS } from './progress'

const bar = () => document.querySelector('[data-pjax-progress]')

beforeEach(() => {
  vi.useFakeTimers()
  document.body.innerHTML = ''
})

afterEach(() => {
  vi.useRealTimers()
})

describe('progress', () => {
  // The whole point of the delay: a navigation that resolves in 30ms should not
  // flash a bar, which reads as jank rather than feedback.
  it('shows nothing for a navigation faster than the delay', () => {
    const p = createProgress(document)
    p.start()
    vi.advanceTimersByTime(PROGRESS_DELAY_MS - 1)
    expect(bar()).toBeNull()

    p.done()
    vi.advanceTimersByTime(1000)
    expect(bar()).toBeNull()
  })

  it('shows the bar once the navigation outlasts the delay', () => {
    const p = createProgress(document)
    p.start()
    vi.advanceTimersByTime(PROGRESS_DELAY_MS)
    expect(bar()).not.toBeNull()
  })

  it('removes the bar when the navigation finishes', () => {
    const p = createProgress(document)
    p.start()
    vi.advanceTimersByTime(PROGRESS_DELAY_MS)
    expect(bar()).not.toBeNull()

    p.done()
    expect(bar()).toBeNull()
  })

  // Rapid clicks call start() again while one is pending; the bar must not
  // accumulate, and the pending timer must not fire after done().
  it('does not stack bars across overlapping navigations', () => {
    const p = createProgress(document)
    p.start()
    p.start()
    vi.advanceTimersByTime(PROGRESS_DELAY_MS)
    expect(document.querySelectorAll('[data-pjax-progress]')).toHaveLength(1)

    p.done()
    expect(bar()).toBeNull()
  })

  it('is safe to finish without ever having started', () => {
    const p = createProgress(document)
    expect(() => p.done()).not.toThrow()
    expect(bar()).toBeNull()
  })
})
