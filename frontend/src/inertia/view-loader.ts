import { createApp, createSSRApp, type App, type Component } from 'vue'

type ModulesMap = Record<string, () => Promise<any>>

const cache = new Map<string, Component>()
let currentApp: App | null = null
let _modules: ModulesMap = {}
let _setup: ((app: App) => void) | undefined

export interface InitModulesOptions {
  /** Hook invoked on every Vue App created by the loader (plugins, globals). */
  setup?: (app: App) => void
}

/** Initialize the view loader with a modules map. Call once at app startup. */
export function initModules(modules: ModulesMap, opts: InitModulesOptions = {}): void {
  _modules = modules
  _setup = opts.setup
}

export function hasView(name: string): boolean {
  return !!_modules[name]
}

export async function loadView(name: string): Promise<Component> {
  if (!_modules[name]) {
    throw new Error(`View ${name} not found`)
  }
  const cached = cache.get(name)
  if (cached) return cached
  const mod = await _modules[name]()
  const component = ((mod as any).default || mod) as Component
  cache.set(name, component)
  return component
}

export interface MountViewOptions {
  /**
   * If true, treat existing markup inside the target as SSR output and
   * hydrate it instead of replacing it. Used on first boot.
   */
  hydrate?: boolean
}

export async function mountView(
  viewName: string,
  props: Record<string, any> = {},
  targetElement: HTMLElement | null = null,
  opts: MountViewOptions = {},
): Promise<App> {
  if (!hasView(viewName)) {
    throw new Error(`View ${viewName} not found`)
  }

  const component = await loadView(viewName)
  const target = targetElement || document.getElementById('app') || document.body

  if (currentApp) {
    currentApp.unmount()
    currentApp = null
  }

  if (opts.hydrate && target.firstChild) {
    const app = createSSRApp(component, props)
    if (_setup) _setup(app)
    app.mount(target, true)
    currentApp = app
    return app
  }

  target.innerHTML = ''
  const app = createApp(component, props)
  if (_setup) _setup(app)
  app.mount(target)
  currentApp = app
  return app
}

export function getCurrentApp(): App | null {
  return currentApp
}

export function unmountCurrentApp(): void {
  if (currentApp) {
    currentApp.unmount()
    currentApp = null
  }
}

/** Drop cached components. Useful for HMR or long-running SPAs. */
export function clearViewCache(): void {
  cache.clear()
}
