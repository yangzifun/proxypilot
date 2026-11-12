/* /client-auth 客户端授权页脚本（外部文件）。
 *
 * 强制 CSP 不允许可执行内联脚本：本文件由 script-src 'self' 放行。
 * 动态值（授权码 / Turnstile site key）从 DOM 读取，不再由服务端拼进脚本。
 */
(function () {
  var codeEl = document.getElementById('code');
  var CODE = codeEl ? codeEl.textContent.trim() : '';
  var tsEl = document.querySelector('.cf-turnstile');
  var SITE_KEY = tsEl ? (tsEl.getAttribute('data-sitekey') || '') : '';
  if (!SITE_KEY) {
    var box0 = document.getElementById('tsBox');
    if (box0) box0.style.display = 'none';
  }
  setTimeout(function () {
    if (SITE_KEY && !window.turnstile) {
      var box = document.getElementById('tsBox');
      if (box) {
        box.innerHTML = '<div style="color:#fb7185;font-size:12px;line-height:1.6">人机验证组件加载失败，请检查网络后刷新本页</div>';
      }
    }
  }, 5000);

  function turnstileToken() {
    var el = document.querySelector('[name="cf-turnstile-response"]');
    return el ? el.value : '';
  }
  function $(id) { return document.getElementById(id); }
  function say(t, cls) {
    var m = $('msg');
    if (!m) return;
    m.className = 'msg ' + (cls || '');
    m.textContent = t;
  }
  async function post(url, body, token) {
    var headers = Object.assign({ 'Content-Type': 'application/json' }, token ? { Authorization: 'Bearer ' + token } : {});
    var r = await fetch(url, { method: 'POST', headers: headers, body: JSON.stringify(body || {}) });
    var d = await r.json().catch(function () { return {}; });
    if (!r.ok) throw new Error(d.error || ('HTTP ' + r.status));
    return d;
  }

  var btnSend = $('btnSend');
  if (btnSend) {
    btnSend.onclick = async function () {
      var email = $('email').value.trim();
      if (!email) return say('请填写邮箱', 'err');
      btnSend.disabled = true;
      try {
        await post('/api/portal/send-code', { email: email, cf_turnstile_response: turnstileToken() });
        say('验证码已发送，请查收邮件', 'ok');
        $('step2').classList.remove('hide');
      } catch (e) { say('发送失败：' + e.message, 'err'); }
      btnSend.disabled = false;
    };
  }

  function showDone() {
    var card = document.querySelector('.card');
    if (!card) return;
    card.innerHTML = '<div class="brand"><div class="logo">✓</div><h1>授权成功</h1></div>'
      + '<p>客户端已获得登录凭证，可以关闭本页面回到客户端了。</p>';
  }

  var btnLogin = $('btnLogin');
  if (btnLogin) {
    btnLogin.onclick = async function () {
      var email = $('email').value.trim();
      var code = $('otp').value.trim();
      if (code.length !== 6) return say('请输入 6 位验证码', 'err');
      btnLogin.disabled = true;
      try {
        var login = await post('/api/portal/login', { email: email, code: code });
        await post('/api/client-auth/approve', { code: CODE }, login.token);
        try { localStorage.setItem('pp_portal_token', login.token); } catch (e) {}
        showDone();
      } catch (e) { say('授权失败：' + e.message, 'err'); btnLogin.disabled = false; }
    };
  }

  // 浏览器里已经登录过用户中心 → 直接授权，不再要求输入验证码
  (function autoApprove() {
    var tok = '';
    try { tok = localStorage.getItem('pp_portal_token') || ''; } catch (e) {}
    if (!tok) return;
    say('检测到已登录的账号，正在授权…');
    post('/api/client-auth/approve', { code: CODE }, tok)
      .then(showDone)
      .catch(function (e) {
        try { localStorage.removeItem('pp_portal_token'); } catch (err) {}
        say('当前登录状态已失效，请在下方重新验证邮箱：' + e.message, 'err');
      });
  })();
})();