import { beforeEach, describe, expect, it, vi } from 'vitest'
import { INERTIA_VIEW_KEY } from '../constants'
import { createNavigator, historyOp, type Deps, type Trigger } from './navigate'

const HERE = 'http://localhost:3000/admin/login'
const THERE = 'http://localhost:3000/admin'

// ---------------------------------------------------------------------------
// historyOp — every row of the design's §4.2 table. This is where the first
// draft of the design was wrong (a rule that could only ever downgrade to
// replace), so it gets exhaustive coverage rather than a couple of spot checks.
// ---------------------------------------------------------------------------
describe('historyOp', () => {
  const cases: Array<{
    trigger: Trigger
    redirected: boolean
    target: string
    want: 'push' | 'replace' | 'none'
    why: string
  }> = [
    { trigger: 'boot', redirected: false, target: HERE, want: 'replace', why: 'seeds the first entry' },
    { trigger: 'link', redirected: false, target: THERE, want: 'push', why: 'ordinary forward navigation' },
    { trigger: 'link', redirected: true, target: THERE, want: 'push', why: 'redirect to a new URL is still forward' },
    { trigger: 'link', redirected: true, target: HERE, want: 'replace', why: 'redirected back to where we already are' },
    { trigger: 'form', redirected: false, target: HERE, want: 'replace', why: 'failed login re-renders the URL we posted from' },
    { trigger: 'form', redirected: true, target: THERE, want: 'push', why: 'successful login lands somewhere new' },
    { trigger: 'form', redirected: true, target: HERE, want: 'replace', why: 'redirected to the same URL' },
    { trigger: 'popstate', redirected: false, target: HERE, want: 'none', why: 'the entry already exists' },
    { trigger: 'popstate', redirected: true, target: THERE, want: 'replace', why: 'Back must not mint a forward entry' },
  ]

  for (const c of cases) {
    it(`${c.trigger}${c.redirected ? ' + redirect' : ''} -> ${c.want} (${c.why})`, () => {
      expect(historyOp(c.trigger, c.redirected, c.target, HERE)).toBe(c.want)
    })
  }

  // A GET search form lands on a different URL, so it should push even though
  // nothing redirected — the rule keys on the URL, not on the trigger.
  it('pushes for a form that renders a different URL (GET search)', () => {
    expect(historyOp('form', false, 'http://localhost:3000/search?q=x', HERE)).toBe('push')
  })
})

// ---------------------------------------------------------------------------
// visit — the state machine, driven through an injected fetch.
// ---------------------------------------------------------------------------
function jsonResponse(body: unknown, init: { ok?: boolean; status?: number } = {}) {
  return {
    ok: init.ok ?? true,
    status: init.status ?? 200,
    json: async () => body,
  } as Response
}

function makeDeps(overrides: Partial<Deps> = {}) {
  const calls = {
    push: [] as string[],
    replace: [] as string[],
    mounted: [] as Array<{ view: string; props: Record<string, unknown> }>,
    hardNavigated: [] as string[],
    requests: [] as Array<{ url: string; init: RequestInit }>,
    progress: [] as string[],
  }
  let current = HERE

  const deps: Deps = {
    fetch: vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.requests.push({ url: String(input), init: init ?? {} })
      return jsonResponse({ [INERTIA_VIEW_KEY]: 'Page' })
    }) as unknown as Deps['fetch'],
    currentUrl: () => current,
    pushState: (url) => {
      calls.push.push(url)
      current = url
    },
    replaceState: (url) => {
      calls.replace.push(url)
      current = url
    },
    hardNavigate: (url) => calls.hardNavigated.push(url),
    mount: async (view, props) => {
      calls.mounted.push({ view, props })
    },
    progressStart: () => calls.progress.push('start'),
    progressDone: () => calls.progress.push('done'),
    saveScroll: () => {},
    restoreScroll: () => {},
    ...overrides,
  }
  return { deps, calls }
}

beforeEach(() => {
  document.body.innerHTML = ''
})

describe('visit', () => {
  it('mounts the returned view and pushes for a link', async () => {
    const { deps, calls } = makeDeps()
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.mounted).toEqual([{ view: 'Page', props: {} }])
    expect(calls.push).toEqual([THERE])
    expect(calls.replace).toEqual([])
  })

  it('passes props through, minus the view key', async () => {
    const { deps, calls } = makeDeps({
      fetch: (async () => jsonResponse({ [INERTIA_VIEW_KEY]: 'Page', a: 1 })) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.mounted[0].props).toEqual({ a: 1 })
  })

  it('sends the PJAX marker and asks for JSON, without a bogus Content-Type', async () => {
    const { deps, calls } = makeDeps()
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    const headers = calls.requests[0].init.headers as Record<string, string>
    expect(headers['X-Pjax']).toBe('true')
    expect(headers['Accept']).toBe('application/json')
    // A GET has no body, so a Content-Type would describe something absent.
    expect(headers['Content-Type']).toBeUndefined()
  })

  it('follows a {redirect} payload and mounts the target', async () => {
    let call = 0
    const { deps, calls } = makeDeps({
      fetch: (async () => {
        call += 1
        return call === 1
          ? jsonResponse({ redirect: THERE })
          : jsonResponse({ [INERTIA_VIEW_KEY]: 'Dashboard' })
      }) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(HERE, { trigger: 'form', body: new FormData() })

    expect(calls.mounted).toEqual([{ view: 'Dashboard', props: {} }])
    expect(calls.push).toEqual([THERE])
  })

  // The path §6.2 depends on: Back to the login page while signed in.
  it('replaces the URL when a redirect arrives during popstate', async () => {
    let call = 0
    const { deps, calls } = makeDeps({
      fetch: (async () => {
        call += 1
        return call === 1
          ? jsonResponse({ redirect: THERE })
          : jsonResponse({ [INERTIA_VIEW_KEY]: 'Dashboard' })
      }) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(HERE, { trigger: 'popstate' })

    expect(calls.replace).toEqual([THERE])
    expect(calls.push).toEqual([])
    expect(calls.mounted).toEqual([{ view: 'Dashboard', props: {} }])
  })

  // Saving on popstate would stamp the *outgoing* viewport offset onto the
  // entry we just arrived at, destroying the offset restore() is about to read.
  it('does not save scroll on popstate, only restores', async () => {
    const order: string[] = []
    const { deps } = makeDeps({
      saveScroll: () => order.push('save'),
      restoreScroll: () => order.push('restore'),
    })
    await createNavigator(deps).visit(HERE, { trigger: 'popstate' })

    expect(order).toEqual(['restore'])
  })

  it('saves scroll before leaving on a link navigation', async () => {
    const order: string[] = []
    const { deps } = makeDeps({
      saveScroll: () => order.push('save'),
      restoreScroll: () => order.push('restore'),
    })
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(order).toEqual(['save'])
  })

  it('writes no history for a plain popstate', async () => {
    const { deps, calls } = makeDeps()
    await createNavigator(deps).visit(HERE, { trigger: 'popstate' })

    expect(calls.push).toEqual([])
    expect(calls.replace).toEqual([])
    expect(calls.mounted).toHaveLength(1)
  })

  it('falls back to a full navigation when the server errors', async () => {
    const { deps, calls } = makeDeps({
      fetch: (async () => jsonResponse({}, { ok: false, status: 500 })) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.hardNavigated).toEqual([THERE])
    expect(calls.mounted).toEqual([])
  })

  it('falls back to a full navigation when the payload names no view', async () => {
    const { deps, calls } = makeDeps({
      fetch: (async () => jsonResponse({ nothing: true })) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.hardNavigated).toEqual([THERE])
  })

  it('falls back to a full navigation when fetch rejects', async () => {
    const { deps, calls } = makeDeps({
      fetch: (async () => {
        throw new Error('offline')
      }) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.hardNavigated).toEqual([THERE])
  })

  it('gives up on a redirect loop instead of hanging', async () => {
    // Counted here rather than through makeDeps' recorder: overriding fetch
    // replaces the recorder, so asserting on calls.requests would silently
    // measure zero and pass no matter how long the loop ran.
    let hops = 0
    const { deps, calls } = makeDeps({
      fetch: (async () => {
        hops += 1
        return jsonResponse({ redirect: THERE })
      }) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.mounted).toEqual([])
    expect(calls.hardNavigated).toEqual([THERE])
    // A literal bound on purpose: asserting against the module's own
    // MAX_REDIRECTS would move with it, so raising the cap could never fail
    // this test. What matters is that the number is small, not that it
    // matches a constant.
    expect(hops).toBeLessThanOrEqual(10)
  })

  it('aborts the previous request when a second navigation starts', async () => {
    const signals: Array<AbortSignal | undefined> = []
    const { deps } = makeDeps({
      fetch: (async (_url: unknown, init?: RequestInit) => {
        signals.push(init?.signal ?? undefined)
        await new Promise((r) => setTimeout(r, 5))
        return jsonResponse({ [INERTIA_VIEW_KEY]: 'Page' })
      }) as unknown as Deps['fetch'],
    })
    const nav = createNavigator(deps)

    const first = nav.visit(THERE, { trigger: 'link' })
    const second = nav.visit(`${THERE}/other`, { trigger: 'link' })
    await Promise.all([first, second])

    expect(signals[0]?.aborted).toBe(true)
    expect(signals[1]?.aborted).toBe(false)
  })

  it('runs the progress bar around the request', async () => {
    const { deps, calls } = makeDeps()
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.progress).toEqual(['start', 'done'])
  })

  it('stops the progress bar even when the navigation fails', async () => {
    const { deps, calls } = makeDeps({
      fetch: (async () => {
        throw new Error('offline')
      }) as unknown as Deps['fetch'],
    })
    await createNavigator(deps).visit(THERE, { trigger: 'link' })

    expect(calls.progress).toEqual(['start', 'done'])
  })

  it('posts form data with the form method', async () => {
    const { deps, calls } = makeDeps()
    const body = new FormData()
    body.set('username', 'alice')

    await createNavigator(deps).visit(HERE, { trigger: 'form', method: 'POST', body })

    expect(calls.requests[0].init.method).toBe('POST')
    expect(calls.requests[0].init.body).toBe(body)
  })
})
