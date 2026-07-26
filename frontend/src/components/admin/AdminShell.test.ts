// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import AdminShell from './AdminShell.vue'

// Mounted via createApp directly: the repo deliberately has no @vue/test-utils.
function mount(props: Record<string, unknown>) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(AdminShell, props as any) }).mount(el)
  return el
}

const props = {
  menu: [
    { title: 'Posts', path: '/admin/post', section: 'Content' },
    { title: 'Users', path: '/admin/user', section: 'Access' },
  ],
  user: { id: 1, username: 'alice' },
  mount: '/admin',
  currentPath: '/admin/post',
}

// The dropdown teleports its content, so query the document rather than the
// mount point.
const openUserMenu = async (el: HTMLElement) => {
  const trigger = [...el.querySelectorAll('button')].find((b) =>
    b.textContent?.includes('alice'),
  ) as HTMLButtonElement
  trigger.click()
  await nextTick()
  await nextTick()
}

describe('AdminShell', () => {
  it('shows only the active section, and puts it in the breadcrumb', () => {
    const el = mount(props)
    // Content is active, so its item shows and Access's does not — that is the
    // point of two columns rather than one long list.
    const panel = el.querySelectorAll('aside')[1]
    expect(panel.textContent).toContain('Posts')
    expect(panel.textContent).not.toContain('Users')
    // Home is contributed by the shell rather than the menu, which makes it the
    // one item likely to be concatenated into every panel by mistake.
    expect(panel.textContent).not.toContain('Overview')

    const crumbs = el.querySelector('nav[aria-label="Breadcrumb"]')!
    expect(crumbs.textContent).toContain('Content')
    expect(crumbs.textContent).toContain('Posts')
  })

  it('treats a subpage as its parent item, and appends the crumb tail', () => {
    const el = mount({ ...props, currentPath: '/admin/post/3/edit', crumb: 'Edit' })
    const crumbs = el.querySelector('nav[aria-label="Breadcrumb"]')!
    expect(crumbs.textContent).toContain('Posts')
    expect(crumbs.textContent).toContain('Edit')
    // The parent becomes a link once there is a tail after it.
    expect(crumbs.querySelector('a[href="/admin/post"]')).not.toBeNull()
  })

  it('resolves an unregistered admin path to Home by prefix', () => {
    const el = mount({ ...props, currentPath: '/admin/nothing-registered-here' })
    const crumbs = el.querySelector('nav[aria-label="Breadcrumb"]')!
    expect(crumbs.textContent).toContain('Home')
    expect(crumbs.textContent).toContain('Overview')
  })

  // The no-match fallback needs a path outside the mount to reach at all: Home's
  // own item is the mount, so it prefix-matches everything beneath it. Only a
  // mount that disagrees with the served path gets here — which is exactly the
  // misconfiguration worth degrading gracefully rather than crashing on.
  it('falls back to Home for a path outside the mount', () => {
    const el = mount({ ...props, currentPath: '/somewhere-else' })
    const crumbs = el.querySelector('nav[aria-label="Breadcrumb"]')!
    expect(crumbs.textContent).toContain('Home')
    expect(el.querySelectorAll('aside')[1].textContent).toContain('Overview')
  })

  // Logout is the one control that must really submit. It is a menu item inside
  // a form, and reka-ui menu items intercept selection — if a future version
  // calls preventDefault on the click, the button would silently stop working
  // and nothing else here would notice.
  it('logs out by submitting a real form POST', async () => {
    const el = mount(props)
    await openUserMenu(el)

    const form = document.querySelector('form[method="post"]') as HTMLFormElement
    expect(form).not.toBeNull()
    expect(form.getAttribute('action')).toBe('/admin/logout')

    const submit = form.querySelector('button[type="submit"]') as HTMLButtonElement
    expect(submit).not.toBeNull()

    // happy-dom does not navigate, so listen for the event instead.
    const submitted = vi.fn((e: Event) => e.preventDefault())
    form.addEventListener('submit', submitted)
    submit.click()
    await nextTick()
    expect(submitted).toHaveBeenCalled()
  })

  // The rail seeds Home itself, so a resource registering that exact section name
  // used to push a second column with the same title and a duplicate key.
  it('merges a section named Home into the built-in one', () => {
    const el = mount({
      ...props,
      menu: [{ title: 'Settings', path: '/admin/settings', section: 'Home' }],
      currentPath: '/admin/settings',
    })
    const rail = el.querySelectorAll('aside')[0]
    expect(rail.textContent!.match(/Home/g)).toHaveLength(1)
    // And the registered item lands in Home's own panel, beside Overview.
    const panel = el.querySelectorAll('aside')[1]
    expect(panel.textContent).toContain('Overview')
    expect(panel.textContent).toContain('Settings')
  })

  it('renders no button inside a button', () => {
    const el = mount(props)
    expect(el.querySelector('button button')).toBeNull()
  })
})
