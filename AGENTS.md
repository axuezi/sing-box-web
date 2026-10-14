# AI 开发规范

本文件写给参与本项目的 AI 编程助手（Claude Code、Codex 等），人类协作者同样适用。开始任何改动前先读完本文件，再读 [docs/architecture.md](docs/architecture.md)。

## 项目是什么

sing-box-web 是 sing-box 的 Web 管理后台，**管理现有的 `config.json` 而不是接管它**。Go 单二进制（net/http + SQLite）内嵌 Vue 3 前端，以 GPL-3.0 开源。当前版本 v1.0.0，变化记在 [CHANGELOG.md](CHANGELOG.md)。

## 底线（不可违反）

1. **配置只经 `internal/apply` 写入。** 任何修改 `config.json` 的代码都通过 `Apply.Update`，不得直接写文件，不得绕过 `sing-box check`、健康检查和自动回滚。
2. **不接管配置。** 只改本项目管理的字段；用 gjson / sjson 局部修改，保留其余字段和顺序。读出再原样保存必须零改动，新增表单字段时要补这类测试。
3. **最小权限。** 后台不得挂载 `docker.sock`、不得要求额外 capability、不得在容器里执行宿主机命令。需要特权的新动作加到宿主机控制单元 `deploy/ctl/sing-box-web-ctl` 的白名单里，并对每个参数做严格校验。
4. **不写死环境。** 代码、默认值和文档里不得出现某台服务器特有的域名、IP、路径或账号；部署相关的值一律走参数 / `SBW_*` 环境变量。
5. **不泄露隐私。** 访问流向、连接、订阅记录是用户的浏览数据。测试一律用合成数据；不要把线上数据库或日志里的真实记录复制到对话、提交、截图或文档里。
6. **数据库迁移只增不改。** 新表、新列写新的 `internal/store/migrations/NNN_*.sql`，编号递增；已发布的迁移文件不得修改。
7. **审计。** 所有改变状态的操作写审计日志，`target` 记名称（用户名、入站标签）而不是数据库 ID。

## 开工前

- 用户也会用别的 AI 工具改同一个仓库：**动手前重新读要改的文件**，不要凭上一轮记忆改。
- 先弄清楚需求再写代码；需求里有取舍（删功能、改交互、影响线上）时先问。
- 一次只做一件事，不顺手重构无关代码；发现的其他问题记下来告诉用户。

## 代码约定

### Go

- 目标版本见 `go.mod`；只用标准库和已有依赖，新增依赖要说明理由。
- `gofmt`，`go vet` 通过；错误用 `fmt.Errorf("...: %w", err)` 包装。
- 包注释写清楚包的职责；导出的东西都要有注释，说明「为什么」而不是复述代码。
- HTTP 接口：路由在 `internal/server/server.go`，需要登录的接口经 `s.authed`（写请求自带 CSRF 校验）；错误用 `writeError(w, status, code, msg)`，`code` 是稳定的英文标识，前端据此显示本地化文案。
- 长耗时或会重载 sing-box 的操作用 `context.WithoutCancel(r.Context())`，不能因为浏览器断开而半途而废。

### 前端（`web/`）

- Vue 3 `<script setup lang="ts">` + Naive UI；模仿相邻文件的写法和注释密度。
- 文件放置：某个菜单页专用的组件放 `views/<页面>/`（标签页命名为 `XxxTab.vue`），两个以上页面共用的才放 `components/`。后端 handler 按功能分文件（`handlers_<功能>.go`），不要按开发阶段命名。
- **文案**：所有界面文字放 `src/locales/zh-CN.ts` 和 `src/locales/en.ts`，两边键必须一致，不得在组件里写死中文或英文。后端返回的英文错误码 / 状态词要在前端翻译后再显示。
- **手机**：用 `useIsMobile()`（≤ 767px）。表格在手机上改用 `MobileCards` 或两行列表；不得出现横向溢出、逐字换行、文字重叠。
- **主题**：颜色用 CSS 变量（`styles/viz.css` 的 `--viz-*`、Naive UI 主题），浅色和深色都要检查。图表只用 `--viz-cat-1..6` 和 `--viz-other`。
- **状态**：页面筛选条件放 URL 查询参数；纯个人偏好用 `composables/prefs` 或 `localStorage`（读写包 try/catch）。
- 主菜单固定 5 项（`router.ts` 的 `navItems`），新功能优先做成现有页面的标签页；确需新增菜单项先问用户。新增顶级页面要补 `nav.<name>` 文案（页面标题会自动使用）；页面并入别处时，在 `moved` 里保留旧地址的重定向。

## 验证

改完后按顺序做，结果如实告诉用户（失败就贴输出，跳过了就说跳过）：

1. `make check`（gofmt、go vet、go test、前端构建与类型检查）。没有本机 Node 时可以用容器构建前端：`docker run --rm -v "$PWD/web":/w -w /w node:24-slim sh -c "npm ci && npx vue-tsc --noEmit && npx vite build"`（不要用 alpine 镜像，`node_modules` 里有 glibc 二进制）。
2. 新逻辑配单元测试或接口测试（`internal/server/*_test.go` 有现成的接口测试写法）。
3. **界面改动必须看截图**：本地起一个沙箱（真实 sing-box 跑在回环地址、全新数据库、合成数据，用 `-controller signal` 或 `none`），用无头 Chromium 截图，至少覆盖桌面 1440 宽和手机 390 宽、浅色和深色。容器里截中文要先装 CJK 字体（如 `fonts-noto-cjk`）。后台页面在内部容器里滚动，整页截图请用加高视口而不是 `captureBeyondViewport`。
4. 涉及配置写入时，在沙箱里用真实客户端连一次，确认改动后仍能连通。
5. 停掉沙箱进程：按 PID 停，不要用会匹配到自己命令行的 `pkill -f`。

## 部署（如果项目部署在你所在的机器上）

- 部署、重启、改防火墙或网关配置都会影响线上用户，**每一次都要用户明确同意**；之前同意过不代表这次也同意。
- 本机的部署手册不在仓库里（通常放在部署目录下，如 `DEPLOYMENT.md`），先读它，并核对 compose 当前用的镜像标签，确认线上正在跑的版本。
- 标准流程：
  1. 构建镜像时 `SINGBOX_IMAGE` 用线上 sing-box 同款镜像，`VERSION` 用有意义的标签。
  2. 部署前备份数据库、sing-box 配置和 compose 文件，并给当前镜像打 `before-<新标签>` 作为回滚点。
  3. 改 compose 的 `image:` 后 `docker compose up -d`。
  4. 验证：容器运行中、本地端口和公网地址 200、启动后一分钟内无错误日志、关键页面能打开。
  5. 把回滚方法（改回哪个镜像标签）告诉用户，并在部署手册里记一笔。
- 管理员通常经由同一个 sing-box 访问后台，sing-box 重载会切断他们的请求，这是预期行为。
- sing-box 热重载时如果端口绑定失败会直接退出，改端口类配置前务必先做端口检查。
- 不要删除备份、旧镜像或用户数据，除非用户明确要求。

## 文档

- 用户可见的变化写进 `CHANGELOG.md` 的「未发布」；发布时改成版本号和日期。
- 改了参数、部署方式或对 sing-box 的要求，同步更新 `README.md` 和 `README.zh-CN.md`（两份内容一致）。
- 改了模块职责或不变量，同步更新 `docs/architecture.md`。
- 不要另建进度表、需求草稿之类的临时文档放进仓库；过程性记录写在对话或提交说明里。

## 提交

- 只在用户要求时提交或推送；在默认分支上先建分支。
- 提交说明用英文祈使句，首行 ≤ 72 字符，正文说明「为什么」；格式参考 `feat:` / `fix:` / `docs:` / `chore:` / `test:` 前缀（发布说明会过滤 `docs:`、`chore:`、`test:`）。
- 不提交数据库文件、构建产物（`bin/`、`web/dist/*`）、密钥或任何线上数据。
