/* /admin 与 /（门户）外壳的引导脚本（外部文件）。
 *
 * 背景（2026-11-12 白屏事故）：V1.2 使用「每请求 nonce」的强制 CSP，但部署前面有
 * Cloudflare 与缓存，HTML 里的 nonce 与 CSP 响应头的 nonce 可能来自不同响应，导致
 * 全部脚本被拦、后台白屏。现改为：
 *   1) 动态数据用 <script type="application/json" id="pp-boot"> 数据块下发（不执行）；
 *   2) 所有可执行脚本都是同源外部文件（script-src 'self' 放行），不再使用 nonce；
 *   3) CSP 对所有请求完全相同，缓存/代理拆分响应也不会再拦掉脚本。
 *
 * 本文件职责：解析 #pp-boot 数据块 -> window.__PP_BOOT__，并负责首屏加载失败提示。
 * 不要改成内联脚本：强制 CSP 不允许可执行内联脚本。
 */
(function () {
  try {
    var el = document.getElementById('pp-boot');
    if (el && el.textContent) {
      var data = JSON.parse(el.textContent);
      if (data && typeof data === 'object') window.__PP_BOOT__ = data;
    }
  } catch (e) {
    /* 数据块损坏时保持 window.__PP_BOOT__ 原值（main.jsx 还有兜底解析） */
  }
  if (!window.__PP_BOOT__) window.__PP_BOOT__ = {};

  var retry = document.getElementById('pp-boot-retry');
  function showRetry(message) {
    if (!retry || !document.querySelector('.pp-boot')) return;
    if (message) retry.innerHTML = message + '，<a href="" id="pp-reload-link">刷新页面</a>';
    retry.hidden = false;
  }
  // 强制 CSP 不允许 onclick 内联事件处理器，这里用事件委托实现「刷新页面」。
  document.addEventListener('click', function (e) {
    var node = e.target;
    while (node && node !== document) {
      if (node.id === 'pp-reload-link') {
        e.preventDefault();
        location.reload();
        return;
      }
      node = node.parentNode;
    }
  });
  window.setTimeout(function () { showRetry('加载时间较长'); }, 12000);
  window.addEventListener('error', function () { showRetry('资源加载失败'); }, true);
})();