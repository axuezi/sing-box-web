# 更新日志

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [未发布]

### 新增

- 后台一键升级（一键安装）：概览页显示管理后台的新版本和更新说明，可一键升级；sing-box 由一键安装脚本安装时，还可升级到随版本发布的 sing-box（带 V2Ray API）。升级由新增的宿主机单元 `sing-box-web-upgrade.path` / `.service` 以 root 执行：按同一版本的 `checksums.txt` 校验 sha256，替换后重启并做健康检查，失败自动换回旧版本；新内核要先通过对当前配置的 `sing-box check`。下载沿用安装时的 `GH_PROXY`。升级请求和结果写入审计日志。Docker 部署只提示新版本和升级方式。已有的一键安装重新执行一次新版 `install.sh` 后即可使用。（#8）
- 新参数 `-upgrade-dir`、`-release-repo`、`-release-api`；`-update-check` 现在同时检查 sing-box-web 的新版本。

### 变更

- 默认 Mihomo 订阅模板重写（未自定义模板的用户更新订阅后生效）：
  - 新增 `AI` 分组（url-test 自动选可用的最快节点，节点全部不可用时拒绝而不是直连），ChatGPT / OpenAI、Claude / Anthropic、xAI / Grok 和 `geosite:category-ai-!cn` 走该分组，并经它用海外 DNS 解析。
  - 默认 DNS 改为国内 DoH。
  - geosite / geoip 改从 jsDelivr 下载并每日自动更新（GitHub 在国内经常连不上，规则数据缺失会导致国内网站也走代理）。
  - 局域网地址（`GEOIP,private`、`.lan`、`.local`）、SSH（22 端口）和微信直连。
  - Apple 只有国内 CDN（`geosite:apple-cn`）直连，其余 Apple 服务走代理。

### 修复

- 删除用户时一并删除其流量统计（按天、按小时）、访问流向和订阅拉取记录。以前这些数据会留下，流量页里显示为「已删除用户 #id」。删除确认框里有相应提示。
- 流量统计连接泄漏：每次采集（默认每分钟）都会向 sing-box 的 V2Ray 统计接口新开一条 HTTP/2 连接且从不关闭，运行几天后累积上千条连接，sing-box 和后台的内存随之持续上涨。现在所有查询共用一条连接，空闲 5 分钟自动关闭。

## [1.0.1] - 2026-10-15

### 新增

- 自定义 Mihomo 订阅模板：入站页可编辑 Clash Verge 等 Mihomo 客户端拿到的完整配置，面板只填 `{{proxies}}` 和 `{{proxy_names}}`，端口、DNS、TUN、策略组和规则由模板决定；必须包含单独一行的 `{{proxies}}`，最大 32 KB；可一键恢复默认，未设置时输出与以前完全相同。分享链接（Shadowrocket 等）和 sing-box 格式不受影响。（#1）

### 修复

- Clash Verge 导入订阅后配置名显示为 `\"用户名.yaml\` 的问题：`Content-Disposition` 的 `filename` 不再加引号（Clash Verge 的解析会把引号和反斜杠留在名称里），中文等特殊字符的用户名放在 `filename*`（RFC 5987），并去掉 `.yaml` 等后缀，客户端里只显示用户名。
- 流量统计：系统时钟跳变（NTP 校时、虚拟机挂起、手动改时间）不再被误判为 sing-box 重启，避免重复记账和误触发流量额度。改为依据 sing-box 的运行时长回退或计数器变小判断重启。（#6）
- IP 访问规则：周期同步与管理员修改同时下发时串行执行，旧规则不会覆盖新规则；规则文件改用唯一的临时文件名。（#7）

### 安全

- 登录：排队等待密码校验的并发请求在 IP 被锁定后不再继续校验，「15 分钟 5 次失败锁定」无法再用并发绕过。（#2）
- 二步验证：已开启时不能只凭会话重新绑定密钥，必须先用密码和当前验证码关闭；开启时用过的验证码不能再用于登录。新增 `passwd -reset-totp`，丢失验证器时可在服务器上关闭 TOTP。（#3）
- 宿主机控制单元：写结果时不再跟随指向目录的符号链接（`mv -T`）。已部署的 `/usr/local/libexec/sing-box-web-ctl` 需要手动更新。（#4）

## [1.0.0] - 2026-10-14

第一个正式版本。

### 功能

- **账户与安全**：单管理员，argon2id 密码，会话 12 小时；登录失败限速并可交给 fail2ban；TOTP 二步验证；可修改登录用户名；全部操作写审计日志。
- **用户**：新建、编辑、停用、删除，分别重置密码 / UUID；接管现有配置中的用户；到期时间与每月流量额度，到期或超额自动移出配置（每次流量采集后立即检查，超额最多约 1 分钟后停用），续期、重置流量或到重置日后自动恢复；在线状态与最近活动。
- **订阅**：令牌订阅地址，按 User-Agent 返回 Mihomo / Clash YAML、sing-box JSON 或 base64；分享链接与二维码；订阅访问记录；`Subscription-Userinfo` 用量头。
- **入站**：Hysteria2 / VLESS（含 Reality）结构化表单，自签证书、Reality 密钥与 short ID 一键生成，端口冲突与宿主机端口占用预检。
- **出站与 DNS**：direct、SOCKS5、HTTP、Shadowsocks、VLESS（含 Reality）、Trojan、Hysteria2 出站表单，可设经由（串联代理）、源地址、网卡、域名解析服务器与策略；DNS 默认服务器、解析策略、缓存，以及 https / tls / quic / h3 / udp / tcp / local 服务器的增删改。只改表单管的字段，原样保存零改动；标签创建后不可改；仍被引用的不能删并列出引用位置；拒绝经由 / 解析循环。DNS 规则只读展示，并提示 sing-box 1.14 起弃用的 `strategy` 写法。
- **配置写入**：校验 → 存版本 → 原子替换 → 重载 → 健康检查 → 失败自动回滚；与浏览器请求解耦；进程中断后启动时自动恢复。
- **观测**：服务器状态（CPU、内存、Swap、磁盘、负载及提示）、实时速率（5 秒平均，避免缓冲造成的尖峰和 0）、连接列表与断开、sing-box 实时日志；sing-box 版本提示（每 6 小时查询 GitHub 最新稳定版，有新版本时显示「新版本 x.y.z」，否则显示「已是最新」；只提示不升级，`-update-check off` 关闭）。
- **流量分析**：按用户 / 入站 / 出站的小时、天、月流量，与上一周期 / 上周 / 上月同期 / 自定义日期对比；访问流向桑基图（默认关闭，保留 31 天）。
- **访问控制**：代理入站的 IP 黑名单 / 白名单（nftables + HAProxy），带到期、白名单开启确认与防自封；fail2ban 封禁管理。
- **真实客户端 IP**：通过前置 HAProxy 的连接日志还原经 SNI 分流入站的真实来源。
- **安装**：一键安装脚本 `install.sh`，检测已有的 systemd sing-box，没有时安装随版本发布、带 `with_v2ray_api` 的 sing-box（`sing-box-v2api_linux_<arch>.tar.gz`）；准备配置（日志文件、Clash API、V2Ray 统计 API，先备份并经 `sing-box check` 校验）；以 systemd 服务安装后台并生成管理员密码；支持升级与卸载。也可用 Docker 部署。
- **界面**：主菜单 5 项（概览、用户、流量、节点配置、设置），其余功能为页内标签页，旧地址自动跳转；中文 / 英文、浅色 / 深色，手机适配；页面状态写在网址里。

### 已知限制

- 路由规则和 DNS 规则没有编辑页面，需手动编辑 `config.json`；入站表单不含 ACME 证书和 transport（WS / gRPC 等）；WireGuard 等 endpoints 和 selector / urltest 出站只读列出。
- 配置变更历史只在数据库 `revisions` 表中保留，没有回滚页面；需要手动回滚时请 SSH 处理。
- 超额停用最多延迟约 1 分钟（流量每分钟采集一次）。
- Docker 镜像约 285 MB（内置 sing-box 二进制用于 `sing-box check`）。
- 按小时的流量数据保留 62 天，按天 400 天。

### 计划

- 一键导出加密备份包（配置 + 证书 + 数据库），以及数据库定时备份
- 把沙箱端到端测试和截图脚本放进仓库，作为回归测试
- 发布流程：GHCR 镜像（`.goreleaser.yaml` 目前只出二进制）

[未发布]: https://github.com/axuezi/sing-box-web/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/axuezi/sing-box-web/releases/tag/v1.0.0
