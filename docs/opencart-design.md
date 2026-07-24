# OpenCart 式架构 — 简化 MVP

> 状态:**Design / 待实现** · 目标文件:`docs/opencart-design.md`
> 一句话:在现有 `inertia.Engine` 之上,加一层 **Registry(全局对象)+ 约定路由 MVC + Loader**,让"加一个功能"退化成"写一个 controller 方法 + main 里一行注册"。

---

## 1. 目标与非目标

### 1.1 要解决的痛点(现状)

当前每个 resource 都是一个 `app.Module`:`New(dbMod)` → `Register(a *app.App)` 里手写 6 条 `eng.GET/POST` → `a.Use(...)` → 装配顺序/`Boot`/`Shutdown` 全靠人肉保证(见 [commands/serve.go](../commands/serve.go)、[internal/scaffold/templates/resource/handler.go.tmpl](../internal/scaffold/templates/resource/handler.go.tmpl))。OpenCart 里同样的 CRUD 只是一个 3 行的 controller 方法。我们要的就是那种体感。

### 1.2 MVP 只做四件事

1. **Registry** —— 全局对象容器,db/session/config/logger 统一 `Set`/`Get`,controller 一行取用。
2. **约定路由 MVC** —— URL `/area/controller/action` 按约定自动映射到 controller 方法,**不写路由表**。
3. **Loader** —— 按路由键实例化 controller/model,依赖通过 Registry 注入。
4. **零 Module** —— 去掉 `app.Module`/`Boot`/`Shutdown` 接口;基础设施服务在 `main` 里直接构造、启动、关闭。

### 1.3 非目标(显式砍掉)

- ❌ 自建 HTTP server / 替换 `inertia.Engine`(继续用 inertia 做 HTTP/中间件/SSR/静态)。
- ❌ 动词感知路由(POST→Create 之类)。OpenCart 用 **action 名** 区分增删改查,不用动词;我们照做。
- ❌ mount 子引擎 / 事件系统 / 插件市场。留给以后。
- ❌ 路径里的 id 段(`/x/edit/42`)。MVP 的 id 走 query(`?id=42`),URL 严格三段 `/area/controller/action`。

---

## 2. 三个核心决策

| 决策 | 选择 | 理由 |
|------|------|------|
| HTTP 基座 | **保留 `inertia.Engine`** | HTTP/中间件/SSR/静态都已跑通;新 engine 只是在它上面注册路由的适配层。 |
| `app.Module` | **删除** | 纯 OpenCart 风格:服务在 `main` 构造并装入 Registry;controller/resource 是纯 struct。需要清理(关连接池)的服务各自暴露 `Close()`。 |
| 路由形式 | **路径约定 `/area/controller/action`** | 现代且 SEO 友好;同时天然兼容 `?route=` 语义。 |

### OpenCart → 本方案对照

| OpenCart (PHP) | 本方案 (Go) | 说明 |
|----------------|-------------|------|
| `index.php` + 全局 `$registry` | `main` + `engine.Registry` | 全局容器,装 db/session/config/logger |
| `Front::dispatch(action)` | 启动时按方法名**自动注册** inertia 路由 | 不写路由表;见 §4.3 |
| `Controller` 基类 + `$this->load->model/view` | `engine.Controller` 基类 + `c.Model(...)` | 嵌入即得全局对象与加载器 |
| `catalog/controller/x/y.php` | `controller/<area>/<name>.go` | 文件路径 ≈ 路由键 |
| `?route=a/b/c` | `/a/b/c` | action 缺省 `Index`;id 走 `?id=` |
| `new $class()` 动态实例化 | `RegisterController(route, ctor)` 显式工厂 | Go 无运行时包扫描的妥协 |
| `index/form/add/edit/delete` 方法 | 同名 exported 方法即 action | 约定见 §6 |

---

## 3. 架构总览

```
┌────────────────────────────────────────────────────────────┐
│ main / cobra serve(装配点,对应 OpenCart index.php)         │
│   1. 构造并启动服务: db.Start() → reg.Set("db", ...)        │
│   2. server.New(cfg)        → inertia.Engine(HTTP/SSR/静态) │
│   3. engine.New(httpEng, reg) → 适配层                      │
│   4. blog.Register(e); admin.Register(e)  ← 每个包一行      │
│   5. httpEng.Serve() → 退出后 main 关闭服务                 │
└────────────────────────────────────────────────────────────┘
                              │
┌────────────────────────────────────────────────────────────┐
│ engine.Engine(适配层,薄)                                   │
│   Reg  *Registry        // 全局对象                         │
│   http *inertia.Engine  // 底层 HTTP                        │
│   RegisterController(route, ctor):                          │
│     反射枚举 ctor() 的 exported 无参方法 →                   │
│     在 inertia 上注册 /route 与 /route/method 路由           │
└────────────────────────────────────────────────────────────┘
                              │  (请求来时,inertia 照常走中间件链)
                              ▼
┌────────────────────────────────────────────────────────────┐
│ controller/admin/user.go                                    │
│   type User struct { engine.Controller }                   │
│   func (c *User) Index() { c.Render("Admin/User/Index") }  │
│   func (c *User) Edit()  { id := c.Ctx().Query("id"); … }  │
└────────────────────────────────────────────────────────────┘
```

**请求生命周期**(inertia 收到 `/admin/user/edit?id=42`):

1. `inertia.Engine` 走全局中间件链(session 加载、admin auth、recovery/gzip…)。
2. 命中启动时注册的路由 `GET /admin/user/edit` → 派发 handler。
3. handler:`ctor()` 新建 controller 实例 → 注入 Registry 与本次 `*inertia.Context` → 调用缓存的 `Edit` 方法。
4. 方法内:`c.DB()` 取 db、`c.Model("admin/user")` 取 model、`c.Render(...)` 写响应。

> controller **每请求新建**(同 OpenCart `new Controller()`),无共享可变状态 → 并发安全。per-request 的 `*inertia.Context` 只挂在本次实例上,**绝不**写进全局 Registry(这是旧设计稿里的并发坑,本方案规避)。

---

## 4. 核心组件

### 4.1 Registry —— 全局对象容器

```go
// internal/engine/registry.go
package engine

// Registry 是全局服务容器,对应 OpenCart 的 $registry。
// 装配期(main,单线程)写;服务期(多请求)只读。Go 的 map 并发读安全,
// 故无需加锁——前提是 Serve 之后再无人 Set。
type Registry struct{ items map[string]any }

func NewRegistry() *Registry { return &Registry{items: make(map[string]any)} }

// Set 注册服务,只在装配期调用。同名覆盖(后注册胜,支持覆盖默认实现)。
func (r *Registry) Set(key string, v any) { r.items[key] = v }

// Get 取出服务,缺失返回 nil。调用方负责类型断言。
func (r *Registry) Get(key string) any { return r.items[key] }

// MustGet 缺失即 panic,用于装配期 fail-fast。
func (r *Registry) MustGet(key string) any {
    v, ok := r.items[key]
    if !ok { panic("engine: registry key not set: " + key) }
    return v
}
```

约定 key 用常量集中,杜绝拼写错误:

```go
// internal/engine/keys.go
const (
    KeyDB      = "db"       // *sqldb.DB
    KeySession = "session"  // session.Provider(工厂,非 per-request session)
    KeyConfig  = "config"   // *config.Config
    KeyLogger  = "logger"   // *slog.Logger
)
```

### 4.2 Controller 基类 —— 全局对象的便捷入口

```go
// internal/engine/controller.go
package engine

import (
    "net/http"
    "github.com/dnsoa/go/sqldb"
    "github.com/millken/inertia"
)

// Controller 是所有 controller 嵌入的基类,对应 OpenCart 的 Controller 基类。
// 嵌入方式: type User struct { engine.Controller }
type Controller struct {
    Reg  *Registry
    Load *Loader
    ctx  *inertia.Context // per-request,由 dispatcher 注入,不导出
}

// bind 由 dispatcher 在调用方法前设置本次请求上下文。
// (嵌入为值类型时,*Receiver 上的指针方法会作用于被嵌入字段,故可改 ctx。)
func (c *Controller) bind(ctx *inertia.Context) { c.ctx = ctx }

// —— 全局对象便捷取用 ——
func (c *Controller) Ctx() *inertia.Context { return c.ctx }
func (c *Controller) DB() *sqldb.DB {
    if v := c.Reg.Get(KeyDB); v != nil { return v.(*sqldb.DB) }
    return nil
}
func (c *Controller) Logger() *slog.Logger {
    if l, ok := c.Reg.Get(KeyLogger).(*slog.Logger); ok { return l }
    return slog.Default()
}

// Model 是 c.Load.Model(route) 的快捷方式。
func (c *Controller) Model(route string) any { return c.Load.MustModel(route) }

// —— 响应便捷方法(inertia 无 Redirect,这里补上)——
func (c *Controller) Render(view string) error { return c.ctx.Render(view) }
func (c *Controller) JSON(v any) error         { return c.ctx.JSON(v) }
func (c *Controller) Redirect(url string, code int) {
    c.ctx.Header("Location", url)
    c.ctx.Status(code)
}
```

### 4.3 Loader + 启动时自动注册路由(关键)

核心思路:**不**在请求期解析路径,而是在 `RegisterController` 时**反射枚举** controller 的 exported 无参方法,给每个方法在 inertia 上注册一条显式路由。这样:

- 没有运行时 catch-all,与生产环境的 `StaticFS("/*")` **零冲突**(字面路由优先于 `/*`,已被现有 `/`、`/api/health` 与静态共存佐证)。
- 404 由 inertia 自动处理(未注册即 404)。
- 请求期零路径解析;方法 handle 在注册时缓存。

```go
// internal/engine/loader.go
package engine

type Loader struct {
    reg    *Registry
    http   *inertia.Engine
    ctrls  map[string]func() any // route → 工厂
    models map[string]func() any // route → 工厂
}

func NewLoader(http *inertia.Engine, reg *Registry) *Loader {
    return &Loader{
        http: http, reg: reg,
        ctrls: make(map[string]func() any), models: make(map[string]func() any),
    }
}

// RegisterController 把 route(如 "admin/user")接到一个 controller 工厂。
// 工厂返回的 *T 必须嵌入 engine.Controller。
//
// 副作用:反射 *T 的 exported 无参方法,为每个方法注册路由:
//   Index  → GET/POST /admin/user        (也注册 /admin/user/index)
//   Edit   → GET/POST /admin/user/edit
//   Add    → GET/POST /admin/user/add
//   ...
func (l *Loader) RegisterController(route string, ctor func() any) {
    l.ctrls[route] = ctor
    t := reflect.TypeOf(ctor()) // 一次反射,拿类型
    for i := 0; i < t.NumMethod(); i++ {
        m := t.Method(i)
        if !isAction(m) { continue } // 只收 exported 无参无返回方法
        l.registerAction(route, m.Name, ctor)
    }
}

// isAction:方法名大写开头、零参、零返回 → 视为 action。
// 由此推出一条简单规则:不想被路由的辅助方法,加参数或改成未导出。
func isAction(m reflect.Method) bool { /* PkgPath=="" && Type.NumIn()==1 && NumOut()==0 */ }

// registerAction:为 action 名注册 /route 或 /route/action,handler 闭包缓存方法 handle。
func (l *Loader) registerAction(route, name string, ctor func() any) {
    path := "/" + route
    if name != "Index" { path = "/" + route + "/" + strings.ToLower(name) }
    h := l.buildHandler(name, ctor)            // 闭包内缓存 reflect method value
    l.http.GET(path, h)                        // OpenCart 风格:动词不参与契约,
    l.http.POST(path, h)                       // GET/POST 同方法,由方法自决行为
    if name == "Index" { l.http.GET("/"+route+"/index", h); l.http.POST("/"+route+"/index", h) }
}

// buildHandler:每请求新建实例 → 注入 Reg/Load/ctx → 调缓存方法。
func (l *Loader) buildHandler(name string, ctor func() any) inertia.HandlerFunc {
    return func(c *inertia.Context) {
        inst := ctor()
        setBase(inst, l.reg, l, c)             // 设 Reg/Load + bind(ctx)
        callAction(inst, name)                 // 反射调方法(可缓存 method value 优化)
    }
}

// RegisterModel 注册 model 工厂;依赖在工厂闭包里从 Registry 取,零反射。
func (l *Loader) RegisterModel(route string, ctor func() any) { l.models[route] = ctor }
func (l *Loader) MustModel(route string) any { /* models[route]() */ }
```

> **唯一不可避免的反射**:Go 没有"按字符串调方法"。我们只在①注册时枚举方法、②请求时按缓存 handle 调用这两处用 reflect,且②可优化为预编译的类型化 adapter。相对 HTTP IO,开销可忽略。

### 4.4 Engine —— 适配层

```go
// internal/engine/engine.go
package engine

import "github.com/millken/inertia"

// Engine 把 Registry + Loader 挂到一个现成的 inertia.Engine 上。
// 它本身不监听端口、不做信号处理——那是 inertia.Serve 的事。
type Engine struct {
    Reg  *Registry
    Load *Loader
    http *inertia.Engine
}

func New(http *inertia.Engine, reg *Registry) *Engine {
    return &Engine{Reg: reg, Load: NewLoader(http, reg), http: http}
}

// Use 透传全局中间件给 inertia(session、recovery、gzip、admin auth…都用它)。
func (e *Engine) Use(mw ...inertia.HandlerFunc) { e.http.Use(mw...) }

// RegisterController / RegisterModel 透传给 Loader。
func (e *Engine) RegisterController(route string, ctor func() any) { e.Load.RegisterController(route, ctor) }
func (e *Engine) RegisterModel(route string, ctor func() any)     { e.Load.RegisterModel(route, ctor) }
```

---

## 5. 路由约定

URL 严格三段,**id 走 query**,**动词不参与路由**:

| URL | controller route | 方法 | 说明 |
|-----|------------------|------|------|
| `/admin/user` | `admin/user` | `Index` | 列表(action 缺省) |
| `/admin/user/index` | `admin/user` | `Index` | 同上,显式 |
| `/admin/user/form` | `admin/user` | `Form` | 新建/编辑表单(`?id=` 区分) |
| `/admin/user/add` | `admin/user` | `Add` | 创建(POST) |
| `/admin/user/edit` | `admin/user` | `Edit` | 更新(POST,`?id=42`) |
| `/admin/user/delete` | `admin/user` | `Delete` | 删除(POST,`?id=42`) |

规则一句话:**`<area>/<controller>/<action>`,`action` 缺省为 `Index`;id 与其它参数一律 `c.Ctx().Query(...)`**。方法名沿用 OpenCart 习惯(`Index/Form/Add/Edit/Delete`),也可自定义任意 exported 无参方法。

---

## 6. 写法(用户视角)

### 6.1 Controller

```go
// controller/admin/user.go
package admin

import (
    "net/http"
    "myapp/controller/admin/model"   // model 包
    "myapp/internal/engine"
)

type User struct{ engine.Controller }

// GET /admin/user
func (c *User) Index() {
    m := c.Model("admin/user").(*model.User)
    items, _ := m.All(c.Ctx().Request.Context())
    c.Ctx().Set("items", items)
    _ = c.Render("Admin/User/Index")
}

// GET/POST /admin/user/edit?id=42
func (c *User) Edit() {
    ctx := c.Ctx()
    id := ctx.Query("id")
    m := c.Model("admin/user").(*model.User)
    if ctx.Request.Method == http.MethodPost {
        u := bind(ctx)            // 从 PostForm 绑定
        _ = m.Update(ctx.Request.Context(), id, u)
        c.Redirect("/admin/user", http.StatusFound)
        return
    }
    u, _ := m.Get(ctx.Request.Context(), id)
    ctx.Set("item", u)
    _ = c.Render("Admin/User/Form")
}
```

**对比现状**:没有 `Module` struct、没有 `New(db)`、没有 6 条 `eng.GET/POST`、没有 `Register(a *app.App)`。**只有方法**。

### 6.2 Model

```go
// controller/admin/model/user.go
package model

import (
    "context"
    "github.com/dnsoa/go/sqldb"
)

type User struct {
    ID    int64  `db:"id"`
    Name  string `db:"name"`
    Email string `db:"email"`
}

// Model 的 db 由工厂闭包从 Registry 注入(见 §6.3),构造函数零参数。
type UserModel struct{ DB *sqldb.DB }

func (m *UserModel) All(ctx context.Context) ([]User, error) {
    var us []User
    err := m.DB.Table("users").ScanContext(ctx, &us)
    return us, err
}
// Get / Update / Insert / Delete … 同构
```

### 6.3 每个业务包一行注册

```go
// controller/admin/register.go
package admin

import (
    "myapp/controller/admin/model"
    "myapp/internal/engine"
)

func Register(e *engine.Engine) {
    e.RegisterController("admin/user", func() any { return &User{} })
    e.RegisterModel("admin/user", func() any {
        return &model.UserModel{DB: e.Reg.Get(engine.KeyDB).(*sqldb.DB)}
    })
}
```

### 6.4 main(装配点)

```go
// commands/serve.go(简化后)
func runServe(ctx context.Context, cfg *config.Config) error {
    reg := engine.NewRegistry()
    reg.Set(engine.KeyConfig, cfg)
    reg.Set(engine.KeyLogger, slog.Default())

    // 服务在 main 构造并启动;无 Module/Boot。
    dbSvc := db.New(cfg.DB)
    if err := dbSvc.Start(ctx); err != nil { return err }   // open + migrate + ping
    defer dbSvc.Close()
    reg.Set(engine.KeyDB, dbSvc.DB())

    sess := session.New(cfg.Session, dbSvc)                 // 启动逻辑内联或自启
    reg.Set(engine.KeySession, sess)

    // inertia 作为底层 HTTP/SSR/静态。
    httpEng, _, err := server.New(cfg.Server)
    if err != nil { return err }
    e := engine.New(httpEng, reg)

    // 全局中间件。
    httpEng.Use(recoveryMW, gzipMW, session.Middleware(sess), admin.AuthMiddleware(reg))

    // 每个业务包一行注册(替代 a.Use(...))。
    server.RegisterSampleRoutes(httpEng)                    // /、/api/health 等纯路由
    admin.Register(e)
    blog.Register(e)

    slog.Info("listening", "addr", cfg.Server.Addr)
    if err := httpEng.Serve(); err != nil { return err }    // inertia 管信号 + 优雅关停
    return nil                                              // defer 关闭服务
}
```

> 唯一的 Go 妥协:没有运行时包扫描,必须显式 `import + Register(e)`。但相比现状每个 resource 一个 `app.Module + a.Use`,这里是**每个包一行**,且不涉及任何生命周期接口。

---

## 7. 扩展:加一个资源

**手写(3 步):**
1. `controller/<area>/<name>.go` —— struct 嵌入 `engine.Controller` + 几个方法。
2. `controller/<area>/model/<name>.go` —— Model struct + CRUD 方法。
3. 该包 `Register(e)` + `main` 加一行 `xxx.Register(e)`。

**或一键生成(生成器只产 controller/model/register/view 骨架,不产 Module、不产路由表):**

```
goapp gen resource blog-post
  → controller/blog/post.go
  → controller/blog/model/post.go
  → controller/blog/register.go   (追加一行)
  → frontend/pages/blog-post/{index,form}.vue
```

生成器模板变化:[resource/handler.go.tmpl](../internal/scaffold/templates/resource/handler.go.tmpl)(Module)→ 新 `controller.go.tmpl`(Controller struct + 方法);新增 `register.go.tmpl`。

---

## 8. admin 如何融入

admin 不再是"特殊 Module",而是 **area=admin + 一个全局 auth 中间件 + 一组同构 controller**:

```go
// controller/admin/auth.go
func AuthMiddleware(reg *engine.Registry) inertia.HandlerFunc {
    return func(c *inertia.Context) {
        if !strings.HasPrefix(c.Request.URL.Path, "/admin") { c.Next(); return } // 非 admin 放行
        if isLoginPage(c.Request.URL.Path) { c.Next(); return }                  // 登录页放行
        if !loggedIn(c, reg) { c.Redirect("/admin/login", http.StatusFound); return }
        c.Next()
    }
}
```

> inertia 没有 prefix-group 中间件,故 auth 中间件用**路径前缀**自判——与现状 [admin.go](../internal/module/admin/admin.go) 的做法一致。`/admin/user` 与 `/admin/auth` 之类 controller 和 `blog.Post` **完全同构**,只是挂在 `/admin` 下、被 auth 包裹。

---

## 9. 迁移:从现状到 MVP

### 9.1 删除

| 路径 | 处理 |
|------|------|
| [internal/app/](../internal/app/)(`App`/`Module`/`Booter`/`Shutdowner`) | **删除** |
| `internal/module/<pkg>/handler.go`(每个 resource 的 Module) | 改写为 `controller/<area>/<name>.go` |
| [commands/serve.go](../commands/serve.go) 里的 `a.Use(...)` 链 | 改为 `xxx.Register(e)` |

### 9.2 保留(只改外壳)

| 路径 | 处理 |
|------|------|
| [internal/module/db/](../internal/module/db/) 实现逻辑 | 保留;`New`+`Boot` 合并为 `New`+`Start`,`DB()` 不变,加 `Close()` |
| [internal/module/session/](../internal/module/session/) 实现 | 保留;middleware 改为导出的 `Middleware(sess)` 供 main `Use` |
| [internal/config/](../internal/config/)、[internal/buildinfo/](../internal/buildinfo/)、[internal/driver/](../internal/driver/) | 不动 |
| [server/](../server/) 的 inertia 装配 | 保留;`server.Routes` 改为普通 `RegisterSampleRoutes(httpEng)` |
| [frontend/](../frontend/)(Inertia+Vue) | 不动 |
| [internal/scaffold/naming.go](../internal/scaffold/naming.go) | 保留,继续做命名推导 |

### 9.3 顺序(降风险)

1. 新建 `internal/engine/`(Registry/Controller/Loader/Engine),不碰旧码,独立单测过。
2. db/session 暴露 `Start`/`Close` 与导出 middleware,旧 Module 接口暂留过渡。
3. 写一个新 controller(如 `admin/user`)走新链路,与旧码并存,跑通全流程。
4. admin 改造为 area+auth 中间件,替换旧 admin Module。
5. 生成器换新模板(controller/model/register)。
6. 删 `internal/app/` 与旧 Module 外壳,改 `commands/serve.go`。

---

## 10. 待验证风险点(写码前确认)

1. **路由器优先级**:`StaticFS("/")` 注册 `GET /*`,而 controller 路由是字面路径 `/admin/user`。需确认 inertia radix **字面优先于 `/*`**(现有 `/`、`/api/health` 与静态共存已间接佐证,但仍需对 controller 路径实测)。
2. **action 识别规则**:`isAction` 判定 exported 无参无返回方法。需确认嵌入 `engine.Controller` 带来的提升方法(`Ctx`/`DB`/`Render` 等)**不会**被误注册为路由——枚举时应排除 `engine.Controller` 自身的方法集。
3. **per-request 注入**:`setBase` 通过反射设被嵌入 `Controller` 字段;或要求工厂返回的实例其基类 `Reg/Load` 已就位、dispatcher 只 `bind(ctx)`。二选一,实现时定。
4. **inertia 无 Group 中间件**:admin auth 用路径前缀自判(同现状),MVP 接受;若后续要严格隔离,等 inertia 加 Group 支持。
5. **反射性能**:注册时枚举 + 请求时缓存 handle 调用。先按此实现,若压测显示热点再预编译类型化 adapter。

---

## 附:与旧设计稿的差异

本稿相对 `docs/design/opencart-architecture.md` 和本文件旧版的**简化**:

- 砍掉运行时 catch-all 路径解析 → 改为**启动时按方法名自动注册**(规避与 `StaticFS("/*")` 的冲突,且请求期零解析)。
- 砍掉动词感知路由、mount 子引擎、事件系统、`GetAs[T]` 泛型(用 `Get`+断言)。
- 砍掉 per-request 写 Registry(并发坑)→ per-request 对象挂 controller 实例。
- id 走 query,URL 严格三段,匹配用户选定的 `/area/controller/action` 模型。
