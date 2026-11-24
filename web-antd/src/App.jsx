import React, { lazy, Suspense, useEffect, useState } from 'react';
import { Button, Result, Spin } from 'antd';
import { useAuth } from './auth';
import { useHashRoute } from './hooks';
import { PAGE_META } from './nav';
import AppLayout from './shell/AppLayout';
import PageErrorBoundary from './components/PageErrorBoundary';

import Login from './pages/Login';
import Dashboard from './pages/Dashboard';

// 路由级按需加载：重页面拆到独立 chunk，首屏只保留 Dashboard / Login / AppLayout。
// esbuild splitting 会为每个 lazy import 生成 chunks/*.js；加载失败由 PageErrorBoundary 兜底提示。
const Groups = lazy(() => import('./pages/Groups'));
const Templates = lazy(() => import('./pages/Templates'));
const ServersUnified = lazy(() => import('./pages/ServersUnified'));
const Nodes = lazy(() => import('./pages/Nodes'));
const UserTraffic = lazy(() => import('./pages/UserTraffic'));
const MihomoPage = lazy(() => import('./pages/Mihomo'));
const NodeBindings = lazy(() => import('./pages/NodeBindings'));
const TemplateHub = lazy(() => import('./pages/TemplateHub'));
const MtprotoPage = lazy(() => import('./pages/MtprotoPage'));
const MasquePage = lazy(() => import('./pages/MasquePage'));
const VpnPage = lazy(() => import('./pages/VpnPage'));
const CdnPreferred = lazy(() => import('./pages/CdnPreferred'));
const Logs = lazy(() => import('./pages/Logs'));
const AdminUsersPage = lazy(() => import('./pages/System').then((m) => ({ default: m.AdminUsersPage })));
const SystemSettingsPage = lazy(() => import('./pages/System').then((m) => ({ default: m.SystemSettingsPage })));
const LocalServer = lazy(() => import('./pages/LocalServer'));
const Security = lazy(() => import('./pages/Security'));
const Certs = lazy(() => import('./pages/Certs'));
const Backup = lazy(() => import('./pages/Backup'));

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

