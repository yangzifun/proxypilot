import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Segmented, Tooltip } from 'antd';
import { getTheme, setTheme, subscribeTheme, THEME_OPTIONS } from '../theme';
import { SCALE_OPTIONS, useScale } from '../uiScale';
import { Avatar, Badge, Button, Dropdown, Layout, Menu, Tag } from 'antd';
import {
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  SafetyCertificateOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useAuth } from '../auth';
import { menuItems, PAGE_META, NAV_GROUPS, groupOf } from '../nav';
import { LOGO_URL } from '../theme';
import JobCenter from '../components/JobCenter';

const { Sider, Header, Content } = Layout;

const SIDER_COLLAPSED_KEY = 'pp.sider.collapsed';

function readSiderCollapsed() {
  try {
    return localStorage.getItem(SIDER_COLLAPSED_KEY) === '1';
  } catch (e) {
    return false;
  }
}

function persistSiderCollapsed(value) {
  try {
    localStorage.setItem(SIDER_COLLAPSED_KEY, value ? '1' : '0');
  } catch (e) {
    /* ignore */
  }
}

/* 版本号以服务端注入的 boot 为准，避免前端硬编码落后于后端 */
const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};

export default function AppLayout({ activeKey, onNavigate, children }) {
  const [siteTheme, setSiteTheme] = useState(() => getTheme());
  useEffect(() => subscribeTheme((v) => setSiteTheme(v)), []);
  const [uiScale, setUiScale] = useScale();
  const { user, isSuper, logout } = useAuth();
  const meta = PAGE_META[activeKey] || PAGE_META.dash;

  const items = useMemo(() => menuItems(isSuper), [isSuper]);

  // 侧边栏折叠：初始值读取 localStorage，展开/收起时持久化。
  const [collapsed, setCollapsed] = useState(readSiderCollapsed);
  const siderRef = useRef(null);
  useEffect(() => {
    const node = siderRef.current;
    if (!node) return;
    const width = collapsed ? '80px' : '224px';
    // styles.css 对 .pp-admin > .ant-layout-sider 有 224px !important 约束；
    // 折叠时用 inline !important 覆盖，避免只折叠菜单而侧边栏宽度不变。
    node.style.setProperty('width', width, 'important');
    node.style.setProperty('min-width', width, 'important');
    node.style.setProperty('max-width', width, 'important');
    node.style.setProperty('flex', `0 0 ${width}`, 'important');
  }, [collapsed]);
  const toggleSider = () => {
    setCollapsed((prev) => {
      const next = !prev;
      persistSiderCollapsed(next);
      return next;
    });
  };

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
    <Layout className="pp-admin" style={{ minHeight: '100vh' }}>
      <Sider
        ref={siderRef}
        className="pp-sider"
        theme="dark"
        width={224}
        collapsed={collapsed}
        collapsedWidth={80}
        trigger={null}
      >
        <div
          className="pp-brand"
          style={collapsed ? { justifyContent: 'center', padding: '0 8px' } : undefined}
        >
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
          <div className="pp-brand-text" style={{ display: collapsed ? 'none' : undefined }}>
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
            // 只有分组标题自身不导航；分组内的叶子项（订阅用户 / 节点服务…）正常跳转
            if (NAV_GROUPS.some((g) => g.key === key)) return;
            onNavigate(key);
          }}
          style={{ borderInlineEnd: 'none', padding: '0 8px', background: 'transparent' }}
        />
        <Tooltip
          title={`ProxyPilot ${boot.version || '版本未知'} · React 18 · Ant Design 6`}
          placement="right"
        >
          <div className="pp-sider-foot">
            <span className="pp-sider-foot-dot" />
            {collapsed ? null : <span className="pp-sider-foot-text">{boot.version || '—'}</span>}
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
            <Tooltip title={collapsed ? '展开侧边栏' : '收起侧边栏'}>
              <Button
                type="text"
                size="small"
                aria-label={collapsed ? '展开侧边栏' : '收起侧边栏'}
                aria-expanded={!collapsed}
                icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
                onClick={toggleSider}
              />
            </Tooltip>
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
                  style={{ background: 'var(--pp-primary-soft)', color: 'var(--pp-primary)', fontSize: 13, fontWeight: 600 }}
                  icon={!user ? <UserOutlined /> : undefined}
                >
                  {user ? user.slice(0, 1).toUpperCase() : null}
                </Avatar>
                <div style={{ lineHeight: 1.2 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--pp-text)' }}>{user || '未登录'}</div>
                  <div style={{ fontSize: 11, color: 'var(--pp-text-tertiary)' }}>
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
