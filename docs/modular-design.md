# goapp-template 模块化设计文档

> 状态:v5(已落地 phase 1–4:薄内核 + db + session + 完整 admin 框架 + `internal/scaffold` 生成器)· 适用范围:单个 `goapp-template`,主要自用 · 目标:让 db / session / admin 等 feature 成为**可组合的 Module**,按需启用;资源脚手架由 `internal/scaffold` 生成。

---

## 1. 背景与目标

### 现状
- `main.go` → 仅 `signal.NotifyContext`(补 SIGTERM)+ `root.ExecuteContext(ctx)`。
- `commands/` → `root.go`(AppInit:加载 .env/config/日志,**经 cobra context 传递 cfg**)、`serve.go`(runServer → `eng.Serve()` + `eng.Close()`;dev-addr 的 env 覆盖**散落在此**)、`paths.go`、`version.go`。
- `server/` → `server.go`(装配 inertia.Engine;`ModeName` 与 `New` **重复推导 mode**)、`routes.go`(2 条业务路由)、`mode_dev.go`/`mode_prod.go`(build-tag 的 defaultMode + staticFS + loadSSRBundle;**SSR bundle 文件名两处硬编码**)。
- `internal/config/` → `Config{Log,Server}` + `Load`。
- `frontend/` → Vue + Vite + SSR(已迁移到 Vite)。
- 依赖 `github.com/millken/inertia`(框架:engine/ssr/router/middleware)。

骨架(bootstrap/config/lifecycle)和业务(routes)目前混在 `commands`+`server` 里,feature(db/session/admin)还没有位置。上述冗余在阶段 1 一并清掉(见 §7)。

> **composition root 是 `commands`**:cobra 保留,yaml 解码与模块构造都发生在 `commands`(AppInit / serve RunE)。`main.go` 只建 signal ctx + 跑 cobra。

### 目标
- **单个模板**:不拆多 app、不做 monorepo、不建独立 foundations 仓库(自用,YAGNI)。
- **feature 可组合**:db/session/admin/mvc 等是 Module,`commands` 里 `app.Use(...)` 启用;不需要的 feature 不构造即可。
- **一处骨架,多项目复用**:新项目 = `cp -r` 模板 + 裁剪;基础更新发生在一处。
- **未来可 lift**:等真的有 2+ 项目共用 base、手动 port 痛了,再把 `internal/app` + `internal/module/*` 整体抽成 `goapp-foundations`。

### 非目标
- 不做多 app monorepo / 对外 starter 家族。
- DB 访问与迁移已定(sqldb);session 后端仍待定但留可插拔。
- 不引入 DI 容器框架(fx/dig)。

---

## 2. 设计原则

1. **依赖方向单一,且无环**:`commands → app 内核 / server(装配)→ module → inertia`。**关键不变式:`app` 内核不 import `config`** —— 因此即便将来 `config` 引用了某个 module(`config→db`),经 `db→app→inertia` 也回不来,**环在结构上不可能形成**(不靠"组合 Config 放哪"这种约定)。
2. **构造期注入,运行期取用**:Module 在 `commands` 构造时注入依赖(显式、可测);资源在 `Boot` 阶段创建;provider 只在 Boot 后有效。
3. **App 内核只管"生命周期 + 组合"**:它包裹一个**已装配好的** `*inertia.Engine`,提供 Module 注册 / Boot / Serve / Shutdown。**engine 装配(模式/SSR/staticFS/rootHTML/中间件/错误处理)留在 `server.New`(template 侧)**,因为它们耦合 embed/构建模式,属部署关切。**db/session 等业务服务不属于 App**,由 Module 暴露;**模块不读 App 的配置**(配置走构造注入)。
4. **约定优于配置(MVC)**,但可覆盖。
5. **演进式 / 不预抽**:Module 接口先做最小集(`Register` + 按需的 `Boot`/`Shutdown`,类似 `http.Pusher` 的接口断言)。**不预设用不到的抽象**——逃生门 Option、`Provider` 接口、辅助子包、把装配搬进内核,等真实需求(第二个消费者、第一个单测、真要抽 foundations)出现再加,不为对称性预抽。

---

## 3. 总体架构

```
                ┌─────────────────────────────────────────────┐
                │  commands (serve RunE) —— composition root   │
                │  server.New(cfg.Server) → eng                │
                │  app.New(eng) → a → a.Use(mods…) → a.Serve() │
                └─────┬───────────────────────┬─────────────────┘
        cfg.Server   │                         │ mods(server.Routes(), db, session, admin …)
                ┌─────▼──────────┐    ┌────────▼─────────┐
                │  server (装配)  │    │  internal/app(薄内核)│
                │  · server.New   │    │  · 包裹 *inertia.Engine │
                │    建 engine    │    │  · Module 注册 + lifecycle│
                │    (mode/SSR/   │    │  · Serve(ctx)→Boot→    │
                │     staticFS/   │    │    eng.Serve→Shutdown  │
                │     rootHTML/   │    │  · 只 import inertia    │
                │     middleware) │    └────────┬─────────────┘
                │  · server.Routes│             │ import
                │    (app.Module) │     ┌───────▼────────┐
                └────┬────────────┘     │ inertia (框架)   │
        import config│                  │ engine/ssr/...  │
                ┌─────▼──────────┐      └─────────────────┘
                │ internal/config │   ┌────────────────────┐
                │  Config{Log,    │   │ internal/module/*   │
                │   Server} +Load │   │ db session admin mvc│
                └─────────────────┘   └─────────┬──────────┘
                                                │ provider 接口互相依赖(*sqldb.DB / Store …)
```

**关键分工**:
- **`internal/app`** 是薄内核,只 import `inertia`。它不建 engine、不读 config —— 接收一个已装配好的 `*inertia.Engine`,负责 Module 注册与 Boot/Serve/Shutdown 生命周期。
- **`server.New(cfg)`** 负责 engine 装配(mode/staticFS/rootHTML/SSR/错误处理/中间件/StaticFS),耦合 embed 与构建模式 —— 留在 template 侧不动。
- **`internal/config`** phase 1 **完全不动**(`Config{Log,Server}` + `Load` 保持现状)。因为 app 不 import config,phase 2 往 config 加 db 段也不会成环(§4.4)。

**同步语义**:基础更新都在模板里发生;copy 出去的老项目手动 port。抽 foundations 是后续机械动作。

---

## 4. 核心契约

### 4.1 App 内核(`internal/app/app.go`)—— 薄内核

App 只承担**Module 注册 + 生命周期**,包裹一个外部装配好的 engine。不持有业务服务,不读配置。

```go
package app   // 只 import inertia(+ stdlib),不 import config

type App struct {
    Engine *inertia.Engine      // 外部(server.New)装配好传入;模块用它注册路由/中间件
    Logger *slog.Logger

    mods          []Module
    life          []lifecycle   // 每个模块的 boot/shutdown 钩子(按需)
    shutdownTimeout time.Duration
}

// New 包裹一个已装配好的 engine。engine 的创建由 server.New 完成(template 侧),
// 不在 app 内 —— 这样 app 无需 import config,环不可能形成。
// opts 当前只有 WithShutdownTimeout。日后写单测需要跳过装配时,再加 WithEngine 逃生门(现在不预设)。
func New(eng *inertia.Engine, opts ...Option) (*App, error)

// Use 依次调用每个 Module.Register(挂路由/中间件),收集 Booter/Shutdowner 钩子。
// Register 失败:直接返回错误(此时进程尚未 Serve,挂上的路由不会被访问,
// engine 也无反注册 API,故不做"回滚"——见 §4.5)。
func (a *App) Use(mods ...Module) error

// Serve(ctx):
//   ctx 来自 cmd.Context()(main 建的 signal ctx,经 cobra 透传,须含 SIGINT+SIGTERM),
//   用于 Boot 阶段(慢速 DB 连接期间 Ctrl-C 可中断)。HTTP 服务阶段由 eng.Serve() 自带信号处理;
//   退出后 app 自造一个带 shutdownTimeout 的 ctx 跑 Shutdown 钩子,再 eng.Close()。
func (a *App) Serve(ctx context.Context) error

// ModeName 仅当日后 app 持有 mode 时提供;phase 1 模式名仍由 server 侧给出(见 §7)。
```

> 与 v3 的差别:v3 让 `app.New(*config.ServerConfig)` 自建 engine,导致 app import config → 才有 cycle → 才需把组合 Config 挪到 commands + 重构 config。v4 让 app 接收已装配的 engine,**不 import config**,cycle 在结构上消失,config 一行不用改。

### 4.2 Module 接口(`internal/app/module.go`)

```go
package app

// Module 是唯一必须实现的契约:把路由/中间件挂到 App 上。Register 不得做 IO。
type Module interface {
    Register(a *App) error
}

// 可选 lifecycle 钩子(接口断言,按需实现,类似 http.Pusher):
type Booter interface {
    Module
    Boot(ctx context.Context) error        // Serve 前执行:开连接池、连 DB/Redis、跑迁移…
}
type Shutdowner interface {
    Module
    Shutdown(ctx context.Context) error    // Serve 退出后逆序执行:关连接池…
}
```

- `Register`:**纯装配** —— 注册路由、挂中间件。不做 IO(便于快速失败 + 测试)。**约定:模块不读 `a` 上的配置**(模块配置经构造注入);`a` 只提供 `Engine`/`Logger`/中间件注册面。(若想强约束,未来可把 Register 收窄成 `Register(r Registrar)` 接口,仅暴露路由/中间件/Logger。)
- `Boot`:**有副作用初始化** —— 连接 DB、跑迁移。失败则 `Serve` 不启动。
- `Shutdown`:逆序释放。

**顺序契约**:Boot 按 `Use` 注册顺序;Shutdown 逆序。`commands` 里**先 Use 的先 Boot**——把 `db` 排在依赖它的 `admin` 之前即可,无需拓扑排序。

**中间件顺序保证**(已核实 `engine.go:327`):中间件在**请求时**才与 handler 合成链,所以"路由先注册、中间件后 Use"也能生效;中间件执行顺序 = `eng.Use` 调用顺序 = 模块注册顺序(`server.New` 里的全局中间件最先,之后按 `app.Use` 序)。这条对 `session`(中间件)+ `admin`(依赖 session)的组合至关重要:把 `session` 排在 `admin` 前,其中间件就先于 admin 的 handler 包裹请求。

### 4.3 依赖注入:provider 接口 + 构造注入

Module 之间**不通过 App 传业务服务**,而是在 `commands` 构造时注入对方,**依赖小型 provider 接口**(accept interfaces)。资源在 `Boot` 后才可用,所以 handler 里**延迟取用**。

```go
// db 模块暴露 *sqldb.DB
package db
import "github.com/dnsoa/go/sqldb"
type Provider interface { DB() *sqldb.DB }
type Module struct{ cfg *Config; db *sqldb.DB }       // cfg 为指针:commands 传入(可能 nil,Boot 时校验)
func New(cfg *Config) *Module { return &Module{cfg: cfg} }
func (m *Module) Register(a *app.App) error        { return nil }   // db 无路由
func (m *Module) Boot(ctx context.Context) error   { /* sqldb.Open + Ping + 迁移 */ m.db = d; return nil }
func (m *Module) DB() *sqldb.DB {                                        // 实现 Provider
    if m.db == nil { panic("db: DB() called before Boot") }             // Boot 前调用 → 明确 panic,而非 handler 里 nil deref
    return m.db
}
func (m *Module) Shutdown(context.Context) error { if m.db != nil { return m.db.Close() }; return nil }
```

```go
// admin 模块依赖 db(与 session),只依赖接口
package admin
func New(db db.Provider, sess session.Provider, opts ...Option) *Module { … }
func (m *Module) Register(a *app.App) error {
    a.Engine.GET("/admin", func(c *inertia.Context) {
        var users []User
        _ = m.db.DB().Table("users").Scan(&users)   // ← 运行期取用,Boot 后必然非 nil
        ...
    })
    return nil
}
```

```go
// commands/serve.go —— composition root 在 commands(不在 main.go;main 只跑 cobra)
package commands

import (
    "time"
    "github.com/millken/goapp-template/internal/app"
    "github.com/millken/goapp-template/internal/config"
    "github.com/millken/goapp-template/server"
    "github.com/spf13/cobra"
)

// phase 1:config 不动,直接用 config.Config(组合 Config 是否上移 commands,留到 phase 2 再定,见 §4.4)
func runServe(cmd *cobra.Command, cfg *config.Config) error {
    eng, mode, err := server.New(&cfg.Server)        // engine 装配在 server 侧(耦合 embed/构建模式)
    if err != nil { return err }

    a, err := app.New(eng, app.WithShutdownTimeout(10*time.Second))
    if err != nil { return err }
    // 注册序 = Boot 序。phase 1 只有示例路由 Module;phase 2 追加 db/session/admin。
    if err := a.Use(server.Routes()); err != nil { return err }

    slog.Info("server starting", "addr", cfg.Server.Addr, "mode", mode, "dev_addr", cfg.Server.DevAddr)
    return a.Serve(cmd.Context())                    // ctx 一路贯通到 Boot
}

// (phase 2 起,这里会变成:)
//   dbMod := dbmod.New(cfg.DB); sessMod := sessmod.New(cfg.Session, dbMod); adminMod := adminmod.New(dbMod, sessMod)
//   a.Use(server.Routes(), dbMod, sessMod, adminMod)
```

**为什么不用 service locator / DI 容器**:构造注入让依赖在 `commands` 显式可见、可测,没有"运行时 resolve 失败"的魔法。App 因此保持小而稳。

**务实取舍(不为对称性预抽)**:`Provider` 接口不是必须。若某模块初期只有**一个**消费者(如 admin 是 db 的唯一消费者),`commands` 里直接传具体的 `*db.Module` 给 `admin.New` 即可;等出现第二个消费者、或要写 mock 时,再把 `DB() *sqldb.DB` 抽成 `db.Provider` 接口。同理,模块的 `Config`/`Migrations` 等结构,只在被用到时才存在。

### 4.4 配置 —— phase 1 不动;无环靠 app 不 import config

> v3 草案为了让 `app.New(*config.ServerConfig)` 自建 engine(app import config)而不成环,被迫把组合 Config 挪到 `commands` + 把 `Load` 改成泛化解码 + 导出默认值 + 重写测试。
> **v4 不需要任何这些**:app 改为接收已装配的 engine、不 import config ⇒ `config → db → app → inertia` 无回边 ⇒ 无论组合 Config 放哪都不成环。

- **phase 1:`internal/config` 完全不动**(`Config{Log,Server}` + `Load(path) (Config,error)` + `defaults()` 保持现状,`config_test.go` 不动)。
- **phase 2(加 db)时的选择,届时再定,不预设**:
  - (a) 直接在 `config.Config` 加 `DB *db.Config`(`config` import `db`)—— 因 app 不 import config,`config→db→app→inertia` 无环,可工作;代价是 config 知道一个 feature 模块。
  - (b) 把组合 Config 上移 `commands`,保持 `internal/config` 模块无关 —— 更干净,但要那时才引入泛化 `Load` + 导出默认值(即 v3 那套,但推迟到真有第二个模块时)。
- **启用一致性规则(破解"双真相源",phase 2 起生效)**:启用一个模块 = `commands` 里构造并 `Use`(编译期)**且** 配置段非 nil(运行期)。已 `Use` 的模块,在 `Boot` 开头校验配置存在性,缺失返回明确错误(如 `db: module enabled but [db] config section missing`),**不是 nil 跳过**;"nil 跳过"只适用于未 `Use` 的模块。因此模块构造函数收**指针** `Config`,`cfg==nil` 时由 `Boot` 报清晰错误。

### 4.5 生命周期与 ctx 归属(精确化)

```
server.New(&cfg.Server) → (eng, mode)     // 装配 engine(mode/SSR/staticFS/rootHTML/中间件/错误处理);mode 单次推导带出
app.New(eng, opts…)                        // 包裹 engine;不建 engine、不读 config
  → app.Use(mods…)                         // 每个 Register(挂路由/中间件);任一失败 → 返回 err,进程退出前不会 Serve
                                           //   (engine 无反注册 API,且路由此时不可达 → 不做回滚)
  → app.Serve(ctx)                         // ctx = cmd.Context()(main signal ctx 经 cobra 透传, SIGINT+SIGTERM)
       ├─ for mod in mods(顺序): mod.Boot(ctx)        // 用 ctx:慢连接可被 Ctrl-C 中断
       │      任一 Boot 失败 → 已 Boot 的逆序 Shutdown(shutdownCtx)→ 返回 err
       ├─ eng.Serve()                                  // 阻塞;engine 自带信号处理 + HTTP 优雅关闭
       └─ eng.Serve 返回后:
            shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
            defer cancel()                             // 注:不能用 ctx——engine 内部的 signal ctx 已 stop
            errs := nil
            for mod in mods(逆序): errs = errors.Join(errs, mod.Shutdown(shutdownCtx))
            eng.Close()                                // 关 SSR VM
            return errors.Join(serveErr, errs)
```

要点:
- **Boot 用传入 ctx**(可中断);**Shutdown 用 app 自造的超时 ctx**——因为 `eng.Serve()` 返回前已 `stop()` 它内部的 signal ctx,此时原 ctx 已失效,必须新建。否则一个卡死的 `db.Close()` 会让进程永远退不出去。
- 多个 Shutdown 错误用 `errors.Join` 聚合,不丢。
- **Use 不回滚**(见上):Register 副作用是挂路由/中间件,无法反注册;且 Use 失败时进程直接退出,挂上的东西不可达。
- **二次 Ctrl-C 强杀**(细节):`eng.Serve` 返回前 `stop()` 意在恢复默认信号(第二次信号强杀),但只要 main 的 signal ctx 还没 stop,`os/signal` 仍认为信号有人处理,第二次不会强杀。缓解:进入 Shutdown 阶段前让 main 的 signal ctx 也 stop(把 stop 传下,或 main 在 eng.Serve 返回后自行 stop)。在此之前,**Shutdown 钩子卡死的唯一保底是 `shutdownTimeout`** —— 这也是后续 `RunWithContext` 统一方案的又一理由。

**信号归属(待清的既有冗余)**:当前有**两套**信号处理——`main.go` 的 `signal.NotifyContext(os.Interrupt)`(还漏了 SIGTERM)与 `inertia` 内部 `eng.Serve()` 的 `signal.NotifyContext(SIGINT, SIGTERM)`。main 的 ctx 经 cobra 传下后,只有 Boot 真正消费它。阶段 1 迁移:
- 立刻能做:`main.go` 改用 `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`(补 SIGTERM),并删除任何与 engine 重复的关闭逻辑;`app.Serve(ctx)` 把它用于 Boot。
- 更彻底(后续 inertia 增强):给 `inertia.Engine` 加 `RunWithContext(ctx context.Context) error`,让外部 ctx 一路贯通到 HTTP 服务阶段,实现**单一信号 owner**(顺带解决上面"二次强杀"问题)。在此增强落地前,`app.Serve` 采用上面的"Boot 用 ctx + eng.Serve 自管 + Shutdown 自造超时 ctx"方案。

---

## 5. Feature 模块设计

模块统一放在 `internal/module/<name>/`。每个模块自带:`Module` 类型、`Config`、(按需)`Provider` 接口、`Register`/`Boot`/`Shutdown`。

### 5.1 `db`(第一个做的)—— 基于 `github.com/dnsoa/go/sqldb`

DB 访问与迁移**默认用 `sqldb`**(零外部依赖,仅 stdlib;`*sqldb.DB` 内嵌 `*sql.DB`,自带查询 builder、struct 扫描、事务、文件式迁移)。

**Provider 暴露 `*sqldb.DB`**(不收窄成更窄的接口):builder `db.Table(...)`、`QueryScan`、`Transaction`、`MigrateUp` 都是具体类型方法,且 builder 类型不可命名,只能链式调用。**契约:`DB()` 仅 Boot 后有效;Boot 前调用直接 panic 带清晰信息**(优于 handler 里 nil deref)。

```go
package db

import (
    "context"
    "embed"
    "errors"
    "fmt"
    "io/fs"
    "time"
    "github.com/dnsoa/go/sqldb"
    "github.com/millken/goapp-template/internal/app"
)

//go:embed migrations/*.sql   // 迁移 SQL 文件由本包提供(见下方"迁移接线")。模板自带一个示例迁移,空也不会编译失败。
var migrationFS embed.FS

type Provider interface { DB() *sqldb.DB }

type Config struct {
    Driver          string        `yaml:"driver"`   // "pgx" / "mysql" / "sqlite3"…
    DSN             string        `yaml:"dsn"`
    MaxOpenConns    int           `yaml:"max_open"`
    MaxIdleConns    int           `yaml:"max_idle"`
    ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
    Debug           bool          `yaml:"debug"`
    Migrations      *Migrations   `yaml:"migrations"`   // 出现该段即启用迁移(见下方"迁移接线")
}

// Migrations 只含 yaml 可表达的开关/参数。fs.FS 不在这里(它不能从 yaml 来)。
type Migrations struct {
    Table   string `yaml:"table"`    // 默认 "migrations"(sqldb 默认值,非 schema_migrations)
    Service string `yaml:"service"`  // 默认 "default";多服务隔离用
}

type Module struct{ cfg *Config; db *sqldb.DB }

func New(cfg *Config) *Module                        { return &Module{cfg: cfg} }
func (m *Module) Register(a *app.App) error          { return nil } // 纯服务,无路由
func (m *Module) Boot(ctx context.Context) error {
    if m.cfg == nil {
        return errors.New("db: module enabled but [db] config section missing") // 见 §4.4 一致性规则
    }
    d, err := sqldb.Open(m.cfg.Driver, m.cfg.DSN, sqldb.WithDebug(m.cfg.Debug))
    if err != nil { return fmt.Errorf("db open: %w", err) }
    if m.cfg.MaxOpenConns > 0    { d.SetMaxOpenConns(m.cfg.MaxOpenConns) }
    if m.cfg.MaxIdleConns > 0    { d.SetMaxIdleConns(m.cfg.MaxIdleConns) }
    if m.cfg.ConnMaxLifetime > 0 { d.SetConnMaxLifetime(m.cfg.ConnMaxLifetime) }
    if err := d.PingContext(ctx); err != nil { d.Close(); return fmt.Errorf("db ping: %w", err) }
    m.db = d
    if mg := m.cfg.Migrations; mg != nil {              // 出现 migrations: 段即启用
        var opts []sqldb.MigrationOption
        if t := mg.Table; t != ""   { opts = append(opts, sqldb.WithMigrationTable(t)) }
        if s := mg.Service; s != "" { opts = append(opts, sqldb.WithMigrationService(s)) }
        sub, _ := fs.Sub(migrationFS, "migrations")     // FS 来自包级 embed,不来自 yaml
        if err := d.MigrateUp(ctx, sub, opts...); err != nil {
            d.Close(); return fmt.Errorf("db migrate: %w", err)
        }
    }
    return nil
}
func (m *Module) DB() *sqldb.DB {
    if m.db == nil { panic("db: DB() called before Boot") }
    return m.db
}
func (m *Module) Shutdown(context.Context) error { if m.db != nil { return m.db.Close() }; return nil }
```

迁移文件:放 `internal/module/db/migrations/*.up.sql` + `*.down.sql`(前缀字典序排序)。每个迁移在独立事务里执行(先记录 version,再跑 SQL,失败整体回滚)。

**迁移接线(重要,避免静默不迁移)**:`fs.FS` **不能从 yaml 来**。所以:
- 迁移 SQL 文件由 db 包级 `//go:embed migrations/*.sql` 提供(`Migrations` 结构里**没有** `FS` 字段)。
- yaml 里**出现 `migrations:` 段即启用**迁移;`table`/`service` 可选。
- 这样避免了"配置了 `migrations:` 但 FS 为 nil → Boot 里判断为假 → 静默不跑"的坑。
- 备选(若需 FS 来自别处):`db.New(cfg, db.WithMigrationsFS(fs))` 构造注入 —— 等真有需求再加,不预设。

**sqldb 集成注意事项**
- **驱动要自己 import**:`sqldb` 不带任何驱动依赖,模板必须 blank-import 选择的驱动(`_ "github.com/lib/pq"` / `_ "github.com/go-sql-driver/mysql"` / `_ "modernc.org/sqlite"` …),`sql.Open` 才注册得到。直接放 `main` 或一个小 `internal/driver` 包均可(不强制)。
- **Flavor 自动判定**:`sqldb.Open` 按驱动名映射 MySQL/PostgreSQL/SQLite(占位符 `?`→`$N`、引号风格随之),per-connection,配置不用填。
- **builder 不可命名**:查询构造只能 `mod.DB().Table(...)` 链式。
- **Transaction 无 ctx**:`db.Transaction(func(*sqldb.Tx) error)` 不接受 context;需要 ctx 传播就用 `BeginTx` + 手动管理。
- **迁移绕过 debug 日志**:迁移在裸 `*sql.DB` 上执行,不走 DB 级 `WithDebug`。
- **默认迁移表名是 `migrations`**(不是 `schema_migrations`);多服务用 `WithMigrationService` 隔离。
- **builder 无 DELETE**:删除走 `db.Exec`;`Update` 必须带 `Where`。

**迁移执行时机**:默认在 `Boot` 里自动跑(便于自用/开发);生产若想显式控制,可加 `migrate` 子命令复用同一 Module —— 后续迭代点。

### 5.2 `session`(已实现:签名 cookie + 可插拔 store)
- 职责:请求级会话,中间件形式挂到 `*inertia.Context`。
- 依赖:`db.Provider`(store=db 时);store=memory 时不需要。
- Config:`secret`(必填)、`cookie_name`、`ttl`、`store`(`memory`|`db`)、`db_table`、`secure`、`same_site`、`path`、`domain`。`HttpOnly` 恒为 true(无 opt-out 字段)。
- Register:挂一个中间件(靠 §4.2 顺序保证,排在依赖它的 admin 之前),把 `c.Writer` 注入 session。
- **cookie 写回时机**:session ID 装在 HMAC-SHA256 签名 cookie 里;数据存 Store。`Session.Save/Destroy` **同步**写/清 cookie —— 因为 inertia 的 ResponseWriter 是**写穿透**(首个 body 写就刷 header),事后写 cookie 会丢。所以 handler 必须在写 body 前 `Save`。
- DBStore:`ON CONFLICT`/`ON DUPLICATE KEY` 按 `db.Flavor` 生成(3 占位符);`expires_at BIGINT`(UnixNano,PG/MySQL 的 INTEGER 会溢出);表名校验 `^[A-Za-z_]\w*$`。
- 暴露 `Provider.Session(c *inertia.Context) Session`。

### 5.3 `admin`(已实现:完整后台框架)
不再是"只有鉴权中间件"的薄壳,而是一套后台基础框架:
- **组成**(`internal/module/admin/`):`admin.go`(Module/Config/New/Register/Boot)、`auth.go`(鉴权中间件 + `isUnder` + 已登录请求注入 `adminMenu`/`adminUser`/`adminMount`/`loginPath` 共享 props)、`handlers.go`(loginForm/loginSubmit/logout/dashboard)、`menu.go`(`MenuItem` + `AddMenuItem` + 排序的 `menuItems`)、`user.go`(`User` + `HashPassword`/`verifyPassword`(bcrypt)+ `findUser` + `authenticate`,含未知用户的等时 dummy 比较)。
- **依赖**:`SessionProvider`(登录态)+ `DBProvider`(用户查库),构造注入;`New(cfg, sessProv, dbProv)`。
- **路由**(挂在 `Mount()` 下,默认 `/admin`):`GET /login`、`POST /login`、`POST /logout`、`GET /`(dashboard)。auth 中间件放行 login,其余需登录。
- **认证来源**:`users` 表(经 db 模块;见 §5.1 的 `002_users` 迁移),密码 bcrypt。首个用户用 `goapp admin create-user <username>` 播种(读 stdin 或 `--password`)——模板里不写死 hash。
- **Config**:`mount`、`login_path`、`auth_key`(默认 `admin_user_id`)、`users_table`(默认 `users`,校验标识符)。
- **登录/登出**遵守写穿透:`sess.Save/Destroy` 先于 302 重定向的 body 写出。
- **前端**:`frontend/src/components/AdminLayout.vue`(侧栏菜单 + 登出表单 + slot)、`frontend/pages/admin/{login,dashboard}.vue`。菜单项由 admin 资源在 `Register` 时 `AddMenuItem` 注册,中间件注入为 prop。
- **生成的 admin 资源**依赖 `dbProvider{DB() *sqldb.DB}` + `adminHost{Mount(); AddMenuItem(admin.MenuItem)}`(都是小接口,`*db.Module`/`*admin.Module` 满足),在 `commands/serve.go` 里排在 `adminMod` 之后:`a.Use(..., adminMod, adminpost.New(dbMod, adminMod))`。

### 5.4 `scaffold` 生成器(`internal/scaffold`,原 mvc)
把 Go + Inertia + Vue 的 CRUD 结构约定化的**开发期代码生成器**——**是工具,不是运行时 Module**,所以放 `internal/scaffold/`(不在 `internal/module/`)。原 `mvc` 包即此;`mvc` 作为命令别名保留。
- `Spec`/`NewSpec`:资源名归一化(snake/kebab/Camel → 各种形态)。
- `Resource(name)`:生成 public 资源(handler + model + `index/form.vue`)。CLI `goapp gen resource <name>`(别名 `mvc`)。
- `Admin(name)`:生成 admin 资源(handler 挂 admin mount + 注册菜单 + 复用 resource 的 model + admin 版 `index/form.vue` 套 `AdminLayout`)。CLI `goapp gen admin <name>`。

### 5.5 生成器机制与约定
- **CLI**:`goapp gen resource <name>` / `goapp gen admin <name>`(cobra,在 `commands/gen.go`;`gen` 跳过 AppInit,纯写文件)。
- **机制**:`text/template`(`[[ ]]` 分隔符,避开 Vue 的 `{{ }}`)+ `internal/scaffold/templates/{resource,admin}/*.tmpl` 单个 `//go:embed`。生成器与运行期 Module 解耦。
- **不生成迁移**:schema 变更是事件驱动、通常整体性的,不是"每资源一个";且只有 `internal/module/db/migrations/` 被 embed+执行,每资源的 `migrations/` 会是**没人跑的死文件**。迁移**手写**在 `internal/module/db/migrations/`(`NNN_name.up.sql`/`.down.sql`,sqldb 按文件名序执行并记录版本)。
- **生成物是普通文件**:随意改;覆盖前需 `--force`。
- **接线断言测试**:`internal/scaffold/build_test.go` 把生成物写进真实模块树,补一个 `New((*db.Module)(nil)[, (*admin.Module)(nil)])` 断言文件再 `go build`——因为生成包单独能编译(自带接口),类型不匹配只在接线点暴露。

---

## 6. 目标目录结构

```
goapp-template/
  main.go                  # 仅:signal ctx(SIGINT+SIGTERM)+ cobra ExecuteContext
  commands/                # composition root:root(AppInit)/serve/gen/admin
    root.go serve.go gen.go admin_user.go   # gen=脚手架;admin create-user=播种首个用户
  internal/
    app/                   # 薄内核:Module/Booter/Shutdowner + App{New(eng)/Use/Serve(ctx)};只 import inertia
      app.go module.go
    config/                # Config{Log,Server,DB,Session,Admin} + Load;不 import app(无环)
    scaffold/              # 【工具层,非运行时】代码生成器(原 mvc):naming/resource/admin + templates/{resource,admin}/*.tmpl
    module/                # 运行时 feature 模块,各自一个子包
      db/      (含 migrations/*.sql —— 001_init、002_users)
      session/ (session.go/impl.go/store*.go/cookie.go)
      admin/   (admin.go/auth.go/handlers.go/menu.go/user.go —— 完整后台框架)
      adminpost/ # 【生成器产物示例】goapp gen admin post
    driver/                # blank-import 选择的 DB 驱动(默认 sqlite3)
    buildinfo/
  server/                  # engine 装配:server.New(装配)+ server.Routes(app.Module)+ mode_*.go + rootHTML + embedded/
    server.go routes.go mode_dev.go mode_prod.go
  frontend/                # Vue+Vite+SSR
    src/components/AdminLayout.vue   # admin 外壳(菜单+登出+slot)
    pages/                 # 视图按 glob 发现,key = pages/ 下相对路径
      admin/{login,dashboard}.vue    # admin 框架页面;生成的资源落 pages/admin/<name>/
  docs/modular-design.md
```

> 边界:`internal/app` = 通用生命周期内核(只 import inertia,可复用);`internal/scaffold` = 开发期生成器(工具,非运行时);`internal/module/*` = 运行时模块;`server` = template 的 engine 装配 + 示例路由(耦合 embed/构建模式)。Module 各自注册自己的路由。

---

## 7. 从当前模板的迁移路径(分阶段、每步可验证)

阶段 1 顺便清掉下面几处既有冗余(都是"装配/配置"层的事,正好和立 App 内核重叠):
- **ModeName 重复推导 mode**(`server.go:17-25` vs `:29-32`):让 `server.New` 把 mode 作为结果带出,只算一次,日志不再会说谎。
- **SSR bundle 文件名两处硬编码**(`config.go:88` 默认 + `mode_prod.go` embedded)→ 提常量;并明确 `StaticPath`/`SSRBundlePath` 在 prod 构建下是 no-op(字段注释或 prod 下配置即警告)。
- **config 经 cobra context 传递**(`root.go:46`、`serve.go:21-24` 的 WithValue + 断言):`AppInit` 直接返回/存 cfg(或包级变量),serve 职责收缩为"取 cfg → server.New → app.New → Use → Serve"。
- **dev-addr 三级覆盖散落 serve.go**(flag → `VITE_DEV_ADDR` env → config):env 覆盖收进 config 层,命令层只留 flag。
- **双信号处理**:`main.go` 补 SIGTERM、删与 engine 重复的关闭逻辑(见 §4.5)。

1. **立 App 内核(无功能变化)+ 清冗余**
   - 新建 `internal/app`(薄内核):`Module`/`Booter`/`Shutdowner` + `App{New(eng,* opts)/Use/Serve(ctx)}` + `WithShutdownTimeout`。只 import inertia。
   - `server.New` 改为返回 `(eng, mode, err)`(mode 单次推导);**不再**在其内部 `registerRoutes` —— 路由改由 `server.Routes()` 这个 `app.Module` 在 `app.Use` 时注册。
   - `commands`:AppInit 存 cfg(去掉 context.Value 仪式);`runServe` = `server.New(&cfg.Server)` → `app.New(eng)` → `a.Use(server.Routes())` → `a.Serve(cmd.Context())`。
   - `main.go`:signal ctx 补 SIGTERM。
   - **`internal/config` 不动**(app 不 import config,无环)。
   - **验证**:行为与今天一致(SIGINT/SIGTERM 优雅关闭、SSR、`/`、`/api/health`、现有 `config.yaml` 直接可用)。
2. **第一个 feature 模块:`db`**
   - 实现 `internal/module/db`(sqldb),`commands` 接上(本地 SQLite/Postgres 跑通 Boot/Shutdown + 一次迁移,验证 §5.1 迁移接线)。
   - 定型 provider 接口 + 构造注入 + `DB()` Boot 前 panic 契约。
   - 此时再决定 db 配置段放 `config.Config` 还是上移 `commands`(§4.4)。
3. **`session`(依赖 db)**
   - 验证模块间依赖(db → session)、中间件顺序保证(§4.2)。
4. **`scaffold` 生成器 + `admin` 框架**(已实现)
   - `internal/scaffold`(原 mvc,移出 module/):`goapp gen resource`(别名 `mvc`)+ `goapp gen admin`。**不再生成每资源迁移**;接线断言 build 测试守住"生成物能接上真实模块"。
   - `admin` 从鉴权中间件扩成完整框架:login/logout(bcrypt + users 表)+ dashboard + menu 注册 + `AdminLayout.vue`;`goapp admin create-user` 播种首用户。serve 顺序:routes → db → session → admin → admin 资源。
5. **(未来)lift 成 `goapp-foundations`** —— 多项目复用成熟后再做(届时可考虑把 server.New 装配并入 app.New;`internal/scaffold` 也一并带走)。

---

## 8. 关键取舍与开放问题

| 议题 | 结论 |
|---|---|
| app 内核形态 | **薄内核**:`app.New(*inertia.Engine)`,只 import inertia;装配留 `server.New` |
| DB 访问 | **sqldb(已定)** —— `github.com/dnsoa/go/sqldb`,`*sql.DB` + builder + struct 扫描 + 事务,零外部依赖 |
| 迁移 | **sqldb.MigrateUp(已定)** —— 包级 `//go:embed` 的 `*.up/down.sql`,表名/服务可配;FS 不来自 yaml |
| 环规避 | app 不 import config ⇒ `config→db→app→inertia` 无环(结构上保证,不靠约定) |
| 组合 Config 落点 | **phase 1 不动**(留 `config.Config`);phase 2 再定(config 字段 vs 上移 commands,二者都无环) |
| 模块间 DI | 构造注入 + provider 接口(不用 service locator / DI 容器);接口等第二个消费者/第一个 mock 出现再抽 |
| App 持有业务服务 | 否;模块不读 App 配置(构造注入) |
| Provider 有效期 | 仅 Boot 后;Boot 前调用 → panic 带清晰信息 |
| Boot/Shutdown ctx | Boot 用 Serve 传入 ctx;Shutdown 用 app 自造超时 ctx;错误 errors.Join |
| Use 回滚 | 不做(engine 无反注册 API,且失败即退出) |
| 信号 owner | 阶段 1 补 SIGTERM + 删冗余;后续 inertia 加 `RunWithContext(ctx)` 彻底统一(顺带解决二次强杀) |
| session 后端 | **memory / db 已实现**(签名 cookie,Save/Destroy 同步写 cookie);Redis 待需求 |
| admin | **完整框架已实现**:auth + login/logout(bcrypt/users 表)+ dashboard + menu;`create-user` 播种 |
| 脚手架生成器 | **`internal/scaffold`(工具层,原 mvc)**:`gen resource`(别名 mvc)/ `gen admin`;不生成迁移;接线 build 测试把关 |
| 迁移生成 | **不做**:手写在 `db/migrations/`(版本化 `NNN_*.up/down.sql`);每资源迁移会是死文件 |
| 何时抽 foundations | 等痛了(YAGNI);届时把 app + module + scaffold 带走,server.New 装配可并入 app.New |

app 薄内核、ctx 契约、回滚语义、迁移接线、db/session/admin/scaffold 均已实现。**开放**:session 的 Redis 后端(待需求)、admin 登录的 CSRF/换 session id(见 §9)。

---

## 9. 风险

- **import cycle**:已结构性规避——**app 内核不 import config**(§4.1),`config→db→app→inertia` 无回边。落地时若 app 不得已要读 config,优先改成由 `commands` 解析后注入,而非让 app import config。
- **临时耦合(资源 Boot 后才可用)**:provider 接口 + handler 内延迟取用 + `DB()` Boot 前 panic + 注册序=Boot 序的契约。
- **信号双 owner / 二次强杀**:阶段 1 缓解;彻底统一(含二次 Ctrl-C 强杀)待 inertia `RunWithContext`(§4.5)。在此之前 Shutdown 卡死的保底是 `shutdownTimeout`。
- **过度抽象**:app 保持薄内核(不建 engine、不读 config);Module 接口先做最小 `Register`,lifecycle 按需加;逃生门(`WithEngine`/`WithMigrationsFS`/`Provider` 接口/装配并入 app)等真实需求出现再加。
- **可测试性**:`server.New` 装配 engine 会碰文件系统(embed/staticFS)—— 需要时再加 `WithEngine`/装配注入逃生门,现在不预设。
- **约定太死**:生成的是普通文件,可被显式改写/覆盖;不强制。
- **强制配置面变大**:serve 无条件 `Use(db, session, admin)`,启动需要 `[db]`+`[session]`(含 secret)+`[admin]` 三段齐全,否则 Boot 报缺段。这是"模块默认开、缺段即响亮报错"的取舍;要精简就在 `commands/serve.go` 注释掉对应 New/Use。
- **admin 登录 session 未换 ID(session fixation)**:登录后复用登录前的 session ID。HttpOnly+签名下风险低,暂记为已知取舍;若日后 `session.Session` 加 `Rotate` 再改,不为此现在扩接口。
- **admin 登录无 CSRF token**:login/logout 是普通表单 POST,靠 cookie `SameSite=Lax` 缓解;需要更强防护时在 session 层加 CSRF,不预设。
- **admin 生成资源的前端链接依赖 mount**:handler 用 `Mount()` 算路由(mount 可配),但生成的 vue 用服务端下发的 `basePath` 建链接,已与 mount 解耦;`pages/admin/<name>/` 目录前缀仍假设 mount=`/admin`,改 mount 时注意页面目录。
