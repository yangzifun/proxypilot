import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, App as AntdApp, Button, Divider, Form, Input, Modal, Tabs, Tag } from 'antd';
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
// 管理后台登录不再要求人机验证；用户中心仍独立使用 Turnstile。
// 固定为空，避免后台登录页加载/弹出 Cloudflare Turnstile。
const SITE_KEY = '';
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
  // 人机验证改为点击后在弹窗内完成（与用户中心一致）：
  // tsOpen 控制「人机验证」弹窗，pending 记录验证通过后要自动执行的动作。
  const [tsOpen, setTsOpen] = useState(false);
  const [pending, setPending] = useState(null); // { kind: 'pwd'|'send', values? }（SSO 直接跳转，不经过弹窗）

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
  // 真正发起密码登录：Turnstile 已通过（或环境未配置 SITE_KEY）后调用。
  // MFA/TOTP 二次验证阶段不再要求人机验证（第一步已消费 token）。
  const doPasswordLogin = async (values, tsToken) => {
    setErr('');
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
        turnstileToken: tsToken || turnstile.getToken(),
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

  const submitPassword = async (values) => {
    setErr('');
    if (step !== 'totp' && SITE_KEY && !turnstile.getToken()) {
      // 没有 token：先弹出人机验证，通过后自动用本次账号密码登录
      setPending({ kind: 'pwd', values });
      setTsOpen(true);
      return;
    }
    await doPasswordLogin(values, turnstile.getToken());
  };

  /* ---------- 邮箱验证码 ---------- */
  // 真正发送邮箱验证码：Turnstile 已通过（或环境未配置 SITE_KEY）后调用。
  const doSendEmail = async (values, tsToken) => {
    setErr('');
    setBusy(true);
    try {
      const email = String(values.email || '').trim();
      const res = await sendEmailCode({ email, turnstileToken: tsToken || turnstile.getToken() });
      // Turnstile token 是一次性的：成功/失败后都重置，避免 60 秒后「重新发送」复用
      // 已被消费的 token 被服务端 403（实测表现为“重发永远失败”）。
      turnstile.reset();
      setEmailState({ email, ticket: '' });
      setStep('code');
      startCountdown(60);
      message.success((res && res.message) || '验证码发送请求已受理，请查收邮箱（10 分钟内有效）');
    } catch (e) {
      turnstile.reset();
      setErr(e.message || '发送失败');
    } finally {
      setBusy(false);
    }
  };

  const submitEmailSend = async (values) => {
    setErr('');
    if (SITE_KEY && !turnstile.getToken()) {
      // 没有 token：先弹出人机验证，通过后自动发送（含「重新发送」）
      setPending({ kind: 'send', values });
      setTsOpen(true);
      return;
    }
    await doSendEmail(values, turnstile.getToken());
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
   * 单点登录：SSO 不做人机验证（与用户中心一致），直接跳转即可。
   * 后端 OIDC 的 Turnstile 校验已移除，不再需要携带 cf_turnstile_response。
   */
  const startOidc = () => {
    window.location.href = '/api/oidc/login';
  };

  // 人机验证通过后自动继续原操作（SSO 不做人机验证，不在此列）。
  useEffect(() => {
    if (!tsOpen || !turnstile.token || !pending) return;
    setTsOpen(false);
    const action = pending;
    setPending(null);
    const tsToken = turnstile.token;
    if (action.kind === 'pwd') {
      doPasswordLogin(action.values || {}, tsToken);
      return;
    }
    doSendEmail(action.values || {}, tsToken);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tsOpen, turnstile.token, pending]);

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
                <div style={{ color: 'var(--pp-bg-elevated)', fontWeight: 650, fontSize: 15, lineHeight: 1.3 }}>ProxyPilot</div>
                <div style={{ color: 'var(--pp-text-tertiary)', fontSize: 11.5 }}>All-in-One Proxy Tool</div>
              </div>
            </div>

            <LoginAnimation />
          </div>

          <div style={{ position: 'relative', zIndex: 1, color: 'var(--pp-text-tertiary)', fontSize: 11.5 }}>
            <Tag color="geekblue" style={{ background: 'var(--pp-primary-soft)', border: 'none', color: 'var(--pp-primary)' }}>
              {boot.version || '—'} · Ant Design
            </Tag>
            <span style={{ marginLeft: 8 }}>后端由 Go 服务端与节点 Agent 提供</span>
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
                  prefix={<UserOutlined style={{ color: 'var(--pp-text-tertiary)' }} />}
                  placeholder="用户名"
                  autoComplete="username"
                  autoFocus
                />
              </Form.Item>
              <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
                <Input.Password
                  prefix={<LockOutlined style={{ color: 'var(--pp-text-tertiary)' }} />}
                  placeholder="密码"
                  autoComplete="current-password"
                />
              </Form.Item>
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
                    prefix={<KeyOutlined style={{ color: 'var(--pp-text-tertiary)' }} />}
                    placeholder="备份码（如 A1B2C3D4）"
                    maxLength={20}
                    autoFocus
                    style={{ letterSpacing: 2, fontSize: 15 }}
                  />
                </Form.Item>
              ) : (
                <Form.Item name="totp" rules={[{ required: true, message: '请输入 6 位动态码' }]}>
                  <Input
                    prefix={<SafetyOutlined style={{ color: 'var(--pp-text-tertiary)' }} />}
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
                  prefix={<MailOutlined style={{ color: 'var(--pp-text-tertiary)' }} />}
                  placeholder="已绑定的管理员邮箱"
                  autoComplete="email"
                />
              </Form.Item>
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
                  prefix={<SafetyOutlined style={{ color: 'var(--pp-text-tertiary)' }} />}
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
              <Divider plain style={{ color: 'var(--pp-text-tertiary)', fontSize: 12, margin: '20px 0 14px' }}>
                或
              </Divider>
              <Button block size="large" onClick={startOidc}>
                {OIDC.buttonText}
              </Button>
            </>
          ) : null}

          {!EMAIL_ENABLED && !OIDC.enabled ? (
            <div style={{ marginTop: 18, fontSize: 12, color: 'var(--pp-text-tertiary)', lineHeight: 1.7 }}>
              提示：邮箱验证码登录需要先在系统设置中配置邮件服务；
              单点登录需要 <code>OIDC_ISSUER</code> / <code>OIDC_CLIENT_ID</code>。
            </div>
          ) : null}
        </div>
      </div>

      {/* 人机验证弹窗：仅密码登录 / 邮箱验证码发送需要；SSO 直接跳转不弹窗 */}
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
      >
        <div style={{ display: 'flex', justifyContent: 'center', padding: '8px 0 4px' }}>
          <div className="pp-turnstile" ref={turnstile.slotRef} />
        </div>
        {turnstile.status === 'error' ? (
          <div style={{ fontSize: 12, color: 'var(--pp-danger)', textAlign: 'center', marginTop: 8 }}>
            验证组件加载失败，<a onClick={() => turnstile.reload()}>点此重试</a>
          </div>
        ) : null}
        <div style={{ fontSize: 12, color: 'var(--pp-text-tertiary)', textAlign: 'center', marginTop: 10 }}>
          {pending && pending.kind === 'pwd'
            ? '完成验证后将自动登录'
            : '完成验证后将自动发送验证码'}
        </div>
      </Modal>
    </div>
  );
}
