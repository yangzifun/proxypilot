/* =================================================================
 *  用户中心 · 我的订阅
 *  登录后展示该邮箱名下的 UUID（节点数 / 状态 / 三种订阅链接 / 二维码）
 * ================================================================= */
import React, { useCallback, useEffect, useState } from 'react';
import { App as AntdApp, Button, Col, Dropdown, Empty, Modal, QRCode, Row, Skeleton, Tag } from 'antd';
import {
  CloudDownloadOutlined,
  DownOutlined,
  InfoCircleOutlined,
  LogoutOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { portalApi, portalSetToken } from './portalApi';
import { CopyText } from '../components';
import Guide from './Guide';
import EmailChange from './EmailChange';
import PortalAccount from './PortalAccount';
import PortalUuid from './PortalUuid';
import PortalNav from './PortalNav';
import useIsPc from './useIsPc';
import { fmtAgo, fmtNum, fmtTime, shortUuid } from '../format';
import { LOGO_URL } from '../theme';

const STATUS = {
  active: { color: 'success', text: '正常' },
  suspended: { color: 'warning', text: '暂停' },
  inactive: { color: 'default', text: '停用' },
};

const LINKS = [
  { key: 'raw', label: '原始订阅', hint: '通用 v2rayN / Shadowrocket 等' },
  { key: 'clash', label: 'Clash 订阅', hint: 'Clash / Mihomo 客户端' },
  { key: 'batch', label: '批量配置', hint: '一次性导出全部节点' },
];

export default function PortalHome({ email, onLogout }) {
  const { message } = AntdApp.useApp();
  const isPc = useIsPc();
  const [picked, setPicked] = useState('');
  const [state, setState] = useState({ data: [], email: email || '', loading: true, error: null });
  const [qr, setQr] = useState(null); // { title, url }
  const [profileOpen, setProfileOpen] = useState(false); // 点右上用户入口：账号与信息

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await portalApi('portal/uuids');
      setState({ data: (res && res.data) || [], email: (res && res.email) || email || '', loading: false, error: null });
    } catch (e) {
      if (e.status === 401) {
        onLogout();
        return;
      }
      setState((s) => ({ ...s, loading: false, error: e }));
    }
  }, [email, onLogout]);

  useEffect(() => {
    load();
  }, [load]);

  const logout = () => {
    portalSetToken('');
    onLogout();
  };

  useEffect(() => {
    if (!state.data.length) {
      if (picked) setPicked('');
      return;
    }
    if (!picked || !state.data.some((x) => x.uuid === picked)) setPicked(state.data[0].uuid);
  }, [state.data, picked]);

  const openQr = (item, link) => {
    setQr({ title: `${item.remark || item.owner || shortUuid(item.uuid)} · ${link.label}`, url: item.sub[link.key] });
  };

  const userEmail = state.email || email;

  /* 右上角用户下拉：账号与信息 / 退出登录（刷新按钮始终在头部可见） */
  const userMenu = {
    items: [
      { key: 'account', icon: <SettingOutlined />, label: '账号与信息' },
      { type: 'divider' },
      { key: 'logout', icon: <LogoutOutlined />, danger: true, label: '退出登录' },
    ],
    onClick: ({ key }) => {
      if (key === 'account') setProfileOpen(true);
      if (key === 'logout') logout();
    },
  };

  return (
    <div className={`pp-portal${!isPc ? ' pp-has-nav' : ''}`}>
      <div className="pp-portal-wide">
        <header className="pp-portal-top">
          {/* 左上角：纯品牌标识（网站图标 + ProxyPilot + 用户中心），不再承担账号信息入口 */}
          <div className="pp-portal-brand pp-portal-brand-sm">
            <img src={LOGO_URL} alt="ProxyPilot" className="pp-portal-logo" />
            <div>
              <div className="pp-portal-brand-title">ProxyPilot</div>
              <div className="pp-portal-brand-sub">用户中心</div>
            </div>
          </div>
          {/* 右上角：刷新（常驻） + 独立用户信息入口（头像 + 邮箱 + 下拉） */}
          <div className="pp-portal-top-actions">
            <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
              刷新
            </Button>
            <div className="pp-portal-user-chip">
              <button
                type="button"
                className="pp-portal-user-main"
                onClick={() => setProfileOpen(true)}
                title={userEmail || '账号与信息'}
                aria-label="打开账号与信息"
              >
                <span className="pp-portal-user-avatar" aria-hidden="true">
                  <UserOutlined />
                </span>
                <span className="pp-portal-user-mail">{userEmail || '我的账号'}</span>
              </button>
              <Dropdown menu={userMenu} trigger={['click']} placement="bottomRight">
                <button
                  type="button"
                  className="pp-portal-user-caret"
                  title="账号与信息 / 退出登录"
                  aria-label="用户菜单"
                >
                  <DownOutlined />
                </button>
              </Dropdown>
            </div>
          </div>
        </header>

        {state.error ? (
          <div className="pp-portal-err" style={{ marginBottom: 14 }} onClick={() => load()}>
            {state.error.message || '加载失败'}（点此重试）
          </div>
        ) : null}

        <div className={isPc ? 'pp-home-pc' : undefined}>

        <div className="pp-home-cards">
        {state.loading && !state.data.length ? (
          <div className="pp-portal-card">
            <Skeleton active paragraph={{ rows: 4 }} />
          </div>
        ) : null}

        {!state.loading && !state.data.length ? (
          <div className="pp-portal-card">
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                <span style={{ color: '#64748b' }}>
                  该邮箱还没有绑定订阅
                  <br />
                  <span style={{ fontSize: 12, color: '#94a3b8' }}>请联系管理员把 UUID 绑定到你的邮箱</span>
                </span>
              }
            />
          </div>
        ) : null}

        {/* 只有一个 UUID 时不需要「选哪张卡」，卡片里的 UUID / 链接与下方区块重复，直接省略 */}
        <div className="pp-sub-grid" style={state.data.length <= 1 ? { display: 'none' } : undefined}>
          {state.data.map((item) => {
            const st = STATUS[String(item.status)] || { color: 'default', text: '未知' };
            return (
                <div
                  className={`pp-sub-card${picked === item.uuid ? ' is-active' : ''}${!isPc ? ' is-tappable' : ''}`}
                  key={item.uuid}
                  onClick={() => setPicked(item.uuid)}
                  style={{ cursor: 'pointer' }}
                >
                  <div className="pp-sub-head">
                    <div className="pp-sub-title">
                      {item.remark || item.owner || shortUuid(item.uuid)}
                      <Tag color={st.color} style={{ marginInlineEnd: 0 }}>
                        {st.text}
                      </Tag>
                    </div>
                    <span className="pp-sub-count">{fmtNum(item.configs)} 个节点</span>
                  </div>

                  {/* 移动端：UUID / 链接 / 二维码 已在「订阅生成」「信息与邮箱」里，卡片保持精简 */}
                  {isPc ? (
                    <>
                      <div className="pp-sub-meta" onClick={(e) => e.stopPropagation()}>
                        <div>
                          <span>UUID</span>
                          <CopyText value={item.uuid} display={<span className="pp-mono">{shortUuid(item.uuid)}</span>} />
                        </div>
                        <div>
                          <span>创建</span>
                          <b>{fmtTime(item.created_at)}</b>
                        </div>
                        <div>
                          <span>更新</span>
                          <b>{fmtAgo(item.updated_at)}</b>
                        </div>
                      </div>

                      <div className="pp-sub-links">
                        {LINKS.map((link) => {
                          const url = item.sub && item.sub[link.key];
                          if (!url) return null;
                          return (
                            <div className="pp-sub-link" key={link.key}>
                              <div className="pp-sub-link-main">
                                <div className="pp-sub-link-label">{link.label}</div>
                                <div className="pp-sub-link-hint">{link.hint}</div>
                                <div className="pp-sub-link-url pp-mono" title={url}>
                                  {url}
                                </div>
                              </div>
                              <div className="pp-sub-link-actions" onClick={(e) => e.stopPropagation()}>
                                <CopyText value={url} display={<Button size="small" icon={<CloudDownloadOutlined />}>复制</Button>} />
                                <Button size="small" icon={<QrcodeOutlined />} onClick={() => openQr(item, link)}>
                                  二维码
                                </Button>
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    </>
                  ) : (
                    <div className="pp-sub-hint">在下方「订阅生成」里取链接 / 二维码</div>
                  )}
                </div>
            );
          })}
        </div>
        </div>

        {isPc && picked ? (
          <div className="pp-portal-embedded">
            <PortalUuid uuid={picked} embedded onBack={() => {}} onUnauthorized={onLogout} />
          </div>
        ) : null}

        {/* 移动端：只并入「订阅生成」；信息与邮箱已合并进顶部账号卡（用户信息） */}
        {!isPc && picked ? (
          <PortalUuid uuid={picked} sections={['sub']} onUnauthorized={onLogout} />
        ) : null}
        </div>

        <div className="pp-portal-foot" style={{ textAlign: 'center', marginTop: 18 }}>
          订阅链接含你的 UUID，请勿公开分享
        </div>
      </div>

      {/* 移动端：右下角浮动按钮 → 账号与信息（含编辑） */}
      {!isPc ? (
        <button type="button" className="pp-fab-info" onClick={() => setProfileOpen(true)} aria-label="账号与信息">
          <InfoCircleOutlined />
          <span>信息</span>
        </button>
      ) : null}

      {!isPc ? (
        <PortalNav
          active="home"
          onHome={() => {
            if (window.scrollY) window.scrollTo(0, 0);
          }}
          onManage={() => {
            window.location.hash = '#/manage';
          }}
          onStats={() => {
            window.location.hash = '#/stats';
          }}
          manageDisabled={!state.data.length}
        />
      ) : null}

      {/* 点右上用户入口：账号与信息（邮箱换绑 + 用户信息编辑） */}
      <Modal
        open={profileOpen}
        title="账号与信息"
        onCancel={() => setProfileOpen(false)}
        footer={null}
        width={760}
        centered
      >
        <PortalAccount
          email={state.email || email}
          count={state.data.length}
          uuid={picked}
          sub={state.data.find((x) => x.uuid === picked) || state.data[0] || null}
          onEmailChanged={(mail) => {
            setState((s) => ({ ...s, email: mail || s.email }));
            load();
          }}
        />
      </Modal>

      <Modal
        open={!!qr}
        title={qr ? qr.title : ''}
        onCancel={() => setQr(null)}
        footer={<Button onClick={() => setQr(null)}>关闭</Button>}
        width={420}
        centered
        destroyOnHidden
      >
        {qr ? (
          <div style={{ textAlign: 'center', padding: '6px 0 2px' }}>
            <QRCode
              value={qr.url}
              size={200}
              type="svg"
              errorLevel="M"
              bordered={false}
              style={{ padding: 18, background: '#fff', borderRadius: 12, display: 'inline-block' }}
            />
            <div style={{ marginTop: 12, fontSize: 12, color: '#94a3b8', wordBreak: 'break-all' }}>{qr.url}</div>
            <CopyText value={qr.url} display={<Button size="small" style={{ marginTop: 10 }}>复制链接</Button>} />
          </div>
        ) : null}
      </Modal>
    </div>
  );
}
