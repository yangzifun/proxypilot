/* =================================================================
 *  web-antd / scripts / render-check.mjs
 *  用 jsdom 真跑一遍前端 bundle（dist/app.js）：
 *    1) 未登录 → 渲染 Ant Design 登录页
 *    2) 已登录 → 渲染外壳 + 系统概览（指标卡 / 面积图 / 环形图 / 表格）
 *    3) #status → 服务器监控（仪表盘 / 世界地图 / 服务器卡片）
 *    4) #sys   → 系统管理（账号表 / 密钥表）
 *  fetch 全部打桩（返回 worker.js 真实响应结构的样例）。
 *  用法：node scripts/render-check.mjs
 * ================================================================= */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';

const here = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(here, '..');
const appJs = fs.readFileSync(path.join(webRoot, 'dist', 'app.js'), 'utf8');
const appCss = fs.readFileSync(path.join(webRoot, 'dist', 'app.css'), 'utf8');

let pass = 0;
let fail = 0;
function check(name, ok, detail) {
  if (ok) {
    pass += 1;
    console.log('  \u2705', name);
  } else {
    fail += 1;
    console.log('  \u274c', name, detail ? '\n     ' + String(detail).slice(0, 600) : '');
  }
}

/* ---------------- 打桩数据 ---------------- */

const NOW = Date.now();
const daily = [];
for (let i = 20; i >= 0; i--) {
  const d = new Date(NOW - i * 86400000).toISOString().slice(0, 10);
  daily.push({ date: d, total: 40 + ((i * 37) % 90), subscription: 20 + ((i * 13) % 40), api_generation: 5 + (i % 17), unique_uuids: 3 + (i % 5) });
}

const RESPONSES = [
  [/\/api\/stats/, {
    uuids: 128, templates: 6,
    access_stats: {
      success: true, total_requests: 18234, unique_uuids: 97, subscription_requests: 15110, api_generation_requests: 3124,
      today_total: 402, today_subscription: 331, today_apigen: 71,
      daily_stats: daily,
      popular_uuids: [
        { uuid: '11111111-2222-3333-4444-555555555555', access_count: 1200, subscription_count: 1100, apigen_count: 100, owner: 'alice' },
        { uuid: '22222222-3333-4444-5555-666666666666', access_count: 900, subscription_count: 850, apigen_count: 50, owner: 'bob' },
      ],
      latest_logs: [
        { uuid: '11111111-2222-3333-4444-555555555555', query_type: 'subscription', created_at: NOW - 60000, owner: 'alice' },
        { uuid: '22222222-3333-4444-5555-666666666666', query_type: 'api-generation', created_at: NOW - 900000, owner: 'bob' },
      ],
    },
  }],
  [/\/api\/servers\/monitor/, {
    success: true,
    servers: [
      { id: 1, name: 'hk-01', status: 'online', hostname: 'hk01', last_ip: '1.1.1.1', ip_country: 'HK', ip_region: 'Central',
        cpu_percent: 32, cores: 4, mem_percent: 58, mem_used_mb: 900, mem_total_mb: 1600, disk_percent: 41,
        uptime_sec: 864000, load1: 0.4, load5: 0.5, load15: 0.6, last_seen: NOW - 30000, agent_version: '1.2.7',
        services: { singbox: 'active', sshd: 'active' } },
      { id: 2, name: 'jp-02', status: 'online', hostname: 'jp02', last_ip: '2.2.2.2', ip_country: 'JP', ip_region: 'Tokyo',
        cpu_percent: 81, cores: 8, mem_percent: 72, mem_used_mb: 2000, mem_total_mb: 2800, disk_percent: 66,
        uptime_sec: 360000, load1: 2.1, load5: 1.8, load15: 1.5, last_seen: NOW - 20000, agent_version: '1.2.7',
        services: { singbox: 'failed', sshd: 'active' } },
      { id: 3, name: 'us-03', status: 'offline', hostname: '', last_ip: '3.3.3.3', ip_country: 'US', ip_region: 'LA',
        cpu_percent: 0, mem_percent: 0, disk_percent: 0, last_seen: NOW - 7200000, services: {} },
    ],
    watch: ['singbox', 'sshd'],
    matrix: [
      { service: 'singbox', states: [{ server_id: 1, state: 'active' }, { server_id: 2, state: 'failed' }, { server_id: 3, state: 'offline' }] },
      { service: 'sshd', states: [{ server_id: 1, state: 'active' }, { server_id: 2, state: 'active' }, { server_id: 3, state: 'offline' }] },
    ],
    summary: { servers: 3, online: 2, offline: 1, failed_services: 1, failed_list: [{ server_id: 2, server_name: 'jp-02', service: 'singbox', state: 'failed' }], not_installed: 0 },
  }],
  [/\/api\/admin\/users/, { users: [
    { id: 1, username: 'admin', email: 'admin@example.com', email_verified: 1, email_verified_at: NOW, mfa_enabled: 1, last_mfa_login: NOW - 3600000, created_at: NOW - 86400000 * 30 },
    { id: 2, username: 'ops', email: '', email_verified: 0, mfa_enabled: 0, created_at: NOW - 86400000 * 3 },
  ], self: 'admin' }],
  [/\/api\/apikeys/, { success: true, total: 1, data: [
    { id: 1, key_name: 'ci-deploy', key_hint: 'pk_ab12…', enabled: 1, permissions: 'groups:read,configs:write', permissions_text: '配置分组 · 读取，节点配置 · 写入', expires_at: 0, usage_count: 42, last_used_at: NOW - 60000, created_at: NOW - 86400000 },
  ] }],
  [/\/api\/mfa\/status/, { mfa_enabled: true, last_mfa_login: NOW - 3600000, backup_codes_remaining: 8 }],
  [/\/api\/logs\/summary/, { success: true, total: 15234, oldest: NOW - 86400000 * 200, newest: NOW - 1000, subscription: 13000, api_generation: 2234, older_than: { '30d': 120, '90d': 800, '180d': 4200, '365d': 9000 } }],
  [/\/api\/node-configs\/logs/, { success: true, total: 2, data: [
    { id: 9, action: 'singbox-apply', status: 'success', server_name: 'hk-01', config_name: 'sg-1', message: '已写入 3 个文件并重启', created_at: NOW - 120000 },
    { id: 8, action: 'agent-token', status: 'success', server_name: 'jp-02', message: '轮换令牌', created_at: NOW - 900000 },
  ] }],
  [/\/api\/servers\/status/, { success: true, total: 2, online: 1, offline: 1, unknown: 0, data: [] }],
  [/\/api\/singbox\/templates/, { success: true, data: [
    { id: 1, name: 'sb-default', protocols: ['hy2', 'ss', 'anytls', 'reality', 'vlws'], port_base: 13700, user_count: 12, warp_enabled: 1, enabled: 1, updated_at: NOW - 900000, remark: '' },
  ] }],
  [/\/api\/singbox\/nodes/, { success: true, data: [
    { id: 1, name: 'hk-node', server_id: 1, server_name: 'hk-01', server_host: '1.1.1.1', template_id: 1, template_name: 'sb-default', domain: 'hk.example.com', uuid_list: ['11111111-2222-3333-4444-555555555555'], enabled: 1, last_apply_at: NOW - 600000 },
  ] }],
  [/\/api\/singbox\/uuids/, { success: true, data: [{ uuid: '11111111-2222-3333-4444-555555555555', node_count: 3, owner: 'alice', email: 'a@b.com', status: 'active' }] }],
  [/\/api\/servers/, { success: true, total: 3, data: [
    { id: 1, name: 'hk-01', last_status: 'online', last_ip: '1.1.1.1', ip_country: 'HK', ip_region: 'Central', agent_version: '1.2.7', agent_last_seen: NOW - 30000, agent_next_poll_sec: 30, agent_token_hint: 'pk_…ab12', tags: '香港,核心', has_agent_token: true },
    { id: 2, name: 'jp-02', last_status: 'online', last_ip: '2.2.2.2', ip_country: 'JP', agent_version: '1.2.7', agent_last_seen: NOW - 20000, agent_next_poll_sec: 30, tags: '东京' },
    { id: 3, name: 'us-03', last_status: 'offline', last_ip: '3.3.3.3', ip_country: 'US', agent_last_seen: NOW - 7200000, agent_next_poll_sec: 30, tags: '美西' },
  ] }],
  [/\/api\/uuids/, { success: true, total: 128, page: 1, size: 10, data: [
    { uuid: '11111111-2222-3333-4444-555555555555', owner: 'alice', email: 'a@b.com', contact_info: '', status: 'active', remark: '', access_count: 1200, last_accessed: NOW - 60000, created_at: NOW - 86400000 * 20, updated_at: NOW - 60000, count: 12, node_count: 12 },
    { uuid: '22222222-3333-4444-5555-666666666666', owner: 'bob', email: 'b@b.com', contact_info: 'tg:bob', status: 'suspended', remark: '月付', access_count: 900, last_accessed: NOW - 900000, created_at: NOW - 86400000 * 10, updated_at: NOW - 900000, count: 7, node_count: 7 },
  ] }],
  [/\/api\/templates\/manage/, { success: true, total: 2, page: 1, size: 10, data: [
    { id: 1, name: '默认 Clash', url: 'https://s3.example.com/clash.yaml', description: '默认模板', is_default: 1, created_at: NOW - 86400000 },
  ] }],
  [/\/api\/bucket\/files/, { success: true, prefix: 'rule', root: 'rule', files: [{ key: 'rule/clash.yaml', size: 4096, uploaded: new Date(NOW).toISOString() }], truncated: false }],
];

function fakeFetch(url) {
  const u = String(url);
  const hit = RESPONSES.find(([re]) => re.test(u));
  const body = hit ? hit[1] : { success: true, data: [] };
  return Promise.resolve({
    ok: true,
    status: 200,
    headers: { get: () => 'application/json' },
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  });
}

/* ---------------- 启动一个 jsdom 页面 ---------------- */
async function mount({ token, hash = '#dash' }) {
  const errors = [];
  const vc = new VirtualConsole();
  vc.on('jsdomError', (e) => {
    if (!/Could not parse CSS|Not implemented/.test(String(e && e.message))) errors.push(e);
  });
  vc.on('error', (e) => errors.push(e));

  const dom = new JSDOM(
    `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><style>${appCss}</style></head>
     <body><div id="root"></div></body></html>`,
    { url: 'https://example.workers.dev/admin' + hash, runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: vc }
  );
  const { window } = dom;

  // 浏览器能力垫片
  window.matchMedia = window.matchMedia || ((q) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
    dispatchEvent() { return false; },
  }));
  /* antd 6 的批处理依赖 MessageChannel（jsdom 没有），补一个最小实现 */
  if (!window.MessageChannel) {
    window.MessageChannel = class {
      constructor() {
        const make = () => ({ onmessage: null, onmessageerror: null, _peer: null, close() {}, postMessage(data) { const peer = this._peer; setTimeout(() => { if (peer && peer.onmessage) peer.onmessage({ data }); }, 0); }, start() {} });
        this.port1 = make();
        this.port2 = make();
        this.port1._peer = this.port2;
        this.port2._peer = this.port1;
      }
    };
  }
  window.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  window.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
  Object.defineProperty(window.HTMLElement.prototype, 'clientWidth', { configurable: true, get() { return 820; } });
  Object.defineProperty(window.HTMLElement.prototype, 'clientHeight', { configurable: true, get() { return 400; } });
  window.HTMLElement.prototype.getBoundingClientRect = function () {
    return { width: 820, height: 400, top: 0, left: 0, right: 820, bottom: 400, x: 0, y: 0, toJSON() {} };
  };
  window.scrollTo = () => {};
  window.fetch = fakeFetch;
  window.__PP_BOOT__ = {
    superAdmin: 'admin', version: 'V13.2', builtAt: 'test', flavor: 'antd',
    turnstile: '', emailLogin: true,
    oidc: { enabled: true, buttonText: '使用 SSO 登录', passwordDisabled: false },
  };
  window.localStorage.clear();
  if (token) window.localStorage.setItem('token', token);

  const script = window.document.createElement('script');
  script.textContent = appJs;
  window.document.body.appendChild(script);

  await new Promise((r) => setTimeout(r, 450));
  return { dom, window, errors };
}

const fakeJwt = [
  Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url'),
  Buffer.from(JSON.stringify({ sub: 'admin', exp: Math.floor(NOW / 1000) + 86400 })).toString('base64url'),
  'sig',
].join('.');

/* ---------------- 断言 ---------------- */
// 注意：bundle 是以 <script> 注入的，body.textContent 会把源码也算进去，先剔除脚本/样式
const text = (win) => {
  const clone = win.document.body.cloneNode(true);
  clone.querySelectorAll('script, style, noscript').forEach((n) => n.remove());
  return (clone.textContent || '').replace(/\s+/g, ' ');
};
const q = (win, sel) => win.document.querySelectorAll(sel).length;

console.log('\nProxyPilot · Ant Design 前端渲染检查\n');

/* 1) 未登录 → 登录页 */
{
  const { window, errors } = await mount({ token: '' });
  check('未登录渲染登录页', q(window, '.pp-login') === 1, 'pp-login=' + q(window, '.pp-login'));
  check('登录页有品牌与标语', text(window).includes('ProxyPilot') && text(window).includes('登录控制台'));
  check('登录页渲染 antd 输入框', q(window, 'input') >= 2, 'inputs=' + q(window, 'input'));
  check('登录页有 OIDC 按钮', text(window).includes('使用 SSO 登录'));
  check('登录页有邮箱验证码页签', text(window).includes('邮箱验证码'));
  check('登录页无运行时错误', errors.length === 0, errors.map((e) => e && e.message).join(' | '));
}

/* 2) 已登录 → 外壳 + 系统概览 */
{
  const { window, errors } = await mount({ token: fakeJwt, hash: '#dash' });
  await new Promise((r) => setTimeout(r, 250));
  const body = text(window);
  check('渲染侧边菜单（9 个入口）', ['系统概览', '订阅分组', '订阅模板', '服务器管理', '节点管理', '服务器监控', '日志管理', '系统管理', '安全中心']
    .every((t) => body.includes(t)), body.slice(0, 200));
  check('顶栏显示登录账号', body.includes('admin'));
  check('概览：4 个指标卡', q(window, '.pp-stat') >= 4, 'stat=' + q(window, '.pp-stat'));
  check('概览：指标数值已填充', /18,234|18234|1\.8万/.test(body), body.slice(0, 300));
  check('概览：面积图已绘制（svg path）', q(window, '.pp-chart svg path') >= 2, 'paths=' + q(window, '.pp-chart svg path'));
  check('概览：环形图已绘制', q(window, '.pp-donut svg circle') >= 2, 'circles=' + q(window, '.pp-donut svg circle'));
  check('概览：热门订阅条形列表', q(window, '.pp-barlist-row') >= 1, 'rows=' + q(window, '.pp-barlist-row'));
  check('概览：最新订阅时间线', q(window, '.ant-timeline-item') >= 2, 'items=' + q(window, '.ant-timeline-item'));
  check('概览：无运行时错误', errors.length === 0, errors.map((e) => e && e.message).join(' | '));
}

/* 3) 服务器监控 */
{
  const { window, errors } = await mount({ token: fakeJwt, hash: '#status' });
  await new Promise((r) => setTimeout(r, 300));
  const body = text(window);
  check('监控：标题正确', body.includes('服务器监控'));
  check('监控：仪表盘渲染', q(window, '.pp-gauge svg') >= 2, 'gauges=' + q(window, '.pp-gauge svg'));
  check('监控：柱状图渲染', q(window, '.pp-chart svg rect') >= 3, 'rects=' + q(window, '.pp-chart svg rect'));
  check('监控：服务矩阵已移除（改在详情里看）', !body.includes('服务状态矩阵'), body.slice(-200));
  check('监控：服务器以卡片展示', q(window, '.pp-srv-card') >= 1, 'cards=' + q(window, '.pp-srv-card'));
  check('监控：告警列出失败服务', body.includes('jp-02'));
  check('监控：无运行时错误', errors.length === 0, errors.map((e) => e && e.message).join(' | '));
}

/* 4) 系统管理 */
{
  const { window, errors } = await mount({ token: fakeJwt, hash: '#sys' });
  await new Promise((r) => setTimeout(r, 300));
  const body = text(window);
  check('系统管理：超管身份生效（非 403）', !body.includes('仅超级管理员可访问'), body.slice(0, 160));
  check('系统管理：账号表渲染出 rows', body.includes('ops') && body.includes('admin'), body.slice(0, 240));
  check('系统管理：指标卡 + 表格', q(window, '.pp-stat') >= 4 && q(window, '.ant-table') >= 1, 'stat=' + q(window, '.pp-stat') + ' tables=' + q(window, '.ant-table'));
  check('系统管理：无运行时错误', errors.length === 0, errors.map((e) => e && e.message).join(' | '));
}

/* 5) 其余页面全量过一遍：不能有运行时错误，且关键内容要出现 */
{
  const ROUTES = [
    { hash: '#uuids', name: '订阅分组', need: ['订阅分组', 'alice', '新建分组', 'b@b.com'] },
    { hash: '#tpls', name: '订阅模板', need: ['订阅模板', '默认 Clash', '新建模板', '对象存储文件'] },
    { hash: '#servers', name: '服务器管理', need: ['服务器列表', 'hk-01', '检测全部', '接入服务器'] },
    { hash: '#singbox', name: '节点管理', need: ['协议模板', 'sb-default', '新建模板', '节点绑定（'] },
    { hash: '#logs', name: '日志管理', need: ['日志总量', '按保留期的可删行数', '执行清理', '下发记录'] },
    { hash: '#sec', name: '安全中心', need: ['修改密码', '双因素认证', '重新生成备份码', '关闭双因素认证'] },
  ];

  for (const r of ROUTES) {
    // eslint-disable-next-line no-await-in-loop
    const { window, errors } = await mount({ token: fakeJwt, hash: r.hash });
    // eslint-disable-next-line no-await-in-loop
    await new Promise((res) => setTimeout(res, 320));
    const body = text(window);
    const missing = r.need.filter((n) => !body.includes(n));
    check(`${r.name}：关键内容渲染`, missing.length === 0, '缺少：' + missing.join('、') + '\n     ' + body.slice(0, 260));
    check(`${r.name}：无运行时错误`, errors.length === 0, errors.map((e) => e && e.message).join(' | '));
    window.close();
  }
}

console.log(`\n结果：${pass} 通过，${fail} 失败\n`);
process.exit(fail ? 1 : 0);
