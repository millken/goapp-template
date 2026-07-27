// @vitest-environment node
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// A form that posts without the CSRF field submits fine in development — the
// check has nothing to compare until the token is delivered — and then answers
// 403 the moment enforcement is on. Nothing about that failure points at the
// missing field, so it gets a test rather than a convention.
const root = new URL('..', import.meta.url).pathname

function vueFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(join(root, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name)
    if (entry.isDirectory()) vueFiles(rel, out)
    else if (entry.name.endsWith('.vue')) out.push(rel)
  }
  return out
}

describe('every posting form carries the CSRF field', () => {
  it('has no form that posts without it', () => {
    const offenders: string[] = []
    for (const file of [...vueFiles('pages'), ...vueFiles('src/components/admin')]) {
      const src = readFileSync(join(root, file), 'utf8')
      // Each <form …> opening tag through to its </form>.
      for (const m of src.matchAll(/<form\b[^>]*>[\s\S]*?<\/form>/g)) {
        const block = m[0]
        if (!/method="post"/i.test(block)) continue
        if (block.includes('CsrfField') || block.includes('name="_csrf"')) continue
        offenders.push(`${file}: ${block.slice(0, 80).replace(/\s+/g, ' ')}…`)
      }
    }
    expect(offenders).toEqual([])
  })
})
