// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { createApp, h } from 'vue'
import ServerTable from './ServerTable.vue'

// Mounted via createApp directly, and with the same `as any` concession as
// DataTable.test.ts — see the comment there for why h()'s overloads need it.
function mount(props: Record<string, unknown>, slots: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(ServerTable, props as any, slots) }).mount(el)
  return el
}

const rows = (n: number) =>
  Array.from({ length: n }, (_, i) => ({ id: i + 1, kind: `kind ${i + 1}` }))

const columns = [
  { key: 'id', label: 'ID' },
  { key: 'kind', label: '类型' },
]

const base = {
  columns,
  total: 10,
  page: 1,
  pageSize: 25,
  pageHref: (p: number) => `?page=${p}`,
}

describe('ServerTable', () => {
  it('renders exactly the rows it is given', () => {
    const el = mount({ ...base, data: rows(10) })
    expect(el.querySelectorAll('tbody tr').length).toBe(10)
  })

  // The line between this component and DataTable: pageSize describes the server's
  // slice, it does not perform one. If this ever slices, the component has started
  // holding an opinion about which rows exist, and it will disagree with the server.
  it('does not slice client-side', () => {
    const el = mount({ ...base, data: rows(10), pageSize: 5, total: 10 })
    expect(el.querySelectorAll('tbody tr').length).toBe(10)
  })

  // DataTable's footer reads data.length, which on a paged list is the page size. That
  // is the specific lie this component exists to avoid.
  it('counts from total, not from the rows it holds', () => {
    const el = mount({ ...base, data: rows(25), total: 1000, pageSize: 25, page: 2 })
    expect(el.textContent).toContain('共 1000 条')
    expect(el.textContent).not.toContain('共 25 条')
    expect(el.textContent).toContain('第 2 / 40 页')
  })

  // Paging has to be links, or PJAX cannot turn it into a navigation and the URL stops
  // being shareable.
  it('pages with anchors built by pageHref', () => {
    const el = mount({ ...base, data: rows(25), total: 100, pageSize: 25, page: 2 })

    const prev = el.querySelector('[data-testid="prev-page"]') as HTMLAnchorElement
    const next = el.querySelector('[data-testid="next-page"]') as HTMLAnchorElement
    expect(prev?.tagName).toBe('A')
    expect(next?.tagName).toBe('A')
    expect(prev?.getAttribute('href')).toBe('?page=1')
    expect(next?.getAttribute('href')).toBe('?page=3')
  })

  // A disabled anchor does not exist. At the ends the arrow is inert markup, not a
  // link that looks dead and navigates anyway.
  it('renders no anchor at either end', () => {
    const first = mount({ ...base, data: rows(25), total: 100, pageSize: 25, page: 1 })
    expect(first.querySelector('[data-testid="prev-page"]')).toBeNull()
    expect(first.querySelector('[data-testid="next-page"]')).not.toBeNull()

    const last = mount({ ...base, data: rows(25), total: 100, pageSize: 25, page: 4 })
    expect(last.querySelector('[data-testid="prev-page"]')).not.toBeNull()
    expect(last.querySelector('[data-testid="next-page"]')).toBeNull()
  })

  it('hides the pager when there is only one page', () => {
    const el = mount({ ...base, data: rows(3), total: 3, pageSize: 25 })
    expect(el.querySelector('[data-testid="prev-page"]')).toBeNull()
    expect(el.querySelector('[data-testid="next-page"]')).toBeNull()
    // The count stays: a footer that vanishes leaves "how many are there?"
    // unanswered.
    expect(el.textContent).toContain('共 3 条')
  })

  // An <a> wrapping a <button> is reparsed by browsers, which then makes hydration
  // disagree with the server's markup. ssr_newpages_test.go checks the rendered output
  // for it; this catches it at the component level, where the fix is obvious.
  it('never nests a button inside a button or an anchor', () => {
    const el = mount({ ...base, data: rows(25), total: 100, pageSize: 25, page: 2 })
    expect(el.querySelectorAll('button button').length).toBe(0)
    expect(el.querySelectorAll('a button').length).toBe(0)
    expect(el.querySelectorAll('button a').length).toBe(0)
  })

  it('renders a cell slot in place of the raw value', () => {
    const el = mount(
      { ...base, data: [{ id: 7, kind: 'mail' }], total: 1 },
      { 'cell-kind': ({ row }: { row: Record<string, unknown> }) => h('em', {}, `→${row.kind}`) },
    )
    expect(el.querySelector('tbody em')?.textContent).toBe('→mail')
  })

  it('renders a row-actions dropdown trigger only when the slot is given', () => {
    const without = mount({ ...base, data: rows(1), total: 1 })
    expect(without.querySelectorAll('thead th').length).toBe(2)

    const withActions = mount(
      { ...base, data: rows(1), total: 1 },
      { 'row-actions': () => h('span', {}, 'delete') },
    )
    // One extra header cell for the actions column, and a trigger in the row.
    expect(withActions.querySelectorAll('thead th').length).toBe(3)
    expect(withActions.querySelectorAll('tbody button').length).toBe(1)
  })

  // Two different emptinesses. Showing "nothing here yet" to somebody whose filter
  // matched nothing is a lie about a table that is full.
  it('distinguishes an empty table from an empty filter result', () => {
    const empty = mount({ ...base, data: [], total: 0 }, { empty: () => '还没有任务。' })
    expect(empty.textContent).toContain('还没有任务。')

    const filteredOut = mount(
      { ...base, data: [], total: 0, filtered: true },
      { empty: () => '还没有任务。' },
    )
    expect(filteredOut.textContent).toContain('没有匹配该筛选条件的记录。')
    expect(filteredOut.textContent).not.toContain('还没有任务。')
  })
})
