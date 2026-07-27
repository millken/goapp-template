import { describe, expect, it } from 'vitest'

// Source read through Vite's ?raw rather than node's fs: this config compiles
// against DOM libs with no node types, and widening that for one test would be
// the wrong trade.
import alertIndex from './alert/index.ts?raw'
import sonnerIndex from './sonner/index.ts?raw'
import sonnerSource from './sonner/Sonner.vue?raw'

// The files under components/ui are copies of shadcn-vue registry items, and
// `gen ui --force` overwrites them wholesale to pick up upstream changes. Where
// we have deliberately diverged, that overwrite silently reverts us — and every
// deviation below fails quietly rather than loudly: a missing stylesheet leaves
// toasts unstyled in a corner, a missing variant just renders the default one.
//
// So each deviation gets a test. If one fails right after a `gen ui --force`,
// the answer is to re-apply the deviation, not to delete the test. README's
// 后台 UI 组件 section lists them in prose as well.
describe('local deviations from the shadcn-vue registry', () => {
  it('sonner imports the stylesheet vue-sonner 2.x ships separately', () => {
    // Without it the toaster has neither positioning nor card styles: bare text
    // wherever the document flow puts it. The registry copy omits the import.
    expect(sonnerSource).toContain(`import "vue-sonner/style.css"`)
  })

  it('alert keeps the success variant upstream does not have', () => {
    // Upstream ships default and destructive only; flash messages need a third.
    expect(alertIndex).toContain('success')
  })

  it('registry imports are rewritten to this project’s paths', () => {
    // Registry items import from @/registry/default/ui; gen ui rewrites that to
    // @/components/ui on the way in. A file still carrying the registry path
    // would not resolve here.
    expect(sonnerIndex).not.toContain('@/registry/default/ui')
    expect(alertIndex).not.toContain('@/registry/default/ui')
  })
})
