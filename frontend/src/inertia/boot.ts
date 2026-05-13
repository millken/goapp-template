import { initModules, mountView, type InitModulesOptions } from './view-loader'
import { enablePjax } from './pjax-loader'
import { INERTIA_DATA_PLACEHOLDER, INERTIA_VIEW_KEY } from './constants'

export interface BootOptions extends InitModulesOptions {
  /** Mount target. Defaults to #app, then document.body. */
  el?: HTMLElement | null
  /** Disable PJAX navigation listener. Defaults to enabled. */
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
  if (options.pjax !== false) enablePjax()

  void (async () => {
    const target: HTMLElement =
      options.el || document.getElementById('app') || document.body

    const raw: string =
      target?.dataset?.page ?? ((window as any).__INERTIA_PAGE_DATA__ || '{}')

    let page: Record<string, any> = {}
    if (raw && raw !== INERTIA_DATA_PLACEHOLDER) {
      try {
        page = JSON.parse(raw) || {}
      } catch (e) {
        console.error('Failed to parse page JSON:', e)
      }
    }

    const { [INERTIA_VIEW_KEY]: viewName = 'App', ...props } = page

    try {
      await mountView(viewName as string, props, target, { hydrate: true })
    } catch (error) {
      console.error(`Error loading view ${viewName}`, error)
    }
  })()
}
