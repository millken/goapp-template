import type { Component } from 'vue'

// Auto-discover page components via Vite's import.meta.glob (lazy: each page is
// loaded on demand). Keys are normalized to match Go-side component names:
// '../../pages/Home.vue' -> 'Home', '../../pages/fund/index.vue' -> 'fund/index'.
const raw = import.meta.glob('../../pages/**/*.vue')

function pageKey(p: string): string {
  const i = p.lastIndexOf('pages/')
  return (i >= 0 ? p.slice(i + 'pages/'.length) : p).replace(/\.vue$/, '')
}

export const modules: Record<string, () => Promise<Component>> = Object.fromEntries(
  Object.entries(raw).map(([p, loader]) => [pageKey(p), loader as () => Promise<Component>]),
)
