import { createSSRRender } from 'millken-inertia-vue/ssr-render'
import ssrModules from './ssr-modules'

const { inertiaRenderComponent, inertiaRenderTemplate } = createSSRRender(ssrModules)
export { inertiaRenderComponent, inertiaRenderTemplate }
