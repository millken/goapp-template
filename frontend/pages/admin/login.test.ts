// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { createApp, h } from 'vue'
import Login from './login.vue'

function mount(props: Record<string, unknown>) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(Login, props as any) }).mount(el)
  return el
}

describe('admin login page', () => {
  // This is the one admin page outside AdminShell, which renders flash for
  // everything else. The admin area bounces a disabled user here with the reason
  // staged as a flash; if the page ignores the prop, the message is delivered and
  // discarded and the user sees a blank form. Asserting the rendered DOM, not the
  // prop: a test that only checked the prop arrived would pass against exactly
  // the page that drops it.
  it('renders a flash staged by the admin area', () => {
    const el = mount({ flash: { error: '该账号已被禁用。' } })
    expect(el.textContent).toContain('该账号已被禁用。')
  })

  it('renders a failed sign-in error', () => {
    const el = mount({ error: 'invalid username or password' })
    expect(el.textContent).toContain('invalid username or password')
  })

  it('shows neither when there is nothing to say', () => {
    const el = mount({})
    expect(el.querySelector('[role="alert"]')).toBeNull()
  })
})
