package session

import (
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
