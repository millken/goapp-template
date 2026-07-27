// @vitest-environment node
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// A check over the source tree rather than over a component, which is why it
// lives here: tsconfig.node.json covers scripts/ and gives it node's types, so
// the files can just be read. Importing CSS with ?raw does not work under
// vitest — CSS handling is off, and the import comes back as an empty string,
// which is how the first version of this file passed while examining nothing.
const root = new URL('..', import.meta.url).pathname

function sources(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(join(root, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name)
    if (entry.isDirectory()) {
      sources(rel, out)
    } else if (/\.(vue|ts)$/.test(entry.name) && !entry.name.endsWith('.test.ts')) {
      out.push(rel)
    }
  }
  return out
}

const read = (rel: string) => readFileSync(join(root, rel), 'utf8')

// Semantic colours are the ones the theme has to answer for. Literal palette
// classes (text-white, bg-red-500) are not in scope: they resolve without it.
const SEMANTIC =
  /\b(?:text|bg|border|ring|fill|from|to|via)-(background|foreground|card|popover|primary|secondary|muted|accent|destructive|sidebar)(-foreground)?\b/g

// A <button> is shrink-to-fit even when it is a flex container, so a menu item
// rendered as one stops short of the menu's width and its hover highlight boxes
// the text rather than the row. Nothing about that fails loudly — it just looks
// slightly wrong, and only on hover, which is how it shipped once already.
describe('dropdown menu items rendered as buttons', () => {
  it('carry w-full so the highlight fills the row', () => {
    const files = [...sources('src/components'), ...sources('pages')]
    const offenders: string[] = []
    for (const file of files) {
      for (const m of read(file).matchAll(/<DropdownMenuItem\b[^>]*>/g)) {
        const tag = m[0]
        if (tag.includes('as="button"') && !/\bclass="[^"]*\bw-full\b/.test(tag)) {
          offenders.push(`${file}: ${tag.replace(/\s+/g, ' ')}`)
        }
      }
    }
    expect(offenders).toEqual([])
  })
})

// The compiled-output counterpart to the source audit below. That one knows which
// component asked for a colour but only sees the semantic names it was told
// about; this one sees every custom property that survived the build, including
// ones no list mentions. Three visual bugs in a row came from a property that
// was referenced and never defined, and each rendered something plausible rather
// than failing — so the check is: nothing is used without a fallback unless it is
// defined, or injected at runtime by a library.
describe('compiled stylesheet', () => {
  const dist = join(root, 'dist/assets')

  // Injected on the element at runtime, so they are legitimately absent from the
  // stylesheet. A name that is not on this list and not defined is a real gap —
  // if a library upgrade adds one, this test should be read, not widened
  // reflexively.
  const RUNTIME_PREFIX = ['--tw-', '--reka-', '--radix-']
  const RUNTIME_NAMES = new Set([
    // vue-sonner sets these on the toaster from its own props.
    '--gap', '--width', '--offset', '--z-index', '--initial-height',
    '--front-toast-height', '--toasts-before', '--swipe-amount-x', '--swipe-amount-y',
    '--offset-top', '--offset-right', '--offset-bottom', '--offset-left',
    '--mobile-offset-top', '--mobile-offset-right', '--mobile-offset-bottom',
    '--mobile-offset-left',
  ])

  it('defines every custom property it reads without a fallback', () => {
    let css = ''
    try {
      for (const f of readdirSync(dist)) {
        if (f.endsWith('.css')) css += readFileSync(join(dist, f), 'utf8')
      }
    } catch {
      // Same convention as the Go SSR tests: a check that needs build output
      // skips rather than fails when there is none.
      return
    }
    if (!css) return

    const defined = new Set([...css.matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]))
    const missing = new Set<string>()
    // The comma form carries a fallback, so an undefined name is harmless there.
    for (const m of css.matchAll(/var\(\s*(--[\w-]+)\s*\)/g)) {
      const name = m[1]
      if (defined.has(name) || RUNTIME_NAMES.has(name)) continue
      if (RUNTIME_PREFIX.some((p) => name.startsWith(p))) continue
      missing.add(name)
    }
    expect([...missing]).toEqual([])
  })
})

describe('theme tokens', () => {
  const theme = read('src/styles/main.css')
  const declared = new Set([...theme.matchAll(/--color-([a-z-]+):/g)].map((m) => m[1]))

  // When a class has no token behind it nothing errors: Tailwind emits a
  // var(--color-…) that is undefined, the property falls back to whatever is
  // inherited, and the result is a plausible-looking wrong colour. That is how
  // the destructive button got near-black text on red in light mode — the theme
  // defined --destructive and never --destructive-foreground.
  it('declares every semantic colour the components ask for', () => {
    const files = [...sources('src/components'), ...sources('pages')]
    expect(files.length).toBeGreaterThan(20) // the scan found the tree, not nothing

    const missing = new Map<string, string>()
    for (const file of files) {
      for (const m of read(file).matchAll(SEMANTIC)) {
        const token = m[1] + (m[2] ?? '')
        if (!declared.has(token)) missing.set(token, file)
      }
    }
    expect([...missing].map(([t, f]) => `${t} (in ${f})`)).toEqual([])
  })

  // Three separate visual bugs traced back to the same cause: shadcn's base
  // layer was never fully copied into this theme. Tailwind v4's `border` sets
  // width and style but no colour, so 31 bare borders were drawn in
  // currentColor; nothing set the document background, so every translucent
  // surface composited over white; and --destructive-foreground was missing
  // entirely. None of them errored — each just looked plausibly wrong.
  it('carries the base layer the components assume', () => {
    // A universal selector giving borders the token, or every bare `border`
    // falls back to the text colour.
    expect(theme).toMatch(/\*[^{]*\{[^}]*border-color:\s*var\(--border\)/)
    // The document's own ground, or bg-*/40 composites over the browser default.
    expect(theme).toMatch(/\bbody\s*\{[^}]*background-color:\s*var\(--background\)/)
  })

  // The two destructive foregrounds are opposites on purpose, and a well-meaning
  // tidy-up that made them match would push one theme under AA. Measured on the
  // actual values: on the light red, near-white text is 4.56:1 and near-black
  // 4.15:1; on the lighter dark red it flips to 2.77:1 and 6.84:1.
  it('keeps the destructive foreground per-theme, not shared', () => {
    const block = (sel: string) =>
      theme.match(new RegExp(`${sel}\\s*\\{[\\s\\S]*?\\n\\}`))?.[0] ?? ''
    const of = (b: string) => b.match(/--destructive-foreground:\s*([^;]+);/)?.[1]?.trim()

    const light = of(block(':root'))
    const dark = of(block('\\.dark'))
    expect(light).toBeDefined()
    expect(dark).toBeDefined()
    expect(light).not.toBe(dark)
  })
})
