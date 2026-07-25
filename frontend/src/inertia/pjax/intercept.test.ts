import { beforeEach, describe, expect, it } from 'vitest'
import { formTarget, installDelegation, shouldInterceptForm, shouldInterceptLink } from './intercept'

const ORIGIN = 'http://localhost:3000'

function anchor(attrs: Record<string, string>): HTMLAnchorElement {
  const a = document.createElement('a')
  for (const [k, v] of Object.entries(attrs)) a.setAttribute(k, v)
  document.body.append(a)
  return a
}

function click(overrides: Partial<MouseEvent> = {}): MouseEvent {
  return { button: 0, ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, defaultPrevented: false, ...overrides } as MouseEvent
}

beforeEach(() => {
  document.body.innerHTML = ''
})

describe('shouldInterceptLink', () => {
  it('accepts a plain same-origin link', () => {
    expect(shouldInterceptLink(anchor({ href: `${ORIGIN}/about` }), click(), ORIGIN)).toBe(true)
  })

  // Regression for the live bug: the old handler called preventDefault()
  // unconditionally, so Cmd-click could not open a new tab.
  it.each([
    ['ctrl', { ctrlKey: true }],
    ['meta', { metaKey: true }],
    ['shift', { shiftKey: true }],
    ['alt', { altKey: true }],
  ])('declines a %s-click so the browser can open a new tab/window', (_name, mods) => {
    expect(shouldInterceptLink(anchor({ href: `${ORIGIN}/about` }), click(mods), ORIGIN)).toBe(false)
  })

  it('declines a non-primary button', () => {
    expect(shouldInterceptLink(anchor({ href: `${ORIGIN}/about` }), click({ button: 1 }), ORIGIN)).toBe(false)
  })

  it('declines an event someone else already handled', () => {
    expect(
      shouldInterceptLink(anchor({ href: `${ORIGIN}/about` }), click({ defaultPrevented: true }), ORIGIN),
    ).toBe(false)
  })

  it('declines a cross-origin link', () => {
    expect(shouldInterceptLink(anchor({ href: 'https://example.com/x' }), click(), ORIGIN)).toBe(false)
  })

  it('declines a download link', () => {
    expect(
      shouldInterceptLink(anchor({ href: `${ORIGIN}/f.zip`, download: '' }), click(), ORIGIN),
    ).toBe(false)
  })

  it.each(['_blank', '_parent-ish'])('declines target=%s', (target) => {
    expect(shouldInterceptLink(anchor({ href: `${ORIGIN}/about`, target }), click(), ORIGIN)).toBe(false)
  })

  it.each(['_self', ''])('accepts target=%s (still this window)', (target) => {
    expect(shouldInterceptLink(anchor({ href: `${ORIGIN}/about`, target }), click(), ORIGIN)).toBe(true)
  })

  it('declines a hash-only link so the browser jumps to the anchor', () => {
    expect(shouldInterceptLink(anchor({ href: '#section' }), click(), ORIGIN)).toBe(false)
  })

  it('accepts a link to another path that also carries a hash', () => {
    expect(shouldInterceptLink(anchor({ href: `${ORIGIN}/about#team` }), click(), ORIGIN)).toBe(true)
  })

  it('declines a non-http scheme', () => {
    expect(shouldInterceptLink(anchor({ href: 'mailto:a@b.c' }), click(), ORIGIN)).toBe(false)
  })

  it('declines when data-no-pjax is on the link', () => {
    expect(
      shouldInterceptLink(anchor({ href: `${ORIGIN}/about`, 'data-no-pjax': '' }), click(), ORIGIN),
    ).toBe(false)
  })

  // The opt-out has to be inheritable, or you could not exclude a whole region.
  it('declines when data-no-pjax is on an ancestor', () => {
    const region = document.createElement('div')
    region.setAttribute('data-no-pjax', '')
    const a = document.createElement('a')
    a.setAttribute('href', `${ORIGIN}/about`)
    region.append(a)
    document.body.append(region)
    expect(shouldInterceptLink(a, click(), ORIGIN)).toBe(false)
  })
})

function form(attrs: Record<string, string>): HTMLFormElement {
  const f = document.createElement('form')
  for (const [k, v] of Object.entries(attrs)) f.setAttribute(k, v)
  document.body.append(f)
  return f
}

describe('shouldInterceptForm', () => {
  it.each(['get', 'GET', 'post', 'POST'])('accepts method=%s', (method) => {
    expect(shouldInterceptForm(form({ action: `${ORIGIN}/login`, method }), ORIGIN)).toBe(true)
  })

  it('accepts a form with no method (defaults to GET)', () => {
    expect(shouldInterceptForm(form({ action: `${ORIGIN}/search` }), ORIGIN)).toBe(true)
  })

  it('declines a method native forms cannot send anyway', () => {
    expect(shouldInterceptForm(form({ action: `${ORIGIN}/x`, method: 'dialog' }), ORIGIN)).toBe(false)
  })

  it('declines a cross-origin action', () => {
    expect(shouldInterceptForm(form({ action: 'https://example.com/x', method: 'post' }), ORIGIN)).toBe(false)
  })

  it('declines target=_blank', () => {
    expect(
      shouldInterceptForm(form({ action: `${ORIGIN}/x`, method: 'post', target: '_blank' }), ORIGIN),
    ).toBe(false)
  })

  it('declines data-no-pjax', () => {
    expect(
      shouldInterceptForm(form({ action: `${ORIGIN}/x`, method: 'post', 'data-no-pjax': '' }), ORIGIN),
    ).toBe(false)
  })

  // FormData handles multipart correctly as long as Content-Type is left unset,
  // so there is no reason to make it a special case.
  it('accepts a multipart form', () => {
    expect(
      shouldInterceptForm(
        form({ action: `${ORIGIN}/upload`, method: 'post', enctype: 'multipart/form-data' }),
        ORIGIN,
      ),
    ).toBe(true)
  })
})

describe('installDelegation', () => {
  const ORIGIN_HERE = document.location.origin

  function setup() {
    const links: string[] = []
    const forms: HTMLFormElement[] = []
    const teardown = installDelegation(document, ORIGIN_HERE, {
      onLink: (url) => links.push(url),
      onForm: (f) => forms.push(f),
    })
    return { links, forms, teardown }
  }

  it('catches a click on a descendant of the link', () => {
    const { links, teardown } = setup()
    document.body.innerHTML = `<a href="/about"><span><em>go</em></span></a>`
    const em = document.querySelector('em')!

    const e = new MouseEvent('click', { bubbles: true, cancelable: true })
    em.dispatchEvent(e)

    expect(links).toEqual([`${ORIGIN_HERE}/about`])
    expect(e.defaultPrevented).toBe(true)
    teardown()
  })

  it('leaves a declined click alone', () => {
    const { links, teardown } = setup()
    document.body.innerHTML = `<a href="/about" data-no-pjax>go</a>`

    const e = new MouseEvent('click', { bubbles: true, cancelable: true })
    document.querySelector('a')!.dispatchEvent(e)

    expect(links).toEqual([])
    expect(e.defaultPrevented).toBe(false)
    teardown()
  })

  // Proves the bubble-phase choice: a page handler runs first and can opt out.
  it('yields to a page handler that already prevented the default', () => {
    const { links, teardown } = setup()
    document.body.innerHTML = `<a href="/about">go</a>`
    const a = document.querySelector('a')!
    a.addEventListener('click', (e) => e.preventDefault())

    a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))

    expect(links).toEqual([])
    teardown()
  })

  it('catches a form submit', () => {
    const { forms, teardown } = setup()
    document.body.innerHTML = `<form action="/login" method="post"></form>`
    const f = document.querySelector('form')!

    const e = new Event('submit', { bubbles: true, cancelable: true })
    f.dispatchEvent(e)

    expect(forms).toEqual([f])
    expect(e.defaultPrevented).toBe(true)
    teardown()
  })

  it('stops intercepting after teardown', () => {
    const { links, teardown } = setup()
    teardown()
    document.body.innerHTML = `<a href="/about">go</a>`

    const e = new MouseEvent('click', { bubbles: true, cancelable: true })
    document.querySelector('a')!.dispatchEvent(e)

    expect(links).toEqual([])
    expect(e.defaultPrevented).toBe(false)
  })
})

describe('formTarget', () => {
  it('serializes a GET form into the query string and sends no body', () => {
    document.body.innerHTML = `
      <form action="/search" method="get">
        <input name="q" value="hello world" />
        <input name="page" value="2" />
      </form>`
    const got = formTarget(document.querySelector('form')!)

    expect(got.method).toBe('GET')
    expect(got.body).toBeUndefined()
    expect(new URL(got.url).pathname).toBe('/search')
    expect(new URL(got.url).searchParams.get('q')).toBe('hello world')
    expect(new URL(got.url).searchParams.get('page')).toBe('2')
  })

  it('keeps a POST body as FormData and leaves the URL clean', () => {
    document.body.innerHTML = `
      <form action="/admin/login" method="post">
        <input name="username" value="alice" />
      </form>`
    const got = formTarget(document.querySelector('form')!)

    expect(got.method).toBe('POST')
    expect(got.body).toBeInstanceOf(FormData)
    expect((got.body as FormData).get('username')).toBe('alice')
    expect(new URL(got.url).search).toBe('')
  })

  it('submits to the current URL when the form has no action', () => {
    document.body.innerHTML = `<form method="post"></form>`
    const got = formTarget(document.querySelector('form')!)

    expect(got.url).toBe(document.location.href)
  })
})
