# goapp-template

Go + Vue 3 + Inertia.js 应用模板。

- **后端**：Go 1.24 + [cobra](https://github.com/spf13/cobra) + [millken/inertia](https://github.com/millken/inertia) + [phuslu/log](https://github.com/phuslu/log)
- **前端**：Vue 3 + Vite + Tailwind CSS 4
- **SSR**：QuickJS（cgo，Actor 模式跨 goroutine 安全）
- **构建**：dev/prod 双模式，prod 通过 `//go:build prod` 把前端 dist embed 进二进制

## 项目结构

```
.
├── main.go                    # 入口
├── commands/                  # cobra 子命令
│   ├── root.go                # 根命令 + AppInit (env/config/logging)
│   ├── serve.go               # serve 子命令
│   ├── version.go
│   └── paths.go               # ~/.myapp 路径辅助
├── internal/
│   ├── buildinfo/             # 版本信息（ldflags 注入）
│   └── config/                # YAML 配置加载
├── server/
│   ├── server.go              # inertia.Engine 构造
│   ├── routes.go              # HTTP 路由
│   ├── mode_dev.go            # !prod: 从磁盘读 dist
│   ├── mode_prod.go           # prod:  从 embed 读 dist
│   └── embedded/              # build-prod 时 dist 复制到此
├── frontend/
│   ├── pages/                 # Vue 页面（gen-ssr-modules.ts 自动扫描）
│   ├── src/
│   │   ├── inertia/           # 客户端 boot / pjax / view-loader
│   │   └── styles/main.css
│   ├── ssr/                   # SSR 构建 polyfills + esbuild options
│   ├── ssr-build.ts           # esbuild 入口
│   ├── ssr-esm-render.ts      # SSR bundle 入口（导出 inertiaRender*）
│   ├── ssr-modules.ts         # 自动生成（同步 import）
│   └── scripts/gen-ssr-modules.ts
├── config.yaml                # 本地开发配置（MYAPP_HOME=. 时使用）
└── Makefile
```

## 快速开始

```bash
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
myapp version
myapp -v ...           # 全局 verbose（debug 日志）
```

环境变量：
- `MYAPP_HOME`：覆盖默认 `~/.myapp/`，影响 `config.yaml` 和 `.env` 路径
- `VITE_DEV_ADDR`：dev 模式下传递 Vite 实际地址（`make dev` 自动设置）
- `DEV_PORT`：`make dev DEV_PORT=5174` 改 Vite 端口

## 配置（`config.yaml`）

```yaml
log:
  level: info        # debug | info | warn | error
  format: text       # text | json
  # file:
  #   path: logs/app.log
  #   max_size: 104857600
  #   max_backups: 7

server:
  addr: :8080
  static_path: frontend/dist                            # dev 模式下读静态资源
  dev_addr: http://localhost:5173                       # Vite dev server
  ssr_bundle_path: frontend/dist/ssr-render-cjs.js      # SSR bundle 路径
  ssr: false                                            # true 启用 QuickJS SSR

db:                                                     # 数据库模块（启用 serve 时必须配置）
  driver: sqlite3                                       # sqlite3 | mysql | pgx
  dsn: "app.db"                                         # SQLite 文件 / MySQL/PG DSN
  # max_open: 0
  # max_idle: 0
  # conn_max_lifetime: 0s
  # debug: false
  migrations:                                           # 出现该段即启用自动迁移
    table: schema_migrations                            # 默认 migrations
    service: default                                    # 多服务共享 DB 时隔离用

session:                                                # 会话模块（启用 serve 时必须配置）
  secret: "change-me"                                   # HMAC 签名密钥（必填）
  store: memory                                         # memory（开发默认）| db（生产，复用 db）
  cookie_name: session                                  # 默认 session
  ttl: 24h                                              # 默认 24h
  # db_table: sessions                                  # store=db 时的表名，默认 sessions
  # secure: false                                       # HTTPS-only
  # same_site: lax                                      # lax | strict | none
  # path: "/"
  # domain: ""
```

## SSR 工作流

启用 SSR 时（`ssr: true`）：

1. `pnpm generate` 扫描 `pages/*.vue` 生成 `ssr-modules.ts`
2. `pnpm build:ssr`（esbuild + `millken-esbuild-plugin-vue`）打成 CJS bundle
3. Go 启动时 `quickjs.NewVM` 加载 bundle，开一个专属 goroutine 持有 QuickJS Runtime
4. HTTP handler 通过 channel 把 `inertiaRenderComponent(name, props)` 派发到该 goroutine 执行

QuickJS 不带 Node.js globals，[frontend/ssr/polyfills.ts](frontend/ssr/polyfills.ts) 提供最小 `Buffer.from`（Vue SSR 的 `entities` 依赖）。

## Build tags

- 默认（`!prod`）：从磁盘读 `frontend/dist/`，热改 SSR bundle 直接生效
- `-tags prod`：`//go:embed embedded/dist`，全部资源 embed，单二进制部署

`make build-prod` 自动 `cp -r frontend/dist server/embedded/dist`，构建完成后清理。

## 自定义为新项目

1. 改 `go.mod` 的 module 名
2. 改 [Makefile](Makefile) `BINARY := myapp`
3. 改 [internal/buildinfo](internal/buildinfo/) 默认 `AppName`
4. 删除示例页面 `frontend/pages/Home.vue` 和 [server/routes.go](server/routes.go) 中的 `/` 路由

## 本地依赖（开发者）

如果需要同时修改 `github.com/millken/inertia`，把它克隆到 `../inertia/`，根目录的 `go.work`（已被 `.gitignore` 忽略）会自动接管：

```
parent/
├── inertia/
└── goapp-template/
    └── go.work    # use ../inertia/...
```
