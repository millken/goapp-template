import { createSSRRender } from './ssr/render'
import ssrModules from './ssr-modules'

const { inertiaRenderComponent, inertiaRenderTemplate } = createSSRRender(ssrModules)

// Named exports on the entry point are deterministically retained by esbuild
// (unaffected by minify/tree-shaking) and compiled to `exports.X = ...` in the
// CJS bundle, which the Go runtimes read as `module.exports.X` / `exports.X`.
// Must stay named exports — a `export default { ... }` would land on
// `exports.default` and the Go side would no longer find the functions.
export { inertiaRenderComponent, inertiaRenderTemplate }
