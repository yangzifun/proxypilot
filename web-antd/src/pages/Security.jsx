import React, { useCallback, useEffect, useState } from 'react';
import {
  CheckCircleOutlined,
  KeyOutlined,
  LockOutlined,
  MailOutlined,
  MobileOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SafetyOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  Form,
  Input,
  QRCode,
  Row,
  Space,
  Steps,
  Tag,
  Typography,
} from 'antd';
import { api } from '../api';
import { readForm } from '../hooks';
import { useAuth } from '../auth';
import { CopyText, ErrorBlock, PageCard, StatCard } from '../components';
import EmailBindModal from '../components/EmailBind';
import { fmtAgo, fmtNum, fmtTime } from '../format';
import { chartPalette } from '../theme';

export default function Security() {
  const { user, superAdmin } = useAuth();
  const { message } = AntdApp.useApp();

  const [status, setStatus] = useState({ data: null, loading: true, error: null });
  const [emailOpen, setEmailOpen] = useState(false);
  const [pwForm] = Form.useForm();

  const loadStatus = useCallback(async () => {
    setStatus((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await api('mfa/status');
      setStatus({ data: res, loading: false, error: null });
    } catch (e) {
      setStatus({ data: null, loading: false, error: e });
    }
  }, []);

  useEffect(() => {
    loadStatus();
  }, [loadStatus]);

  const changePassword = async () => {
    const v = await readForm(pwForm);
    if (!v) return;
    try {
      await api('change-password', {
        method: 'POST',
        body: { oldPassword: v.oldPassword, newPassword: v.newPassword },
      });
      message.success('密码已更新');
      pwForm.resetFields();
    } catch (e) {
      message.error(e.message || '修改失败');
    }
  };

  const mfaEnabled = !!(status.data && status.data.mfa_enabled);

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={12} lg={6}>
          <StatCard
            icon={<SafetyCertificateOutlined />}
            label="双因素认证"
            value={mfaEnabled ? '已启用' : '未启用'}
            accent={mfaEnabled ? chartPalette[3] : chartPalette[2]}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
            icon={<KeyOutlined />}
            label="剩余备份码"
            value={fmtNum(status.data && status.data.backup_codes_remaining)}
            unit="个"
            accent={chartPalette[0]}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
            icon={<MobileOutlined />}
            label="最近 MFA 登录"
            value={status.data && status.data.last_mfa_login ? fmtAgo(status.data.last_mfa_login) : '—'}
            accent={chartPalette[5]}
            hint={status.data && status.data.last_mfa_login ? fmtTime(status.data.last_mfa_login) : '尚无记录'}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard icon={<SafetyOutlined />} label="当前账号" value={user || '—'} accent={chartPalette[1]} hint={user === superAdmin ? '超级管理员' : '管理员'} />
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={12}>
          <PageCard
            title={<span className="pp-section-title"><LockOutlined style={{ color: '#4f46e5' }} />修改密码</span>}
          >
            <Form form={pwForm} layout="vertical" requiredMark={false} onFinish={changePassword}>
              <Form.Item name="oldPassword" label="当前密码" rules={[{ required: true, message: '请输入当前密码' }]}>
                <Input.Password placeholder="当前密码" autoComplete="current-password" />
              </Form.Item>
              <Form.Item
                name="newPassword"
                label="新密码"
                rules={[
                  { required: true, message: '请输入新密码' },
                  { min: 6, message: '至少 6 位' },
                ]}
              >
                <Input.Password placeholder="至少 6 位" autoComplete="new-password" />
              </Form.Item>
              <Form.Item
                name="confirm"
                label="确认新密码"
                dependencies={['newPassword']}
                rules={[
                  { required: true, message: '请再次输入新密码' },
                  ({ getFieldValue }) => ({
                    validator(_, value) {
                      if (!value || getFieldValue('newPassword') === value) return Promise.resolve();
                      return Promise.reject(new Error('两次输入不一致'));
                    },
                  }),
                ]}
              >
                <Input.Password placeholder="再次输入" autoComplete="new-password" />
              </Form.Item>
              <Button type="primary" htmlType="submit" icon={<LockOutlined />}>
                更新密码
              </Button>
            </Form>

            <Divider style={{ margin: '20px 0 14px' }} />
            <Descriptions
              size="small"
              column={1}
              items={[
                { key: 'u', label: '登录账号', children: <b>{user || '—'}</b> },
                {
                  key: 'm',
                  label: 'MFA',
                  children: mfaEnabled ? <Tag color="success">已启用</Tag> : <Tag>未启用</Tag>,
                },
              ]}
            />
          </PageCard>
        </Col>

        <Col xs={24} xl={12}>
          {status.error ? <ErrorBlock error={status.error} onRetry={loadStatus} /> : null}
          <MfaPanel
            enabled={mfaEnabled}
            backupRemaining={status.data && status.data.backup_codes_remaining}
            onChanged={loadStatus}
          />
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={12}>
          <PageCard
            title={<span className="pp-section-title"><MailOutlined style={{ color: '#0ea5e9' }} />我的邮箱</span>}
            extra={<Button size="small" onClick={() => setEmailOpen(true)}>绑定 / 验证邮箱</Button>}
          >
            <div style={{ fontSize: 13, color: '#64748b', lineHeight: 1.9 }}>
              绑定并验证邮箱后，可以用「邮箱验证码」登录，也可关联 OIDC 单点登录。
              验证码会发送到你填写的地址，验证通过即绑定到当前账号。
            </div>
            <div style={{ marginTop: 10, fontSize: 12, color: '#94a3b8' }}>
              当前账号：<b style={{ color: '#334155' }}>{user || '—'}</b>
              {user === superAdmin ? '（超级管理员）' : '（管理员）'}
            </div>
          </PageCard>
        </Col>
      </Row>

      <EmailBindModal
        open={emailOpen}
        username={user || ''}
        email=""
        verified={false}
        onClose={() => setEmailOpen(false)}
        onChanged={() => loadStatus().catch(() => {})}
      />
    </div>
  );
}

/* ---------------- MFA ---------------- */
function MfaPanel({ enabled, backupRemaining, onChanged }) {
  const { message } = AntdApp.useApp();
  const [stage, setStage] = useState('idle'); // idle | scan | done
  const [busy, setBusy] = useState(false);
  const [init, setInit] = useState(null); // { secret, otpauth_url, qr_url }
  const [codes, setCodes] = useState([]);
  const [password, setPassword] = useState('');
  const [otp, setOtp] = useState('');

  useEffect(() => {
    if (!enabled && stage === 'done') setStage('idle');
  }, [enabled, stage]);

  const startBind = async () => {
    if (!password) {
      message.warning('请先输入当前登录密码');
      return;
    }
    setBusy(true);
    try {
      const res = await api('mfa/init', { method: 'POST', body: { password } });
      setInit(res);
      setStage('scan');
      setPassword('');
    } catch (e) {
      message.error(e.message || '发起绑定失败');
    } finally {
      setBusy(false);
    }
  };

  const confirmBind = async () => {
    if (!/^\d{6}$/.test(otp)) {
      message.warning('请输入 6 位动态码');
      return;
    }
    setBusy(true);
    try {
      const res = await api('mfa/verify-first', { method: 'POST', body: { totp_code: otp, secret: init.secret } });
      setCodes(res.backup_codes || []);
      setStage('done');
      setOtp('');
      message.success('双因素认证已启用');
      onChanged().catch(() => {});
    } catch (e) {
      message.error(e.message || '验证失败');
    } finally {
      setBusy(false);
    }
  };

  const disable = async () => {
    setBusy(true);
    try {
      await api('mfa/disable', { method: 'POST', body: { password } });
      message.success('已关闭双因素认证');
      setPassword('');
      setStage('idle');
      onChanged().catch(() => {});
    } catch (e) {
      message.error(e.message || '关闭失败');
    } finally {
      setBusy(false);
    }
  };

  const regen = async () => {
    setBusy(true);
    try {
      const res = await api('mfa/backup-codes/regenerate', { method: 'POST', body: { password } });
      setCodes(res.backup_codes || []);
      setStage('done');
      setPassword('');
      message.success('备份码已重新生成，旧备份码全部失效');
      onChanged().catch(() => {});
    } catch (e) {
      message.error(e.message || '生成失败');
    } finally {
      setBusy(false);
    }
  };

  const stepIndex = enabled ? 2 : stage === 'scan' ? 1 : stage === 'done' ? 2 : 0;

  return (
    <PageCard
      title={<span className="pp-section-title"><SafetyCertificateOutlined style={{ color: '#10b981' }} />双因素认证（TOTP）</span>}
      extra={enabled ? <Tag color="success">已启用</Tag> : <Tag color="warning">未启用</Tag>}
    >
      <Steps
        size="small"
        current={stepIndex}
        items={[{ title: '验证密码' }, { title: '扫码绑定' }, { title: '完成' }]}
        style={{ marginBottom: 20 }}
      />

      {!enabled && stage === 'idle' && (
        <>
          <Alert
            type="info"
            showIcon
            style={{ borderRadius: 10, marginBottom: 16 }}
            message="用任意 TOTP App 扫码即可"
            description="支持 Google Authenticator / Microsoft Authenticator / 1Password 等。启用后登录需要额外输入动态码。"
          />
          <Space orientation="vertical" style={{ width: '100%' }} size={12}>
            <Input.Password
              placeholder="当前登录密码"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              prefix={<LockOutlined style={{ color: '#94a3b8' }} />}
            />
            <Button type="primary" icon={<QrcodeOutlined />} onClick={startBind} loading={busy} block>
              开始绑定
            </Button>
          </Space>
        </>
      )}

      {!enabled && stage === 'scan' && init && (
        <Row gutter={18}>
          <Col xs={24} sm={10} style={{ textAlign: 'center' }}>
            <div
              style={{
                background: '#fff',
                padding: 10,
                border: '1px solid #eef2f7',
                borderRadius: 12,
                display: 'inline-block',
              }}
            >
              {/*
                二维码必须留「静默区」（四周 ≥4 个模块宽的纯白边），否则手机端扫码识别不了。
                antd 的 bordered=false 会去掉内边距，所以这里自己补 20px 白边；
                同时用 svg 渲染（任何缩放下边缘都清晰）+ 中等纠错等级。
              */}
              <QRCode
                value={init.otpauth_url}
                size={168}
                type="svg"
                errorLevel="M"
                bordered={false}
                style={{ padding: 20, background: '#fff', borderRadius: 10 }}
              />
            </div>
            <div style={{ marginTop: 8, fontSize: 12, color: '#94a3b8' }}>用 TOTP App 扫码</div>
          </Col>
          <Col xs={24} sm={14}>
            <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
              或手动输入密钥
            </div>
            <CopyText value={init.secret} display={<span className="pp-mono">{init.secret}</span>} />
            <Divider style={{ margin: '14px 0' }} />
            <Input
              placeholder="输入 App 生成的 6 位动态码"
              value={otp}
              onChange={(e) => setOtp(e.target.value.replace(/\D/g, '').slice(0, 6))}
              maxLength={6}
              style={{ letterSpacing: 5, fontSize: 16, textAlign: 'center' }}
            />
            <Space style={{ marginTop: 14, width: '100%' }}>
              <Button type="primary" onClick={confirmBind} loading={busy}>
                完成启用
              </Button>
              <Button onClick={() => { setStage('idle'); setInit(null); setOtp(''); }}>取消</Button>
            </Space>
          </Col>
        </Row>
      )}

      {stage === 'done' && (
        <>
          <Alert
            type="success"
            showIcon
            style={{ borderRadius: 10, marginBottom: 14 }}
            message="请立即保存这些备份码"
            description="每个备份码只能使用一次；丢失设备时可用来登录。关闭弹窗后无法再次查看。"
          />
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(112px, 1fr))',
              gap: 8,
              background: '#f8fafc',
              border: '1px solid #eef2f7',
              borderRadius: 12,
              padding: 14,
            }}
          >
            {codes.map((c) => (
              <span key={c} className="pp-mono" style={{ fontSize: 13, color: '#334155', letterSpacing: 0.5 }}>
                {c}
              </span>
            ))}
          </div>
          <Space style={{ marginTop: 14 }}>
            <CopyText value={codes.join('\n')} display={<Button size="small">复制全部备份码</Button>} />
            <Button size="small" onClick={() => setStage(enabled ? 'idle' : 'idle')}>
              我已保存
            </Button>
          </Space>
        </>
      )}

      {enabled && stage !== 'done' && (
        <>
          <Alert
            type="success"
            showIcon
            style={{ borderRadius: 10, marginBottom: 16 }}
            message="双因素认证已开启"
            description={`剩余可用备份码 ${fmtNum(backupRemaining)} 个。登录时需要输入 App 动态码，或使用备份码。`}
          />
          <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
            敏感操作需要再次输入当前密码
          </div>
          <Input.Password
            placeholder="当前登录密码"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            prefix={<LockOutlined style={{ color: '#94a3b8' }} />}
            style={{ marginBottom: 12 }}
          />
          <Space wrap>
            <Button icon={<ReloadOutlined />} onClick={regen} loading={busy} disabled={!password}>
              重新生成备份码
            </Button>
            <Button danger icon={<WarningOutlined />} onClick={disable} loading={busy} disabled={!password}>
              关闭双因素认证
            </Button>
          </Space>
        </>
      )}
    </PageCard>
  );
}
