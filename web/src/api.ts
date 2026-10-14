export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly body: Record<string, unknown> = {},
  ) {
    super(message)
  }
}

let csrfToken = ''
let onUnauthorized: (() => void) | null = null

export function setCsrfToken(token: string) {
  csrfToken = token
}

/** Called when any request comes back 401, e.g. after the session expired. */
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? 'GET').toUpperCase()
  const headers: Record<string, string> = { Accept: 'application/json', ...(init.headers as Record<string, string>) }
  if (init.body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) headers['X-CSRF-Token'] = csrfToken

  const res = await fetch(`/api${path}`, { credentials: 'same-origin', ...init, method, headers })
  if (res.status === 204) return undefined as T
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401 && body.code === 'unauthenticated' && path !== '/me' && path !== '/login') {
      onUnauthorized?.()
    }
    throw new ApiError(res.status, body.code ?? 'error', body.error ?? res.statusText, body)
  }
  return body as T
}

export const post = <T>(path: string, data?: unknown) =>
  api<T>(path, { method: 'POST', body: data === undefined ? undefined : JSON.stringify(data) })

export interface Me {
  username: string
  csrf_token: string
  expires_at: string
}

export interface Inbound {
  tag: string
  type: string
  listen: string
  listen_port: number
  users: number
  tls: boolean
  reality: boolean
  server_name?: string
}

export interface Status {
  panel: { version: string; commit: string; started_at: string; rss_bytes: number }
  service: {
    controller: string
    status: { state: string; started_at?: string; restart_count: number }
    error?: string
  }
  runtime: {
    available: boolean
    version?: string
    error?: string
    // newest stable sing-box release once checked; update repeats it only when it is newer
    latest?: { version: string; url: string; checked_at: string }
    update?: { version: string; url: string; checked_at: string }
  }
  config: {
    path: string
    sha256?: string
    mod_time?: string
    size: number
    error?: string
    summary?: {
      inbounds: Inbound[]
      outbounds: { tag: string; type: string }[]
      route_rules: number
      dns_servers: number
      log_level: string
      log_output: string
      clash_api?: string
      v2ray_api?: string
    }
  }
  log: { path: string; size: number; error?: string }
}

export interface AuditEntry {
  id: number
  at: string
  actor: string
  ip: string
  action: string
  target: string
  detail: string
}

export interface LogLine {
  time?: string
  level: string
  text: string
}

export const patch = <T>(path: string, data: unknown) =>
  api<T>(path, { method: 'PATCH', body: JSON.stringify(data) })
export const put = <T>(path: string, data: unknown) =>
  api<T>(path, { method: 'PUT', body: JSON.stringify(data) })
export const del = <T>(path: string) => api<T>(path, { method: 'DELETE' })

export interface User {
  id: number
  name: string
  note: string
  enabled: boolean
  inbounds: string[]
  created_at: string
  updated_at: string
  last_sub_at?: string
  month_up: number
  month_down: number
  expires_at?: string
  quota_bytes: number
  reset_day: number
  cycle_start?: string
  next_reset: string
  cycle_up: number
  cycle_down: number
  limited: '' | 'expired' | 'quota'
  /** unknown: connections exist that may be this user's but cannot be matched */
  online: 'online' | 'offline' | 'unknown'
  conns: number
  last_active_at?: string
}

export interface PresenceSummary {
  available: boolean
  error?: string
  log_followed: boolean
  connections: number
  unattributed: number
}

export interface InboundTLS {
  mode: 'none' | 'tls' | 'reality'
  server_name: string
  alpn: string[] | null
  cert_source: 'file' | 'inline'
  certificate_path: string
  key_path: string
  certificate: string
  key: string
  handshake_server: string
  handshake_port: number
  private_key: string
  public_key: string
  short_ids: string[] | null
}

export interface InboundForm {
  tag: string
  type: 'hysteria2' | 'vless'
  listen: string
  listen_port: number
  tls: InboundTLS
  up_mbps: number
  down_mbps: number
  obfs_password: string
  masquerade: string
  flow: '' | 'xtls-rprx-vision'
}

/** How an inbound appears in subscriptions (Settings → Subscription). */
export interface InboundExport {
  name?: string
  host?: string
  port?: number
  insecure: boolean
  hidden: boolean
}

export interface InboundItem {
  form: InboundForm
  extra: string[]
  users: number
  export: InboundExport
}

export interface InboundsResponse {
  inbounds: InboundItem[]
  others: { tag: string; type: string; listen: string; listen_port: number }[]
  public_host: string
  user_count: number
}

export interface OutboundTLS {
  mode: 'none' | 'tls' | 'reality'
  server_name: string
  insecure: boolean
  alpn: string[] | null
  utls: string
  public_key: string
  short_id: string
}

export type OutboundType = 'direct' | 'socks' | 'http' | 'shadowsocks' | 'vless' | 'trojan' | 'hysteria2'

export interface OutboundForm {
  tag: string
  type: OutboundType
  server: string
  server_port: number
  username: string
  password: string // masked when read
  method: string
  uuid: string // masked when read
  flow: string
  tls: OutboundTLS
  up_mbps: number
  down_mbps: number
  obfs_password: string // masked when read
  detour: string
  bind_interface: string
  inet4_bind_address: string
  inet6_bind_address: string
  resolver: string
  resolver_strategy: string
}

export interface OutboundItem {
  form: OutboundForm
  extra: string[]
  refs: string[]
}

/** An outbound, endpoint or DNS server shown but not edited here. */
export interface OtherItem {
  tag: string
  type: string
  legacy?: boolean
  refs: string[]
}

export interface OutboundsResponse {
  outbounds: OutboundItem[]
  others: OtherItem[]
  route_final: string
  tags: string[]
  dns_servers: string[]
}

export type DnsServerType = 'local' | 'udp' | 'tcp' | 'tls' | 'https' | 'quic' | 'h3'

export interface DnsServerForm {
  tag: string
  type: DnsServerType
  server: string
  server_port: number
  path: string
  server_name: string
  insecure: boolean
  detour: string
  resolver: string
}

export interface DnsServerItem {
  form: DnsServerForm
  extra: string[]
  refs: string[]
}

export interface DnsSettings {
  strategy: string
  final: string
  disable_cache: boolean
}

export interface DnsRule {
  action: string
  server?: string
  strategy?: string
  invert?: boolean
  conditions: { key: string; values: string[] }[]
  legacy_strategy?: boolean
}

export interface DnsResponse {
  settings: DnsSettings
  servers: DnsServerItem[]
  others: OtherItem[]
  rules: DnsRule[]
  outbounds: string[]
}

export interface AnalysisSeries {
  up: number
  down: number
  series: [number, number][] // per bucket: [up, down]
}

export interface AnalysisItem extends AnalysisSeries {
  key: string
  name: string
  /** deleted user, or inbound/outbound no longer in the config */
  gone: boolean
  cmp?: AnalysisSeries
}

export interface AnalysisRange {
  from: string
  to: string
  buckets: string[]
}

export interface TrafficAnalysis {
  kind: 'user' | 'inbound' | 'outbound'
  gran: 'hour' | 'day' | 'month'
  range: AnalysisRange
  cmp: AnalysisRange | null
  items: AnalysisItem[]
  first_day: string
  first_hour: string
  now: string
  /** today's comparison day was cut to the same hours as today so far */
  cmp_partial_today: boolean
}

/** Editable user fields; expires_at is unix seconds, 0 = never. */
export interface UserInput {
  name: string
  note: string
  enabled: boolean
  inbounds: string[]
  expires_at: number
  quota_bytes: number
  reset_day: number
}

export interface UsersResponse {
  users: User[]
  inbounds: { tag: string; type: string }[]
  supported_types: string[]
  presence: PresenceSummary
}

export interface ApplyResult {
  changed: boolean
  status: string
  log: string
}

export interface ClientInfo {
  subscription: { auto: string; mihomo: string; singbox: string; base64: string }
  links: { name: string; inbound: string; url?: string; error?: string }[]
  skipped: Record<string, string>
  enabled: boolean
}

export interface Sample {
  t: number
  up: number
  down: number
  conns: number
  mem: number
}

export interface Conn {
  id: string
  inbound: string
  network: string
  source: string
  client_ip?: string // real client behind a local relay
  host: string
  dest: string
  outbound: string
  rule: string
  upload: number
  download: number
  start: string
  /** '' when the connection cannot be matched to a user */
  user: string
}

export interface DayTotal {
  day: string
  up: number
  down: number
}

export interface Series {
  kind: 'user' | 'inbound' | 'outbound'
  key: string
  name: string
  daily: DayTotal[]
  today: DayTotal
  month: DayTotal
  total: DayTotal
}

export interface TrafficStats {
  days: string[]
  series: Series[]
  collector?: { last_run: string; error?: string; code?: string }
}

export interface SubAccess {
  at: string
  ip: string
  user_agent: string
  format: string
}

export interface AccessRule {
  id: number
  list: 'deny' | 'allow'
  cidr: string
  note: string
  expires_at?: string
  created_at: string
  created_by: string
}

export interface AccessTarget {
  tag: string
  type: string
  listen: string
  port: number
  networks: string[]
  by: 'firewall' | 'haproxy' | 'none'
  reason?: string
}

export interface AccessSource {
  ip: string
  inbound: string
  conns: number
}

export interface AccessState {
  available: boolean
  error?: string
  code?: string // "no_ctl": the controller cannot manage IP rules
  my_ip?: string
  info?: { nft: boolean; haproxy: boolean; jails: string[] | null }
  targets?: AccessTarget[] | null
  rules?: AccessRule[]
  allowlist?: { on: boolean; confirm_by?: string; outside: AccessSource[] }
  sources?: AccessSource[]
  stats?: Record<string, number> | null
  last?: { at: string; ok: boolean; output: string }
}

export interface F2BBan {
  jail: string
  ip: string
  since?: string
  until?: string
}
