/* =================================================================
 *  用户中心（Portal）入口
 *  · 未登录：邮箱验证码登录
 *  · PC：单页（账号 + 卡片 + 全部区块）
 *  · 移动端：我的订阅（含订阅生成 / 信息与邮箱）、配置管理（含订阅源）、访问统计
 * ================================================================= */
import React, { lazy, Suspense, useEffect, useState } from 'react';
import PortalLogin from './PortalLogin';
import { portalToken, portalSetToken } from './portalApi';
import useIsPc from './useIsPc';

// 桌面/移动布局按设备按需加载：登录首屏（PortalLogin）保持同步，不等待布局 chunk。
// UUID 拉取由对应布局（PortalHome / PortalMobileApp）各自负责，入口不再重复请求。
const PortalHome = lazy(() => import('./PortalHome'));
const PortalMobileApp = lazy(() => import('./PortalMobileApp'));

export default function PortalApp() {
  const isPc = useIsPc();
  const [authed, setAuthed] = useState(() => !!portalToken());
  const [email, setEmail] = useState('');

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

  const onLoggedIn = (mail) => {
    setEmail(mail || '');
    setAuthed(true);
  };

  const onLogout = () => {
    portalSetToken('');
    setAuthed(false);
    setEmail('');
    window.location.hash = '';
  };

  if (!authed) return <PortalLogin onLoggedIn={onLoggedIn} />;

  // 已登录：只加载当前设备的布局（PC → PortalHome；移动 → PortalMobileApp）
  return (
    <Suspense
      fallback={(
        <div
          className="pp-portal pp-portal-layout-loading"
          style={{
            minHeight: '100vh',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--pp-text-secondary)',
            fontSize: 14,
          }}
        >
          正在加载用户中心…
        </div>
      )}
    >
      {isPc ? <PortalHome email={email} onLogout={onLogout} /> : <PortalMobileApp email={email} onLogout={onLogout} />}
    </Suspense>
  );
}