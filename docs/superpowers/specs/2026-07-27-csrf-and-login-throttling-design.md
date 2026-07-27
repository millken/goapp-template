# CSRF and Login Throttling — Design Spec

Date: 2026-07-27 · Status: Validated — approved for planning · Depends on: the
`session` component; the throttle additionally on `db` and `admin`

Two independent pieces of hardening that share nothing but a subject. They are in
one spec because they are each too small for their own, and the plan keeps them
in separate tasks.

## 1. Goal

Close two gaps in code that already ships.

**CSRF.** Every mutating route in the admin area is a cookie-authenticated form
POST, and the only thing standing between them and a cross-site submission is
`SameSite=Lax`. Lax does block cross-site form posts, so this is not an open
door — but `config.yaml` lets an operator set `same_site: none`, and doing so
removes the entire defence with nothing anywhere saying so. A template should
not have a config value that quietly turns off a security property.

**Login throttling.** `POST /admin/login` will answer an unlimited number of
guesses as fast as bcrypt allows. The user-management spec listed failed-login
counting as out of scope; this is where it comes back.

## 2. Decisions

| Question | Decision |
|---|---|
| Which requests are checked | **Every unsafe method** — POST, PUT, PATCH, DELETE — not just the admin area. A public generated resource with a mutating form is exactly what CSRF targets |
| Where the token lives | **The `session` component.** Without a cookie there is no CSRF risk, so a project that trims session loses both the protection and the thing needing protection |
| When a token is created | **On demand**, when a handler renders a page that needs one — not for every visitor |
| How it reaches the server | A hidden `_csrf` form field. PJAX sends the form as `FormData`, so one field serves both plain posts and PJAX |
| Session id on login | **Regenerated.** Not a bonus: without it this design would introduce session fixation, because issuing a token makes a session exist before sign-in |
| Throttle dimension | **Client IP, sliding window.** Counting per username is a denial-of-service vector: anyone could lock anyone out |
| Client IP source | `RemoteAddr`, unless the peer is inside a configured trusted-proxy range. **Trusting `X-Forwarded-For` by default would let a caller forge the dimension being counted**, which is worse than no throttle because it looks like one |

## 3. CSRF

### 3.1 The token

One per session, 32 random bytes, base64url, stored under the session key
`_csrf`.

```go
// Session gains:
CSRFToken(ctx context.Context) (string, error)
```

It creates and persists on first call — which is what makes a session (and its
cookie) exist for an anonymous visitor to a page with a form, and only then. That
is also what makes the next section necessary.

### 3.2 This feature introduces session fixation unless login regenerates the id

Found while checking the paragraph that used to sit here, which claimed the
session id changes on login. It does not: `store.Save` keeps the id it is given
and only generates one when the id is empty (`store_db.go`, `store_memory.go`),
and `LoginSubmit` calls `Set` then `Save` on whatever session the request already
had.

Today that is harmless, because no session exists before login — sessions are
lazy, so login's `Save` is the first one and it generates a fresh id. **This
design breaks that.** Issuing a CSRF token on the login page creates a session
for the anonymous visitor, so login then reuses that id, and anyone who can plant
a cookie before sign-in still holds a valid session after it.

So the feature carries a fix for the hole it would otherwise open:

```go
// Session gains:
Regenerate(ctx context.Context) error   // new id, same values, old entry destroyed
```

`LoginSubmit` calls it immediately after a successful `authenticate` and before
storing the user id. The CSRF token is regenerated with it, since a token tied to
the abandoned id is worthless.

Nothing else regenerates: the point is the privilege change, and rotating on
every request would invalidate any form the user has open.


### 3.3 Why on demand, and what it costs

The obvious alternative is for the session middleware to inject a token into
every response. It is simpler and impossible to forget. It also means every
visitor to every page gets `Set-Cookie`, including the public home page — which
makes the page uncacheable by any CDN and hands a cookie to people who never
submit anything.

On demand avoids that, at the price of a real failure mode: a handler that
forgets to provide the token renders a form whose submit answers 403. Two things
make that failure loud instead of silent:

- **The existing tests.** Every mutating route already has tests that POST to it.
  With the check in place, those tests fail unless they carry a token, so the
  handler side cannot regress quietly.
- **A source audit**, alongside the ones in `frontend/scripts/`: every
  `<form method="post">` under `frontend/pages/` and the admin composites must
  contain the CSRF field. This is the template side, which no Go test sees.

### 3.4 Delivery

Handlers that render a form set the prop:

```go
c.Set("csrfToken", token)
```

and pages render one hidden input. A composite carries it so the markup exists in
exactly one place:

```vue
<!-- frontend/src/components/admin/CsrfField.vue -->
<input type="hidden" name="_csrf" :value="csrfToken ?? ''">
```

**Applies to the public generated resource too, which is easy to forget.**
`gen resource` produces public pages with mutating forms and a handler that
deliberately avoids the session. The check does not care: it runs in the session
middleware on every unsafe method, so those forms need a token exactly as the
admin's do. They cannot use the admin's `CsrfField` — a build without the admin
component has no such file — so the input is written inline, and the handler
tolerates a nil `Session` for a build without that component.

(Recorded after the fact: §2 said "not just the admin area" and the plan's
delivery task then omitted these templates, which shipped every generated
public form answering 403. The contradiction was between two of my own
documents, and a reviewer blessed the omission on the grounds that there was
"no session there" — session is not absent, only not required.)

**The generated templates cannot make this conditional.** `markers.forms` has no
`.vue` form, and a marker in a file type it does not know makes `init` fail
outright — so the field is unconditional in the scaffold. In a build with no
session component the prop is absent, the field renders empty, and no middleware
exists to check it. Harmless, and the only shape available.

### 3.5 Verification

Inside the existing `session.Service.Middleware()`, before the handler runs. Same
middleware because it already holds the session and is already mounted once,
globally — a second mount point is a second thing to get the ordering of wrong.

- Safe methods (GET, HEAD, OPTIONS) pass untouched.
- Unsafe methods must present a token matching the session's, compared with
  `subtle.ConstantTimeCompare`. It is read from the `_csrf` form field, or from
  an `X-CSRF-Token` header for a future JSON client.
- A request with no session at all still fails: there is nothing to match, which
  is the correct answer for a mutating request that carries no established
  session.
- Failure is **403 with a plain body**, not a redirect. A redirect would re-render
  the form and look like a validation problem; this is not one.

There is no per-route exemption mechanism. Nothing in the template needs one, and
inventing an exemption list before there is a caller is how such a list ends up
with an entry nobody can justify.

## 4. Login throttling

### 4.1 Client IP

```go
// [admin] gains:
TrustedProxies []string `yaml:"trusted_proxies"` // CIDRs; empty means trust none
```

The setting lives on `[admin]` because the login form is its only consumer, which
is the same bar the rest of this project applies. If a second consumer appears it
should move to `[server]`.

Resolution: if the peer address is inside one of the trusted prefixes, walk
`X-Forwarded-For` from the right and take the first entry that is not itself
trusted; otherwise use `RemoteAddr` and ignore the header entirely. Default is
empty, so out of the box the header is never read.

An unparseable prefix in the config is a startup error, not a warning:
a typo that silently disables the trust list would silently disable the throttle.

### 4.2 Storage

Migration `005_login_attempts`:

```sql
CREATE TABLE IF NOT EXISTS login_attempts (
    ip TEXT NOT NULL,
    at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS login_attempts_ip_at ON login_attempts (ip, at);
```

The window is a count, not a counter: `WHERE ip = ? AND at > ?`. Rows older than
the window are deleted on each write, so there is no scheduled job and the table
stays proportional to recent activity rather than to history.

### 4.3 The rule

- More than **10 failures from one IP within 15 minutes** refuses further
  attempts from that IP until the oldest of them ages out.
- Refusal is **429**, rendered on the login page with how long to wait — a number
  the operator can act on, rather than a generic failure.
- A successful sign-in deletes that IP's rows.
- **A correct password against a disabled account does not count.** It is not a
  guess, and counting it would let a disabled user lock their own address out
  while trying to understand why they cannot get in.
- A storage failure while counting **allows the attempt** and logs. The throttle
  is a rate limit, not an authorisation decision, and failing it closed would
  turn a database blip into a total lockout.

## 5. Testing

**CSRF**
- A POST with no token, a wrong token, and a valid token — 403, 403, through.
- The same three through the PJAX path, which sends `FormData` via `fetch`; it is
  a different code path and the reason the field beats a header.
- GET is untouched, and issues no cookie for a page that renders no form.
- Constant-time comparison is used (asserted by reading the code path, not by
  timing, which is not reproducible in CI).
- A build with the session component trimmed still generates and compiles a
  working resource.
- The template audit: every `<form method="post">` carries the field.
- **The session id after login differs from the id the login page was served
  with**, and the pre-login entry is gone from the store. This is the one test
  that would have caught the hole this design opened.

**Throttling**
- The boundary: the tenth failure is refused and the ninth is not.
- A success inside the window clears the count.
- A disabled account with the right password does not count.
- `X-Forwarded-For` is ignored when the peer is not a trusted proxy — the case
  most likely to be got wrong, and the one where getting it wrong means the
  throttle can be bypassed by anyone who sets a header.
- With a trusted proxy configured, the rightmost untrusted entry is the one
  counted.
- A storage failure lets the attempt through rather than locking everyone out.

## 6. Out of scope

- CAPTCHA.
- Account-level lockout — the denial-of-service vector this design exists to
  avoid.
- Two-factor authentication, password reset, email.
- Distributed rate limiting beyond what the shared table already provides.
- Per-route CSRF exemptions, until something needs one.
- Rotating the CSRF token on privilege change.
