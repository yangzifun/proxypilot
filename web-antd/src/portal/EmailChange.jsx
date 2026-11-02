/* =================================================================
 *  用户中心 · 更换绑定邮箱（两步：发码 → 验证）
 *  主页面与 UUID 详情页共用
 * ================================================================= */
import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Input, Modal, Space } from 'antd';
import { portalToken } from './portalApi';

export default function EmailChange({ open, onClose, onDone, currentEmail }) {
  const { message } = AntdApp.useApp();
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [step, setStep] = useState('');
  const [sending, setSending] = useState(false);
  const [saving, setSaving] = useState(false);
  const [countdown, setCountdown] = useState(0);

  useEffect(() => {
    if (!open) {
      setEmail('');
      setCode('');
      setStep('');
      setCountdown(0);
      setSending(false);
      setSaving(false);
    }
  }, [open]);

  useEffect(() => {
    if (!countdown) return undefined;
    const t = setTimeout(() => setCountdown((n) => n - 1), 1000);
    return () => clearTimeout(t);
  }, [countdown]);

  const post = async (path, body) => {
    const res = await fetch('/api/' + path, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: 'Bearer ' + portalToken(),
      },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `请求失败（HTTP ${res.status}）`);
    return data;
  };

  const sendCode = async () => {
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) {
      message.error('请填写正确的邮箱');
      return;
    }
    if (currentEmail && email.trim().toLowerCase() === String(currentEmail).toLowerCase()) {
      message.error('新邮箱与当前邮箱相同');
      return;
    }
    setSending(true);
    try {
      await post('portal/email/send-code', { email: email.trim() });
      message.success('验证码已发送，10 分钟内有效');
      setStep('code');
      setCountdown(60);
    } catch (e) {
      message.error(e.message || '发送失败');
    } finally {
      setSending(false);
    }
  };

  const verify = async () => {
    if (!/^\d{6}$/.test(code)) {
      message.error('请输入 6 位验证码');
      return;
    }
    setSaving(true);
    try {
      const data = await post('portal/email/verify', { email: email.trim(), code });
      // 服务端会换发新令牌（sub 变成新邮箱），这里同步覆盖本地
      if (data && data.token) {
        try {
          localStorage.setItem('pp_portal_token', data.token);
        } catch (e) {
          /* ignore */
        }
      }
      message.success('邮箱已更换');
      if (onDone) onDone(data && data.email);
      if (onClose) onClose();
    } catch (e) {
      message.error(e.message || '换绑失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title="更换绑定邮箱"
      onCancel={onClose}
      footer={<Button onClick={onClose}>关闭</Button>}
      centered
      destroyOnHidden
      width={430}
    >
      <Space orientation="vertical" style={{ width: '100%' }} size={10}>
        <div className="pp-muted" style={{ fontSize: 12, lineHeight: 1.7 }}>
          验证码会发到<strong>新邮箱</strong>，验证通过后绑定关系立即生效，之后请用新邮箱接收登录验证码。
        </div>
        <Space.Compact style={{ width: '100%' }}>
          <Input
            placeholder="新邮箱地址"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={step === 'code'}
          />
          <Button onClick={sendCode} loading={sending} disabled={countdown > 0 || step === 'code'}>
            {countdown > 0 ? `${countdown}s` : '发送验证码'}
          </Button>
        </Space.Compact>
        {step === 'code' ? (
          <>
            <Input
              placeholder="6 位验证码"
              value={code}
              maxLength={6}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
              onPressEnter={verify}
            />
            <Button type="primary" block onClick={verify} loading={saving}>
              确认更换
            </Button>
          </>
        ) : null}
      </Space>
    </Modal>
  );
}
