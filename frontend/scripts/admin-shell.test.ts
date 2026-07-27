// @vitest-environment node
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// AdminShell computes the signed-in user's avatar URL from its urlPrefix prop,
// falling back to a hardcoded '/uploads' when the prop is absent. resolve (in
// internal/controller/admin/auth.go) sets the real prefix — storage.url_prefix,
// which an operator can point anywhere — as a shared page prop alongside
// adminMenu, adminUser and the rest, but only forwarding it from a page to the
// shell makes it take effect. A page that forgets shows every other admin page
// a broken avatar image the moment the operator's prefix differs from the
// default, and nothing about a 404'd <img> points back at the missing
// attribute — so this is a test, not a convention.
//
// Generator templates are in scope too: a scaffolded page copies whichever
// habit templates/admin/*.vue.tmpl has, for every resource its author ever
// generates. But `cmd/goappctl` is tooling that `goappctl init` deletes from
// every generated project, so that half only exists in the template's own
// checkout. It is a separate `it` — skipped, not silently empty, when the
// directory is gone — so a generated project's `pnpm test` passes while the
// template's own run still exercises it.
const root = new URL('..', import.meta.url).pathname
const templatesDir = '../cmd/goappctl/internal/scaffold/templates/admin'
const templatesPresent = existsSync(join(root, templatesDir))

function filesWithSuffix(dir: string, suffix: string, out: string[] = []): string[] {
  for (const entry of readdirSync(join(root, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name)
    if (entry.isDirectory()) filesWithSuffix(rel, suffix, out)
    else if (entry.name.endsWith(suffix)) out.push(rel)
  }
  return out
}

function offendingAdminShellUsages(files: string[]): string[] {
  const offenders: string[] = []
  for (const file of files) {
    const src = readFileSync(join(root, file), 'utf8')
    for (const m of src.matchAll(/<AdminShell\b[^>]*>/g)) {
      const tag = m[0]
      if (!/:url-prefix="urlPrefix"/.test(tag)) {
        offenders.push(`${file}: ${tag.replace(/\s+/g, ' ')}`)
      }
    }
  }
  return offenders
}

describe('every page that renders AdminShell forwards urlPrefix', () => {
  it('has no <AdminShell> usage missing :url-prefix="urlPrefix" (pages)', () => {
    const files = filesWithSuffix('pages/admin', '.vue')
    // The scan found the tree, not nothing.
    expect(files.length).toBeGreaterThan(5)
    expect(offendingAdminShellUsages(files)).toEqual([])
  })

  // Only runs where cmd/goappctl exists — the template itself, not a
  // generated project. it.skipIf reports as a skip in the run's output, so a
  // directory that went missing for the wrong reason is visible, not a test
  // that quietly covers nothing.
  it.skipIf(!templatesPresent)(
    'has no <AdminShell> usage missing :url-prefix="urlPrefix" (generator templates)',
    () => {
      const files = filesWithSuffix(templatesDir, '.vue.tmpl')
      expect(files.length).toBeGreaterThan(0)
      expect(offendingAdminShellUsages(files)).toEqual([])
    },
  )
})
