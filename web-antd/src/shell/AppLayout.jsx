import React, { useEffect, useMemo, useState } from 'react';
import { Segmented, Tooltip } from 'antd';
import { getTheme, setTheme, subscribeTheme, THEME_OPTIONS } from '../theme';
import { SCALE_OPTIONS, useScale } from '../uiScale';
import { Avatar, Badge, Dropdown, Layout, Menu, Tag } from 'antd';
import { LogoutOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons';
import { useAuth } from '../auth';
import { menuItems, PAGE_META, NAV_GROUPS, groupOf } from '../nav';
import { LOGO_URL } from '../theme';
import JobCenter from '../components/JobCenter';

const { Sider, Header, Content } = Layout;

/* 版本号以服务端注入的 boot 为准，避免前端硬编码落后于后端 */
const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};

export default function AppLayout({ activeKey, onNavigate, children }) {
  const [siteTheme, setSiteTheme] = useState(() => getTheme());
  useEffect(() => subscribeTheme((v) => setSiteTheme(v)), []);
  const [uiScale, setUiScale] = useScale();
  const { user, isSuper, logout } = useAuth();
  const meta = PAGE_META[activeKey] || PAGE_META.dash;

  const items = useMemo(() => menuItems(isSuper), [isSuper]);

  // 侧边栏折叠（窄屏自动折叠）
  const [collapsed, setCollapsed] = useState(false);
  useEffect(() => {
    const apply = () => setCollapsed(window.innerWidth < 1100);
    apply();
    window.addEventListener('resize', apply);
    return () => window.removeEventListener('resize', apply);
  }, []);

  // 展开的分组：记住用户选择，并始终包含当前页面所在分组
  const OPEN_KEY = 'pp.nav.openGroups';
  const [openKeys, setOpenKeys] = useState(() => {
    try {
      const saved = JSON.parse(localStorage.getItem(OPEN_KEY) || 'null');
      if (Array.isArray(saved)) return saved;
    } catch (e) { /* ignore */ }
    return NAV_GROUPS.map((g) => g.key);
  });
  useEffect(() => {
    const g = groupOf(activeKey);
    if (g) setOpenKeys((prev) => (prev.includes(g) ? prev : [...prev, g]));
  }, [activeKey]);
  const onOpenChange = (keys) => {
    setOpenKeys(keys);
    try {
      localStorage.setItem(OPEN_KEY, JSON.stringify(keys));
    } catch (e) { /* ignore */ }
  };

  const userMenu = {
    items: [
      { key: 'sec', icon: <SafetyCertificateOutlined />, label: '安全中心' },
      { type: 'divider' },
      { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'logout') logout();
      else if (key === 'sec') onNavigate('sec');
    },
  };

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider className="pp-sider" theme="dark" width={224}>
        <div className="pp-brand">
          <div className="pp-brand-mark">
            <img
              src={LOGO_URL}
              alt="ProxyPilot"
              onError={(e) => {
                e.currentTarget.onerror = null;
                e.currentTarget.src = '/favicon.svg';
              }}
            />
          </div>
          <div className="pp-brand-text">
            <div className="pp-brand-title">ProxyPilot</div>
            <div className="pp-brand-sub">控制台 · Ant Design</div>
          </div>
        </div>
        <div className="pp-sider-divider" />
        <Menu
          className="pp-nav"
          mode="inline"
          theme="dark"
          items={items}
          selectedKeys={[activeKey]}
          inlineCollapsed={collapsed}
          openKeys={collapsed ? undefined : openKeys}
          onOpenChange={collapsed ? undefined : onOpenChange}
          onClick={({ key }) => {
            if (!groupOf(key)) onNavigate(key);
          }}
          style={{ borderInlineEnd: 'none', padding: '0 8px', background: 'transparent' }}
        />
        <Tooltip
          title={`ProxyPilot ${boot.version || 'V15.0'} · React 18 · Ant Design 6`}
          placement="right"
        >
          <div className="pp-sider-foot">
            <span className="pp-sider-foot-dot" />
            {collapsed ? null : <span className="pp-sider-foot-text">{boot.version || 'V15.0'}</span>}
          </div>
        </Tooltip>
      </Sider>

      <Layout>
        <Header className="pp-header">
          <div className="pp-header-titles">
            <div className="pp-header-title">{meta.title}</div>
            <div className="pp-header-sub">{meta.subtitle}</div>
          </div>

          <div className="pp-header-actions">
            <Tooltip title="界面缩放比例">
              <Segmented
                size="small"
                value={uiScale}
                onChange={(v) => setUiScale(v)}
                options={SCALE_OPTIONS.map((o) => ({ value: o.value, label: o.label }))}
              />
            </Tooltip>
            <Tooltip title="切换整站主题">
              <Segmented
                size="small"
                value={siteTheme}
                onChange={(v) => setTheme(v)}
                options={THEME_OPTIONS}
              />
            </Tooltip>
            <JobCenter />
            <Dropdown menu={userMenu} trigger={['click']} placement="bottomRight">
              <div className="pp-user">
                <Avatar
                  size={30}
                  style={{ background: '#eef2ff', color: '#4f46e5', fontSize: 13, fontWeight: 600 }}
                  icon={!user ? <UserOutlined /> : undefined}
                >
                  {user ? user.slice(0, 1).toUpperCase() : null}
                </Avatar>
                <div style={{ lineHeight: 1.2 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: '#0f172a' }}>{user || '未登录'}</div>
                  <div style={{ fontSize: 11, color: '#94a3b8' }}>
                    {isSuper ? '超级管理员' : '管理员'}
                  </div>
                </div>
                {isSuper ? (
                  <Tag color="geekblue" style={{ marginInlineStart: 2 }}>
                    超管
                  </Tag>
                ) : null}
              </div>
            </Dropdown>
          </div>
        </Header>

        <Content>
          <div className="pp-content">{children}</div>
        </Content>
      </Layout>
    </Layout>
  );
}
