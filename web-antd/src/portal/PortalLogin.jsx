/* =================================================================
 *  用户中心 · 登录（邮箱验证码 + Turnstile）
 *  桌面：左侧深色品牌面板 + 右侧表单；移动端：品牌收成顶部条，表单通栏
 * ================================================================= */
import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Input, Modal, Tabs } from 'antd';
import { ArrowRightOutlined, KeyOutlined, LockOutlined, MailOutlined, SafetyOutlined } from '@ant-design/icons';
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
  const [tsOpen, setTsOpen] = useState(false);
  // 人机验证通过后要执行的动作：send=发验证码 / sso=跳转单点登录
  const [pending, setPending] = useState(null);
  // 登录方式：code=邮箱验证码 / password=密码
  const [mode, setMode] = useState('code');
  const [password, setPassword] = useState('');
  // SSO（与管理端共用同一套 OIDC 配置）
  const [sso, setSso] = useState({ enabled: false, button_text: '使用 SSO 登录', provider_name: '' });

  useEffect(() => {
    let alive = true;
    portalApi('portal/oidc/status', { auth: false })
      .then((r) => { if (alive && r && r.enabled) setSso(r); })
      .catch(() => {});
    // 回调失败时后端会带 ?oidc_error=xxx 跳回来
    const sp = new URLSearchParams(window.location.search);
    const oe = sp.get('oidc_error');
    if (oe) {
      const map = {
        turnstile_failed: '人机验证未通过',
        state_mismatch: '登录状态已失效，请重试',
        token_failed: '身份提供方返回异常，请重试',
        token_rejected: '身份提供方返回异常，请重试',
        id_token_invalid: '身份令牌校验失败',
        userinfo_failed: '读取用户信息失败',
        email_missing: '身份提供方未返回邮箱',
        email_unverified: '该邮箱在身份提供方处未验证',
        no_uuid_binding: '这个邮箱还没有绑定订阅，请联系管理员',
        account_disabled: '该账号已被停用',
      };
      setErr(map[oe] || ('SSO 登录失败：' + oe));
      window.history.replaceState({}, '', window.location.pathname);
    }
    return () => { alive = false; };
  }, []);

  useEffect(() => {
    if (countdown <= 0) return undefined;
    const t = setTimeout(() => setCountdown((n) => n - 1), 1000);
    return () => clearTimeout(t);
  }, [countdown]);

  const emailOk = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(String(email).trim());

  // 发起 SSO：配置了 Turnstile 时先过验证，再带 token 跳转
  const startSSO = (tsToken) => {
    const q = tsToken ? '?cf_turnstile_response=' + encodeURIComponent(tsToken) : '';
    window.location.href = '/api/portal/oidc/login' + q;
  };

  const doSend = async () => {
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

  const sendCode = async () => {
    if (!emailOk) {
      setErr('请填写正确的邮箱地址');
      return;
    }
    if (SITE_KEY && !turnstile.getToken()) {
      setPending('send');
      setTsOpen(true);
      return;
    }
    await doSend();
  };

  useEffect(() => {
    if (!tsOpen) return;
    if (!turnstile.token) return;
    setTsOpen(false);
    const action = pending;
    setPending(null);
    if (action === 'sso') {
      startSSO(turnstile.token);
      return;
    }
    doSend();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tsOpen, turnstile.token, pending]);

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

  const loginByPassword = async () => {
    if (!emailOk) {
      setErr('请填写正确的邮箱地址');
      return;
    }
    if (!password) {
      setErr('请输入密码');
      return;
    }
    setBusy('login');
    setErr('');
    try {
      const res = await portalApi('portal/login/password', {
        method: 'POST',
        body: { email: email.trim(), password },
        auth: false,
      });
      portalSetToken(res.token || '');
      if (res.must_change_password) {
        message.warning('这是初始密码，请尽快在「账号」里修改');
      } else {
        message.success('登录成功');
      }
      onLoggedIn(res.email || email.trim(), { mustChangePassword: !!res.must_change_password });
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
            <SafetyOutlined /> 密码 / 验证码 两种登录方式
          </div>
        </aside>

        <main className="pp-auth-form">
          <div className="pp-auth-mobilehead">
            <img src={LOGO_URL} alt="ProxyPilot" className="pp-auth-mobilelogo" />
            <div className="pp-auth-mobilename">ProxyPilot</div>
            <div className="pp-auth-mobilesub">用户中心 · 订阅查询</div>
          </div>
          <h1 className="pp-auth-title">欢迎回来</h1>
          <p className="pp-auth-desc">用密码或邮箱验证码登录，查看你的订阅</p>

          <Tabs
            activeKey={mode}
            onChange={(k) => { setMode(k); setErr(''); }}
            items={[
              { key: 'code', label: '邮箱验证码' },
              { key: 'password', label: '密码登录' },
            ]}
            className="pp-auth-tabs"
          />

          {mode === 'code' ? (
            <div className="pp-auth-steps">
              <span><b>1</b><i className="pp-step-full">填订阅所属邮箱</i><i className="pp-step-short">填邮箱</i></span>
              <span><b>2</b><i className="pp-step-full">收 6 位验证码</i><i className="pp-step-short">收验证码</i></span>
              <span><b>3</b><i className="pp-step-full">登录查看订阅</i><i className="pp-step-short">看订阅</i></span>
            </div>
          ) : (
            <div className="pp-auth-steps">
              <span><b>1</b><i className="pp-step-full">填邮箱与密码</i><i className="pp-step-short">填账号</i></span>
              <span><b>2</b><i className="pp-step-full">登录后可在账号里改密</i><i className="pp-step-short">可改密</i></span>
            </div>
          )}

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
              suffix={mode === 'code' ? (
                <button
                  type="button"
                  className="pp-auth-send-inline"
                  onClick={sendCode}
                  disabled={countdown > 0 || !emailOk || busy === 'send'}
                >
                  {busy === 'send' ? '发送中…' : countdown > 0 ? `${countdown}s` : '发送验证码'}
                </button>
              ) : null}
              autoComplete="email"
              inputMode="email"
              className="pp-auth-input pp-auth-email"
            />
            {mode === 'code' ? (
              <Button
                size="large"
                onClick={sendCode}
                loading={busy === 'send'}
                disabled={countdown > 0 || !emailOk}
                className="pp-auth-inline"
              >
                {countdown > 0 ? `${countdown}s` : '发送验证码'}
              </Button>
            ) : null}
          </div>

          {mode === 'code' ? (
            <>
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
            </>
          ) : (
            <>
              <label className="pp-auth-label" style={{ marginTop: 14 }}>
                密码
              </label>
              <div className="pp-auth-row">
                <Input.Password
                  size="large"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  onPressEnter={loginByPassword}
                  placeholder="登录密码"
                  prefix={<KeyOutlined style={{ color: '#94a3b8' }} />}
                  autoComplete="current-password"
                  className="pp-auth-input"
                />
              </div>
              <div className="pp-auth-foot" style={{ marginTop: 8 }}>
                初始密码由管理员提供；登录后可在「账号」里修改
              </div>
            </>
          )}


          <div className="pp-auth-fill" aria-hidden="true" />

          <Button
            type="primary"
            size="large"
            block
            onClick={mode === 'code' ? login : loginByPassword}
            loading={busy === 'login'}
            className="pp-auth-submit"
            icon={<ArrowRightOutlined />}
            iconPosition="end"
          >
            登录
          </Button>

          {sso.enabled ? (
            <>
              <div className="pp-auth-or">或</div>
              <Button
                size="large"
                block
                icon={<SafetyOutlined />}
                onClick={() => {
                  if (SITE_KEY && !turnstile.getToken()) {
                    setPending('sso');
                    setTsOpen(true);
                    return;
                  }
                  startSSO(turnstile.getToken());
                }}
              >
                {sso.button_text}
              </Button>
              <div className="pp-auth-foot" style={{ marginTop: 8 }}>
                单点登录同样使用你绑定的邮箱身份，无需密码
              </div>
            </>
          ) : null}

          <div className="pp-auth-foot">UUID 由管理员绑定到邮箱，登录后即可查看你的订阅</div>
        </main>
      </div>

      <Modal
        open={tsOpen}
        onCancel={() => {
          setTsOpen(false);
          setPending(null);
          turnstile.reset();
        }}
        footer={null}
        title="人机验证"
        width={360}
        centered
        destroyOnHidden
      >
        <div style={{ display: 'flex', justifyContent: 'center', padding: '8px 0 4px' }}>
          <div className="pp-turnstile" ref={turnstile.slotRef} />
        </div>
        {turnstile.status === 'error' ? (
          <div style={{ fontSize: 12, color: '#f43f5e', textAlign: 'center', marginTop: 8 }}>
            验证组件加载失败，<a onClick={() => turnstile.reload()}>点此重试</a>
          </div>
        ) : null}
        <div style={{ fontSize: 12, color: '#94a3b8', textAlign: 'center', marginTop: 10 }}>
          {pending === 'sso' ? '完成验证后将跳转到单点登录' : '完成验证后将自动发送验证码'}
        </div>
      </Modal>
    </div>
  );
}
