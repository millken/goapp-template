package session

import "strings"

// flashPrefix namespaces one-shot messages inside the session values. Each
// message is stored as its own flat string key (_flash:success → "Post
// created") rather than as a nested map, because the two stores disagree about
// nested types: store_db round-trips values through JSON, so a map[string]string
// would read back as map[string]any, while store_memory keeps it as written. A
// string survives both paths identically.
//
// The `_` prefix marks the key as framework-reserved, as _ViEW_ does in the PJAX
// payload, so it cannot collide with an application key.
const flashPrefix = "_flash:"

// takeFlash removes every staged message from the session and returns them keyed
// by kind, with the prefix stripped. Consumption is package-private: the
// middleware does it, and it must persist the removal (see Middleware).
func (s *session) takeFlash() map[string]string {
	var out map[string]string
	for key, value := range s.values {
		kind, isFlash := strings.CutPrefix(key, flashPrefix)
		if !isFlash {
			continue
		}
		message, isString := value.(string)
		if !isString {
			// Not writable through Flash; drop it rather than surface a type we
			// cannot render.
			delete(s.values, key)
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[kind] = message
		delete(s.values, key)
	}
	return out
}
