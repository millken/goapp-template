import { mountView } from './view-loader'
import { INERTIA_VIEW_KEY } from './constants'

interface PjaxState {
  pjaxUrl: string
  pjaxData: Record<string, any>
}

interface PjaxResponse {
  redirect?: string
  [key: string]: any
}

interface PjaxAnchor extends HTMLAnchorElement {
  __pjaxBound?: boolean
}

// Link.vue imports this module, so a page that uses <InertiaLink> drags it into
// the SSR bundle, where `window` does not exist. Resolve everything
// window-derived once behind a guard so the rest of the file can use it freely.
const win: Window = typeof window !== 'undefined' ? window : ({} as Window)
const supported: boolean = !!win.history?.pushState
const origin: string = win.location?.origin ?? ''

let popstateBound = false

/** Install the global popstate listener for PJAX. Call once from boot(). */
export function enablePjax(): void {
  if (!supported || popstateBound) return
  win.addEventListener('popstate', onPopState)
  popstateBound = true
}

export function pjaxClick(el: PjaxAnchor): { destroy(): void } | undefined {
  const href = el.getAttribute('href')
  if (
    !supported ||
    !el.href ||
    !href ||
    href.startsWith('#') ||
    !sameWindowOrigin(el.target, el.href) ||
    el.__pjaxBound
  ) {
    return
  }

  el.addEventListener('click', handleClick, true)
  el.__pjaxBound = true
  return {
    destroy() {
      el.removeEventListener('click', handleClick, true)
      el.__pjaxBound = false
    },
  }
}

function onPopState(e: PopStateEvent): void {
  if (!e.state?.pjaxUrl) return
  const { [INERTIA_VIEW_KEY]: view, ...props } = e.state.pjaxData || {}
  void loadAndMountComponent(e.state.pjaxUrl, view, props)
}

async function handleClick(e: MouseEvent): Promise<void> {
  e.preventDefault()
  const el = e.currentTarget as HTMLAnchorElement | null
  if (!el) return

  try {
    // The same URL serves HTML and PJAX JSON, so bust the cache to make sure a
    // stored HTML response is not replayed for this request.
    const url = new URL(el.href)
    url.searchParams.set('_t', Date.now().toString())

    const response = await fetch(url.toString(), {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json',
        'X-PJAX': 'true',
        'X-Requested-With': 'XMLHttpRequest',
      },
    })

    if (!response.ok) {
      if (response.redirected) win.location.href = el.href
      return
    }

    const data: PjaxResponse = await response.json()
    const { [INERTIA_VIEW_KEY]: view, redirect, ...props } = data

    // Server-driven redirect takes precedence; do not render then unload.
    if (redirect) {
      win.location.href = redirect
      return
    }

    if (!view) {
      console.error('No view found in PJAX response')
      win.location.reload()
      return
    }

    await loadAndMountComponent(el.href, view, props)
    const state: PjaxState = { pjaxUrl: el.href, pjaxData: data }
    win.history.pushState(state, '', el.href)
    win.scrollTo(0, 0)
  } catch {
    win.location.href = el.href
  }
}

/** True when following `url` would stay in this window and on this origin. */
function sameWindowOrigin(target: string, url: string): boolean {
  const t = (target || '').toLowerCase()
  const staysInThisWindow =
    !t ||
    t === win.name ||
    t === '_self' ||
    (t === '_top' && win === win.top) ||
    (t === '_parent' && win === win.parent)

  return staysInThisWindow && url.startsWith(origin)
}

async function loadAndMountComponent(
  url: string,
  view: string | undefined,
  props: Record<string, any>,
): Promise<void> {
  if (!view) return
  try {
    await mountView(view, props)
  } catch (error) {
    console.error('Error loading component:', error)
    win.location.href = url
  }
}
