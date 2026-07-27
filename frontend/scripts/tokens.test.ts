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
