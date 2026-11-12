/* /admin/certs 证书页脚本（外部文件）。
 *
 * 强制 CSP 不允许可执行内联脚本：本文件由 script-src 'self' 放行。
 * 页面 HTML 只保留结构与文案，逻辑全部在这里。
 */
(function () {
  var token = localStorage.getItem('token') || '';
  var $ = function (id) { return document.getElementById(id); };
  function setMsg(text, cls) {
    var m = $('msg');
    if (!m) return;
    m.className = 'msg ' + (cls || '');
    m.textContent = text;
  }
  function auth() { return token ? { Authorization: 'Bearer ' + token } : {}; }
  function fmtTime(ms) { return ms ? new Date(Number(ms)).toLocaleString() : '—'; }

  async function load() {
    try {
      var r = await fetch('/api/admin/vpn/cert', { headers: auth() });
      if (r.status === 401 || r.status === 403) {
        setMsg('未登录管理后台，请先在 /admin 登录后再打开本页', 'err');
        return;
      }
      var d = await r.json();
      if (!d.cert) {
        var domains = $('domains');
        if (domains) domains.textContent = '还没有证书';
        return;
      }
      if ($('domains')) $('domains').textContent = d.cert.domains;
      if ($('source')) $('source').textContent = d.cert.source;
      if ($('notAfter')) $('notAfter').textContent = fmtTime(d.cert.not_after);
      if ($('fp')) $('fp').textContent = d.cert.fingerprint;
      if (!d.has_cloudflare_token) setMsg('提示：尚未配置 Cloudflare API Token（系统设置 → 邮件/集成），无法签发', 'err');
    } catch (e) { setMsg('加载失败：' + e.message, 'err'); }
  }

  var btnIssue = $('btnIssue');
  if (btnIssue) {
    btnIssue.onclick = async function () {
      btnIssue.disabled = true;
      setMsg('正在申请（DNS-01 验证通常 30–90 秒）…');
      try {
        var r = await fetch('/api/admin/vpn/cert/issue', {
          method: 'POST',
          headers: Object.assign({ 'Content-Type': 'application/json' }, auth()),
          body: '{}',
        });
        var d = await r.json();
        if (!r.ok) setMsg('签发失败：' + (d.error || r.status), 'err');
        else setMsg('签发成功：' + d.domains + '，到期 ' + fmtTime(d.not_after), 'ok');
      } catch (e) { setMsg('签发失败：' + e.message, 'err'); }
      btnIssue.disabled = false;
      load();
    };
  }

  var btnDist = $('btnDist');
  if (btnDist) {
    btnDist.onclick = async function () {
      btnDist.disabled = true;
      setMsg('正在下发…');
      try {
        var r = await fetch('/api/admin/vpn/cert/distribute', { method: 'POST', headers: auth() });
        var d = await r.json();
        if (!r.ok) {
          setMsg('下发失败：' + (d.error || r.status), 'err');
        } else {
          var rows = (d.results || []).map(function (x) {
            return '<tr><td>' + x.target + '</td><td class="' + (x.ok ? 'ok' : 'err') + '">' + (x.ok ? '已排队' : '失败') +
              '</td><td><code>' + (x.error || ('task#' + (x.task_id || '-'))) + '</code></td></tr>';
          }).join('');
          var table = $('distTable');
          if (table) {
            table.querySelectorAll('tr:not(:first-child)').forEach(function (e) { e.remove(); });
            table.insertAdjacentHTML('beforeend', rows);
          }
          setMsg('已下发 ' + d.queued + ' 个目标', 'ok');
        }
      } catch (e) { setMsg('下发失败：' + e.message, 'err'); }
      btnDist.disabled = false;
    };
  }

  load();
})();