# sing-box-web

中文 · [English](README.md)

一个轻量的 [sing-box](https://github.com/SagerNet/sing-box) Web 管理后台：**管理你现有的 `config.json`，而不是接管它**。

当前版本 **v1.0.0**，已在单机、二十人以内的规模上稳定运行。

## 功能

| 页面 | 能做什么 |
|---|---|
| 概览 | 服务器状态（CPU、内存、Swap、磁盘、负载）、实时速率、sing-box 运行状态与重载 / 重启、sing-box 版本提示、入站摘要、本月用户流量、最近操作 |
| 用户 | 新建、编辑、停用、删除；到期时间与每月流量额度，到期或超额自动移出配置、续期后自动恢复；在线状态；订阅链接、分享链接与二维码 |
| 流量 | 按用户 / 入站 / 出站查看小时、天、月流量，可与上一周期等对比；「访问流向」桑基图展示用户 → 客户端 IP → 网站 → 出站（需手动开启记录）；「连接」实时连接列表，可筛选、断开 |
| 节点配置 | 入站：Hysteria2 / VLESS（含 Reality）表单，自签证书与 Reality 密钥一键生成；出站：direct / SOCKS5 / HTTP / Shadowsocks / VLESS / Trojan / Hysteria2 表单（经由、源地址、域名解析）；DNS：全局设置与服务器表单，规则只读展示。其他类型只读列出 |
| 设置 | 账户：登录名、密码、TOTP 二步验证；访问控制：代理入站的 IP 黑名单 / 白名单（nftables 与 HAProxy）与 fail2ban 封禁（可选）；日志：sing-box 实时日志与后台审计日志 |

订阅地址 `/sub/{token}` 按客户端 User-Agent 自动返回 Mihomo / Clash YAML、sing-box JSON 或 base64 分享链接，也可用 `?format=mihomo|singbox|base64|links` 指定。

界面支持中文和英文、浅色和深色，手机上可正常使用。

## 设计原则

- **不接管配置**：只改它负责的字段（入站用户、它管理的入站），手写的其他字段原样保留。
- **安全写入**：每次修改先用 `sing-box check` 校验，存一份版本再原子替换、重载并做健康检查；失败自动回滚。
- **最小权限**：Docker 部署不挂 `docker.sock`，重载、重启、防火墙规则通过宿主机上的控制单元执行，它只接受固定几种动作且逐项校验参数。一键安装时后台以 root 运行，但 systemd 只允许它写自己的数据目录和 sing-box 配置目录。
- **小**：单个 Go 二进制内嵌前端，常驻内存约 30–40 MB。

## 对 sing-box 的要求

| 用途 | 需要 |
|---|---|
| 实时速率、连接列表 | `experimental.clash_api`（建议只监听 127.0.0.1 并设 secret） |
| 按用户 / 入站流量统计 | `experimental.v2ray_api`，sing-box 需用 `with_v2ray_api` 构建标签编译（官方镜像不带） |
| 实时日志、用户在线状态 | `log.output` 写到文件，级别 `info` 或更详细 |

缺少某项时，对应功能显示为不可用，其余照常工作。

## 安装

### 一键安装（推荐）

适用于使用 systemd 的 Linux（amd64 / arm64），以 root 运行：

```bash
bash <(curl -fsSL https://github.com/axuezi/sing-box-web/releases/latest/download/install.sh)
```

脚本会：

- **检测 sing-box**：已有 systemd 管理的 `sing-box.service` 就直接使用（支持 `-c` 文件和 `-C` 目录两种写法，缺少 `ExecReload` 时补一个）；没有就安装带 `with_v2ray_api` 的 sing-box（由本项目从官方源码编译，随每个版本发布）。现有 sing-box 不带 `with_v2ray_api` 时只给出提示，不会替换。sing-box 跑在 Docker 里时请改用下面的 Docker 部署。
- **准备配置**：没有配置时创建一份最小配置；已有配置时列出需要添加的项（日志文件、只监听本机并带 secret 的 Clash API、V2Ray 统计 API），确认后先备份、经 `sing-box check` 校验再替换，已有字段不动。
- **安装后台**：二进制装到 `/usr/local/bin/sing-box-web`，数据在 `/var/lib/sing-box-web`，参数在 `/etc/default/sing-box-web`，以 systemd 服务 `sing-box-web` 运行并通过 `systemctl reload` 控制 sing-box。
- **管理员**：首次安装生成随机密码并只显示一次；重复运行即升级，保留数据、参数和密码。

安装时会询问监听地址：默认只监听 `127.0.0.1`（通过 SSH 隧道或带 TLS 的反向代理访问），也可以选择公网地址加随机端口（明文 HTTP，不推荐长期使用）。

```bash
bash install.sh --version v1.0.0 --listen 127.0.0.1:9091 --yes   # 指定版本、监听地址，不询问
bash install.sh uninstall            # 卸载后台（保留数据，sing-box 不动）
bash install.sh uninstall --purge    # 连同数据一起删除
```

无法直接访问 GitHub 时，可以设置下载前缀：`GH_PROXY=https://<镜像>/ bash install.sh`。

一键安装使用 systemd 控制方式，「访问控制」页（nftables / HAProxy / fail2ban）需要下面 Docker 部署里的宿主机控制单元，在一键安装中不可用。

### Docker 部署

后台与 sing-box 跑在同一台机器上。镜像内置了 sing-box 二进制，用于在写入前执行 `sing-box check`；构建时请用与线上一致的 sing-box 镜像作为基础：

```bash
docker build --build-arg SINGBOX_IMAGE=<你的 sing-box 镜像> -t sing-box-web:v1.0.0 .
```

1. 安装宿主机控制单元：见 [deploy/README.md](deploy/README.md)。
2. 参照 [deploy/compose.example.yaml](deploy/compose.example.yaml) 写 compose 文件并启动。
3. 首次启动后设置管理员密码：

   ```bash
   docker exec -it sing-box-web sing-box-web passwd -data-dir /data
   ```

4. 在后台前面放一个带 TLS 的反向代理（Caddy、Nginx 等），后台本身只监听 127.0.0.1。

### 常用参数

每个参数都可以用环境变量 `SBW_<大写参数名>` 设置，例如 `SBW_LISTEN`。

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-listen` | `127.0.0.1:9091` | 监听地址 |
| `-data-dir` | `./data` | SQLite 数据库目录 |
| `-singbox-config` | `/etc/sing-box/config.json` | sing-box 配置文件 |
| `-singbox-log` | 配置里的 `log.output` | sing-box 日志文件 |
| `-controller` | `none` | 控制方式：`none` / `ctlfile` / `signal` / `systemd` |
| `-ctl-dir` | `/run/sing-box-web/ctl` | 与宿主机控制单元共享的目录（`ctlfile`） |
| `-systemd-unit` | `sing-box.service` | sing-box 的 systemd 单元（`systemd`，一键安装使用此方式） |
| `-pid-file` | 空 | sing-box 的 PID 文件（`signal`） |
| `-check-cmd` | `sing-box check -c {config}` | 写入前的校验命令 |
| `-auth-log` | 空 | 登录失败写入此文件，供 fail2ban 使用 |
| `-gateway-log` | 空 | 接收前置 TCP 代理（如 HAProxy）连接日志的 UDP 地址，用于显示 Reality 等经代理入站的真实客户端 IP |
| `-trusted-proxies` | `127.0.0.1/32,::1/128` | 允许设置 `X-Forwarded-*` 的反向代理 |
| `-update-check` | `on` | 每 6 小时向 GitHub（api.github.com）查询 sing-box 最新稳定版，只用于提示；`off` 关闭 |

## 开发

需要 Go 1.27+、Node.js 24+、make。AI 协作者请先读 [AGENTS.md](AGENTS.md)，内部设计见 [docs/architecture.md](docs/architecture.md)。

```bash
make web      # 构建前端到 web/dist
make build    # 编译 bin/sing-box-web（内嵌前端）
./bin/sing-box-web serve -listen 127.0.0.1:9091
```

开发前端时热更新（两个终端）：

```bash
make dev-api
make dev-web   # Vite 监听 :5173，/api 代理到 :9091
```

`make check` 会执行 CI 的全部检查。版本变化见 [CHANGELOG.md](CHANGELOG.md)。

## 许可证

[GPL-3.0](LICENSE)。sing-box 同为 GPL-3.0；附带 sing-box 二进制的发布镜像会注明其源码地址 <https://github.com/SagerNet/sing-box>。
