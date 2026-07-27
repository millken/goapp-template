// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import PermissionGrid from './PermissionGrid.vue'

function mount(props: Record<string, unknown>) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(PermissionGrid, props as any) }).mount(el)
  return el
}

const box = (el: HTMLElement, resource: string, verb: string) =>
  el.querySelector(`input[data-resource="${resource}"][data-verb="${verb}"]`) as HTMLInputElement

const rows = [
  { resource: 'post', access: false, modify: false },
  { resource: 'user', access: true, modify: false },
]

describe('PermissionGrid', () => {
  it('renders one row per resource with the stored state', () => {
    const el = mount({ rows })
    expect(box(el, 'post', 'access').checked).toBe(false)
    expect(box(el, 'user', 'access').checked).toBe(true)
  })

  // The implication is one-way, matching permSet.Allows on the server.
  it('ticks access when modify is ticked', async () => {
    const el = mount({ rows })
    const modify = box(el, 'post', 'modify')
    modify.checked = true
    modify.dispatchEvent(new Event('change'))
    await nextTick()
    expect(box(el, 'post', 'access').checked).toBe(true)
  })

  it('unticks modify when access is unticked', async () => {
    const el = mount({ rows: [{ resource: 'post', access: true, modify: true }] })
    const access = box(el, 'post', 'access')
    access.checked = false
    access.dispatchEvent(new Event('change'))
    await nextTick()
    expect(box(el, 'post', 'modify').checked).toBe(false)
  })

  // Ticking access alone must NOT tick modify — that would grant write access
  // to anyone given read access.
  it('does not tick modify when access is ticked', async () => {
    const el = mount({ rows })
    const access = box(el, 'post', 'access')
    access.checked = true
    access.dispatchEvent(new Event('change'))
    await nextTick()
    expect(box(el, 'post', 'modify').checked).toBe(false)
  })

  it('lists stale keys with a warning that saving removes them', () => {
    const el = mount({ rows, stale: ['billing.modify'] })
    expect(el.textContent).toContain('billing.modify')
    expect(el.textContent).toContain('保存将清除')
  })

  it('says the grid is advisory for a superuser group', () => {
    const el = mount({ rows, superuser: true })
    expect(el.textContent).toContain('绕过所有权限检查')
  })
})
