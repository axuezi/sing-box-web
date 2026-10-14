# Deployment files

| Path | Installs to | Purpose |
| --- | --- | --- |
| `compose.example.yaml` | your compose project | The panel container: read-only, no capabilities, no `docker.sock` |
| `ctl/sing-box-web-ctl` | `/usr/local/libexec/` (0755) | Runs whitelisted actions for the panel: `status`, `reload`, `restart` on the sing-box container; `access-*` (nftables table `inet sbw_access` and HAProxy lists) and `f2b-*` (fail2ban) for the access control page |
| `ctl/sing-box-web-ctl.path`, `.service` | `/etc/systemd/system/` | Triggers the script when a request file appears |
| `ctl/sing-box-web-ctl.default` | `/etc/default/sing-box-web-ctl` | Container name, shared directory, and the optional fail2ban jails / HAProxy list directory |
| `fail2ban/sing-box-web.conf` | `/etc/fail2ban/filter.d/` | Matches failed logins from `-auth-log` |
| `fail2ban/jail.local.example` | append to `/etc/fail2ban/jail.local` | Bans an IP after 5 failures in 15 min |

The panel only needs the `requests/`, `results/` and `access/` subdirectories
of the control directory; keep `.tmp/` private to root. Run the panel with
`-controller ctlfile -ctl-dir <dir>`.

## Optional integrations

- **Access control** – set `F2B_JAILS`, `HAPROXY_ACL_DIR` and
  `HAPROXY_CONTAINER` in `/etc/default/sing-box-web-ctl`. For inbounds behind
  HAProxy, add to the backend that forwards to sing-box:

  ```
  tcp-request content reject if { src -f <lists>/deny.lst }
  tcp-request content reject unless { src -f <lists>/allow.lst }
  ```

  where `<lists>` is where `HAPROXY_ACL_DIR` is mounted inside the HAProxy
  container. The script reloads HAProxy with USR2, keeping open connections.
- **Real client IPs behind HAProxy** – have the frontend log each connection
  it forwards to sing-box, as it opens, and start the panel with
  `-gateway-log 127.0.0.1:5514`:

  ```
  log 127.0.0.1:5514 format raw local0 info
  log-format "sbw-gw %ci %bi:%bp"
  option logasap
  # after tcp-request inspect-delay: only log the sing-box (e.g. Reality) SNI
  tcp-request content set-log-level silent unless { req.ssl_sni -i <reality sni> }
  ```
