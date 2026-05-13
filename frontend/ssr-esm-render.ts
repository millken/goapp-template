import { createSSRRender } from './ssr/render'
import ssrModules from './ssr-modules'

const { inertiaRenderComponent, inertiaRenderTemplate } = createSSRRender(ssrModules)

// Explicit module.exports assignment prevents esbuild from tree-shaking
// away entry-point exports when building CJS bundles with minification.
;(module as any).exports = { inertiaRenderComponent, inertiaRenderTemplate }
