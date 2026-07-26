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
  createApp({ render: () => h(DataTable, props as any, slots as any) }).mount(el)
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

  it('shows the empty state when no rows match', () => {
    const el = mount({ columns, data: [] })
    expect(el.textContent).toContain('No results.')
  })
})
