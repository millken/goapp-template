import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const devPort = Number(process.env.DEV_PORT) || 5173

export default defineConfig({
  plugins: [vue()],
  // Mirror the Vue production feature flags set by the SSR build
  // (vite.config.ssr.ts) so the client and server compile Vue identically —
  // avoids hydration mismatches and shrinks the client bundle.
  define: {
    __VUE_OPTIONS_API__: 'false',
    __VUE_PROD_DEVTOOLS__: 'false',
    __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false',
    __VUE_FEATURE_SUSPENSE__: 'false',
    __VUE_FEATURE_TELEPORT__: 'false',
    __VUE_FEATURE_TRANSITION__: 'false',
    __VUE_FEATURE_KEEP_ALIVE__: 'false',
    __VUE_FEATURE_SCOPED_SLOT__: 'false',
    'process.env.NODE_ENV': '"production"',
  },
  server: {
    host: '127.0.0.1',
    port: devPort,
    strictPort: true,
    // In dev mode the page is served by the Go backend (e.g. :8080) which
    // reverse-proxies non-route requests to this Vite server. The HMR
    // WebSocket would otherwise try to connect to the backend port and fail.
    // Point the HMR client straight at the Vite dev server port instead.
    hmr: {
      host: '127.0.0.1',
      port: devPort,
      protocol: 'ws',
    },
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@pages': fileURLToPath(new URL('./pages', import.meta.url)),
    },
  },
  build: {
    rollupOptions: {
      input: { main: './src/main.ts' },
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
})
