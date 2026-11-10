/* =================================================================
 *  用户中心（Portal）入口
 *  · 未登录：邮箱验证码登录
 *  · PC：单页（账号 + 卡片 + 全部区块）
 *  · 移动端：我的订阅（含订阅生成 / 信息与邮箱）、配置管理（含订阅源）、访问统计
 * ================================================================= */
import React, { useEffect, useMemo, useState } from 'react';
import { Button, Segmented, Skeleton } from 'antd';
import PortalLogin from './PortalLogin';
import PortalHome from './PortalHome';
import PortalUuid from './PortalUuid';
import PortalNav from './PortalNav';
import PortalMobileApp from './PortalMobileApp';
import { portalApi, portalToken, portalSetToken } from './portalApi';
import useIsPc from './useIsPc';

/** 极简 hash 路由：'' 我的订阅 / #/manage 配置管理 / #/stats 访问统计 / #/u/<uuid> 全量页签 */
function readRoute() {
  const h = String(window.location.hash || '');
  if (/^#\/manage\/?$/.test(h)) return { name: 'manage' };
  if (/^#\/stats\/?$/.test(h)) return { name: 'stats' };
  const m = h.match(/^#\/u\/(.+)$/);
  if (m) return { name: 'uuid', uuid: decodeURIComponent(m[1]) };
  return { name: 'home' };
}

/* 移动端的分区页面：标题 + 指定区块 + 底部导航（两段式时带底部切换条） */
function SectionPage({ title, uuid: uuidProp, sections, active, onNavigate, segmentable }) {
  const [which, setWhich] = useState(sections[0]);
  const [uuid, setUuid] = useState(uuidProp || '');
  const [phase, setPhase] = useState(uuidProp ? 'ready' : 'loading');
  const list = segmentable ? [which] : sections;

  /* 外层拿到的 UUID 可能还没到（或拿不到）：这里自己再兜一次，别让页面永远停在骨架屏 */
  useEffect(() => {
    let alive = true;
    if (uuidProp) {
      setUuid(uuidProp);
      setPhase('ready');
      return undefined;
    }
    portalApi('portal/uuids')
      .then((r) => {
        if (!alive) return;
        const list2 = Array.isArray(r && r.data) ? r.data : Array.isArray(r && r.data && r.data.uuids) ? r.data.uuids : [];
        setUuid(list2.length ? list2[0].uuid : '');
        setPhase(list2.length ? 'ready' : 'empty');
      })
      .catch(() => {
        if (alive) setPhase('error');
      });
    return () => {
      alive = false;
    };
  }, [uuidProp]);

  return (
    <div className="pp-portal pp-has-nav">
      <div className="pp-portal-wide">
        <header className="pp-portal-top">
          <div>
            <div className="pp-portal-brand-title">{title}</div>
            <div className="pp-portal-brand-sub">
              {uuid ? '节点由管理员维护，这里只做查看与订阅管理' : '还没有绑定 UUID'}
            </div>
          </div>
        </header>

        {uuid ? (
          <PortalUuid uuid={uuid} sections={list} onUnauthorized={() => onNavigate('home')} />
        ) : phase === 'loading' ? (
          <div className="pp-portal-card">
            <Skeleton active paragraph={{ rows: 3 }} />
          </div>
        ) : (
          <div className="pp-portal-card" style={{ textAlign: 'center', color: '#94a3b8', padding: '28px 16px' }}>
            {phase === 'error' ? '加载失败，请下拉刷新或重新进入' : '这个邮箱还没有绑定 UUID'}
          </div>
        )}
      </div>

      {segmentable ? (
        <div className="pp-page-switch">
          <Segmented
            block
            value={which}
            onChange={setWhich}
            options={[
              { label: '配置管理', value: 'configs' },
              { label: '外部订阅源', value: 'sources' },
            ]}
          />
        </div>
      ) : null}

      <PortalNav
        active={active}
        onHome={() => onNavigate('home')}
        onManage={() => onNavigate('manage')}
        onStats={() => onNavigate('stats')}
        manageDisabled={!uuid}
      />
    </div>
  );
}

export default function PortalApp() {
  const isPc = useIsPc();
  const [authed, setAuthed] = useState(() => !!portalToken());
  const [email, setEmail] = useState('');
  const [route, setRoute] = useState(readRoute);
  const [uuids, setUuids] = useState([]);

  /* SSO 回调成功后后端会带 ?token=xxx 跳回首页，这里接住并清理地址栏 */
  useEffect(() => {
    const sp = new URLSearchParams(window.location.search);
    const tk = sp.get('token');
    if (tk) {
      portalSetToken(tk);
      setAuthed(true);
      sp.delete('token');
      const qs = sp.toString();
      window.history.replaceState({}, '', window.location.pathname + (qs ? '?' + qs : ''));
    } else if (sp.get('oidc_error')) {
      sp.delete('oidc_error');
      const qs = sp.toString();
      window.history.replaceState({}, '', window.location.pathname + (qs ? '?' + qs : ''));
    }
  }, []);

  useEffect(() => {
    const onHash = () => setRoute(readRoute());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  /* 移动端分区页面需要知道操作哪个 UUID：取该邮箱的第一个 */
  useEffect(() => {
    if (!authed) return;
    let alive = true;
    portalApi('portal/uuids')
      .then((r) => {
        if (alive) setUuids((r && r.data) || []);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [authed]);

  const firstUuid = uuids.length ? uuids[0].uuid : '';

  const onLoggedIn = (mail) => {
    setEmail(mail || '');
    setAuthed(true);
  };

  const onLogout = () => {
    portalSetToken('');
    setAuthed(false);
    setEmail('');
    setUuids([]);
    window.location.hash = '';
  };

  const go = (name) => {
    window.location.hash = name === 'home' ? '' : `#/${name}`;
    setRoute({ name });
  };

  if (!authed) return <PortalLogin onLoggedIn={onLoggedIn} />;

  /* 移动端走 Ant Design Mobile 版本（独立的底部 TabBar 外壳） */
  if (!isPc) {
    return <PortalMobileApp email={email} onLogout={onLogout} />;
  }

  /* PC 端只渲染桌面单页；移动端布局与交互全部在 PortalMobileApp 内，互不影响 */
  return <PortalHome email={email} onLogout={onLogout} />;
}
