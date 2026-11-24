import React, { Suspense, useEffect, useState } from 'react';
import { Button, Result, Spin } from 'antd';
import { useAuth } from './auth';
import { useHashRoute } from './hooks';
import { PAGE_META } from './nav';
import AppLayout from './shell/AppLayout';
import PageErrorBoundary from './components/PageErrorBoundary';
import { lazyWithRetry } from './lazyRoute';

import Login from './pages/Login';
import Dashboard from './pages/Dashboard';

// 路由级按需加载：重页面拆到独立 chunk，首屏只保留 Dashboard / Login / AppLayout。
// esbuild splitting 会为每个 lazy import 生成 chunks/*.js；加载失败由 PageErrorBoundary 兜底提示。
// 常用页面 loader 复用同一构造函数（esbuild 保证同一动态 import 指向同一 chunk）
const loadGroups = () => import('./pages/Groups');
const loadTemplates = () => import('./pages/Templates');
const loadServersUnified = () => import('./pages/ServersUnified');
const loadUserTraffic = () => import('./pages/UserTraffic');
const loadSystem = () => import('./pages/System');

const Groups = lazyWithRetry(loadGroups);
const Templates = lazyWithRetry(loadTemplates);
const ServersUnified = lazyWithRetry(loadServersUnified);
const Nodes = lazyWithRetry(() => import('./pages/Nodes'));
const UserTraffic = lazyWithRetry(loadUserTraffic);
const MihomoPage = lazyWithRetry(() => import('./pages/Mihomo'));
const NodeBindings = lazyWithRetry(() => import('./pages/NodeBindings'));
const TemplateHub = lazyWithRetry(() => import('./pages/TemplateHub'));
const MtprotoPage = lazyWithRetry(() => import('./pages/MtprotoPage'));
const MasquePage = lazyWithRetry(() => import('./pages/MasquePage'));
const VpnPage = lazyWithRetry(() => import('./pages/VpnPage'));
const CdnPreferred = lazyWithRetry(() => import('./pages/CdnPreferred'));
const Logs = lazyWithRetry(() => import('./pages/Logs'));
const AdminUsersPage = lazyWithRetry(() => loadSystem().then((m) => ({ default: m.AdminUsersPage })));
const SystemSettingsPage = lazyWithRetry(() => loadSystem().then((m) => ({ default: m.SystemSettingsPage })));
const LocalServer = lazyWithRetry(() => import('./pages/LocalServer'));
const Security = lazyWithRetry(() => import('./pages/Security'));
const Certs = lazyWithRetry(() => import('./pages/Certs'));
const Backup = lazyWithRetry(() => import('./pages/Backup'));

// 首屏后的空闲预取列表：常用页面 chunk（失败静默，不阻塞首屏）
const PREFETCH_LOADERS = [loadGroups, loadTemplates, loadServersUnified, loadUserTraffic, loadSystem];

/** 空闲预取常用路由 chunk；返回 Promise（失败项为 null），供测试/调试调用。 */
export function prefetchCommonPages() {
  return Promise.all(PREFETCH_LOADERS.map((load) => Promise.resolve().then(load).catch(() => null)));
}

const PAGES = {
  dash: Dashboard,
  uuids: Groups,
  tpls: Templates,
  userst: UserTraffic,
  servers: ServersUnified,
  normal: Nodes,
  mihomo: MihomoPage,
  bindings: NodeBindings,
  tplhub: TemplateHub,
  mtproto: MtprotoPage,
  masque: MasquePage,
  vpn: VpnPage,
  cdnpref: CdnPreferred,
  singbox: Nodes,
  status: ServersUnified,
  logs: Logs,
  sys: AdminUsersPage,
  local: LocalServer,
  sysusers: AdminUsersPage,
  syssettings: SystemSettingsPage,
  backup: Backup,
  certs: Certs,
  sec: Security,
};

export default function App() {
  const { token, isSuper, superAdmin } = useAuth();
  const [route, navigate] = useHashRoute('dash');
  const [routeLoading, setRouteLoading] = useState(false);

  useEffect(() => {
    setRouteLoading(true);
    const timer = setTimeout(() => setRouteLoading(false), 360);
    return () => clearTimeout(timer);
  }, [route]);

  // 已登录后空闲预取常用路由 chunk：requestIdleCallback（fallback setTimeout 2500ms），失败静默。
  useEffect(() => {
    if (!token) return undefined;
    let idleId = null;
    let timer = null;
    const run = () => {
      prefetchCommonPages();
    };
    try {
      if (typeof window !== 'undefined' && typeof window.requestIdleCallback === 'function') {
        idleId = window.requestIdleCallback(run, { timeout: 4000 });
      } else {
        timer = setTimeout(run, 2500);
      }
    } catch (e) {
      timer = setTimeout(run, 2500);
    }
    return () => {
      try {
        if (idleId != null && typeof window.cancelIdleCallback === 'function') window.cancelIdleCallback(idleId);
      } catch (e) {
        /* ignore */
      }
      if (timer) clearTimeout(timer);
    };
  }, [token]);

  // 主题由 main.jsx 的 ThemeProvider 统一提供（单一 ConfigProvider），App 不再重复包裹。
  if (!token) return <Login />;

  const legacySettings = route === 'settings';
  const normalized = route === 'singbox' ? 'normal' : route === 'status' ? 'servers' : legacySettings ? 'syssettings' : route;
  const key = PAGE_META[normalized] ? normalized : 'dash';
  const meta = PAGE_META[key];
  const PageComp = PAGES[key] || Dashboard;

  let body;
  if (meta.superOnly && !isSuper) {
    body = (
      <Result
        status="403"
        title="仅超级管理员可访问此页面"
        subTitle={`该页面只对「${superAdmin}」这类超级管理员账号开放；其余管理后台功能对普通管理员均可用。`}
        extra={
          <Button type="primary" onClick={() => navigate('dash')}>
            返回系统概览
          </Button>
        }
      />
    );
  } else {
    body = <PageComp initialTab={legacySettings ? 'settings' : undefined} />;
  }

  // 全站主题：main.jsx 的 ThemeProvider 已按当前主题生成唯一 ConfigProvider（token+components）。
  // 路由内容包在错误边界内：页面渲染异常时显示错误页而不是白屏；
  // key=route 使切换页面时自动重置错误状态。
  return (
    <AppLayout activeKey={key} onNavigate={navigate}>
      {routeLoading ? <div className="pp-route-progress" aria-hidden="true"><span /></div> : null}
      <PageErrorBoundary key={key} pageKey={key} onHome={() => navigate('dash')}>
        <Suspense
          fallback={(
            <div className="pp-route-loading" style={{ padding: '48px 0', textAlign: 'center' }}>
              <Spin />
            </div>
          )}
        >
          <div className="pp-page-enter" key={key}>
            {body}
          </div>
        </Suspense>
      </PageErrorBoundary>
    </AppLayout>
  );
}

