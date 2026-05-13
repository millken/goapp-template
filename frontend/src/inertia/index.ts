export { boot, type BootOptions } from './boot'
export {
  initModules,
  hasView,
  loadView,
  mountView,
  getCurrentApp,
  unmountCurrentApp,
  clearViewCache,
  type InitModulesOptions,
  type MountViewOptions,
} from './view-loader'
export { pjaxClick, enablePjax } from './pjax-loader'
export { INERTIA_VIEW_KEY, INERTIA_DATA_PLACEHOLDER } from './constants'
export { default as InertiaLink } from './Link.vue'
