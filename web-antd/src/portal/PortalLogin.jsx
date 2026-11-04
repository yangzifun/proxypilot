/* =================================================================
 *  用户中心 · 登录（邮箱验证码 + Turnstile）
 *  桌面：左侧深色品牌面板 + 右侧表单；移动端：品牌收成顶部条，表单通栏
 * ================================================================= */
import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Input } from 'antd';
import { ArrowRightOutlined, LockOutlined, MailOutlined, SafetyOutlined } from '@ant-design/icons';
import { portalApi, portalSetToken } from './portalApi';
import { useTurnstile } from '../hooks';
import LoginAnimation from '../components/LoginAnimation';
import { LOGO_URL } from '../theme';

const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};
const SITE_KEY = String(boot.turnstile || '');

export default function PortalLogin({ onLoggedIn }) {
  const { message } = AntdApp.useApp();
  const turnstile = useTurnstile(SITE_KEY);
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const [countdown, setCountdown] = useState(0);

  useEffect(() => {
    if (countdown <= 0) return undefined;
    const t = setTimeout(() => setCountdown((n) => n - 1), 1000);
    return () => clearTimeout(t);
  }, [countdown]);

  const emailOk = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(String(email).trim());

  const sendCode = async () => {
    if (!emailOk) {
      setErr('请填写正确的邮箱地址');
      return;
    }
    if (SITE_KEY && !turnstile.getToken()) {
      setErr('请先完成下方的人机验证');
      return;
    }
    setBusy('send');
    setErr('');
    try {
      const res = await portalApi('portal/send-code', {
        method: 'POST',
        body: { email: email.trim(), cf_turnstile_response: turnstile.getToken() },
      });
      message.success(`验证码已发送至 ${(res && res.email) || email.trim()}，10 分钟内有效`);
      setCountdown(60);
    } catch (e) {
      setErr(e.message || '发送失败');
      turnstile.reset();
    } finally {
      setBusy('');
    }
  };

  const login = async () => {
    if (!emailOk) {
      setErr('请填写正确的邮箱地址');
      return;
    }
    if (!/^\d{6}$/.test(code.trim())) {
      setErr('请输入 6 位数字验证码');
      return;
    }
    setBusy('login');
    setErr('');
    try {
      const res = await portalApi('portal/login', {
        method: 'POST',
        body: { email: email.trim(), code: code.trim() },
        auth: false,
      });
      portalSetToken(res.token || '');
      message.success('登录成功');
      onLoggedIn(res.email || email.trim());
    } catch (e) {
      setErr(e.message || '登录失败');
    } finally {
      setBusy('');
    }
  };

  return (
    <div className="pp-auth">
      <div className="pp-auth-card">
        <aside className="pp-auth-brand">
          <div className="pp-auth-brandhead">
            <img src={LOGO_URL} alt="ProxyPilot" className="pp-auth-logo" />
            <div>
              <div className="pp-auth-name">ProxyPilot</div>
              <div className="pp-auth-sub">用户中心</div>
            </div>
          </div>

          <LoginAnimation />

          <div className="pp-auth-brandfoot">
            <SafetyOutlined /> 邮箱验证码登录 · 无需密码
          </div>
        </aside>

        <main className="pp-auth-form">
          <h1 className="pp-auth-title">欢迎回来</h1>
          <p className="pp-auth-desc">用邮箱验证码登录，查看你的订阅</p>

          <div className="pp-auth-steps">
            <span><b>1</b><i className="pp-step-full">填订阅所属邮箱</i><i className="pp-step-short">填邮箱</i></span>
            <span><b>2</b><i className="pp-step-full">收 6 位验证码</i><i className="pp-step-short">收验证码</i></span>
            <span><b>3</b><i className="pp-step-full">登录查看订阅</i><i className="pp-step-short">看订阅</i></span>
          </div>

          {err ? (
            <div className="pp-auth-err" onClick={() => setErr('')}>
              {err}
            </div>
          ) : null}

          <label className="pp-auth-label">邮箱</label>
          <div className="pp-auth-row">
            <Input
              size="large"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="订阅所属邮箱"
              prefix={<MailOutlined style={{ color: '#94a3b8' }} />}
              suffix={(
                <button
                  type="button"
                  className="pp-auth-send-inline"
                  onClick={sendCode}
                  disabled={countdown > 0 || !emailOk || busy === 'send'}
                >
                  {busy === 'send' ? '发送中…' : countdown > 0 ? `${countdown}s` : '发送验证码'}
                </button>
              )}
              autoComplete="email"
              inputMode="email"
              className="pp-auth-input pp-auth-email"
            />
            <Button
              size="large"
              onClick={sendCode}
              loading={busy === 'send'}
              disabled={countdown > 0 || !emailOk}
              className="pp-auth-inline"
            >
              {countdown > 0 ? `${countdown}s` : '发送验证码'}
            </Button>
          </div>

          <label className="pp-auth-label" style={{ marginTop: 14 }}>
            验证码
          </label>
          <div className="pp-auth-row">
            <Input
              size="large"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
              onPressEnter={login}
              placeholder="6 位验证码"
              prefix={<LockOutlined style={{ color: '#94a3b8' }} />}
              inputMode="numeric"
              maxLength={6}
              className="pp-auth-input pp-auth-code"
            />
          </div>

          {SITE_KEY ? (
            <div className="pp-turnstile pp-auth-ts">
              <div className="pp-turnstile" ref={turnstile.slotRef} />
              {turnstile.status === 'error' ? (
                <div style={{ fontSize: 12, color: '#f43f5e', marginTop: 4, textAlign: 'center' }}>
                  人机验证加载失败，<a onClick={() => turnstile.reload()}>点此重试</a>
                </div>
              ) : null}
            </div>
          ) : null}

          <div className="pp-auth-fill" aria-hidden="true" />

          <Button
            type="primary"
            size="large"
            block
            onClick={login}
            loading={busy === 'login'}
            className="pp-auth-submit"
            icon={<ArrowRightOutlined />}
            iconPosition="end"
          >
            登录
          </Button>

          <div className="pp-auth-foot">UUID 由管理员绑定到邮箱，登录后即可查看你的订阅</div>
        </main>
      </div>
    </div>
  );
}
