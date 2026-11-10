package api

import "net/http"

const settingsPageHTML = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>系统设置 · ProxyPilot</title>
<style>
:root{--bd:#e2e8f0;--muted:#64748b;--pri:#2563eb}
*{box-sizing:border-box}body{margin:0;background:#f8fafc;font:14px/1.6 -apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#0f172a}
.wrap{max-width:880px;margin:0 auto;padding:24px 16px 64px}
.head{display:flex;align-items:center;justify-content:space-between;margin-bottom:16px}
h1{font-size:20px;margin:0}.muted{color:var(--muted);font-size:12px}
.card{background:#fff;border:1px solid var(--bd);border-radius:12px;padding:18px;margin-bottom:14px}
.card h2{font-size:15px;margin:0 0 12px}
.row{display:flex;gap:12px;align-items:center;margin:10px 0}
.row label{width:220px;color:#334155}
.row input[type=text],.row input[type=password],.row input[type=url]{flex:1;padding:8px 10px;border:1px solid var(--bd);border-radius:8px;font:inherit}
.row .hint{color:var(--muted);font-size:12px}
.checks{display:flex;gap:18px;flex-wrap:wrap}
.checks label{display:flex;gap:6px;align-items:center;width:auto;color:#0f172a}
button{padding:8px 16px;border-radius:8px;border:1px solid var(--bd);background:#fff;cursor:pointer;font:inherit}
button.primary{background:var(--pri);border-color:var(--pri);color:#fff}
button:disabled{opacity:.6;cursor:not-allowed}
#msg{margin:12px 0;font-size:13px}.ok{color:#047857}.err{color:#b91c1c}
a{color:var(--pri);text-decoration:none}
</style></head>
<body><div class="wrap">
<div class="head"><div><h1>系统设置</h1><div class="muted">登录方式 / 人机验证 / 邮件 / OIDC / 站点地址（保存后即时生效，30 秒内全量刷新）</div></div>
  <div><a href="/admin">← 返回后台</a></div></div>
<div id="msg"></div>

<div class="card"><h2>登录方式</h2>
<div class="checks">
 <label><input type="checkbox" data-bool="auth_password_enabled"> 密码登录</label>
 <label><input type="checkbox" data-bool="auth_email_code_enabled"> 邮箱验证码登录</label>
 <label><input type="checkbox" data-bool="auth_oidc_enabled"> OIDC 单点登录</label>
</div>
<div class="muted" id="eff" style="margin-top:8px"></div>
</div>

<div class="card"><h2>人机验证（Turnstile）</h2>
<div class="row"><label>Site Key</label><input type="text" data-key="turnstile_site_key" placeholder="前端 site key"></div>
<div class="row"><label>Secret Key</label><input type="password" data-key="turnstile_secret" data-secret="1" placeholder="留空保持不变"></div>
<div class="muted">Secret 留空表示不修改；填入 __clear__ 可清空（回落环境变量/关闭）。</div>
</div>

<div class="card"><h2>发信通道</h2>
<div class="row"><label>通道</label><input type="text" data-key="mail_provider" placeholder="auto / smtp / cloudflare"></div>
<div class="row"><label>Cloudflare API Token</label><input type="password" data-key="cloudflare_api_token" data-secret="1" placeholder="留空保持不变"></div>
<div class="row"><label>Cloudflare Account ID</label><input type="text" data-key="cloudflare_account_id" placeholder="9ef942805abcaef4dbf6574131453c58"></div>
</div>

<div class="card"><h2>邮件（SMTP）</h2>
<div class="row"><label>SMTP 地址</label><input type="text" data-key="smtp_addr" placeholder="smtp.example.com:587"></div>
<div class="row"><label>用户名</label><input type="text" data-key="smtp_user" placeholder="notice@example.com"></div>
<div class="row"><label>密码</label><input type="password" data-key="smtp_pass" data-secret="1" placeholder="留空保持不变"></div>
<div class="row"><label>发件人</label><input type="text" data-key="mail_from" placeholder="notice@example.com"></div>
</div>

<div class="card"><h2>OIDC</h2>
<div class="row"><label>Issuer</label><input type="url" data-key="oidc_issuer" placeholder="https://id.example.com"></div>
<div class="row"><label>Client ID</label><input type="text" data-key="oidc_client_id"></div>
<div class="row"><label>Client Secret</label><input type="password" data-key="oidc_client_secret" data-secret="1" placeholder="留空保持不变"></div>
<div class="row"><label>Scopes</label><input type="text" data-key="oidc_scopes" placeholder="openid profile email"></div>
<div class="row"><label>显示名称</label><input type="text" data-key="oidc_provider_name" placeholder="例如 企业 SSO"></div>
<div class="row"><label>按钮文案</label><input type="text" data-key="oidc_button_text"></div>
<div class="row"><label>回调地址</label><input type="url" data-key="oidc_redirect_uri" placeholder="https://pp.example.com/api/oidc/callback"></div>
</div>

<div class="card"><h2>通知 / 巡检 / 保留</h2>
<div class="checks">
 <label><input type="checkbox" data-bool="monitor_notify_enabled"> 服务器离线邮件通知</label>
</div>
<div class="row"><label>收件邮箱</label><input type="text" data-key="monitor_notify_emails" placeholder="a@example.com,b@example.com"></div>
<div class="row"><label>通知冷却（分钟）</label><input type="text" data-key="monitor_notify_cooldown_min" placeholder="10"></div>
<div class="row"><label>UUID 巡检间隔（分钟）</label><input type="text" data-key="uuid_reconcile_interval_min" placeholder="30"></div>
<div class="row"><label>批量任务保留（天）</label><input type="text" data-key="batch_retention_days" placeholder="7"></div>
<div class="row"><label>Agent 任务保留（天）</label><input type="text" data-key="agent_task_retention_days" placeholder="7"></div>
<div class="row"><label>监控指标保留（天）</label><input type="text" data-key="metrics_retention_days" placeholder="7"></div>
</div>

<div class="card"><h2>站点</h2>
<div class="row"><label>对外地址</label><input type="url" data-key="site_public_base_url" placeholder="https://pp.example.com"></div>
<div class="muted">用于 Agent 分发地址与 OIDC 回调推导；反代后建议填写。</div>
</div>

<div style="display:flex;gap:10px"><button class="primary" id="save">保存设置</button><button id="reload">重新加载</button></div>
</div>
<script>
(function(){
  var token = localStorage.getItem('token') || '';
  var msg = document.getElementById('msg');
  function show(text, ok){ msg.textContent = text || ''; msg.className = ok ? 'ok' : 'err'; }
  function auth(){ return token ? {'Authorization':'Bearer '+token} : {}; }
  function needAuth(){
    if (token) return true;
    var t = prompt('请输入管理员 token（登录后台后 localStorage.token）');
    if (!t) return false;
    token = t.trim(); localStorage.setItem('token', token); return true;
  }
  function load(){
    fetch('/api/settings', {headers: auth()}).then(function(r){
      if (r.status === 401) { show('未授权：请先登录后台，或提供 token', false); throw new Error('401'); }
      return r.json();
    }).then(function(j){
      if (!j.success) throw new Error(j.error || '加载失败');
      document.querySelectorAll('[data-key]').forEach(function(el){
        var k = el.getAttribute('data-key');
        if (el.getAttribute('data-secret') === '1') {
          el.value = ''; el.placeholder = (j.has && j.has[k]) ? '已配置（留空保持不变）' : '未配置';
        } else {
          el.value = (j.values && j.values[k]) || '';
        }
      });
      document.querySelectorAll('[data-bool]').forEach(function(el){
        var k = el.getAttribute('data-bool');
        el.checked = String((j.values && j.values[k]) || '') === '1';
      });
      var e = j.effective || {};
      document.getElementById('eff').textContent =
        '当前：密码 ' + (e.password_enabled?'开':'关') + ' · 邮箱 ' + (e.email_code_enabled?'开':'关') +
        ' · OIDC ' + (e.oidc_enabled?'开':'关') + ' · 邮件 ' + (e.mail_configured?'已配置':'未配置') +
        ' · 人机验证 ' + (e.turnstile_required?'开':'关');
      show('', true);
    }).catch(function(err){ if (err.message !== '401') show(err.message || '加载失败', false); });
  }
  function save(){
    var body = {};
    document.querySelectorAll('[data-key]').forEach(function(el){
      var k = el.getAttribute('data-key'), v = el.value.trim();
      if (el.getAttribute('data-secret') === '1' && v === '') return;
      body[k] = v;
    });
    document.querySelectorAll('[data-bool]').forEach(function(el){
      body[el.getAttribute('data-bool')] = el.checked ? '1' : '0';
    });
    var btn = document.getElementById('save'); btn.disabled = true;
    fetch('/api/settings', {method:'PUT', headers: Object.assign({'Content-Type':'application/json'}, auth()), body: JSON.stringify(body)})
      .then(function(r){ return r.json().then(function(j){ return {status:r.status, j:j}; }); })
      .then(function(x){
        if (x.status >= 400) throw new Error((x.j && x.j.error) || ('保存失败 HTTP ' + x.status));
        show('已保存，配置已生效', true); load();
      }).catch(function(err){ show(err.message || '保存失败', false); })
      .finally(function(){ btn.disabled = false; });
  }
  document.getElementById('save').onclick = function(){ if (needAuth()) save(); };
  document.getElementById('reload').onclick = function(){ if (needAuth()) load(); };
  if (needAuth()) load();
})();
</script>
</body></html>`

// GET /admin/settings
func (h *handlers) settingsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(settingsPageHTML))
}
