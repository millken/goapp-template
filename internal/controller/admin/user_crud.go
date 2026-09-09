package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/validate"
	"github.com/millken/inertia"
)

// usernameRe is deliberately ASCII-only: admin accounts are created by an
// operator, not chosen by a visitor, and they show up in log lines, CLI
// arguments and create-user invocations where a Unicode identifier is a
// nuisance rather than a feature.
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// bcryptMaxPassword is bcrypt's hard limit, in BYTES — not characters.
// x/crypto/bcrypt returns ErrPasswordTooLong beyond it rather than truncating,
// so a password that passes a character-based length check can still fail at
// hashing time: 30 Chinese characters are 90 bytes.
const bcryptMaxPassword = 72

// passwordFits rejects a password bcrypt cannot store. Separate from
// validate.MaxLen, which counts runes — the right unit for a name, the wrong one
// for this. Without it, a non-ASCII password of legal character length reaches
// HashPassword and turns into a 500 the user cannot act on.
var passwordFits validate.Rule = func(value string) error {
	if len(value) > bcryptMaxPassword {
		return errors.New("太长了：上限是 72 字节，中文约 24 个字")
	}
	return nil
}

// userRow is one row of the user list, and the edit form's model.
type userRow struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	GroupID  int64  `json:"group_id"`
	Group    string `json:"group"`
	Status   int    `json:"status"`
	Created  int64  `json:"created_at"`
	Avatar   string `json:"avatar"`
}

// groupOption is a choice in the form's group select.
type groupOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// mountUsers registers the user resource. Every route goes through the
// registrar, so user.access guards the reads and user.modify the writes without
// either being named here.
func (a *Admin) mountUsers(eng *inertia.Engine) {
	base := a.Prefix() + "/user"
	r := a.Resource(eng, "user")

	r.GET(base, a.usersIndex)
	r.GET(base+"/new", a.userNew)
	r.POST(base, a.userCreate)
	r.GET(base+"/:id/edit", a.userEdit)
	r.POST(base+"/:id", a.userUpdate)
	r.POST(base+"/:id/delete", a.userDelete)
	r.POST(base+"/:id/status", a.userSetStatus)
	r.Menu("访问控制", "用户", base)
}

func (a *Admin) userBase() string { return a.Prefix() + "/user" }

// usersIndex lists users with their group name — one query, joined, rather than
// a lookup per row.
func (a *Admin) usersIndex(c *inertia.Context) {
	q := fmt.Sprintf(`SELECT u.id, u.username, COALESCE(u.group_id, 0), COALESCE(g.name, ''),
		u.status, u.created_at, u.avatar
		FROM %s u LEFT JOIN admin_groups g ON g.id = u.group_id
		ORDER BY u.username`, a.adminsTable())

	rows, err := a.DB.QueryContext(c.Request.Context(), q)
	if err != nil {
		slog.Error("admin: list users", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	items := []userRow{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Username, &u.GroupID, &u.Group, &u.Status, &u.Created, &u.Avatar); err != nil {
			slog.Error("admin: scan user", "err", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		slog.Error("admin: list users", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Set("items", items)
	c.Set("basePath", a.userBase())
	if err := c.Render("admin/user/index"); err != nil {
		slog.Error("render admin user index", "err", err)
	}
}

func (a *Admin) userNew(c *inertia.Context) {
	a.renderUserForm(c, userRow{Status: statusActive}, nil)
}

func (a *Admin) userCreate(c *inertia.Context) {
	ctx := c.Request.Context()
	item := userRow{
		Username: c.PostForm("username"),
		Status:   statusActive,
	}
	item.GroupID, _ = parseInt64(c.PostForm("group_id"))
	password := c.PostForm("password")

	//goappctl:storage
	// Marked, so a storage-less build never stores a path it has no way to
	// validate or render: with the component off this line is gone and Avatar
	// stays "".
	item.Avatar = c.PostForm("avatar")
	//goappctl:end

	v := a.validateUser(ctx, item, password, true, 0)
	if !v.OK() {
		a.renderUserForm(c, item, v.Errors())
		return
	}

	hash, err := HashPassword(password)
	if err != nil {
		slog.Error("admin: hash password", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	q := fmt.Sprintf(`INSERT INTO %s (username, password_hash, created_at, status, group_id, avatar)
		VALUES (?, ?, ?, ?, ?, ?)`, a.adminsTable())
	if _, err := a.DB.ExecContext(ctx, q, item.Username, hash, time.Now().UnixNano(),
		statusActive, item.GroupID, item.Avatar); err != nil {
		slog.Error("admin: create user", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "用户已创建")
	// c.Redirect, not http.Redirect: under a PJAX navigation this has to become a
	// {redirect} payload the client can act on. A raw 3xx is followed by fetch
	// transparently, leaving the list rendered while the address bar still names
	// the URL that was posted to — which is what TestRedirects_UnderPJAX exists
	// to prevent.
	a.redirect(c, a.userBase())
}

func (a *Admin) userEdit(c *inertia.Context) {
	id, _ := c.Params.GetInt64("id")
	item, err := a.findUserRow(c.Request.Context(), id)
	if err != nil {
		slog.Error("admin: load user", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if item == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	a.renderUserForm(c, *item, nil)
}

func (a *Admin) userUpdate(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	current, err := a.findUserRow(ctx, id)
	if err != nil {
		slog.Error("admin: load user", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if current == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	item := userRow{ID: id, Username: c.PostForm("username"), Status: current.Status}
	item.GroupID, _ = parseInt64(c.PostForm("group_id"))
	password := c.PostForm("password")

	//goappctl:storage
	// Marked, so a storage-less build never stores a path it has no way to
	// validate or render: with the component off this line is gone and Avatar
	// stays "".
	item.Avatar = c.PostForm("avatar")
	//goappctl:end

	v := a.validateUser(ctx, item, password, password != "", id)
	// Rule 2: you may edit your own username and password, but not move yourself
	// to another group — that is how you take away your own access.
	if item.GroupID != current.GroupID {
		if err := a.notSelf(c, id); err != nil {
			v.Check(false, "group_id", "不能修改自己所在的分组")
		}
	}
	if !v.OK() {
		a.renderUserForm(c, item, v.Errors())
		return
	}

	// The group change can strand the last superuser, so it runs under the
	// guard; the guard is harmless when nothing about superuser status changed.
	err = a.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		q := fmt.Sprintf(`UPDATE %s SET username = ?, group_id = ?, avatar = ? WHERE id = ?`, a.adminsTable())
		if _, err := tx.ExecContext(ctx, q, item.Username, item.GroupID, item.Avatar, id); err != nil {
			return err
		}
		if password == "" {
			return nil
		}
		hash, err := HashPassword(password)
		if err != nil {
			return err
		}
		pq := fmt.Sprintf(`UPDATE %s SET password_hash = ? WHERE id = ?`, a.adminsTable())
		_, err = tx.ExecContext(ctx, pq, hash, id)
		return err
	})
	switch {
	case errors.Is(err, errLastSuperuser):
		v.Check(false, "group_id", "系统必须至少保留一个启用的超级管理员")
		a.renderUserForm(c, item, v.Errors())
		return
	case err != nil:
		slog.Error("admin: update user", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "用户已更新")
	a.redirect(c, a.userBase())
}

// userDelete removes a user. Rule 1 blocks your own account; rule 3 blocks the
// change if it would leave no enabled superuser, and rolls it back.
func (a *Admin) userDelete(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	// A refusal is a flash plus a redirect — the same shape the last-superuser
	// rule gets below. Both are things the operator asked for that will not
	// happen, and both need the reason to land on the page they were looking at.
	// Answering one with a bare 403 would strand them on a blank response with
	// the explanation staged but never rendered.
	if err := a.notSelf(c, id); err != nil {
		a.flash(c, "error", "不能删除自己的账号")
		a.redirect(c, a.userBase())
		return
	}

	err := a.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		q := fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, a.adminsTable())
		_, err := tx.ExecContext(ctx, q, id)
		return err
	})
	switch {
	case errors.Is(err, errLastSuperuser):
		a.flash(c, "error", "系统必须至少保留一个启用的超级管理员")
	case err != nil:
		slog.Error("admin: delete user", "err", err, "user", id)
		a.flash(c, "error", "删除失败，请查看日志")
	default:
		a.flash(c, "success", "用户已删除")
	}
	a.redirect(c, a.userBase())
}

// userSetStatus enables or disables a user. The value comes from the request
// rather than being toggled, so a double-submitted form cannot flip a user back
// on — and the two directions are not symmetric: only disabling can lock anyone
// out, so only disabling is guarded.
func (a *Admin) userSetStatus(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	want := statusActive
	if c.PostForm("status") == "0" {
		want = statusDisabled
	}

	if want == statusDisabled {
		if err := a.notSelf(c, id); err != nil {
			a.flash(c, "error", "不能禁用自己的账号")
			a.redirect(c, a.userBase())
			return
		}
	}

	set := func(tx *sqldb.Tx) error {
		q := fmt.Sprintf(`UPDATE %s SET status = ? WHERE id = ?`, a.adminsTable())
		_, err := tx.ExecContext(ctx, q, want, id)
		return err
	}

	var err error
	if want == statusDisabled {
		err = a.keepingASuperuser(ctx, set)
	} else {
		// Enabling can only increase the count, so the guard has nothing to
		// check; running it anyway would be misleading rather than wrong.
		err = a.DB.Transaction(set)
	}
	switch {
	case errors.Is(err, errLastSuperuser):
		a.flash(c, "error", "系统必须至少保留一个启用的超级管理员")
	case err != nil:
		slog.Error("admin: set user status", "err", err, "user", id)
		a.flash(c, "error", "操作失败，请查看日志")
	case want == statusDisabled:
		a.flash(c, "success", "用户已禁用")
	default:
		a.flash(c, "success", "用户已启用")
	}
	a.redirect(c, a.userBase())
}

// validateUser checks a submitted user. requirePassword is false on an update
// with a blank password field, which means "leave it alone" — otherwise every
// username edit would silently reset someone's password. exceptID is the row
// being updated, so the uniqueness rule ignores its own current value.
//
// Cheap rules come first: a failure short-circuits the field, so the database is
// never queried for input that was blank or malformed anyway.
func (a *Admin) validateUser(ctx context.Context, item userRow, password string, requirePassword bool, exceptID int64) *validate.Validator {
	v := validate.New()
	v.Field("username", item.Username,
		validate.Required,
		validate.MinLen(3),
		validate.MaxLen(64),
		validate.Msg(validate.Match(usernameRe), "只能包含字母、数字、点、下划线和连字符"),
		a.usernameAvailable(ctx, exceptID),
	)
	if requirePassword {
		v.Field("password", password,
			validate.Required,
			validate.MinLen(8),
			passwordFits,
		)
	}
	v.Check(item.GroupID != 0, "group_id", "请选择一个分组")
	if item.GroupID != 0 {
		exists, err := a.groupExists(ctx, item.GroupID)
		if err != nil {
			slog.Error("admin: check group", "err", err)
			v.Check(false, "group_id", "无法校验，请重试")
		} else {
			v.Check(exists, "group_id", "该分组不存在")
		}
	}
	//goappctl:storage
	// A path from a form is a string a browser sent, and the picker is not the
	// only way to fill this field. Empty means "no avatar" and is valid.
	if item.Avatar != "" {
		v.Check(a.Storage.ValidatePath(item.Avatar) == nil, "avatar", "图片路径不合法")
	}
	//goappctl:end
	return v
}

// usernameAvailable rejects a username another row already uses. A failed query
// degrades to a message on the form rather than a 500 — the user gets something
// actionable, and the cause is in the log.
func (a *Admin) usernameAvailable(ctx context.Context, exceptID int64) validate.Rule {
	return func(name string) error {
		q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE username = ? AND id != ?`, a.adminsTable())
		var n int
		if err := a.DB.QueryRowContext(ctx, q, name, exceptID).Scan(&n); err != nil {
			slog.Error("admin: check username", "err", err)
			return errors.New("无法校验，请重试")
		}
		if n > 0 {
			return errors.New("已被占用")
		}
		return nil
	}
}

func (a *Admin) groupExists(ctx context.Context, id int64) (bool, error) {
	var n int
	if err := a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM admin_groups WHERE id = ?`, id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// findUserRow loads one row for the edit form, returning (nil, nil) if absent.
func (a *Admin) findUserRow(ctx context.Context, id int64) (*userRow, error) {
	q := fmt.Sprintf(`SELECT u.id, u.username, COALESCE(u.group_id, 0), COALESCE(g.name, ''),
		u.status, u.created_at, u.avatar
		FROM %s u LEFT JOIN admin_groups g ON g.id = u.group_id
		WHERE u.id = ?`, a.adminsTable())
	var u userRow
	if err := a.DB.QueryRowContext(ctx, q, id).
		Scan(&u.ID, &u.Username, &u.GroupID, &u.Group, &u.Status, &u.Created, &u.Avatar); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// groupOptions are the choices in the form's group select.
func (a *Admin) groupOptions(ctx context.Context) ([]groupOption, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, name FROM admin_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []groupOption{}
	for rows.Next() {
		var g groupOption
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// renderUserForm renders the create/edit form. errs is nil on a first visit and
// carries one message per bad field after a failed submit; item repopulates the
// inputs, so what was typed survives the re-render.
func (a *Admin) renderUserForm(c *inertia.Context, item userRow, errs map[string]string) {
	groups, err := a.groupOptions(c.Request.Context())
	if err != nil {
		slog.Error("admin: load group options", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Set("item", item)
	c.Set("groups", groups)
	c.Set("basePath", a.userBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/user/form"); err != nil {
		slog.Error("render admin user form", "err", err)
	}
}

// flash stages a one-shot message for the page we are about to redirect to. The
// session middleware injects it as the `flash` prop on the next request and
// clears it, so it shows exactly once.
//
// How it is shown follows from the kind, and the default is the right answer
// almost always: a success is a receipt you do not need once you have read it,
// so it becomes a toast that dismisses itself; an error is context you need
// while fixing something, so it stays on the page until the next navigation.
//
// To override that default, stage the key yourself: stageFlash(c, "toast:error",
// msg) or "alert:success" — AdminShell.vue's split() reads the prefix.
func (a *Admin) flash(c *inertia.Context, kind, message string) {
	a.stageFlash(c, kind, message)
}

func (a *Admin) stageFlash(c *inertia.Context, key, message string) {
	sess := a.Session.Session(c)
	sess.Flash(key, message)
	if _, err := sess.Save(c.Request.Context()); err != nil {
		slog.Error("admin: stage flash", "err", err)
	}
}

// redirect sends the caller to location, as a {redirect} payload under PJAX and
// a 302 otherwise. Every redirect in the admin area goes through here rather than
// http.Redirect, so a PJAX client is never left showing one page while the
// address bar names another.
func (a *Admin) redirect(c *inertia.Context, location string) {
	if err := c.Redirect(location); err != nil {
		slog.Error("admin: redirect", "err", err, "to", location)
	}
}

// parseInt64 is strconv.ParseInt with the base and bit size fixed, for form
// fields that carry ids.
func parseInt64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
