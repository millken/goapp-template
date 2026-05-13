import { build } from 'esbuild'
import { createSSRBuildOptions } from './ssr/build-options'
import vuePlugin from 'millken-esbuild-plugin-vue'

build(createSSRBuildOptions({
  entryPoints: ['./ssr-esm-render.ts'],
  plugins: [vuePlugin()],
})).then(() => {
  console.log('SSR build done')
}).catch((err) => {
  console.error('SSR build failed:', err)
  process.exit(1)
})
