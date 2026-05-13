/**
 * QuickJS polyfills for APIs missing from the QuickJS runtime.
 *
 * QuickJS has neither browser nor Node.js globals by default.
 * Vue 3 SSR renderer requires: atob/btoa, TextEncoder/TextDecoder.
 * The `entities` package (Vue SSR dep since Vue 3.5.26) uses atob.
 */
export const quickjsPolyfills = `

`
