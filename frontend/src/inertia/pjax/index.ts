// Public entry: wires the pieces to the real browser.
//
// This is the only file in pjax/ that touches globals; the modules it composes
// take their dependencies as arguments so they stay testable.

import { formTarget, installDelegation } from './intercept'
import { createNavigator, type Trigger } from './navigate'
import { createProgress } from './progress'
import { createScroll, type PjaxState } from './scroll'

export interface PjaxOptions {
  /** Renders a view with its props — supplied by the view loader. */
  mount(view: string, props: Record<string, unknown>): Promise<void>
}

/**
 * Turns on PJAX for the whole document and returns a teardown.
 *
 * Every same-origin link and form is intercepted; `data-no-pjax` on an element
 * or any ancestor opts out. With JavaScript disabled or broken nothing here
 * runs, and the untouched href/action attributes still work.
 */
export function enablePjax(opts: PjaxOptions): () => void {
  const win = window
  const doc = document
  const progress = createProgress(doc)
  const scroll = createScroll(win)

  const nav = createNavigator({
    fetch: win.fetch.bind(win),
    currentUrl: () => win.location.href,
    pushState: (url) => win.history.pushState(marker(), '', url),
    replaceState: (url) => win.history.replaceState(marker(), '', url),
    hardNavigate: (url) => {
      win.location.href = url
    },
    mount: opts.mount,
    progressStart: progress.start,
    progressDone: progress.done,
    saveScroll: scroll.save,
    restoreScroll: scroll.restore,
  })

  const go = (url: string, trigger: Trigger, init: { method?: string; body?: BodyInit } = {}) =>
    void nav.visit(url, { trigger, ...init })

  // Seed the entry the page loaded on. Without this the first Back finds a null
  // state, and the old implementation bailed out of popstate entirely — the URL
  // reverted while the previous DOM stayed on screen.
  win.history.replaceState(marker(), '')

  const teardown = installDelegation(doc, win.location.origin, {
    onLink: (url) => go(url, 'link'),
    onForm: (form) => {
      const { url, method, body } = formTarget(form)
      go(url, 'form', { method, body })
    },
  })

  const onPopState = (e: PopStateEvent) => {
    // Entries this layer never wrote belong to someone else (an in-page anchor,
    // another library); leaving them alone is the safe default.
    if (!(e.state as PjaxState | null)?.pjax) return
    go(win.location.href, 'popstate')
  }
  win.addEventListener('popstate', onPopState)

  return () => {
    teardown()
    win.removeEventListener('popstate', onPopState)
  }
}

/** Preserves whatever scroll offset is already recorded on the entry. */
function marker(): PjaxState {
  const existing = (window.history.state ?? {}) as PjaxState
  return { ...existing, pjax: true }
}
