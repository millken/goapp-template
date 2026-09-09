---
name: goapp
description: Go + Inertia + Vue 3 全栈框架(goapp-template 及其生成的项目)的开发规约。在这个仓库里加页面、加 CRUD、加后台资源与权限、加后台任务或 cron、写迁移、做上传、改配置、动前后端契约之前先读。Triggers: add a page/route/CRUD, admin resource, permission, queue task, cron plan, migration, file upload, Inertia props, SSR, goappctl gen.
---

# goapp 框架规约

Go 后端 + Inertia + Vue 3 的单体应用。**没有 REST 层**:后端 handler 直接把 props 交给一个 Vue 页面组件,
`c.Render("admin/product/index")` 对应 `frontend/pages/admin/product/index.vue`。JSON 只用于健康检查和少数数据端点。

导入路径统一是 `<module>/internal/...`,`<module>` 取自 `go.mod`;CLI 二进制名以下写作 `<app>`(模板里是 `myapp`)。

## 0. 先确认哪些组件在场

组件是可选的,`goappctl init` 会把没选的整目录删掉。**动手前先看目录,不要假设**:

```bash
ls internal/service/          # db / session / storage / queue 哪些在
ls internal/controller/       # 有没有 admin/
ls frontend/ssr* 2>/dev/null  # 有没有 SSR
```

`svc.DB`(核心)之外,`svc.Session`、`svc.Storage`、`svc.Queue` 三个字段**随组件消失**——见
`internal/app/services.go` 的说明:只有对应组件自己和 `admin` 可以引用它们,核心代码引用就编译不过。

## 1. 目录地图

| 路径 | 作用 |
|---|---|
| `commands/` | cobra 命令。`serve.go` 是**组装根**,按依赖顺序 Start 各服务;读它就知道全局怎么串起来 |
| `internal/app/services.go` | `*app.Services`:进程级服务容器。**每请求状态放 `*inertia.Context`,绝不放这里** |
| `internal/config/` | 配置结构与默认值;每个组件的 `Config` 由组件自己定义,这里只组装 |
| `internal/controller/` | 路由与 handler。`mount_gen.go` 是公开区域的接线表,`admin/` 是后台区域 |
| `internal/service/` | 领域服务(db / session / storage / queue),各自 `New` + `Start` + `Stop` |
| `internal/tasks/tasks.go` | 后台任务与 cron 的**唯一注册点**,只做接线 |
| `internal/validate/` | 表单校验,产出 `field → message` 给 `errors` prop |
| `server/` | inertia 引擎构造、dev/prod 模式、SSR |
| `frontend/pages/` | 页面组件,**路径即组件名** |
| `frontend/src/components/admin/` | 后台可复用件:`AdminShell` `DataTable` `ServerTable` `FormField` `CsrfField` `ConfirmDialog` `FileManager` … |

## 2. 加功能的三条路

**公开 CRUD** —— `goappctl gen resource post`
生成 `internal/controller/post/{handler,model}.go` + `frontend/pages/post/{index,form}.vue`,
并**自动改** `internal/controller/mount_gen.go` 的 `gen:mounts` 区块(幂等,`--no-mount` 可关)。

**后台 CRUD** —— `goappctl gen admin post`
生成 `internal/controller/admin/post/{handler,model}.go`(`package post`)+ `frontend/pages/admin/post/{index,form}.vue`。
接线要**手动一行**:`commands/serve.go` 里 `adm.Mount(eng)` 之后加 `post.Mount(eng, svc, adm)`(命令会把这行打印出来)。

**只加一个页面** —— 手写,照 `internal/controller/site/site.go` 的形状:`Mount` 里 `eng.GET`,handler 里
`c.Set(...)` 然后 `c.Render("<页面路径>")`,再建对应的 `.vue`。

生成的项目里没有生成器,先 `go install github.com/millken/goapp-template/cmd/goappctl@latest`。

## 3. 不能破的规矩

**后台路由必须走 registrar。** 不要 `eng.GET` + `adm.AuthMiddleware()`:

```go
r := adm.Resource(eng, "post")   // 权限前缀,决定 post.access / post.modify
r.GET(ct.base, ct.Index)         // GET/HEAD → <res>.access
r.POST(ct.base+"/:id/delete", ct.Delete)  // 其余方法 → <res>.modify
r.Menu("内容", "Post", ct.base)  // 侧边栏,按 access 权限显示
```

权限由 HTTP 方法推导(`internal/controller/admin/permission.go`),所以**加路由不可能忘记加检查**——
这正是这个设计要堵的洞。`modify` 蕴含 `access`,反之不成立。资源名必须匹配 `^[a-z0-9][a-z0-9_-]*$`,
带点会和权限键的分隔符冲突,启动即 panic。

**校验失败要重渲染同一个 `item`。** `internal/validate`:

```go
v := validate.New().Field("name", form.Name, validate.Required, validate.MaxLen(80))
if !v.OK() { ct.renderForm(c, item, v.Errors()); return }  // item 回填输入,errors 显示消息
```

错误**不跨重定向**(重定向会丢掉所有 props),所以校验失败是重渲染,不是 redirect。

**flash 只用于跨重定向的成功消息。** `kind` 决定呈现:success → 自动消失的 toast,其余 → 留在页面上的 alert。
要显式指定就把样式写进 key:`stageFlash(c, "toast:error", msg)`,前端 `AdminShell.vue` 的 `split()` 读这个前缀。
值必须是扁平字符串——`store_db` 会把 session 过一遍 JSON,`store_memory` 不会,结构体两边类型不一致。

**后台表单必须带 CSRF。** 每个受保护的后台请求由 `resolve` 注入 `csrfToken` prop,页面把它传给
`<CsrfField :csrf-token="csrfToken">` 或交给 composite。少了这一半页面看着正常、提交 403。

**SSR 下 overlay 不能默认打开。** teleport 的内容不进 SSR 输出,所以对话框/抽屉的初始状态必须是关。
`ref<T | null>(null)` 而不是 `:open="true"`。

**迁移是手写的,三方言并行。** `internal/service/db/migrations/{sqlite3,mysql,postgres}/` 下
`NNN_name.up.sql` / `.down.sql`,**三个目录同名同数**(有 parity 测试盯着)。生成器不产迁移。
MySQL 的 DSN 必须带 `multiStatements=true`。组件自带迁移的,用自己的 migration service name
(见 `internal/service/queue/migrations.go`),这样 queue-less 的项目不会留下没人用的表。
改了迁移就跑一遍 MySQL 往返:`MYSQL_TEST_DSN=... go test ./... -run TestMySQLMigrations`。

**`internal/tasks/tasks.go` 只放接线。** 一个 handler 应该是几行:解开 payload,调 `internal/service/…`。
`Register` 在 `queue.Service.Start` **之前**被调用,所以注册期不要解引用 `svc` 的字段;handler 体内读随便。
接线错误(非法 kind、重复、表达式解析不了)直接 panic,因为唯一来源就是这个文件。

**goappctl 标记要成对。** `//goappctl:<component>` … `//goappctl:end`(YAML 用 `#`,Vue/MD 用 `<!-- -->`),
**不能嵌套**,每个组件在一个文件里一个块。注意:`init` 不遍历点目录,所以 `.github/` 和 `.claude/`
里的标记永远不会被剥离,也不会被改写 module 路径——那里的内容要写成组件无关、身份无关的。
按组件删除的文件走 `cmd/goappctl/internal/components/components.go` 的 `Owned` 列表。

## 4. 命令

```bash
make dev            # Vite + Go(会 idempotent 地建 config.yaml、播种 admin 账号)
make test           # go test ./...
make lint           # golangci-lint(配置在 .golangci.yml,CI 里钉了版本)
make build-prod     # 构建前端 → 嵌入 → -tags prod 的二进制

<app> serve
<app> admin create-user <username>
<app> queue worker | enqueue <kind> [json] | ls | retry <id>… | kinds
```

## 5. 配置

`config.yaml`(gitignore,`make dev` 会从 `config.example.yaml` 幂等地建一份,已存在就不动)。段落:
`log` `server` 和各组件的 `db` `session` `admin` `storage` `queue`——**缺哪段哪个服务就起不来**,
`config.example.yaml` 是权威参考(带每段说明)。默认值在 `internal/config/`;
应用主目录来自 `<APP>_HOME` 环境变量,否则 `~/.<app>`(见 `commands/paths.go`)。

## 6. 测试的写法

表驱动;数据库用 `sqldb.Open("sqlite3", ":memory:")`;存储后端用 `internal/service/storage/backendtest`
跑同一套契约;SSR 有提交好的渲染 fixture(仅模板仓库有:`frontend/pages/admin/ssrfixture/` + `server/ssr_fixture_test.go`,
改了生成模板它会漂,按目录里的配方重生成);
MySQL 往返测试由 `MYSQL_TEST_DSN` 门控,CI 在 `.github/workflows/migrations.yml` 里给它起
mysql:8.0——**库名必须 `_test` 结尾**,它会 MigrateDown 到零。

## 7. 想清楚再动手时读这几个文件

- `commands/serve.go` —— 组装顺序和每个"为什么在这个位置"
- `internal/controller/admin/user_crud.go` —— 后台 CRUD 的范本(列表 / 表单 / 校验 / flash)
- `internal/controller/admin/permission.go` —— 权限模型
- `cmd/goappctl/internal/scaffold/templates/` —— 生成物长什么样(模板仓库里才有)
- `README.md` —— 每个组件的完整说明

## 8. 写代码的风格

注释解释**为什么**,不解释代码在做什么;一个反直觉的选择要留下它挡住了哪个失败。
提交信息:`type(scope): 一句话`,正文用散文说清动机与取舍(照 `git log` 的现有风格)。
