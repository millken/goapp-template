// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import DataTable from './DataTable.vue'

// Mounted via createApp directly: the repo deliberately has no @vue/test-utils,
// and a table renders enough real DOM to assert on.
//
// The `as any` below is a type-only concession: h()'s overloads infer the
// props generic from the *static* type of the second argument, and a bare
// `Record<string, unknown>` (as opposed to an object literal) makes it pick a
// constructor overload and infer the component's public instance shape
// instead of its props, which vue-tsc then rejects. The runtime call is
// unaffected — props and slots reach the component exactly as given.
function mount(props: Record<string, unknown>, slots: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(DataTable, props as any, slots) }).mount(el)
  return el
}

const rows = (n: number) =>
  Array.from({ length: n }, (_, i) => ({ id: i + 1, name: `row ${String(i + 1).padStart(2, '0')}` }))

const columns = [
  { key: 'id', label: 'ID' },
  { key: 'name', label: 'Name', sortable: true },
]

const bodyTexts = (el: HTMLElement) =>
  [...el.querySelectorAll('tbody td')].map((td) => td.textContent?.trim())

describe('DataTable', () => {
  it('renders a page of rows and slices at pageSize', () => {
    const el = mount({ columns, data: rows(25), pageSize: 20 })
    expect(el.querySelectorAll('tbody tr').length).toBe(20)
    expect(el.textContent).toContain('row 01')
    expect(el.textContent).not.toContain('row 21') // page two
  })

  it('filters on searchKey', async () => {
    const el = mount({ columns, data: rows(25), searchKey: 'name' })
    const input = el.querySelector('input')!
    input.value = 'row 07'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(el.querySelectorAll('tbody tr').length).toBe(1)
    expect(el.textContent).toContain('row 07')
  })

  it('sorts when a sortable header is clicked', async () => {
    const el = mount({ columns, data: rows(5) })
    const sortBtn = [...el.querySelectorAll('thead button')].find((b) =>
      b.textContent?.includes('Name'),
    ) as HTMLButtonElement
    sortBtn.click() // asc
    await nextTick()
    sortBtn.click() // desc
    await nextTick()
    expect(bodyTexts(el)[1]).toBe('row 05')
  })

  it('renders a #cell-<key> slot instead of the raw value', () => {
    const el = mount(
      { columns, data: rows(2) },
      { 'cell-name': ({ row }: { row: Record<string, unknown> }) => h('em', String(row.name)) },
    )
    expect(el.querySelectorAll('tbody em').length).toBe(2)
  })

  it('shows the empty state when the table itself is empty', () => {
    const el = mount({ columns, data: [] })
    expect(el.textContent).toContain('还没有记录。')
  })

  // A filter that matches nothing is not an empty table. Saying "No posts yet."
  // over 25 hidden rows reads as data loss.
  it('distinguishes an empty table from a filter that matches nothing', async () => {
    const el = mount(
      { columns, data: rows(25), searchKey: 'name' },
      { empty: () => 'No posts yet.' },
    )
    const input = el.querySelector('input')!
    input.value = 'zzz'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(el.textContent).toContain('没有匹配该筛选条件的记录。')
    expect(el.textContent).not.toContain('No posts yet.')
  })

  // PaginationItem renders its own <button>, so using it to wrap
  // First/Previous/Next/Last nests a button inside a button — browsers reparse
  // that and hydration then disagrees with the server's markup. The real guard
  // is server/ssr_fixture_test.go's maxButtonDepth, but that only reaches this
  // component once the generated fixture consumes it; until then a regression
  // here would be invisible. happy-dom keeps the nesting as authored rather
  // than reparenting it, so the selector below sees what the browser would
  // have had to fix up.
  it('renders no button inside a button, pager included', async () => {
    const el = mount({ columns, data: rows(45) }) // 3 pages, so the pager renders
    await nextTick()
    expect(el.querySelector('nav')).not.toBeNull() // the pager is actually present
    expect(el.querySelector('button button')).toBeNull()
  })

  // searchKey is a field key — "name", "username". Showing it to the operator
  // reads like a leak of the schema; the header already has a human name for
  // that column, so the placeholder uses it.
  it('names the search column the way its header does', () => {
    const el = mount({ columns, data: rows(3), searchKey: 'name' })
    const input = el.querySelector('input')!
    expect(input.getAttribute('placeholder')).toContain('Name')
    expect(input.getAttribute('placeholder')).not.toContain('按name')
  })
})
