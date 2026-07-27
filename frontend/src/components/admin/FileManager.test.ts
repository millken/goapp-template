// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import FileManager from './FileManager.vue'

// Mounted via createApp directly: the repo deliberately has no @vue/test-utils.
function mount(props: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp({
    render: () =>
      h(FileManager, { basePath: '/admin/filemanager', ...props } as any),
  })
  app.mount(el)
  return el
}

// The component's mount-time refresh() awaits both `fetch` and `res.json()`,
// and this environment's fetch/Response polyfill resolves each of those over
// several microtask turns rather than one — a single pair of `nextTick()`s
// (enough in a browser) leaves the listing still in flight here. Looping
// nextTick a bounded number of times drains that chain honestly, without
// hard-coding a turn count that's really an implementation detail of the
// polyfill.
async function flush(times = 10) {
  for (let i = 0; i < times; i++) await nextTick()
}

const listing = (over: Record<string, unknown> = {}) => ({
  ok: true,
  path: '',
  breadcrumb: [{ name: '全部文件', path: '' }],
  entries: [
    { name: 'photos', path: 'photos', dir: true, size: 0, mtime: 0, url: '/uploads/photos' },
    { name: 'a.png', path: 'a.png', dir: false, size: 3, mtime: 0, url: '/uploads/a.png' },
  ],
  total: 2,
  page: 1,
  pageSize: 40,
  ...over,
})

// Ticks the per-entry "选择" toggle for the entry at `path`, the same control
// a user clicks before a mutation that needs a selection (删除, 移动到…).
async function select(el: HTMLElement, path: string) {
  const li = el.querySelector(`[data-entry="${path}"]`) as HTMLElement
  const button = [...li.querySelectorAll('button')].find((b) => b.textContent?.includes('选择'))
  button!.click()
  await nextTick()
}

// ConfirmDialog and the move FileManagerDialog render through DialogPortal,
// which teleports to document.body rather than into the mounted root — so
// their buttons never show up under `el`, only as its siblings.
function outsideButtons(el: HTMLElement) {
  return [...document.querySelectorAll('button')].filter((b) => !el.contains(b))
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchMock = vi.fn(async () => new Response(JSON.stringify(listing()), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }))
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  document.body.innerHTML = ''
})

describe('FileManager', () => {
  it('lists the directory it was given on mount', async () => {
    const el = mount()
    await flush()
    expect(fetchMock).toHaveBeenCalled()
    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain('/admin/filemanager/api/list')
    expect(el.textContent).toContain('a.png')
    expect(el.textContent).toContain('photos')
  })

  it('sends the CSRF token on a mutation', async () => {
    const el = mount({ csrfToken: 'tok' })
    await flush()
    const button = [...el.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('新建目录'),
    )
    expect(button).toBeTruthy()
    // A mkdir needs a name, so drive it through the mkdir-name input exactly as
    // a user would: set its value and fire the event shadcn's Input listens
    // for. Its v-model goes through @vueuse/core's useVModel in passive mode,
    // which syncs via a watcher rather than updating the ref inline — so the
    // new value only lands after a tick, not synchronously within
    // dispatchEvent. Clicking the button in the same microtask would still see
    // the old (empty) name and silently no-op instead of proving the header.
    const input = el.querySelector('input[data-testid="mkdir-name"]') as HTMLInputElement | null
    if (input) {
      input.value = 'newdir'
      input.dispatchEvent(new Event('input'))
      await nextTick()
    }
    button!.click()
    await flush()
    const call = fetchMock.mock.calls.find(([u]) => String(u).includes('/api/mkdir'))
    expect(call).toBeTruthy()
    const init = call![1] as RequestInit
    expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBe('tok')
  })

  it('emits select in pick mode instead of navigating', async () => {
    const selected: unknown[] = []
    const el = document.createElement('div')
    document.body.appendChild(el)
    createApp({
      render: () =>
        h(FileManager, {
          basePath: '/admin/filemanager',
          mode: 'pick',
          onSelect: (e: unknown) => selected.push(e),
        } as any),
    }).mount(el)
    await flush()

    const file = [...el.querySelectorAll('[data-entry]')].find(
      (n) => n.getAttribute('data-entry') === 'a.png',
    ) as HTMLElement
    expect(file).toBeTruthy()
    file.click()
    await nextTick()
    expect(selected).toHaveLength(1)
    expect((selected[0] as { path: string }).path).toBe('a.png')
  })

  it('hides files entirely in dirs mode', async () => {
    const el = mount({ mode: 'dirs' })
    await flush()
    expect(el.textContent).toContain('photos')
    expect(el.textContent).not.toContain('a.png')
  })

  it('keeps a per-item failure message visible after the refresh that follows it', async () => {
    const el = mount({ csrfToken: 'tok' })
    await flush()

    fetchMock.mockImplementation(async (url: unknown) => {
      if (String(url).includes('/api/mkdir')) {
        return new Response(
          JSON.stringify({ ok: true, errors: [{ name: 'newdir', error: '已存在' }] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      // The follow-up refresh() this mutation triggers — a normal listing,
      // which is exactly what used to wipe the error message above out
      // before it ever reached the DOM.
      return new Response(JSON.stringify(listing()), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    const button = [...el.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('新建目录'),
    )
    const input = el.querySelector('input[data-testid="mkdir-name"]') as HTMLInputElement | null
    expect(input).toBeTruthy()
    input!.value = 'newdir'
    input!.dispatchEvent(new Event('input'))
    await nextTick()
    button!.click()
    // Both the mutation's fetch and the refresh() it kicks off have to
    // settle before the message can be asserted — a couple of nextTick()s
    // is not enough, per flush()'s own note above.
    await flush()

    expect(el.textContent).toContain('newdir：已存在')
  })

  it('keeps an upload failure message visible after the refresh that follows it', async () => {
    const el = mount({ csrfToken: 'tok' })
    await flush()

    fetchMock.mockImplementation(async (url: unknown) => {
      if (String(url).includes('/api/upload')) {
        // The real server's shape for a multipart body over the request
        // ceiling: 413, with the message the handler actually sends
        // (internal/controller/admin/filemanager.go's fmBodyFail).
        return new Response(JSON.stringify({ error: '上传内容超过单次请求上限' }), {
          status: 413,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      // The follow-up refresh() this upload triggers — files earlier in the
      // batch may already be on disk, so refreshing after a failed upload is
      // correct; this is exactly the listing that used to wipe the error
      // message out before it ever reached the DOM.
      return new Response(JSON.stringify(listing()), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    const input = el.querySelector('input[data-testid="upload-input"]') as HTMLInputElement | null
    expect(input).toBeTruthy()

    const file = new File(['x'.repeat(10)], 'big.png', { type: 'image/png' })
    const dt = new DataTransfer()
    dt.items.add(file)
    input!.files = dt.files
    input!.dispatchEvent(new Event('change'))

    // Both the upload's fetch and the refresh() it kicks off have to settle
    // before the message can be asserted — see flush()'s own note above.
    await flush()

    expect(el.textContent).toContain('上传内容超过单次请求上限')
  })

  it('pages through a total larger than one page', async () => {
    fetchMock.mockImplementation(
      async () =>
        new Response(JSON.stringify(listing({ total: 100, page: 1, pageSize: 40 })), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    const el = mount()
    await flush()
    expect(el.textContent).toContain('100')
    const next = [...el.querySelectorAll('button')].find(
      (b) => b.getAttribute('data-testid') === 'next-page',
    )
    expect(next).toBeTruthy()
    next!.click()
    await nextTick()
    const last = String(fetchMock.mock.calls.at(-1)![0])
    expect(last).toContain('page=2')
  })

  // Finding 1: recursive delete had no confirmation at all. These pin that a
  // click on 删除 opens ConfirmDialog instead of firing the request, that the
  // dialog names what it is about to destroy, and that the request only goes
  // out once the confirm button inside it is clicked.
  describe('delete confirmation', () => {
    it('does not delete on click — it opens a confirmation naming the count', async () => {
      const el = mount({ csrfToken: 'tok' })
      await flush()
      await select(el, 'a.png')

      const deleteBtn = [...el.querySelectorAll('button')].find(
        (b) => b.textContent?.trim() === '删除',
      )
      deleteBtn!.click()
      await flush()

      expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/api/delete'))).toBe(false)
      expect(document.body.textContent).toContain('1 项')
    })

    it('calls out that a directory takes everything inside it with it', async () => {
      const el = mount({ csrfToken: 'tok' })
      await flush()
      await select(el, 'photos')

      const deleteBtn = [...el.querySelectorAll('button')].find(
        (b) => b.textContent?.trim() === '删除',
      )
      deleteBtn!.click()
      await flush()

      expect(document.body.textContent).toContain('目录')
    })

    it('deletes only once the dialog is confirmed', async () => {
      const el = mount({ csrfToken: 'tok' })
      await flush()
      await select(el, 'a.png')

      const deleteBtn = [...el.querySelectorAll('button')].find(
        (b) => b.textContent?.trim() === '删除',
      )
      deleteBtn!.click()
      await flush()

      const confirmBtn = outsideButtons(el).find((b) => b.textContent?.trim() === '删除')
      expect(confirmBtn).toBeTruthy()
      confirmBtn!.click()
      await flush()

      const call = fetchMock.mock.calls.find(([u]) => String(u).includes('/api/delete'))
      expect(call).toBeTruthy()
      expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({ paths: ['a.png'] })
    })
  })

  // Finding 2: Service.Move, /api/move and mode="dirs" all existed with no
  // caller. These pin the 移动到… control end to end: it opens a dirs-mode
  // FileManagerDialog, "选择此目录" is how the root (or any folder with no
  // subfolder of its own) gets chosen, and a colliding item's failure surfaces
  // the same way mkdir/upload's already do.
  describe('move', () => {
    it('posts the selected paths and the chosen directory, root included', async () => {
      const el = mount({ csrfToken: 'tok' })
      await flush()
      await select(el, 'a.png')

      const moveBtn = [...el.querySelectorAll('button')].find(
        (b) => b.textContent?.trim() === '移动到…',
      )
      expect(moveBtn).toBeTruthy()
      moveBtn!.click()
      await flush()

      const chooseCurrent = outsideButtons(el).find(
        (b) => b.getAttribute('data-testid') === 'choose-current-dir',
      )
      expect(chooseCurrent).toBeTruthy()
      chooseCurrent!.click()
      await flush()

      const call = fetchMock.mock.calls.find(([u]) => String(u).includes('/api/move'))
      expect(call).toBeTruthy()
      expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({
        paths: ['a.png'],
        to: '',
      })
    })

    it('surfaces a colliding item the same way mkdir and upload report a failure', async () => {
      const el = mount({ csrfToken: 'tok' })
      await flush()
      await select(el, 'a.png')

      fetchMock.mockImplementation(async (url: unknown) => {
        if (String(url).includes('/api/move')) {
          return new Response(
            JSON.stringify({ ok: true, errors: [{ name: 'a.png', error: '同名项已存在' }] }),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          )
        }
        return new Response(JSON.stringify(listing()), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      })

      const moveBtn = [...el.querySelectorAll('button')].find(
        (b) => b.textContent?.trim() === '移动到…',
      )
      moveBtn!.click()
      await flush()
      const chooseCurrent = outsideButtons(el).find(
        (b) => b.getAttribute('data-testid') === 'choose-current-dir',
      )
      chooseCurrent!.click()
      await flush()

      expect(el.textContent).toContain('a.png：同名项已存在')
    })
  })

  // Finding: rename now 409s on a name collision instead of overwriting. No
  // special-case code was needed for this — mutate() already surfaces any
  // non-ok response's body.error — but the shape is new enough to pin.
  it('surfaces a 409 from renaming onto an existing name', async () => {
    const el = mount({ csrfToken: 'tok' })
    await flush()

    fetchMock.mockImplementation(async (url: unknown) => {
      if (String(url).includes('/api/rename')) {
        return new Response(JSON.stringify({ error: '同名项已存在' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      return new Response(JSON.stringify(listing()), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    const original = window.prompt
    window.prompt = () => 'photos'
    try {
      const li = el.querySelector('[data-entry="a.png"]') as HTMLElement
      const renameBtn = [...li.querySelectorAll('button')].find((b) =>
        b.textContent?.includes('重命名'),
      )
      renameBtn!.click()
      await flush()
    } finally {
      window.prompt = original
    }

    expect(el.textContent).toContain('同名项已存在')
  })

  // Finding 5: the query watcher had no debounce (one request per keystroke)
  // and, on top of that, double-fired when the page wasn't already 1 (it set
  // page.value = 1 *and* called refresh() itself, while the [path, page]
  // watcher fired again from the page change it just made).
  describe('search debounce', () => {
    it('typing several characters quickly issues exactly one request', async () => {
      const el = mount()
      await flush()
      fetchMock.mockClear()

      const input = el.querySelector('input[data-testid="search"]') as HTMLInputElement
      vi.useFakeTimers()
      try {
        let typed = ''
        for (const ch of 'photo') {
          typed += ch
          input.value = typed
          input.dispatchEvent(new Event('input'))
          await nextTick() // let v-model propagate the keystroke into `query`
          vi.advanceTimersByTime(50) // well under the debounce delay
        }
        vi.advanceTimersByTime(300)
      } finally {
        vi.useRealTimers()
      }
      await flush()

      expect(fetchMock).toHaveBeenCalledTimes(1)
      expect(String(fetchMock.mock.calls[0][0])).toContain('q=photo')
    })

    it('a query change from page 2 issues one request, not two', async () => {
      fetchMock.mockImplementation(
        async () =>
          new Response(JSON.stringify(listing({ total: 100, page: 2, pageSize: 40 })), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }),
      )
      const el = mount()
      await flush()
      const next = [...el.querySelectorAll('button')].find(
        (b) => b.getAttribute('data-testid') === 'next-page',
      )
      next!.click()
      await flush()
      fetchMock.mockClear()

      const input = el.querySelector('input[data-testid="search"]') as HTMLInputElement
      vi.useFakeTimers()
      try {
        input.value = 'a'
        input.dispatchEvent(new Event('input'))
        await nextTick()
        vi.advanceTimersByTime(300)
      } finally {
        vi.useRealTimers()
      }
      await flush()

      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
  })
})
