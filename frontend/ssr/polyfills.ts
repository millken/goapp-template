/**
 * QuickJS polyfills for APIs missing from the QuickJS runtime.
 *
 * QuickJS has neither browser nor Node.js globals by default.
 * Vue 3 SSR renderer requires: atob/btoa, TextEncoder/TextDecoder.
 * The `entities` package (Vue SSR dep since Vue 3.5.26) uses atob.
 */
export const quickjsPolyfills = `
if (typeof Buffer === 'undefined') {
  var _b64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  var _b64map = {};
  for (var _i = 0; _i < _b64.length; _i++) _b64map[_b64[_i]] = _i;

  function _fromBase64(s) {
    s = s.replace(/=+$/, '').replace(/[^A-Za-z0-9+/]/g, '');
    var bytes = [];
    for (var i = 0; i < s.length; i += 4) {
      var a = _b64map[s[i]] || 0, b = _b64map[s[i+1]] || 0,
          c = _b64map[s[i+2]] || 0, d = _b64map[s[i+3]] || 0;
      bytes.push((a << 2) | (b >> 4));
      if (i+2 < s.length) bytes.push(((b & 0xf) << 4) | (c >> 2));
      if (i+3 < s.length) bytes.push(((c & 0x3) << 6) | d);
    }
    return new Uint8Array(bytes);
  }

  globalThis.Buffer = {
    from: function(data, encoding) {
      if (typeof data === 'string') {
        if (encoding === 'base64') return _fromBase64(data);
        return new TextEncoder().encode(data);
      }
      return data instanceof Uint8Array ? data : new Uint8Array(data);
    },
    isBuffer: function() { return false; },
  };
}
`
