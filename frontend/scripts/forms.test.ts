// @vitest-environment node
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// A form that posts without the CSRF field submits fine in development — the
// check has nothing to compare until the token is delivered — and then answers
// 403 the moment enforcement is on. Nothing about that failure points at the
// missing field, so it gets a test rather than a convention.
//
// Generator templates are in scope too: a scaffolded page copies whichever
// habit templates/*/form.vue.tmpl has, for every resource its author ever
// generates. But `cmd/goappctl` is tooling that `goappctl init` deletes from
// every generated project, so that half only exists in the template's own
// checkout. It is a separate `it` — skipped, not silently empty, when the
// directory is gone — so a generated project's `pnpm test` passes while the
// template's own run still exercises it.
const root = new URL('..', import.meta.url).pathname
const templatesDir = '../cmd/goappctl/internal/scaffold/templates'
const templatesPresent = existsSync(join(root, templatesDir))

function filesWithSuffix(dir: string, suffix: string, out: string[] = []): string[] {
  for (const entry of readdirSync(join(root, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name)
    if (entry.isDirectory()) filesWithSuffix(rel, suffix, out)
    else if (entry.name.endsWith(suffix)) out.push(rel)
  }
  return out
}

function offendingForms(files: string[]): string[] {
  const offenders: string[] = []
  for (const file of files) {
    const src = readFileSync(join(root, file), 'utf8')
    // Each <form …> opening tag through to its </form>.
    for (const m of src.matchAll(/<form\b[^>]*>[\s\S]*?<\/form>/g)) {
      const block = m[0]
      if (!/method="post"/i.test(block)) continue
      if (block.includes('CsrfField') || block.includes('name="_csrf"')) continue
      offenders.push(`${file}: ${block.slice(0, 80).replace(/\s+/g, ' ')}…`)
    }
  }
  return offenders
}

describe('every posting form carries the CSRF field', () => {
  it('has no form that posts without it (pages)', () => {
    const files = [
      ...filesWithSuffix('pages', '.vue'),
      ...filesWithSuffix('src/components/admin', '.vue'),
    ]
    expect(offendingForms(files)).toEqual([])
  })

  // Only runs where cmd/goappctl exists — the template itself, not a
  // generated project. it.skipIf reports as a skip in the run's output, so a
  // directory that went missing for the wrong reason is visible, not a test
  // that quietly covers nothing.
  it.skipIf(!templatesPresent)('has no form that posts without it (generator templates)', () => {
    const files = filesWithSuffix(templatesDir, '.vue.tmpl')
    // The scan found the tree, not nothing.
    expect(files.length).toBeGreaterThan(0)
    expect(offendingForms(files)).toEqual([])
  })
})
