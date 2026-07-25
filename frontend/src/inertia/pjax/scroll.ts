// Scroll position across history entries.
//
// The offset lives in history.state rather than in a module-level map so it
// survives a reload and a restored session — the same reason the browser keeps
// it there for ordinary navigations.

/** The shape this module reads and writes; the navigator owns `pjax`. */
export interface PjaxState {
  pjax?: true
  scrollY?: number
}

/**
 * The slice of `window` this module needs. Narrower than Window on purpose: it
 * documents the real dependency and lets a test pass a plain object instead of
 * casting a stub through `unknown`.
 */
export interface ScrollHost {
  readonly scrollY: number
  scrollTo(x: number, y: number): void
  history: {
    state: unknown
    replaceState(state: unknown, unused: string): void
  }
}

export interface Scroll {
  /** Stamp the current offset onto the entry being left. */
  save(): void
  /** Jump to the offset stored on the current entry, or to the top. */
  restore(): void
}

export function createScroll(win: ScrollHost): Scroll {
  return {
    save() {
      const state = (win.history.state ?? {}) as PjaxState
      win.history.replaceState({ ...state, scrollY: win.scrollY }, '')
    },
    restore() {
      const state = (win.history.state ?? {}) as PjaxState
      win.scrollTo(0, state.scrollY ?? 0)
    },
  }
}
