import { createApp, createSSRApp, type App, type Component } from 'vue'

type ModulesMap = Record<string, () => Promise<any>>

const cache = new Map<string, Component>()
let registry: ModulesMap = {}
let setupHook: ((app: App) => void) | undefined
let currentApp: App | null = null

export interface InitModulesOptions {
  /** Hook invoked on every Vue App created by the loader (plugins, globals). */
  setup?: (app: App) => void
}

/** Initialize the view loader with a modules map. Call once at app startup. */
export function initModules(modules: ModulesMap, opts: InitModulesOptions = {}): void {
  registry = modules
  setupHook = opts.setup
}

export function hasView(name: string): boolean {
  return !!registry[name]
}

export async function loadView(name: string): Promise<Component> {
  const loader = registry[name]
  if (!loader) {
    throw new Error(`View ${name} not found`)
  }

  const cached = cache.get(name)
  if (cached) return cached

  const mod = await loader()
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
  const component = await loadView(viewName)
  const target = targetElement || document.getElementById('app') || document.body

  unmountCurrentApp()

  // Hydration only makes sense when the target already holds server-rendered
  // markup; an empty target means the client renders from scratch.
  const hydrate = Boolean(opts.hydrate && target.firstChild)
  if (!hydrate) target.innerHTML = ''

  const app = hydrate ? createSSRApp(component, props) : createApp(component, props)
  setupHook?.(app)
  app.mount(target, hydrate)
  currentApp = app
  return app
}

export function getCurrentApp(): App | null {
  return currentApp
}

export function unmountCurrentApp(): void {
  if (!currentApp) return
  currentApp.unmount()
  currentApp = null
}

/** Drop cached components. Useful for HMR or long-running SPAs. */
export function clearViewCache(): void {
  cache.clear()
}
