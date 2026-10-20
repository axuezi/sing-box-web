#!/usr/bin/env bash
# sing-box-web installer: installs the panel as a systemd service next to a
# systemd-managed sing-box, installing sing-box (built with with_v2ray_api)
# only when there is none. Re-running it upgrades the panel and keeps its
# settings and data.
#
#   bash <(curl -fsSL https://github.com/axuezi/sing-box-web/releases/latest/download/install.sh)
#   bash install.sh [install] [--version vX.Y.Z] [--listen ADDR] [--yes]
#   bash install.sh uninstall [--purge]
#
# Environment: GH_PROXY  prefix for github.com downloads, e.g. https://mirror.example/
#              SBW_REPO  owner/repo to download from (default axuezi/sing-box-web)
set -euo pipefail

REPO=${SBW_REPO:-axuezi/sing-box-web}
GH_PROXY=${GH_PROXY:-}

SBW_BIN=/usr/local/bin/sing-box-web
SBW_DATA=/var/lib/sing-box-web
SBW_ENV=/etc/default/sing-box-web
SBW_UNIT=/etc/systemd/system/sing-box-web.service
SBW_LOGDIR=/var/log/sing-box-web
SBW_UPGRADE_DIR=$SBW_DATA/upgrade
UPG_BIN=/usr/local/libexec/sing-box-web-upgrade
UPG_UNIT=/etc/systemd/system/sing-box-web-upgrade

SB_UNIT=sing-box.service
SB_BIN_DEFAULT=/usr/local/bin/sing-box
SB_CONF_DEFAULT=/etc/sing-box/config.json
SB_LOG=/var/log/sing-box/sing-box.log
SB_DROPIN=/etc/systemd/system/sing-box.service.d/10-sing-box-web.conf
SB_LOGROTATE=/etc/logrotate.d/sing-box-web
INSTALLED_SB_MARK=$SBW_DATA/.installed-sing-box

# --- output ------------------------------------------------------------------

ZH=0
case "${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}" in zh*) ZH=1 ;; esac
# say "english" "中文"
say() { if [ "$ZH" = 1 ]; then printf '%s\n' "$2"; else printf '%s\n' "$1"; fi; }
info() { printf '\033[1;34m==>\033[0m '; say "$@"; }
warn() { printf '\033[1;33m!!\033[0m ' >&2; say "$@" >&2; }
die() { printf '\033[1;31mxx\033[0m ' >&2; say "$@" >&2; exit 1; }

YES=0
# ask "question" "问题" default(y|n) -> 0 for yes
ask() {
  local def=$3 ans hint="[y/N]"
  [ "$def" = y ] && hint="[Y/n]"
  if [ "$YES" = 1 ] || [ ! -r /dev/tty ]; then [ "$def" = y ]; return; fi
  printf '%s %s ' "$(say "$1" "$2")" "$hint" >/dev/tty
  read -r ans </dev/tty || ans=""
  case "${ans:-$def}" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

# --- helpers -----------------------------------------------------------------

TMP=""
cleanup() { [ -n "$TMP" ] && rm -rf "$TMP"; }
trap cleanup EXIT

download() { # url dest
  curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "${GH_PROXY}$1" ||
    die "Download failed: ${GH_PROXY}$1" "下载失败：${GH_PROXY}$1"
}

release_url() { # asset
  if [ -n "$VERSION" ]; then
    echo "https://github.com/$REPO/releases/download/$VERSION/$1"
  else
    echo "https://github.com/$REPO/releases/latest/download/$1"
  fi
}

# fetch_asset name -> extracts into $TMP/<name without .tar.gz>/ after
# checking it against checksums.txt from the same release.
fetch_asset() {
  local name=$1 dir="$TMP/${1%.tar.gz}"
  [ -f "$TMP/checksums.txt" ] || download "$(release_url checksums.txt)" "$TMP/checksums.txt"
  download "$(release_url "$name")" "$TMP/$name"
  local want got
  want=$(awk -v n="$name" '$2 == n || $2 == "*"n {print $1}' "$TMP/checksums.txt")
  [ -n "$want" ] || die "$name is not in checksums.txt" "checksums.txt 中没有 $name"
  got=$(sha256sum "$TMP/$name" | awk '{print $1}')
  [ "$want" = "$got" ] || die "Checksum mismatch for $name" "$name 校验和不匹配"
  mkdir -p "$dir"
  tar -xzf "$TMP/$name" -C "$dir"
}

port_in_use() { ss -Htln "sport = :$1" 2>/dev/null | grep -q .; }

random_port() {
  local p
  for _ in $(seq 50); do
    p=$(( (RANDOM << 15 | RANDOM) % 40000 + 20000 ))
    port_in_use "$p" || { echo "$p"; return; }
  done
  die "No free port found" "找不到空闲端口"
}

has_v2ray_api() { "$1" version 2>/dev/null | grep -q 'with_v2ray_api'; }

# --- checks ------------------------------------------------------------------

preflight() {
  [ "$(id -u)" = 0 ] || die "Run as root." "请用 root 运行。"
  [ "$(uname -s)" = Linux ] || die "Linux only." "只支持 Linux。"
  [ -d /run/systemd/system ] || die "systemd is required. For Docker, see deploy/README.md." \
    "需要 systemd。Docker 部署请看 deploy/README.md。"
  case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "Unsupported CPU: $(uname -m) (amd64 and arm64 only)" "不支持的 CPU：$(uname -m)（只支持 amd64、arm64）" ;;
  esac
  local missing=""
  for c in curl tar sha256sum ss awk; do command -v "$c" >/dev/null || missing="$missing $c"; done
  [ -z "$missing" ] || die "Missing commands:$missing" "缺少命令：$missing"
}

# detect_singbox sets SB_STATE (none|unit|binary), SB_BIN, SB_CONF and
# SB_USER, or stops when sing-box runs in a way the panel cannot manage.
detect_singbox() {
  SB_STATE=none SB_BIN="" SB_CONF="" SB_USER=""

  if command -v docker >/dev/null 2>&1 &&
    docker ps --format '{{.Image}} {{.Names}}' 2>/dev/null | grep -qi 'sing-box'; then
    die "sing-box runs in Docker here. Use the Docker deployment instead: https://github.com/$REPO/blob/main/deploy/README.md" \
      "这台机器的 sing-box 跑在 Docker 里，请改用 Docker 部署：https://github.com/$REPO/blob/main/deploy/README.md"
  fi

  if [ "$(systemctl show -p LoadState --value "$SB_UNIT" 2>/dev/null)" = loaded ]; then
    SB_STATE=unit
    local argv
    argv=$(systemctl show -p ExecStart --value "$SB_UNIT" | sed -n 's/.*argv\[\]=\([^;]*\);.*/\1/p' | head -n1)
    [ -n "$argv" ] || die "Cannot read ExecStart of $SB_UNIT." "读不到 $SB_UNIT 的 ExecStart。"
    # shellcheck disable=SC2086
    set -- $argv
    SB_BIN=$1
    shift
    local conf="" confdir="" workdir=""
    while [ $# -gt 0 ]; do
      case "$1" in
        -c | --config) [ -z "$conf" ] || die "$SB_UNIT uses several -c files; the panel manages one config.json." \
          "$SB_UNIT 用了多个 -c 配置文件，面板只能管理单个 config.json。"
          conf=$2; shift ;;
        -C | --config-directory) confdir=$2; shift ;;
        -D | --directory) workdir=$2; shift ;;
      esac
      shift
    done
    if [ -n "$confdir" ]; then
      local others
      others=$(find "$confdir" -maxdepth 1 -name '*.json' ! -name config.json 2>/dev/null | head -n3)
      [ -z "$others" ] || die "$confdir has other .json files besides config.json ($others); the panel manages one config.json." \
        "$confdir 里除了 config.json 还有别的 .json（$others），面板只能管理单个 config.json。"
      conf="$confdir/config.json"
    elif [ -z "$conf" ]; then
      conf="${workdir:-/}/config.json"
    fi
    case "$conf" in /*) ;; *) conf="${workdir:-/}/$conf" ;; esac
    SB_CONF=$conf
    SB_USER=$(systemctl show -p User --value "$SB_UNIT" 2>/dev/null || true)
    return
  fi

  if pgrep -x sing-box >/dev/null 2>&1; then
    die "sing-box is running but not as $SB_UNIT; the panel needs it under systemd." \
      "sing-box 正在运行，但不是由 $SB_UNIT 管理的；面板需要它由 systemd 管理。"
  fi
  local found
  found=$(command -v sing-box 2>/dev/null || true)
  if [ -n "$found" ]; then
    SB_STATE=binary SB_BIN=$found SB_CONF=$SB_CONF_DEFAULT
  fi
}

# --- sing-box ----------------------------------------------------------------

install_singbox() {
  info "Installing sing-box (built with with_v2ray_api) to $SB_BIN_DEFAULT" \
    "安装 sing-box（带 with_v2ray_api）到 $SB_BIN_DEFAULT"
  fetch_asset "sing-box-v2api_linux_$ARCH.tar.gz"
  install -m 0755 "$TMP/sing-box-v2api_linux_$ARCH/sing-box" "$SB_BIN_DEFAULT"
  mkdir -p /usr/local/share/licenses/sing-box
  cp "$TMP/sing-box-v2api_linux_$ARCH/LICENSE" "$TMP/sing-box-v2api_linux_$ARCH/SOURCE" /usr/local/share/licenses/sing-box/
  mkdir -p "$SBW_DATA"
  : >"$INSTALLED_SB_MARK"
  SB_BIN=$SB_BIN_DEFAULT
  SB_CONF=$SB_CONF_DEFAULT
}

write_singbox_unit() {
  info "Creating $SB_UNIT" "创建 $SB_UNIT"
  cat >/etc/systemd/system/$SB_UNIT <<EOF
[Unit]
Description=sing-box service
Documentation=https://sing-box.sagernet.org
After=network.target nss-lookup.target network-online.target

[Service]
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
StateDirectory=sing-box
ExecStart=$SB_BIN -D /var/lib/sing-box -c $SB_CONF run
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=10s
LimitNOFILE=infinity

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

# The panel reloads sing-box with `systemctl reload`; add ExecReload when the
# existing unit lacks one.
ensure_reload() {
  if [ -z "$(systemctl show -p ExecReload --value "$SB_UNIT")" ]; then
    info "Adding ExecReload (SIGHUP) to $SB_UNIT" "为 $SB_UNIT 添加 ExecReload（SIGHUP）"
    mkdir -p "$(dirname "$SB_DROPIN")"
    # shellcheck disable=SC2016 # $MAINPID is for systemd
    printf '[Service]\nExecReload=/bin/kill -HUP $MAINPID\n' >"$SB_DROPIN"
    systemctl daemon-reload
  fi
}

# prepare_config adds the log file, Clash API and (when supported) V2Ray
# stats API the panel reads. Existing configs are only changed after a yes,
# with a backup, and only if `sing-box check` accepts the result.
prepare_config() {
  local v2ray=false candidate="$TMP/config.json" changes
  [ "$SB_V2RAY" = 1 ] && v2ray=true
  mkdir -p "$(dirname "$SB_CONF")"
  changes=$("$SBW_BIN" prepare -singbox-config "$SB_CONF" -out "$candidate" \
    -log-file "$SB_LOG" -v2ray-api="$v2ray" -listen "$LISTEN")
  [ -n "$changes" ] || return 0

  if [ -f "$SB_CONF" ]; then
    say "The panel needs these additions to $SB_CONF:" "面板需要在 $SB_CONF 中添加："
    printf '  - %s\n' "${changes//$'\n'/$'\n'  - }"
    if ! ask "Apply them (a backup is kept)?" "是否添加（会保留备份）？" y; then
      warn "Skipped; live speed, connections, logs or per-user traffic may show as unavailable." \
        "已跳过；实时速率、连接、日志或按用户流量可能显示为不可用。"
      return 0
    fi
  else
    info "Creating $SB_CONF" "创建 $SB_CONF"
  fi
  "$SB_BIN" check -c "$candidate" || die "sing-box check rejected the prepared config; nothing was changed." \
    "sing-box check 未通过，配置未做任何修改。"

  mkdir -p "$(dirname "$SB_LOG")"
  if [ -n "$SB_USER" ] && [ "$SB_USER" != root ]; then chown "$SB_USER" "$(dirname "$SB_LOG")"; fi
  if [ -f "$SB_CONF" ]; then
    local backup
    backup="$SB_CONF.bak-$(date -u +%Y%m%d%H%M%S)"
    cp -p "$SB_CONF" "$backup"
    info "Backup: $backup" "备份：$backup"
    chmod --reference="$SB_CONF" "$candidate"
    chown --reference="$SB_CONF" "$candidate"
  elif [ -n "$SB_USER" ] && [ "$SB_USER" != root ]; then
    chown "$SB_USER" "$candidate"
  fi
  mv -f "$candidate" "$SB_CONF"
  CONFIG_CHANGED=1
}

write_logrotate() {
  [ -d /etc/logrotate.d ] || return 0
  [ -f "$SB_LOGROTATE" ] && return 0
  cat >"$SB_LOGROTATE" <<EOF
$SB_LOG $SBW_LOGDIR/*.log {
    weekly
    rotate 4
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
}
EOF
}

# --- panel -------------------------------------------------------------------

choose_listen() {
  if [ -n "$LISTEN" ]; then return; fi
  if [ -f "$SBW_ENV" ]; then
    LISTEN=$(sed -n 's/^SBW_LISTEN=//p' "$SBW_ENV" | tail -n1)
    [ -n "$LISTEN" ] && return
  fi
  LISTEN=127.0.0.1:9091
  local port
  if port_in_use 9091 && ! systemctl is-active --quiet sing-box-web; then
    port=$(random_port)
    LISTEN=127.0.0.1:$port
  fi
  if [ "$YES" = 1 ] || [ ! -r /dev/tty ]; then return; fi
  port=$(random_port)
  say "Where should the panel listen?" "面板监听在哪里？"
  say "  1) $LISTEN only (recommended; reach it through a reverse proxy with TLS or an SSH tunnel)" \
    "  1) 仅本机 $LISTEN（推荐；通过带 TLS 的反向代理或 SSH 隧道访问）"
  say "  2) 0.0.0.0:$port, open to the internet over plain HTTP (the password crosses the network unencrypted)" \
    "  2) 0.0.0.0:$port，公网直接访问，明文 HTTP（登录密码不加密传输）"
  local ans
  printf '%s ' "$(say "Choice [1]:" "选择 [1]：")" >/dev/tty
  read -r ans </dev/tty || ans=""
  [ "$ans" = 2 ] && LISTEN="0.0.0.0:$port"
  return 0
}

install_panel() {
  info "Downloading sing-box-web ${VERSION:-latest}" "下载 sing-box-web ${VERSION:-最新版}"
  fetch_asset "sing-box-web_linux_$ARCH.tar.gz"
  if systemctl is-active --quiet sing-box-web; then systemctl stop sing-box-web; fi
  install -m 0755 "$TMP/sing-box-web_linux_$ARCH/sing-box-web" "$SBW_BIN"
  mkdir -p "$SBW_DATA" "$SBW_LOGDIR"
  chmod 0700 "$SBW_DATA"
  UPGRADES=0
  if [ -f "$TMP/sing-box-web_linux_$ARCH/sing-box-web-upgrade" ]; then
    mkdir -p "$(dirname "$UPG_BIN")"
    install -m 0755 "$TMP/sing-box-web_linux_$ARCH/sing-box-web-upgrade" "$UPG_BIN"
    UPGRADES=1
  fi
}

# One-click upgrades from the panel: the panel drops a request into
# $SBW_UPGRADE_DIR/requests and this root unit carries it out (see
# deploy/upgrade/sing-box-web-upgrade). Only for releases that ship it.
write_upgrade_units() {
  [ "$UPGRADES" = 1 ] || return 0
  cat >"$UPG_UNIT.path" <<EOF
[Unit]
Description=Watch sing-box-web upgrade requests

[Path]
DirectoryNotEmpty=$SBW_UPGRADE_DIR/requests
MakeDirectory=yes

[Install]
WantedBy=multi-user.target
EOF
  cat >"$UPG_UNIT.service" <<EOF
[Unit]
Description=Run sing-box-web upgrade requests
StartLimitIntervalSec=0

[Service]
Type=oneshot
ExecStart=$UPG_BIN
StateDirectory=sing-box-web-upgrade
StateDirectoryMode=0700
TimeoutStartSec=15min
EOF
  systemctl daemon-reload
  systemctl enable --now sing-box-web-upgrade.path >/dev/null 2>&1 ||
    warn "Could not enable sing-box-web-upgrade.path; one-click upgrades stay off." \
      "无法启用 sing-box-web-upgrade.path，后台一键升级不可用。"
}

# set_env KEY VALUE: set KEY in $SBW_ENV, replacing an earlier value.
set_env() {
  if grep -q "^$1=" "$SBW_ENV"; then
    sed -i "s|^$1=.*|$1=$2|" "$SBW_ENV"
  else
    printf '%s=%s\n' "$1" "$2" >>"$SBW_ENV"
  fi
}

write_panel_files() {
  if [ ! -f "$SBW_ENV" ]; then
    cat >"$SBW_ENV" <<EOF
# sing-box-web settings; every flag has an SBW_* variable (sing-box-web -h).
SBW_LISTEN=$LISTEN
SBW_DATA_DIR=$SBW_DATA
SBW_SINGBOX_CONFIG=$SB_CONF
SBW_CONTROLLER=systemd
SBW_SYSTEMD_UNIT=$SB_UNIT
SBW_CHECK_CMD=$SB_BIN check -c {config}
# Failed logins for fail2ban (deploy/fail2ban):
SBW_AUTH_LOG=$SBW_LOGDIR/auth.log
# Reverse proxies allowed to set X-Forwarded-For:
SBW_TRUSTED_PROXIES=127.0.0.1/32,::1/128
EOF
    chmod 0600 "$SBW_ENV"
  elif [ -n "$LISTEN_FLAG" ]; then
    set_env SBW_LISTEN "$LISTEN"
  fi
  # Added on upgrade too, so installs from older versions get them.
  if [ "$UPGRADES" = 1 ]; then set_env SBW_UPGRADE_DIR "$SBW_UPGRADE_DIR"; fi
  # Remembered for one-click upgrades, which download the same way.
  if [ -n "$GH_PROXY" ]; then set_env GH_PROXY "$GH_PROXY"; fi
  if [ "$REPO" != axuezi/sing-box-web ]; then set_env SBW_RELEASE_REPO "$REPO"; fi
  local confdir
  confdir=$(dirname "$SB_CONF")
  cat >"$SBW_UNIT" <<EOF
[Unit]
Description=sing-box-web panel
Documentation=https://github.com/$REPO
After=network-online.target $SB_UNIT
Wants=network-online.target

[Service]
EnvironmentFile=$SBW_ENV
ExecStart=$SBW_BIN serve
Restart=on-failure
RestartSec=5s
# Runs as root to write $confdir and run systemctl for $SB_UNIT;
# everything else is read-only.
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=$SBW_DATA $SBW_LOGDIR $confdir
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

set_password() {
  local pw rc=0
  pw=$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')
  printf '%s\n' "$pw" | SBW_DATA_DIR="$SBW_DATA" "$SBW_BIN" passwd -if-unset >/dev/null 2>"$TMP/passwd.err" || rc=$?
  case $rc in
    0) ADMIN_PW=$pw ;;
    3) ADMIN_PW="" ;;
    *) cat "$TMP/passwd.err" >&2; die "Could not set the administrator password." "设置管理员密码失败。" ;;
  esac
}

wait_panel() {
  local host=${LISTEN%:*} port=${LISTEN##*:}
  case "$host" in 0.0.0.0 | "" | "[::]") host=127.0.0.1 ;; esac
  for _ in $(seq 20); do
    curl -fsS -o /dev/null "http://$host:$port/" 2>/dev/null && return 0
    sleep 0.5
  done
  return 1
}

public_ip() {
  curl -fsS4 --max-time 5 https://api.ipify.org 2>/dev/null ||
    ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}'
}

# --- commands ----------------------------------------------------------------

do_install() {
  preflight
  TMP=$(mktemp -d)
  CONFIG_CHANGED=0 ADMIN_PW=""

  detect_singbox
  case $SB_STATE in
    none)
      install_singbox
      write_singbox_unit
      ;;
    binary)
      info "Using existing sing-box at $SB_BIN" "使用已有的 sing-box：$SB_BIN"
      write_singbox_unit
      ;;
    unit)
      info "Using existing $SB_UNIT ($SB_BIN, config $SB_CONF)" "使用已有的 $SB_UNIT（$SB_BIN，配置 $SB_CONF）"
      ensure_reload
      ;;
  esac
  "$SB_BIN" version >/dev/null 2>&1 || die "$SB_BIN does not run." "$SB_BIN 无法运行。"
  SB_V2RAY=0
  if has_v2ray_api "$SB_BIN"; then SB_V2RAY=1; else
    warn "This sing-box was built without with_v2ray_api: per-user traffic and monthly quotas will be unavailable. Builds with it are attached to each sing-box-web release (sing-box-v2api_linux_$ARCH.tar.gz)." \
      "这个 sing-box 编译时没有 with_v2ray_api：按用户流量统计和每月额度将不可用。带它的版本附在每个 sing-box-web release 里（sing-box-v2api_linux_$ARCH.tar.gz）。"
  fi

  choose_listen
  install_panel
  prepare_config
  write_logrotate
  write_panel_files
  write_upgrade_units
  set_password

  if [ "$SB_STATE" != unit ] || ! systemctl is-active --quiet "$SB_UNIT"; then
    systemctl enable --now "$SB_UNIT"
  elif [ "$CONFIG_CHANGED" = 1 ]; then
    info "Restarting $SB_UNIT to apply the config changes" "重启 $SB_UNIT 以应用配置修改"
    systemctl restart "$SB_UNIT"
  fi
  systemctl is-active --quiet "$SB_UNIT" || warn "$SB_UNIT is not running; see: journalctl -u $SB_UNIT" \
    "$SB_UNIT 未在运行，请查看：journalctl -u $SB_UNIT"

  systemctl enable sing-box-web >/dev/null 2>&1
  systemctl restart sing-box-web
  wait_panel || die "The panel did not come up; see: journalctl -u sing-box-web" \
    "面板没有启动成功，请查看：journalctl -u sing-box-web"

  local port=${LISTEN##*:} url
  if [ "${LISTEN%:*}" = 127.0.0.1 ] || [ "${LISTEN%:*}" = localhost ]; then
    url="http://127.0.0.1:$port"
  else
    url="http://$(public_ip):$port"
  fi
  echo
  info "sing-box-web $("$SBW_BIN" version | awk '{print $2}') is running." "sing-box-web $("$SBW_BIN" version | awk '{print $2}') 已启动。"
  say "  URL:      $url" "  地址：    $url"
  if [ -n "$ADMIN_PW" ]; then
    say "  Username: admin" "  用户名：  admin"
    say "  Password: $ADMIN_PW   (shown once; change it under Settings)" "  密码：    $ADMIN_PW   （只显示这一次，可在「设置」中修改）"
  else
    say "  Existing administrator kept. Reset: sing-box-web passwd -data-dir $SBW_DATA" \
      "  沿用已有管理员。重置密码：sing-box-web passwd -data-dir $SBW_DATA"
  fi
  if [ "${url#http://127.0.0.1}" != "$url" ]; then
    say "  From your computer: ssh -L $port:127.0.0.1:$port root@<server>, then open $url" \
      "  在你的电脑上执行：ssh -L $port:127.0.0.1:$port root@<服务器>，然后打开 $url"
    say "  Or put a reverse proxy with TLS (Caddy, Nginx) in front of 127.0.0.1:$port." \
      "  或在 127.0.0.1:$port 前面配置带 TLS 的反向代理（Caddy、Nginx）。"
  else
    say "  Open TCP port $port in your firewall / cloud security group if needed." \
      "  如有需要，请在防火墙 / 云安全组放行 TCP $port 端口。"
  fi
  say "  Settings: $SBW_ENV (then: systemctl restart sing-box-web)" "  参数：$SBW_ENV（修改后执行 systemctl restart sing-box-web）"
}

do_uninstall() {
  [ "$(id -u)" = 0 ] || die "Run as root." "请用 root 运行。"
  systemctl disable --now sing-box-web sing-box-web-upgrade.path 2>/dev/null || true
  rm -f "$SBW_UNIT" "$SBW_BIN" "$SB_LOGROTATE" "$UPG_UNIT.path" "$UPG_UNIT.service" "$UPG_BIN"
  rm -rf /var/lib/sing-box-web-upgrade
  if [ -f "$SB_DROPIN" ]; then rm -f "$SB_DROPIN"; rmdir "$(dirname "$SB_DROPIN")" 2>/dev/null || true; fi
  systemctl daemon-reload
  local ours=0
  [ -f "$INSTALLED_SB_MARK" ] && ours=1
  if [ "$PURGE" = 1 ]; then
    rm -rf "$SBW_DATA" "$SBW_LOGDIR" "$SBW_ENV"
    info "Removed sing-box-web and its data." "已删除 sing-box-web 及其数据。"
  else
    info "Removed sing-box-web. Kept $SBW_DATA and $SBW_ENV (use --purge to delete)." \
      "已删除 sing-box-web。保留了 $SBW_DATA 和 $SBW_ENV（加 --purge 一并删除）。"
  fi
  say "sing-box itself was left running." "sing-box 本身保持运行，未做改动。"
  if [ "$ours" = 1 ]; then
    say "It was installed by this script; to remove it: systemctl disable --now $SB_UNIT && rm /etc/systemd/system/$SB_UNIT $SB_BIN_DEFAULT" \
      "它是本脚本安装的；如需删除：systemctl disable --now $SB_UNIT && rm /etc/systemd/system/$SB_UNIT $SB_BIN_DEFAULT"
  fi
}

usage() {
  cat <<EOF
Usage: $0 [install] [--version vX.Y.Z] [--listen ADDR] [--yes]
       $0 uninstall [--purge]
EOF
}

CMD=install VERSION="" LISTEN="" PURGE=0
while [ $# -gt 0 ]; do
  case "$1" in
    install | uninstall) CMD=$1 ;;
    --version) VERSION=$2; shift ;;
    --listen) LISTEN=$2; shift ;;
    -y | --yes) YES=1 ;;
    --purge) PURGE=1 ;;
    -h | --help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
  shift
done
case "$VERSION" in "" | v*) ;; *) VERSION="v$VERSION" ;; esac
LISTEN_FLAG=$LISTEN

case $CMD in
  install) do_install ;;
  uninstall) do_uninstall ;;
esac
