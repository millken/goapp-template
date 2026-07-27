# CSRF and Login Throttling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Verify a CSRF token on every unsafe request, and stop `POST /admin/login` answering unlimited guesses.

**Architecture:** The token lives on the session — no cookie, no CSRF risk — and is verified inside the session middleware, which is already mounted once and globally. Delivery rides the props the admin area already injects, so one line in `resolve` covers every authenticated page. The throttle is an IP-keyed sliding window over a small table, with `X-Forwarded-For` ignored unless the peer is a configured proxy.

**Tech Stack:** Go 1.26, `crypto/rand`, `crypto/subtle`, `net/netip`, `github.com/dnsoa/go/sqldb`, Vue 3.

**Spec:** `docs/superpowers/specs/2026-07-27-csrf-and-login-throttling-design.md` — read it before any task.

## Global Constraints

- **The check lives in `session.Service.Middleware()`**, not a second middleware. It already holds the session and is already mounted once; a second mount point is a second thing to get the ordering of wrong.
- **Safe methods pass untouched**: GET, HEAD, OPTIONS. Unsafe: POST, PUT, PATCH, DELETE.
- **Compare with `subtle.ConstantTimeCompare`**, never `==`.
- **Failure is 403 with a plain body**, never a redirect — a redirect re-renders the form and reads as a validation problem, which this is not.
- **No per-route exemptions.** Nothing needs one, and an exemption list written before it has a caller ends up with entries nobody can justify.
- **`.vue` files cannot carry markers.** `markers.forms` has no `.vue` form and a marker in an unsupported type makes `init` fail, so the CSRF field in the scaffold is unconditional. With no session component the prop is absent, the field renders empty, and no middleware exists to check it.
- **`X-Forwarded-For` is ignored unless the peer is inside a configured trusted prefix.** Trusting it by default lets a caller forge the dimension being counted, which is worse than no throttle because it looks like one.
- **The throttle fails open.** A storage error allows the attempt and logs; it is a rate limit, not an authorisation decision, and failing closed turns a database blip into a total lockout.
- Go stdlib plus existing dependencies; no new dependencies, Go or npm.
- Verification: `go build ./...`, `go vet ./...`, `gofmt -l ./` silent, `go test ./... -count=1`, plus `pnpm -C frontend run test`, `run type-check`, `run build`.

## Two things that shape the tasks

1. **Every admin page renders a form.** `AdminShell` carries the logout form, so "pages that need a token" is "all of them". They all pass through `resolve`, which already injects `adminMenu`, `adminUser` and friends — so one line there covers the authenticated area. Only the login page (public, no `resolve`) and the generated public resource need their own.
2. **Turning the check on breaks every existing POST test.** There are ~25 call sites through the `post`, `postForm` and `loginAndGetCookie` helpers. Delivery therefore lands before enforcement, and the enforcement task owns migrating the helpers.

## File Structure

```
internal/service/session/csrf.go          Task 1: token, Regenerate, the check
internal/service/session/store.go         Task 1: two methods on the Session interface
internal/service/session/impl.go          Task 1: their implementations
internal/controller/admin/auth.go         Task 2: resolve injects csrfToken
internal/controller/admin/handlers.go     Task 2: login renders it; Task 4: Regenerate
frontend/src/components/admin/CsrfField.vue   Task 2
frontend/pages/admin/**, templates/admin/**   Task 2: every form carries the field
frontend/scripts/forms.test.ts            Task 2: the template audit
internal/service/session/session.go       Task 3: the check runs in Middleware
internal/controller/admin/clientip.go     Task 5
internal/service/db/migrations/005_login_attempts.{up,down}.sql   Task 6
internal/controller/admin/throttle.go     Task 6
README.md                                 Task 7
```

---

### Task 1: The token, and the ability to regenerate a session

Session primitives only. Nothing calls them yet, which is deliberate: the check
and the delivery both depend on these, and a task that adds all three at once
cannot be reviewed in pieces.

**Files:**
- Create: `internal/service/session/csrf.go`, `internal/service/session/csrf_test.go`
- Modify: `internal/service/session/store.go` (the `Session` interface), `internal/service/session/impl.go`

**Interfaces:**
- Consumes: `session` struct (`id`, `values`, `mod`, `w`), `Save`, `Destroy`, `store.Delete`.
- Produces:
  ```go
  // On the Session interface:
  CSRFToken(ctx context.Context) (string, error)
  Regenerate(ctx context.Context) error

  // Package-level, used by the middleware in Task 3:
  const csrfKey = "_csrf"
  const CSRFFormField = "_csrf"
  const CSRFHeader = "X-CSRF-Token"
  func (s *session) validCSRF(r *http.Request) bool
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/service/session/csrf_test.go`:

```go
package session

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newCSRFSession returns a session backed by the memory store, with a recorder
// standing in for the response writer the middleware normally injects.
func newCSRFSession(t *testing.T) (*session, *Service) {
	t.Helper()
	svc := New(&Config{Secret: "test-secret", Store: StoreMemory}, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return &session{values: map[string]any{}, mod: svc, w: httptest.NewRecorder()}, svc
}

func TestCSRFToken_StableWithinASession(t *testing.T) {
	s, _ := newCSRFSession(t)
	ctx := context.Background()

	first, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if len(first) < 32 {
		t.Errorf("token is %d chars, want something unguessable", len(first))
	}
	second, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if second != first {
		t.Error("a second call minted a new token; a form rendered earlier would stop working")
	}
}

// The first call is what brings a session into existence for an anonymous
// visitor — that is the whole reason the token is created on demand rather than
// for everyone.
func TestCSRFToken_PersistsTheSession(t *testing.T) {
	s, _ := newCSRFSession(t)
	if s.ID() != "" {
		t.Fatal("fixture should start unsaved")
	}
	if _, err := s.CSRFToken(context.Background()); err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if s.ID() == "" {
		t.Error("the token was not persisted, so it will not survive to the next request")
	}
}

func TestValidCSRF(t *testing.T) {
	s, _ := newCSRFSession(t)
	token, err := s.CSRFToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	form := func(v string) *http.Request {
		r := httptest.NewRequest("POST", "/x", strings.NewReader(url.Values{CSRFFormField: {v}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	if !s.validCSRF(form(token)) {
		t.Error("the session's own token was rejected")
	}
	if s.validCSRF(form("")) {
		t.Error("an empty token was accepted")
	}
	if s.validCSRF(form(token + "x")) {
		t.Error("a wrong token was accepted")
	}

	// The header form, for a future JSON client.
	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set(CSRFHeader, token)
	if !s.validCSRF(r) {
		t.Error("the header form was rejected")
	}

	// A session that never minted a token has nothing to match, and must not
	// accept an empty submission by matching empty against empty.
	fresh, _ := newCSRFSession(t)
	if fresh.validCSRF(form("")) {
		t.Error("a session with no token accepted an empty one")
	}
}

// Without this, issuing a token on the login page would leave the pre-login id
// in place after sign-in — session fixation, introduced by the CSRF feature
// rather than fixed by it.
func TestRegenerate_NewIDSameValuesOldEntryGone(t *testing.T) {
	s, svc := newCSRFSession(t)
	ctx := context.Background()
	s.Set("keep", "me")
	old, err := s.Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Regenerate(ctx); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if s.ID() == old || s.ID() == "" {
		t.Errorf("id = %q, want a new non-empty id (was %q)", s.ID(), old)
	}
	if v, _ := s.Get("keep"); v != "me" {
		t.Error("values did not survive regeneration")
	}
	if _, _, ok, err := svc.store.Load(ctx, old); err != nil || ok {
		t.Error("the old entry is still in the store; the abandoned cookie still works")
	}
}

// A token tied to the abandoned id is worthless, so it goes with it.
func TestRegenerate_RotatesTheToken(t *testing.T) {
	s, _ := newCSRFSession(t)
	ctx := context.Background()
	before, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Regenerate(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Error("the token survived regeneration")
	}
}
```

Add `"net/http"` to the imports.

- [ ] **Step 2: Run it — must fail**

Run: `go test ./internal/service/session/ -run 'TestCSRF|TestValidCSRF|TestRegenerate' -count=1 2>&1 | head`
Expected: compile failure — `CSRFToken`, `validCSRF`, `Regenerate`, `CSRFFormField` undefined.

- [ ] **Step 3: Implement `csrf.go`**

```go
package session

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
)

// csrfKey namespaces the token inside the session values, alongside the flash
// keys. The `_` prefix marks it framework-reserved, as flashPrefix does, so it
// cannot collide with an application key.
const csrfKey = "_csrf"

// CSRFFormField and CSRFHeader are where a request may carry the token. The
// field is the one that matters: the PJAX layer submits a form as FormData, so
// a hidden input reaches the server unchanged whether or not JavaScript
// intercepted the submit. The header exists for a future JSON client.
const (
	CSRFFormField = "_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

// CSRFToken returns this session's token, minting and persisting one on first
// call. Persisting is the point: the token has to survive to the request that
// submits the form, and saving is also what gives an anonymous visitor a
// session — which is why this is called by handlers that render a form rather
// than by the middleware for everyone.
func (s *session) CSRFToken(ctx context.Context) (string, error) {
	if v, ok := s.values[csrfKey]; ok {
		if token, ok := v.(string); ok && token != "" {
			return token, nil
		}
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("session: generate csrf token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	s.Set(csrfKey, token)
	if _, err := s.Save(ctx); err != nil {
		return "", fmt.Errorf("session: persist csrf token: %w", err)
	}
	return token, nil
}

// validCSRF reports whether r carries this session's token.
//
// A session that has never minted one rejects everything, including an empty
// submission — matching empty against empty would turn "no token anywhere" into
// a pass, which is exactly the request this exists to refuse.
func (s *session) validCSRF(r *http.Request) bool {
	want, _ := s.values[csrfKey].(string)
	if want == "" {
		return false
	}

	got := r.Header.Get(CSRFHeader)
	if got == "" {
		// ParseForm on a POST reads the body, which the handler then re-reads
		// from the parsed form rather than the stream — the same thing every
		// c.PostForm call already relies on.
		if err := r.ParseForm(); err == nil {
			got = r.PostFormValue(CSRFFormField)
		}
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// Regenerate moves the session to a fresh id, keeping its values and deleting
// the old entry.
//
// Called on sign-in. Without it this package's own CSRF token would introduce
// session fixation: issuing a token on the login page creates a session before
// authentication, and Store.Save keeps an id it is given, so the id an attacker
// could plant would still be valid afterwards.
func (s *session) Regenerate(ctx context.Context) error {
	old := s.id
	// The token belongs to the abandoned id; a form rendered against it is
	// worthless now, so mint a new one on next use rather than carry it over.
	delete(s.values, csrfKey)

	s.id = ""
	if _, err := s.Save(ctx); err != nil {
		s.id = old
		return fmt.Errorf("session: regenerate: %w", err)
	}
	if old != "" {
		if err := s.mod.store.Delete(ctx, old); err != nil {
			// The new session is live and the cookie points at it; the stale
			// entry will expire on its own. Worth knowing about, not worth
			// failing a sign-in over.
			return fmt.Errorf("session: regenerate: drop old entry: %w", err)
		}
	}
	return nil
}
```

Add both methods to the `Session` interface in `store.go`:

```go
	// CSRFToken returns the session's token, minting and persisting one on
	// first call. Handlers that render a form call this; the middleware
	// verifies it on unsafe requests.
	CSRFToken(ctx context.Context) (string, error)
	// Regenerate moves the session to a fresh id, keeping its values. Call it
	// on sign-in: without it, a session established before authentication
	// stays valid after it.
	Regenerate(ctx context.Context) error
```

- [ ] **Step 4: Run the package**

Run: `go test ./internal/service/session/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l ./ && go vet ./... && go test ./... -count=1
git add internal/service/session/
git commit -m "feat(session): a CSRF token, and the means to regenerate a session"
```

---

### Task 2: Deliver the token to every form

Delivery before enforcement, so the suite stays green while the field spreads.
Nothing verifies the token yet.

**Files:**
- Modify: `internal/controller/admin/auth.go` (`resolve`), `internal/controller/admin/handlers.go` (`LoginForm` and the two re-render paths in `LoginSubmit`)
- Create: `frontend/src/components/admin/CsrfField.vue`, `frontend/scripts/forms.test.ts`
- Modify: every page with a form — `frontend/pages/admin/login.vue`, `frontend/pages/admin/user/index.vue`, `frontend/pages/admin/user/form.vue`, `frontend/pages/admin/group/index.vue`, `frontend/pages/admin/group/form.vue`, `frontend/pages/admin/account/password.vue`, `frontend/src/components/admin/AdminShell.vue`, `frontend/src/components/admin/ConfirmDialog.vue`, and the scaffold templates `cmd/goappctl/internal/scaffold/templates/admin/{index,form}.vue.tmpl`
- Modify: `frontend/pages/admin/ssrfixture/index.vue` (regenerated)

**Interfaces:**
- Consumes: `Session.CSRFToken(ctx)` from Task 1.
- Produces: a `csrfToken` prop on every admin page; `<CsrfField :token="csrfToken" />` renders `<input type="hidden" name="_csrf">`.

- [ ] **Step 1: Write the failing audit**

Create `frontend/scripts/forms.test.ts`:

```typescript
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
```

- [ ] **Step 2: Run it — must fail, listing every form**

Run: `cd frontend && npx vitest run scripts/forms.test.ts`
Expected: FAIL listing 9 forms.

- [ ] **Step 3: Create the field component**

`frontend/src/components/admin/CsrfField.vue`:

```vue
<script setup lang="ts">
// The hidden input every posting form needs, in one place so the field name
// lives in one place too. It must match session.CSRFFormField on the server.
//
// The PJAX layer submits a form as FormData, so this input reaches the server
// unchanged whether or not JavaScript intercepted the submit — which is why the
// token travels as a field rather than a header.
//
// An absent token renders an empty value rather than nothing: in a build with
// the session component trimmed there is no token and no middleware to check
// one, and a .vue file cannot carry a goappctl marker to make the field
// conditional.
defineProps<{ token?: string }>()
</script>

<template>
  <input type="hidden" name="_csrf" :value="token ?? ''">
</template>
```

- [ ] **Step 4: Inject the prop server-side**

In `internal/controller/admin/auth.go`, inside `resolve`, beside the other
`c.Set` calls:

```go
	// Every admin page renders at least the shell's logout form, so every admin
	// page needs a token. resolve is the one place they all pass through.
	token, err := a.Session.Session(c).CSRFToken(c.Request.Context())
	if err != nil {
		slog.Error("admin auth: csrf token", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}
	c.Set("csrfToken", token)
```

In `internal/controller/admin/handlers.go`, `LoginForm` is public and does not
pass through `resolve`, so it sets its own. Add before its `c.Render`:

```go
	token, err := sess.CSRFToken(c.Request.Context())
	if err != nil {
		slog.Error("admin login: csrf token", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Set("csrfToken", token)
```

`LoginSubmit` re-renders the login page on a failed attempt in two places (the
disabled-account branch and the invalid-credentials branch). Both need the same
three lines before their `c.Render("admin/login")`. Read the file and add them;
the session variable is already in scope in that function.

- [ ] **Step 5: Add the field to every form**

Each page imports the component and drops it inside every `<form method="post">`.
The prop comes from `csrfToken`, which every page already receives now.

`AdminShell.vue` — add `csrfToken?: string` to its props, import
`CsrfField from '@/components/admin/CsrfField.vue'`, and put
`<CsrfField :token="csrfToken" />` inside the logout form. Every page that
renders the shell must pass `:csrf-token="csrfToken"` down to it.

`ConfirmDialog.vue` — add `csrfToken?: string` to its props and
`<CsrfField :token="csrfToken" />` inside its form; every call site passes
`:csrf-token="csrfToken"`.

The remaining forms — `login.vue`, `user/form.vue`, `group/form.vue`,
`account/password.vue`, and the per-row status forms in `user/index.vue` — each
get the import and one `<CsrfField :token="csrfToken" />` inside the form. Pages
that do not already declare `csrfToken` in `defineProps` gain it as
`csrfToken?: string`.

The scaffold templates `index.vue.tmpl` and `form.vue.tmpl` get the same
treatment, including `csrfToken?: string` in their props block.

- [ ] **Step 6: Regenerate the fixture and verify**

```bash
head -20 frontend/pages/admin/ssrfixture/index.vue > /tmp/fh.txt
go run ./cmd/goappctl gen admin ssrfixture --force >/dev/null
rm -rf internal/controller/adminssrfixture frontend/pages/admin/ssrfixture/form.vue
git checkout internal/controller/mount_gen.go
cat /tmp/fh.txt frontend/pages/admin/ssrfixture/index.vue > /tmp/f.vue && mv /tmp/f.vue frontend/pages/admin/ssrfixture/index.vue

cd frontend && npx vitest run scripts/forms.test.ts && cd ..
go test ./... -count=1
pnpm -C frontend run type-check && pnpm -C frontend run test && pnpm -C frontend run build
go test ./server/ -count=1
```

Expected: the audit passes, and everything else stays green — nothing verifies
the token yet, so no existing test changes behaviour.

- [ ] **Step 7: Commit**

```bash
gofmt -l ./ && go vet ./...
git add -A
git commit -m "feat(admin): deliver a CSRF token to every posting form"
```

---

### Task 3: Enforce it

The check, and the test-suite migration it forces.

**Files:**
- Modify: `internal/service/session/session.go` (`Middleware`)
- Modify: `internal/controller/admin/login_test.go` (the shared helpers), and any test the change breaks
- Test: `internal/service/session/csrf_middleware_test.go` (create)

**Interfaces:**
- Consumes: `validCSRF`, `CSRFFormField` from Task 1.
- Produces: unsafe requests without a valid token get 403. Test helpers gain a
  way to carry a token — see Step 3.

- [ ] **Step 1: Write the failing test**

Create `internal/service/session/csrf_middleware_test.go`:

```go
package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/millken/inertia"
)

// mint drives a GET through the middleware, has the handler ask for a token,
// and returns the token with the cookie it was stored under.
func mint(t *testing.T, eng *inertia.Engine, svc *Service) (string, *http.Cookie) {
	t.Helper()
	var token string
	eng.GET("/mint", func(c *inertia.Context) {
		v, err := svc.Session(c).CSRFToken(c.Request.Context())
		if err != nil {
			t.Fatalf("CSRFToken: %v", err)
		}
		token = v
	})
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mint", nil))
	cs := w.Result().Cookies()
	if len(cs) == 0 {
		t.Fatal("minting a token did not set a cookie")
	}
	return token, &http.Cookie{Name: cs[0].Name, Value: cs[0].Value}
}

// Built from the helpers this package already has — newTestEngine in
// session_test.go and installedWith in flash_test.go. Do not redeclare either;
// a duplicate is a compile error.
func csrfStack(t *testing.T) (*inertia.Engine, *Service) {
	t.Helper()
	eng := newTestEngine(t)
	svc := installedWith(t, eng, &Config{Secret: "test-secret", Store: StoreMemory}, nil)
	return eng, svc
}

func TestCSRFMiddleware(t *testing.T) {
	eng, svc := csrfStack(t)
	ran := false
	eng.POST("/act", func(c *inertia.Context) { ran = true })
	eng.GET("/read", func(c *inertia.Context) { ran = true })
	token, cookie := mint(t, eng, svc)

	post := func(body url.Values, ck *http.Cookie) *httptest.ResponseRecorder {
		ran = false
		r := httptest.NewRequest(http.MethodPost, "/act", strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if ck != nil {
			r.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, r)
		return w
	}

	if w := post(url.Values{CSRFFormField: {token}}, cookie); w.Code != http.StatusOK || !ran {
		t.Errorf("a valid token was refused: %d", w.Code)
	}
	if w := post(url.Values{}, cookie); w.Code != http.StatusForbidden || ran {
		t.Errorf("a missing token was accepted: %d", w.Code)
	}
	if w := post(url.Values{CSRFFormField: {token + "x"}}, cookie); w.Code != http.StatusForbidden || ran {
		t.Errorf("a wrong token was accepted: %d", w.Code)
	}
	if w := post(url.Values{CSRFFormField: {token}}, nil); w.Code != http.StatusForbidden || ran {
		t.Errorf("a token without the matching session was accepted: %d", w.Code)
	}

	// Safe methods are untouched, and a GET that asks for nothing sets no cookie.
	ran = false
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/read", nil))
	if !ran {
		t.Error("a GET was refused")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("a GET that needs no token still set a cookie")
	}
}

// The PJAX layer submits a form as FormData through fetch. It is a different
// code path from a browser form post, and the reason the token travels as a
// field rather than a header.
func TestCSRFMiddleware_PJAXFormData(t *testing.T) {
	eng, svc := csrfStack(t)
	eng.POST("/act", func(c *inertia.Context) {})
	token, cookie := mint(t, eng, svc)

	var body strings.Builder
	const boundary = "TESTBOUNDARY"
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString(`Content-Disposition: form-data; name="` + CSRFFormField + `"` + "\r\n\r\n")
	body.WriteString(token + "\r\n")
	body.WriteString("--" + boundary + "--\r\n")

	r := httptest.NewRequest(http.MethodPost, "/act", strings.NewReader(body.String()))
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r.Header.Set("X-Pjax", "true")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: a multipart PJAX submit carries the field", w.Code)
	}
}
```

- [ ] **Step 2: Run it — must fail**

Run: `go test ./internal/service/session/ -run TestCSRFMiddleware -count=1`
Expected: FAIL — every POST is accepted, so the "missing token" case gets 200.

- [ ] **Step 3: Implement the check**

In `internal/service/session/session.go`, inside `Middleware()`, after the
session is attached to the request context and before the flash block:

```go
		// Unsafe methods carry a token or they do not run. The check is here
		// rather than in its own middleware because this one already holds the
		// session and is already mounted once, globally.
		//
		// 403 with a plain body, not a redirect: a redirect re-renders the form
		// and reads as a validation problem, and this is not one.
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sess.validCSRF(c.Request) {
				slog.Warn("session: csrf check failed",
					"method", c.Request.Method, "path", c.Request.URL.Path)
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		}
```

`multipart/form-data` bodies need `ParseMultipartForm`, which `PostFormValue`
calls internally — no extra work, but confirm the PJAX test passes rather than
assuming it.

- [ ] **Step 4: Migrate the test helpers**

Every existing POST test now needs a token. Rather than change 25 call sites,
change the three helpers they share, in `internal/controller/admin/login_test.go`:

```go
// csrfFor drives a GET through the stack and returns the token the page was
// given, plus the cookie it belongs to. Tests that post go through here, the
// same way a browser gets a token by loading the form first.
func csrfFor(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	body := w.Body.String()
	const marker = `name="_csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		// Fall back to the page data, which carries the prop even when the
		// markup is not rendered in this mode.
		const prop = `\"csrfToken\":\"`
		if j := strings.Index(body, prop); j >= 0 {
			rest := body[j+len(prop):]
			return rest[:strings.Index(rest, `\"`)]
		}
		t.Fatalf("no csrf token on %s (status %d)", path, w.Code)
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}
```

Then `postForm`, `post` and `loginAndGetCookie` add the token to the values they
send. `loginAndGetCookie` fetches one from `/admin/login` first; `post` takes the
cookie it is already given and fetches one from the admin mount.

Work through the failures the suite reports rather than guessing which tests
need what — `go test ./internal/controller/admin/ -count=1` names each one.

- [ ] **Step 5: Everything green**

Run: `go test ./... -count=1`
Expected: PASS. If a test still fails with 403, it posts without going through a
migrated helper; give it a token the same way.

- [ ] **Step 6: Commit**

```bash
gofmt -l ./ && go vet ./...
git add -A
git commit -m "feat(session): verify a CSRF token on every unsafe request"
```

---

### Task 4: Regenerate the session on sign-in

**Files:**
- Modify: `internal/controller/admin/handlers.go` (`LoginSubmit`)
- Test: `internal/controller/admin/login_test.go`

**Interfaces:**
- Consumes: `Session.Regenerate(ctx)` from Task 1.

- [ ] **Step 1: Write the failing test** (append to `login_test.go`)

```go
// Issuing a CSRF token on the login page creates a session before
// authentication, and Store.Save keeps an id it is given — so without
// regenerating, the id an attacker could plant before sign-in stays valid after
// it. This is the test that would have caught the hole the CSRF work opened.
func TestLogin_RegeneratesTheSessionID(t *testing.T) {
	eng, _ := loginStack(t)

	// Load the login page the way a browser would: this mints a token and
	// establishes a session id.
	w1 := httptest.NewRecorder()
	eng.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/admin/login", nil))
	pre := w1.Result().Cookies()
	if len(pre) == 0 {
		t.Fatal("the login page set no cookie, so there is no id to fixate")
	}
	before := pre[0].Value
	token := csrfFor(t, eng, &http.Cookie{Name: pre[0].Name, Value: before}, "/admin/login")

	r := postForm("/admin/login", url.Values{
		"username": {"alice"}, "password": {"pw"}, "_csrf": {token},
	})
	r.AddCookie(&http.Cookie{Name: pre[0].Name, Value: before})
	w2 := httptest.NewRecorder()
	eng.ServeHTTP(w2, r)

	after := w2.Result().Cookies()
	if len(after) == 0 {
		t.Fatal("sign-in set no cookie")
	}
	if after[0].Value == before {
		t.Error("the session id survived sign-in — an id planted beforehand still works")
	}
}
```

- [ ] **Step 2: Run it — must fail**

Run: `go test ./internal/controller/admin/ -run TestLogin_RegeneratesTheSessionID -count=1`
Expected: FAIL — "the session id survived sign-in".

- [ ] **Step 3: Implement.** In `LoginSubmit`, between the successful
`authenticate` and `sess.Set(a.authKey(), user.ID)`:

```go
	// A new id for the authenticated session. The login page minted a CSRF
	// token, which means a session already existed, and Store.Save keeps the id
	// it is given — so without this an id planted before sign-in would still be
	// valid after it.
	if err := sess.Regenerate(c.Request.Context()); err != nil {
		slog.Error("admin login: regenerate session", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
```

- [ ] **Step 4: Run and commit**

```bash
go test ./... -count=1
gofmt -l ./ && go vet ./...
git add internal/controller/admin/
git commit -m "fix(admin): a new session id on sign-in, so a planted one dies there"
```

---

### Task 5: The client's address

**Files:**
- Create: `internal/controller/admin/clientip.go`, `internal/controller/admin/clientip_test.go`
- Modify: `internal/controller/admin/admin.go` (`Config`, `Validate`)

**Interfaces:**
- Produces:
  ```go
  // Config gains:
  TrustedProxies []string `yaml:"trusted_proxies"`
  // On *Admin, resolved once by Validate:
  func (a *Admin) clientIP(r *http.Request) string
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/clientip_test.go`:

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	for _, c := range []struct {
		name    string
		trusted []string
		remote  string
		xff     string
		want    string
	}{
		{
			// The default. Anyone can send this header, so believing it would
			// let a caller pick the bucket they are counted in — a throttle
			// that can be bypassed by setting a header is worse than none,
			// because it looks like protection.
			name: "no trust configured ignores the header",
			remote: "203.0.113.7:1234", xff: "1.2.3.4", want: "203.0.113.7",
		},
		{
			name: "trusted peer yields the client",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.0.0.1:1234", xff: "203.0.113.7", want: "203.0.113.7",
		},
		{
			// Everything to the right was added by infrastructure we trust; the
			// first untrusted entry walking left is the furthest we can believe.
			name: "walks left past trusted hops",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.0.0.1:1234", xff: "203.0.113.7, 10.0.0.9, 10.0.0.8", want: "203.0.113.7",
		},
		{
			name: "untrusted peer ignores the header even when one is configured",
			trusted: []string{"10.0.0.0/8"},
			remote:  "198.51.100.5:9999", xff: "1.2.3.4", want: "198.51.100.5",
		},
		{
			name: "garbage in the header falls back to the peer",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.0.0.1:1234", xff: "not-an-ip", want: "10.0.0.1",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := New(nil, &Config{Mount: "/admin", TrustedProxies: c.trusted})
			if err := a.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remote
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := a.clientIP(r); got != c.want {
				t.Errorf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

// A typo that silently disabled the trust list would silently disable the
// throttle, so it stops startup instead.
func TestValidate_RejectsABadPrefix(t *testing.T) {
	a := New(nil, &Config{Mount: "/admin", TrustedProxies: []string{"10.0.0.0/8", "nonsense"}})
	if err := a.Validate(); err == nil {
		t.Error("an unparseable trusted_proxies entry must fail startup")
	}
}
```

- [ ] **Step 2: Run it — must fail** (`TrustedProxies` and `clientIP` undefined)

- [ ] **Step 3: Implement `clientip.go`**

```go
package admin

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP is the address the throttle counts against.
//
// X-Forwarded-For is only read when the peer is one of the configured proxies:
// the header is caller-supplied, so believing it unconditionally would let
// anyone choose which bucket they land in. With no proxies configured — the
// default — the header is never read at all.
func (a *Admin) clientIP(r *http.Request) string {
	peer := hostOnly(r.RemoteAddr)
	if len(a.trusted) == 0 || !a.isTrusted(peer) {
		return peer
	}

	// Everything to the right was appended by infrastructure we trust. Walking
	// left, the first entry that is not itself trusted is the furthest back we
	// can believe; anything beyond it was supplied by the client.
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		addr, err := netip.ParseAddr(candidate)
		if err != nil {
			// A malformed hop means the chain cannot be trusted past this
			// point; the peer is the safe answer.
			return peer
		}
		if !a.isTrusted(addr.String()) {
			return addr.String()
		}
	}
	return peer
}

func (a *Admin) isTrusted(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, p := range a.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// hostOnly strips the port RemoteAddr carries, tolerating an address that has
// none.
func hostOnly(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}
```

In `admin.go`, add the field to `Config`:

```go
	// TrustedProxies are CIDR blocks whose X-Forwarded-For header is believed,
	// for the login throttle's client address. Empty (the default) means the
	// header is never read.
	TrustedProxies []string `yaml:"trusted_proxies"`
```

add `trusted []netip.Prefix` to the `Admin` struct beside `perms`, and parse it
in `Validate`:

```go
	a.trusted = a.trusted[:0]
	for _, raw := range a.cfg.TrustedProxies {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("admin: trusted_proxies %q: %w", raw, err)
		}
		a.trusted = append(a.trusted, p)
	}
```

`New(nil, cfg)` must tolerate a nil `cfg` as it already does; check how the
existing defaults are applied and follow that shape.

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/controller/admin/ -run 'TestClientIP|TestValidate_RejectsABadPrefix' -count=1 -v
go test ./... -count=1
gofmt -l ./ && go vet ./...
git add internal/controller/admin/
git commit -m "feat(admin): resolve the client address, trusting no proxy by default"
```

---

### Task 6: The throttle

**Files:**
- Create: `internal/service/db/migrations/005_login_attempts.up.sql`, `005_login_attempts.down.sql`, `internal/controller/admin/throttle.go`, `internal/controller/admin/throttle_test.go`
- Modify: `internal/controller/admin/handlers.go` (`LoginSubmit`)

**Interfaces:**
- Consumes: `clientIP` from Task 5.
- Produces:
  ```go
  const loginWindow = 15 * time.Minute
  const loginMaxAttempts = 10
  func (a *Admin) loginBlocked(ctx context.Context, ip string) (blocked bool, retryAfter time.Duration)
  func (a *Admin) recordLoginFailure(ctx context.Context, ip string)
  func (a *Admin) clearLoginFailures(ctx context.Context, ip string)
  ```

- [ ] **Step 1: Write the migration pair**

`005_login_attempts.up.sql`:

```sql
-- 005_login_attempts.up.sql
-- Failed sign-in attempts, counted per client address over a sliding window.
-- There is no counter column on purpose: a window is a count of rows newer than
-- a cutoff, which needs no reset and cannot drift.
--
-- Rows are deleted as they age out, on write, so the table stays proportional to
-- recent activity rather than to history and needs no scheduled job.
--
-- SQLite flavor (the template's default driver). PostgreSQL and MySQL accept
-- this as written; `at` is UnixNano (BIGINT), matching the other tables.
CREATE TABLE IF NOT EXISTS login_attempts (
    ip TEXT NOT NULL,
    at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS login_attempts_ip_at ON login_attempts (ip, at);
```

`005_login_attempts.down.sql`:

```sql
-- 005_login_attempts.down.sql
-- Drops the window along with the table. Anyone currently throttled is released,
-- which is the correct outcome: the data is a rate limit, not a record.
DROP INDEX IF EXISTS login_attempts_ip_at;
DROP TABLE IF EXISTS login_attempts;
```

- [ ] **Step 2: Write the failing test**

Create `internal/controller/admin/throttle_test.go`:

```go
package admin

import (
	"context"
	"testing"
	"time"
)

func TestLoginThrottle_BoundaryAndClear(t *testing.T) {
	_, adm := loginStack(t)
	ctx := context.Background()
	const ip = "203.0.113.7"

	for i := range loginMaxAttempts - 1 {
		adm.recordLoginFailure(ctx, ip)
		if blocked, _ := adm.loginBlocked(ctx, ip); blocked {
			t.Fatalf("blocked after %d failures, the limit is %d", i+1, loginMaxAttempts)
		}
	}
	adm.recordLoginFailure(ctx, ip)
	blocked, retry := adm.loginBlocked(ctx, ip)
	if !blocked {
		t.Errorf("not blocked after %d failures", loginMaxAttempts)
	}
	if retry <= 0 || retry > loginWindow {
		t.Errorf("retryAfter = %v, want something inside the window", retry)
	}

	// Another address is unaffected — the whole point of counting per address.
	if blocked, _ := adm.loginBlocked(ctx, "198.51.100.1"); blocked {
		t.Error("a different address was blocked")
	}

	adm.clearLoginFailures(ctx, ip)
	if blocked, _ := adm.loginBlocked(ctx, ip); blocked {
		t.Error("a successful sign-in did not clear the count")
	}
}

// The window slides: attempts older than it do not count, which is what makes
// this a rate limit rather than a permanent ban.
func TestLoginThrottle_OldAttemptsAgeOut(t *testing.T) {
	_, adm := loginStack(t)
	ctx := context.Background()
	const ip = "203.0.113.8"

	stale := time.Now().Add(-2 * loginWindow).UnixNano()
	for range loginMaxAttempts + 5 {
		if _, err := adm.DB.ExecContext(ctx,
			`INSERT INTO login_attempts (ip, at) VALUES (?, ?)`, ip, stale); err != nil {
			t.Fatal(err)
		}
	}
	if blocked, _ := adm.loginBlocked(ctx, ip); blocked {
		t.Error("attempts older than the window still count")
	}
}

// A rate limit is not an authorisation decision. If the count cannot be read,
// letting the attempt through costs one unthrottled try; refusing it locks
// everyone out of the admin area over a database blip.
func TestLoginThrottle_FailsOpen(t *testing.T) {
	_, adm := loginStack(t)
	if err := adm.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := adm.loginBlocked(context.Background(), "203.0.113.9"); blocked {
		t.Error("a storage failure blocked the attempt; the throttle must fail open")
	}
}
```

- [ ] **Step 3: Run it — must fail** (the helpers do not exist)

- [ ] **Step 4: Implement `throttle.go`**

```go
package admin

import (
	"context"
	"log/slog"
	"time"
)

// The window and its limit. A count over a window rather than a counter: there
// is nothing to reset, and nothing drifts.
const (
	loginWindow      = 15 * time.Minute
	loginMaxAttempts = 10
)

// loginBlocked reports whether ip has spent its attempts, and how long until the
// oldest one ages out.
//
// Fails open. A rate limit is not an authorisation decision, and refusing on a
// storage error would turn a database blip into a total lockout.
func (a *Admin) loginBlocked(ctx context.Context, ip string) (bool, time.Duration) {
	cutoff := time.Now().Add(-loginWindow).UnixNano()
	var n int
	var oldest *int64
	if err := a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(at) FROM login_attempts WHERE ip = ? AND at > ?`,
		ip, cutoff).Scan(&n, &oldest); err != nil {
		slog.Error("admin: count login attempts", "err", err, "ip", ip)
		return false, 0
	}
	if n < loginMaxAttempts || oldest == nil {
		return false, 0
	}
	retry := time.Until(time.Unix(0, *oldest).Add(loginWindow))
	if retry < time.Second {
		retry = time.Second
	}
	return true, retry
}

// recordLoginFailure adds one attempt and drops the ones that have aged out, so
// the table tracks recent activity rather than history.
func (a *Admin) recordLoginFailure(ctx context.Context, ip string) {
	now := time.Now()
	if _, err := a.DB.ExecContext(ctx,
		`INSERT INTO login_attempts (ip, at) VALUES (?, ?)`, ip, now.UnixNano()); err != nil {
		slog.Error("admin: record login failure", "err", err, "ip", ip)
		return
	}
	if _, err := a.DB.ExecContext(ctx,
		`DELETE FROM login_attempts WHERE at <= ?`, now.Add(-loginWindow).UnixNano()); err != nil {
		slog.Warn("admin: prune login attempts", "err", err)
	}
}

// clearLoginFailures forgets an address, called on a successful sign-in.
func (a *Admin) clearLoginFailures(ctx context.Context, ip string) {
	if _, err := a.DB.ExecContext(ctx, `DELETE FROM login_attempts WHERE ip = ?`, ip); err != nil {
		slog.Warn("admin: clear login attempts", "err", err, "ip", ip)
	}
}
```

- [ ] **Step 5: Wire it into `LoginSubmit`**

At the top of the handler, before reading the form:

```go
	ip := a.clientIP(c.Request)
	if blocked, retry := a.loginBlocked(c.Request.Context(), ip); blocked {
		token, err := sess.CSRFToken(c.Request.Context())
		if err != nil {
			slog.Error("admin login: csrf token", "err", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Set("csrfToken", token)
		c.Set("loginPath", a.LoginPath())
		c.Set("error", fmt.Sprintf("尝试次数过多，请在 %d 分钟后重试。",
			int(retry.Minutes())+1))
		c.Status(http.StatusTooManyRequests)
		if rerr := c.Render("admin/login"); rerr != nil {
			slog.Error("render admin login", "err", rerr)
		}
		return
	}
```

On a failed attempt — the `errInvalidCredentials` branch, **not** the
`errAccountDisabled` branch — add `a.recordLoginFailure(c.Request.Context(), ip)`.
A correct password against a disabled account is not a guess, and counting it
would let a disabled user lock their own address out while trying to work out
why they cannot get in.

After a successful sign-in, `a.clearLoginFailures(c.Request.Context(), ip)`.

Read the handler and place these where the branches actually are; do not assume
the shape from this description.

- [ ] **Step 6: Test the disabled-account exemption** (append to `throttle_test.go`)

```go
// A correct password against a disabled account is not a guess. Counting it
// would let a disabled user lock their own address out while trying to
// understand why they cannot get in.
func TestLoginThrottle_DisabledAccountDoesNotCount(t *testing.T) {
	eng, adm := loginStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	for range loginMaxAttempts + 2 {
		token := csrfFor(t, eng, nil, "/admin/login")
		r := postForm("/admin/login", url.Values{
			"username": {"alice"}, "password": {"pw"}, "_csrf": {token},
		})
		eng.ServeHTTP(httptest.NewRecorder(), r)
	}

	var n int
	if err := adm.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_attempts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("recorded %d attempts for a disabled account with the right password", n)
	}
}
```

Add the imports it needs (`net/http/httptest`, `net/url`).

Note: `csrfFor` needs the cookie the login page set to be carried into the POST
for the token to match. Follow the shape `TestLogin_RegeneratesTheSessionID` uses
in Task 4 — fetch the page, keep its cookie, post with both.

- [ ] **Step 7: Run everything and commit**

```bash
go test ./... -count=1
gofmt -l ./ && go vet ./...
git add internal/service/db/ internal/controller/admin/
git commit -m "feat(admin): an IP-keyed sliding window on failed sign-ins"
```

---

### Task 7: Document both

**Files:**
- Modify: `README.md`, `config.example.yaml`

- [ ] **Step 1: Add the config key.** In `config.example.yaml`, under the admin
section, inside its marker block:

```yaml
  # CIDRs whose X-Forwarded-For is believed when resolving the client address
  # for the login throttle. Empty (the default) means the header is never read —
  # anyone can send it, so trusting it without knowing the peer is a proxy lets a
  # caller choose which bucket they are counted in.
  trusted_proxies: []
```

- [ ] **Step 2: Document both in the README**, inside the `goappctl:admin` block
for the throttle and the `goappctl:session` block for CSRF — verify the block
boundaries with `grep -n "goappctl:" README.md` before inserting, and check they
still balance afterwards.

For CSRF, cover: it applies to every unsafe method, not just the admin area; the
token is a hidden field so PJAX and plain posts use one mechanism; a handler that
renders a form calls `CSRFToken`; the field component; and that sign-in
regenerates the session id, with the reason.

For the throttle: the window and limit, that it counts per address and why not
per username, that `X-Forwarded-For` is ignored unless `trusted_proxies` says
otherwise, and that it fails open.

- [ ] **Step 3: Verify and commit**

```bash
go test ./... -count=1
grep -c "goappctl:admin\|goappctl:session" README.md
grep -c "goappctl:end" README.md
go run ./cmd/goappctl init --dry-run --module example.com/x --with db,ssr 2>&1 | grep -Ei "session|error" | head -3
git add README.md config.example.yaml
git commit -m "docs: CSRF and the login throttle"
```

---

## Self-Review Notes

- **Spec coverage:** §3.1 token → Task 1; §3.2 regeneration → Tasks 1 and 4; §3.3 on-demand and its failure modes → Task 2 (delivery) plus the audit; §3.4 delivery and the `.vue` marker constraint → Task 2; §3.5 verification → Task 3; §4.1 client IP → Task 5; §4.2 storage and §4.3 the rule → Task 6; §5 testing → each task's tests; §6 exclusions respected.
- **Type consistency:** `CSRFToken`/`Regenerate` (Task 1) are consumed by name in Tasks 2 and 4; `CSRFFormField` is the single source of the field name, used by `validCSRF` (Task 1), the middleware test (Task 3) and `CsrfField.vue` (Task 2, which hard-codes `_csrf` because a Vue file cannot import a Go constant — the audit in Task 2 and the middleware test in Task 3 both fail if they drift); `clientIP` (Task 5) is consumed by `LoginSubmit` (Task 6); `loginWindow`/`loginMaxAttempts` are declared once in Task 6 and used by its tests.
- **Ordering is load-bearing.** Delivery (Task 2) precedes enforcement (Task 3) so the suite stays green while the field spreads; enforcement owns the helper migration. Task 4 depends on Task 2 having made the login page mint a token, since that is what creates the session it regenerates.
- **Helpers that already exist and must not be redeclared:** `newTestEngine` and `installedWith` in the session package's tests, `newTestEngine`/`putInGroup`/`loginStack`/`loginAndGetCookie`/`post`/`postForm` in the admin package's. Task 1's `newCSRFSession`, Task 3's `csrfStack`/`mint` and Task 3's `csrfFor` are the new ones. Also: `inertia.New` returns `(*Engine, error)` — the existing `newTestEngine` handles that, which is why the plan builds on it rather than calling `inertia.New` directly.
- **One thing the plan cannot pre-write:** Task 3 Step 4 migrates test helpers whose exact failure list depends on the code as it stands. The step says to work from what the suite reports rather than inventing a list here, which would be stale by the time it is read.
