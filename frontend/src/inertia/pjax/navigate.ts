// The PJAX state machine: fetch a URL, classify the answer, mount the view and
// write history. Everything it touches is injected, so the whole thing can be
// driven from a test without a browser navigation.

import { INERTIA_VIEW_KEY } from '../constants'

/** What started this navigation. History semantics depend on it. */
export type Trigger = 'link' | 'form' | 'popstate' | 'boot'

export type HistoryOp = 'push' | 'replace' | 'none'

/** A redirect chain longer than this is a server bug, not a navigation. */
export const MAX_REDIRECTS = 5

export interface Deps {
  fetch: typeof fetch
  /** The URL currently shown in the address bar. */
  currentUrl(): string
  pushState(url: string): void
  replaceState(url: string): void
  /** Leave the SPA entirely — the fallback whenever PJAX cannot proceed. */
  hardNavigate(url: string): void
  mount(view: string, props: Record<string, unknown>): Promise<void>
  progressStart(): void
  progressDone(): void
  saveScroll(): void
  restoreScroll(): void
}

export interface VisitOptions {
  trigger: Trigger
  method?: string
  body?: BodyInit
}

/**
 * Decides how a settled navigation records itself.
 *
 * Deliberately keyed on the *result* rather than on what the caller intended: a
 * form submit cannot know in advance whether the server will re-render or
 * redirect, so any mode chosen up front is wrong for one of the two outcomes.
 *
 * The same-URL check is what makes a failed login replace rather than push —
 * you submitted from that URL and you are still on it — while a GET search
 * form, which lands somewhere new, correctly pushes.
 */
export function historyOp(
  trigger: Trigger,
  redirected: boolean,
  targetUrl: string,
  currentUrl: string,
): HistoryOp {
  if (trigger === 'boot') return 'replace'
  // Back/forward already has its entry; a redirect still has to correct the URL,
  // or the address bar would name one page while another is on screen.
  if (trigger === 'popstate') return redirected ? 'replace' : 'none'
  return targetUrl === currentUrl ? 'replace' : 'push'
}

interface Payload {
  redirect?: string
  [key: string]: unknown
}

export function createNavigator(deps: Deps) {
  let inFlight: AbortController | null = null

  async function request(url: string, opts: VisitOptions, signal: AbortSignal): Promise<Payload> {
    const res = await deps.fetch(url, {
      method: opts.method ?? 'GET',
      body: opts.body,
      signal,
      headers: {
        // The marker the server switches on. Case is irrelevant on the wire,
        // but matching the server's spelling keeps greps honest.
        'X-Pjax': 'true',
        Accept: 'application/json',
      },
      // Same-origin only, and the session cookie must ride along.
      credentials: 'same-origin',
    })
    if (!res.ok) throw new Error(`pjax: ${res.status}`)
    return (await res.json()) as Payload
  }

  async function visit(url: string, opts: VisitOptions): Promise<void> {
    inFlight?.abort()
    const controller = new AbortController()
    inFlight = controller

    // Not on popstate: we have already arrived at that entry, so saving now
    // would stamp the outgoing viewport's offset over the one restore() needs.
    if (opts.trigger !== 'popstate') deps.saveScroll()
    deps.progressStart()

    try {
      let target = url
      let redirected = false

      for (let hop = 0; hop <= MAX_REDIRECTS; hop++) {
        // Only the first request carries the form body; a redirect is a GET.
        const payload = await request(target, hop === 0 ? opts : { trigger: opts.trigger }, controller.signal)

        if (typeof payload.redirect === 'string') {
          target = new URL(payload.redirect, deps.currentUrl()).href
          redirected = true
          continue
        }

        const { [INERTIA_VIEW_KEY]: view, ...props } = payload
        if (typeof view !== 'string' || view === '') {
          // A 200 with no view means the server and client disagree about the
          // protocol. Reloading shows whatever the server really meant.
          throw new Error('pjax: response names no view')
        }

        const op = historyOp(opts.trigger, redirected, target, deps.currentUrl())
        await deps.mount(view, props as Record<string, unknown>)
        if (op === 'push') deps.pushState(target)
        else if (op === 'replace') deps.replaceState(target)

        if (opts.trigger === 'popstate') deps.restoreScroll()
        return
      }

      throw new Error('pjax: too many redirects')
    } catch (err) {
      // An abort is this module cancelling itself; the newer navigation owns the
      // outcome, so there is nothing to report or fall back to.
      if (controller.signal.aborted) return
      console.error('pjax navigation failed, falling back to a full load', err)
      deps.hardNavigate(url)
    } finally {
      if (inFlight === controller) inFlight = null
      deps.progressDone()
    }
  }

  return { visit }
}
