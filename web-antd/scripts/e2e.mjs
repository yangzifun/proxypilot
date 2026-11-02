/* =================================================================
 *  web-antd / scripts / e2e.mjs
 *  本地模拟环境：内存版后端 + jsdom 真点真填，跑通关键业务流程。
 *
 *  与 smoke / render-check 的区别：
 *    · smoke   ：Worker 路由（谁回 html、谁转发）
 *    · render-chk：页面能不能画出来
 *    · e2e     ：把按钮点下去、把表填上，断言「发出的请求体」和「界面的变化」
 *
 *  用法：node scripts/e2e.mjs
 * ================================================================= */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';

const here = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(here, '..');
const appJs = fs.readFileSync(path.join(webRoot, 'dist', 'app.js'), 'utf8');
const appCss = fs.readFileSync(path.join(webRoot, 'dist', 'app.css'), 'utf8');

// 界面里某次表单校验失败会以 rejected promise 冒出来，别让它中断整轮测试
process.on('unhandledRejection', (e) => {
  console.log('  \u26a0\ufe0f  未捕获的 promise 拒绝：' + (e && e.message ? e.message : JSON.stringify(e)));
});

/* ================= 内存后端 ================= */

const NOW = Date.now();
const state = {
  uuids: [
    { uuid: '11111111-1111-4111-8111-111111111111', owner: 'alice', email: 'alice@example.com', contact_info: '', status: 'active', remark: '', access_count: 120, last_accessed: NOW - 60000, created_at: NOW - 86400000 * 20, updated_at: NOW - 60000, count: 3 },
    { uuid: '22222222-2222-4222-8222-222222222222', owner: 'bob', email: 'bob@example.com', contact_info: '', status: 'suspended', remark: '月付', access_count: 88, last_accessed: NOW - 900000, created_at: NOW - 86400000 * 9, updated_at: NOW - 900000, count: 2 },
  ],
  templates: [
    { id: 1, name: '默认 Clash', url: 'https://s3.example.com/clash.yaml', description: '默认', is_default: 1, created_at: NOW - 86400000 },
    { id: 2, name: '备用模板', url: 'https://s3.example.com/alt.yaml', description: '', is_default: 0, created_at: NOW - 86400000 * 3 },
  ],
  servers: [
    { id: 1, name: 'hk-01', last_status: 'online', last_ip: '1.1.1.1', last_ip4: '1.1.1.1', last_ip6: '2001:db8::1', ip_country: 'HK', ip_region: '香港', agent_version: '1.2.7', agent_last_seen: NOW - 30000, agent_next_poll_sec: 30, tags: '香港', has_agent_token: true, agent_token_hint: 'pk_…ab12', allow_exec: true, agent_update: true, agent_latest: '1.2.8' },
    { id: 2, name: 'jp-02', last_status: 'offline', last_ip: '2001:db8::2', last_ip4: '', last_ip6: '2001:db8::2', ip_country: 'JP', ip_region: '东京', agent_version: '1.2.7', agent_last_seen: NOW - 7200000, agent_next_poll_sec: 30, tags: '东京' },
  ],
  apikeys: [
    { id: 1, key_name: 'ci-deploy', key_hint: 'pk_9a3f…', enabled: 1, permissions: 'groups:read', permissions_text: '配置分组 · 读取', expires_at: 0, usage_count: 5, last_used_at: NOW - 60000, created_at: NOW - 86400000 },
  ],
  mfa: { enabled: false, backupRemaining: 0, lastLogin: 0 },
  logs: { total: 15234, oldest: NOW - 86400000 * 200, newest: NOW - 1000, subscription: 13000, api_generation: 2234, older_than: { '30d': 120, '90d': 800, '180d': 4200, '365d': 9000 } },
  users: [
    { id: 1, username: 'admin', email: 'admin@example.com', email_verified: 1, email_verified_at: NOW, mfa_enabled: 1, last_mfa_login: NOW - 3600000, created_at: NOW - 86400000 * 120 },
    { id: 2, username: 'ops', email: '', email_verified: 0, mfa_enabled: 0, created_at: NOW - 86400000 * 30 },
  ],
  bucket: [{ key: 'rule/clash.yaml', size: 2048, uploaded: new Date(NOW).toISOString() }],
  singboxTpls: [
    { id: 1, name: 'sb-main', remark: '', protocols: ['hy2', 'ss'], port_base: 13700, user_count: 2, warp_enabled: 1, enabled: 1, updated_at: NOW - 1000 },
  ],
  singboxNodes: [
    { id: 1, name: 'hk-node', server_id: 1, server_name: 'hk-01', server_host: '1.1.1.1', template_id: 1, template_name: 'sb-main', domain: 'hk.example.com', uuid_list: ['11111111-1111-4111-8111-111111111111'], enabled: 1, last_apply_at: NOW - 1000, warp_enabled: -1, warp_effective: 1 },
  ],
  configs: [
    { id: 1, uuid: '11111111-1111-4111-8111-111111111111', protocol: 'vless', config_data: 'vless://abc@1.2.3.4:443?type=ws#hk', remark: 'hk', updated_at: NOW - 1000 },
    { id: 2, uuid: '11111111-1111-4111-8111-111111111111', protocol: 'hy2', config_data: 'hysteria2://xyz@1.2.3.5:443#jp', remark: 'jp', updated_at: NOW - 2000 },
  ],
  subs: [{ id: 7, uuid: '11111111-1111-4111-8111-111111111111', url: 'https://ext.example.com/sub/abc', remark: '机场A', enabled: 1 }],
  nodeTpls: [],
  /* 远程终端：最近一次 exec 的命令，任务列表里回放成「已完成」任务 */
  lastExec: '',
};

const calls = [];
let seq = 100;

function json(body, status = 200) {
  return { status, body };
}

/** 路由：返回 { status, body } */
function handle(method, url, body) {
  const u = new URL(url, 'https://x.local');
  const p = u.pathname.replace(/^\/api\//, '');
  const q = u.searchParams;

  // ---- 登录 ----
  if (p === 'login' && method === 'POST') {
    if (body && body.step === 'verify_totp') return json({ token: 'tok-totp', requires_mfa: true });
    if (body && body.username === 'needs-mfa') return json({ requires_mfa: true, step: 'totp_required' });
    if (!body || !body.username || !body.password) return json({ error: '用户名或密码错误' }, 401);
    return json({ token: 'tok-login', requires_mfa: false });
  }
  if (p === 'login/email-code' && method === 'POST') return json({ success: true, message: '已发送' });
  if (p === 'login/email-code/verify' && method === 'POST') {
    if (body && body.code === '123456') return json({ token: 'tok-email', requires_mfa: false });
    return json({ error: '验证码不正确' }, 401);
  }

  // ---- 统计 ----
  if (p === 'stats') {
    const days = Number(q.get('days')) || 30;
    const daily = [];
    for (let i = days - 1; i >= 0; i--) {
      const d = new Date(NOW - i * 86400000).toISOString().slice(0, 10);
      daily.push({ date: d, total: 40 + ((i * 7) % 60), subscription: 30 + ((i * 5) % 40), api_generation: 4 + (i % 9), unique_uuids: 3 });
    }
    return json({
      uuids: state.uuids.length, templates: state.templates.length,
      access_stats: {
        success: true, total_requests: 24318, unique_uuids: 137, subscription_requests: 20114, api_generation_requests: 4204,
        today_total: 612, today_subscription: 501, today_apigen: 111, daily_stats: daily,
        popular_uuids: [{ uuid: state.uuids[0].uuid, access_count: 120, owner: 'alice' }],
        latest_logs: [{ uuid: state.uuids[0].uuid, query_type: 'subscription', created_at: NOW - 1000, owner: 'alice' }],
      },
    });
  }

  // ---- 订阅分组 ----
  if (p === 'uuids') {
    if (method === 'GET') {
      const key = (q.get('q') || '').toLowerCase();
      const st = q.get('status') || '';
      let rows = state.uuids.filter((r) => (!key || (r.uuid + r.owner + r.email + r.remark).toLowerCase().includes(key)) && (!st || r.status === st));
      return json({ success: true, total: rows.length, page: 1, size: 10, data: rows });
    }
    if (method === 'POST' || method === 'PUT') {
      const exist = state.uuids.find((r) => r.uuid === body.uuid);
      if (exist) { Object.assign(exist, body, { updated_at: NOW }); return json({ success: true, created: false }); }
      state.uuids.push({ ...body, access_count: 0, last_accessed: 0, created_at: NOW, updated_at: NOW, count: 0 });
      return json({ success: true, created: true });
    }
    if (method === 'DELETE') {
      const before = state.uuids.length;
      state.uuids = state.uuids.filter((r) => r.uuid !== body.uuid);
      return json({ success: true, deleted_configs: before - state.uuids.length, deleted_info: 1, nodes_updated: 1, nodes_deployed: 1 });
    }
  }
  if (p === 'uuid-configs' && method === 'GET') {
    return json({ success: true, total: 2, data: [
      { id: 1, uuid: q.get('uuid'), protocol: 'vless', config_data: 'vless://abc@1.2.3.4:443?type=ws#hk', remark: 'hk' },
      { id: 2, uuid: q.get('uuid'), protocol: 'hy2', config_data: 'hysteria2://xyz@1.2.3.5:443#jp', remark: 'jp' },
    ] });
  }

  // ---- 模板 ----
  if (p === 'templates/manage') {
    if (method === 'GET') return json({ success: true, total: state.templates.length, page: 1, size: 10, data: state.templates });
    if (method === 'POST') {
      state.templates.push({ id: ++seq, name: body.name, url: body.url, description: body.description || '', is_default: 0, created_at: NOW });
      return json({ success: true, id: seq });
    }
    if (method === 'PUT') {
      const t = state.templates.find((x) => x.id === body.id);
      if (t) Object.assign(t, body);
      return json({ success: true });
    }
    if (method === 'DELETE') {
      state.templates = state.templates.filter((x) => x.id !== body.id);
      return json({ success: true });
    }
  }
  if (p === 'templates/manage/set-default' && method === 'POST') {
    state.templates.forEach((t) => { t.is_default = t.id === body.id ? 1 : 0; });
    return json({ success: true });
  }

  // ---- 服务器 ----
  if (p === 'servers') {
    if (method === 'GET') return json({ success: true, total: state.servers.length, data: state.servers });
    if (method === 'POST') {
      const rec = { id: ++seq, name: body.name, last_status: 'unknown', agent_next_poll_sec: 30, tags: body.tags || '' };
      state.servers.push(rec);
      return json({
        success: true, data: rec, agent_token: 'ag_TESTTOKEN123',
        install_command: 'curl -fsSL https://x.local/install.sh | bash -s -- --token ag_TESTTOKEN123',
        message: '请把 install_command 复制到服务器执行',
      });
    }
  }
  if (/^servers\/\d+$/.test(p) && method === 'DELETE') {
    const id = Number(p.split('/')[1]);
    state.servers = state.servers.filter((s) => s.id !== id);
    return json({ success: true });
  }
  if (/^servers\/\d+\/agent-key$/.test(p)) {
    return json({ ok: true, viewable: true, agent_token: 'ag_VIEWABLE', install_command: 'curl -fsSL https://x.local/install.sh | bash -s -- --token ag_VIEWABLE', token_hint: 'pk_…ab12', note: '本次查看已记入审计日志' });
  }
  if (/^servers\/\d+\/agent-token$/.test(p) && method === 'POST') {
    return json({ success: true, agent_token: 'ag_ROTATED', install_command: 'curl ... --token ag_ROTATED', message: '旧令牌已失效' });
  }
  if (/^servers\/\d+\/tasks$/.test(p)) {
    const data = [{ id: 1, type: 'deploy', status: 'success', created_at: NOW - 60000, message: 'ok' }];
    if (state.lastExec) {
      data.unshift({
        id: 9001, type: 'exec', status: 'done', exit_code: 0, created_at: NOW,
        output: `echo:${state.lastExec}\n__PP_CWD__/root\n__PP_RC__0`,
      });
    }
    return json({ success: true, data });
  }
  if (p === 'servers/monitor') {
    // HK 有精确经纬度；JP 只给国家码 → 走国家中心点兜底
    const GEO = { HK: [22.32, 114.17] };
    const s = state.servers.map((x, i) => {
      const geo = GEO[x.ip_country] || [null, null];
      return {
      id: x.id, name: x.name, status: x.last_status, hostname: x.name, last_ip: x.last_ip, ip_country: x.ip_country, ip_region: x.ip_region,
      last_ip4: x.last_ip4 || '', last_ip6: x.last_ip6 || '',
      ip_lat: geo[0], ip_lon: geo[1],
      cpu_percent: 30 + i * 20, cores: 4, mem_percent: 50 + i * 10, mem_used_mb: 900, mem_total_mb: 2000, disk_percent: 40,
      uptime_sec: 3600 * 24, load1: 0.4, load5: 0.5, load15: 0.6, last_seen: x.agent_last_seen, agent_version: x.agent_version,
      services: { singbox: i === 1 ? 'failed' : 'active' },
      net: { rxBytes: 1024 * 1024 * 350, txBytes: 1024 * 1024 * 42 },
      disk_io: { readBytes: 1024 * 1024 * 12, writeBytes: 1024 * 1024 * 80 },
      conn: { total: 120 + i },
      proc: { total: 180 + i },
      };
    });
    return json({
      success: true, servers: s, watch: ['singbox'],
      matrix: [{ service: 'singbox', states: s.map((x) => ({ server_id: x.id, state: x.status === 'offline' ? 'offline' : 'active' })) }],
      summary: { servers: s.length, online: s.filter((x) => x.status === 'online').length, offline: s.filter((x) => x.status === 'offline').length, failed_services: 1, failed_list: [{ server_id: 2, server_name: 'jp-02', service: 'singbox', state: 'failed' }], not_installed: 0 },
    });
  }

  // ---- 节点 ----
  if (p === 'singbox/templates' && method === 'GET') return json({ success: true, data: state.singboxTpls });
  if (p === 'singbox/nodes' && method === 'GET') return json({ success: true, data: state.singboxNodes });
  if (p === 'singbox/uuids') return json({ success: true, data: state.uuids.map((x) => ({ uuid: x.uuid, node_count: x.count, owner: x.owner })) });
  if (p === 'singbox/apply-all') return json({ success: true, ok_count: 1, total: 1, data: [{ id: 1, name: 'hk-node', ok: true }] });
  if (p === 'singbox/deploy-all') return json({ success: true, ok_count: 1, total: 1 });

  // ---- 后台任务 ----
  if (p === 'jobs' && method === 'GET') {
    const list = state.jobs || [];
    return json({ success: true, data: list, active: list.filter((j) => ['queued', 'running'].includes(j.status)).length });
  }
  if (/^jobs\/\d+$/.test(p) && method === 'GET') {
    const id = Number(p.split('/')[1]);
    const job = (state.jobs || []).find((j) => Number(j.id) === id) || null;
    return json({ success: true, data: job, items: state.jobItems || [] });
  }

  // ---- MTProto ----
  if (p === 'mtproto/nodes' && method === 'GET') return json({ success: true, data: [] });
  if (p === 'portal/mtproto' && method === 'GET') return json({ success: true, data: [] });
  if (p === 'mtproto/templates' && method === 'GET') return json({ success: true, data: [] });
  if (p === 'mtproto/uuids') return json({ success: true, data: [] });

  // ---- 日志 ----
  if (p === 'logs/summary') return json({ success: true, ...state.logs });
  if (p === 'logs/cleanup' && method === 'POST') {
    if (body.password !== 'secret123') return json({ error: '密码错误' }, 401);
    return json({ success: true, deleted: 800, remaining: state.logs.total - 800, batches: 2, capped: false, cutoff: '2026-01-01', would_delete: 800 });
  }
  if (p === 'node-configs/logs') return json({ success: true, data: [{ id: 9, action: 'singbox-apply', status: 'success', server_name: 'hk-01', message: 'ok', created_at: NOW - 1000 }] });

  // ---- 系统 / 密钥 ----
  if (p === 'admin/users') {
    if (method === 'GET') return json({ users: state.users, self: 'admin' });
    if (method === 'POST') { state.users.push({ id: ++seq, username: body.username, email: body.email || '', email_verified: 0, mfa_enabled: 0, created_at: NOW }); return json({ success: true }); }
    if (method === 'PUT') { const u = state.users.find((x) => x.username === body.username); if (u && body.newPassword) u.pwReset = true; return json({ success: true }); }
    if (method === 'DELETE') { state.users = state.users.filter((x) => x.username !== body.username); return json({ success: true }); }
  }
  if (p === 'apikeys') {
    if (method === 'GET') return json({ success: true, total: state.apikeys.length, data: state.apikeys });
    if (method === 'POST') {
      if (!body.key_name) return json({ error: '请填写密钥名称' }, 400);
      if (!body.permissions || !body.permissions.length) return json({ error: '请至少选择一项权限' }, 400);
      state.apikeys.push({ id: ++seq, key_name: body.key_name, key_hint: 'pk_new…', enabled: 1, permissions: body.permissions.join(','), permissions_text: body.permissions.join(','), expires_at: 0, usage_count: 0 });
      return json({ success: true, id: seq, key_name: body.key_name, api_key: 'pk_LIVE_' + seq + '_SECRET', permissions: body.permissions });
    }
  }
  if (/^apikeys\/\d+\/rotate$/.test(p) && method === 'POST') return json({ success: true, id: 1, api_key: 'pk_ROTATED_SECRET' });
  if (/^apikeys\/\d+$/.test(p)) return json({ success: true });

  // ---- MFA ----
  if (p === 'mfa/status') return json({ mfa_enabled: state.mfa.enabled, last_mfa_login: state.mfa.lastLogin, backup_codes_remaining: state.mfa.backupRemaining });
  if (p === 'mfa/init' && method === 'POST') {
    if (body.password !== 'secret123') return json({ error: '密码错误' }, 401);
    return json({ success: true, secret: 'JBSWY3DPEHPK3PXP', otpauth_url: 'otpauth://totp/ProxyPilot:admin?secret=JBSWY3DPEHPK3PXP&issuer=ProxyPilot', qr_url: 'https://qrcode.example.com/qr' });
  }
  if (p === 'mfa/verify-first' && method === 'POST') {
    if (String(body.totp_code) !== '123456') return json({ error: '验证码无效' }, 400);
    state.mfa = { enabled: true, backupRemaining: 10, lastLogin: NOW };
    return json({ success: true, backup_codes: ['AAAABBBB', 'CCCCDDDD', 'EEEEFFFF'] });
  }
  if (p === 'mfa/disable' && method === 'POST') { state.mfa.enabled = false; return json({ success: true }); }
  if (p === 'mfa/backup-codes/regenerate' && method === 'POST') return json({ success: true, backup_codes: ['ZZZZ1111'] });
  if (p === 'change-password' && method === 'POST') {
    if (body.oldPassword !== 'secret123') return json({ error: '旧密码错误' }, 403);
    return json({ success: true });
  }

  // ---- 对象存储 ----
  if (p === 'bucket/files') return json({ success: true, prefix: 'rule', root: 'rule', files: state.bucket, truncated: false });
  if (p === 'bucket/file' && method === 'GET') return json({ success: true, key: q.get('key'), size: 12, content: 'rules:\n  - DOMAIN,example.com\n' });
  if (p === 'bucket/file' && (method === 'PUT' || method === 'POST')) { state.bucket.push({ key: body.key, size: body.content.length }); return json({ success: true, key: body.key }); }

  // ---- singbox 模板 / 节点（写操作）----
  if (p === 'singbox/templates' && method === 'POST') {
    const rec = { id: ++seq, name: body.name, remark: body.remark || '', protocols: ['hy2'], port_base: body.port_base, user_count: 1, warp_enabled: 0, enabled: 1, updated_at: NOW };
    state.singboxTpls.push(rec);
    return json({ success: true, data: rec });
  }
  if (p === 'singbox/templates/from-config' && method === 'POST') {
    if (!body.sb_json && !body.xray_json) return json({ error: '至少要启用 sing-box 或 Xray，并提供对应配置文件' }, 400);
    state.singboxTpls.push({ id: ++seq, name: body.name, remark: '', protocols: ['hy2', 'ss', 'reality'], port_base: 13700, user_count: 2, warp_enabled: 0, enabled: 1, updated_at: NOW });
    return json({ success: true, data: { id: seq } });
  }
  if (p === 'singbox/templates/preview' && method === 'POST') {
    const sbJson = JSON.stringify(
      {
        log: { level: 'warn' },
        inbounds: [
          { type: 'hysteria2', tag: 'hy2-sb', listen: '::', listen_port: 13701, tls: { enabled: true, alpn: ['h3'] }, users: [{ name: 'u1', password: 'x' }] },
          { type: 'shadowsocks', tag: 'ss2022-sb', listen: '::', listen_port: 13702, method: '2022-blake3-aes-256-gcm', password: 'k', users: [{ name: 'u1', password: 'p' }, { name: 'u2', password: 'q' }] },
        ],
      },
      null,
      2
    );
    return json({ success: true, ports: { hy2: 13701, ss: 13702 }, users: 2, files: [{ name: 'sb.json', content: sbJson }, { name: 'Caddyfile', content: ':443 {' }] });
  }
  if (/^singbox\/templates\/\d+$/.test(p)) {
    const id = Number(p.split('/').pop());
    const t = state.singboxTpls.find((x) => x.id === id);
    if (!t) return json({ error: '模板不存在' }, 404);
    if (method === 'GET') return json({ success: true, data: Object.assign({}, t, { ss_server_key: 'K'.repeat(43), reality_private_key: 'R'.repeat(43), users_json: '[{"name":"u1","uuid":"11111111-1111-4111-8111-111111111111"}]' }) });
    if (method === 'PUT') { Object.assign(t, body, { updated_at: NOW }); return json({ success: true, data: t }); }
    if (method === 'DELETE') { state.singboxTpls = state.singboxTpls.filter((x) => x.id !== id); return json({ success: true }); }
  }
  if (p === 'singbox/nodes' && method === 'POST') {
    const rec = {
      id: ++seq, name: body.name, server_id: body.server_id, server_name: 'hk-01', server_host: '1.1.1.1',
      template_id: body.template_id, template_name: 'sb-main', domain: body.domain || '', uuid_list: [],
      enabled: 1, last_apply_at: 0, warp_enabled: body.warp_enabled, warp_effective: 0, remark: body.remark || '',
    };
    state.singboxNodes.push(rec);
    return json({ success: true, data: rec, deployed: body.auto_deploy ? { ok: true, changed: 3 } : null });
  }
  if (/^singbox\/nodes\/\d+\/apply$/.test(p) && method === 'POST') return json({ success: true, changed: 2 });
  if (/^singbox\/nodes\/\d+$/.test(p)) {
    const id = Number(p.split('/').pop());
    const n = state.singboxNodes.find((x) => x.id === id);
    if (method === 'PUT') { if (n) Object.assign(n, body); return json({ success: true, data: n }); }
    if (method === 'DELETE') { state.singboxNodes = state.singboxNodes.filter((x) => x.id !== id); return json({ success: true, removed_node_configs: 1 }); }
  }
  if (p === 'monitor/uuid-check' && method === 'POST') return json({ success: true, checked: 12, fixed: 2 });

  // ---- 分组配置写操作 ----
  if (p === 'uuid-configs' && method === 'POST') {
    const lines = String(body.config_data || '').split('\n').filter(Boolean);
    lines.forEach((line) => state.configs.push({ id: ++seq, uuid: body.uuid, protocol: 'vless', config_data: line, remark: '', updated_at: NOW }));
    return json({ success: true, added: lines.length });
  }
  if (p === 'uuid-configs' && method === 'PUT') {
    const c = state.configs.find((x) => x.id === body.id);
    if (c) { c.config_data = body.config_data; c.updated_at = NOW; }
    return json({ success: true });
  }
  if (p === 'uuid-configs' && method === 'DELETE') {
    if (Array.isArray(body.ids)) {
      const before = state.configs.length;
      state.configs = state.configs.filter((c) => body.ids.indexOf(c.id) < 0);
      return json({ success: true, deleted: before - state.configs.length });
    }
    if (body.uuid) {
      const before = state.configs.length;
      state.configs = state.configs.filter((c) => c.uuid !== body.uuid);
      return json({ success: true, deleted: before - state.configs.length, nodes_revoked: 1, nodes_deployed: 1 });
    }
    state.configs = state.configs.filter((c) => c.id !== body.id);
    return json({ success: true, deleted: 1 });
  }

  // ---- 授权节点 ----
  if (/^uuids\/[^/]+\/nodes$/.test(p)) {
    const target = decodeURIComponent(p.split('/')[1]);
    if (method === 'GET') {
      return json({
        success: true, uuid: target,
        data: state.singboxNodes.map((n) => ({
          id: n.id, name: n.name, server_name: n.server_name, template_name: n.template_name,
          users: (n.uuid_list || []).length, authorized: (n.uuid_list || []).indexOf(target) >= 0,
        })),
      });
    }
    if (method === 'PUT') {
      const want = new Set(body.node_ids || []);
      let changed = 0;
      state.singboxNodes.forEach((n) => {
        const has = (n.uuid_list || []).indexOf(target) >= 0;
        if (has !== want.has(n.id)) {
          n.uuid_list = want.has(n.id)
            ? (n.uuid_list || []).concat([target])
            : (n.uuid_list || []).filter((u) => u !== target);
          changed++;
        }
      });
      return json({ success: true, uuid: target, changed, results: [] });
    }
  }

  // ---- 外部订阅源（不在 /api 下）----
  if (u.pathname === '/manage/uuid-sub-sources') {
    if (method === 'GET') return json({ success: true, total: state.subs.length, data: state.subs });
    if (method === 'POST') {
      if (!/^https?:\/\//i.test(String(body.url || ''))) return json({ error: '外部订阅URL无效（仅支持 http/https 公网地址）' }, 400);
      state.subs.push({ id: ++seq, uuid: body.uuid, url: body.url, remark: body.remark || '', enabled: 1, created_at: NOW });
      return json({ success: true, message: '外部订阅源添加成功' });
    }
    if (method === 'PUT') { const s = state.subs.find((x) => x.id === body.id); if (s) Object.assign(s, body); return json({ success: true }); }
    if (method === 'DELETE') { state.subs = state.subs.filter((x) => x.id !== body.id); return json({ success: true }); }
  }

  // ---- 服务器编辑 ----
  if (/^servers\/\d+$/.test(p) && method === 'PUT') {
    const id = Number(p.split('/')[1]);
    const s = state.servers.find((x) => x.id === id);
    if (s) Object.assign(s, body);
    return json({ success: true, data: s });
  }

  // ---- 服务器详情 / 任务 / 通知 / 邮箱 / 批量推送 ----
  if (/^servers\/\d+$/.test(p) && method === 'GET') {
    const id = Number(p.split('/')[1]);
    const s = state.servers.find((x) => x.id === id) || state.servers[0];
    return json({
      success: true,
      data: {
        ...s, id, tags: '香港', remark: '测试机', enabled: true, allow_exec: false,
        last_metrics: {
          hostname: s.name, kernel: '6.1.0', os: 'Debian 12', uptimeSec: 86400 * 3, cores: 4,
          cpuPercent: 34, memPercent: 56, diskPercent: 41, memUsedMb: 900, memTotalMb: 1600,
          load1: 0.4, load5: 0.5, load15: 0.6,
          services: { 'sing-box': 'active', caddy: 'failed', sshd: 'active' },
        },
      },
      metrics_recent: [0, 1, 2, 3, 4, 5].map((i) => ({
        status: 'online', latency_ms: 30 + i, cpu_percent: 20 + i * 3, mem_percent: 50 + i, disk_percent: 41, load1: 0.4, created_at: NOW - (5 - i) * 60000,
      })),
      node_configs: [{ id: 1, name: 'sb-main', engine: 'sing-box', target_path: '/etc/sing-box/sb.json', service_name: 'sing-box', enabled: 1, last_push_status: 'success' }],
    });
  }
  if (p === 'agent/tasks' && method === 'POST') return json({ success: true, id: 1 });
  if (p === 'agent/latest') return json({ success: true, latest: '1.2.8', base: 'https://x.local/agent' });
  if (p === 'shell/status') return json({ durable: true, agent_min: '1.3.0' });
  if (/^servers\/\d+\/exec$/.test(p) && method === 'POST') {
    state.lastExec = String(body.command || '');
    return json({
      success: true, mode: 'agent', queued: true, task_id: 9001,
      message: '命令已排队；agent 下次轮询执行后可在「节点配置下发 → 任务队列」查看输出',
    });
  }
  if (p === 'agent/push-update' && method === 'POST') {
    return json({
      success: true,
      latest: '1.2.8',
      queued: 1,
      results: [
        { server_id: 1, name: 'hk-01', ok: true, task_id: 11 },
        { server_id: 2, name: 'jp-02', ok: true, skipped: true, reason: '已是 1.2.8' },
      ],
    });
  }
  if (p === 'monitor/notify' && method === 'GET') return json({ success: true, enabled: false, emails: '', cooldown_min: 10, email_ready: true });
  if (p === 'monitor/notify' && method === 'PUT') {
    state.notify = { enabled: !!body.enabled, emails: body.emails, cooldown_min: body.cooldown_min };
    return json({ success: true, ...state.notify });
  }
  if (p === 'monitor/notify/test' && method === 'POST') return json({ success: true, to: 'ops@example.com' });
  if (p === 'admin/users/email/code' && method === 'POST') return json({ success: true, email: body.email, expires_in: 600 });
  if (p === 'admin/users/email/verify' && method === 'POST') return json({ success: true, username: body.username, email: body.email, email_verified: true });
  if (p === 'admin/users/email/unbind' && method === 'POST') return json({ success: true });
  if (p === 'singbox/push' && method === 'POST') {
    return json({ success: true, ok_count: body.server_ids.length, total: body.server_ids.length, data: body.server_ids.map((id) => ({ id, ok: true })) });
  }

  // ---- 下发模板 / 检测 / 备份码 ----
  if (p === 'node-templates' && method === 'GET') {
    return json({
      success: true, total: 1,
      data: [{ id: 1, name: 'sb-config', engine: 'sing-box', target_path: '/etc/sing-box/config.json', service_name: 'sing-box', used_by: 0, enabled: 1, content: '', remark: '' }],
      engines: ['xray', 'sing-box', 'clash', 'mihomo', 'other'],
      placeholders: ['SERVER_ID', 'SERVER_NAME', 'SOCKS_PORT'],
      group_placeholders: ['UUID', 'GROUP_NAME'],
    });
  }
  if (p === 'node-templates' && method === 'POST') {
    state.nodeTpls.push({ id: ++seq, name: body.name, target_path: body.target_path });
    return json({ success: true, data: { id: seq } });
  }
  if (/^node-templates\/\d+$/.test(p)) return json({ success: true });
  if (/^servers\/\d+\/test$/.test(p) && method === 'POST') return json({ success: true, durationMs: 12 });
  if (p === 'mfa/login-with-backup' && method === 'POST') {
    if (String(body.backup_code || '').toUpperCase() !== 'A1B2C3D4') return json({ error: '备份码无效' }, 401);
    return json({ token: 'tok-backup' });
  }

  // ---- 用户中心（portal）----
  if (p === 'portal/send-code' && method === 'POST') return json({ success: true, email: body.email });
  if (p === 'portal/login' && method === 'POST') {
    if (String(body.code) !== '123456') return json({ error: '验证码不正确' }, 401);
    return json({ token: 'portal-tok', email: body.email, expires_in: 3600 });
  }
  if (p === 'portal/email/send-code' && method === 'POST') return json({ success: true, email: body.email });
  if (p === 'portal/email/verify' && method === 'POST') {
    if (String(body.code) !== '123456') return json({ error: '验证码不正确' }, 401);
    return json({ success: true, email: body.email, token: 'portal-tok-2' });
  }
  if (p === 'portal/uuids') {
    return json({
      success: true,
      email: 'me@example.com',
      total: 2,
      data: [
        {
          uuid: '11111111-1111-4111-8111-111111111111',
          owner: 'alice', remark: '主力节点', status: 'active', configs: 12,
          created_at: NOW - 86400000 * 30, updated_at: NOW - 3600000,
          sub: {
            raw: 'https://x.local/sub/11111111-1111-4111-8111-111111111111',
            batch: 'https://x.local/batch-configs/11111111-1111-4111-8111-111111111111',
            clash: 'https://x.local/clash-config?uuid=11111111-1111-4111-8111-111111111111',
          },
        },
        {
          uuid: '22222222-2222-4222-8222-222222222222',
          owner: 'alice', remark: '', status: 'suspended', configs: 3,
          created_at: NOW - 86400000 * 8, updated_at: NOW - 86400000,
          sub: { raw: 'https://x.local/sub/22222222-2222-4222-8222-222222222222' },
        },
      ],
    });
  }

  // ---- 用户中心 · 单个 UUID 管理（/manage/*）----
  if (p === '/manage/configs' && method === 'POST') return json({ success: true });
  if (p === '/manage/configs/batch' && method === 'DELETE') {
    return json({ success: true, deleted: (body && body.ids && body.ids.length) || 0 });
  }
  if (p === '/manage/configs' && method === 'PUT') return json({ success: true });
  if (/^\/manage\/configs\/id\/\d+$/.test(p) && method === 'DELETE') return json({ success: true });
  if (/^\/manage\/configs\/[^/]+$/.test(p) && method === 'DELETE') return json({ success: true });
  if (/^\/manage\/configs\/[^/]+$/.test(p) && method === 'GET') {
    return json({
      uuid: decodeURIComponent(p.split('/').pop()),
      configs: [
        { id: 1, protocol: 'vless', remark: '香港 01', config_data: 'vless://aaaa@hk1.example.com:443?type=ws#HK-01' },
        { id: 2, protocol: 'trojan', remark: '', config_data: 'trojan://bbbb@jp1.example.com:443?security=tls&sni=jp1.example.com&type=ws&path=%2Fws&host=jp1.example.com#JP-01-long-remark' },
      ],
      pagination: { page: 1, pageSize: 10, total: 2, totalPages: 1 },
      filters: { protocol: 'all', q: '' },
    });
  }
  if (/^\/manage\/stats\/[^/]+$/.test(p)) {
    return json({
      success: true,
      total_access: 128, subscription_count: 96, apigen_count: 32,
      today_total: 7, today_subscription: 5, today_apigen: 2,
      daily_stats: [
        { date: '2026-10-20', total: 3 }, { date: '2026-10-21', total: 9 },
        { date: '2026-10-22', total: 4 }, { date: '2026-10-23', total: 12 },
      ],
      recent_logs: [{ id: 1, created_at: NOW - 60000, query_type: 'subscription', ip_address: '203.0.113.7' }],
    });
  }
  if (/^\/manage\/uuid-info\/[^/]+$/.test(p) && method === 'GET') {
    // 与真实后端一致：信息包在 data 里（{ success, exists, data }）
    return json({
      success: true,
      exists: true,
      data: { uuid: p.split('/').pop(), owner: 'alice', email: 'me@example.com', contact_info: 'tg:@alice', remark: '主力节点' },
    });
  }
  if (p === '/manage/uuid-info' && method === 'POST') return json({ success: true });
  if (p === 'templates' && method === 'GET') {
    return json([{ name: '内置默认', is_default: 1 }, { name: '高倍率', is_default: 0 }]);
  }

  /* 订阅输出是三张纯文本响应：/sub/:uuid、/clash-config?uuid=&template=、/batch-configs/:uuid */
  if (/^\/sub\/[^/]+$/.test(p)) {
    return { status: 200, raw: true, body: 'dmxlc3M6Ly9hYmNAaGsubGFiOmV4YW1wbGUuY29tOjQ0MyNISy0wMQ==' };
  }
  if (p === '/clash-config') {
    return {
      status: 200,
      raw: true,
      body: `proxies:\n  - name: HK-01\n    type: vless\n    server: hk.example.com\n    port: 443\n    uuid: ${q.get('uuid') || ''}\n`,
    };
  }
  if (/^\/batch-configs\/[^/]+$/.test(p)) {
    return { status: 200, raw: true, body: 'vless://aaa@hk.example.com:443#HK-01\ntrojan://bbb@jp.example.com:443#JP-01\n' };
  }

  return json({ error: 'mock 未实现：' + method + ' /api/' + p }, 404);
}

/* ================= jsdom 驱动 ================= */

const fakeJwt = [
  Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url'),
  Buffer.from(JSON.stringify({ sub: 'admin', exp: Math.floor(NOW / 1000) + 86400 })).toString('base64url'),
  'sig',
].join('.');

class ShimResponse {
  constructor(body, init) {
    this._body = String(body);
    this.status = (init && init.status) || 200;
    this.ok = this.status >= 200 && this.status < 300;
    this.headers = { get: () => 'application/json' };
  }
  text() { return Promise.resolve(this._body); }
  json() { return Promise.resolve(JSON.parse(this._body)); }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitFor(fn, ms = 2500) {
  const t0 = Date.now();
  for (;;) {
    try {
      const v = fn();
      if (v) return v;
    } catch (e) { /* keep polling */ }
    if (Date.now() - t0 > ms) return null;
    await sleep(30);
  }
}

let pass = 0;
let fail = 0;
function check(name, ok, detail) {
  if (ok) { pass++; console.log('  \u2705', name); }
  else { fail++; console.log('  \u274c', name, detail ? '\n     ' + String(detail).slice(0, 500) : ''); }
}

async function mount({ token, hash = '#dash', path: urlPath = '/admin', mode = 'admin', portalTok = '', pc = false } = {}) {
  calls.length = 0;
  const vc = new VirtualConsole();
  vc.on('jsdomError', () => {});
  vc.on('error', (...a) => {
    if (process.env.PP_DEBUG) console.error('[jsdom]', ...a);
  });
  const dom = new JSDOM(
    `<!DOCTYPE html><html><head><meta charset="utf-8"><style>${appCss}</style></head><body><div id="root"></div></body></html>`,
    { url: 'https://e2e.local' + urlPath + hash, runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: vc }
  );
  const { window } = dom;
  window.Response = ShimResponse;
  // pc:true → 让 useIsPc() 判定为 PC 布局；默认按移动端（竖屏窄屏）处理
  window.matchMedia = (q) => ({ matches: !!pc, media: q, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false });
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
  /* 实时终端：jsdom 没有 WebSocket，用一个可观测的假实现驱动 ready / 输出 */
  window.__PP_WS = [];
  window.WebSocket = class {
    constructor(url) {
      this.url = String(url);
      this.readyState = 0;
      this.binaryType = 'arraybuffer';
      this.sent = [];
      window.__PP_WS.push(this);
      setTimeout(() => {
        this.readyState = 1;
        if (this.onopen) this.onopen({});
        setTimeout(() => {
          if (this.onmessage) this.onmessage({ data: JSON.stringify({ type: 'ready' }) });
          setTimeout(() => {
            const bytes = new TextEncoder().encode('root@hk-01:~# ');
            if (this.onmessage) this.onmessage({ data: bytes.buffer });
          }, 20);
        }, 30);
      }, 20);
    }
    send(d) { this.sent.push(d); }
    close() {
      this.readyState = 3;
      if (this.onclose) this.onclose({ code: 1000 });
    }
  };
  Object.defineProperty(window.HTMLElement.prototype, 'clientWidth', { configurable: true, get() { return 820; } });
  window.HTMLElement.prototype.getBoundingClientRect = function () {
    return { width: 820, height: 400, top: 0, left: 0, right: 820, bottom: 400, x: 0, y: 0, toJSON() {} };
  };
  window.scrollTo = () => {};

  const mem = new Map();
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k) => (mem.has(k) ? mem.get(k) : null),
      setItem: (k, v) => mem.set(k, String(v)),
      removeItem: (k) => mem.delete(k),
      clear: () => mem.clear(),
      key: (i) => [...mem.keys()][i] || null,
      get length() { return mem.size; },
    },
  });

  window.fetch = (input, init) => {
    const url = String(input && input.url ? input.url : input);
    const method = ((init && init.method) || 'GET').toUpperCase();
    let body = null;
    if (init && init.body) { try { body = JSON.parse(init.body); } catch (e) { body = init.body; } }
    calls.push({ method, url, body });
    const res = handle(method, url, body);
    // raw:true 的响应是纯文本（订阅输出），不要再 JSON.stringify 包一层
    return Promise.resolve(new ShimResponse(res.raw ? String(res.body) : JSON.stringify(res.body), { status: res.status }));
  };

  window.__PP_BOOT__ =
    mode === 'portal'
      ? { mode: 'portal', version: 'V13.2', builtAt: 'e2e', turnstile: '' }
      : {
          mode: 'admin',
          superAdmin: 'admin', version: 'V13.2', builtAt: 'e2e', flavor: 'antd',
          turnstile: '', emailLogin: true, oidc: { enabled: false, buttonText: 'SSO', passwordDisabled: false },
        };
  if (token) mem.set('token', token);
  if (portalTok) mem.set('pp_portal_token', portalTok);

  const s = window.document.createElement('script');
  s.textContent = appJs;
  window.document.body.appendChild(s);
  await sleep(420);
  return { window, doc: window.document };
}

/* --- DOM 操作helpers --- */
function visibleText(doc) {
  const c = doc.body.cloneNode(true);
  c.querySelectorAll('script, style').forEach((n) => n.remove());
  return (c.textContent || '').replace(/\s+/g, ' ');
}
function btn(doc, text, root = doc) {
  const list = [...root.querySelectorAll('button, .ant-btn, .ant-tabs-tab, a.ant-btn')];
  const want = String(text).replace(/\s+/g, '');
  // 操作列已改为「只有图标 + aria-label」，所以文字匹配不上时再比一次 aria-label / title
  return list.find((b) => {
    const label = (b.textContent || '').replace(/\s+/g, '');
    if (label.includes(want)) return true;
    const aria = String(b.getAttribute('aria-label') || '').replace(/\s+/g, '');
    if (aria && aria.includes(want)) return true;
    const title = String(b.getAttribute('title') || '').replace(/\s+/g, '');
    return !!title && title.includes(want);
  });
}
function click(el) {
  if (!el) return false;
  // 用原生 click()：能正确翻转 checkbox，也能让 React 的合成事件正常触发
  if (typeof el.click === 'function') el.click();
  else el.dispatchEvent(new el.ownerDocument.defaultView.MouseEvent('click', { bubbles: true, cancelable: true }));
  return true;
}
function setValue(window, input, value) {
  if (!input) return false; // 让断言去报错，而不是整条用例崩掉
  const proto = input.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
  setter.call(input, String(value));
  input.dispatchEvent(new window.Event('input', { bubbles: true }));
  return true;
}
/** antd 表格会插一行隐藏的 measure-row，交互必须挑真正的数据行 */
function dataRows(doc) {
  return [...doc.querySelectorAll('.ant-table-tbody tr.ant-table-row')];
}
function inputByPlaceholder(doc, text) {
  return [...doc.querySelectorAll('input, textarea')].find((i) => (i.getAttribute('placeholder') || '').includes(text));
}
function inputByLabel(doc, label) {
  const items = [...doc.querySelectorAll('.ant-form-item')];
  const hit = items.find((it) => (it.querySelector('label') || {}).textContent?.includes(label));
  return hit ? hit.querySelector('input, textarea') : null;
}
function modalOk(doc) {
  // Modal.confirm / Popconfirm 用的是 confirm-btns，普通 Modal 用 modal-footer
  const confirmBtns = [...doc.querySelectorAll('.ant-modal-confirm-btns button')];
  if (confirmBtns.length) {
    return confirmBtns.find((b) => b.className.includes('ant-btn-primary')) || confirmBtns[confirmBtns.length - 1];
  }
  const footers = [...doc.querySelectorAll('.ant-modal-footer')];
  const last = footers[footers.length - 1];
  if (!last) return null;
  const btns = [...last.querySelectorAll('button')];
  return btns.find((b) => b.className.includes('ant-btn-primary')) || btns[btns.length - 1];
}
function popConfirmOk(doc) {
  const boxes = [
    ...doc.querySelectorAll('.ant-popconfirm-buttons'),
    ...doc.querySelectorAll('.ant-popover .ant-popover-buttons'),
    ...doc.querySelectorAll('.ant-popover .ant-btn-primary'),
  ];
  const last = boxes[boxes.length - 1];
  if (!last) return null;
  return last.tagName === 'BUTTON' ? last : last.querySelector('.ant-btn-primary') || last.querySelector('button');
}
const lastCall = (re, method) => [...calls].reverse().find((c) => re.test(c.url) && (!method || c.method === method));

/* ================= 场景 ================= */

console.log('\nProxyPilot · Ant Design 前端 E2E（内存后端 + jsdom 交互）\n');

/* S1 登录（账号密码） */
{
  const { window, doc } = await mount({ token: '' });
  const user = inputByPlaceholder(doc, '用户名');
  const pass = inputByPlaceholder(doc, '密码');
  check('S1 登录页出现用户名/密码框', !!user && !!pass);
  setValue(window, user, 'admin');
  setValue(window, pass, 'secret123');
  click(btn(doc, '登录'));
  const ok = await waitFor(() => lastCall(/\/api\/login$/, 'POST'));
  check('S1 提交登录请求', !!ok && ok.body.username === 'admin' && ok.body.password === 'secret123', JSON.stringify(ok && ok.body));
  const authed = await waitFor(() => !doc.querySelector('.pp-login') && doc.querySelectorAll('.pp-stat').length >= 4);
  check('S1 登录后进入控制台', !!authed);
  check('S1 会话写入 localStorage', window.localStorage.getItem('token') === 'tok-login', window.localStorage.getItem('token'));
}

/* S2 登录失败提示 */
{
  const { window, doc } = await mount({ token: '' });
  setValue(window, inputByPlaceholder(doc, '用户名'), 'nobody');
  setValue(window, inputByPlaceholder(doc, '密码'), '');
  click(btn(doc, '登录'));
  await sleep(300);
  const hasErr = await waitFor(() => doc.querySelector('.ant-alert-error, .ant-form-item-explain-error'));
  check('S2 空密码被前端拦下（不误发请求）', !lastCall(/\/api\/login$/, 'POST') && !!hasErr, 'calls=' + calls.length);
}

/* S3 双因素登录 */
{
  const { window, doc } = await mount({ token: '' });
  setValue(window, inputByPlaceholder(doc, '用户名'), 'needs-mfa');
  setValue(window, inputByPlaceholder(doc, '密码'), 'secret123');
  click(btn(doc, '登录'));
  const step2 = await waitFor(() => inputByPlaceholder(doc, '动态'));
  check('S3 触发 TOTP 第二步', !!step2);
  setValue(window, step2, '123456');
  click(btn(doc, '验证并登录'));
  const second = await waitFor(() => calls.filter((c) => /\/api\/login$/.test(c.url) && c.method === 'POST').length >= 2);
  const last = [...calls].reverse().find((c) => /\/api\/login$/.test(c.url));
  check('S3 第二次带 step/totp_code', !!second && last.body.step === 'verify_totp' && last.body.totp_code === '123456', JSON.stringify(last && last.body));
}

/* S4 概览加载 */
{
  const { doc } = await mount({ token: fakeJwt });
  await waitFor(() => doc.querySelectorAll('.pp-stat').length >= 4);
  check('S4 请求 /api/stats 带 days', !!lastCall(/\/api\/stats\?days=30/), calls.map((c) => c.url).join(' | '));
  check('S4 渲染 4 个指标卡', doc.querySelectorAll('.pp-stat').length === 4);
  check(
    'S4 面积图 + 环形图都在',
    !!(
      (await waitFor(
        () =>
          doc.querySelectorAll('.pp-chart svg path').length >= 2 && doc.querySelectorAll('.pp-donut svg circle').length >= 2,
        3000
      ))
    ),
    'paths=' +
      doc.querySelectorAll('.pp-chart svg path').length +
      ' circles=' +
      doc.querySelectorAll('.pp-donut svg circle').length
  );
}

/* S5 新建用户 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '新建用户'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  setValue(window, inputByPlaceholder(doc, 'xxxxxxxx'), '33333333-3333-4333-8333-333333333333');
  setValue(window, inputByLabel(doc, '所有者'), 'carol');
  await sleep(80);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/uuids$/, 'POST'));
  check('S5 POST /api/uuids 载荷正确', !!req && req.body.uuid === '33333333-3333-4333-8333-333333333333' && req.body.owner === 'carol' && req.body.status === 'active', JSON.stringify(req && req.body));
  const appeared = await waitFor(() => visibleText(doc).includes('carol'));
  check('S5 列表刷新出现新分组', !!appeared);
}

/* S6 编辑分组 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  const editBtn = dataRows(doc)[0].querySelectorAll('button')[1];
  click(editBtn);
  await waitFor(() => doc.querySelector('.ant-modal'));
  const owner = inputByLabel(doc, '所有者');
  check('S6 编辑弹窗回填原值', owner && owner.value === 'alice', owner && owner.value);
  setValue(window, owner, 'alice-2');
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/uuids$/, 'PUT'));
  check('S6 PUT /api/uuids 带 uuid 与改动', !!req && req.body.uuid === '11111111-1111-4111-8111-111111111111' && req.body.owner === 'alice-2', JSON.stringify(req && req.body));
}

/* S7 删除分组（二次确认） */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  const delBtn = dataRows(doc)[0].querySelectorAll('button')[2];
  click(delBtn);
  const ok = await waitFor(() => popConfirmOk(doc) || modalOk(doc));
  check('S7 点击删除先弹二次确认', !!ok);
  click(popConfirmOk(doc) || modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/uuids$/, 'DELETE'));
  check('S7 DELETE /api/uuids 带 uuid', !!req && req.body.uuid === '11111111-1111-4111-8111-111111111111', JSON.stringify(req && req.body));
}

/* S8 搜索分组 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  const search = inputByPlaceholder(doc, '搜索');
  setValue(window, search, 'bob');
  const hit = await waitFor(() => lastCall(/\/api\/uuids\?.*q=bob/));
  check('S8 搜索参数带到接口', !!hit, calls.map((c) => c.url).join(' | '));
}

/* S9 模板设为默认 + 新建 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#tpls' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(
    [...doc.querySelectorAll('button')].filter(
      (b) => (b.textContent.includes('设为默认') || b.getAttribute('aria-label') === '设为默认') && !b.disabled
    )[0]
  );
  const def = await waitFor(() => lastCall(/templates\/manage\/set-default/, 'POST'));
  check('S9 设为默认 POST 带 id', !!def && def.body.id === 2, JSON.stringify(def && def.body));

  click(btn(doc, '新建模板'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  setValue(window, inputByLabel(doc, '模板名称'), 'E2E 模板');
  setValue(window, inputByLabel(doc, '模板地址'), 'https://s3.example.com/e2e.yaml');
  click(modalOk(doc));
  const created = await waitFor(() => lastCall(/templates\/manage$/, 'POST'));
  check('S9 新建模板 POST 载荷', !!created && created.body.name === 'E2E 模板' && created.body.url === 'https://s3.example.com/e2e.yaml', JSON.stringify(created && created.body));
}

/* S10 接入服务器 → 安装命令弹窗 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#servers' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '接入服务器'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  setValue(window, inputByLabel(doc, '服务器名称'), 'sg-03');
  setValue(window, inputByLabel(doc, '标签'), '新加坡');
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/servers$/, 'POST'));
  check('S10 创建服务器 POST 载荷', !!req && req.body.name === 'sg-03' && req.body.tags === '新加坡', JSON.stringify(req && req.body));
  const shown = await waitFor(() => visibleText(doc).includes('ag_TESTTOKEN123'));
  check('S10 弹出接入密钥 + 安装命令', !!shown, visibleText(doc).slice(0, 200));
}

/* S11 查看接入命令（已有服务器） */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#servers' });
  await waitFor(() => dataRows(doc).length >= 2);
  check('S11 工具栏有「更新 Agent」按钮并显示最新版本', !!btn(doc, '更新 Agent') && visibleText(doc).includes('1.2.8'), visibleText(doc).slice(0, 120));
  const labels = (r) => [...r.querySelectorAll('button')].map((b) => b.getAttribute('aria-label')).filter(Boolean);
  check(
    'S11 行内按钮与老版一致（安装 agent / 更新 agent / 探测 / 命令 / 编辑 / 轮换密钥 / 任务队列 / 删除）',
    ['安装 agent', '更新 agent', '探测', '命令', '编辑', '轮换密钥', '任务队列', '删除'].every((t) =>
      labels(dataRows(doc)[0]).some((l) => l.startsWith(t))
    ),
    JSON.stringify(labels(dataRows(doc)[0]))
  );
  check(
    'S11 操作列每个图标按钮都有提示（aria-label）',
    [...dataRows(doc)[0].querySelectorAll('button')].every((b) => (b.getAttribute('aria-label') || '').trim().length > 0),
    [...dataRows(doc)[0].querySelectorAll('button')].map((b) => b.getAttribute('aria-label')).join(' | ')
  );
  click(btn(doc, '安装 agent'));
  const req = await waitFor(() => lastCall(/agent-key/));
  check('S11 点「安装 agent」请求 agent-key', !!req);
  const shown = await waitFor(() => visibleText(doc).includes('ag_VIEWABLE'));
  check('S11 显示明文密钥与命令', !!shown);
  click([...doc.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === '关闭'));
  await sleep(200);

  /* 实时终端：WebSocket 直连（Durable Object 中转） */
  click(btn(doc, '命令'));
  const shellBox = await waitFor(() => doc.querySelector('.pp-shell-term'), 3000);
  check('S11 点「命令」打开实时终端（非命令推送）', !!shellBox, 'shell=' + !!shellBox);
  const ready = await waitFor(() => visibleText(doc).includes('已连接'), 4000);
  check('S11 终端状态变为「已连接」', !!ready, visibleText(doc).slice(0, 160));
  const sock = (window.__PP_WS || [])[0];
  check(
    'S11 浏览器连到 /api/shell/browser/:id 且带登录令牌',
    !!sock && String(sock.url).includes('/api/shell/browser/1') && String(sock.url).includes('token='),
    sock ? sock.url : 'no ws'
  );
  const printed = await waitFor(() => {
    const box = doc.querySelector('.pp-shell-term');
    return box && box.textContent.includes('root@hk-01') ? box.textContent : null;
  }, 3000);
  check('S11 终端能收到远端输出', !!printed, String(printed || '').slice(0, 80));
  click([...doc.querySelectorAll('.ant-modal button')].find((b) => b.textContent.includes('清屏')));
  await sleep(120);
  check(
    'S11 清屏按钮清空本地屏幕',
    !(doc.querySelector('.pp-shell-term') || {}).textContent,
    (doc.querySelector('.pp-shell-term') || {}).textContent
  );
}

/* S11b 批量更新 Agent（未勾选 → all: true） */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#servers' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '更新 Agent'));
  await sleep(200);
  const okBtn = await waitFor(() => [...doc.querySelectorAll('.ant-popover button')].find((b) => b.textContent.trim() === '推送'));
  check('S11b 点「更新 Agent」弹出确认', !!okBtn);
  if (okBtn) {
    click(okBtn);
    const req = await waitFor(() => lastCall(/agent\/push-update$/, 'POST'));
    check('S11b 未勾选时推送全部（all: true）', !!req && req.body.all === true, JSON.stringify(req && req.body));
  }
}

/* S12 API 密钥创建 → 明文只显示一次 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#sys' });
  await waitFor(() => doc.querySelector('.ant-tabs'));
  click(btn(doc, 'API 密钥'));
  await waitFor(() => dataRows(doc).length >= 1);
  click(btn(doc, '新建密钥'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  setValue(window, inputByLabel(doc, '密钥名称'), 'e2e-key');
  await sleep(80);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/apikeys$/, 'POST'));
  check(
    'S12 创建密钥 POST 名称+默认权限',
    !!req && req.body.key_name === 'e2e-key' && Array.isArray(req.body.permissions) && req.body.permissions.length > 0,
    JSON.stringify(req && req.body)
  );
  const shown = await waitFor(() => visibleText(doc).includes('pk_LIVE_'));
  check('S12 明文密钥弹窗出现', !!shown);
}

/* S13 日志清理（密码校验） */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#logs' });
  await waitFor(() => visibleText(doc).includes('清理访问日志'));
  // 日志页挂载后可能还有一次数据回填重渲染，会把未提交的输入重置；
  // 每次尝试都重新填一次密码，再立刻提交。
  let req = null;
  for (let attempt = 0; attempt < 3 && !req; attempt++) {
    setValue(window, inputByPlaceholder(doc, '当前登录密码'), 'secret123');
    await sleep(150);
    click(btn(doc, '执行清理'));
    const okBtn = await waitFor(() => popConfirmOk(doc));
    click(okBtn);
    try { req = await waitFor(() => lastCall(/logs\/cleanup/, 'POST'), 2000); } catch (e) { req = null; }
  }
  check('S13 清理 POST 带 days+password', !!req && req.body.password === 'secret123' && typeof req.body.days === 'number', JSON.stringify(req && req.body));
}

/* S14 MFA 绑定全流程 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#sec' });
  await waitFor(() => visibleText(doc).includes('双因素认证'));
  setValue(window, inputByPlaceholder(doc, '当前登录密码'), 'secret123');
  await sleep(60);
  click(btn(doc, '开始绑定'));
  const init = await waitFor(() => lastCall(/mfa\/init/, 'POST'));
  check('S14 绑定第一步 POST mfa/init', !!init && init.body.password === 'secret123');
  const shownSecret = await waitFor(() => visibleText(doc).includes('JBSWY3DPEHPK3PXP'));
  check('S14 出现二维码/密钥', !!shownSecret && !!doc.querySelector('.ant-qrcode'), visibleText(doc).slice(-220));
  const otp = [...doc.querySelectorAll('input')].find((i) => (i.getAttribute('placeholder') || '').includes('动态码'));
  setValue(window, otp, '123456');
  await sleep(60);
  click(btn(doc, '完成启用'));
  const verify = await waitFor(() => lastCall(/mfa\/verify-first/, 'POST'));
  check('S14 校验 TOTP 提交', !!verify && verify.body.totp_code === '123456' && verify.body.secret === 'JBSWY3DPEHPK3PXP', JSON.stringify(verify && verify.body));
  const codes = await waitFor(() => visibleText(doc).includes('AAAABBBB'));
  check('S14 展示 10 组备份码', !!codes);
}

/* S15 监控页自动刷新 + 服务矩阵 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#status' });
  await waitFor(() => doc.querySelectorAll('.pp-gauge').length >= 2);
  check('S15 请求 /api/servers/monitor', !!lastCall(/servers\/monitor/));
  check('S15 渲染仪表盘与矩阵', doc.querySelectorAll('.pp-gauge svg').length >= 2 && visibleText(doc).includes('singbox'));
  check('S15 告警区列出失败服务', visibleText(doc).includes('jp-02'));
  check('S15 服务状态矩阵已移除', !visibleText(doc).includes('服务状态矩阵'), visibleText(doc).slice(-200));
  check(
    'S15 服务器明细为卡片（含网络/负载指标）',
    doc.querySelectorAll('.pp-srv-card').length >= 2 &&
      visibleText(doc).includes('网络 ↓ / ↑') &&
      visibleText(doc).includes('负载 1 / 5 / 15'),
    'cards=' + doc.querySelectorAll('.pp-srv-card').length
  );
  check(
    'S15 地区用国旗图片（不依赖 emoji）',
    doc.querySelectorAll('.pp-srv-card .pp-flag, .pp-srv-card .pp-cc').length >= 2,
    'flag=' + doc.querySelectorAll('.pp-srv-card .pp-flag').length + ' cc=' + doc.querySelectorAll('.pp-srv-card .pp-cc').length
  );

  /* 顶部三卡同处一个 grid：靠 align-items:stretch 做到等高 */
  check(
    'S15 顶部三卡在同一 grid（等高布局）',
    doc.querySelectorAll('.pp-mon-top-grid > .pp-card').length === 3,
    'gridCards=' + doc.querySelectorAll('.pp-mon-top-grid > .pp-card').length
  );

  /* 双栈机器两行都显示；只有 IPv6 的机器不硬凑 IPv4 行 */
  const cards = [...doc.querySelectorAll('.pp-srv-card')];
  const ipsOf = (c) => [...(c ? c.querySelectorAll('.pp-srv-ip') : [])].map((n) => n.textContent.trim());
  check(
    'S15 双栈机器同时显示 IPv4 与 IPv6',
    ipsOf(cards[0]).join('|').includes('IPv4 1.1.1.1') && ipsOf(cards[0]).join('|').includes('IPv6 2001:db8::1'),
    ipsOf(cards[0]).join(' | ')
  );
  check(
    'S15 只有 IPv6 的机器只显示 IPv6 一行',
    ipsOf(cards[1]).length === 1 && ipsOf(cards[1])[0].startsWith('IPv6'),
    ipsOf(cards[1]).join(' | ')
  );
  check(
    'S15 网络/磁盘显示实际流量（兼容 camelCase 与 snake_case）',
    cards[0] && cards[0].textContent.includes('350.0 MB') && cards[0].textContent.includes('12.0 MB'),
    cards[0] ? cards[0].textContent.replace(/\s+/g, ' ').slice(0, 160) : 'no card'
  );
  check(
    'S15 掉线设备卡片带红色边框标记',
    !!doc.querySelector('.pp-srv-card.is-down'),
    'down=' + doc.querySelectorAll('.pp-srv-card.is-down').length
  );

  /* 全球分布：卡片可开大图；不再显示常驻标签，改为悬停圆点看信息 */
  check('S15 全球分布卡有「查看大图」按钮', !!btn(doc, '查看大图'));
  const hoverDot = doc.querySelector('.pp-map-card .pp-map-cluster');
  if (hoverDot) {
    hoverDot.dispatchEvent(new window.MouseEvent('mouseover', { bubbles: true }));
    await sleep(80);
  }
  const hoverTip = doc.querySelector('.pp-map-card .pp-map-tip');
  check(
    'S15 悬停圆点显示名称与地区信息',
    !!hoverTip && /香港/.test(hoverTip.textContent) && /地区/.test(hoverTip.textContent),
    hoverTip ? hoverTip.textContent.replace(/\s+/g, ' ').slice(0, 80) : 'no tip'
  );
  check(
    'S15 小图不显示地区名（悬停查看，避免拥挤）',
    doc.querySelectorAll('.pp-map-card .pp-map-place').length === 0 &&
      doc.querySelectorAll('.pp-map-label').length === 0 &&
      doc.querySelectorAll('.pp-map-links').length === 0,
    'places=' + doc.querySelectorAll('.pp-map-card .pp-map-place').length
  );

  click(btn(doc, '查看大图'));
  const bigMap = await waitFor(() => doc.querySelector('.ant-modal .pp-map-lg'), 2500);
  const bigPlaces = [...doc.querySelectorAll('.ant-modal .pp-map-place')].map((n) => n.textContent.trim());
  check('S15 点「查看大图」弹出大地图', !!bigMap, 'lg=' + doc.querySelectorAll('.pp-map-lg').length);
  check(
    'S15 大图用地图内文字显示地区名（无卡片/引线）',
    bigPlaces.length >= 2 &&
      bigPlaces.some((t) => t.includes('香港')) &&
      bigPlaces.some((t) => t.includes('东京')) &&
      doc.querySelectorAll('.ant-modal .pp-map-label').length === 0 &&
      doc.querySelectorAll('.ant-modal .pp-map-links').length === 0,
    JSON.stringify(bigPlaces)
  );
  const zoomIn = doc.querySelector('.ant-modal .pp-map-zoom button[aria-label="放大"]');
  const zoomOut = doc.querySelector('.ant-modal .pp-map-zoom button[aria-label="缩小"]');
  check('S15 大地图带缩放控件', !!zoomIn && !!zoomOut);
  if (zoomIn) {
    click(zoomIn);
    await sleep(120);
    const tf = (doc.querySelector('.ant-modal .pp-map-stage') || {}).style?.transform || '';
    check('S15 点「放大」后地图放大', /scale\(1\.35\)/.test(tf), tf);
    click(doc.querySelector('.ant-modal .pp-map-zoom button[aria-label="重置缩放"]'));
    await sleep(120);
    const tf2 = (doc.querySelector('.ant-modal .pp-map-stage') || {}).style?.transform || '';
    check('S15 重置后回到 100%', /scale\(1\)/.test(tf2), tf2);
  }
  const mapModal = doc.querySelector('.pp-map-lg') ? doc.querySelector('.pp-map-lg').closest('.ant-modal') : null;
  click(mapModal ? [...mapModal.querySelectorAll('button')].find((b) => b.textContent.includes('关闭')) : null);
  const mapClosed = await waitFor(() => !doc.querySelector('.ant-modal .pp-map-lg'), 2000);
  check('S15 大地图弹窗可关闭', !!mapClosed);
}

/* S16 节点页：一键下发 */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#singbox' });
  await waitFor(() => visibleText(doc).includes('节点绑定（'));
  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('节点绑定')));
  await waitFor(() => visibleText(doc).includes('一键下发'));
  click(btn(doc, '一键下发'));
  const ok = await waitFor(() => modalOk(doc));
  check('S16 一键下发先二次确认', !!ok);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/singbox\/apply-all/, 'POST'));
  check('S16 POST apply-all 带 push', !!req && req.body && req.body.push === true, JSON.stringify(req && req.body));
}

/* S17 系统管理非超管 → 403 */
{
  const otherJwt = [
    fakeJwt.split('.')[0],
    Buffer.from(JSON.stringify({ sub: 'ops', exp: Math.floor(NOW / 1000) + 86400 })).toString('base64url'),
    'sig',
  ].join('.');
  const { doc } = await mount({ token: otherJwt, hash: '#sys' });
  await sleep(200);
  check('S17 非超管访问 #sys 显示 403', visibleText(doc).includes('仅超级管理员可访问'));
}

/* S18 新建协议模板（JSON 驱动：自动填示例 + 实时解析 + from-config 保存） */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#singbox' });
  await waitFor(() => visibleText(doc).includes('新建模板'));
  click(btn(doc, '新建模板'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  const prev = await waitFor(() => lastCall(/singbox\/templates\/preview/, 'POST'));
  check('S18 打开即由后端渲染示例配置', !!prev);
  const ta = await waitFor(() => {
    const el = [...doc.querySelectorAll('.ant-modal textarea')][0];
    return el && String(el.value).includes('inbounds') ? el : null;
  });
  check('S18 sb.json 已自动填入', !!ta);
  const parseTitles = [...doc.querySelectorAll('.ant-modal span')].filter((el) => el.childElementCount === 0 && el.textContent.trim() === '解析结果');
  check('S18 解析结果只渲染一次', parseTitles.length === 1, String(parseTitles.length));
  const verLabels = [...doc.querySelectorAll('.ant-modal div')].filter((el) => el.childElementCount === 0 && el.textContent.includes('sing-box 版本（留空'));
  check('S18 版本选择只渲染一次', verLabels.length === 1, String(verLabels.length));
  const portVals = [...doc.querySelectorAll('.ant-modal .ant-input-number-input')].map((el) => el.value);
  check(
    'S18 解析结果实时展示（协议/端口/用户数）',
    visibleText(doc).includes('解析结果') && visibleText(doc).includes('hysteria2') && portVals.includes('13701'),
    JSON.stringify(portVals)
  );
  check(
    'S18 解析结果按协议渲染编辑字段',
    visibleText(doc).includes('加密方法') &&
      visibleText(doc).includes('TLS SNI') &&
      visibleText(doc).includes('用户凭据由节点授权注入'),
    visibleText(doc).slice(-300)
  );
  const tagInput = doc.querySelector('.ant-modal input[placeholder="tag"]');
  setValue(window, tagInput, 'hy2-edited');
  await sleep(250);
  check('S18 改标签实时写回 sb.json', String(ta.value).includes('hy2-edited'), String(ta.value).slice(0, 160));
  const sniInput = doc.querySelector('.ant-modal input[placeholder="example.com"]');
  setValue(window, sniInput, 'cdn.example.com');
  await sleep(250);
  check('S18 改 TLS SNI 实时写回 sb.json', String(ta.value).includes('"server_name": "cdn.example.com"'), String(ta.value).slice(0, 200));
  setValue(window, inputByPlaceholder(doc, '例如：内置默认'), 'e2e-tpl');
  await sleep(100);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/singbox\/templates\/from-config$/, 'POST'));
  check(
    'S18 保存走 from-config（id=0 + sb_json）',
    !!req && req.body.id === 0 && req.body.name === 'e2e-tpl' && /hysteria2/.test(String(req.body.sb_json)),
    JSON.stringify(req && req.body).slice(0, 220)
  );
}

/* S19 编辑模板：读取明文参数 → 自动渲染当前配置 → 回写走 from-config(id) */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#singbox' });
  await waitFor(() => visibleText(doc).includes('新建模板'));
  await waitFor(() => dataRows(doc).length >= 1);
  click(
    [...doc.querySelectorAll('.ant-table-tbody button')].find(
      (b) => b.textContent.trim() === '编辑' || b.getAttribute('aria-label') === '编辑'
    )
  );
  await waitFor(() => doc.querySelector('.ant-modal'));
  const det = await waitFor(() => lastCall(/\/api\/singbox\/templates\/\d+$/, 'GET'));
  check('S19 编辑时读取模板明文参数', !!det);
  const prev = await waitFor(() => lastCall(/singbox\/templates\/preview/, 'POST'));
  check('S19 编辑时自动渲染当前配置', !!prev);
  const ta = await waitFor(() => {
    const el = [...doc.querySelectorAll('.ant-modal textarea')][0];
    return el && String(el.value).includes('inbounds') ? el : null;
  });
  check('S19 sb.json 已回填', !!ta);
  await sleep(100);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/from-config$/, 'POST'));
  check('S19 保存带 id 走更新', !!req && Number(req.body.id) > 0, JSON.stringify(req && req.body).slice(0, 160));
}

/* S20 绑定节点（含 select 选择服务器） */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#singbox' });
  await waitFor(() => visibleText(doc).includes('节点绑定（'));
  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('节点绑定')));
  await waitFor(() => visibleText(doc).includes('绑定节点'));
  click([...doc.querySelectorAll('button')].find((b) => b.textContent.replace(/\s+/g, '') === '绑定节点'));
  const modal = await waitFor(() => doc.querySelector('.ant-modal'));
  check('S20 节点弹窗打开', !!modal);
  if (!modal) {
    check('S20 绑定节点 POST（server/template/auto_deploy）', false, 'modal 未打开');
  } else {
    const footer = modal.querySelector('.ant-modal-footer');
    const footerText = footer ? [...footer.querySelectorAll('button')].map((b) => b.textContent.trim()) : [];
    check('S20 弹窗按钮为中文（取消）', footerText.some((t) => t.indexOf('取消') >= 0), JSON.stringify(footerText));
    check(
      'S20 节点弹窗不含端口基线 / WARP / 授权提示',
      !modal.textContent.includes('端口基线') && !modal.textContent.includes('WARP') && !modal.textContent.includes('当前授权'),
      modal.textContent.replace(/\s+/g, ' ').slice(0, 240)
    );
    setValue(window, inputByLabel(doc, '节点名称'), 'e2e-node');
    // 选择服务器：rc-select 靠 mousedown 打开下拉
    const sel = modal.querySelector('.ant-select-content, .ant-select-selector');
    if (sel) {
      sel.dispatchEvent(new window.MouseEvent('mousedown', { bubbles: true }));
      click(sel);
    }
    await sleep(220);
    const opt = doc.querySelector('.ant-select-item-option');
    click(opt);
    await sleep(140);
    check('S20 已选中服务器', !!opt && !!visibleText(doc).match(/hk-01|jp-02/), 'options=' + doc.querySelectorAll('.ant-select-item-option').length);
    setValue(window, inputByLabel(doc, '域名'), 'e2e.example.com');
    await sleep(80);
    click(modalOk(doc));
    const req = await waitFor(() => lastCall(/\/api\/singbox\/nodes$/, 'POST'));
    check(
      'S20 绑定节点 POST（server/template/auto_deploy）',
      !!req && req.body.name === 'e2e-node' && req.body.server_id > 0 && req.body.auto_deploy === true,
      JSON.stringify(req && req.body)
    );
  }
}

/* S21 单节点下发 */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#singbox' });
  await waitFor(() => visibleText(doc).includes('节点绑定（'));
  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('节点绑定')));
  await waitFor(() => dataRows(doc).length >= 1);
  click(
    [...doc.querySelectorAll('.ant-table-tbody button')].find(
      (b) => b.textContent.trim() === '下发' || b.getAttribute('aria-label') === '下发'
    )
  );
  const req = await waitFor(() => lastCall(/singbox\/nodes\/\d+\/apply/, 'POST'));
  check('S21 单节点下发 POST apply', !!req, calls.map((c) => c.method + ' ' + c.url).join(' | '));
}

/* S22 分组详情：添加节点配置 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '详情'));
  await waitFor(() => doc.querySelector('.ant-drawer'));
  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('节点配置')));
  await waitFor(() => visibleText(doc).includes('添加配置'));
  click(btn(doc, '添加配置'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  setValue(window, inputByPlaceholder(doc, 'vless://'), 'vless://new@9.9.9.9:443?type=ws#new');
  await sleep(80);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/uuid-configs$/, 'POST'));
  check('S22 添加配置 POST uuid+config_data', !!req && /new@9.9.9.9/.test(req.body.config_data) && !!req.body.uuid, JSON.stringify(req && req.body).slice(0, 200));
}

/* S23 分组详情：授权节点 */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '详情'));
  await waitFor(() => doc.querySelector('.ant-drawer'));
  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('授权节点')));
  const tbl = await waitFor(() => doc.querySelector('.ant-drawer .ant-table-tbody'));
  check(
    'S23 授权节点表渲染（含节点与授权状态）',
    !!tbl && visibleText(doc).includes('hk-node') && /已授权|未授权/.test(visibleText(doc)),
    visibleText(doc).slice(-220)
  );
  const rows = [...doc.querySelectorAll('.ant-drawer .ant-table-tbody tr.ant-table-row')];
  const box = rows.length ? rows[0].querySelector('input[type=checkbox]') : null;
  if (box) {
    box.click();
    await sleep(150);
  }
  click(btn(doc, '保存授权'));
  const req = await waitFor(() => lastCall(/\/api\/uuids\/[^/]+\/nodes$/, 'PUT'));
  check('S23 保存授权 PUT node_ids', !!req && Array.isArray(req.body.node_ids), JSON.stringify(req && req.body));
}

/* S24 分组详情：外部订阅源 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '详情'));
  await waitFor(() => doc.querySelector('.ant-drawer'));
  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('外部订阅源')));
  await waitFor(() => visibleText(doc).includes('添加订阅源'));
  click(btn(doc, '添加订阅源'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  setValue(window, inputByLabel(doc, '订阅地址'), 'https://sub.example.com/api/v1/client/subscribe?token=abc');
  await sleep(80);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/manage\/uuid-sub-sources$/, 'POST'));
  check('S24 添加外部订阅源 POST', !!req && /sub\.example\.com/.test(req.body.url) && !!req.body.uuid, JSON.stringify(req && req.body));
}

/* S25 编辑服务器 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#servers' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '编辑'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  const name = inputByLabel(doc, '名称');
  check('S25 编辑弹窗回填名称', name && name.value === 'hk-01', name && name.value);
  check(
    'S25 编辑弹窗不再有「默认协议模板」',
    !visibleText(doc).includes('默认协议模板') && !inputByLabel(doc, '默认协议模板')
  );
  setValue(window, name, 'hk-01-renamed');
  setValue(window, inputByLabel(doc, '标签'), '香港,改过');
  await sleep(80);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/servers\/\d+$/, 'PUT'));
  check('S25 编辑服务器 PUT 载荷', !!req && req.body.name === 'hk-01-renamed' && req.body.tags === '香港,改过', JSON.stringify(req && req.body));
}

/* S26 监控：世界地图打点 + 点开详情 */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#status' });
  const dot = await waitFor(() => doc.querySelector('.pp-map-cluster'));
  check('S26 世界地图打点渲染', !!dot, 'dots=' + doc.querySelectorAll('.pp-map-cluster').length);
  check('S26 无经纬度时按国家中心点兜底', doc.querySelectorAll('.pp-map-cluster').length >= 2 && visibleText(doc).includes('按国家中心点估算'), visibleText(doc).slice(-160));
  click(dot);
  const drawer = await waitFor(() => doc.querySelector('.ant-drawer') && lastCall(/\/api\/servers\/\d+$/, 'GET'));
  check('S26 点击圆点打开服务器详情', !!drawer);
  const svc = await waitFor(() => visibleText(doc).includes('sing-box'));
  check('S26 详情里有服务状态', !!svc);
  const svcRows = [...doc.querySelectorAll('.ant-drawer .pp-svc-row')].map((n) => n.textContent.replace(/\s+/g, ' '));
  check(
    'S26 服务状态只显示 sing-box / xray / mtg / cloudflared',
    svcRows.length === 4 &&
      svcRows.some((t) => t.includes('sing-box')) &&
      svcRows.some((t) => t.includes('xray')) &&
      svcRows.some((t) => t.includes('mtg')) &&
      svcRows.some((t) => t.includes('cloudflared')) &&
      !svcRows.some((t) => /sshd|caddy|nginx|clash|mihomo/.test(t)),
    JSON.stringify(svcRows)
  );
}

/* S27 服务器详情：服务重启排队 */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#status' });
  await waitFor(() => doc.querySelector('.pp-map-cluster'));
  click(doc.querySelector('.pp-map-cluster'));
  await waitFor(() => visibleText(doc).includes('重启'));
  const row = [...doc.querySelectorAll('.ant-drawer button')].find((b) => b.textContent.trim() === '重启' && !b.disabled);
  check('S27 有可用的「重启」按钮', !!row);
  click(row);
  const req = await waitFor(() => lastCall(/\/api\/agent\/tasks$/, 'POST'));
  check('S27 重启走 agent/tasks 任务队列', !!req && req.body.type === 'restart' && !!req.body.service_name, JSON.stringify(req && req.body));
}

/* S28 通知设置 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#servers' });
  await waitFor(() => visibleText(doc).includes('通知设置'));
  click(btn(doc, '通知设置'));
  await waitFor(() => lastCall(/\/api\/monitor\/notify$/, 'GET'));
  setValue(window, inputByLabel(doc, '收件邮箱'), 'ops@example.com');
  await sleep(300);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/monitor\/notify$/, 'PUT'));
  check('S28 保存通知设置 PUT emails', !!req && /ops@example\.com/.test(req.body.emails), JSON.stringify(req && req.body));
}

/* S29 邮箱绑定（安全中心，本人） */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#sec' });
  await waitFor(() => visibleText(doc).includes('绑定 / 验证邮箱'));
  click(btn(doc, '绑定 / 验证邮箱'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  setValue(window, inputByPlaceholder(doc, 'user@example.com'), 'me@example.com');
  await sleep(100);
  click(btn(doc, '发送验证码'));
  const sent = await waitFor(() => lastCall(/users\/email\/code$/, 'POST'));
  check('S29 发送验证码带 username+email', !!sent && sent.body.username === 'admin' && sent.body.email === 'me@example.com', JSON.stringify(sent && sent.body));
  const codeInput = await waitFor(() => inputByPlaceholder(doc, '6 位数字验证码'));
  setValue(window, codeInput, '123456');
  await sleep(80);
  click(btn(doc, '验证并绑定'));
  const verified = await waitFor(() => lastCall(/users\/email\/verify$/, 'POST'));
  check('S29 验证并绑定 POST', !!verified && verified.body.code === '123456' && verified.body.email === 'me@example.com', JSON.stringify(verified && verified.body));
}

/* S30 批量推送 UUID × 服务器 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#uuids' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '批量推送'));
  const modal = await waitFor(() => doc.querySelector('.ant-modal'));
  check('S30 批量推送弹窗打开', !!modal);
  if (modal) {
    const sels = [...modal.querySelectorAll('.ant-select-content, .ant-select-selector')];
    // 服务器
    if (sels[0]) sels[0].dispatchEvent(new window.MouseEvent('mousedown', { bubbles: true }));
    await sleep(200);
    click(doc.querySelector('.ant-select-item-option'));
    await sleep(150);
    // UUID
    const sels2 = [...doc.querySelectorAll('.ant-modal .ant-select-content, .ant-modal .ant-select-selector')];
    if (sels2[1]) sels2[1].dispatchEvent(new window.MouseEvent('mousedown', { bubbles: true }));
    await sleep(220);
    const opts = [...doc.querySelectorAll('.ant-select-item-option')];
    click(opts[opts.length - 1]);
    await sleep(150);
    click(modalOk(doc));
    const req = await waitFor(() => lastCall(/singbox\/push$/, 'POST'));
    check(
      'S30 批量推送 POST uuids+server_ids',
      !!req && Array.isArray(req.body.uuids) && req.body.uuids.length > 0 && Array.isArray(req.body.server_ids) && req.body.server_ids.length > 0,
      JSON.stringify(req && req.body)
    );
  } else {
    check('S30 批量推送 POST uuids+server_ids', false, 'modal 未打开');
  }
}

/* S31 节点管理不再包含「下发模板」页签 */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#singbox' });
  await waitFor(() => visibleText(doc).includes('协议模板'));
  const statLabels = [...doc.querySelectorAll('.pp-stat-label')].map((x) => x.textContent.replace(/\s+/g, ''));
  check('S31 节点管理顶部不再有统计卡', statLabels.length === 0, JSON.stringify(statLabels));
  const tabs = [...doc.querySelectorAll('.ant-tabs-tab')].map((t) => t.textContent.replace(/\s+/g, ''));
  check(
    'S31 三个页签：协议模板 / 节点绑定 / MTProto',
    tabs.length === 3 && tabs.some((t) => t.includes('MTProto')) && !tabs.some((t) => t.includes('下发模板')),
    JSON.stringify(tabs)
  );
  const heads = [...doc.querySelectorAll('.pp-table thead th')].map((th) => th.textContent.replace(/\s+/g, ''));
  check(
    'S31 协议模板表不含「用户 / WARP」列',
    !heads.includes('用户') && !heads.includes('WARP'),
    JSON.stringify(heads)
  );

  const mtTab = [...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('MTProto'));
  if (mtTab) click(mtTab);
  await waitFor(() => visibleText(doc).includes('MTProto 节点'));
  const mtHeads = [...doc.querySelectorAll('.pp-table thead th')].map((th) => th.textContent.replace(/\s+/g, ''));
  check(
    'S31 MTProto 页签渲染节点表（伪装域名 / 端口 / 授权）',
    mtHeads.includes('伪装域名') && mtHeads.includes('端口') && mtHeads.includes('授权'),
    JSON.stringify(mtHeads)
  );
}

/* S31.6 任务中心：运行中详情自动刷新 */
{
  state.jobs = [{ id: 9, title: '自动刷新测试任务', status: 'running', total: 2, done: 1, failed: 0, percent: 50, created_at: NOW }];
  state.jobItems = [
    { id: 1, seq: 1, label: '节点A', status: 'success', message: '' },
    { id: 2, seq: 2, label: '节点B', status: 'pending', message: '' },
  ];
  const { doc } = await mount({ token: fakeJwt, hash: '#dash' });
  await waitFor(() => visibleText(doc).includes('任务'));
  click(btn(doc, '任务'));
  await waitFor(() => visibleText(doc).includes('自动刷新测试任务'));
  click(btn(doc, '详情'));
  await waitFor(() => visibleText(doc).includes('节点B'));
  state.jobs[0] = Object.assign({}, state.jobs[0], { status: 'success', done: 2, percent: 100 });
  state.jobItems[1] = Object.assign({}, state.jobItems[1], { status: 'success', message: '已完成' });
  await sleep(3600);
  const txt = visibleText(doc);
  check('S31.6 运行中详情 3s 内自动刷新', txt.includes('2/2') && txt.includes('已完成'), txt.slice(0, 240));
}

/* S32 检测全部 */
{
  const { doc } = await mount({ token: fakeJwt, hash: '#servers' });
  await waitFor(() => dataRows(doc).length >= 2);
  click(btn(doc, '检测全部'));
  const req = await waitFor(() => lastCall(/\/api\/servers\/\d+\/test$/, 'POST'));
  check('S32 检测全部逐台 POST test', !!req, calls.map((c) => c.method + ' ' + c.url).join(' | '));
}

/* S33 API 密钥权限编辑 */
{
  const { window, doc } = await mount({ token: fakeJwt, hash: '#sys' });
  await waitFor(() => doc.querySelector('.ant-tabs'));
  click(btn(doc, 'API 密钥'));
  await waitFor(() => dataRows(doc).length >= 1);
  click(btn(doc, '权限'));
  await waitFor(() => doc.querySelector('.ant-modal'));
  await sleep(120);
  click(modalOk(doc));
  const req = await waitFor(() => lastCall(/\/api\/apikeys\/\d+$/, 'PUT'));
  check(
    'S33 编辑密钥权限 PUT permissions',
    !!req && Array.isArray(req.body.permissions) && req.body.permissions.length > 0,
    JSON.stringify(req && req.body)
  );
}

/* S34 登录：用备份码代替 TOTP */
{
  const { window, doc } = await mount({ token: '' });
  setValue(window, inputByPlaceholder(doc, '用户名'), 'needs-mfa');
  setValue(window, inputByPlaceholder(doc, '密码'), 'secret123');
  click(btn(doc, '登录'));
  await waitFor(() => inputByPlaceholder(doc, '动态'));
  click(btn(doc, '用备份码登录'));
  const backupInput = await waitFor(() => inputByPlaceholder(doc, '备份码'));
  check('S34 切换到备份码输入', !!backupInput);
  setValue(window, backupInput, 'A1B2C3D4');
  await sleep(80);
  click(btn(doc, '用备份码登录'));
  const req = await waitFor(() => lastCall(/mfa\/login-with-backup$/, 'POST'));
  check(
    'S34 POST mfa/login-with-backup',
    !!req && req.body.backup_code === 'A1B2C3D4' && req.body.username === 'needs-mfa',
    JSON.stringify(req && req.body)
  );
  const ok = await waitFor(() => doc.querySelectorAll('.pp-stat').length >= 4);
  check('S34 备份码登录后进入控制台', !!ok);
}

/* S35 OIDC 回调：/admin#token=<jwt> 要能落盘并进入控制台 */
{
  const { window, doc } = await mount({ token: '', hash: '#token=oidc-jwt-abc' });
  await waitFor(() => doc.querySelectorAll('.pp-stat').length >= 4);
  check('S35 从 #token= 恢复登录态', window.localStorage.getItem('token') === 'oidc-jwt-abc', String(window.localStorage.getItem('token')));
  check('S35 进入控制台（不是登录页）', doc.querySelectorAll('.pp-login').length === 0 && doc.querySelectorAll('.pp-stat').length >= 4);
  check('S35 hash 被改回 #dash', String(window.location.hash) === '#dash', String(window.location.hash));
}

/* S36 用户中心：邮箱登录 → 我的订阅 */
{
  const { window, doc } = await mount({ path: '/', mode: 'portal' });
  check('S36 渲染用户中心登录页', visibleText(doc).includes('欢迎回来') && !!inputByPlaceholder(doc, '订阅所属邮箱'));
  setValue(window, inputByPlaceholder(doc, '订阅所属邮箱'), 'me@example.com');
  await sleep(80);
  click(btn(doc, '发送验证码'));
  const sent = await waitFor(() => lastCall(/\/api\/portal\/send-code$/, 'POST'));
  check('S36 发送验证码 POST email', !!sent && sent.body.email === 'me@example.com', JSON.stringify(sent && sent.body));

  setValue(window, inputByPlaceholder(doc, '6 位验证码'), '123456');
  await sleep(80);
  click(btn(doc, '登录'));
  const login = await waitFor(() => lastCall(/\/api\/portal\/login$/, 'POST'));
  check('S36 登录 POST email+code', !!login && login.body.code === '123456', JSON.stringify(login && login.body));
  const home = await waitFor(() => visibleText(doc).includes('个节点'));
  check('S36 登录后进入我的订阅', !!doc.querySelector('.adm-tab-bar') || !!home);
  check('S36 会话写入 pp_portal_token', window.localStorage.getItem('pp_portal_token') === 'portal-tok');
}

/* S37 用户中心：订阅卡片 / 链接 / 二维码 */
{
  const { window, doc } = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok' });
  const card = await waitFor(() => doc.querySelector('.msub'));
  check('S37 已登录直接进入订阅生成（移动端专版）', !!card, 'msub=' + doc.querySelectorAll('.msub').length);
  const body = visibleText(doc);
  check('S37 首页展示三种订阅格式', body.includes('原始') && body.includes('Clash') && body.includes('批量') && body.includes('生成订阅'));
  check('S37 我的订阅页有应用栏与标签栏（5 个：订阅/TG/配置/统计/我的）', !!doc.querySelector('.adm-nav-bar') && doc.querySelectorAll('.adm-tab-bar-item').length === 5);
  check(
    'S37 顶部订阅卡展示名称 / 状态 / 节点数',
    !!doc.querySelector('.ppm-hero') &&
      visibleText(doc).includes('主力节点') &&
      visibleText(doc).includes('正常') &&
      visibleText(doc).includes('12 个节点'),
    'hero=' + doc.querySelectorAll('.ppm-hero').length
  );

  /* 账号与退出收进第 4 个页签「我的」 */
  click([...doc.querySelectorAll('.adm-tab-bar-item')].find((b) => b.textContent.trim() === '我的'));
  const meCard = await waitFor(() => doc.querySelector('.macc'));
  check('S37 第 4 个页签打开「账号与信息」', !!meCard && visibleText(doc).includes('账号与信息'));
  check('S37 账号页有退出登录按钮', !!btn(doc, '退出登录'));
  check(
    'S37 移动端：底部 5 个切换栏、卡片无「管理配置」按钮',
    doc.querySelectorAll('.adm-tab-bar-item').length === 5 &&
      doc.querySelectorAll('.pp-sub-foot button').length === 0 &&
      !visibleText(doc).includes('管理配置'),
    'nav=' + doc.querySelectorAll('.adm-tab-bar-item').length + ' footBtns=' + doc.querySelectorAll('.pp-sub-foot button').length
  );

  click(btn(doc, '更换'));
  const mailModal = await waitFor(() => doc.querySelector('.ant-modal input[placeholder*="新邮箱"]'));
  check('S37 主页面可打开换绑邮箱', !!mailModal);
  if (mailModal) {
    setValue(window, mailModal, 'new@example.com');
    await sleep(80);
    click([...doc.querySelectorAll('.ant-modal button')].find((b) => b.textContent.includes('发送验证码')));
    const mailCall = await waitFor(() => lastCall(/\/api\/portal\/email\/send-code$/, 'POST'));
    check('S37 换绑邮箱 POST 新地址', !!mailCall && mailCall.body.email === 'new@example.com', JSON.stringify(mailCall && mailCall.body));
    click([...doc.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === '关闭'));
    await sleep(250);
  }
  /* 二维码入口已移到「订阅生成」的生成结果里，这里只验证卡片上不再有二维码按钮 */
  check('S37 订阅卡片不再直出二维码（改为生成后展示）', ![...doc.querySelectorAll('.pp-sub-card button')].some((b) => b.textContent.includes('二维码')));
}

/* S38 用户中心：单个 UUID 管理页（配置 / 订阅 / 统计 / 订阅源 / 信息） */
{
  const uuid = '11111111-1111-4111-8111-111111111111';
  const { window, doc } = await mount({
    path: '/',
    hash: '#/u/' + uuid,
    mode: 'portal',
    portalTok: 'portal-tok',
  });

  const table = await waitFor(() => doc.querySelector('.pp-table .ant-table-row, .ant-table-row'));
  check('S38 进入 UUID 管理页并加载配置列表', !!table && visibleText(doc).includes('香港 01'));
  check('S38 配置管理有引导说明', visibleText(doc).includes('查看这个 UUID 的节点（只读）'));

  /* 配置内容列吃满剩余宽度、内容完整展示（不再截断成 xxx…） */
  const fullCells = [...doc.querySelectorAll('.pp-cfg-table .pp-config-full')];
  const longCell = fullCells.find((c) => c.textContent.includes('host=jp1.example.com'));
  check(
    'S38 配置内容完整展示（长链接不截断）',
    fullCells.length >= 2 && !!longCell && !longCell.textContent.includes('…') && longCell.textContent.length > 100,
    'cells=' + fullCells.length + ' len=' + (longCell ? longCell.textContent.length : 0)
  );
  check(
    'S38 配置管理表格为固定布局（内容列自适应剩余宽度）',
    !!doc.querySelector('.pp-cfg-table .ant-table-layout-fixed'),
    'fixed=' + doc.querySelectorAll('.pp-cfg-table .ant-table-layout-fixed').length
  );
  check(
    'S38 配置管理为只读（无新增/编辑/删除/清空）',
    !btn(doc, '添加配置') &&
      !btn(doc, '清空全部') &&
      !visibleText(doc).includes('批量删除') &&
      ![...doc.querySelectorAll('.ant-table-tbody button')].some((b) => /编辑|删除/.test(b.textContent)),
    visibleText(doc).slice(0, 240)
  );
  check(
    'S38 移动端详情页：应用栏带返回、不显示标签栏',
    !!doc.querySelector('.adm-nav-bar') && doc.querySelectorAll('.adm-tab-bar-item').length === 0,
    'nav=' + doc.querySelectorAll('.adm-tab-bar-item').length
  );

  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('订阅生成')));
  await sleep(200);
  const segs = [...doc.querySelectorAll('.pp-fmt .ant-segmented-item')].map((e) => e.textContent.trim());
  check('S38 订阅生成有三种格式可切换', segs.length === 3, segs.join(' | '));
  check(
    'S38 生成前不展示任何链接',
    doc.querySelectorAll('.pp-fmt-out').length === 0 && visibleText(doc).includes('还没有生成'),
    'out=' + doc.querySelectorAll('.pp-fmt-out').length
  );

  // 生成原始订阅
  click(btn(doc, '生成订阅'));
  const rawReq = await waitFor(() => lastCall(/\/sub\/[0-9a-f-]{36}/), 3000);
  check('S38 点生成会请求原始订阅地址（不带 template）', !!rawReq && !String(rawReq.url).includes('template='), rawReq ? rawReq.url : 'no call');
  // 生成是异步的：先等结果区落 DOM，再断言按钮与导入项，否则会跟渲染抢跑
  const outBox = await waitFor(() => doc.querySelector('.pp-fmt-out'), 3000);
  check('S38 生成后出现结果区（复制 / 下载 / 二维码）', !!outBox && !!btn(doc, '复制链接') && !!btn(doc, '下载配置') && !!btn(doc, '查看二维码'));
  const chips = await waitFor(() => doc.querySelectorAll('.pp-app-chip').length >= 3, 2000);
  check('S38 生成后可一键导入客户端', !!chips, 'apps=' + doc.querySelectorAll('.pp-app-chip').length);

  // 切到 Clash 并生成，检查模板参数
  click([...doc.querySelectorAll('.pp-fmt .ant-segmented-item')].find((t) => t.textContent.includes('Clash')));
  await sleep(120);
  check('S38 选择 Clash 时才出现订阅模板', visibleText(doc).includes('订阅模板'));
  click(btn(doc, '生成订阅'));
  const clashReq = await waitFor(() => lastCall(/clash-config/), 3000);
  check('S38 Clash 请求带 template=<模板名>', !!clashReq && /template=[^&]+/.test(String(clashReq.url)), clashReq ? clashReq.url : 'no call');
  check('S38 订阅生成有引导说明', visibleText(doc).includes('订阅怎么用') && doc.querySelectorAll('.pp-guide-more').length >= 1);

  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('访问统计')));
  const stat = await waitFor(() => lastCall(/\/manage\/stats\//));
  check('S38 统计请求发出', !!stat);
  check('S38 统计数值渲染', await waitFor(() => visibleText(doc).includes('总访问') && visibleText(doc).includes('128')));
  check('S38 统计有引导说明', visibleText(doc).includes('这个 UUID 的访问统计'));
  check(
    'S38 统计有图表（趋势 + 构成）',
    doc.querySelectorAll('.pp-chart-box').length >= 2 &&
      visibleText(doc).includes('近 30 天拉取趋势') &&
      visibleText(doc).includes('访问构成') &&
      doc.querySelectorAll('.pp-chart-box svg').length >= 1,
    'boxes=' + doc.querySelectorAll('.pp-chart-box').length + ' svg=' + doc.querySelectorAll('.pp-chart-box svg').length
  );

  /* 最近访问改为弹窗：页面上只留摘要 + 按钮，明细点开再看 */
  const logBtn = btn(doc, '查看最近访问');
  check('S38 最近访问只显示摘要 + 查看按钮', !!doc.querySelector('.pp-recent') && !!logBtn, 'recent=' + doc.querySelectorAll('.pp-recent').length);
  check('S38 最近访问不再直接铺表格', doc.querySelectorAll('.ant-tabs-tabpane-active .ant-table').length === 0,
    'tables=' + doc.querySelectorAll('.ant-tabs-tabpane-active .ant-table').length);
  click(logBtn);
  const logRows = await waitFor(() => doc.querySelectorAll('.ant-modal .ant-table-row').length, 3000);
  check('S38 点按钮弹窗展示最近访问明细', !!logRows, 'rows=' + doc.querySelectorAll('.ant-modal .ant-table-row').length);
  check(
    'S38 弹窗内有时间 / 类型 / IP / 客户端列',
    ['时间', '类型', 'IP', '客户端'].every((t) => visibleText(doc).includes(t)) && visibleText(doc).includes('subscription')
  );
  click(doc.querySelector('.ant-modal .ant-modal-close'));
  const closed = await waitFor(() => !doc.querySelector('.ant-modal .ant-table-row'), 2000);
  check('S38 弹窗可关闭', !!closed);

  click([...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('外部订阅源')));
  await sleep(400);
  const srcCall = lastCall(/uuid-sub-sources/, 'GET');
  check('S38 外部订阅源列表请求', !!srcCall, calls.map((c) => c.method + ' ' + c.url).slice(-5).join(' | '));
  check('S38 外部订阅源渲染', visibleText(doc).includes('机场A'), visibleText(doc).slice(-160));
  check('S38 外部订阅源有引导说明', visibleText(doc).includes('把别处的订阅合并进来'));

  const infoTab = [...doc.querySelectorAll('.ant-tabs-tab')].find((t) => t.textContent.includes('信息与邮箱'));
  check('S38 存在「信息与邮箱」页签', !!infoTab);
  click(infoTab);
  await sleep(500);
  const infoCall = lastCall(/uuid-info/);
  check('S38 UUID 信息加载', !!infoCall, calls.map((c) => c.method + ' ' + c.url).slice(-4).join(' | '));
  check('S38 信息区渲染联系方式', visibleText(doc).includes('tg:@alice'), visibleText(doc).slice(-160));
  check('S38 信息与邮箱有引导说明', visibleText(doc).includes('账号与绑定信息'));
  check('S38 最近访问里的 unknown IP 显示为占位', !visibleText(doc).includes('unknown'));
  click(btn(doc, '更换绑定邮箱'));
  const mail = await waitFor(() => doc.querySelector('.ant-modal input[placeholder*="新邮箱"]'));
  check('S38 换绑邮箱弹窗打开', !!mail);
}

/* S39 用户中心 · PC 单页模式（无页签，区块左右两列；账号卡合并 UUID 信息） */
{
  const { doc } = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok', pc: true });
  await waitFor(() => doc.querySelector('.pp-sub-card'));
  const secs = await waitFor(() => (doc.querySelectorAll('.pp-embedded-sec').length ? doc.querySelectorAll('.pp-embedded-sec') : null));
  check('S39 PC 单页：不出现页签', doc.querySelectorAll('.ant-tabs-tab').length === 0, 'tabs=' + doc.querySelectorAll('.ant-tabs-tab').length);
  check('S39 PC 单页：五个内容区块（信息已并入顶部账号卡）', doc.querySelectorAll('.pp-embedded-sec').length === 5, 'secs=' + doc.querySelectorAll('.pp-embedded-sec').length);
  check(
    'S39 PC 单页：上排「统计|订阅生成」，下排整行「配置管理|外部订阅源|Telegram」',
    doc.querySelectorAll('.pp-embedded-big').length === 1 &&
      doc.querySelectorAll('.pp-embedded-top .pp-embedded-sec').length === 2 &&
      doc.querySelectorAll('.pp-embedded-bottom .pp-embedded-sec').length === 3 &&
      doc.querySelectorAll('.pp-embedded-top')[0].textContent.includes('访问统计') &&
      doc.querySelectorAll('.pp-embedded-top')[0].textContent.includes('订阅生成') &&
      doc.querySelectorAll('.pp-embedded-bottom')[0].textContent.includes('配置管理') &&
      doc.querySelectorAll('.pp-embedded-bottom')[0].textContent.includes('外部订阅源') &&
      doc.querySelectorAll('.pp-embedded-bottom')[0].textContent.includes('Telegram'),
    'big=' + doc.querySelectorAll('.pp-embedded-big').length
  );
  const text = visibleText(doc);
  check(
    'S39 PC 主页：订阅卡 + 详情整行，账号卡已移除',
    !doc.querySelector('.pp-home-pc > .pp-account-card') && !!doc.querySelector('.pp-home-pc > .pp-portal-embedded'),
    'pcWrap=' + doc.querySelectorAll('.pp-home-pc').length
  );
  const meta = doc.querySelector('.pp-sub-meta');
  check(
    'S39 PC 单页：去掉重复的 UUID 头与「所有者」',
    !doc.querySelector('.pp-portal-embedded .pp-portal-top') && !!meta && !meta.textContent.includes('所有者'),
    meta ? meta.textContent.replace(/\s+/g, ' ').slice(0, 80) : 'no meta'
  );
  check('S39 PC 单页：区块标题齐全', ['配置管理', '订阅生成', '访问统计', '外部订阅源'].every((t) => text.includes(t)));
  check(
    'S39 PC 单页：账号信息只在头像弹窗里',
    doc.querySelectorAll('.pp-account-card').length === 0 && !!doc.querySelector('.pp-avatar-btn'),
    'cards=' + doc.querySelectorAll('.pp-account-card').length
  );
  check('S39 PC 单页：一次性加载统计与订阅源', !!lastCall(/\/manage\/stats\//) && !!lastCall(/uuid-sub-sources/));
  check('S39 PC 单页：卡片可直接切换（有选中态）', doc.querySelectorAll('.pp-sub-card.is-active').length === 1);
}

/* S40 移动端页面拆分：我的订阅（含订阅生成/信息与邮箱）、配置管理（含订阅源）、访问统计 */
{
  const { doc } = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok' });
  await waitFor(() => doc.querySelector('.pp-sub-card'));
  await waitFor(() => doc.querySelector('.msub'));
  const text = visibleText(doc);
  check(
    'S40 移动端「我的订阅」为专用订阅生成页（无页签）',
    !!doc.querySelector('.msub') && !text.includes('信息与邮箱'),
    'msub=' + doc.querySelectorAll('.msub').length + ' | ' + text.slice(-200)
  );
  const navs = [...doc.querySelectorAll('.adm-tab-bar-item')].map((b) => b.textContent.trim());
  check(
    'S40 底部导航为 我的订阅 / Telegram / 配置管理 / 访问统计 / 我的',
    navs.length === 5 &&
      navs[0].includes('我的订阅') &&
      navs[1].includes('Telegram') &&
      navs[2].includes('配置管理') &&
      navs[3].includes('访问统计') &&
      navs[4].includes('我的'),
    JSON.stringify(navs)
  );

  const { doc: doc2 } = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok' });
  await waitFor(() => doc2.querySelector('.adm-tab-bar'));
  const tgTab = [...doc2.querySelectorAll('.adm-tab-bar-item')].find((b) => b.textContent.includes('Telegram'));
  click(tgTab);
  await waitFor(() => visibleText(doc2).includes('Telegram 代理'));
  check('S40 点底部 Telegram 进入独立 TG 代理页', visibleText(doc2).includes('Telegram 代理'), visibleText(doc2).slice(0, 200));

  const manageTab = [...doc2.querySelectorAll('.adm-tab-bar-item')].find((b) => b.textContent.includes('配置管理'));
  click(manageTab);
  await waitFor(() => doc2.querySelector('.mcfg') || doc2.querySelector('.mstat-loading'));
  check('S40 点底部「配置管理」进入配置页', !!doc2.querySelector('.mcfg'), 'mcfg=' + doc2.querySelectorAll('.mcfg').length);

  const { doc: doc3 } = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok' });
  await waitFor(() => doc3.querySelector('.adm-tab-bar'));
  click([...doc3.querySelectorAll('.adm-tab-bar-item')].find((b) => b.textContent.includes('访问统计')));
  await waitFor(() => doc3.querySelector('.mstat') || doc3.querySelector('.mstat-loading'));
  check('S40 点底部「访问统计」进入统计页', visibleText(doc3).includes('访问统计') || !!doc3.querySelector('.mstat'));
}

/* S41 移动端补齐：多 UUID 切换 / 本地二维码 / 搜索 / 协议筛选 / 外部订阅源 / 统计刷新 */
{
  const { window, doc } = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok' });
  await waitFor(() => doc.querySelector('.msub'));

  /* —— 多 UUID：选择器出现，切到第二个 —— */
  const picks = [...doc.querySelectorAll('.ppm-hero-chips .ppm-chip')];
  check('S41 多 UUID 显示切换 chip（mock 有两个）', picks.length === 2, 'chips=' + picks.length);
  const second = picks.find((p) => p.textContent.includes('alice'));
  check(
    'S41 chip 带备注/所有者与节点数',
    !!second && !!second.querySelector('.ppm-chip-count') && second.textContent.includes('3'),
    second ? second.textContent : 'no chip'
  );
  click(second);
  await sleep(220);
  click(btn(doc, '生成订阅'));
  const sub2 = await waitFor(() => lastCall(/\/sub\/22222222-2222-4222-8222-222222222222/), 3000);
  check('S41 切换 UUID 后订阅生成用新 UUID', !!sub2, calls.map((c) => c.url).slice(-3).join(' | '));

  /* —— 二维码改为本机 SVG 渲染，不再请求外部图片 —— */
  await waitFor(() => doc.querySelector('.msub-out'), 3000);
  click(btn(doc, '二维码'));
  const qr = await waitFor(() => doc.querySelector('.msub-qr svg') || doc.querySelector('.msub-qr .ant-qrcode'), 3000);
  check('S41 二维码本机渲染（无外网图片依赖）', !!qr && doc.querySelectorAll('.msub-qr img').length === 0,
    'qr=' + !!qr + ' popup=' + doc.querySelectorAll('.adm-popup').length + ' svg=' + doc.querySelectorAll('.msub-qr svg').length +
      ' body=' + (doc.querySelector('.msub-qr') ? doc.querySelector('.msub-qr').innerHTML.slice(0, 160) : 'no-node'));
  click(doc.querySelector('.adm-popup-mask'));
  await sleep(150);

  /* —— 访问统计：刷新会重新请求 —— */
  click([...doc.querySelectorAll('.adm-tab-bar-item')].find((b) => b.textContent.includes('访问统计')));
  await waitFor(() => doc.querySelector('.mstat'));
  const kpis = [...doc.querySelectorAll('.mstat-kpi')].map((k) => ({
    label: k.querySelector('.mstat-kpi-label')?.textContent || '',
    value: k.querySelector('.mstat-kpi-value')?.textContent || '',
  }));
  check(
    'S41 统计 KPI 取值正确（今日访问读 today_total）',
    (kpis.find((k) => k.label === '今日访问') || {}).value === '7' &&
      (kpis.find((k) => k.label === '总访问') || {}).value === '128',
    JSON.stringify(kpis)
  );
  const donutVal = doc.querySelector('.mstat-panel .pp-donut-value');
  check(
    'S41 环形图中心值不再被 fmtNum 弄成 0',
    !!donutVal && donutVal.textContent.replace(/\s/g, '') === '128',
    donutVal ? donutVal.textContent : 'none'
  );
  const before = calls.filter((c) => /\/manage\/stats\//.test(c.url)).length;
  click(btn(doc, '刷新'));
  const reloaded = await waitFor(() => calls.filter((c) => /\/manage\/stats\//.test(c.url)).length > before, 2500);
  check('S41 统计页「刷新」重新拉取数据', !!reloaded, 'before=' + before + ' after=' + calls.filter((c) => /\/manage\/stats\//.test(c.url)).length);

  /* —— 配置管理：协议筛选 + 搜索 + 外部订阅源弹层 —— */
  const m2 = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok' });
  await waitFor(() => m2.doc.querySelector('.adm-tab-bar'));
  click([...m2.doc.querySelectorAll('.adm-tab-bar-item')].find((b) => b.textContent.includes('配置管理')));
  await waitFor(() => m2.doc.querySelector('.mcfg'));

  const tabs = [...m2.doc.querySelectorAll('.adm-capsule-tabs-tab')];
  check('S41 协议筛选覆盖 VLESS/VMess/Trojan/SS/Hysteria2/TUIC/AnyTLS/Snell',
    ['VLESS', 'VMess', 'Trojan', 'SS', 'Hysteria2', 'TUIC', 'AnyTLS', 'Snell'].every((t) => tabs.some((x) => x.textContent.trim() === t)),
    tabs.map((t) => t.textContent.trim()).join(' | '));

  click(tabs.find((t) => t.textContent.trim() === 'VLESS'));
  const protoCall = await waitFor(() => lastCall(/\/manage\/configs\/[^?]+\?.*protocol=vless/), 3000);
  check('S41 协议筛选请求带 protocol=vless', !!protoCall, calls.map((c) => c.url).slice(-3).join(' | '));

  const search = m2.doc.querySelector('.mcfg-search input');
  setValue(m2.window, search, 'hk');
  const qCall = await waitFor(() => lastCall(/\/manage\/configs\/[^?]+\?.*q=hk/), 3000);
  check('S41 搜索输入自动带 q=hk（300ms 防抖）', !!qCall, calls.map((c) => c.url).slice(-3).join(' | '));

  click(m2.doc.querySelector('.mcfg-src'));
  const popup = await waitFor(() => m2.doc.querySelector('.msrc-body'), 2500);
  check('S41 外部订阅源弹层可打开', !!popup);
  check('S41 弹层展示已有订阅源', await waitFor(() => visibleText(m2.doc).includes('机场A'), 2000));

  const urlInput = m2.doc.querySelector('.msrc-add input[placeholder^="http"]');
  setValue(m2.window, urlInput, 'https://new.example.com/sub/x');
  await sleep(80);
  click([...m2.doc.querySelectorAll('.msrc-add button')].find((b) => b.textContent.includes('添加订阅源')));
  const addCall = await waitFor(() => lastCall(/\/manage\/uuid-sub-sources$/, 'POST'), 3000);
  check(
    'S41 新增外部订阅源 POST body（uuid + url）',
    !!addCall && addCall.body.url === 'https://new.example.com/sub/x' && !!addCall.body.uuid,
    JSON.stringify(addCall && addCall.body)
  );
  check('S41 新增后列表刷新出现新地址', await waitFor(() => visibleText(m2.doc).includes('new.example.com'), 2500));

  /* —— 账号与信息：UUID / 绑定邮箱 / 协议说明 / 退出 —— */
  const m3 = await mount({ path: '/', mode: 'portal', portalTok: 'portal-tok' });
  await waitFor(() => m3.doc.querySelector('.adm-tab-bar'));
  click([...m3.doc.querySelectorAll('.adm-tab-bar-item')].find((b) => b.textContent.trim() === '我的'));
  const acc = await waitFor(() => m3.doc.querySelector('.macc'), 2500);
  check('S41 「我的」页含 UUID 与绑定邮箱', !!acc && visibleText(m3.doc).includes('11111111-1111-4111-8111-111111111111') && visibleText(m3.doc).includes('me@example.com'));
  check('S41 「我的」页说明支持的协议', visibleText(m3.doc).includes('支持的协议'));
  check('S41 「我的」页有退出登录', !!btn(m3.doc, '退出登录'));
}

console.log(`\n结果：${pass} 通过，${fail} 失败\n`);
process.exit(fail ? 1 : 0);
