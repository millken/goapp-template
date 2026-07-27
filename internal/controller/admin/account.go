package admin

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/millken/goapp-template/internal/validate"
	"github.com/millken/inertia"
)

// mountAccount registers the signed-in user's own account page.
//
// This is the only route in the admin area registered outside the permission
// registrar, and it is deliberate: it uses AuthMiddleware, so it requires a
// session but no permission. Routing it through the registrar would create
// account.access / account.modify keys, and a user whose group holds neither
// could never change their own password — the one thing every user must be able
// to do for themselves. It is the third exemption, alongside logout and the
// dashboard.
//
// If you are reading this while auditing "which routes skip the registrar", this
// is the expected answer, not an oversight.
func (a *Admin) mountAccount(eng *inertia.Engine) {
	auth := a.AuthMiddleware()
	base := a.accountBase()
	eng.GET(base, auth, a.passwordForm)
	eng.POST(base, auth, a.passwordSubmit)
}

func (a *Admin) accountBase() string { return a.Prefix() + "/account/password" }

func (a *Admin) passwordForm(c *inertia.Context) {
	a.renderPasswordForm(c, nil)
}

func (a *Admin) passwordSubmit(c *inertia.Context) {
	ctx := c.Request.Context()
	id, ok := a.callerID(c)
	if !ok {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	current := c.PostForm("current")
	password := c.PostForm("password")
	confirm := c.PostForm("confirm")

	q := fmt.Sprintf(`SELECT password_hash FROM %s WHERE id = ?`, a.usersTable())
	var hash string
	if err := a.DB.QueryRowContext(ctx, q, id).Scan(&hash); err != nil {
		slog.Error("admin: load own password hash", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	v := validate.New()
	// A wrong current password is a field error rather than a refusal: the user
	// is allowed on this page, they just mistyped.
	v.Check(verifyPassword(hash, current), "current", "当前密码不正确")
	v.Field("password", password,
		validate.Required,
		validate.MinLen(8),
		passwordFits,
	)
	v.Check(password == confirm, "confirm", "两次输入不一致")
	if !v.OK() {
		a.renderPasswordForm(c, v.Errors())
		return
	}

	newHash, err := HashPassword(password)
	if err != nil {
		slog.Error("admin: hash password", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	uq := fmt.Sprintf(`UPDATE %s SET password_hash = ? WHERE id = ?`, a.usersTable())
	if _, err := a.DB.ExecContext(ctx, uq, newHash, id); err != nil {
		slog.Error("admin: change own password", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// The session is untouched: the password changed, not the identity, and
	// signing the user out of the page they just used would be gratuitous.
	a.flash(c, "success", "密码已修改")
	a.redirect(c, a.mount())
}

func (a *Admin) renderPasswordForm(c *inertia.Context, errs map[string]string) {
	c.Set("basePath", a.accountBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/account/password"); err != nil {
		slog.Error("render admin account password", "err", err)
	}
}
