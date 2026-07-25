import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'

// Standalone rather than merged into vite.config.ts: the build config carries a
// dev server, HMR settings and rollup inputs that mean nothing to a unit test,
// and keeping them apart leaves both readable.
//
// These tests never reach production. Vite bundles only what is reachable from
// its entries (src/main.ts, ssr-esm-render.ts); *.test.ts is neither an entry
// nor imported by one, and the Go binary embeds only frontend/dist.
export default defineConfig({
  test: {
    // The pjax modules drive history, scroll and DOM events, so they need a
    // document. happy-dom is the lighter of the two usual choices.
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@pages': fileURLToPath(new URL('./pages', import.meta.url)),
    },
  },
})
