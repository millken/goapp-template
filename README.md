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
├── internal/controller/admin/   #   后台：登录/登出 + 权限（组）+ dashboard + 菜单
<!--goappctl:end-->
├── server/server.go             # inertia.Engine 构造
├── server/mode_dev.go           # !prod：从磁盘读 dist
├── server/mode_prod.go          # prod： 从 embed 读 dist
├── server/embedded/             # build-prod 时 dist 复制到此
├── frontend/pages/Home.vue      # 示例页面
<!--goappctl:admin-->
├── frontend/pages/admin/        # login / dashboard
├── frontend/src/components/admin/ #   AdminShell / PageHeader / DataTable / FormField / ConfirmDialog / ThemeToggle
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
myapp admin create-user <username> [--group Administrators] # 创建 admin 登录用户（bcrypt）
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
goappctl gen admin post           # admin 资源（鉴权保护，页面套 AdminShell）
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
[@tanstack/vue-table](https://tanstack.com/table)（headless，只有逻辑）。`main.css` 除了令牌，还带着 shadcn 的**基础层**（`*` 的边框色、`body` 的底色、
`:focus-visible` 的描边色）。这几行看着像样板，漏掉却不会报错 —— Tailwind v4 的
`border` 只设宽度和线型，没有基础层时 31 处裸 `border` 会画成正文色；`body` 没有底色
时半透明面板会叠在浏览器白底上。`frontend/scripts/tokens.test.ts` 钉住了它们，
另外还会在**编译产物**里查有没有"引用了却没人定义"的自定义属性 —— 这类问题渲染出来
总是"看着像那么回事"，靠肉眼一张截图抓一个。

有四处改动没有跟上游保持一致，
`gen ui --force` 更新组件时会把它们冲掉 —— `frontend/src/components/ui/deviations.test.ts`
会因此变红，那时要做的是把改动补回去而不是删测试：`alert/index.ts` 多了一个 `success` 变体
（上游只有 `default`、`destructive`）；`sonner/Sonner.vue` 补了一行
`import "vue-sonner/style.css"`（vue-sonner 2.x 把样式单独 export，registry 的拷贝没引，
不引的话 toast 既没有定位也没有卡片样式）；所有 `@/registry/default/ui` 导入都已重写成
`@/components/ui`；**控件高度整体降一档**（`button` 默认、`input`、`select` 触发器
从 `h-10` 改成 `h-9`），因为 registry 出的是 40px 而本后台的设计是 36px —— 这样
一个普通 `<Button>` 不用每处都传 `size="sm"` 就是对的高度。在这些 shadcn 拷贝之上，`frontend/src/components/admin/` 放的是拼出后台页面的
组合组件（`AdminShell`、`PageHeader`、`DataTable`、`FormField`、`ConfirmDialog`、`ThemeToggle`）——
生成的页面靠它们拼装，不直接摸 shadcn 层。

- **只服务后台。** 这些文件归 `admin` 组件所有，`goappctl init` 不选 admin 时 `main.css` 里的
  主题块随之消失，公开页面体积回到原样。**依赖不会一起消失**：9 个 npm 包仍留在
  `frontend/package.json` 里 —— 移除它们会让 `pnpm-lock.yaml` 失效，而模板自带的 CI
  跑的是 `pnpm install --frozen-lockfile`，锁文件和 package.json 对不上就直接失败。
  实测未使用的依赖不占用打包体积，trim 后多背的只是一次下载，不是运行时重量。
- **`gen resource` 保持纯 Tailwind**，所以它在无 db / 无 session 的构建里照样可用。
- **不含表单校验组件。** 校验在服务端（[internal/validate](internal/validate/validate.go)），
  失败时重渲染并给出 `errors` prop —— 不需要客户端再来一套。

初始带 13 个组件：`alert` `badge` `button` `card` `dialog` `dropdown-menu` `input`
`label` `pagination` `select` `separator` `sonner` `table`。其中 `sonner` 是 shadcn
淘汰自家 toast 之后的替代品，底层是 `vue-sonner`。加新组件用生成器：

```bash
goappctl gen ui tooltip                  # 可一次多个：gen ui tooltip popover
goappctl gen ui tooltip --dry-run        # 只打印计划，不写文件
goappctl gen ui tooltip --force          # 覆盖已存在文件（用于跟进上游更新）
```

它从 shadcn-vue registry 取源码、递归拉上 `registryDependencies`、写进
`frontend/src/components/ui/`，并把 registry 内部的 `@/registry/default/ui` 导入改写成
`@/components/ui`（少了这步重写，构建会直接失败）。**不装依赖** —— 缺哪些 npm 包会打印一条
`pnpm add` 让你自己跑，和 `gen admin` 打印 mount 行是同一个原则。

用生成器而不是官方 CLI，是因为运行它的人不同：派生项目的开发者手上一定有 Go 工具链，
未必配好了 Node 和 `pnpm dlx`。

**（仅 SSR 构建适用）不要在 SSR 阶段渲染打开的弹层。** `admin` 开、`ssr` 关的项目没有
`frontend/ssr/render.ts` 这个文件，下面这条不适用。启用 SSR 时，`Dialog` / `DropdownMenu` /
`Select` 的浮层走 Teleport，而 Vue 的 SSR renderer 把这类内容放进 `ctx.teleports`，
[frontend/ssr/render.ts](frontend/ssr/render.ts) 并未收集 —— 服务端不会输出它们，
客户端 hydration 时会凭空多出 DOM。弹层默认关闭即可。

### 设计约定

组件本身就是规范（页面从 `frontend/src/components/admin/` 拼装，改约定就是改组件）；
以下是组件管不住的部分：

- **页面解剖**：`AdminShell` → `PageHeader`（标题 + 右侧动作）→ 卡片。内容区铺满视口；
  唯一例外是表单卡片保持 `max-w-lg` —— 超宽输入框可用性反而差。
- **导航两层封顶**：菜单 = 分组（图标栏）→ 条目（第二栏），由
  `r.Menu(section, title, path)` 注册；更深的层级用面包屑尾巴表达
  （`AdminShell` 的 `crumb` prop），不做菜单嵌套。
- **Dialog 只用于破坏性确认**（`ConfirmDialog`，表单 POST 到真实 handler）；
  新建和编辑一律整页。
- **表格**：行操作收进行尾 "…" 下拉；行的自然链接（名称列）指向编辑页；
  空态文案写业务话（"No posts yet."），不写 "No data"。
- **语义色和主色分工**：绿点/徽章表示启用态、`destructive` 表示危险动作；
  `--primary` 留给每页的主动作。改品牌色只动 `main.css` 的 `--primary`。
- **暗色**：两份 HTML 壳里的启动脚本先于首屏设置 `.dark`，`ThemeToggle` 写
  `localStorage.theme`；组件用令牌（`bg-background` 等），不写死颜色。

## 后台权限

权限单元是**资源 + 动词**：`post.access`（读）和 `post.modify`（写）。动词由 HTTP 方法决定 ——
GET/HEAD 是 `access`，其余是 `modify` —— 所以没有任何路由需要自己声明权限。

`modify` **蕴含** `access`：能改的人当然能读。反向不成立。

生成的 `Mount` 通过注册器登记路由，一次调用同时做三件事：注册、挂上守卫、记入权限目录。

```go
r := adm.Resource(eng, "post")
r.GET(ct.base, ct.Index)                 // 需要 post.access
r.POST(ct.base+"/:id", ct.Update)        // 需要 post.modify
r.Menu("Content", "Post", ct.base)       // 侧边栏条目，受 post.access 控制
```

**守卫就是路由中间件**，注册即生效 —— 这是它相对手写 `if hasPermission(...)` 的关键差别：
后者漏一处就是静默的洞。生成的代码漏不掉（`gen admin` 只经注册器，有测试钉住）；
手写路由仍可以直接调 `eng.GET`，那就绕过了守卫 —— 所以后台路由请一律走 `adm.Resource(...)`。

权限存在分组上：`user_groups.permissions` 是一个 JSON 键数组，`superuser = 1` 直接放行。
迁移会播种一个 `Administrators` 超管组，`admin create-user` 默认把用户放进去：

```bash
myapp admin create-user alice                      # 进 Administrators（超管）
myapp admin create-user bob --group Editors        # 进指定组
```

**豁免路由**：登录页完全公开，不经过任何中间件；登出和 `/admin` 仪表盘经过 `AuthMiddleware`——
要求登录，但不做权限检查。仪表盘必须豁免 —— 否则权限为空的用户登录后只看到 403，无法自助。
侧边栏会按权限过滤，所以他看到的是一个短菜单而不是一堵墙。

分组、权限和用户都在后台里管理：**Access → Users / Groups**。权限编辑器的行来自
`adm.Permissions()`（启动时真正注册的路由），所以它不会提供一个没人检查的权限；
勾 `modify` 会自动带上 `access`（服务端同样归一化一次，前端那套只是即时反馈）。

三条防自锁规则在服务端强制，不靠 UI 禁用按钮：不能删除或禁用自己的账号；不能修改
自己所在的分组；系统必须至少保留一个**启用的**超级管理员。第三条的实现方式是把改动
放进事务、然后数一次剩余的启用超管，为 0 就回滚 —— 四条能触发它的路径（禁用、删除、
移出超管组、清掉分组的超管标记）共用同一个守卫，所以将来新增的第五条路径也漏不掉。

禁用一个用户**下一个请求就生效**：`findCaller` 一次查询里就 JOIN 了 `user_groups`
并读出 `status`，不需要额外一次查询。被禁用的用户会带着一条说明跳回登录页，
且重新登录也会被拒绝。

**不经注册器的一共三条**：登出、`/admin` 仪表盘，以及 `/admin/account/password`
（自助改密，GET + POST 两个注册）。三者都用 `AuthMiddleware` —— 要求登录但不要求
权限，所以权限为空的用户仍然能登录、看到自己在哪、改自己的密码、再登出。
最后一条必须如此：走注册器就会产生 `account.access`/`account.modify`，而权限为空的
用户将永远改不了自己的密码。入口在右上角用户菜单里。

### CSRF 与登录限流

**每一个不安全方法都验 CSRF token** —— POST/PUT/PATCH/DELETE，不只是后台。公开的
`gen resource` 表单同样会变更数据，正是 CSRF 的典型目标。校验在 session 中间件里，
它本来就持有会话、也已经全局挂了一次。

token 存在会话上（**没有 cookie 就没有 CSRF 风险**，所以裁掉 session 的项目同时失去
防护和需要防护的东西），以**隐藏字段** `_csrf` 提交。选字段而不是 header 是因为 PJAX
把表单原样当 `FormData` fetch 出去——一套机制同时覆盖普通提交和 PJAX。渲染表单的
handler 调 `sess.CSRFToken(ctx)`，页面用 `<CsrfField :token="csrfToken" />`。
按需铸造而不是给每个访客发，是为了不给公开页面种 cookie（那会让 CDN 缓存不了）；
代价是漏发会在提交时 403，由 `frontend/scripts/forms.test.ts` 和现有的 POST 测试兜住。

**登录成功会重生成会话 id**。这不是额外好处，是必需：登录页铸造 token 意味着认证前
就存在会话，而 `Store.Save` 会保留传给它的 id —— 不重生成的话，别人事先植入的 id
在你登录之后依然有效。

**登录限流按客户端 IP 滑动窗口**：15 分钟内失败 10 次即拒绝，答 429 并告诉你还要等多久；
成功登录清零。**不按用户名计数** —— 那样任何人都能故意把别人锁出去。密码正确但账号被禁用
**不计入**：那不是猜密码。

`X-Forwarded-For` **只在对端属于 `[admin] trusted_proxies` 配置的网段时才读**，默认谁都不信。
盲信它等于让调用者自己挑要被计进哪个桶——那比没有限流更糟，因为它看起来像有防护。
限流本身**失败即放行**：它是速率限制不是授权判断，数据库抖一下就把所有人锁在外面是更坏的结果。


**这一层仍然只是认证与授权，不是完整的账号运营模块。** 没有审计日志、没有最近登录
时间、没有邮箱或找回密码、没有失败次数锁定、也没有批量操作 —— 一次一个用户、一次
一个分组；权限只挂在分组上，没有针对单个用户的例外。公开路由
（`internal/controller/site/`）不涉及用户，这一整套只服务后台。

**`users_table` 只影响运行时查询。** 迁移操作字面量 `users` 表（嵌入的 SQL 读不到配置），
所以把它指向别的表意味着那张表的结构由你负责，包括 `group_id` 列。
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
