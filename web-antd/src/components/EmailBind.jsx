import React, { useEffect, useState } from 'react';
import { Alert, App as AntdApp, Button, Input, Modal, Space, Steps, Tag } from 'antd';
import { CheckCircleOutlined, DisconnectOutlined, MailOutlined, SafetyOutlined } from '@ant-design/icons';
import { api } from '../api';

/**
 * 邮箱绑定 / 验证 / 解绑（系统管理与安全中心共用）
 *
 * 后端语义（worker.js handleAdminEmailCode / Verify）：
 *   · 发码与验证都接受 body.email —— 所以不需要先改账号邮箱、也不需要超管权限，
 *     验证通过后会把该邮箱直接写到账号上并置 email_verified=1。
 *   · 解绑只需要 username（本人或超管）。
 */
export default function EmailBindModal({ open, username, email, verified, canEdit = true, onClose, onChanged }) {
  const { message } = AntdApp.useApp();
  const [mail, setMail] = useState(email || '');
  const [code, setCode] = useState('');
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const [countdown, setCountdown] = useState(0);

  useEffect(() => {
    if (!open) return;
    setMail(email || '');
    setCode('');
    setErr('');
    setCountdown(0);
    setStep(0);
  }, [open, email]);

  useEffect(() => {
    if (countdown <= 0) return undefined;
    const t = setTimeout(() => setCountdown((n) => n - 1), 1000);
    return () => clearTimeout(t);
  }, [countdown]);

  const mailOk = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(String(mail).trim());

  const sendCode = async () => {
    if (!mailOk) {
      setErr('邮箱格式不正确');
      return;
    }
    setBusy('send');
    setErr('');
    try {
      const res = await api('admin/users/email/code', {
        method: 'POST',
        body: { username, email: mail.trim() },
      });
      message.success(`验证码已发送至 ${res.email || mail.trim()}`);
      setStep(1);
      setCountdown(60);
    } catch (e) {
      setErr(e.message || '发送失败');
    } finally {
      setBusy('');
    }
  };

  const verify = async () => {
    if (!/^\d{6}$/.test(String(code).trim())) {
      setErr('请输入 6 位数字验证码');
      return;
    }
    setBusy('verify');
    setErr('');
    try {
      await api('admin/users/email/verify', {
        method: 'POST',
        body: { username, code: String(code).trim(), email: mail.trim() },
      });
      message.success('邮箱验证通过，已绑定');
      setStep(2);
      onChanged && onChanged();
    } catch (e) {
      setErr(e.message || '验证失败');
    } finally {
      setBusy('');
    }
  };

  const unbind = async () => {
    setBusy('unbind');
    setErr('');
    try {
      await api('admin/users/email/unbind', { method: 'POST', body: { username } });
      message.success('邮箱已解绑');
      setMail('');
      setStep(0);
      onChanged && onChanged();
    } catch (e) {
      setErr(e.message || '解绑失败');
    } finally {
      setBusy('');
    }
  };

  return (
    <Modal
      open={open}
      title={`邮箱绑定 · ${username}`}
      onCancel={onClose}
      width={560}
      footer={<Button onClick={onClose}>关闭</Button>}
      destroyOnHidden
    >
      <div style={{ margin: '4px 0 16px', fontSize: 13, color: '#64748b' }}>
        当前邮箱：
        {email ? <span className="pp-mono">{email}</span> : '未绑定'}
        {verified ? (
          <Tag color="success" icon={<CheckCircleOutlined />} style={{ marginLeft: 8 }}>
            已验证
          </Tag>
        ) : email ? (
          <Tag color="warning" style={{ marginLeft: 8 }}>
            未验证
          </Tag>
        ) : null}
      </div>

      <Steps
        size="small"
        current={step}
        items={[{ title: '填写邮箱' }, { title: '验证码' }, { title: '完成' }]}
        style={{ marginBottom: 18 }}
      />

      {err ? (
        <Alert
          type="error"
          showIcon
          message={err}
          style={{ borderRadius: 10, marginBottom: 12 }}
          closable
          onClose={() => setErr('')}
        />
      ) : null}

      <Space orientation="vertical" style={{ width: '100%' }} size={12}>
        <Input
          value={mail}
          onChange={(e) => setMail(e.target.value)}
          placeholder="user@example.com"
          prefix={<MailOutlined style={{ color: '#94a3b8' }} />}
          disabled={!canEdit || step === 2}
        />
        <Space wrap>
          <Button
            type="primary"
            onClick={sendCode}
            loading={busy === 'send'}
            disabled={!canEdit || !mailOk || step === 2}
          >
            {countdown > 0 ? `${countdown} 秒后重发` : '发送验证码'}
          </Button>
          {email ? (
            <Button danger icon={<DisconnectOutlined />} onClick={unbind} loading={busy === 'unbind'} disabled={!canEdit}>
              解绑
            </Button>
          ) : null}
        </Space>

        {step >= 1 && step < 2 ? (
          <>
            <Input
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
              placeholder="6 位数字验证码"
              prefix={<SafetyOutlined style={{ color: '#94a3b8' }} />}
              maxLength={6}
              style={{ letterSpacing: 6, fontSize: 16 }}
            />
            <Button type="primary" onClick={verify} loading={busy === 'verify'}>
              验证并绑定
            </Button>
          </>
        ) : null}

        <div style={{ fontSize: 12, color: '#94a3b8', lineHeight: 1.8 }}>
          验证通过后，该邮箱才能用于「邮箱验证码登录」与 OIDC 账号关联；一个邮箱只能绑定一个账号。
        </div>
      </Space>
    </Modal>
  );
}
