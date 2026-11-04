import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, App as AntdApp, Button, Divider, Form, Input, Tabs, Tag } from 'antd';
import {
  ApiOutlined,
  CloudServerOutlined,
  KeyOutlined,
  LockOutlined,
  LoginOutlined,
  MailOutlined,
  SafetyCertificateOutlined,
  SafetyOutlined,
  ThunderboltOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useAuth } from '../auth';
import { useTurnstile } from '../hooks';
import LoginAnimation from '../components/LoginAnimation';
import { LOGO_URL } from '../theme';

const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};
const SITE_KEY = String(boot.turnstile || '');
const OIDC = boot.oidc || { enabled: false, buttonText: '使用单点登录（OIDC）', passwordDisabled: false };
const EMAIL_ENABLED = !!boot.emailLogin;

/** OIDC 失败时后端会带 ?oidc_error=... 回到登录页 */
const OIDC_ERROR = (() => {
  try {
    return new URLSearchParams(window.location.search).get('oidc_error') || '';
  } catch (e) {
    return '';
  }
})();

export default function Login() {
  const { loginPassword, sendEmailCode, verifyEmailCode, verifyEmailMfa, loginWithBackup } = useAuth();
  const { message } = AntdApp.useApp();
  const turnstile = useTurnstile(SITE_KEY);

  const [mode, setMode] = useState(OIDC.passwordDisabled ? 'email' : 'password');
  const [step, setStep] = useState('credential'); // credential | totp | code | mfa
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [cred, setCred] = useState({ username: '', password: '' });
  const [mfaToken, setMfaToken] = useState('');
  const [emailState, setEmailState] = useState({ email: '', ticket: '' });
  const [countdown, setCountdown] = useState(0);
  const [backupMode, setBackupMode] = useState(false);
  const timerRef = useRef(null);

  useEffect(() => () => timerRef.current && clearInterval(timerRef.current), []);

  const startCountdown = (sec = 60) => {
    setCountdown(sec);
    if (timerRef.current) clearInterval(timerRef.current);
    timerRef.current = setInterval(() => {
      setCountdown((n) => {
        if (n <= 1) {
          clearInterval(timerRef.current);
          return 0;
        }
        return n - 1;
      });
    }, 1000);
  };

  /* ---------- 账号密码 ---------- */
  const submitPassword = async (values) => {
    setErr('');
    if (step !== 'totp' && SITE_KEY && !turnstile.getToken()) {
      setErr('请先完成下方的人机验证');
      return;
    }
    setBusy(true);
    try {
      if (step === 'totp') {
        if (backupMode) {
          await loginWithBackup({ mfaToken, backupCode: String(values.backup || '').trim() });
        } else {
          await loginPassword({ username: cred.username, mfaToken, totpCode: String(values.totp || '').trim() });
        }
        message.success('登录成功');
        return;
      }
      const res = await loginPassword({
        username: String(values.username || '').trim(),
        password: values.password,
        // 服务端 verifyTurnstileRequired 对空 token 会直接 400，所以这里先拦一道
        turnstileToken: turnstile.getToken(),
      });
      if (res.needMfa) {
        setCred({ username: String(values.username || '').trim() });
        setMfaToken(res.mfaToken || '');
        setStep('totp');
        message.info('该账号已开启双因素认证，请输入动态码');
      } else {
        message.success('登录成功');
      }
    } catch (e) {
      setErr(e.message || '登录失败');
      turnstile.reset();
    } finally {
      setBusy(false);
    }
  };

  /* ---------- 邮箱验证码 ---------- */
  const submitEmailSend = async (values) => {
    setErr('');
    if (SITE_KEY && !turnstile.getToken()) {
      setErr('请先完成下方的人机验证');
      return;
    }
    setBusy(true);
    try {
      const email = String(values.email || '').trim();
      await sendEmailCode({ email, turnstileToken: turnstile.getToken() });
      setEmailState({ email, ticket: '' });
      setStep('code');
      startCountdown(60);
      message.success('验证码已发送，请查收邮箱（10 分钟内有效）');
    } catch (e) {
      setErr(e.message || '发送失败');
      turnstile.reset();
    } finally {
      setBusy(false);
    }
  };

  const submitEmailVerify = async (values) => {
    setErr('');
    setBusy(true);
    try {
      const res = await verifyEmailCode({
        email: emailState.email,
        code: String(values.code || '').trim(),
      });
      if (res.needMfa) {
        setEmailState((s) => ({ ...s, ticket: res.ticket }));
        setStep('mfa');
        message.info('请输入动态验证码或备份码');
      } else {
        message.success('登录成功');
      }
    } catch (e) {
      setErr(e.message || '验证失败');
    } finally {
      setBusy(false);
    }
  };

  const submitEmailMfa = async (values) => {
    setErr('');
    setBusy(true);
    try {
      await verifyEmailMfa({
        ticket: emailState.ticket,
        totpCode: String(values.totp || '').trim(),
        backupCode: String(values.backup || '').trim(),
      });
      message.success('登录成功');
    } catch (e) {
      setErr(e.message || '验证失败');
    } finally {
      setBusy(false);
    }
  };

  const backToStart = () => {
    setErr('');
    setStep('credential');
    setEmailState({ email: '', ticket: '' });
  };

  const tabItems = useMemo(() => {
    const items = [];
    if (!OIDC.passwordDisabled) items.push({ key: 'password', label: '账号密码' });
    if (EMAIL_ENABLED) items.push({ key: 'email', label: '邮箱验证码' });
    return items;
  }, []);

  /**
   * 单点登录：后端 GET /api/oidc/login 会校验 Turnstile（?cf_turnstile_response=），
   * 所以不能简单 href 跳转，必须先把当前 widget 的 token 带上。
   */
  const startOidc = () => {
    let qs = '';
    if (SITE_KEY) {
      if (!turnstile.getToken()) {
        message.warning('请先完成下方的人机验证，再点单点登录');
        return;
      }
      qs = '?cf_turnstile_response=' + encodeURIComponent(turnstile.getToken());
    }
    window.location.href = '/api/oidc/login' + qs;
  };

  const emailSubStep = step === 'code' || step === 'mfa';

  return (
    <div className="pp-login">
      <div className="pp-login-wrap">
        {/* ---------- 左侧品牌区 ---------- */}
        <div className="pp-login-hero">
          <div>
            <div className="pp-login-brand">
              <div className="pp-brand-mark" style={{ width: 40, height: 40, borderRadius: 13, fontSize: 19 }}>
                <img src={LOGO_URL} alt="ProxyPilot" />
              </div>
              <div>
                <div style={{ color: '#fff', fontWeight: 650, fontSize: 15, lineHeight: 1.3 }}>ProxyPilot</div>
                <div style={{ color: '#64748b', fontSize: 11.5 }}>All-in-One Proxy Tool</div>
              </div>
            </div>

            <LoginAnimation />
          </div>

          <div style={{ position: 'relative', zIndex: 1, color: '#475569', fontSize: 11.5 }}>
            <Tag color="geekblue" style={{ background: 'rgba(99,102,241,.14)', border: 'none', color: '#a5b4fc' }}>
              {boot.version || 'V15.0'} · Ant Design
            </Tag>
            <span style={{ marginLeft: 8 }}>worker.js 提供全部后端能力</span>
          </div>
        </div>

        {/* ---------- 右侧表单区 ---------- */}
        <div className="pp-login-form">
          <h2>登录控制台</h2>
          <p className="pp-sub">
            {step === 'totp'
              ? '账号已启用双因素认证，请输入验证器动态码'
              : step === 'mfa'
                ? '请完成二次验证'
                : step === 'code'
                  ? `验证码已发送至 ${emailState.email}`
                  : '使用后台管理员账号登录'}
          </p>

          {!SITE_KEY && boot.turnstileRequired ? (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 16, borderRadius: 10 }}
              message="服务端开启了人机验证，但前端没有拿到站点密钥"
              description="Worker 环境变量里 TURNSTILE_SECRET_KEY 与 TURNSTILE_SITE_KEY 必须成对配置。只配了 secret 时，服务端会对所有登录请求返回「请完成人机验证」。"
            />
          ) : null}

          {OIDC_ERROR && !err ? (
            <Alert
              type="error"
              showIcon
              message="单点登录失败"
              description={OIDC_ERROR}
              style={{ marginBottom: 16, borderRadius: 10 }}
            />
          ) : null}

          {err ? (
            <Alert
              type="error"
              showIcon
              message={err}
              style={{ marginBottom: 16, borderRadius: 10 }}
              closable
              onClose={() => setErr('')}
            />
          ) : null}

          {tabItems.length > 1 ? (
            <Tabs
              activeKey={mode}
              onChange={(k) => {
                setMode(k);
                setErr('');
                setStep('credential');
                turnstile.reset();
              }}
              items={tabItems}
              style={{ marginTop: -6 }}
            />
          ) : (
            <div style={{ height: 10 }} />
          )}

          {/* 账号密码 */}
          {mode === 'password' && step !== 'totp' && (
            <Form layout="vertical" requiredMark={false} onFinish={submitPassword} size="large">
              <Form.Item name="username" rules={[{ required: true, message: '请输入用户名' }]}>
                <Input
                  prefix={<UserOutlined style={{ color: '#94a3b8' }} />}
                  placeholder="用户名"
                  autoComplete="username"
                  autoFocus
                />
              </Form.Item>
              <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
                <Input.Password
                  prefix={<LockOutlined style={{ color: '#94a3b8' }} />}
                  placeholder="密码"
                  autoComplete="current-password"
                />
              </Form.Item>
              {SITE_KEY ? (
                <div style={{ marginBottom: 16 }}>
                  <div className="pp-turnstile" ref={turnstile.slotRef} />
                  {turnstile.status === 'error' ? (
                    <div style={{ fontSize: 12, color: '#f43f5e', marginTop: 4, textAlign: 'center' }}>
                      人机验证加载失败，
                      <a onClick={() => turnstile.reload()}>点此重试</a>
                    </div>
                  ) : turnstile.status === 'loading' ? (
                    <div style={{ fontSize: 12, color: '#94a3b8', marginTop: 4, textAlign: 'center' }}>人机验证加载中…</div>
                  ) : null}
                </div>
              ) : null}
              <Button type="primary" htmlType="submit" block size="large" loading={busy} icon={<LoginOutlined />}>
                登录
              </Button>
            </Form>
          )}

          {/* 双因素：密码流 */}
          {mode === 'password' && step === 'totp' && (
            <Form layout="vertical" requiredMark={false} onFinish={submitPassword} size="large">
              {backupMode ? (
                <Form.Item name="backup" rules={[{ required: true, message: '请输入备份码' }]}>
                  <Input
                    prefix={<KeyOutlined style={{ color: '#94a3b8' }} />}
                    placeholder="备份码（如 A1B2C3D4）"
                    maxLength={20}
                    autoFocus
                    style={{ letterSpacing: 2, fontSize: 15 }}
                  />
                </Form.Item>
              ) : (
                <Form.Item name="totp" rules={[{ required: true, message: '请输入 6 位动态码' }]}>
                  <Input
                    prefix={<SafetyOutlined style={{ color: '#94a3b8' }} />}
                    placeholder="6 位动态验证码"
                    maxLength={6}
                    autoFocus
                    style={{ letterSpacing: 4, fontSize: 16 }}
                  />
                </Form.Item>
              )}
              <Button type="primary" htmlType="submit" block size="large" loading={busy}>
                {backupMode ? '用备份码登录' : '验证并登录'}
              </Button>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 8 }}>
                <Button
                  type="link"
                  onClick={() => {
                    setBackupMode((v) => !v);
                    setErr('');
                  }}
                  style={{ padding: 0 }}
                >
                  {backupMode ? '改用动态验证码' : '用备份码登录'}
                </Button>
                <Button type="link" onClick={backToStart} style={{ padding: 0 }}>
                  返回重新登录
                </Button>
              </div>
            </Form>
          )}

          {/* 邮箱：第一步 */}
          {mode === 'email' && !emailSubStep && (
            <Form layout="vertical" requiredMark={false} onFinish={submitEmailSend} size="large">
              <Form.Item
                name="email"
                rules={[
                  { required: true, message: '请输入邮箱' },
                  { type: 'email', message: '邮箱格式不正确' },
                ]}
              >
                <Input
                  prefix={<MailOutlined style={{ color: '#94a3b8' }} />}
                  placeholder="已绑定的管理员邮箱"
                  autoComplete="email"
                />
              </Form.Item>
              {SITE_KEY ? (
                <div style={{ marginBottom: 16 }}>
                  <div className="pp-turnstile" ref={turnstile.slotRef} />
                  {turnstile.status === 'error' ? (
                    <div style={{ fontSize: 12, color: '#f43f5e', marginTop: 4, textAlign: 'center' }}>
                      人机验证加载失败，
                      <a onClick={() => turnstile.reload()}>点此重试</a>
                    </div>
                  ) : turnstile.status === 'loading' ? (
                    <div style={{ fontSize: 12, color: '#94a3b8', marginTop: 4, textAlign: 'center' }}>人机验证加载中…</div>
                  ) : null}
                </div>
              ) : null}
              <Button type="primary" htmlType="submit" block size="large" loading={busy}>
                发送登录验证码
              </Button>
            </Form>
          )}

          {/* 邮箱：验证码 */}
          {mode === 'email' && step === 'code' && (
            <Form layout="vertical" requiredMark={false} onFinish={submitEmailVerify} size="large">
              <Form.Item name="code" rules={[{ required: true, message: '请输入 6 位验证码' }]}>
                <Input
                  prefix={<SafetyOutlined style={{ color: '#94a3b8' }} />}
                  placeholder="6 位数字验证码"
                  maxLength={6}
                  autoFocus
                  style={{ letterSpacing: 6, fontSize: 17 }}
                />
              </Form.Item>
              <Button type="primary" htmlType="submit" block size="large" loading={busy}>
                验证并登录
              </Button>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 10 }}>
                <Button type="link" size="small" onClick={backToStart} style={{ padding: 0 }}>
                  换个邮箱
                </Button>
                <Button
                  type="link"
                  size="small"
                  disabled={countdown > 0}
                  style={{ padding: 0 }}
                  onClick={() => submitEmailSend({ email: emailState.email })}
                >
                  {countdown > 0 ? `${countdown} 秒后重发` : '重新发送'}
                </Button>
              </div>
            </Form>
          )}

          {/* 邮箱：二次验证 */}
          {mode === 'email' && step === 'mfa' && (
            <Form layout="vertical" requiredMark={false} onFinish={submitEmailMfa} size="large">
              <Form.Item name="totp" label="动态验证码（或填下方备份码）">
                <Input placeholder="6 位动态码" maxLength={6} autoFocus />
              </Form.Item>
              <Form.Item name="backup" label="备份码">
                <Input placeholder="XXXX-XXXX（用备份码登录会消耗一次）" maxLength={20} />
              </Form.Item>
              <Button type="primary" htmlType="submit" block size="large" loading={busy}>
                验证并登录
              </Button>
              <Button type="link" block onClick={backToStart} style={{ marginTop: 8 }}>
                返回重新登录
              </Button>
            </Form>
          )}

          {OIDC.enabled ? (
            <>
              <Divider plain style={{ color: '#cbd5e1', fontSize: 12, margin: '20px 0 14px' }}>
                或
              </Divider>
              <Button block size="large" onClick={startOidc}>
                {OIDC.buttonText}
              </Button>
              {SITE_KEY ? (
                <div style={{ marginTop: 6, fontSize: 11.5, color: '#b6c2d1', lineHeight: 1.6 }}>
                  单点登录同样需要通过人机验证：先在上面完成验证码，再点此按钮。
                </div>
              ) : null}
            </>
          ) : null}

          {!EMAIL_ENABLED && !OIDC.enabled ? (
            <div style={{ marginTop: 18, fontSize: 12, color: '#b6c2d1', lineHeight: 1.7 }}>
              提示：邮箱验证码登录需要 Worker 配置 <code>EMAIL</code> 与 <code>SENDER_EMAIL</code>；
              单点登录需要 <code>OIDC_ISSUER</code> / <code>OIDC_CLIENT_ID</code>。
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
