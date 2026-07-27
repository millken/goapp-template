package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/validate"
	"github.com/millken/inertia"
)

// groupRow is one row of the group list, and the edit form's model.
type groupRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Superuser bool   `json:"superuser"`
	Members   int    `json:"members"`
	Keys      int    `json:"keys"`
}

// mountGroups registers the group resource.
func (a *Admin) mountGroups(eng *inertia.Engine) {
	base := a.Prefix() + "/group"
	r := a.Resource(eng, "group")

	r.GET(base, a.groupsIndex)
	r.GET(base+"/new", a.groupNew)
	r.POST(base, a.groupCreate)
	r.GET(base+"/:id/edit", a.groupEdit)
	r.POST(base+"/:id", a.groupUpdate)
	r.POST(base+"/:id/delete", a.groupDelete)
	r.Menu("Access", "Groups", base)
}

func (a *Admin) groupBase() string { return a.Prefix() + "/group" }

// groupsIndex lists groups with their member counts — one query with a join
// rather than a count per row.
func (a *Admin) groupsIndex(c *inertia.Context) {
	q := fmt.Sprintf(`SELECT g.id, g.name, g.superuser, COUNT(u.id)
		FROM user_groups g LEFT JOIN %s u ON u.group_id = g.id
		GROUP BY g.id, g.name, g.superuser
		ORDER BY g.name`, a.usersTable())

	rows, err := a.DB.QueryContext(c.Request.Context(), q)
	if err != nil {
		slog.Error("admin: list groups", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []groupRow{}
	for rows.Next() {
		var g groupRow
		var superuser int
		if err := rows.Scan(&g.ID, &g.Name, &superuser, &g.Members); err != nil {
			slog.Error("admin: scan group", "err", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		g.Superuser = superuser != 0
		items = append(items, g)
	}
	if err := rows.Err(); err != nil {
		slog.Error("admin: list groups", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Set("items", items)
	c.Set("basePath", a.groupBase())
	if err := c.Render("admin/group/index"); err != nil {
		slog.Error("render admin group index", "err", err)
	}
}

func (a *Admin) groupNew(c *inertia.Context) {
	a.renderGroupForm(c, groupRow{}, nil, nil, nil)
}

func (a *Admin) groupCreate(c *inertia.Context) {
	ctx := c.Request.Context()
	item := groupRow{
		Name:      c.PostForm("name"),
		Superuser: c.PostForm("superuser") != "",
	}

	if v := a.validateGroup(ctx, item, 0); !v.OK() {
		a.renderGroupForm(c, item, v.Errors(), nil, nil)
		return
	}

	superuser := 0
	if item.Superuser {
		superuser = 1
	}
	if _, err := a.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at)
		 VALUES (?, ?, '[]', ?)`,
		item.Name, superuser, time.Now().UnixNano()); err != nil {
		slog.Error("admin: create group", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "分组已创建")
	a.redirect(c, a.groupBase())
}

func (a *Admin) groupEdit(c *inertia.Context) {
	id, _ := c.Params.GetInt64("id")
	item, keys, err := a.findGroupRow(c.Request.Context(), id)
	if err != nil {
		slog.Error("admin: load group", "err", err, "group", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if item == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	a.renderGroupForm(c, *item, nil, keys, nil)
}

// groupUpdate renames a group, sets its superuser flag, and stores its
// permission keys. The whole change runs under the last-superuser guard, since
// clearing the flag drops every member of the group out of the count at once.
func (a *Admin) groupUpdate(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	item := groupRow{
		ID:        id,
		Name:      c.PostForm("name"),
		Superuser: c.PostForm("superuser") != "",
	}
	keys := a.submittedKeys(c)

	if v := a.validateGroup(ctx, item, id); !v.OK() {
		a.renderGroupForm(c, item, v.Errors(), keys, nil)
		return
	}

	raw, err := json.Marshal(keys)
	if err != nil {
		slog.Error("admin: encode permissions", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	superuser := 0
	if item.Superuser {
		superuser = 1
	}
	err = a.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE user_groups SET name = ?, superuser = ?, permissions = ? WHERE id = ?`,
			item.Name, superuser, string(raw), id)
		return err
	})
	switch {
	case errors.Is(err, errLastSuperuser):
		v := validate.New()
		v.Check(false, "superuser", "系统必须至少保留一个启用的超级管理员")
		a.renderGroupForm(c, item, v.Errors(), keys, nil)
		return
	case err != nil:
		slog.Error("admin: update group", "err", err, "group", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "分组已更新")
	a.redirect(c, a.groupBase())
}

// groupDelete refuses while the group still has members: a user with no group is
// refused everything, including logout.
func (a *Admin) groupDelete(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	var members int
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE group_id = ?`, a.usersTable())
	if err := a.DB.QueryRowContext(ctx, q, id).Scan(&members); err != nil {
		slog.Error("admin: count group members", "err", err, "group", id)
		a.flash(c, "error", "删除失败，请查看日志")
		a.redirect(c, a.groupBase())
		return
	}
	if members > 0 {
		a.flash(c, "error", fmt.Sprintf("该分组还有 %d 个成员，请先把他们转到别的分组", members))
		a.redirect(c, a.groupBase())
		return
	}

	// An empty group cannot be the last superuser's group, so no guard is
	// needed: the count it protects only ever includes groups with members.
	if _, err := a.DB.ExecContext(ctx, `DELETE FROM user_groups WHERE id = ?`, id); err != nil {
		slog.Error("admin: delete group", "err", err, "group", id)
		a.flash(c, "error", "删除失败，请查看日志")
	} else {
		a.flash(c, "success", "分组已删除")
	}
	a.redirect(c, a.groupBase())
}

func (a *Admin) validateGroup(ctx context.Context, item groupRow, exceptID int64) *validate.Validator {
	v := validate.New()
	v.Field("name", item.Name,
		validate.Required,
		validate.MinLen(2),
		validate.MaxLen(64),
		a.groupNameAvailable(ctx, exceptID),
	)
	return v
}

func (a *Admin) groupNameAvailable(ctx context.Context, exceptID int64) validate.Rule {
	return func(name string) error {
		var n int
		if err := a.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM user_groups WHERE name = ? AND id != ?`, name, exceptID).Scan(&n); err != nil {
			slog.Error("admin: check group name", "err", err)
			return errors.New("无法校验，请重试")
		}
		if n > 0 {
			return errors.New("已被占用")
		}
		return nil
	}
}

// findGroupRow loads one group plus its stored permission keys, returning
// (nil, nil, nil) if absent.
func (a *Admin) findGroupRow(ctx context.Context, id int64) (*groupRow, []string, error) {
	var g groupRow
	var superuser int
	var raw string
	if err := a.DB.QueryRowContext(ctx,
		`SELECT id, name, superuser, permissions FROM user_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	g.Superuser = superuser != 0

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		// A group whose stored keys are unreadable must still be editable —
		// otherwise the only way to repair it is SQL, which is what this page
		// exists to avoid.
		slog.Error("admin: group has unreadable permissions", "err", err, "group", id)
		keys = nil
	}
	g.Keys = len(keys)
	return &g, keys, nil
}

// permRow is one resource's two checkboxes in the grid.
type permRow struct {
	Resource string `json:"resource"`
	Access   bool   `json:"access"`
	Modify   bool   `json:"modify"`
}

// permissionRows turns a group's stored keys into grid rows, and returns the
// keys that no longer correspond to a registered route.
//
// Rows come from the catalogue — the routes actually registered at startup — so
// the grid cannot offer a permission nothing checks. Keys outside it are stale:
// a resource that was renamed or removed. They are reported rather than dropped
// quietly, because the alternative is a group holding permissions its own edit
// page never showed.
func (a *Admin) permissionRows(keys []string) ([]permRow, []string) {
	held := make(map[string]bool, len(keys))
	for _, k := range keys {
		held[k] = true
	}

	seen := map[string]bool{}
	rows := []permRow{}
	known := map[string]bool{}
	for _, p := range a.Permissions() {
		known[p.Key] = true
		resource, _, ok := splitPermKey(p.Key)
		if !ok || seen[resource] {
			continue
		}
		seen[resource] = true
		rows = append(rows, permRow{
			Resource: resource,
			Access:   held[resource+verbAccess],
			Modify:   held[resource+verbModify],
		})
	}

	stale := []string{}
	for _, k := range keys {
		if !known[k] {
			stale = append(stale, k)
		}
	}
	slices.Sort(stale)
	return rows, stale
}

// splitPermKey splits "post.modify" into ("post", ".modify"). Reported false for
// anything that is not a permission key, which Resource's name validation makes
// impossible for registered resources but not for stored data.
func splitPermKey(key string) (resource, verb string, ok bool) {
	for _, v := range []string{verbAccess, verbModify} {
		if strings.HasSuffix(key, v) {
			return strings.TrimSuffix(key, v), v, true
		}
	}
	return "", "", false
}

// submittedKeys reads the permission checkboxes and normalises them: modify
// implies access, one-way, exactly as permSet.Allows has it. The grid does the
// same thing client-side for immediate feedback, which is why this cannot be
// left to the grid — convenience there, enforcement here.
//
// Keys not in the catalogue are ignored rather than trusted, so a hand-made POST
// cannot store a permission the interface would never show.
func (a *Admin) submittedKeys(c *inertia.Context) []string {
	if err := c.Request.ParseForm(); err != nil {
		slog.Error("admin: parse permission form", "err", err)
		return nil
	}
	known := map[string]bool{}
	for _, p := range a.Permissions() {
		known[p.Key] = true
	}

	set := map[string]bool{}
	for _, k := range c.Request.Form["permissions"] {
		if !known[k] {
			continue
		}
		set[k] = true
		if resource, verb, ok := splitPermKey(k); ok && verb == verbModify {
			set[resource+verbAccess] = true
		}
	}

	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// renderGroupForm renders the create/edit form with the permission grid.
func (a *Admin) renderGroupForm(c *inertia.Context, item groupRow, errs map[string]string, keys, stale []string) {
	rows, computedStale := a.permissionRows(keys)
	if stale == nil {
		stale = computedStale
	}
	c.Set("item", item)
	c.Set("permissions", rows)
	c.Set("stale", stale)
	c.Set("basePath", a.groupBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/group/form"); err != nil {
		slog.Error("render admin group form", "err", err)
	}
}
