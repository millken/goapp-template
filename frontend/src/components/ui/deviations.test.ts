// @vitest-environment node
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// The files under components/ui are copies of shadcn-vue registry items, and
// `gen ui --force` overwrites them wholesale to pick up upstream changes. Where
// we have deliberately diverged, that overwrite silently reverts us — and both
// deviations below fail quietly rather than loudly: a missing stylesheet leaves
// toasts unstyled in a corner, a missing variant just renders the default one.
//
// So each deviation gets a test. If one of these fails right after a `gen ui
// --force`, the answer is to re-apply the deviation, not to delete the test.
// README's 后台 UI 组件 section lists them in prose as well.
const read = (rel: string) =>
  readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8')

describe('local deviations from the shadcn-vue registry', () => {
  it('sonner imports the stylesheet vue-sonner 2.x ships separately', () => {
    // Without it the toaster has neither positioning nor card styles: bare text
    // wherever the document flow puts it. The registry copy omits the import.
    expect(read('./sonner/Sonner.vue')).toContain(`import "vue-sonner/style.css"`)
  })

  it('alert keeps the success variant upstream does not have', () => {
    // Upstream ships default and destructive only; flash messages need a third.
    expect(read('./alert/index.ts')).toContain('success')
  })

  it('registry imports are rewritten to this project’s paths', () => {
    // Registry items import from @/registry/default/ui; gen ui rewrites that to
    // @/components/ui on the way in. A file still carrying the registry path
    // would not resolve here.
    expect(read('./sonner/index.ts')).not.toContain('@/registry/default/ui')
    expect(read('./alert/index.ts')).not.toContain('@/registry/default/ui')
  })
})
