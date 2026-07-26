import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { quickjsPolyfills } from './ssr/polyfills'

// SSR bundle for the Go quickjs SSR runtime. Produces a single self-contained
// CJS file (dist/ssr-render-cjs.js) whose module.exports exposes
// inertiaRenderComponent / inertiaRenderTemplate. Page components are
// auto-discovered via import.meta.glob — no codegen step.
//   pnpm build:ssr   (or: pnpm vite build --config vite.config.ssr.ts)
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@pages': fileURLToPath(new URL('./pages', import.meta.url)),
    },
    extensions: ['.vue', '.ts', '.js'],
  },
  define: {
    __VUE_OPTIONS_API__: 'false',
    __VUE_PROD_DEVTOOLS__: 'false',
    __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false',
    'process.env.NODE_ENV': '"production"',
    'process.env.VUE_ENV': '"server"',
  },
  build: {
    outDir: 'dist',
    assetsDir: '',
    emptyOutDir: false, // dist/ also holds the client build; don't wipe it
    ssr: './ssr-esm-render.ts',
    minify: 'esbuild',
    rollupOptions: {
      output: {
        format: 'cjs',
        entryFileNames: 'ssr-render-cjs.js',
        banner: quickjsPolyfills,
      },
    },
  },
  ssr: {
    noExternal: true,
  },
})
