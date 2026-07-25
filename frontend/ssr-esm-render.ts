import { createSSRRender } from './ssr/render'
import type { Component } from 'vue'

// Auto-discover page components at build time via Vite's import.meta.glob
// (eager: the SSR bundle needs the components inlined). Keys are normalized to
// match the names the Go side passes to RenderComponent, e.g.
// './pages/Home.vue' -> 'Home', './pages/fund/index.vue' -> 'fund/index'.
const rawModules = import.meta.glob('./pages/**/*.vue', { eager: true })

function pageKey(p: string): string {
  const i = p.lastIndexOf('pages/')
  return (i >= 0 ? p.slice(i + 'pages/'.length) : p).replace(/\.vue$/, '')
}

const modules: Record<string, Component> = Object.fromEntries(
  Object.entries(rawModules).map(([path, mod]) => [
    pageKey(path),
    (mod as { default?: Component }).default ?? (mod as Component),
  ]),
)

const { inertiaRenderComponent, inertiaRenderTemplate } = createSSRRender(modules)

// Named exports are compiled to `exports.X = ...` in the CJS bundle, which the
// Go runtime reads as `module.exports.X`. Keep them named — `export default`
// would land on `exports.default` and the Go side would no longer find them.
export { inertiaRenderComponent, inertiaRenderTemplate }
