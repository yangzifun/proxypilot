import React, { useEffect, useState } from 'react';
import { applyTheme, getTheme, subscribeTheme, THEME_TOKENS } from './theme';
import { ConfigProvider } from 'antd';
import { Button, Result } from 'antd';
import { useAuth } from './auth';
import { useHashRoute } from './hooks';
import { PAGE_META } from './nav';
import AppLayout from './shell/AppLayout';

import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import Groups from './pages/Groups';
import Templates from './pages/Templates';
import ServersUnified from './pages/ServersUnified';
import Nodes from './pages/Nodes';
import UserTraffic from './pages/UserTraffic';
import MihomoPage from './pages/Mihomo';
import NodeBindings from './pages/NodeBindings';
import TemplateHub from './pages/TemplateHub';
import MtprotoPage from './pages/MtprotoPage';
import MasquePage from './pages/MasquePage';
import VpnPage from './pages/VpnPage';
import CdnPreferred from './pages/CdnPreferred';
import Logs from './pages/Logs';
import System, { AdminUsersPage, SystemSettingsPage } from './pages/System';
import LocalServer from './pages/LocalServer';
import Security from './pages/Security';
import Certs from './pages/Certs';
import Backup from './pages/Backup';
import Attachments from './pages/Attachments';

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
  attachments: Attachments,
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

  // 全站主题：启动时应用并订阅变化
  const [siteTheme, setSiteTheme] = useState(() => getTheme());
  useEffect(() => {
    applyTheme(getTheme());
    return subscribeTheme((t) => { setSiteTheme(t); applyTheme(t); });
  }, []);

  const themeTokens = THEME_TOKENS[siteTheme] || THEME_TOKENS.light;

  return (
    <ConfigProvider theme={{ token: themeTokens }}>
    <AppLayout activeKey={key} onNavigate={navigate}>
      {routeLoading ? <div className="pp-route-progress" aria-hidden="true"><span /></div> : null}
      <div className="pp-page-enter" key={key}>
        {body}
      </div>
    </AppLayout>
    </ConfigProvider>
  );
}

