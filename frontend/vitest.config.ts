import { fileURLToPath, URL } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// Standalone rather than merged into vite.config.ts: the build config carries a
// dev server, HMR settings and rollup inputs that mean nothing to a unit test,
// and keeping them apart leaves both readable.
//
// These tests never reach production. Vite bundles only what is reachable from
// its entries (src/main.ts, ssr-esm-render.ts); *.test.ts is neither an entry
// nor imported by one, and the Go binary embeds only frontend/dist.
export default defineConfig({
  // SFC support: any test that mounts a .vue component needs this. The pjax
  // tests are plain TS, which is why the config went without it for so long.
  plugins: [vue()],
  test: {
    // The pjax modules drive history, scroll and DOM events, so they need a
    // document. happy-dom is the lighter of the two usual choices.
    environment: 'happy-dom',
    // pages/ as well as src/: a test file placed beside a page would otherwise
    // be collected by nothing and pass by never running, which is how six
    // PermissionGrid cases went unexecuted while the suite reported green.
    // scripts/ holds checks over the source tree itself, which read files with
    // node's fs — tsconfig.node.json covers that directory and has the types.
    include: ['src/**/*.test.ts', 'pages/**/*.test.ts', 'scripts/**/*.test.ts'],
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@pages': fileURLToPath(new URL('./pages', import.meta.url)),
    },
  },
})
