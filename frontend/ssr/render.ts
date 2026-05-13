/**
 * SSR render functions exported to the Go runtime.
 *
 * Bundle is evaluated by QuickJS / Goja / V8go; functions are reached via
 *   module.exports.inertiaRenderComponent(name, jsonProps)
 */
import { renderToString } from 'vue/server-renderer'
import { createSSRApp, h, type App, type Component } from 'vue'

type PropRecord = Record<string, any>
type ModulesMap = Record<string, Component>

export interface SSRRenderOptions {
  /** Hook to register Vue plugins / global components on each SSR app. */
  setup?: (app: App) => void
}

function parseProps(input?: PropRecord | string): PropRecord {
  if (typeof input === 'string') {
    try {
      const parsed = JSON.parse(input)
      if (parsed && typeof parsed === 'object') return parsed as PropRecord
      return {}
    } catch (e: any) {
      throw new Error(`Invalid JSON props: ${e?.message ?? e}`)
    }
  }
  return (input ?? {}) as PropRecord
}

export function createSSRRender(modules: ModulesMap, opts: SSRRenderOptions = {}) {
  async function inertiaRenderComponent(
    componentName: string,
    props?: PropRecord | string,
  ): Promise<string> {
    const component = modules[componentName]
    if (!component) {
      throw new Error(`Component "${componentName}" not found`)
    }
    const parsedProps = parseProps(props)
    const app = createSSRApp({
      render() {
        return h(component, parsedProps)
      },
    })
    if (opts.setup) opts.setup(app)
    return renderToString(app)
  }

  async function inertiaRenderTemplate(
    template: string,
    props?: PropRecord | string,
  ): Promise<string> {
    const parsedProps = parseProps(props)
    const tempComponent = {
      template,
      data: () => parsedProps,
    }
    const app = createSSRApp({
      render() {
        return h(tempComponent)
      },
    })
    if (opts.setup) opts.setup(app)
    return renderToString(app)
  }

  return { inertiaRenderComponent, inertiaRenderTemplate }
}
