/* /admin/settings 设置页脚本（外部文件）。
 *
 * 强制 CSP 不允许可执行内联脚本：本文件由 script-src 'self' 放行。
 * 页面 HTML 只保留结构与数据属性（data-key/data-bool），逻辑全部在这里。
 */
(function () {
  var token = localStorage.getItem('token') || '';
  var msg = document.getElementById('msg');
  function show(text, ok) {
    if (!msg) return;
    msg.textContent = text || '';
    msg.className = ok ? 'ok' : 'err';
  }
  function auth() { return token ? { Authorization: 'Bearer ' + token } : {}; }
  function needAuth() {
    if (token) return true;
    var t = prompt('请输入管理员 token（登录后台后 localStorage.token）');
    if (!t) return false;
    token = t.trim();
    localStorage.setItem('token', token);
    return true;
  }
  function load() {
    fetch('/api/settings', { headers: auth() }).then(function (r) {
      if (r.status === 401) { show('未授权：请先登录后台，或提供 token', false); throw new Error('401'); }
      return r.json();
    }).then(function (j) {
      if (!j.success) throw new Error(j.error || '加载失败');
      document.querySelectorAll('[data-key]').forEach(function (el) {
        var k = el.getAttribute('data-key');
        if (el.getAttribute('data-secret') === '1') {
          el.value = '';
          el.placeholder = (j.has && j.has[k]) ? '已配置（留空保持不变）' : '未配置';
        } else {
          el.value = (j.values && j.values[k]) || '';
        }
      });
      document.querySelectorAll('[data-bool]').forEach(function (el) {
        var k = el.getAttribute('data-bool');
        el.checked = String((j.values && j.values[k]) || '') === '1';
      });
      var e = j.effective || {};
      var eff = document.getElementById('eff');
      if (eff) {
        eff.textContent =
          '当前：密码 ' + (e.password_enabled ? '开' : '关') + ' · 邮箱 ' + (e.email_code_enabled ? '开' : '关') +
          ' · OIDC ' + (e.oidc_enabled ? '开' : '关') + ' · 邮件 ' + (e.mail_configured ? '已配置' : '未配置') +
          ' · 人机验证 ' + (e.turnstile_required ? '开' : '关');
      }
      show('', true);
    }).catch(function (err) { if (err.message !== '401') show(err.message || '加载失败', false); });
  }
  function save() {
    var body = {};
    document.querySelectorAll('[data-key]').forEach(function (el) {
      var k = el.getAttribute('data-key'), v = el.value.trim();
      if (el.getAttribute('data-secret') === '1' && v === '') return;
      body[k] = v;
    });
    document.querySelectorAll('[data-bool]').forEach(function (el) {
      body[el.getAttribute('data-bool')] = el.checked ? '1' : '0';
    });
    var btn = document.getElementById('save');
    if (btn) btn.disabled = true;
    fetch('/api/settings', {
      method: 'PUT',
      headers: Object.assign({ 'Content-Type': 'application/json' }, auth()),
      body: JSON.stringify(body),
    })
      .then(function (r) { return r.json().then(function (j) { return { status: r.status, j: j }; }); })
      .then(function (x) {
        if (x.status >= 400) throw new Error((x.j && x.j.error) || ('保存失败 HTTP ' + x.status));
        show('已保存，配置已生效', true);
        load();
      }).catch(function (err) { show(err.message || '保存失败', false); })
      .finally(function () { if (btn) btn.disabled = false; });
  }
  var saveBtn = document.getElementById('save');
  var reloadBtn = document.getElementById('reload');
  if (saveBtn) saveBtn.onclick = function () { if (needAuth()) save(); };
  if (reloadBtn) reloadBtn.onclick = function () { if (needAuth()) load(); };
  if (needAuth()) load();
})();