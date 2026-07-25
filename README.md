# goapp-template

Go + Vue 3 + Inertia.js 应用模板。

- **后端**：Go 1.26 + [cobra](https://github.com/spf13/cobra) + [millken/inertia](https://github.com/millken/inertia) + [phuslu/log](https://github.com/phuslu/log)
- **前端**：Vue 3 + Vite + Tailwind CSS 4
- **SSR**：QuickJS（cgo，Actor 模式跨 goroutine 安全）
- **构建**：dev/prod 双模式，prod 通过 `//go:build prod` 把前端 dist embed 进二进制

## 项目结构

```
.
├── main.go                     # 入口（signal ctx + cobra）
├── commands/                   # cobra 子命令 + composition root
│   ├── root.go                 # 根命令 + AppInit（env/config/logging）
│   ├── serve.go                # composition root：Start 基础设施 → 建 Services → 挂路由 → Serve
│   ├── gen.go                  # gen resource / gen admin（脚手架，工具层）
│   ├── admin_user.go           # admin create-user（播种首个用户）
│   ├── version.go
│   └── paths.go                # ~/.myapp 路径辅助（MYAPP_HOME）
├── internal/
│   ├── app/
│   │   ├── services.go         # Services：进程级服务容器（Log / DB / Session）
│   │   └── lifecycle.go        # Lifecycle 接口（Start/Stop），仅基础设施实现
│   ├── buildinfo/              # 版本信息（ldflags 注入）
│   ├── config/                 # YAML 配置加载
│   ├── driver/                 # blank-import DB 驱动（默认 sqlite3）
│   ├── scaffold/               # 代码生成器（工具层，非运行时）
│   ├── service/                # 基础设施服务（实现 Lifecycle，由 serve.go 显式驱动）
│   │   ├── db/                 #   sqldb 连接池 + 迁移（migrations/*.sql）
│   │   └── session/            #   签名 cookie + memory/db store
│   └── controller/             # HTTP 控制器（嵌入 *app.Services，无生命周期）
│       ├── mount_gen.go        #   MountAll：非 admin 区域的路由挂载（gen:mounts 区块）
│       ├── site/               #   公开路由（/ 和 /api/health）
│       └── admin/              #   后台：auth + login/logout + dashboard + menu + users
├── server/
│   ├── server.go               # inertia.Engine 构造（含 SSR VM）
│   ├── mode_dev.go             # !prod：从磁盘读 dist
│   ├── mode_prod.go            # prod： 从 embed 读 dist
│   └── embedded/               # build-prod 时 dist 复制到此
├── frontend/
│   ├── pages/                  # Vue 页面（SSR 构建用 import.meta.glob 自动发现）
│   │   ├── Home.vue
│   │   └── admin/              #   login / dashboard
│   ├── src/
│   │   ├── inertia/            # 客户端 boot / pjax / view-loader
│   │   ├── components/         # AdminLayout.vue
│   │   └── styles/main.css
│   ├── ssr/polyfills.ts        # QuickJS 缺失的最小 polyfill（打包时注入 banner）
│   ├── ssr-esm-render.ts       # SSR bundle 入口（导出 inertiaRender*）
│   ├── vite.config.ts          # 客户端构建
│   └── vite.config.ssr.ts      # SSR 构建（产出 dist/ssr-render-cjs.js）
├── config.example.yaml         # 配置样板（复制成 config.yaml 使用）
└── Makefile
```

## 快速开始

```bash
# 配置（必须：没有 config.yaml 时 serve 会因缺少 [db] 段直接报错）
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
myapp gen resource <name>          # 生成 CRUD 资源（别名 gen mvc）
myapp gen admin <name>             # 生成 admin 资源
myapp admin create-user <username> # 创建 admin 登录用户（bcrypt）
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
- 但 `db` / `session` / `admin` 三段**在启用对应组件时必填**：`serve` 会在 Start 阶段报
  `db: service enabled but [db] config section missing` 这类错误而不是静默降级。
- 查找顺序：`-c <path>` → `$MYAPP_HOME/config.yaml` → `~/.myapp/config.yaml`。

## 脚手架生成器（`myapp gen`）

生成 CRUD 脚手架，减少手写样板。生成器是开发期工具（`internal/scaffold`），不连 DB、不加载配置。
**不生成迁移** —— schema 变更手写在 `internal/service/db/migrations/`（版本化 `NNN_*.up/down.sql`）。

```bash
# 生成一个 public CRUD 资源（handler + model + Vue 页面）
myapp gen resource post
myapp gen resource blog-post     # 资源名支持 snake/kebab/CamelCase
myapp gen mvc post               # mvc 是 gen resource 的别名（沿用旧习惯）

# 生成一个 admin 资源（鉴权保护，路由挂 admin mount 下，页面套 AdminLayout）
myapp gen admin post

# 覆盖已存在文件
myapp gen resource post --force
```

产物：

- `gen resource post` → `internal/controller/post/{handler,model}.go` + `frontend/pages/post/{index,form}.vue`
- `gen admin post` → `internal/controller/adminpost/{handler,model}.go` + `frontend/pages/admin/post/{index,form}.vue`（依赖 `db` + `admin`，自动注册进 admin 菜单）

生成的是普通 controller（结构体嵌入 `*app.Services`，一个 `Mount` 函数），**接线要手动加一行**：

- resource → [internal/controller/mount_gen.go](internal/controller/mount_gen.go) 的 `gen:mounts` 区块里加 `post.Mount(eng, svc)`
- admin resource → [commands/serve.go](commands/serve.go) 里 `adm.Mount(eng)` 之后加 `adminpost.Mount(eng, svc, adm)`

生成的代码是普通文件，可随意修改；生成器不锁死、不接管已写代码。重复路由会在 `eng.RegistrationError()`
处启动前报错，不会静默覆盖。

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

## Build tags

- 默认（`!prod`）：从磁盘读 `frontend/dist/`，热改 SSR bundle 直接生效
- `-tags prod`：`//go:embed embedded/dist`，全部资源 embed，单二进制部署

`make build-prod` 自动 `cp -r frontend/dist server/embedded/dist`，构建完成后清理。

## 自定义为新项目

1. 改 `go.mod` 的 module 名（并全局替换代码里的 import 路径）
2. 改 [Makefile](Makefile) `BINARY := myapp`
3. 改 [internal/buildinfo](internal/buildinfo/) 的 `AppName` —— 它同时决定 `MYAPP_HOME` 环境变量名和 `~/.myapp` 目录
4. `cp config.example.yaml config.yaml` 并改掉 `session.secret`
5. 删示例页面 `frontend/pages/Home.vue` 和 [internal/controller/site/site.go](internal/controller/site/site.go) 里的 `/` 路由

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
