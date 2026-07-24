# OpenCart 风格 Go 架构 —— codegen 落地版(设计文档)

> 状态: **Draft · 与前两稿并排对照**
> 前提: 保留 `github.com/millken/inertia` 作为 HTTP/路由/渲染层(它的 radix router + Context 池比 stdlib `ServeMux` 快,且承载 SSR/Inertia 协议)
> 范围: 仅设计,不动代码
> 相关: [`opencart-design.md`](../opencart-design.md)(反射派发稿)、[`opencart-architecture.md`](./opencart-architecture.md)(`map[string]any` Registry 稿)

---

## 0. 核心立场

**学 OpenCart 的原则,用 Go 的手段实现,而不是把 PHP 的动态性逐行翻译过来。**

OpenCart 简洁、易扩展的功劳不在 `$registry` 魔术属性、`$obj->$action()` 动态调用、`new $class()` 动态加载 —— 那三样只是 PHP 动态类型的产物。真正的价值是四条**原则**;而这四条在 Go 里都能用**编译期安全**的手段拿到,不必付 `any` 断言、运行时反射的代价。

| OpenCart 的优点 | 它为什么好 | 本方案的 Go 手段 |
|---|---|---|
| route = 路径 = 文件 = 方法 | 无路由表要维护,心智负担低 | **代码生成**:scaffold 按约定生成 `*_gen.go`,内含**直接方法引用**(非反射) |
| 加功能 = 丢一个文件 | 零接口、零注册仪式 | 同上,生成器把新 controller 的方法接进 inertia router |
| Registry 一处取所有服务 | 不用把 db 穿过 N 层构造函数 | **一个 typed struct `*app.Services`**,而不是 `map[string]any` |
| 一处装配(`index.php`) | 依赖关系肉眼可见 | `commands/serve.go` 一处显式装配;feature 无生命周期接口 |

**关键洞察:Go 里"约定优于配置"的正解是代码生成,不是运行时反射。** 约定逻辑(方法名 → 动词 + 路径)只活在生成器里;产物是编译期可查、可 `grep`、栈可读的普通 Go 代码。

### 0.1 与前两稿的根本差异

| 维度 | `opencart-design.md` | `opencart-architecture.md` | **本稿(codegen)** |
|---|---|---|---|
| 服务容器 | `map[string]any` Registry | `map[string]any` + 泛型 `GetAs[T]` | **typed `*Services` struct** |
| 派发 | reflect 调方法名 | reflect 调方法名 | **生成的直接方法引用**,零反射 |
| 路由层 | 新 engine 接管 HTTP | 保留 inertia | **保留 inertia**(radix router + Context 池) |
| Handler 签名 | 自定义 Response | `func(*inertia.Context)` | **`func(*inertia.Context)`** |
| per-request 态 | 存进共享 Registry(**数据竞争**) | ctx 参数 | **只在 `*inertia.Context` 里**,controller 无状态 |
| 类型安全 | 运行期断言 | 运行期断言 | **编译期** |

---

## 1. 整体拓扑

```
┌───────────────────────────────────────────────────────────────┐
│ commands/serve.go —— 唯一装配点(对应 OpenCart index.php)      │
│   1. cfg := config.Load()                                      │
│   2. 构造并 Start 基础设施:db → session(有序、可回滚)         │
│   3. svc := app.NewServices(cfg, log, db, session)  ← typed    │
│   4. eng := server.New(cfg.Server)   ← inertia.Engine          │
│   5. controllers.Mount(eng, svc)     ← 生成的路由接线          │
│   6. eng.Serve() / defer Stop(逆序)                            │
└───────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌───────────────────────────────────────────────────────────────┐
│ inertia.Engine(保留)                                          │
│   radix Router[HandlerFuncs] + Context 对象池 + SSR/Inertia    │
│   eng.GET/POST/PUT/DELETE(path, ...HandlerFunc)                │
└───────────────────────────────────────────────────────────────┘
                              │  (由生成代码接线)
                              ▼
┌───────────────────────────────────────────────────────────────┐
│ internal/controller/<area>/<name>.go(手写,只有方法)         │
│   type Post struct{ *app.Services }        // 内嵌 typed 容器  │
│   func (c *Post) Index(ctx *inertia.Context) { ... }           │
│   func (c *Post) Show(ctx *inertia.Context)  { ... }           │
├───────────────────────────────────────────────────────────────┤
│ internal/controller/<area>/routes_gen.go(生成)               │
│   func Mount(eng, svc) { p:=&Post{svc}; eng.GET("/blog/post",  │
│                          p.Index); eng.GET(".../:id", p.Show) }│
├───────────────────────────────────────────────────────────────┤
│ internal/model/<area>/<name>.go(手写)                        │
│   type Model struct{ DB *sqldb.DB }  + CRUD                    │
└───────────────────────────────────────────────────────────────┘
```

---

## 2. 核心组件

### 2.1 `Services` —— typed 服务容器(取代 Registry)

```go
// internal/app/services.go
package app

import (
    "log/slog"

    "github.com/dnsoa/go/sqldb"
    "github.com/millken/goapp-template/internal/service/session"
)

// Services is the typed service container, assembled once after infrastructure
// Start and shared (read-only) by every controller. It replaces OpenCart's
// map-based $registry: same "one place to reach shared services" ergonomics,
// but every field is compile-time typed — no string keys, no `any`, no
// runtime type assertions, no missing-key panics.
//
// It holds ONLY process-lifetime services (safe to share across goroutines).
// Per-request state never lives here — it stays on *inertia.Context.
type Services struct {
    Log     *slog.Logger
    DB      *sqldb.DB        // already-opened handle (post-Start), never nil
    Session *session.Service // provides Session(ctx) per request
}

// NewServices builds the container from already-Started infrastructure. Because
// DB is the resolved handle (not a lazy provider), controllers never hit the
// "DB() before Start" panic path.
func NewServices(log *slog.Logger, db *sqldb.DB, sess *session.Service) *Services {
    return &Services{Log: log, DB: db, Session: sess}
}
```

**要点**

1. **加一个服务 = 加一个字段**,编译器强制所有引用处更新;拼错字段名根本编译不过。
2. **不可变共享**:`Services` 在 Start 后构造完即只读,多 goroutine 共享安全 —— 这是把 OpenCart 那个"进程级 Registry"该有的形态做对了(前一稿把 per-request 的 request/response 塞进共享 map,是数据竞争)。
3. controller 通过内嵌 `*Services` 拿到 `c.DB` / `c.Log` / `c.Session`,写法和 OpenCart 的 `$this->db` 一样短,但全程 typed。
4. **不含 `*config.Config`(实现时发现的循环依赖)**:`config` 导入了 feature/service 包(取它们的 `Config` 类型),而 controller 又导入 `app`;若 `app` 再导入 `config` 就成环 `app→config→controller/admin→app`。故 `Services` 只装运行期服务,不装 config;需要具体配置值的 controller 在构造时以 typed 字段传入。

### 2.2 Controller —— 只有方法

```go
// internal/controller/blog/post.go   (手写;生成器产出骨架)
package blog

import (
    "net/http"

    "github.com/millken/goapp-template/internal/app"
    blogmodel "github.com/millken/goapp-template/internal/model/blog"
    "github.com/millken/inertia"
)

// Post is the controller for /blog/post/*. Embedding *app.Services gives typed
// access to DB/Log/Session/Cfg. It holds no per-request state, so a single
// instance is shared across requests (constructed once in Mount).
type Post struct {
    *app.Services
}

// Index → GET /blog/post
func (c *Post) Index(ctx *inertia.Context) {
    m := &blogmodel.Model{DB: c.DB}
    items, err := m.All(ctx.Request.Context())
    if err != nil {
        c.Log.Error("blog post list", "err", err)
    }
    ctx.Set("items", items)
    if err := ctx.Render("blog/Post/Index"); err != nil {
        c.Log.Error("render", "err", err)
    }
}

// Show → GET /blog/post/:id
func (c *Post) Show(ctx *inertia.Context) {
    id, _ := ctx.Params.GetInt64("id")   // inertia typed param
    m := &blogmodel.Model{DB: c.DB}
    item, err := m.Get(ctx.Request.Context(), id)
    if err != nil || item == nil {
        ctx.AbortWithStatus(404)
        return
    }
    ctx.Set("item", item)
    _ = ctx.Render("blog/Post/Show")
}

// Create → POST /blog/post
func (c *Post) Create(ctx *inertia.Context) {
    var p blogmodel.Post
    p.Title = ctx.PostForm("title")
    p.Body = ctx.PostForm("body")
    if err := (&blogmodel.Model{DB: c.DB}).Insert(ctx.Request.Context(), &p); err != nil {
        c.Log.Error("blog post create", "err", err)
    }
    // inertia.Context has no Redirect; use stdlib against ctx.Writer/ctx.Request.
    // 303 See Other is the correct POST→GET redirect (also what Inertia expects).
    http.Redirect(ctx.Writer, ctx.Request, "/blog/post", http.StatusSeeOther)
}
```

**对比当前 `server/routes.go` 的 Module**:没有 `type Routes struct{}`、没有 `Register(a *app.App) error`、没有 `New(dbMod, ...)` 构造函数、没有 6 行 `eng.GET/POST` 手写路由。**只有方法。** 这正是 OpenCart controller 的观感,但每一处依赖都 typed。

### 2.3 路由约定(方法名 → 动词 + 路径)

生成器按下表把 controller 方法映射为 inertia 路由(`:id` 是 inertia router 的参数语法,已核对):

| 方法名 | HTTP | 路径(以 `blog/post` 为例) | 用途 | OpenCart 对应 |
|---|---|---|---|---|
| `Index` | GET | `/blog/post` | 列表 | `index()` |
| `New` | GET | `/blog/post/new` | 新建表单 | `add()` (GET) |
| `Create` | POST | `/blog/post` | 创建 | `add()` (POST) |
| `Show` | GET | `/blog/post/:id` | 详情 | `info()` |
| `Edit` | GET | `/blog/post/:id/edit` | 编辑表单 | `edit()` (GET) |
| `Update` | POST | `/blog/post/:id` | 更新 | `edit()` (POST) |
| `Delete` | POST | `/blog/post/:id/delete` | 删除 | `delete()` |

> 约定只存在于**生成器**里。运行期没有任何"解析方法名"的逻辑 —— 路由是生成好的静态 `eng.GET(...)` 调用。改约定 = 改生成器模板 + 重跑生成,产物照旧是可读代码。

### 2.4 生成的路由接线

```go
// internal/controller/blog/routes_gen.go   (生成;提交进仓库;可 grep;零反射)
// Code generated by `goapp gen`. DO NOT EDIT.
package blog

import (
    "github.com/millken/goapp-template/internal/app"
    "github.com/millken/inertia"
)

func Mount(eng *inertia.Engine, svc *app.Services) {
    p := &Post{svc}
    eng.GET("/blog/post",          p.Index)
    eng.GET("/blog/post/new",      p.New)
    eng.POST("/blog/post",         p.Create)
    eng.GET("/blog/post/:id",      p.Show)
    eng.GET("/blog/post/:id/edit", p.Edit)
    eng.POST("/blog/post/:id",     p.Update)
    eng.POST("/blog/post/:id/delete", p.Delete)
}
```

```go
// internal/controller/mount_gen.go   (生成;每次 gen 追加一行 area.Mount)
// Code generated by `goapp gen`. DO NOT EDIT.
package controller

import (
    "github.com/millken/goapp-template/internal/app"
    "github.com/millken/goapp-template/internal/controller/admin"
    "github.com/millken/goapp-template/internal/controller/blog"
    "github.com/millken/inertia"
)

// MountAll wires every generated area into the router. Called once from serve
// with the real *inertia.Engine, and from the dup-check test with a throwaway one.
func MountAll(eng *inertia.Engine, svc *app.Services) {
    blog.Mount(eng, svc)
    admin.Mount(eng, svc)   // admin routes carry the auth middleware — see §5
}
```

**为什么是代码生成而不是 AST 扫描 / 反射**:生成器在**创建 resource 时**就知道要产出 `Index/New/Create/Show/Edit/Update/Delete` 这几个方法,直接连带产出对应的 `eng.GET/POST` 行即可,无需构建期解析 Go 源码。产物是普通代码 → 编译期检查方法存在、IDE 可跳转、`grep '/blog/post'` 能定位、panic 变正常错误。
> (可选增强:未来若允许手写额外方法,再加 `go generate` + AST 扫描按约定补全路由。默认不需要。)

---

## 3. Model / View

Model 保持纯数据访问,不进容器、不持有 controller/engine 引用,DB 由调用方显式传入(controller 从 `c.DB` 拿):

```go
// internal/model/blog/post.go   (手写;生成器产出骨架)
package blog

import (
    "context"
    "github.com/dnsoa/go/sqldb"
)

type Post struct {
    ID    int64  `db:"id"`
    Title string `db:"title"`
    Body  string `db:"body"`
}

func (Post) TableName() string { return "posts" }

// Model is a stateless data-access helper. DB is injected explicitly by the
// caller (controller passes c.DB) — no reflection, no registry lookup.
type Model struct{ DB *sqldb.DB }

func (m *Model) All(ctx context.Context) ([]Post, error) { /* ... */ }
func (m *Model) Get(ctx context.Context, id int64) (*Post, error) { /* ... */ }
func (m *Model) Insert(ctx context.Context, p *Post) error { /* ... */ }
func (m *Model) Update(ctx context.Context, id int64, p *Post) error { /* ... */ }
func (m *Model) Delete(ctx context.Context, id int64) error { /* ... */ }
```

> 与 OpenCart `$this->load->model('blog/post')` 的差异:不做动态加载,`&blogmodel.Model{DB: c.DB}` 直接构造 —— 一行、typed、无断言。

### 3.1 View

基础形态不变,仍是 inertia + Vue:`ctx.Render("blog/Post/Index")` 对应 `frontend/pages/blog/Post/Index.vue`,复用 inertia 现有渲染/SSR。

### 3.2 主题切换(theme)—— 与 OpenCart 的关键差异

OpenCart 是**服务端模板解析**:按 active theme 切换 `.tpl` 目录、缺失回退 default。inertia 是**客户端渲染**:服务端只回 `{component, props}`,真正的 `.vue` 组件在前端 —— **Go 侧看不到组件文件,无法在 render 时做"模板是否存在"的判断**。所以照搬 OpenCart 的服务端解析不成立。正确的分层是:

**服务端决定"用哪个 theme",前端负责"解析组件 + 缺失回退"。**

1. **服务端:一处中间件决定 active theme,作为 inertia shared prop 下发**(不散落到各 controller):

```go
// active theme = 优先级:session 覆盖 > host/domain 映射 > config 默认
func ThemeMiddleware(svc *app.Services) inertia.HandlerFunc {
    return func(ctx *inertia.Context) {
        theme := svc.Cfg.View.DefaultTheme
        if t := svc.Session.Session(ctx).Theme(); t != "" {
            theme = t
        }
        ctx.Set("__theme", theme)   // 作为 shared prop 随每个页面下发
        ctx.Next()
    }
}
```

controller 里 `ctx.Render("blog/Post/Index")` **保持逻辑页面名不变**(不带 theme 前缀),theme 只作为一条 shared prop 存在。

2. **前端:inertia 的 `resolveComponent` 按 active theme 前缀查找 + 回退 default**(回退逻辑落在组件文件真正所在的 JS 侧,这才是能做存在性判断的地方):

```js
// frontend/src/inertia/resolve.js —— 目录:pages/themes/<theme>/<name>.vue
const pages = import.meta.glob('../../pages/themes/**/*.vue')
export function resolve(name, theme) {
  const active  = `../../pages/themes/${theme}/${name}.vue`
  const fallback= `../../pages/themes/default/${name}.vue`
  return (pages[active] ?? pages[fallback])()   // 缺失自动回退 default
}
```

**取舍与默认**:
- **单主题(默认,零成本)**:不启用 `ThemeMiddleware`,页面直接放 `pages/<name>.vue`,和 §3.1 完全一样。绝大多数应用停在这里即可。
- **多主题(opt-in)**:启用中间件 + `themes/<theme>/` 目录结构;得到 OpenCart 式的"整套模板集切换 + 缺失回退",但解析发生在前端,契合 inertia 的渲染模型。
- **注意**:这里的 theme 指 **OpenCart 式的整套 UI 换肤(不同 markup/组件)**;若只是明暗色(design tokens / CSS 变量),用 CSS 层解决即可,不必上这套。

> 结论:能做到 OpenCart 的 theme 切换效果,但**落点不同** —— theme 决策在服务端(一处中间件),组件解析+回退在前端。这是 inertia 客户端渲染模型下的正解,不是照搬服务端模板解析。

---

## 4. 基础设施生命周期(保留,但不再是"每 feature 一份")

**关键区分**:feature(blog/post 等)**没有**生命周期 —— 它们只是方法。但**基础设施**(db 连接池、migration、session store)有真实的启动/关闭顺序和失败回滚需求,这块**保留**当前 `internal/app` 里那套写得很好的逻辑(逆序关闭、`Start` 失败只回滚已成功前缀 —— 见 `internal/app/app.go`)。

**生命周期方法统一命名为 `Start(ctx) error` / `Stop(ctx) error`**(取代现有的 `Boot`/`Shutdown`):`Start` 打开资源(db 连接池、migration、session store),`Stop` 释放资源。命名更通用、与领域无关。可选地用一个极小的接口收口:

```go
// internal/app/lifecycle.go
package app

import "context"

// Lifecycle is the only infrastructure contract: Start acquires resources,
// Stop releases them. Features do NOT implement this — only the 2~3 infra
// services (db, session) do. Renamed from the old Boot/Shutdown.
type Lifecycle interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

当前的"伪模块化"问题在于:`app.Module` 的 `Register/Boot/Shutdown` 接口**强加给每个功能**,而 `server/routes.go` 这种纯路由 module 的 `Register` 只是挂两个路由、根本没有生命周期。本方案把两者拆开:

- **路由** → 由 §2.4 的生成代码 `MountAll(eng, svc)` 接线,不走任何接口。
- **基础设施生命周期** → 仅 db / session 这 2~3 个服务需要 `Start`/`Stop`,显式列在 serve 里。

因为服务数量固定且很少,不需要泛型 `[]Module` 迭代;显式调用即可,依赖顺序肉眼可见(正是 OpenCart `index.php` 的做法)。现有 db / session 基础设施的实现**原样保留**,只做三处重命名:类型 `Module`→`Service`、方法 `Boot`→`Start` / `Shutdown`→`Stop`、目录 `internal/module/`→`internal/service/`;不再通过 `app.Use([]Module)` 驱动。

---

## 5. 装配流程(`commands/serve.go` 重写)

```go
func runServe(cmd *cobra.Command, cfg *config.Config) error {
    log := slog.Default()

    // 1. 基础设施:构造 + 有序 Start(db 先于 session)。
    //    internal/service/db 的 db.Service、internal/service/session 的 session.Service。
    dbSvc := db.New(cfg.DB)
    if err := dbSvc.Start(cmd.Context()); err != nil {
        return fmt.Errorf("start db: %w", err)
    }
    defer func() { _ = dbSvc.Stop(context.Background()) }()

    sessSvc := session.New(cfg.Session, dbSvc)      // dbSvc 满足 session.DBProvider
    if err := sessSvc.Start(cmd.Context()); err != nil {
        return fmt.Errorf("start session: %w", err)
    }
    defer func() { _ = sessSvc.Stop(context.Background()) }()

    // 2. typed 容器(从已 Start 的句柄构造,DB 非 nil;不含 config —— 见 §2.1)。
    svc := app.NewServices(log, dbSvc.DB(), sessSvc)

    // 3. HTTP 引擎(inertia 保留)。
    eng, mode, err := server.New(cfg.Server)
    if err != nil {
        return err
    }

    // 4. 全局中间件 + 生成的路由接线。
    eng.Use(sessSvc.Middleware())  // session 中间件:所有请求
    controller.MountAll(eng, svc)  // 生成代码:非 admin area 一次接线

    // 5. admin area(需要自己的 config):校验后带 auth 接线。
    adm := admin.New(svc, cfg.Admin)
    if err := adm.Validate(); err != nil {
        return err
    }
    adm.Mount(eng)

    // 绑定监听前把重复路由注册错误暴露出来(inertia 累积,§6.4)。
    if err := eng.RegistrationError(); err != nil {
        return fmt.Errorf("route registration: %w", err)
    }

    // 6. Serve(inertia owns signals + graceful shutdown)。
    slog.Info("server starting", "addr", cfg.Server.Addr, "mode", mode)
    serveErr := eng.Serve()
    _ = eng.Close()
    return serveErr
}
```

> 相比现状:去掉了 `app.New`/`app.Use(routes, db, session, admin)` 那条 `[]Module` 链。装配从"注册 module 数组"变成"显式构造 + 生成代码接线",更贴近 OpenCart 的一处装配,且没有生命周期接口税。
> `defer Stop` 是**逆序**(session 先于 db 关闭),Go `defer` 天然保证 —— 无需手写 `shutdownN` 反向循环。若需要"`Start` 失败回滚已成功前缀 + 超时",可保留一个瘦 `app` helper 封装这段(见 §8)。

### 5.1 admin mount + 中间件(✅ 已实现)

admin 不再是特殊 Module,而是 `internal/controller/admin` 里一个 **`Admin` 控制器 + 一段 auth 中间件**。它需要自己的 `*Config`(mount/authKey/usersTable),因此**不走 `MountAll`,而在 serve.go 显式接线**(§5 第 5 步):`admin.New(svc, cfg.Admin)` → `Validate()` → `Mount(eng)`。auth 中间件只挂在受保护路由上(inertia per-route 变参 `eng.GET(path, mw, handler)`),所以**无需再按路径自过滤**;login 路由不挂 auth 即公开:

```go
// internal/controller/admin/admin.go —— Admin.Mount 方法
func (a *Admin) Mount(eng *inertia.Engine) {
    auth := a.AuthMiddleware()                 // session-based guard
    eng.GET(a.LoginPath(), a.LoginForm)        // 公开
    eng.POST(a.LoginPath(), a.LoginSubmit)     // 公开
    eng.POST(a.mount()+"/logout", auth, a.Logout)
    eng.GET(a.mount(), auth, a.Dashboard)
}
```

生成的 admin 资源(`goapp gen admin`)以 `Mount(eng, svc, adm *admin.Admin)` 接线:用 `adm.Prefix()` 建路径、`adm.AuthMiddleware()` 守卫、`adm.AddMenuItem(...)` 挂菜单,在 serve.go 里 `adm.Mount(eng)` 之后调用。`config.Admin` 的类型 `admin.Config` 定义在 controller/admin 包,`config` 导入它;controller/admin **不导入 config**(否则 config↔controller/admin 成环)。

---

## 6. 生成器(scaffold)的新角色

**只产 controller / model / view 骨架 + 路由接线,不产 Module、不产生命周期代码。**

### 6.1 输出清单变化

| 当前 | 新方案 |
|---|---|
| `internal/module/<pkg>/handler.go`(Module + Register + 6 行路由) | `internal/controller/<area>/<name>.go`(controller struct + 方法) |
| `internal/module/<pkg>/model.go` | `internal/model/<area>/<name>.go`(Model + CRUD) |
| (无) | **新增** `internal/controller/<area>/routes_gen.go`(`Mount`,生成) |
| (无) | **改动** `internal/controller/mount_gen.go`(追加 `<area>.Mount`,生成) |
| `frontend/pages/<viewdir>/*.vue` | 不变 |

### 6.2 模板清单

```
internal/scaffold/templates/
├── resource/
│   ├── controller.go.tmpl   ← 改:Controller struct + Index/New/Create/Show/Edit/Update/Delete
│   ├── routes_gen.go.tmpl   ← 新:Mount(eng *inertia.Engine, svc) 静态路由接线
│   ├── model.go.tmpl        ← 改:Model struct + CRUD(DB 字段)
│   ├── index.vue.tmpl       ← 不变
│   └── form.vue.tmpl        ← 不变
└── admin/                   ← 同构,routes_gen 前置 auth 中间件
```

### 6.3 命令(不变的对外形态)

```
goapp gen resource blog-post
  → internal/controller/blog/post.go
  → internal/controller/blog/routes_gen.go
  → internal/model/blog/post.go
  → internal/controller/mount_gen.go        (追加 blog.Mount 一行)
  → frontend/pages/blog/Post/{Index,Form}.vue
```

用户**不再需要**:写 Module struct、在 `serve.go` 里 `a.Use(...)`、手写路由表。
用户**只需要**:`goapp gen resource blog-post`,然后填 controller 方法体。路由接线由生成代码完成。

### 6.4 路由唯一性校验(已下沉到 inertia,✅ 已实现)

**根因(已核实并修复)**:inertia 的 radix tree 原本在注册重复 `(method, path)` 时**静默覆盖**上一个 handler(`router/tree.go`),而 `Engine.GET/POST` 又**忽略** `router.Add` 的返回 error —— 重复路由既不报错也不崩,运行期悄悄路由到后注册的 controller,编译器抓不到,是改路由最容易忘、最难查的 bug。

既然 inertia 是自有代码,把校验**下沉到 router 源头**比在每个 app 挂查重 shim 更根本(所有使用者自动受益,且启动即暴露)。已实现:

- **`router.go`**:新增 `var ErrDuplicateRoute`;`Router` 持 `registered map[string]struct{}`,key = `method + normalizePattern(path)`;`Add` 命中则返回 `ErrDuplicateRoute`,否则登记并插入 radix tree(tree/treenode 未改,零风险)。
- **`normalizePattern`**:把参数段 `:id`/`:slug` 归一为 `:`、通配 `*rest` 归一为 `*` —— 于是"同位置不同参数名"(`/x/:id` vs `/x/:slug`,radix 里同一节点、真冲突)会被判重;而"静态 vs 参数"(`/x/new` vs `/x/:id`,radix 静态优先、合法共存)不误判。
- **`engine.go`**:`GET/POST/...` 保持 **void 签名**(不破坏任何调用点),内部经 `addRoute` 把注册错误累积进 `regErr`;新增 `RegistrationError() error` 访问器;`Serve()` 在绑定监听**之前**先返回 `regErr` —— 重复路由让 app **优雅退出并报清 `method path`**,而非 panic。

**app 侧因此不再需要 `RouteRecorder`/`Router` 接口**(已删)。`Mount`/`MountAll` 直接收 `*inertia.Engine`。唯一性测试退化成"挂载 + 查 `RegistrationError()`",走真实注册路径:

```go
// internal/controller/routes_dup_test.go
func TestNoDuplicateRoutes(t *testing.T) {
    eng, err := inertia.New()
    if err != nil {
        t.Fatal(err)
    }
    MountAll(eng, &app.Services{})       // 只注册路由,不触发方法体
    if err := eng.RegistrationError(); err != nil {
        t.Fatalf("duplicate routes: %v", err)
    }
}
```

**可选即时层 —— 生成器静态预检**:`goapp gen` 写文件前扫描既有 `routes_gen.go` 的路由字面量查重,命中则中止生成并打印冲突两端。属"提前一步给反馈";权威判定已由 inertia 的 `ErrDuplicateRoute` + 上面的测试兜底,故此层可后置或省略。

---

## 7. 与 OpenCart 的对照

| OpenCart (PHP) | 本方案 (Go) | 差异说明 |
|---|---|---|
| `$registry` 魔术属性 | `*app.Services` typed struct | 编译期类型安全,无字符串 key |
| `$obj->$action()` 动态调用 | 生成的 `eng.GET(path, p.Method)` | 零反射,方法引用编译期可查 |
| `new $class()` 动态加载 | 生成的 `&Post{svc}` | 无运行时扫描 |
| `route=a/b/c` 解析 | 生成器把方法名映射为静态路由 | 约定在生成器,运行期无解析 |
| `Loader::model()` 动态挂载 | `&blogmodel.Model{DB: c.DB}` 直接构造 | 一行,typed |
| `catalog/` + `admin/` 双应用 | `controller/<area>/` + admin 前缀/中间件 | 同构 |
| `index.php` 装配 | `commands/serve.go` 一处装配 | 同构 |

---

## 8. 迁移:旧架构如何拆除

### 8.1 处理清单

| 文件/目录 | 处理 |
|---|---|
| `internal/app/module.go`(`Module`/`Booter`/`Shutdowner` 接口) | **删除**(feature 不再实现接口) |
| `internal/app/app.go`(`App`/`Use`/`Serve`/`shutdownN`) | **简化为可选瘦 helper**:仅封装 infra 的"有序 Start + 逆序 Stop + Start 失败回滚 + 超时";或直接用 §5 的 `defer` 版删掉 |
| `internal/app/services.go` | **新增**(§2.1) |
| `server/routes.go`(`Routes` module) | 改为 `internal/controller/home/`(或 root)controller + 生成 Mount |
| `internal/module/admin/`(作为 Module) | 重写为 `internal/controller/admin/` + `AuthMiddleware` |
| `internal/module/db/`、`internal/module/session/` | **移到 `internal/service/{db,session}` 并保留实现**;去掉 `Register`(no-op),类型 `Module`→`Service`、`Boot`→`Start`、`Shutdown`→`Stop` 重命名后由 serve 显式调用 |
| `internal/scaffold/templates/{resource,admin}/handler.go.tmpl` | 换为 `controller.go.tmpl` + `routes_gen.go.tmpl` |
| `commands/serve.go` 的 `app.Use(...)` 链 | 改为 §5 的显式装配 + `controller.MountAll` |

### 8.2 保留清单

| 组件 | 原因 |
|---|---|
| `github.com/millken/inertia`(engine/router/context/SSR) | HTTP/路由/渲染层,性能与功能都优于替换 |
| `internal/config`、`internal/buildinfo`、`internal/driver` | 与架构无关 |
| `db.Service` / `session.Service` 的核心逻辑(原 `db.Module`/`session.Module`) | 只去掉 `Register` 外壳,生命周期(原 Boot/Shutdown,改名 Start/Stop)/Store/Migration 全留 |
| `internal/scaffold/naming.go` | 命名推导仍需要 |
| `frontend/`(inertia + Vue) | 视图层不变 |

### 8.3 迁移顺序(降低风险)

1. **基础件**(✅ 已完成):
   - **inertia 路由查重下沉**(§6.4):`router.go` 加 `ErrDuplicateRoute` + 归一化去重,`engine.go` 加 `regErr`/`RegistrationError()` 且 `Serve()` 绑定前返回错误。全 `-race` 绿,既有测试无回归。
   - **`internal/app/lifecycle.go`**:`Lifecycle` 接口(`Start`/`Stop`)。
   - **注意 `services.go` 不在本步** —— 完整 `Services` 需 `*config.Config`,而当前 `config→db→app`(db 仍 `Register(a *app.App)`)会让 `app→config` 成环;此环在第 2 步 db/session 去掉 `app` 依赖后才消解,故 `services.go` 归入第 2 步。app 侧 `Router`/`RouteRecorder` 已随查重下沉删除。
2. **db/session 迁 `internal/service/` + 去 `Register`/`app` 依赖(解环)+ 重命名(`Module`→`Service`、`Boot/Shutdown`→`Start/Stop`);新增 `internal/app/services.go`(`Log`/`DB`/`Session`,不含 config)**(✅ 已完成)。
3. **`internal/controller/site`**(home + health,替代 `server/routes.go`)+ 生成的 `internal/controller/mount_gen.go`(`MountAll`,带 `gen:mounts` 标记区间)(✅ 已完成)。
4. **生成器切换**:模板产出 controller(embed `*app.Services` + `Mount`)/ model,输出到 `internal/controller/<pkg>/`;`build_test`/`resource_test`/`admin_test` 更新(✅ 已完成)。**注**:`mount_gen.go` 的自动追加暂**未实现**,生成器改为在 handler 头注释里给出"把 `<pkg>.Mount(eng, svc)` 加进 `MountAll`"的一行提示(避免测试污染真实 `mount_gen.go`);见 §10 #2。
5. **admin 改造**:`internal/controller/admin`(`Admin` + `AuthMiddleware` + login/logout/dashboard),serve 显式 `New→Validate→Mount(eng)`;端到端登录测试全部移植并通过(✅ 已完成)。
6. **删除 `internal/app/app.go`+`module.go`+`app_test.go` 与旧 `app.Use` 链、`internal/module/`、`server/routes.go`**;`serve.go` 收敛到 §5(✅ 已完成)。

> **迁移状态:全部完成。** `go build ./...`、`go vet ./...`、`go test -race ./...` 全绿;`gofmt` 干净;`go build .`(二进制)通过。inertia 改动在 `../inertia` 仓库 `feat/route-dup-check` 分支。

---

## 9. 测试清单

### 9.1 `internal/app/services_test.go`

| 测试 | 验证 |
|---|---|
| `TestNewServices_Fields` | 传入的 db/session/cfg/log 原样可取,DB 非 nil |
| `TestServices_SharedReadOnly` | 多 goroutine 并发读 `svc.DB` 无 race(`-race`) |

### 9.2 `internal/controller/blog/blog_test.go`(以生成 controller 为例)

| 测试 | 验证 |
|---|---|
| `TestPost_Index` | 构造 `&Post{svc}`,伪造 `*inertia.Context`,断言 Render 页面名 + props |
| `TestPost_Show_NotFound` | 不存在的 id → `AbortWithStatus(404)` |
| `TestPost_Create` | POST 表单 → Model.Insert 被调用 + 302 重定向 |
| `TestMount_RoutesRegistered` | `Mount(eng, svc)` 后,`eng.Lookup` 能命中全部 7 条路由 |
| `TestNoDuplicateRoutes` (§6.4) | `MountAll(eng, svc)` 后 `eng.RegistrationError()==nil` —— **CI 最终防线** |

### 9.3 `internal/controller/admin/auth_test.go`

| 测试 | 验证 |
|---|---|
| `TestAuthMiddleware_Redirects` | 未登录 → 302 到 `/admin/login` |
| `TestAuthMiddleware_Allows` | 已登录 session → 放行到下一 handler |

### 9.4 生成器 `internal/scaffold/resource_test.go`(扩充)

| 测试 | 验证 |
|---|---|
| `TestGen_EmitsController` | 产出的 `post.go` 含 7 个约定方法,可编译 |
| `TestGen_EmitsRoutes` | 产出的 `routes_gen.go` 含 7 条 `eng.GET/POST`,路径与约定表一致 |
| `TestGen_AppendsMountAll` | 二次生成不重复追加,`mount_gen.go` 幂等 |
| `TestGen_RejectsDuplicateRoute` | 生成一个与既有路由冲突的资源 → 生成器中止并报冲突两端(§6.4 即时层) |

### 9.5 集成 `commands/serve_smoke_test.go`

| 测试 | 验证 |
|---|---|
| `TestServe_FullCrudFlow` | 内存 db + stub view:GET list 200 → POST create 302 → GET show 200 → POST update 302 → POST delete 302 |
| `TestServe_AdminGuarded` | 未登录 GET `/admin/post` → 302;登录后 → 200 |

---

## 10. 已定决策(§10 原为待确认点,现拍板)

1. **infra 生命周期驱动:先用 `defer`,不预抽 helper。** §5 的纯 `defer` 版最简;基础设施只有 db/session 两个,`defer` 的逆序语义已覆盖"逆序 Stop"。**放弃**"`Start` 失败回滚已成功前缀 + 超时"这项能力——两个服务时收益不抵复杂度。**触发条件**:infra 增至 ≥4 个,或出现"启到一半失败需部分回滚"的真实场景时,再抽 `app.Bootstrap(services ...Lifecycle)`(~30 行,§4 的 `Lifecycle` 接口已备好)。
2. **`mount_gen.go` 标记注释区间(`// gen:mounts:begin/end`)已就位,但自动追加暂缓。** 落地时发现:生成器若在 `Resource()` 里改写真实 `mount_gen.go`,`build_test`(在真实模块树里生成+编译)会污染该文件、需回滚,复杂且易错。故当前生成器只产文件 + 在 handler 头注释给出"把 `<pkg>.Mount(eng, svc)` 加进 `MountAll` 标记区间"的一行提示(与旧生成器"提示用户接线"一致)。**后续**:实现标记区间的幂等重写(独立于 `build_test` 的生成路径,或加 `Options.RegisterMount`)后再自动化。
3. **Model 保持显式构造 `&blogmodel.Model{DB: c.DB}`。** 不在 `Services`/controller 上加 `c.Model(...)` 泛型糖——显式构造已是一行、typed、可跳转,泛型糖只会把清晰的构造换成一层间接。
4. **Redirect 用 stdlib(已核实 `*inertia.Context` 无 `Redirect`)。** 统一 `http.Redirect(ctx.Writer, ctx.Request, url, http.StatusSeeOther)`;POST→GET 用 **303 See Other**(也是 Inertia 协议期望的重定向码)。不新增 helper——stdlib 一行足够,且不引入自定义响应抽象。
5. **area = Go 包名,`mount_gen.go` 用 import 别名消歧。** `internal/controller/blog` 包名 `blog`;跨 area 同名(`blog.Mount` 与 `admin.Mount`)在 `mount_gen.go` 里靠包路径 + import 别名区分(§2.4 已示范)。
6. **非 CRUD 路由(`/`、`/api/health`)归入 `controller/site` area。** 与 CRUD area 同构,`site` 的 `routes_gen.go`(或标记区间外的手写行)登记这几条口子;`server/routes.go` 的内容迁移到此,随后删除。

7. **路由唯一性:下沉到 inertia,error 而非 panic(✅ 已实现,§6.4)。** `router.Add` 返回 `ErrDuplicateRoute`(归一化 key 捕获同位置参数名冲突);`Engine` 累积进 `regErr`,`GET/POST` 保持 void 签名(零调用点破坏),`Serve()` 绑定前返回错误、`RegistrationError()` 供显式检查。app 侧 `RouteRecorder`/`Router` 接口已删,`Mount` 直收 `*inertia.Engine`,测试退化为"挂载 + 查 `RegistrationError()`"。选 error 而非 panic 是为了让 app 优雅退出。

8. **基础设施命名:类型 `Module`→`Service`,目录 `internal/module/`→`internal/service/`,不提出 `internal/`。** `session.Instance` 会与 per-request 的 `Session` 类型混淆,`Service` 无歧义且与 `app.Services`/`Lifecycle` 词汇一致。保留在 `internal/` 因为这是模板/应用内部实现而非对外库,且与同在 `internal/` 的 `controller`/`model`/`config` 布局一致。

> 以上决策已回填到正文(§2.1/§2.2/§2.4/§4/§5/§6.4/§8)。本文档据此可作为实现蓝本。
