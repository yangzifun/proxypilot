import React, { useEffect, useState } from 'react';
import {
  ApiOutlined,
  CloudOutlined,
  GlobalOutlined,
  LockOutlined,
  MailOutlined,
  SafetyCertificateOutlined,
  SaveOutlined,
  SettingOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Divider,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { api } from '../api';
import { useAsync } from '../hooks';
import { ErrorBlock, PageCard, Toolbar } from '../components';

const SECRETS = ['turnstile_secret', 'smtp_pass', 'oidc_client_secret', 'cloudflare_api_token'];

function BoolField({ name, label, extra }) {
  return (
    <Form.Item name={name} label={label} extra={extra} valuePropName="checked">
      <Switch checkedChildren="开" unCheckedChildren="关" />
    </Form.Item>
  );
}

export default function Settings({ embedded = false }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const mailProvider = Form.useWatch('mail_provider', form);
  const settings = useAsync(() => api('settings'), []);
  const [saving, setSaving] = useState(false);
  const [checking, setChecking] = useState(false);
  const [checkResult, setCheckResult] = useState(null);
  const [hasSecret, setHasSecret] = useState({});

  useEffect(() => {
    const d = settings.data;
    if (!d) return;
    setHasSecret(d.has || {});
    const v = d.values || {};
    form.setFieldsValue({
      ...v,
      auth_password_enabled: v.auth_password_enabled === '1',
      auth_email_code_enabled: v.auth_email_code_enabled === '1',
      auth_oidc_enabled: v.auth_oidc_enabled === '1',
      monitor_notify_enabled: v.monitor_notify_enabled === '1',
      monitor_notify_cooldown_min: Number(v.monitor_notify_cooldown_min) || 10,
      uuid_reconcile_interval_min: Number(v.uuid_reconcile_interval_min) || 30,
      batch_retention_days: Number(v.batch_retention_days) || 7,
      agent_task_retention_days: Number(v.agent_task_retention_days) || 7,
      metrics_retention_days: Number(v.metrics_retention_days) || 7,
      turnstile_secret: '',
      smtp_pass: '',
      oidc_client_secret: '',
    });
  }, [settings.data, form]);

  const save = async () => {
    const values = await form.validateFields();
    const body = {};
    Object.entries(values).forEach(([k, v]) => {
      if (SECRETS.includes(k)) {
        if (String(v || '').trim() !== '') body[k] = String(v).trim();
        return;
      }
      if (typeof v === 'boolean') body[k] = v ? '1' : '0';
      else if (v !== undefined && v !== null) body[k] = String(v).trim();
    });
    setSaving(true);
    try {
      const res = await api('settings', { method: 'PUT', body });
      settings.setData(res);
      message.success('设置已保存，立即生效');
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const checkCloudflare = async () => {
    setChecking(true);
    setCheckResult(null);
    try {
      const res = await api('cloudflare/check', { method: 'POST' });
      setCheckResult(res);
      if (res.overall) message.success('Cloudflare 配置检查全部通过');
      else message.warning('Cloudflare 配置存在问题，请查看检查结果');
    } catch (e) {
      message.error(e.message || '检查失败');
    } finally {
      setChecking(false);
    }
  };

  if (settings.error) {
    return <ErrorBlock error={settings.error} onRetry={() => settings.reload()} />;
  }
  const eff = (settings.data && settings.data.effective) || {};
  const status = [
    eff.password_enabled ? '密码登录' : null,
    eff.email_code_enabled ? '邮箱验证码' : null,
    eff.oidc_enabled ? 'OIDC 登录' : null,
  ].filter(Boolean);

  return (
    <PageCard
      title={embedded ? '运行期配置' : '系统设置'}
      loading={settings.loading}
      extra={
        <Space>
          <Tag color={eff.turnstile_required ? 'orange' : 'default'}>
            人机验证 {eff.turnstile_required ? '开启' : '关闭'}
          </Tag>
          <Tag color={eff.mail_configured ? 'green' : 'default'}>
            邮箱 {eff.mail_configured ? '已配置' : '未配置'}
          </Tag>
          <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>
            保存设置
          </Button>
        </Space>
      }
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={`当前可用登录方式：${status.length ? status.join(' / ') : '无（保存会被拒绝）'}`}
        description="以下配置可在后台直接维护，未填写的值回落环境变量。Secret 留空表示保持原值。"
      />

      <Form form={form} layout="vertical" onFinish={save}>
        <Tabs
          items={[
            {
              key: 'login',
              label: (
                <span>
                  <LockOutlined /> 登录与验证
                </span>
              ),
              children: (
                <>
                  <Divider orientation="left">登录方式</Divider>
                  <BoolField name="auth_password_enabled" label="账号密码登录" />
                  <BoolField name="auth_email_code_enabled" label="邮箱验证码登录" extra="需要先配置 SMTP" />
                  <BoolField name="auth_oidc_enabled" label="OIDC 单点登录（SSO）" extra="需要 Issuer 与 Client ID" />
                  <Divider orientation="left">
                    <SafetyCertificateOutlined /> 人机验证（Cloudflare Turnstile）
                  </Divider>
                  <Form.Item name="turnstile_site_key" label="Site Key">
                    <Input placeholder="1x00000000000000000000AA" allowClear />
                  </Form.Item>
                  <Form.Item
                    name="turnstile_secret"
                    label="Secret Key"
                    extra={hasSecret.turnstile_secret ? '已配置（留空保持不变）' : '未配置；不配置则关闭人机验证'}
                  >
                    <Input.Password placeholder={hasSecret.turnstile_secret ? '已配置，留空保持不变' : '未配置'} autoComplete="new-password" />
                  </Form.Item>
                  <Alert
                    type="warning"
                    showIcon
                    message="Turnstile 调用 Cloudflare siteverify 接口；留空 Secret 即关闭。"
                  />
                  <Divider orientation="left">
                    <GlobalOutlined /> SSO / OIDC
                  </Divider>
                  <Form.Item name="oidc_issuer" label="Issuer" rules={[{ type: 'url', message: '请输入合法 URL' }]}>
                    <Input placeholder="https://id.example.com" allowClear />
                  </Form.Item>
                  <Form.Item name="oidc_client_id" label="Client ID">
                    <Input allowClear />
                  </Form.Item>
                  <Form.Item
                    name="oidc_client_secret"
                    label="Client Secret"
                    extra={hasSecret.oidc_client_secret ? '已配置（留空保持不变）' : '公共客户端可留空'}
                  >
                    <Input.Password placeholder={hasSecret.oidc_client_secret ? '已配置，留空保持不变' : '可留空'} autoComplete="new-password" />
                  </Form.Item>
                  <Form.Item name="oidc_scopes" label="Scopes">
                    <Input placeholder="openid profile email" allowClear />
                  </Form.Item>
                  <Form.Item name="oidc_provider_name" label="显示名称">
                    <Input placeholder="企业 SSO" allowClear />
                  </Form.Item>
                  <Form.Item name="oidc_button_text" label="登录按钮文案">
                    <Input placeholder="使用单点登录（OIDC）" allowClear />
                  </Form.Item>
                  <Form.Item name="oidc_redirect_uri" label="回调地址" extra="留空按当前站点自动推导 /api/oidc/callback" rules={[{ type: 'url', message: '请输入合法 URL' }]}>
                    <Input placeholder="https://pp.example.com/api/oidc/callback" allowClear />
                  </Form.Item>
                  <Alert
                    type="info"
                    showIcon
                    message="管理员 OIDC：已绑定 (issuer, sub) 直接登录；邮箱命中已验证管理员邮箱时自动绑定；其他一律拒绝。"
                  />
                </>
              ),
            },
            {
              key: 'mail',
              label: (
                <span>
                  <MailOutlined /> 邮件 / 通知
                </span>
              ),
              children: (
                <>
                  <Divider orientation="left">发信通道</Divider>
                  <Form.Item
                    name="mail_provider"
                    label="通道"
                    extra="auto：已配置统一 Cloudflare API Token 时优先 Cloudflare，否则 SMTP"
                  >
                    <Select
                      options={[
                        { value: 'auto', label: '自动（推荐）' },
                        { value: 'cloudflare', label: 'Cloudflare Email Sending' },
                        { value: 'smtp', label: 'SMTP' },
                      ]}
                    />
                  </Form.Item>

                  {mailProvider === 'smtp' ? (
                    <>
                      <Divider orientation="left">SMTP</Divider>
                      <Form.Item name="smtp_addr" label="SMTP 地址" extra="host:port，例如 smtp.example.com:587">
                        <Input placeholder="smtp.example.com:587" allowClear />
                      </Form.Item>
                      <Form.Item name="smtp_user" label="用户名">
                        <Input placeholder="notice@example.com" allowClear />
                      </Form.Item>
                      <Form.Item
                        name="smtp_pass"
                        label="密码"
                        extra={hasSecret.smtp_pass ? '已配置（留空保持不变）' : '未配置'}
                      >
                        <Input.Password placeholder={hasSecret.smtp_pass ? '已配置，留空保持不变' : '未配置'} autoComplete="new-password" />
                      </Form.Item>
                      <Form.Item name="mail_from" label="发件人">
                        <Input placeholder="notice@example.com" allowClear />
                      </Form.Item>
                    </>
                  ) : null}

                  <Divider orientation="left">
                    <ThunderboltOutlined /> 离线通知
                  </Divider>
                  <BoolField name="monitor_notify_enabled" label="服务器离线 / 恢复时发送邮件" extra="需要可用的发信通道与收件邮箱" />
                  <Form.Item name="monitor_notify_emails" label="收件邮箱" extra="多个用逗号分隔">
                    <Input placeholder="ops@example.com,admin@example.com" allowClear />
                  </Form.Item>
                  <Form.Item name="monitor_notify_cooldown_min" label="通知冷却（分钟）">
                    <InputNumber min={1} max={1440} style={{ width: 180 }} />
                  </Form.Item>
                </>
              ),
            },
            {
              key: 'cloudflare',
              label: (
                <span>
                  <CloudOutlined /> Cloudflare（统一密钥）
                </span>
              ),
              children: (
                <>
                  <Alert
                    type="info"
                    showIcon
                    style={{ marginBottom: 12 }}
                    message="一个 Cloudflare API Token 同时用于 R2 对象存储与 Email Sending"
                    description="权限需包含：Account → R2 → Object Read & Write；Account → Email Sending → Edit。系统会用 Token 自动换取 R2 的 S3 凭证，无需再填 Access Key / Secret。"
                  />
                  <Form.Item
                    name="cloudflare_api_token"
                    label="Cloudflare API Token"
                    extra={hasSecret.cloudflare_api_token ? '已配置（留空保持不变）' : 'R2 + Email Sending 共用'}
                  >
                    <Input.Password
                      placeholder={hasSecret.cloudflare_api_token ? '已配置，留空保持不变' : '粘贴一个 API Token'}
                      autoComplete="new-password"
                    />
                  </Form.Item>
                  <Form.Item name="cloudflare_account_id" label="Account ID">
                    <Input placeholder="9ef942805abcaef4dbf6574131453c58" allowClear />
                  </Form.Item>
                  <Form.Item name="cloudflare_from" label="邮件发件地址" rules={[{ type: 'email', message: '请输入合法邮箱' }]}>
                    <Input placeholder="notice@yourdomain.com" allowClear />
                  </Form.Item>
                  <Divider orientation="left">R2 对象存储</Divider>
                  <Form.Item name="r2_bucket" label="Bucket">
                    <Input placeholder="proxypilot" allowClear />
                  </Form.Item>
                  <Form.Item name="r2_endpoint" label="Endpoint" extra="留空按 Account ID 自动生成">
                    <Input placeholder="https://<account>.r2.cloudflarestorage.com" allowClear />
                  </Form.Item>
                  <Form.Item name="cdn_base_url" label="CDN 公共地址" extra="R2 自定义域名，如 https://cdn.example.com；留空则只用鉴权下载">
                    <Input placeholder="https://cdn.example.com" allowClear />
                  </Form.Item>
                  <Typography.Text type="secondary">
                    兼容旧配置：如果 Token 为空，仍会回退使用环境变量或旧的 R2 Access Key。
                  </Typography.Text>
                  <Divider orientation="left">密钥检查</Divider>
                  <Space direction="vertical" style={{ width: '100%' }} size={10}>
                    <Space>
                      <Button icon={<SafetyCertificateOutlined />} loading={checking} onClick={checkCloudflare}>
                        检查已保存的 Cloudflare 配置
                      </Button>
                      <Typography.Text type="secondary">检查 Token、R2 读写与 Email Sending 权限</Typography.Text>
                    </Space>
                    {checkResult ? (
                      <Alert
                        type={checkResult.overall ? 'success' : 'warning'}
                        showIcon
                        message={checkResult.overall ? '检查通过' : '检查发现问题'}
                        description={
                          <Space direction="vertical" size={4} style={{ width: '100%' }}>
                            {(checkResult.checks || []).map((it) => (
                              <div key={it.key}>
                                <Tag color={it.ok ? 'success' : 'error'}>{it.ok ? '通过' : '失败'}</Tag>
                                <b>{it.name}</b>：{it.detail}
                              </div>
                            ))}
                          </Space>
                        }
                      />
                    ) : null}
                  </Space>
                </>
              ),
            },
            {
              key: 'misc',
              label: (
                <span>
                  <SettingOutlined /> 高级设置
                </span>
              ),
              children: (
                <>
                  <Form.Item name="site_public_base_url" label="对外访问地址" extra="用于 Agent 分发与 OIDC 回调推导" rules={[{ type: 'url', message: '请输入合法 URL' }]}>
                    <Input placeholder="https://pp.example.com" allowClear />
                  </Form.Item>
                  <Form.Item name="agent_binary_base_url" label="Agent 分发基址" extra="留空使用本站 /agent-dist">
                    <Input placeholder="https://cdn.example.com/agent-dist" allowClear />
                  </Form.Item>
                  <Divider orientation="left">Agent 内网通信（自建组网）</Divider>
                  <Form.Item name="agent_mesh_enabled" label="启用节点自建组网" valuePropName="checked">
                    <Switch checkedChildren="开" unCheckedChildren="关" />
                  </Form.Item>
                  <Form.Item name="agent_mesh_center" label="组网中心域名" extra="节点通过该域名与中心服务器建立自建组网">
                    <Input placeholder="vpncenter.yzfn.top" allowClear />
                  </Form.Item>
                  <Form.Item
                    name="agent_mesh_sync"
                    label="允许 Agent 通过组网内网同步数据"
                    extra="开启后 Agent 优先使用组网内网地址上报数据；组网不可用时按系统设置的回退策略"
                    valuePropName="checked"
                  >
                    <Switch checkedChildren="开" unCheckedChildren="关" />
                  </Form.Item>

                  <Form.Item
                    name="agent_internal_url"
                    label="Agent 访问地址（组网内网）"
                    extra="Agent 只通过组网内网访问面板，例如 http://100.96.0.18:80；不再提供公网 TLS 兜底"
                    rules={[{ type: 'url', message: '请输入合法 URL' }]}
                  >
                    <Input placeholder="http://100.96.0.18:80" allowClear />
                  </Form.Item>
                  <Form.Item
                    name="agent_fallback_url"
                    label="备用 Agent 访问地址"
                    extra="组网内网不可用时的兜底地址，例如 http://10.99.0.1:8080；留空表示不启用"
                  >
                    <Input placeholder="http://10.99.0.1:8080" allowClear />
                  </Form.Item>
                  <Form.Item
                    name="component_base_url"
                    label="sing-box 分发基址"
                    extra="自建 sing-box 单独一个目录（与 Agent 分开）；留空按 Agent 基址推导为同级 singbox/，仍为空则回落 GitHub Release"
                  >
                    <Input placeholder="https://s3.example.com/singbox" allowClear />
                  </Form.Item>
                  <Divider orientation="left">巡检与保留</Divider>
                  <Form.Item name="uuid_reconcile_interval_min" label="UUID 巡检间隔（分钟）">
                    <InputNumber min={5} max={1440} style={{ width: 180 }} />
                  </Form.Item>
                  <Form.Item name="batch_retention_days" label="批量任务保留（天）">
                    <InputNumber min={1} max={365} style={{ width: 180 }} />
                  </Form.Item>
                  <Form.Item name="agent_task_retention_days" label="Agent 任务保留（天）">
                    <InputNumber min={1} max={365} style={{ width: 180 }} />
                  </Form.Item>
                  <Form.Item name="metrics_retention_days" label="监控指标保留（天）">
                    <InputNumber min={1} max={365} style={{ width: 180 }} />
                  </Form.Item>
                  <Typography.Text type="secondary">
                    <ApiOutlined /> 保存后写入 app_settings，30 秒内全节点生效。
                  </Typography.Text>
                </>
              ),
            },
          ]}
        />
        <Toolbar right={<Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>保存设置</Button>} />
      </Form>
    </PageCard>
  );
}
