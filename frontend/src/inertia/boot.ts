import { initModules, mountView, type InitModulesOptions } from './view-loader'
import { enablePjax } from './pjax'
import { INERTIA_DATA_PLACEHOLDER, INERTIA_VIEW_KEY } from './constants'

export interface BootOptions extends InitModulesOptions {
  /** Mount target. Defaults to #app, then document.body. */
  el?: HTMLElement | null
  /** Opt out of PJAX navigation entirely. Defaults to enabled. */
  pjax?: boolean
}

/**
 * Bootstrap the Inertia client app.
 *
 * Reads page data from `target.dataset.page` or `window.__INERTIA_PAGE_DATA__`,
 * picks the view name from `INERTIA_VIEW_KEY`, then hydrates SSR markup if
 * present (otherwise creates a fresh client app).
 */
export function boot(
  modules: Record<string, () => Promise<any>>,
  options: BootOptions = {},
): void {
  initModules(modules, { setup: options.setup })

  if (typeof document === 'undefined') return
  // Enabled before the first view mounts so the entry the page loaded on is
  // seeded with history state; otherwise the first Back has nothing to act on.
  if (options.pjax !== false) {
    enablePjax({
      mount: async (view, props) => {
        await mountView(view, props)
      },
    })
  }

  void mountInitialView(options.el)
}

async function mountInitialView(el?: HTMLElement | null): Promise<void> {
  const target = el || document.getElementById('app') || document.body
  const page = parsePageData(target.dataset.page ?? (window as any).__INERTIA_PAGE_DATA__)
  const { [INERTIA_VIEW_KEY]: viewName = 'App', ...props } = page

  try {
    await mountView(viewName as string, props, target, { hydrate: true })
  } catch (error) {
    console.error(`Error loading view ${viewName}`, error)
  }
}

function parsePageData(raw: unknown): Record<string, any> {
  // The placeholder is what the server leaves in the HTML when it has no page data.
  if (typeof raw !== 'string' || raw === '' || raw === INERTIA_DATA_PLACEHOLDER) return {}

  try {
    return JSON.parse(raw) || {}
  } catch (error) {
    console.error('Failed to parse page JSON:', error)
    return {}
  }
}
