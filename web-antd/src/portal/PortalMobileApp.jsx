/* =================================================================
 *  移动端用户中心（Ant Design Mobile 版）
 *  · NavBar 顶部 + TabBar 底部（我的订阅 / 配置管理 / 访问统计 / 我的）
 *  · 顶部渐变「订阅卡」：名称 / 状态 / 节点数 / UUID，多个 UUID 用 chip 切换
 *  · 账号与退出收进第 4 个页签（「我的」），顶部不再放头像/退出按钮
 *  · 深链 #/u/<uuid> 进入单个 UUID 的完整页签视图
 * ================================================================= */
import React, { useCallback, useEffect, useState } from 'react';
import { NavBar, TabBar, Toast } from 'antd-mobile';
import {
  AppstoreOutlined, BarChartOutlined, CopyOutlined, SendOutlined, SettingOutlined, UserOutlined,
} from '@ant-design/icons';
import PortalUuid from './PortalUuid';
import MobileSubscribe from './MobileSubscribe';
import MobileConfigs from './MobileConfigs';
import MobileStats from './MobileStats';
import MobileAccount from './MobileAccount';
import MtprotoCard from './MtprotoCard';
import { portalApi } from './portalApi';
import { shortUuid } from '../format';

const TABS = [
  { key: 'sub', title: '我的订阅', icon: <AppstoreOutlined />, sections: ['sub'] },
  { key: 'tg', title: 'Telegram', icon: <SendOutlined />, sections: ['mtproto'] },
  { key: 'manage', title: '配置管理', icon: <SettingOutlined />, sections: ['configs', 'sources'] },
  { key: 'stats', title: '访问统计', icon: <BarChartOutlined />, sections: ['stats'] },
  { key: 'me', title: '我的', icon: <UserOutlined />, sections: [] },
];

const NAV_TITLE = { sub: 'ProxyPilot', me: '账号与信息', tg: 'Telegram 代理' };

const STATUS_TEXT = { active: '正常', suspended: '暂停', inactive: '停用' };

const readDetail = () => {
  const m = String(window.location.hash || '').match(/^#\/u\/(.+)$/);
  return m ? decodeURIComponent(m[1]) : '';
};

export default function PortalMobileApp({ email, onLogout }) {
  const [tab, setTab] = useState('sub');
  const [uuids, setUuids] = useState([]);
  const [uuid, setUuid] = useState('');
  const [phase, setPhase] = useState('loading');
  /* 深链 #/u/<uuid>：进入单个 UUID 的完整页签视图 */
  const [detail, setDetail] = useState(readDetail);

  useEffect(() => {
    const onHash = () => setDetail(readDetail());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  const load = useCallback(async () => {
    try {
      const r = await portalApi('portal/uuids');
      const list = Array.isArray(r && r.data) ? r.data : Array.isArray(r && r.data && r.data.uuids) ? r.data.uuids : [];
      setUuids(list);
      setUuid((prev) => (prev && list.some((x) => x.uuid === prev) ? prev : list[0] ? list[0].uuid : ''));
      setPhase(list.length ? 'ready' : 'empty');
    } catch (e) {
      if (e && e.status === 401) {
        Toast.show({ content: '登录已失效，请重新登录' });
        if (onLogout) onLogout();
        return;
      }
      setPhase('error');
    }
  }, [onLogout]);

  useEffect(() => {
    load();
  }, [load]);

  const current = TABS.find((t) => t.key === tab) || TABS[0];
  const selected = uuids.find((x) => x.uuid === uuid) || null;

  const onUnauthorized = () => {
    Toast.show({ content: '登录已失效，请重新登录' });
    if (onLogout) onLogout();
  };

  const copyUuid = async () => {
    try {
      await navigator.clipboard.writeText(uuid);
      Toast.show({ content: '已复制 UUID' });
    } catch (e) {
      Toast.show({ content: '复制失败，请长按选择' });
    }
  };

  const statusText = selected ? STATUS_TEXT[selected.status] || '未知' : '';
  const statusCls = selected
    ? selected.status === 'active'
      ? 'is-on'
      : selected.status === 'suspended'
      ? 'is-warn'
      : 'is-off'
    : 'is-off';

  return (
    <div className="ppm">
      <NavBar
        className="ppm-nav"
        backIcon={detail ? undefined : null}
        onBack={() => {
          window.location.hash = '';
          setDetail('');
        }}
      >
        {detail ? 'UUID 详情' : NAV_TITLE[tab] || current.title}
      </NavBar>

      {/* 深链：单个 UUID 的完整页签视图 */}
      {detail ? (
        <div className="ppm-body">
          <PortalUuid
            uuid={detail}
            onBack={() => {
              window.location.hash = '';
              setDetail('');
            }}
            onUnauthorized={onUnauthorized}
          />
        </div>
      ) : (
        <div className="ppm-body">
          {tab === 'tg' ? (
            <div style={{ padding: 12 }}>
              <MtprotoCard mobile />
            </div>
          ) : tab === 'me' ? (
            /* 「我的」页：不依赖 UUID，未绑定也能看账号与退出 */
            <div className="ppm-me">
              <MobileAccount
                email={email}
                uuid={uuid}
                onLogout={onLogout}
                onEmailChanged={(mail) => {
                  if (mail) Toast.show({ content: `邮箱已更换为 ${mail}` });
                  load();
                }}
              />
            </div>
          ) : phase === 'loading' ? (
            <div className="ppm-tip">加载中…</div>
          ) : !uuid ? (
            <div className="ppm-tip">{phase === 'error' ? '加载失败，请重新进入' : '这个邮箱还没有绑定 UUID'}</div>
          ) : (
            <>
              {/* 顶部订阅卡：名称 / 状态 / 节点数 / UUID；多个 UUID 用 chip 切换 */}
              <div className="ppm-hero">
                <div className="ppm-hero-row">
                  <div className="ppm-hero-main">
                    <div className="ppm-hero-name">
                      {selected ? selected.remark || selected.owner || shortUuid(selected.uuid) : '我的订阅'}
                    </div>
                    <div className="ppm-hero-sub">
                      <AppstoreOutlined /> {Number((selected && selected.configs) || 0)} 个节点
                    </div>
                  </div>
                  {selected ? <span className={`ppm-status ${statusCls}`}>{statusText}</span> : null}
                </div>

                <div className="ppm-hero-meta">
                  <span className="ppm-hero-uuid">{shortUuid(uuid)}</span>
                  <button type="button" className="ppm-hero-copy" onClick={copyUuid}>
                    <CopyOutlined /> UUID
                  </button>
                </div>

                {uuids.length > 1 ? (
                  <div className="ppm-hero-chips">
                    {uuids.map((it) => (
                      <button
                        type="button"
                        key={it.uuid}
                        className={`ppm-chip${it.uuid === uuid ? ' is-active' : ''}${
                          it.status !== 'active' ? ' is-warn' : ''
                        }`}
                        onClick={() => setUuid(it.uuid)}
                      >
                        <i className="ppm-chip-dot" />
                        {it.remark || it.owner || shortUuid(it.uuid)}
                        <span className="ppm-chip-count">{Number(it.configs || 0)}</span>
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>

              {tab === 'sub' ? (
                <MobileSubscribe key={`sub-${uuid}`} uuid={uuid} />
              ) : tab === 'stats' ? (
                <MobileStats key={`stats-${uuid}`} uuid={uuid} />
              ) : tab === 'manage' ? (
                <MobileConfigs key={`cfg-${uuid}`} uuid={uuid} onUnauthorized={onUnauthorized} />
              ) : (
                <PortalUuid
                  key={`all-${uuid}`}
                  uuid={uuid}
                  sections={current.sections}
                  onUnauthorized={onUnauthorized}
                />
              )}
            </>
          )}
        </div>
      )}

      {detail ? null : (
        <TabBar className="ppm-tabbar" activeKey={tab} onChange={setTab} safeArea>
          {TABS.map((t) => (
            <TabBar.Item key={t.key} icon={t.icon} title={t.title} />
          ))}
        </TabBar>
      )}
    </div>
  );
}
