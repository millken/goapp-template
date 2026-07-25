// Document-level delegation. Binding one listener on the document — rather than
// per-link via a component — is what lets PJAX be on by default: pages, admin
// layouts and generated scaffolds need no changes, and links added later are
// covered automatically.
//
// Everything here is a pure predicate over an element plus the event, so the
// rules can be tested without a navigation.

/** Only real web navigations are intercepted; mailto:, tel: and friends are not. */
const NAVIGABLE_PROTOCOLS = new Set(['http:', 'https:'])

/** Methods a plain HTML form can actually send. */
const FORM_METHODS = new Set(['get', 'post'])

/**
 * Opting out has to be inheritable, or a whole region (a third-party widget, a
 * file browser) could not be excluded without touching every link inside it.
 */
function optedOut(el: Element): boolean {
  return el.closest('[data-no-pjax]') !== null
}

/**
 * True when following this element would load in the current window.
 *
 * Only the unambiguous cases are accepted. `_top` and `_parent` can also mean
 * "this window" depending on framing, but getting that wrong sends a page into
 * the wrong frame, and declining merely falls back to a normal navigation.
 */
function staysInThisWindow(target: string): boolean {
  return target === '' || target === '_self'
}

function sameOrigin(url: URL, origin: string): boolean {
  return NAVIGABLE_PROTOCOLS.has(url.protocol) && url.origin === origin
}

/** The subset of MouseEvent the link rules read; keeps tests free of real events. */
export interface ClickLike {
  button: number
  ctrlKey: boolean
  metaKey: boolean
  shiftKey: boolean
  altKey: boolean
  defaultPrevented: boolean
}

export function shouldInterceptLink(el: HTMLAnchorElement, e: ClickLike, origin: string): boolean {
  // Someone else already handled it — a page handler opting out, for instance.
  if (e.defaultPrevented) return false

  // Modifier and middle clicks mean "open elsewhere". Swallowing them is the
  // single most annoying thing a PJAX layer can do.
  if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return false

  if (el.hasAttribute('download')) return false
  if (!staysInThisWindow(el.target)) return false
  if (optedOut(el)) return false

  // A bare "#foo" is an in-page jump; the browser does it better.
  const href = el.getAttribute('href')
  if (!href || href.startsWith('#')) return false

  return sameOrigin(new URL(el.href, document.baseURI), origin)
}

export function shouldInterceptForm(el: HTMLFormElement, origin: string): boolean {
  if (!staysInThisWindow(el.target)) return false
  if (optedOut(el)) return false

  // Read the attribute, not el.method: the property normalizes anything unknown
  // to "get", which would hide a method we cannot actually handle.
  const method = (el.getAttribute('method') ?? 'get').toLowerCase()
  if (!FORM_METHODS.has(method)) return false

  // An action-less form submits to the current URL.
  const action = el.getAttribute('action') || document.location.href
  return sameOrigin(new URL(action, document.baseURI), origin)
}

/** What the delegation hands back to the navigator. */
export interface Handlers {
  onLink(url: string): void
  onForm(form: HTMLFormElement): void
}

/**
 * Binds click and submit on the document and returns a teardown.
 *
 * Bubble phase, deliberately: a page's own handler runs first and can call
 * preventDefault() or stopPropagation() to keep a navigation out of PJAX. The
 * previous implementation used capture, which took that choice away.
 */
export function installDelegation(doc: Document, origin: string, h: Handlers): () => void {
  const onClick = (e: Event) => {
    const mouse = e as MouseEvent
    const target = e.target as Element | null
    const link = target?.closest?.('a')
    if (!link || !shouldInterceptLink(link, mouse, origin)) return
    e.preventDefault()
    h.onLink(link.href)
  }

  const onSubmit = (e: Event) => {
    const form = e.target as HTMLFormElement | null
    if (!form || !shouldInterceptForm(form, origin)) return
    e.preventDefault()
    h.onForm(form)
  }

  doc.addEventListener('click', onClick)
  doc.addEventListener('submit', onSubmit)
  return () => {
    doc.removeEventListener('click', onClick)
    doc.removeEventListener('submit', onSubmit)
  }
}

/** A form turned into the request it should become. */
export interface FormTarget {
  url: string
  method: 'GET' | 'POST'
  body?: FormData
}

/**
 * Reads a form the way the browser would submit it.
 *
 * GET forms put their fields in the query string and send no body — that is
 * what makes a search result a bookmarkable URL. POST forms keep FormData,
 * which also handles multipart correctly as long as the caller never sets
 * Content-Type by hand.
 */
export function formTarget(form: HTMLFormElement): FormTarget {
  const method = (form.getAttribute('method') ?? 'get').toUpperCase() === 'POST' ? 'POST' : 'GET'
  const action = form.getAttribute('action') || document.location.href
  const url = new URL(action, document.baseURI)
  const data = new FormData(form)

  if (method === 'GET') {
    const params = new URLSearchParams()
    for (const [k, v] of data.entries()) {
      if (typeof v === 'string') params.append(k, v)
    }
    url.search = params.toString()
    return { url: url.href, method }
  }
  return { url: url.href, method, body: data }
}
