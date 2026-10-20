# sing-box-web

[中文](README.zh-CN.md) · English

A lightweight web admin UI for [sing-box](https://github.com/SagerNet/sing-box) that **manages your existing `config.json` instead of replacing it**.

Current version: **v1.0.1**, running stably on a single host with up to about twenty users.

## Features

| Page | What it does |
|---|---|
| Overview | Server health (CPU, memory, swap, disk, load), live throughput, sing-box status with reload / restart, new-version notices and one-click upgrades of the panel and its bundled sing-box, inbound summary, this month's traffic per user, recent activity |
| Users | Create, edit, disable, delete; expiry date and monthly quota, with automatic removal from the config when exceeded and restore on renewal; online status; subscription links, share links and QR codes |
| Traffic | Hourly, daily and monthly traffic by user / inbound / outbound with period comparison; a "Flows" Sankey of user → client IP → site → outbound (recording is opt-in); live "Connections", filter and close |
| Nodes | Inbounds: Hysteria2 and VLESS (including Reality) forms with one-click self-signed certificates and Reality keys; Outbounds: direct / SOCKS5 / HTTP / Shadowsocks / VLESS / Trojan / Hysteria2 forms (chaining, source address, name resolution); DNS: global settings and server forms, rules shown read-only. Other types are listed read-only |
| Settings | Account: login name, password, TOTP; Access: IP deny / allow lists for the proxy inbounds (nftables and HAProxy) and fail2ban bans (optional); Logs: live sing-box log and the panel's audit log |

`/sub/{token}` returns a Mihomo / Clash YAML, a sing-box JSON or base64 share links depending on the client's User-Agent; `?format=mihomo|singbox|base64|links` picks one explicitly.

The UI is available in English and Chinese, in light and dark themes, and works on phones.

## Principles

- **Non-invasive** – edits only the fields it manages (inbound users and the inbounds it manages) and keeps every hand-written field intact.
- **Safe writes** – every change runs `sing-box check`, is stored as a revision, swapped in atomically, reloaded and health-checked, and rolled back automatically on failure.
- **Least privilege** – in Docker, no `docker.sock`: reloads, restarts and firewall rules go through a host-side control unit that accepts a fixed set of actions and validates every argument. The one-line install runs the panel as root, with systemd letting it write only its data directory and the sing-box config directory.
- **Small** – a single Go binary with the UI embedded, about 30–40 MB resident.

## What sing-box needs

| For | Needs |
|---|---|
| Live throughput and connections | `experimental.clash_api` (listen on 127.0.0.1 and set a secret) |
| Traffic per user / inbound | `experimental.v2ray_api`; sing-box must be built with the `with_v2ray_api` tag (official images are not) |
| Live log and online status | `log.output` written to a file at level `info` or more verbose |

Features whose source is missing show as unavailable; everything else keeps working.

## Installation

### One-line install (recommended)

For Linux with systemd (amd64 / arm64), as root:

```bash
bash <(curl -fsSL https://github.com/axuezi/sing-box-web/releases/latest/download/install.sh)
```

The script:

- **Finds sing-box**: an existing systemd `sing-box.service` is used as is (both `-c` file and `-C` directory setups; an `ExecReload` is added if missing). Without one, it installs sing-box built with `with_v2ray_api` (compiled by this project from the official source and attached to every release). An existing sing-box without `with_v2ray_api` is never replaced; you only get a warning. If sing-box runs in Docker, use the Docker deployment below.
- **Prepares the config**: creates a minimal config if there is none; otherwise lists what it would add (a log file, the Clash API on loopback with a secret, the V2Ray stats API) and, once you agree, keeps a backup, validates with `sing-box check` and swaps it in. Existing fields are left alone.
- **Installs the panel**: binary in `/usr/local/bin/sing-box-web`, data in `/var/lib/sing-box-web`, settings in `/etc/default/sing-box-web`, running as the systemd service `sing-box-web` and controlling sing-box with `systemctl reload`.
- **Administrator**: a random password is generated on first install and shown once. Re-running the script upgrades and keeps data, settings and password.
- **One-click upgrades**: installs the root unit `sing-box-web-upgrade.path` / `.service`. When a new release is out, the Overview offers to upgrade the panel, and — if the script installed sing-box — the sing-box build attached to that release. The panel only drops a request; the unit downloads the assets (through `GH_PROXY` if set at install), verifies them against the release's `checksums.txt`, swaps them in, restarts and health-checks the service, and puts the old binary back if it does not come up. A new sing-box must first pass `sing-box check` on the current config; restarting it drops all proxy connections for a few seconds.

It asks where to listen: `127.0.0.1` only by default (reach it through an SSH tunnel or a reverse proxy with TLS), or a public address on a random port (plain HTTP, not recommended long term).

```bash
bash install.sh --version v1.0.1 --listen 127.0.0.1:9091 --yes   # pin version and address, no prompts
bash install.sh uninstall            # remove the panel (keeps data, leaves sing-box alone)
bash install.sh uninstall --purge    # also delete its data
```

If GitHub is hard to reach, set a download prefix: `GH_PROXY=https://<mirror>/ bash install.sh`. It is saved in `/etc/default/sing-box-web` and also used by one-click upgrades.

Forgot the password or lost the authenticator? Reset it on the server (`-reset-totp` also turns TOTP off): `sing-box-web passwd -data-dir /var/lib/sing-box-web -reset-totp`.

The one-line install uses the systemd controller; the access control page (nftables / HAProxy / fail2ban) needs the host-side control unit from the Docker deployment and is unavailable there.

### Docker

Run it on the same host as sing-box. The image bundles a sing-box binary for `sing-box check`; build it on top of the sing-box image you actually run:

```bash
docker build --build-arg SINGBOX_IMAGE=<your sing-box image> -t sing-box-web:v1.0.1 .
```

1. Install the host-side control unit: see [deploy/README.md](deploy/README.md).
2. Write a compose file based on [deploy/compose.example.yaml](deploy/compose.example.yaml) and start it.
3. Set the administrator password once after the first start:

   ```bash
   docker exec -it sing-box-web sing-box-web passwd -data-dir /data
   ```

4. Put a reverse proxy with TLS (Caddy, Nginx, …) in front; the panel itself listens on 127.0.0.1 only.

The Overview tells you when a new release is out; upgrade by rebuilding the image and recreating the container (one-click upgrades are for the one-line install).

### Options

Every flag can also be set as `SBW_<FLAG>` in the environment, e.g. `SBW_LISTEN`.

| Flag | Default | Meaning |
|---|---|---|
| `-listen` | `127.0.0.1:9091` | Address to listen on |
| `-data-dir` | `./data` | Directory for the SQLite database |
| `-singbox-config` | `/etc/sing-box/config.json` | sing-box config file |
| `-singbox-log` | `log.output` from the config | sing-box log file |
| `-controller` | `none` | How to control sing-box: `none` / `ctlfile` / `signal` / `systemd` |
| `-ctl-dir` | `/run/sing-box-web/ctl` | Directory shared with the host control unit (`ctlfile`) |
| `-systemd-unit` | `sing-box.service` | sing-box unit (`systemd`; the one-line install uses this) |
| `-pid-file` | empty | sing-box PID file (`signal`) |
| `-check-cmd` | `sing-box check -c {config}` | Command that validates a candidate config |
| `-auth-log` | empty | Append failed logins here for fail2ban |
| `-gateway-log` | empty | UDP address receiving a front TCP proxy's (e.g. HAProxy) connection log, to show real client IPs for inbounds behind it such as Reality |
| `-trusted-proxies` | `127.0.0.1/32,::1/128` | Reverse proxies allowed to set `X-Forwarded-*` |
| `-update-check` | `on` | Ask GitHub (api.github.com) every 6 hours for the latest stable sing-box and sing-box-web releases; `off` disables it (and with it one-click upgrades) |
| `-release-repo` | `axuezi/sing-box-web` | Repository whose releases are offered for upgrade |
| `-release-api` | `https://api.github.com` | GitHub API base URL for the update check, e.g. a mirror |
| `-upgrade-dir` | empty | Directory shared with the upgrade unit; set by the one-line install. Empty: new versions are only shown |

## Development

Requirements: Go 1.27+, Node.js 24+, make. AI contributors start with [AGENTS.md](AGENTS.md); internals are described in [docs/architecture.md](docs/architecture.md).

```bash
make web      # build the frontend into web/dist
make build    # compile bin/sing-box-web with the UI embedded
./bin/sing-box-web serve -listen 127.0.0.1:9091
```

Hot reload while working on the UI (two terminals):

```bash
make dev-api
make dev-web   # Vite on :5173, proxies /api to :9091
```

`make check` runs everything CI runs. See [CHANGELOG.md](CHANGELOG.md) for what changed.

## License

[GPL-3.0](LICENSE). sing-box is also GPL-3.0; release images that bundle a sing-box binary link to its source at <https://github.com/SagerNet/sing-box>.
