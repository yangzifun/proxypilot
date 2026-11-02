/* =================================================================
 *  web-antd / scripts / preview.mjs
 *  生成一个**纯静态**的本地预览页：web-antd/preview/index.html
 *  · 把 dist/app.js + dist/app.css 内联进去
 *  · 用内存版 localStorage 预置一枚假 JWT，打开即进入控制台
 *  · fetch 全部打桩，返回与 worker.js 同结构的样例数据
 *  只是看样式用的，不参与部署；线上由 workers/worker-antd.js 提供服务。
 *  用法：node scripts/preview.mjs
 * ================================================================= */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(here, '..');
const distDir = path.join(webRoot, 'dist');
const outDir = path.join(webRoot, 'preview');

const appJs = fs.readFileSync(path.join(distDir, 'app.js'), 'utf8');
const appCss = fs.readFileSync(path.join(distDir, 'app.css'), 'utf8');

const NOW = Date.now();
const iso = (ms) => new Date(ms).toISOString();

const daily = [];
for (let i = 29; i >= 0; i--) {
  const d = iso(NOW - i * 86400000).slice(0, 10);
  const wave = Math.round(46 + 38 * Math.sin((29 - i) / 3.1) + 22 * Math.cos((29 - i) / 1.7));
  daily.push({
    date: d,
    total: Math.max(6, wave + 18),
    subscription: Math.max(4, Math.round(wave * 0.82)),
    api_generation: Math.max(1, Math.round(wave * 0.18) + 3),
    unique_uuids: 6 + ((29 - i) % 9),
  });
}

const users = [
  { uuid: 'a1b2c3d4-1111-4a2b-9c3d-000000000001', owner: 'alice', email: 'alice@example.com', status: 'active', remark: '月付 · 已续费', access_count: 4213, last_accessed: NOW - 42 * 1000, created_at: NOW - 86400000 * 96, updated_at: NOW - 42 * 1000, count: 14 },
  { uuid: 'a1b2c3d4-2222-4a2b-9c3d-000000000002', owner: 'bob', email: 'bob@example.com', status: 'active', remark: '', access_count: 2890, last_accessed: NOW - 5 * 60000, created_at: NOW - 86400000 * 71, updated_at: NOW - 5 * 60000, count: 9 },
  { uuid: 'a1b2c3d4-3333-4a2b-9c3d-000000000003', owner: 'carol', email: 'carol@example.com', status: 'suspended', remark: '欠费停机', access_count: 1740, last_accessed: NOW - 86400000 * 3, created_at: NOW - 86400000 * 40, updated_at: NOW - 86400000 * 3, count: 6 },
  { uuid: 'a1b2c3d4-4444-4a2b-9c3d-000000000004', owner: 'dave', email: 'dave@example.com', status: 'active', remark: 'TG 联系', access_count: 980, last_accessed: NOW - 26 * 60000, created_at: NOW - 86400000 * 22, updated_at: NOW - 26 * 60000, count: 4 },
  { uuid: 'a1b2c3d4-5555-4a2b-9c3d-000000000005', owner: 'erin', email: 'erin@example.com', status: 'inactive', remark: '试用结束', access_count: 312, last_accessed: NOW - 86400000 * 12, created_at: NOW - 86400000 * 12, updated_at: NOW - 86400000 * 12, count: 2 },
];

const servers = [
  { id: 1, name: 'hk-01', status: 'online', hostname: 'hk-01', last_ip: '203.0.113.11', ip_country: 'HK', ip_region: 'Central', ip_lat: 22.32, ip_lon: 114.17, cpu_percent: 34, cores: 8, mem_percent: 56, mem_used_mb: 4480, mem_total_mb: 8000, disk_percent: 41, uptime_sec: 864000 * 2, load1: 0.42, load5: 0.51, load15: 0.6, last_seen: NOW - 24000, agent_version: '1.2.7', services: { 'sing-box': 'active', caddy: 'active', sshd: 'active' } },
  { id: 2, name: 'jp-02', status: 'online', hostname: 'jp-02', last_ip: '198.51.100.22', ip_country: 'JP', ip_region: 'Tokyo', ip_lat: 35.68, ip_lon: 139.69, cpu_percent: 78, cores: 4, mem_percent: 71, mem_used_mb: 2900, mem_total_mb: 4096, disk_percent: 63, uptime_sec: 360000, load1: 2.1, load5: 1.84, load15: 1.52, last_seen: NOW - 18000, agent_version: '1.2.7', services: { 'sing-box': 'failed', caddy: 'active', sshd: 'active' } },
  { id: 3, name: 'sg-03', status: 'online', hostname: 'sg-03', last_ip: '192.0.2.33', ip_country: 'SG', ip_region: 'Singapore', ip_lat: 1.35, ip_lon: 103.82, cpu_percent: 12, cores: 2, mem_percent: 33, mem_used_mb: 660, mem_total_mb: 2048, disk_percent: 28, uptime_sec: 86400 * 11, load1: 0.1, load5: 0.12, load15: 0.09, last_seen: NOW - 41000, agent_version: '1.2.6', services: { 'sing-box': 'active', caddy: 'not-installed', sshd: 'active' } },
  { id: 4, name: 'us-04', status: 'offline', hostname: '', last_ip: '203.0.113.44', ip_country: 'US', ip_region: 'Los Angeles', ip_lat: 34.05, ip_lon: -118.24, cpu_percent: 0, mem_percent: 0, disk_percent: 0, uptime_sec: 0, last_seen: NOW - 3 * 3600000, agent_version: '1.2.7', services: {} },
];

const SAMPLE_SB = JSON.stringify(
  {
    log: { level: 'warn', timestamp: true },
    inbounds: [
      { type: 'shadowsocks', tag: 'ss2022-sb', listen: '::', listen_port: 13702, method: '2022-blake3-aes-256-gcm', password: 'P46HaJ71OwCIcZ1d/F7/jH+KxrF8aQXVROkMb2vSoPc=', users: [{ name: 'user01', password: '1mg/fSTpInkWA1QzsTKA1983Z+8WnX1/DLHxXWneBN8=' }] },
      { type: 'hysteria2', tag: 'hy2-sb', listen: '::', listen_port: 13701, tls: { enabled: true, alpn: ['h3'] }, users: [{ name: 'user01', password: 'uuid' }] },
      { type: 'vless', tag: 'vless-tcp-reality-vision', listen: '::', listen_port: 13705, tls: { enabled: true, server_name: 'www.microsoft.com', reality: { enabled: true, handshake: { server: 'www.microsoft.com', server_port: 443 }, short_id: ['47e7e28f'] } }, users: [{ name: 'user01', uuid: 'uuid', flow: 'xtls-rprx-vision' }] },
    ],
  },
  null,
  2
);

const RESPONSES = [
  [/\/api\/stats/, {
    uuids: users.length, templates: 3,
    access_stats: {
      success: true, total_requests: 24318, unique_uuids: 137,
      subscription_requests: 20114, api_generation_requests: 4204,
      today_total: 612, today_subscription: 501, today_apigen: 111,
      daily_stats: daily,
      popular_uuids: users.map((u) => ({ uuid: u.uuid, access_count: u.access_count, subscription_count: Math.round(u.access_count * 0.86), apigen_count: Math.round(u.access_count * 0.14), owner: u.owner })),
      latest_logs: [
        { uuid: users[0].uuid, query_type: 'subscription', created_at: NOW - 42000, owner: 'alice' },
        { uuid: users[1].uuid, query_type: 'subscription', created_at: NOW - 300000, owner: 'bob' },
        { uuid: users[3].uuid, query_type: 'api-generation', created_at: NOW - 1560000, owner: 'dave' },
        { uuid: users[2].uuid, query_type: 'subscription', created_at: NOW - 5400000, owner: 'carol' },
        { uuid: users[0].uuid, query_type: 'api-generation', created_at: NOW - 9000000, owner: 'alice' },
      ],
    },
  }],
  [/\/api\/servers\/monitor/, {
    success: true, servers,
    watch: ['sing-box', 'caddy', 'sshd'],
    matrix: [
      { service: 'sing-box', states: servers.map((s) => ({ server_id: s.id, server_name: s.name, state: s.status === 'offline' ? 'offline' : s.services['sing-box'] || 'unknown' })) },
      { service: 'caddy', states: servers.map((s) => ({ server_id: s.id, server_name: s.name, state: s.status === 'offline' ? 'offline' : s.services.caddy || 'unknown' })) },
      { service: 'sshd', states: servers.map((s) => ({ server_id: s.id, server_name: s.name, state: s.status === 'offline' ? 'offline' : s.services.sshd || 'unknown' })) },
    ],
    summary: {
      servers: servers.length, online: 3, offline: 1, failed_services: 1, not_installed: 1,
      failed_list: [{ server_id: 2, server_name: 'jp-02', service: 'sing-box', state: 'failed' }],
    },
  }],
  [/\/api\/uuids/, { success: true, total: 137, page: 1, size: 10, data: users }],
  [/\/api\/servers\/status/, { success: true, total: 4, online: 3, offline: 1, unknown: 0, data: [] }],
  [/\/api\/servers/, {
    success: true, total: 4,
    data: servers.map((s) => ({
      id: s.id, name: s.name, last_status: s.status, last_ip: s.last_ip, hostname: s.hostname,
      ip_country: s.ip_country, ip_region: s.ip_region, agent_version: s.agent_version,
      agent_last_seen: s.last_seen, agent_next_poll_sec: 30, has_agent_token: true,
      agent_token_hint: 'pk_7f3c…9a1b', tags: s.name.slice(0, 2).toUpperCase(), last_error: s.status === 'offline' ? '心跳超时 3 小时' : '',
    })),
  }],
  [/\/api\/templates\/manage/, {
    success: true, total: 3, page: 1, size: 10,
    data: [
      { id: 1, name: '默认 Clash 模板', url: 'https://s3.example.com/rule/clash/default.yaml', description: '全局策略组 + 地区分流', is_default: 1, created_at: NOW - 86400000 * 60 },
      { id: 2, name: 'Mihomo 精简', url: 'https://s3.example.com/rule/clash/lite.yaml', description: '轻量版，移动端友好', is_default: 0, created_at: NOW - 86400000 * 31 },
      { id: 3, name: '测试模板', url: 'https://s3.example.com/rule/clash/test.yaml', description: '', is_default: 0, created_at: NOW - 86400000 * 8 },
    ],
  }],
  [/\/api\/bucket\/files/, {
    success: true, prefix: 'rule', root: 'rule', truncated: false,
    files: [
      { key: 'rule/clash/default.yaml', size: 18234, uploaded: iso(NOW - 86400000 * 3) },
      { key: 'rule/clash/lite.yaml', size: 9210, uploaded: iso(NOW - 86400000 * 9) },
      { key: 'rule/korea.list', size: 8475, uploaded: iso(NOW - 86400000 * 20) },
      { key: 'rule/se_asia.list', size: 6495, uploaded: iso(NOW - 86400000 * 20) },
    ],
  }],
  [/\/api\/singbox\/templates\/preview/, { success: true, ports: { hy2: 13701, ss: 13702, reality: 13705 }, users: 2, files: [{ name: 'sb.json', content: SAMPLE_SB }, { name: 'Caddyfile', content: ':443 {\n  tls internal\n}' }] }],
  [/\/api\/singbox\/templates/, {
    success: true,
    data: [
      { id: 1, name: 'sb-main', remark: '主力协议组合', protocols: ['hy2', 'ss', 'anytls', 'reality', 'vlws'], port_base: 13700, user_count: 12, warp_enabled: 1, enabled: 1, updated_at: NOW - 900000 },
      { id: 2, name: 'xray-edge', remark: 'Xray 备用线路', protocols: ['xhttp'], port_base: 13800, user_count: 5, warp_enabled: 0, enabled: 1, updated_at: NOW - 86400000 * 5 },
    ],
  }],
  [/\/api\/singbox\/nodes/, {
    success: true,
    data: [
      { id: 1, name: 'hk-node', server_id: 1, server_name: 'hk-01', server_host: '203.0.113.11', template_id: 1, template_name: 'sb-main', domain: 'hk.example.com', uuid_list: [users[0].uuid, users[1].uuid], enabled: 1, last_apply_at: NOW - 600000, remark: '' },
      { id: 2, name: 'jp-node', server_id: 2, server_name: 'jp-02', server_host: '198.51.100.22', template_id: 1, template_name: 'sb-main', domain: 'jp.example.com', uuid_list: [users[0].uuid], enabled: 1, last_apply_at: NOW - 86400000, remark: '' },
    ],
  }],
  [/\/api\/singbox\/uuids/, {
    success: true,
    data: users.map((u) => ({ uuid: u.uuid, node_count: u.count, owner: u.owner, email: u.email, status: u.status, remark: u.remark })),
  }],
  [/\/api\/logs\/summary/, {
    success: true, total: 24318, oldest: NOW - 86400000 * 214, newest: NOW - 42000,
    subscription: 20114, api_generation: 4204,
    older_than: { '30d': 1240, '90d': 8210, '180d': 16240, '365d': 22110 },
  }],
  [/\/api\/node-configs\/logs/, {
    success: true, total: 3,
    data: [
      { id: 31, action: 'singbox-apply', status: 'success', server_name: 'hk-01', config_name: 'sb-main', message: '写入 3 个文件，sing-box 已重启', created_at: NOW - 120000 },
      { id: 30, action: 'deploy', status: 'failed', server_name: 'jp-02', config_name: 'sb-main', message: 'connect timeout 15s', created_at: NOW - 1800000 },
      { id: 29, action: 'agent-token', status: 'success', server_name: 'sg-03', message: '轮换接入令牌', created_at: NOW - 5400000 },
    ],
  }],
  [/\/api\/admin\/users/, {
    users: [
      { id: 1, username: 'admin', email: 'admin@example.com', email_verified: 1, email_verified_at: NOW, mfa_enabled: 1, last_mfa_login: NOW - 3600000, created_at: NOW - 86400000 * 120 },
      { id: 2, username: 'ops', email: 'ops@example.com', email_verified: 1, email_verified_at: NOW, mfa_enabled: 0, created_at: NOW - 86400000 * 30 },
      { id: 3, username: 'support', email: '', email_verified: 0, mfa_enabled: 0, created_at: NOW - 86400000 * 6 },
    ],
    self: 'admin',
  }],
  [/\/api\/apikeys/, {
    success: true, total: 2,
    data: [
      { id: 1, key_name: 'ci-deploy', key_hint: 'pk_9a3f…c21d', enabled: 1, permissions: 'groups:read,configs:write', permissions_text: '配置分组 · 读取，节点配置 · 写入', expires_at: 0, usage_count: 428, last_used_at: NOW - 90000, created_at: NOW - 86400000 * 40 },
      { id: 2, key_name: 'monitor-ro', key_hint: 'pk_11bc…7e08', enabled: 0, permissions: 'groups:read', permissions_text: '配置分组 · 读取', expires_at: NOW + 86400000 * 30, usage_count: 12, last_used_at: NOW - 86400000 * 2, created_at: NOW - 86400000 * 12 },
    ],
  }],
  [/\/api\/mfa\/status/, { mfa_enabled: true, last_mfa_login: NOW - 3600000, backup_codes_remaining: 7 }],
  // 服务器详情（放在 /api/servers 通配之前，靠顺序优先匹配）
  [/\/api\/servers\/\d+$/, {
    success: true,
    data: {
      id: 1, name: 'hk-01', last_status: 'online', last_ip: '203.0.113.11', ip_country: 'HK', ip_region: 'Central',
      tags: '香港,核心', remark: '主力节点', enabled: true, allow_exec: false, agent_version: '1.2.7',
      agent_last_seen: NOW - 24000, agent_next_poll_sec: 30, last_latency_ms: 42,
      last_metrics: {
        hostname: 'hk-01', kernel: '6.1.0-18-amd64', os: 'Debian GNU/Linux 12', uptimeSec: 864000 * 2, cores: 8,
        cpuPercent: 34, memPercent: 56, diskPercent: 41, memUsedMb: 4480, memTotalMb: 8000,
        load1: 0.42, load5: 0.51, load15: 0.6,
        services: { 'sing-box': 'active', caddy: 'active', sshd: 'active' },
        net: { rxMbps: 12.4, txMbps: 3.1 }, conn: { tcp: 128, udp: 24 },
        proc: [{ name: 'sing-box', cpu: 12.4, mem: 86 }, { name: 'caddy', cpu: 1.2, mem: 42 }],
      },
    },
    metrics_recent: [0, 1, 2, 3, 4, 5, 6, 7].map((i) => ({
      status: 'online', latency_ms: 38 + ((i * 7) % 12), cpu_percent: 24 + ((i * 5) % 22), mem_percent: 52 + ((i * 3) % 12),
      disk_percent: 41, load1: 0.4, created_at: NOW - (7 - i) * 300000,
    })),
    node_configs: [{ id: 1, name: 'sb-main', engine: 'sing-box', target_path: '/etc/sing-box/sb.json', service_name: 'sing-box', enabled: 1, last_push_status: 'success' }],
  }],
  [/\/api\/agent\/tasks/, { success: true, id: 1 }],
  [/\/api\/monitor\/notify/, { success: true, enabled: true, emails: 'ops@example.com', cooldown_min: 10, email_ready: true }],
  [/\/api\/admin\/users\/email\//, { success: true, email: 'me@example.com', email_verified: true }],
  // 用户中心 · 单个 UUID 管理
  [/\/api\/templates/, [{ id: 1, name: '内置默认' }, { id: 2, name: '高倍率' }]],
  [/\/manage\/configs\/[^/]+/, {
    uuid: '11111111-1111-4111-8111-111111111111',
    configs: [
      { id: 1, protocol: 'vless', remark: '香港 01', config_data: 'vless://aaaa@hk1.example.com:443?type=ws&security=tls#HK-01' },
      { id: 2, protocol: 'trojan', remark: '日本 02', config_data: 'trojan://bbbb@jp1.example.com:443#JP-02' },
      { id: 3, protocol: 'hysteria2', remark: '', config_data: 'hysteria2://cccc@us1.example.com:443#US-01' },
    ],
    pagination: { page: 1, pageSize: 10, total: 3, totalPages: 1 },
    filters: { protocol: 'all', q: '' },
  }],
  [/\/manage\/stats\//, {
    success: true, total_access: 128, subscription_count: 96, apigen_count: 32,
    today_total: 7, today_subscription: 5, today_apigen: 2,
    daily_stats: Array.from({ length: 14 }, (_, i) => ({ date: '2026-10-' + String(i + 14).padStart(2, '0'), total: (i * 7) % 13 })),
    recent_logs: [
      { id: 1, created_at: NOW - 60000, query_type: 'subscription', ip_address: '203.0.113.7' },
      { id: 2, created_at: NOW - 3600000, query_type: 'api-generation', ip_address: '203.0.113.9' },
    ],
  }],
  [/\/manage\/uuid-info\//, { success: true, owner: 'alice', email: 'me@example.com', contact_info: 'tg:@alice', remark: '主力节点' }],
  [/\/manage\/uuid-sub-sources/, { success: true, data: [{ id: 9, url: 'https://ext.example.com/sub/abc', remark: '机场A', enabled: 1 }] }],
  // 用户中心
  [/\/api\/portal\/send-code/, { success: true, email: 'me@example.com' }],
  [/\/api\/portal\/login/, { token: 'portal-preview-token', email: 'me@example.com', expires_in: 3600 }],
  [/\/api\/portal\/uuids/, {
    success: true, email: 'me@example.com', total: 2,
    data: [
      {
        uuid: '11111111-1111-4111-8111-111111111111', owner: 'alice', remark: '主力节点', status: 'active', configs: 12,
        created_at: NOW - 86400000 * 30, updated_at: NOW - 3600000,
        sub: {
          raw: 'https://x.local/sub/11111111-1111-4111-8111-111111111111',
          batch: 'https://x.local/batch-configs/11111111-1111-4111-8111-111111111111',
          clash: 'https://x.local/clash-config?uuid=11111111-1111-4111-8111-111111111111',
        },
      },
      {
        uuid: '22222222-2222-4222-8222-222222222222', owner: 'alice', remark: '备用线路', status: 'suspended', configs: 3,
        created_at: NOW - 86400000 * 8, updated_at: NOW - 86400000,
        sub: {
          raw: 'https://x.local/sub/22222222-2222-4222-8222-222222222222',
          clash: 'https://x.local/clash-config?uuid=22222222-2222-4222-8222-222222222222',
        },
      },
    ],
  }],
  [/\/api\/singbox\/push/, { success: true, ok_count: 1, total: 1, data: [{ id: 1, ok: true }] }],
];

const mockScript = (MODE) => `
(function () {
  var MODE = '${MODE}';
  var NOW = ${NOW};
  var ISO = function (ms) { return new Date(ms).toISOString(); };
  var RESPONSES = ${JSON.stringify(RESPONSES.map(([re, body]) => [re.source, body]))};
  var TABLE = RESPONSES.map(function (p) { return [new RegExp(p[0]), p[1]]; });

  // 内存版 localStorage：file:// 下也能预置登录态
  var mem = new Map();
  var fakeStorage = {
    getItem: function (k) { return mem.has(String(k)) ? mem.get(String(k)) : null; },
    setItem: function (k, v) { mem.set(String(k), String(v)); },
    removeItem: function (k) { mem.delete(String(k)); },
    clear: function () { mem.clear(); },
    key: function (i) { return Array.from(mem.keys())[i] || null; }
  };
  Object.defineProperty(fakeStorage, 'length', { get: function () { return mem.size; } });
  try { Object.defineProperty(window, 'localStorage', { value: fakeStorage, configurable: true }); } catch (e) {}

  if (MODE === 'portal') {
    // 用户中心预览：直接进「我的订阅」
    fakeStorage.setItem('pp_portal_token', 'portal-preview-token');
    window.__PP_BOOT__ = { mode: 'portal', version: 'V13.2 · 预览', builtAt: '${iso(NOW)}', turnstile: '' };
  } else {
    var payload = btoa(JSON.stringify({ sub: 'admin', exp: Math.floor(NOW / 1000) + 86400 }));
    var token = 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.' + payload.replace(/=+$/, '').replace(/\\+/g, '-').replace(/\\//g, '_') + '.preview';
    fakeStorage.setItem('token', token);
    window.__PP_BOOT__ = {
      mode: 'admin',
      superAdmin: 'admin', version: 'V13.2 · 预览', builtAt: '${iso(NOW)}', flavor: 'antd',
      turnstile: '', emailLogin: true,
      oidc: { enabled: true, buttonText: '使用企业 SSO 登录', passwordDisabled: false }
    };
  }

  var realFetch = window.fetch ? window.fetch.bind(window) : null;
  window.fetch = function (input, init) {
    var url = String(input && input.url ? input.url : input);
    if (url.indexOf('/api/') < 0) return realFetch ? realFetch(input, init) : Promise.reject(new Error('offline'));
    var hit = null;
    for (var i = 0; i < TABLE.length; i++) { if (TABLE[i][0].test(url)) { hit = TABLE[i][1]; break; } }
    var body = hit || { success: true, data: [] };
    return Promise.resolve(new Response(JSON.stringify(body), {
      status: 200, headers: { 'Content-Type': 'application/json' }
    }));
  };
  console.log('[preview] 已启用打桩接口，共 ' + TABLE.length + ' 条规则');
})();
`;

const buildHtml = (mode) => {
  const isPortal = mode === 'portal';
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>${isPortal ? '我的订阅 · 用户中心预览' : 'ProxyPilot 控制台 · Ant Design 预览'}</title>
<style>${appCss}</style>
</head>
<body>
<div id="root">
  <div class="pp-boot">
    <div class="pp-boot-logo"><img src="https://s3.yangzifun.org/logo.png" alt="ProxyPilot"></div>
    <div class="pp-boot-title">${isPortal ? '我的订阅' : 'ProxyPilot 控制台'}</div>
    <div class="pp-boot-sub">正在加载…</div>
    <div class="pp-boot-bar"><i></i></div>
  </div>
</div>
<script>${mockScript(mode)}</script>
<script>${appJs}</script>
</body>
</html>`;
};

fs.mkdirSync(outDir, { recursive: true });
const outFile = path.join(outDir, 'index.html');
const html = buildHtml('admin');
fs.writeFileSync(outFile, html, 'utf8');

const portalFile = path.join(outDir, 'portal.html');
const portalHtmlText = buildHtml('portal');
fs.writeFileSync(portalFile, portalHtmlText, 'utf8');

console.log('预览页已生成：' + outFile);
console.log('  （后台）大小：' + (html.length / 1024).toFixed(0) + ' KB');
console.log('用户中心预览：' + portalFile);
console.log('  （用户端）大小：' + (portalHtmlText.length / 1024).toFixed(0) + ' KB');
console.log('后台页面：#dash / #uuids / #tpls / #servers / #singbox / #status / #logs / #sys / #sec');
console.log('用户端：直接打开 portal.html 即进入「我的订阅」；把窗口拉窄可看移动端布局');
