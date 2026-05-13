import { build, type BuildOptions } from 'esbuild'
import { createSSRBuildOptions } from 'millken-inertia-vue/ssr-build'
import vuePlugin from 'millken-esbuild-plugin-vue'

build(createSSRBuildOptions({
  entryPoints: ['./ssr-esm-render.ts'],
  plugins: [vuePlugin()],
}) as BuildOptions).then(() => {
  console.log('SSR build done')
}).catch((err) => {
  console.error('SSR build failed:', err)
  process.exit(1)
})
