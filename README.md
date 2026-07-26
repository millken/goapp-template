# goapp-template

Go + Vue 3 + Inertia.js 应用模板。

- **后端**：Go 1.26 + [cobra](https://github.com/spf13/cobra) + [millken/inertia](https://github.com/millken/inertia) + [phuslu/log](https://github.com/phuslu/log)
- **前端**：Vue 3 + Vite + Tailwind CSS 4
<!--goappctl:ssr-->
- **SSR**：QuickJS（cgo，Actor 模式跨 goroutine 安全）
<!--goappctl:end-->
- **构建**：dev/prod 双模式，prod 通过 `//go:build prod` 把前端 dist embed 进二进制

## 项目结构

路径写成扁平形式、每项统一用 `├──`：这样删掉任意一行都不会留下悬空的树枝
（`goappctl init` 会按所选组件删行）。

```
.
├── main.go                      # 入口（signal ctx + cobra）
<!--goappctl:tooling-->
├── cmd/goappctl/                # 模板工具：init（裁剪）+ gen（脚手架），非运行时
<!--goappctl:end-->
├── commands/                    # cobra 子命令 + composition root
├── commands/root.go             #   根命令 + AppInit（env/config/logging）
├── commands/serve.go            #   composition root：Start 基础设施 → 填 Services → 挂路由
<!--goappctl:admin-->
├── commands/admin_user.go       #   admin create-user（播种首个用户）
<!--goappctl:end-->
├── commands/paths.go            #   ~/.myapp 路径辅助（MYAPP_HOME）
├── internal/app/services.go     # Services：进程级服务容器，由 serve.go 逐组件填充
├── internal/app/lifecycle.go    # Lifecycle 接口（Start/Stop），仅基础设施实现
├── internal/buildinfo/          # 版本信息（ldflags 注入）
├── internal/config/             # YAML 配置加载
<!--goappctl:db-->
├── internal/driver/             # blank-import DB 驱动（默认 sqlite3）
<!--goappctl:end-->
<!--goappctl:db-->
├── internal/service/db/         # sqldb 连接池 + 迁移（migrations/*.sql）
<!--goappctl:end-->
<!--goappctl:session-->
├── internal/service/session/    # 签名 cookie + memory/db store
<!--goappctl:end-->
├── internal/validate/           # 表单校验（纯 stdlib，规则是 func(string) error）
├── internal/controller/         # HTTP 控制器（嵌入 *app.Services，无生命周期）
├── internal/controller/mount_gen.go  #   MountAll：非 admin 区域路由挂载（gen:mounts 区块）
├── internal/controller/site/    #   公开路由（/ 和 /api/health）
<!--goappctl:admin-->
├── internal/controller/admin/   #   后台：auth + login/logout + dashboard + menu + users
<!--goappctl:end-->
├── server/server.go             # inertia.Engine 构造
├── server/mode_dev.go           # !prod：从磁盘读 dist
├── server/mode_prod.go          # prod： 从 embed 读 dist
├── server/embedded/             # build-prod 时 dist 复制到此
├── frontend/pages/Home.vue      # 示例页面
<!--goappctl:admin-->
├── frontend/pages/admin/        # login / dashboard
├── frontend/src/components/     # AdminLayout.vue
├── frontend/src/components/ui/   #   复制进来的 shadcn-vue 组件（admin 专属）
├── frontend/src/lib/utils.ts     #   cn() / valueUpdater()
<!--goappctl:end-->
├── frontend/src/inertia/        # 客户端 boot / pjax / view-loader
├── frontend/src/styles/main.css
<!--goappctl:ssr-->
├── frontend/ssr/polyfills.ts    # QuickJS 缺失的最小 polyfill（打包时注入 banner）
├── frontend/ssr-esm-render.ts   # SSR bundle 入口（导出 inertiaRender*）
<!--goappctl:end-->
├── frontend/vite.config.ts      # 客户端构建
<!--goappctl:ssr-->
├── frontend/vite.config.ssr.ts  # SSR 构建（产出 dist/ssr-render-cjs.js）
<!--goappctl:end-->
├── config.example.yaml          # 配置样板（复制成 config.yaml 使用）
└── Makefile
```

## 快速开始

```bash
# 配置（必须：没有 config.yaml 时 serve 会因缺少组件配置段直接报错）
cp config.example.yaml config.yaml

# 依赖
make tidy
cd frontend && pnpm install && cd ..

# 开发模式（同时启动 Vite + Go serve；Ctrl+C 自动清理 Vite）
make dev

# 生产构建（产物：bin/myapp，前端 dist 已 embed）
make build-prod
```

打开 http://localhost:8080

## CLI

```bash
myapp serve [-a :8080] [--dev-addr http://localhost:5173] [-c config.yaml]
```
<!--goappctl:admin-->
```bash
myapp admin create-user <username> # 创建 admin 登录用户（bcrypt）
```
<!--goappctl:end-->
```bash
myapp version
myapp -v ...           # 全局 verbose（debug 日志）
```

环境变量：
- `MYAPP_HOME`：覆盖默认 `~/.myapp/`，影响 `config.yaml` 和 `.env` 路径（`make dev` 设为 `.`）
- `VITE_DEV_ADDR`：dev 模式下传递 Vite 实际地址（`make dev` 自动设置）
- `DEV_PORT`：`make dev DEV_PORT=5174` 改 Vite 端口

## 配置

配置项、默认值和说明都在 [config.example.yaml](config.example.yaml) 里（单一来源，不在此重复）。

- 配置文件可以完全不存在 —— 缺失的文件或段落都回落到 [internal/config/config.go](internal/config/config.go) 的默认值。
- 但**已启用组件的配置段是必填的**：`serve` 会在 Start 阶段报
  `db: service enabled but [db] config section missing` 这类错误，而不是静默降级。
- 查找顺序：`-c <path>` → `$MYAPP_HOME/config.yaml` → `~/.myapp/config.yaml`。

## 脚手架生成器（`goappctl gen`）

生成 CRUD 脚手架，减少手写样板。生成器在 `goappctl` 里，不在应用二进制里 —— 它是开发期工具，
不连 DB、不加载配置。**不生成迁移** —— schema 变更手写在 `internal/service/db/migrations/`
（版本化 `NNN_*.up/down.sql`）。

```bash
# 模板仓库内
go run ./cmd/goappctl gen resource post

# 已生成的项目里（先装一次）
go install github.com/millken/goapp-template/cmd/goappctl@latest
goappctl gen resource post
goappctl gen resource blog-post   # 资源名支持 snake/kebab/CamelCase
goappctl gen mvc post             # mvc 是 gen resource 的别名
goappctl gen admin post           # admin 资源（鉴权保护，页面套 AdminLayout）
goappctl gen resource post --force        # 覆盖已存在文件
goappctl gen resource post --no-mount     # 不改 mount_gen.go，只打印接线行
goappctl gen resource post -C ../other    # 指定项目根目录
```

产物：

- `gen resource post` → `internal/controller/post/{handler,model}.go` + `frontend/pages/post/{index,form}.vue`
- `gen admin post` → `internal/controller/adminpost/{handler,model}.go` + `frontend/pages/admin/post/{index,form}.vue`

接线：

- **resource 自动接线** —— 直接改 [internal/controller/mount_gen.go](internal/controller/mount_gen.go)
  的 `gen:mounts` 区块（加 import + `post.Mount(eng, svc)`，按名排序）。重复执行不会产生重复项。
- **admin resource 需手动一行** —— admin 区域挂在 [commands/serve.go](commands/serve.go) 里而非
  `gen:mounts` 区块，所以命令会把 `adminpost.Mount(eng, svc, adm)` 打印出来让你粘贴。

校验：

- 生成的 `Create` / `Update` 会先把表单绑进 `item`、跑 [internal/validate](internal/validate/validate.go)，
  失败就用同一个 `item` 重渲染表单 —— 输入自动回填（表单本来就从 `item` 取值：resource 用
  `:value`，admin 用 shadcn `Input` 的 `:model-value`），每个坏字段配一条 `errors` prop 消息。
  校验错误**不进 session**，所以无 session / 无 db 的构建里同样可用。
- 唯一性这类要查库的规则是 handler 里的普通闭包（生成器给了 `nameAvailable` / `nameTaken` 桩子），
  `internal/validate` 本身只依赖标准库。

项目形态靠目录探测（没有 marker 文件）：module 路径读 `go.mod`；缺 `internal/controller/admin/` 时
`gen admin` 直接报错；缺 `internal/service/db/` 时会警告 `svc.DB` 为 nil。

生成的代码是普通文件，可随意修改；生成器不锁死、不接管已写代码。重复路由会在
`eng.RegistrationError()` 处启动前报错，不会静默覆盖。

<!--goappctl:admin-->
## 后台 UI 组件

后台用 [shadcn-vue](https://www.shadcn-vue.com/)：组件**源码复制进仓库**（`frontend/src/components/ui/`），
不是 npm 依赖 —— 和 `gen resource` 产出一样，是「你拥有的普通文件」。数据表格能力来自
[@tanstack/vue-table](https://tanstack.com/table)（headless，只有逻辑）。

- **只服务后台。** 这些文件归 `admin` 组件所有，`goappctl init` 不选 admin 时连同
  `main.css` 里的主题块和 8 个 npm 依赖一起消失，公开页面体积回到原样。
- **`gen resource` 保持纯 Tailwind**，所以它在无 db / 无 session 的构建里照样可用。
- **不含表单校验组件。** 校验在服务端（[internal/validate](internal/validate/validate.go)），
  失败时重渲染并给出 `errors` prop —— 不需要客户端再来一套。

初始带 12 个组件：`alert` `badge` `button` `card` `dialog` `dropdown-menu` `input`
`label` `pagination` `select` `separator` `table`。加新组件：

```bash
cd frontend && pnpm dlx shadcn-vue@latest add combobox
```

**注意**：官方 CLI 拉取 registry 时可能失败（表现为 `Failed to fetch from registry`，
即使 curl 同一个 URL 正常）。手工替代路径是从
`https://shadcn-vue.com/r/styles/default/<name>.json` 取 JSON、按 `files[].path` 落盘
（`ui/**` → `frontend/src/components/ui/`），并把 `@/registry/default/ui` 改写成
`@/components/ui` —— 少了这步重写，构建会直接失败。

**不要在 SSR 阶段渲染打开的弹层。** `Dialog` / `DropdownMenu` / `Select` 的浮层走
Teleport，而 Vue 的 SSR renderer 把这类内容放进 `ctx.teleports`，
[frontend/ssr/render.ts](frontend/ssr/render.ts) 并未收集 —— 服务端不会输出它们，
客户端 hydration 时会凭空多出 DOM。弹层默认关闭即可。
<!--goappctl:end-->

<!--goappctl:ssr-->
## SSR 工作流

启用 SSR（`server.ssr: true`）时：

1. `pnpm build:ssr`（= `vite build --config vite.config.ssr.ts`）把 `pages/*.vue` 打成单文件 CJS
   bundle `frontend/dist/ssr-render-cjs.js`，导出 `inertiaRenderComponent` / `inertiaRenderTemplate`。
   页面由 `import.meta.glob` 自动发现，**没有 codegen 步骤**。
2. Go 启动时 `quickjs.NewVM` 加载该 bundle，开一个专属 goroutine 持有 QuickJS Runtime。
3. HTTP handler 通过 channel 把 `inertiaRenderComponent(name, props)` 派发到该 goroutine 执行。

QuickJS 不带 Node.js globals，[frontend/ssr/polyfills.ts](frontend/ssr/polyfills.ts) 提供最小
`Buffer.from`（Vue SSR 的 `entities` 依赖），以 rollup banner 形式注入 bundle 头部。

bundle 文件名在 Go 侧是 `server.ssrBundleName` 常量，dev 模式的 `server.ssr_bundle_path`
配置项必须与之一致。
<!--goappctl:end-->

## Build tags

- 默认（`!prod`）：从磁盘读 `frontend/dist/`，改前端产物无需重编 Go
- `-tags prod`：`//go:embed embedded/dist`，全部资源 embed，单二进制部署

`make build-prod` 自动 `cp -r frontend/dist server/embedded/dist`，构建完成后清理。

<!--goappctl:tooling-->
## 自定义为新项目

用 `goappctl init` 一步完成：它就地裁剪当前目录 —— 删掉未选的组件、剥掉它们的接线、改写 module
路径和应用名，最后跑 `go build` / `vet` / `test` 验证结果。

```bash
git clone <goapp-template> myapp && cd myapp

# 先看它打算做什么（不写任何文件）
go run ./cmd/goappctl init --module github.com/me/myapp --with db,session,admin --dry-run

# 实际执行；省略 --with 会进入交互勾选
go run ./cmd/goappctl init --module github.com/me/myapp --with db,session,admin --git-reinit
```

| 参数 | 作用 |
|---|---|
| `--module` | 新 module 路径（必填） |
| `--name` | 应用名 / 二进制名，默认取 module 末段；决定 `<NAME>_HOME` 和 `~/.<name>` |
| `--with` | 逗号分隔的组件；`admin` 会自动带上 `session` + `db` |
| `--dry-run` | 只打印计划 |
| `--force` | git 工作区不干净也继续 |
| `--git-reinit` | 丢弃模板的 git 历史，重新 `git init` |

它会拒绝在非模板目录运行（检查 module 路径），所以重复执行不会二次破坏。

生成的项目**不含生成器**：`cmd/goappctl/`、`docs/`、`go.work` 和模板自己的 CI 都会被删掉。
之后要给项目加资源，用装好的二进制（见上面的「脚手架生成器」一节）：
`go install github.com/millken/goapp-template/cmd/goappctl@latest`。

需要手动收尾的只剩：改掉 `config.yaml` 里的 `session.secret`，以及按需删除示例页面
`frontend/pages/Home.vue` 和 [internal/controller/site/site.go](internal/controller/site/site.go) 里的 `/` 路由。

## 本地依赖（开发者）

如果需要同时修改 `github.com/millken/inertia` 或 `github.com/dnsoa/go/sqldb`，把它们克隆到相邻目录，
根目录的 `go.work`（已被 `.gitignore` 忽略）会自动接管：

```
parent/
├── inertia/
├── dnsoa/go/sqldb/
└── goapp-template/
    └── go.work    # use ../inertia/... ../../dnsoa/go/sqldb
```
<!--goappctl:end-->
