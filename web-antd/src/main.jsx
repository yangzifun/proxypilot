import React from 'react';
import { createRoot } from 'react-dom/client';
import { ConfigProvider, App as AntdApp } from 'antd';
import zhCNraw from 'antd/locale/zh_CN';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';

import { themeConfig } from './theme';
import { AuthProvider } from './auth';
import App from './App';

// three.js 单独包复用主包的 React，避免重复打包
window.__PP_REACT__ = React;
import { applyTheme, getTheme } from './theme';
import { applyScale, getScale } from './uiScale';

applyTheme(getTheme());
applyScale(getScale());
import PortalApp from './portal/PortalApp';
import './styles.css';

dayjs.locale('zh-cn');

/**
 * antd 的 locale 在 CJS→ESM 打包后可能多包一层 default（此时 locale.Modal 为空，
 * Modal 的取消/确定按钮会退回英文 OK/Cancel）。这里兼容两种形态。
 */
const zhCN = zhCNraw && zhCNraw.default && zhCNraw.default.Modal ? zhCNraw.default : zhCNraw;

/**
 * 版本兜底：服务端会在 <meta name="pp-version"> 与 __PP_BOOT__ 里注入真实版本。
 * 这里不再写死任何版本号，避免内置版本与发布版本不一致（曾出现加载页显示旧版本）。
 */
const ppVersionMeta = document.querySelector('meta[name="pp-version"]');
const ppVersionFallback = (ppVersionMeta && ppVersionMeta.getAttribute('content')) || '';
window.__PP_BOOT__ = window.__PP_BOOT__ || { superAdmin: 'admin', version: ppVersionFallback, flavor: 'antd' };
if (window.__PP_BOOT__ && !window.__PP_BOOT__.version) window.__PP_BOOT__.version = ppVersionFallback;

/**
 * OIDC 回调成功后 worker.js 会重定向到 `/admin#token=<jwt>`。
 * 这里必须在 AuthProvider 读 token 之前把 hash 里的 JWT 落到 localStorage，
 * 并把 hash 改回 #dash —— 否则它会被哈希路由当成一个未知页面。
 */
(function consumeOidcToken() {
  const hash = String(window.location.hash || '');
  if (hash.indexOf('#token=') !== 0) return;
  try {
    const token = decodeURIComponent(hash.slice('#token='.length)).trim();
    if (token) {
      localStorage.setItem('token', token);
      localStorage.setItem('token_exp', String(Date.now() + 24 * 3600 * 1000));
    }
  } catch (e) {
    /* localStorage 不可用（隐私模式）时只能放弃 */
  }
  try {
    window.history.replaceState(null, '', window.location.pathname + window.location.search + '#dash');
  } catch (e) {
    window.location.hash = '#dash';
  }
})();

/* 用户中心与后台共用同一个 bundle：由外壳注入的 mode（或路径）决定挂哪个应用 */
const IS_PORTAL =
  (window.__PP_BOOT__ && window.__PP_BOOT__.mode === 'portal') || /^\/(me\/?)?$/.test(window.location.pathname);

createRoot(document.getElementById('root')).render(
  <ConfigProvider locale={zhCN} theme={themeConfig} button={{ autoInsertSpace: false }}>
    <AntdApp>
      {IS_PORTAL ? (
        <PortalApp />
      ) : (
        <AuthProvider>
          <App />
        </AuthProvider>
      )}
    </AntdApp>
  </ConfigProvider>
);
