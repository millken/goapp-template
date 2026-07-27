import { describe, expect, it } from 'vitest'
import { mediaUrl } from './media-url'

// One public URL, built one way. AdminShell, ImagePicker and the user list all
// used to hand-roll `${prefix}/${path}` themselves, which is exactly how
// "photo#1.png" ended up permanently broken: unescaped, a browser reads
// "#1.png" as a fragment rather than part of the path. These pin the same
// cases internal/service/storage/storage_test.go pins for Service.URLFor, so
// the two sides stay provably in sync.
describe('mediaUrl', () => {
  it('escapes a fragment-looking "#"', () => {
    expect(mediaUrl('/uploads', 'photo#1.png')).toBe('/uploads/photo%231.png')
  })

  it('escapes a literal "%"', () => {
    expect(mediaUrl('/uploads', '100%.png')).toBe('/uploads/100%25.png')
  })

  it('escapes a query-looking "?"', () => {
    expect(mediaUrl('/uploads', 'a?b.png')).toBe('/uploads/a%3Fb.png')
  })

  it('round-trips a Chinese filename', () => {
    expect(mediaUrl('/uploads', '照片.png')).toBe(
      '/uploads/%E7%85%A7%E7%89%87.png',
    )
  })

  it('escapes per segment, leaving the directory separator alone', () => {
    expect(mediaUrl('/uploads', 'dir#1/a#b.png')).toBe(
      '/uploads/dir%231/a%23b.png',
    )
  })

  it('trims stray slashes at the prefix/path join', () => {
    expect(mediaUrl('/uploads/', '/a/b.png')).toBe('/uploads/a/b.png')
  })
})
