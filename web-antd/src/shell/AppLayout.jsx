import React, { useMemo } from 'react';
import { Avatar, Badge, Dropdown, Layout, Menu, Tag } from 'antd';
import { LogoutOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons';
import { useAuth } from '../auth';
import { menuItems, PAGE_META } from '../nav';
import { LOGO_URL } from '../theme';
import JobCenter from '../components/JobCenter';

const { Sider, Header, Content } = Layout;

/* 版本号以 Worker 注入的 boot 为准，避免前端硬编码落后于后端 */
const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};

export default function AppLayout({ activeKey, onNavigate, children }) {
  const { user, isSuper, logout } = useAuth();
  const meta = PAGE_META[activeKey] || PAGE_META.dash;

  const items = useMemo(() => menuItems(isSuper), [isSuper]);

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
          mode="inline"
          theme="dark"
          items={items}
          selectedKeys={[activeKey]}
          onClick={({ key }) => onNavigate(key)}
          style={{ borderInlineEnd: 'none', padding: '0 8px', background: 'transparent' }}
        />
        <div
          style={{
            padding: '12px 18px 16px',
            borderTop: '1px solid rgba(148,163,184,0.12)',
            color: '#64748b',
            fontSize: 11.5,
            lineHeight: 1.7,
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <Badge status="processing" color="#818cf8" />
            <span style={{ color: '#94a3b8' }}>{boot.version || 'V15.0'} · AntD 6</span>
          </div>
        </div>
      </Sider>

      <Layout>
        <Header className="pp-header">
          <div className="pp-header-titles">
            <div className="pp-header-title">{meta.title}</div>
            <div className="pp-header-sub">{meta.subtitle}</div>
          </div>

          <div className="pp-header-actions">
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
