# 架构与设计说明

面向维护者和 AI 协作者：每个模块为什么这样做、有哪些不能破坏的约定。用法见 [README](../README.zh-CN.md)，协作规范见 [AGENTS.md](../AGENTS.md)。

## 总览

```
浏览器 ──HTTPS──> 反向代理 ──> sing-box-web（Go，内嵌 Vue 前端，SQLite）
                                   │ 读写 config.json、读日志
                                   │ Clash API / V2Ray API（127.0.0.1）
                                   │ 控制目录（请求文件）
                                   ▼
                     宿主机 sing-box-web-ctl ──> sing-box 容器 / nftables / HAProxy / fail2ban
```

- 单进程、单 SQLite 文件（`<data-dir>/sing-box-web.db`），无外部依赖服务。
- 后台容器只读根文件系统、去掉全部 capability、不挂 `docker.sock`。所有需要特权的操作写成请求文件交给宿主机控制单元。
- 一键安装（`install.sh`）是另一种形态：后台作为 systemd 服务以 root 运行，用 `systemd` 控制方式 `systemctl reload` sing-box；`ProtectSystem=strict` 只放开数据目录、日志目录和 sing-box 配置目录。安装脚本用 `sing-box-web prepare` 生成候选配置写到临时文件，由脚本备份、`sing-box check` 后替换——`prepare` 本身不写线上配置，运行中的后台仍只经 `internal/apply` 写入。

## 目录

| 路径 | 内容 |
|---|---|
| `cmd/sing-box-web` | 入口：`serve` / `passwd` / `prepare` / `version`，参数与 `SBW_*` 环境变量，后台任务的启动 |
| `internal/server` | HTTP 路由、鉴权中间件、各页面接口、内嵌静态资源（gzip + 长缓存） |
| `internal/apply` | 配置写入流水线（见下） |
| `internal/protocol` | 各入站类型的用户写法；`RenderManaged` 把数据库中的用户渲染进配置 |
| `internal/inbounds` | Hysteria2 / VLESS 入站表单与配置之间的映射，只动表单管的字段 |
| `internal/outbounds` | 常见出站类型的表单映射、引用查找（路由、DNS、经由、规则集下载等）、经由循环检测 |
| `internal/dnsconf` | DNS 全局设置与 1.12+ 格式服务器的表单映射、引用查找、DNS 规则摘要 |
| `internal/release` | 定期查询 GitHub 上 sing-box 与 sing-box-web 的最新稳定版，供概览提示和一键升级 |
| `internal/upgrade` | 一键升级：向宿主机升级单元投递请求、读取升级状态 |
| `internal/subscription` | 订阅：Mihomo YAML、sing-box JSON、分享链接 |
| `internal/limits` | 到期与流量额度：每分钟检查，超限移出配置、解除后恢复 |
| `internal/traffic` | V2Ray 统计采集、按天 / 小时落盘；实时速率采样 |
| `internal/presence` | 读 sing-box 日志，把连接对应到用户（在线状态） |
| `internal/flows` | 访问流向：Clash 连接增量按小时聚合 |
| `internal/gateway` | 接收前置 TCP 代理的连接日志，还原真实客户端 IP |
| `internal/access` | IP 黑白名单与 fail2ban，经控制单元下发 |
| `internal/sysinfo` | 读 `/proc` 与 statfs 得到服务器状态 |
| `install.sh`, `scripts/build-sing-box.sh` | 一键安装脚本；发布时编译带 `with_v2ray_api` 的 sing-box |
| `internal/control` | 控制 sing-box：`none` / `ctlfile` / `signal` / `systemd` |
| `internal/clashapi`, `internal/v2rayapi` | 两个 API 的最小客户端 |
| `internal/singbox`, `internal/logtail` | 读配置、跟随日志文件 |
| `internal/store` | SQLite 访问与迁移（`migrations/NNN_*.sql`，只增不改） |
| `internal/auth` | argon2id、登录限速、TOTP、令牌 |
| `web/src` | Vue 3 + TypeScript + Naive UI。`views/<页面>/` 每个菜单页一个目录（overview、users、traffic、nodes、settings），里面放页面本身、它的标签页（`*Tab.vue`）和只在这里用到的抽屉 / 弹窗；`views/LoginView.vue` 登录页；`components/` 只放多个页面共用的组件，`components/charts/` 自绘图表；`composables/` 工具；`locales/` 中英文案；`styles/viz.css` 图表色板 |
| `deploy/` | compose 示例、宿主机控制单元、fail2ban 文件 |

## 配置写入（`internal/apply`）

所有对 `config.json` 的修改都必须走 `Apply.Update(change, build, commit)`，不允许任何地方直接写文件。

1. 读取当前配置，`build` 基于它生成候选配置（用 gjson / sjson 局部修改，保留其余字段和顺序）。
2. 候选与当前相同则返回 `unchanged`，不重载。
3. 用 `-check-cmd` 校验候选；失败返回 `config.rejected` 并带上输出。
4. 写入版本记录（`revisions` 表）和恢复记录，原子替换文件，通过控制器重载。
5. 健康检查（控制器报告运行中、启用了 Clash API 时能访问、日志里没有新的致命或重载错误）通过后执行 `commit`（数据库改动），与版本状态在同一事务内提交。
6. 任一步失败：恢复旧文件、重载、再检查；数据库改动全部撤销。恢复本身失败时报 `recovery_failed`，不会声称已回滚。
7. 进程中途退出时，下次启动先根据恢复记录处理未完成的操作，再启动采集和限额任务。

应用过程与 HTTP 请求解耦：管理员的浏览器往往经由同一个 sing-box 访问后台，重载会切断请求；前端把网络错误当作「已断开，稍后核对结果」处理。

## 用户与入站

- 用户存在数据库里，配置中的入站用户由 `protocol.RenderManaged` 根据数据库生成；启用、到期、超额都通过「是否渲染进配置」实现。
- 首次接管时，配置里已有的用户导入为数据库用户：各入站中同名的合并为一个用户，无名称的按位置命名为 `user-1`、`user-2`……
- VLESS 流控存在数据库，不向 sing-box JSON 写私有字段。
- 入站表单只改它认识的字段，transport、sniff 等原样保留；读取后原样保存必须零改动（有测试）。改端口前会检查同配置冲突和宿主机端口占用（读 `/proc/net`），避免 sing-box 重载时绑定失败退出。

## 流量与限额

- 实时速率（概览数字、实时曲线、峰值）来自 Clash API 的累计上下行，每秒读一次，取最近 5 秒的平均（`traffic.RateWindow`）。sing-box 在把数据交给发往客户端的连接时就计数，而 Hysteria2 的 QUIC 缓冲区可达几十 MB：客户端网速慢时，按秒看会出现几 MB/s 的尖峰和连续数秒的 0。实测不限速下载时每秒读数误差约 ±21%、5 秒平均约 ±14%、累计总量误差 0.4%；客户端限速 1 MiB/s 时每秒读数误差可达 260%，5 秒平均消除了读数为 0 的情况。
- 同一个计数位置也意味着：下载中途取消时，已进入缓冲区但客户端没收到的数据也会计入用户流量（取决于客户端网速，可达几十 MB），这是 sing-box 计数方式决定的。

- 采集器每分钟读 V2Ray 统计，**只读不清零**：保存读数和 sing-box 启动时间，下次相减得增量；启动时间变化说明计数归零。读取期间发生重载则跳过本轮。
- 按天数据保留 400 天，按小时 62 天，按月由按天汇总。
- 跨月或跨手动重置的增量按时间比例分摊到新周期（采样估算）。
- 限额每分钟检查，另外每次采集完成后由采集器立即触发一次（`Collector.AfterCollect` → `Enforcer.Kick`，非阻塞、可合并），超额最多约 1 分钟后生效。注意触发只来自周期采集：配置变更前的 `Flush` 在应用流程里运行，不能反过来触发限额检查。被限额的用户仍可拉订阅，响应带 `Subscription-Userinfo`。

## 出站与 DNS 表单

- 与入站相同的约定：读取时遮掩凭据（发回遮掩值即保留原值），写入只改表单字段，读出原样保存零改动（含 `domain_resolver` 的字符串 / 对象两种写法、`alpn` 字符串写法、未写指纹的 uTLS）。
- 标签创建后不可改。删除前用 `References` 查引用：路由规则（含逻辑规则嵌套）、`route.final`（未设置时第一个出站即默认）、DNS 规则的 `outbound` 匹配、各处 `detour`、selector / urltest 的成员与默认、规则集 `download_detour`、Clash API 面板下载经由；DNS 服务器还查 `dns.final`（未设置时第一个即默认）、DNS 规则、各处 `domain_resolver` 与 `route.default_domain_resolver`。
- 经由（detour）与地址解析（domain_resolver）在写入前检查目标存在且不成环。
- DNS 规则只做摘要展示，不提供编辑（与路由规则一样由用户在 config.json 维护）。sing-box 1.14 起 route 动作里的 `strategy` 已弃用；实测它与较新的规则写法（如 `query_type`）同时出现时，`sing-box check` 会直接报错，所以页面会提示迁移。

## 版本提示与一键升级（`internal/release`、`internal/upgrade`）

- 启动 30 秒后及每 6 小时请求一次 GitHub `releases/latest`（只取稳定版），缓存在内存；sing-box 上游和本项目各一个检查器。`/api/status` 在 sing-box 最新版比运行中的版本新时附带 `runtime.update`。版本比较只看 `x.y.z`，同号时运行中的预发布版视为更旧。`-update-check off` 可完全关闭对外请求。
- 本项目的发布说明末尾有发布流程写入的 HTML 注释 `<!-- sing-box-v2api: vX.Y.Z -->`，标明随版本附带的 sing-box 版本；后台据此判断内核能否升级，不需要下载任何文件。
- 一键升级只在一键安装下可用（`-upgrade-dir` 非空）。后台不替换任何二进制（`ProtectSystem=strict` 下它也写不了 `/usr/local/bin`），只把一行 `panel vX.Y.Z` 或 `sing-box vX.Y.Z` 写进 `<upgrade-dir>/requests`，与 ctlfile 相同；`sing-box-web-upgrade.path` 触发以 root 运行的 `deploy/upgrade/sing-box-web-upgrade`。版本号取自后台自己的版本检查，不接受浏览器传入；脚本仍按白名单校验目标和 `vX.Y.Z` 格式，超过 1 分钟的请求直接丢弃。
- 脚本按同一版本的 `checksums.txt` 校验 sha256，旧二进制保存在 `/var/lib/sing-box-web-upgrade`（root 专用）。后台：替换、重启后 30 秒内 `/api/health` 必须报告新版本号，否则换回；内核：只限一键安装装的（数据目录有 `.installed-sing-box`），新内核须带 `with_v2ray_api` 并通过对当前配置的 `sing-box check`，重启后 13 秒内必须保持运行且未被 systemd 重启过，否则换回。
- 升级后台会重启后台本身，所以没有同步应答：脚本把进度和结果写到 `<upgrade-dir>/status/state`（`key=value`），后台读取后显示，并在结果出来后写一次审计日志（升级单元写不了数据库）。请求 90 秒无人处理、或运行超过 20 分钟未结束，都显示为失败。
- Docker 部署不设 `-upgrade-dir`，只提示新版本和手动升级方式。签名校验（cosign / minisign）暂未做，完整性只依赖同一 release 的 sha256。

## 在线状态与访问流向

- Clash API 的连接不带用户名，经 HAProxy 的连接来源都是 127.0.0.1。`presence` 持续读 sing-box 日志，按「入站 + 来源地址」记下用户名，再与实时连接匹配；启动时回读最近日志。日志级别高于 info 时，多用户入站会显示「未知」。
- 访问流向每秒轮询 Clash 连接，按小时聚合到 `traffic_flows`，保留 31 天，默认关闭（设置项 `flows_enabled`）。数据是用户的浏览记录，属于隐私数据。

## 真实客户端 IP（`internal/gateway`）

sing-box 不再支持 PROXY 协议。前置 HAProxy 按 SNI 分流到 sing-box 时，用 `option logasap` 把每条连接的「客户端地址 + 转发用的本地端口」以 UDP 日志发给后台（格式见包注释），后台据此把 127.0.0.1:端口 还原成真实 IP。

## 访问控制（`internal/access`）

- 只限制代理入站；网站、后台、SSH 不受影响。
- 直接对外监听的入站（如 Hysteria2 UDP）在宿主机 nftables 独立表 `inet sbw_access` 执行；经 HAProxy SNI 分流的入站（如 Reality）在 HAProxy 用名单文件执行，更新后 USR2 平滑重载。
- 后台只写规则描述文件，控制单元逐行校验后用固定模板生成规则并原子替换。到期由后台每分钟检查并重新下发；规则存数据库，重启后自动重新下发。
- 开启白名单后 60 秒内必须确认，否则自动关闭；不允许封禁当前管理员 IP。

## 安全

- 单管理员，argon2id；会话 Cookie 12 小时；同一 IP 15 分钟内失败 5 次锁定 15 分钟，失败写入 `-auth-log` 交给 fail2ban；可选 TOTP。
- 写请求要求 CSRF 令牌；只信任 `-trusted-proxies` 设置的 `X-Forwarded-*`。
- 响应带 CSP、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`。
- 所有改动写审计日志（`audit_log`），对象记名称而不是 ID。

## 前端约定

- 页面状态（筛选、范围）写在 URL 查询参数里，可收藏、可分享。
- 主导航只有 5 项（概览、用户、流量、节点配置、设置），其余功能是这些页面里的标签页，用 `?tab=`（流量页用 `?kind=`，设置 → 日志里的子标签用 `?log=`）记住位置。旧地址 `/inbounds`、`/outbounds`、`/connections`、`/access`、`/logs` 在 `router.ts` 里保留同名路由并重定向到对应标签，链接可以继续用这些路由名。
- 窄屏（≤ 767px，`useIsMobile`）表格改为 `MobileCards` 卡片或两行列表；图表在窄屏下保证文字不重叠。
- 图表全部自绘 SVG，颜色只用 `styles/viz.css` 的令牌（`--viz-cat-1..6`、`--viz-other` 等），明暗主题各一套。
- 所有界面文案放在 `locales/zh-CN.ts` 与 `locales/en.ts`，两边键完全一致。
