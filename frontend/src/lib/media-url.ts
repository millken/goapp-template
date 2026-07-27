// Builds the public URL for a path relative to the storage root, mirroring
// internal/service/storage/storage.go's Service.URLFor: each "/"-separated
// segment is escaped on its own, then rejoined — escaping the joined path as
// one string would also escape the "/" separators away.
//
// encodeURIComponent already escapes '#', '%', '?', space, and every
// non-ASCII byte the same way Go's url.PathEscape does (each UTF-8 byte of a
// multi-byte rune becomes its own %XX triple, so a Chinese filename survives
// intact). The one gap: PathEscape also escapes "!'()*", which
// encodeURIComponent treats as safe "sub-delim" characters and leaves alone —
// so those four are escaped by hand afterwards to match.
//
// This is what fixed "photo#1.png": unescaped, a browser reads "#1.png" as a
// URL fragment instead of part of the path, so the image 404s forever.
function escapeSegment(segment: string): string {
  return encodeURIComponent(segment).replace(
    /[!'()*]/g,
    (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`,
  )
}

// mediaUrl joins a configured url_prefix with a stored path, escaping the way
// the server does. `prefix` and `path` may each carry stray leading/trailing
// slashes (a page prop, a stored path with no fixed shape) — trimmed here so
// the join never doubles or drops one.
export function mediaUrl(prefix: string, path: string): string {
  const base = prefix.replace(/\/+$/, '')
  const clean = path.replace(/^\/+/, '')
  const escaped = clean.split('/').map(escapeSegment).join('/')
  return `${base}/${escaped}`
}
