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

const win: Window = typeof window !== 'undefined' ? window : ({} as Window)
const doc: Document = win.document || ({} as Document)
const elementProto = (win as any).Element && (win as any).Element.prototype
const histRef = win.history
const supported: boolean = !!(elementProto && histRef && histRef.pushState)
const origin: string = win.location ? win.location.protocol + '//' + win.location.host : ''

let popstateBound = false

/** Install the global popstate listener for PJAX. Call once from boot(). */
export function enablePjax(): void {
  if (!supported || popstateBound) return
  win.addEventListener('popstate', onPopState)
  popstateBound = true
}

interface PjaxAnchor extends HTMLAnchorElement {
  __pjaxBound?: boolean
}

export function pjaxClick(el: PjaxAnchor): { destroy(): void } | undefined {
  const url = el.href
  const href = el.getAttribute('href')
  if (
    !supported ||
    !url ||
    !href ||
    href.startsWith('#') ||
    !sameWindowOrigin(el.target, url) ||
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
  if (e.state && e.state.pjaxUrl) {
    const data = e.state.pjaxData || {}
    const view = data[INERTIA_VIEW_KEY]
    const { [INERTIA_VIEW_KEY]: _omit, ...props } = data
    void loadAndMountComponent(e.state.pjaxUrl, view, props)
  }
}

const handleClick = async (e: MouseEvent): Promise<void> => {
  e.preventDefault()
  const el = e.currentTarget as HTMLAnchorElement
  if (!el) return

  try {
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

    if (response.ok) {
      const data: PjaxResponse = await response.json()

      // Server-driven redirect takes precedence; do not render then unload.
      if (data.redirect) {
        win.location.href = data.redirect
        return
      }

      const view = data[INERTIA_VIEW_KEY]
      if (!view) {
        console.error('No view found in PJAX response')
        win.location.reload()
        return
      }

      const { [INERTIA_VIEW_KEY]: _omit, redirect: _r, ...props } = data
      await loadAndMountComponent(el.href, view, props)
      const info: PjaxState = { pjaxUrl: el.href, pjaxData: data }
      histRef.pushState(info, '', el.href)
      win.scrollTo(0, 0)
    } else if (response.redirected) {
      win.location.href = el.href
    }
  } catch (_err) {
    win.location.href = el.href
  }
}

function sameWindowOrigin(target: string, url: string): boolean {
  const t = (target || '').toLowerCase()
  return (
    (!t ||
      t === win.name ||
      t === '_self' ||
      (t === '_top' && win === win.top) ||
      (t === '_parent' && win === win.parent)) &&
    (url === origin || url.indexOf(origin) === 0)
  )
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
    try {
      win.location.href = url
    } catch (e) {
      console.error('Failed to navigate to', url, e)
    }
  }
}

// Suppress unused-var warnings for `doc` (kept for future use, mirrors original).
void doc
