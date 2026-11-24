import React, { useEffect, useState } from 'react';
import {
  ApiOutlined,
  CloudOutlined,
  GlobalOutlined,
  LockOutlined,
  MailOutlined,
  ReloadOutlined,
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
  Spin,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { api } from '../api';
import { useAuth } from '../auth';
import { useAsync } from '../hooks';
import { ErrorBlock, PageCard, StatCard, Toolbar } from '../components';
import { fmtBytes, fmtNum, fmtTime, shortUuid } from '../format';
import MailBuiltinGuide from '../components/MailBuiltinGuide';

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
  const { isSuper } = useAuth();
  const [form] = Form.useForm();
  const mailProvider = Form.useWatch('mail_provider', form);
  const settings = useAsync(() => api('settings'), []);
  const vpnSettings = useAsync(() => api('admin/vpn/settings'), []);
  const mailGuide = useAsync(() => api('admin/mail/guide'), []);
  const dohConfig = useAsync(() => api('doh/config'), []);
  const dohStats30 = useAsync(() => api('doh/stats', { query: { range: '30d' } }), []);
  const dohStats24 = useAsync(() => api('doh/stats', { query: { range: '24h' } }), []);
  const [saving, setSaving] = useState(false);
  const [checking, setChecking] = useState(false);
  const [checkResult, setCheckResult] = useState(null);
  const [hasSecret, setHasSecret] = useState({});
  // DoH 服务配置（独立接口 /api/doh/config，不进入通用设置表单）
  const [doh, setDoh] = useState({
    doh_enabled: false,
    doh_domain: 'proxypilot.yangzifun.org',
    doh_upstreams: '',
    doh_max_qps: 30,
    doh_log_queries: false,
  });
  const [dohSaving, setDohSaving] = useState(false);

  useEffect(() => {
    const raw = dohConfig.data;
    if (!raw) return;
    const cfg = raw.data || raw;
    const patch = {};
    const enabled = cfg.doh_enabled !== undefined ? cfg.doh_enabled : cfg.enabled;
    if (enabled !== undefined) patch.doh_enabled = !!enabled;
    const domain = cfg.doh_domain !== undefined ? cfg.doh_domain : cfg.domain;
    if (domain !== undefined) patch.doh_domain = String(domain || '');
    const upstreams = cfg.doh_upstreams !== undefined ? cfg.doh_upstreams : cfg.upstreams;
    if (upstreams !== undefined) {
      patch.doh_upstreams = Array.isArray(upstreams) ? upstreams.join('\n') : String(upstreams || '');
    }
    const qps = cfg.doh_max_qps !== undefined ? cfg.doh_max_qps : cfg.max_qps;
    if (qps !== undefined) patch.doh_max_qps = Number(qps) || 30;
    const logQueries = cfg.doh_log_queries !== undefined ? cfg.doh_log_queries : cfg.log_queries;
    if (logQueries !== undefined) patch.doh_log_queries = !!logQueries;
    setDoh((prev) => ({ ...prev, ...patch }));
  }, [dohConfig.data]);

  useEffect(() => {
    const d = settings.data;
    if (!d) return;
    setHasSecret(d.has || {});
    const v = d.values || {};
    const initialValues = {
      ...v,
      auth_password_enabled: v.auth_password_enabled === '1',
      agent_mesh_enabled: v.agent_mesh_enabled === '1',
      agent_mesh_sync: v.agent_mesh_sync === '1',
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
    };
    // 组网中心域名以通用 settings 的 agent_mesh_center 为准；为空时先不覆盖，
    // 由 vpnSettings 里旧的 server_mesh_domain 回填（兼容历史数据）。
    if (!String(v.agent_mesh_center || '').trim()) delete initialValues.agent_mesh_center;
    form.setFieldsValue(initialValues);
    // Agent 访问地址：优先读数组；兼容旧的单地址配置。
    let urls = [];
    try {
      urls = v.agent_access_urls ? JSON.parse(v.agent_access_urls) : [];
    } catch (e) {
      urls = [];
    }
    if (!Array.isArray(urls) || urls.length === 0) {
      if (v.agent_internal_url) urls.push(v.agent_internal_url);
      if (v.agent_fallback_url) urls.push(v.agent_fallback_url);
    }
    form.setFieldsValue({ agent_access_urls: urls });
  }, [settings.data, form]);

  useEffect(() => {
    // 等 VPN 设置真正加载完成再回填，避免先用默认值占位导致旧 server_mesh_domain 无法回填
    if (!vpnSettings.data) return;
    const d = vpnSettings.data || {};
    const patch = {
      server_mesh_ip4: d.server_mesh_ip4 || '10.99.0.1',
      mesh_cidr4: d.mesh_cidr4 || '10.99.0.0/16',
    };
    // 旧数据兼容：通用设置没有 agent_mesh_center 时，用 VPN 设置的旧 server_mesh_domain
    // 回填「组网中心域名」唯一输入（页面不再单独维护 server_mesh_domain）。
    if (!String(form.getFieldValue('agent_mesh_center') || '').trim()) {
      patch.agent_mesh_center = d.server_mesh_domain || 'vpncenter.yzfn.top';
    }
    form.setFieldsValue(patch);
  }, [vpnSettings.data, form]);

  const save = async () => {
    const values = await form.validateFields();
    const body = {};
    Object.entries(values).forEach(([k, v]) => {
      if (k === 'server_mesh_domain' || k === 'server_mesh_ip4' || k === 'mesh_cidr4') {
        return;
      }
      if (k === 'agent_access_urls') {
        const arr = (Array.isArray(v) ? v : []).map((x) => String(x || '').trim()).filter(Boolean);
        body[k] = JSON.stringify(arr);
        return;
      }
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
      // 组网中心域名只维护一个输入：同步到 VPN 设置的 server_mesh_domain（空则回退默认）
      const meshCenter = String(values.agent_mesh_center || '').trim() || 'vpncenter.yzfn.top';
      await api('admin/vpn/settings', {
        method: 'PUT',
        body: {
          ...(vpnSettings.data || {}),
          server_mesh_domain: meshCenter,
          server_mesh_ip4: values.server_mesh_ip4,
          mesh_cidr4: values.mesh_cidr4,
        },
      });
      settings.setData(res);
      message.success('设置已保存，立即生效');
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const saveDoh = async () => {
    setDohSaving(true);
    try {
      const upstreams = String(doh.doh_upstreams || '')
        .split(/[\n,，]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      await api('doh/config', {
        method: 'PUT',
        body: {
          doh_enabled: !!doh.doh_enabled,
          doh_domain: String(doh.doh_domain || '').trim(),
          doh_upstreams: upstreams,
          doh_max_qps: Number(doh.doh_max_qps) || 30,
          doh_log_queries: !!doh.doh_log_queries,
        },
      });
      message.success('DoH 配置已保存');
    } catch (e) {
      message.error(e.message || 'DoH 配置保存失败');
    } finally {
      setDohSaving(false);
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

  // DoH 统计：接口按小时聚合，range=30d 为近 30 天，range=24h 为近 24 小时。
  const dohRows30 = (dohStats30.data && dohStats30.data.data) || [];
  const dohRows24 = (dohStats24.data && dohStats24.data.data) || [];
  const dohStatsLoading = dohStats30.loading || dohStats24.loading;
  const dohStatsError = dohStats30.error || dohStats24.error;
  const sumDohQueries = (rows) => rows.reduce((acc, r) => acc + (Number(r.queries) || 0), 0);
  const sumDohBytes = (rows) => rows.reduce((acc, r) => acc + (Number(r.bytes_in) || 0) + (Number(r.bytes_out) || 0), 0);
  const dohTopRows = dohRows30.slice(0, 10);
  const refreshDohStats = () => {
    Promise.all([
      dohStats30.reload().catch(() => {}),
      dohStats24.reload().catch(() => {}),
    ]);
  };

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
      {!isSuper ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message="安全敏感设置仅超级管理员可修改"
          description="分发基址（Agent / sing-box / 组件版本与校验值）、Agent 接入与组网端点、登录方式与 OIDC 身份源、各类密钥，只有超级管理员可改动；修改其它设置不受影响。"
        />
      ) : null}

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
                    extra="自建直投：面板直接连收件方 MX，只需填域名与发件人；auto：自建直投可用则用它，否则 SMTP"
                  >
                    <Select
                      options={[
                        { value: 'builtin', label: '自建直投（不需要第三方）' },
                        { value: 'auto', label: '自动（推荐）' },
                        { value: 'smtp', label: 'SMTP' },
                      ]}
                    />
                  </Form.Item>

                  {mailProvider === 'builtin' ? (
                    <>
                      <Divider orientation="left">自建直投</Divider>
                      <Form.Item
                        name="mail_domain"
                        label="发信域名"
                        extra="用于 SPF / DKIM / DMARC 与 Message-ID，例如 proxypilot.yangzifun.org"
                        rules={[
                          {
                            pattern: /^[a-z0-9.-]+\.[a-z]{2,}$/i,
                            message: '请输入合法域名，例如 proxypilot.yangzifun.org',
                          },
                        ]}
                      >
                        <Input placeholder="proxypilot.yangzifun.org" allowClear />
                      </Form.Item>
                      <Form.Item
                        name="mail_from"
                        label="发件人邮箱"
                        extra="必须属于上面的域名，例如 noreply@proxypilot.yangzifun.org"
                        rules={[{ type: 'email', message: '请输入合法邮箱' }]}
                      >
                        <Input placeholder="noreply@proxypilot.yangzifun.org" allowClear />
                      </Form.Item>
                      <Form.Item
                        name="mail_dkim_selector"
                        label="DKIM selector"
                        extra="默认 pp，对应 TXT 记录 pp._domainkey.<域名>。改过 selector 后要同步改 DNS。"
                      >
                        <Input placeholder="pp" allowClear style={{ width: 200 }} />
                      </Form.Item>
                      <Form.Item
                        name="mail_egress_ip"
                        label="出口 IP（可选）"
                        extra="留空自动探测。SPF 里会写成 ip4:<此 IP>，写错会 softfail。"
                      >
                        <Input placeholder="自动探测" allowClear style={{ width: 240 }} />
                      </Form.Item>

                      <Divider orientation="left">配置向导</Divider>
                      {mailGuide.error ? (
                        <ErrorBlock error={mailGuide.error} onRetry={mailGuide.reload} />
                      ) : (
                        <MailBuiltinGuide
                          guide={mailGuide.data}
                          reload={mailGuide.reload}
                          form={form}
                        />
                      )}
                    </>
                  ) : null}

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
                    message="统一密钥：只用于「DNS 记录管理」与「R2 对象存储」"
                    description="权限需包含 Account → R2 → Object Read & Write；如需在发信向导里「一键写入 DNS 记录」，再加 Zone → Zone → Read 与 Zone → DNS → Edit。存储桶（Bucket / Endpoint / CDN）设置见下方「存储桶设置」。"
                  />
                  <Form.Item
                    name="cloudflare_api_token"
                    label="Cloudflare API Token"
                    extra={hasSecret.cloudflare_api_token ? '已配置（留空保持不变）' : 'DNS 与 R2 共用'}
                  >
                    <Input.Password
                      placeholder={hasSecret.cloudflare_api_token ? '已配置，留空保持不变' : '粘贴一个 API Token'}
                      autoComplete="new-password"
                    />
                  </Form.Item>
                  <Form.Item name="cloudflare_account_id" label="Account ID">
                    <Input placeholder="9ef942805abcaef4dbf6574131453c58" allowClear />
                  </Form.Item>
                  <Divider orientation="left">存储桶设置</Divider>
                  <Alert
                    type="info"
                    showIcon
                    style={{ marginBottom: 12 }}
                    message="本页保存后立即生效；也可在订阅模板页快速设置"
                    description="Bucket / Endpoint / CDN 公共地址等保存后立即写入系统设置；订阅模板页的「对象存储设置」读取同一份配置。"
                  />
                  <Form.Item
                    name="r2_bucket"
                    label="Bucket 名称"
                    rules={[{ required: true, message: '请填写 Bucket' }]}
                    extra="R2 控制台里创建的存储桶名，例如 proxypilot"
                  >
                    <Input placeholder="proxypilot" allowClear />
                  </Form.Item>
                  <Form.Item name="r2_endpoint" label="Endpoint" extra="留空按 Account ID 自动拼成 https://<account>.r2.cloudflarestorage.com">
                    <Input placeholder="https://<account>.r2.cloudflarestorage.com" allowClear />
                  </Form.Item>
                  <Form.Item name="cdn_base_url" label="CDN 公共地址" extra="R2 绑定的自定义域名，例如 https://cdn.example.com；留空则只用鉴权下载">
                    <Input placeholder="https://cdn.example.com" allowClear />
                  </Form.Item>
                  <Form.Item name="r2_account_id" label="Account ID（可选覆盖）" extra="一般留空，沿用上方统一配置的 Account ID">
                    <Input placeholder="留空沿用统一配置" allowClear />
                  </Form.Item>
                  <Form.Item name="r2_access_key_id" label="Access Key ID（可选）" extra="仅当不用统一 Token 换 S3 凭证时才需要填">
                    <Input placeholder="留空由 Token 自动换取" allowClear />
                  </Form.Item>
                  <Divider orientation="left">密钥检查</Divider>
                  <Space direction="vertical" style={{ width: '100%' }} size={10}>
                    <Space>
                      <Button icon={<SafetyCertificateOutlined />} loading={checking} onClick={checkCloudflare}>
                        检查已保存的 Cloudflare 配置
                      </Button>
                      <Typography.Text type="secondary">检查 Token 与 R2 读写权限</Typography.Text>
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
                  <Form.Item name="agent_binary_base_url" label="Agent 分发基址" extra="留空使用本站 /agent-dist；建议填写存储桶目录，如 https://s3.proxypilot.yangzifun.org/agent">
                    <Input placeholder="https://cdn.example.com/agent-dist" allowClear />
                  </Form.Item>
                  <Divider orientation="left">Agent 内网通信（自建组网）</Divider>
                  <Form.Item name="agent_mesh_enabled" label="启用节点自建组网" valuePropName="checked">
                    <Switch checkedChildren="开" unCheckedChildren="关" />
                  </Form.Item>
                  <Form.Item name="agent_mesh_center" label="组网中心域名" extra="该域名同时用于 Agent 组网与中心状态（自建组网），只需在此填写一次">
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
                  <Divider orientation="left">自建组网</Divider>
                  <Form.Item name="server_mesh_ip4" label="中心组网 IP">
                    <Input placeholder="10.99.0.1" allowClear />
                  </Form.Item>
                  <Form.Item name="mesh_cidr4" label="组网网段">
                    <Input placeholder="10.99.0.0/16" allowClear />
                  </Form.Item>

                  <Form.Item
                    label="数据同步地址（按优先级）"
                    extra="仅用于 Agent 内部数据同步，不包含组网；可添加多个地址，列表顺序即优先级，失败时按顺序回退"
                  >
                    <Form.List name="agent_access_urls">
                      {(fields, { add, remove, move }) => (
                        <>
                          {fields.map((field, idx) => (
                            <Space key={field.key} align="baseline" style={{ display: 'flex', marginBottom: 8 }}>
                              <Tag color={idx === 0 ? 'green' : 'default'}>{idx === 0 ? '优先' : `备用 ${idx}`}</Tag>
                              <Form.Item
                                name={field.name}
                                noStyle
                                rules={[{ required: true, message: '请输入地址' }, { type: 'url', message: '请输入合法 URL' }]}
                              >
                                <Input placeholder="http://10.99.0.1:8080" style={{ width: 360 }} />
                              </Form.Item>
                              <Button size="small" disabled={idx === 0} onClick={() => move(field.name, idx - 1)}>上移</Button>
                              <Button size="small" danger onClick={() => remove(field.name)}>删除</Button>
                            </Space>
                          ))}
                          <Button type="dashed" onClick={() => add('')}>添加地址</Button>
                        </>
                      )}
                    </Form.List>
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
            {
              key: 'doh',
              label: (
                <span>
                  <GlobalOutlined /> DoH 服务
                </span>
              ),
              children: (
                <>
                  <Alert
                    type="info"
                    showIcon
                    style={{ marginBottom: 16 }}
                    message="DoH（DNS over HTTPS）由面板服务器统一提供"
                    description="每位用户有独立链接 /dns-query/<uuid>；需先把 DoH 域名解析到面板服务器，并确保 HTTPS 证书覆盖该域名。"
                  />
                  {dohConfig.error ? (
                    <Alert
                      type="error"
                      showIcon
                      style={{ marginBottom: 16 }}
                      message={dohConfig.error.message || '读取 DoH 配置失败'}
                      action={
                        <Button size="small" onClick={() => dohConfig.reload && dohConfig.reload()}>
                          重试
                        </Button>
                      }
                    />
                  ) : null}
                  <div style={{ maxWidth: 620 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
                      <Switch
                        checked={!!doh.doh_enabled}
                        onChange={(v) => setDoh((d) => ({ ...d, doh_enabled: v }))}
                        checkedChildren="开"
                        unCheckedChildren="关"
                      />
                      <span>启用 DoH 服务</span>
                    </div>
                    <div style={{ marginBottom: 14 }}>
                      <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>DoH 域名</div>
                      <Input
                        value={doh.doh_domain}
                        onChange={(e) => setDoh((d) => ({ ...d, doh_domain: e.target.value }))}
                        placeholder="proxypilot.yangzifun.org"
                        allowClear
                      />
                    </div>
                    <div style={{ marginBottom: 14 }}>
                      <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
                        上游 DoH 列表（每行或逗号分隔）
                      </div>
                      <Input.TextArea
                        rows={3}
                        value={doh.doh_upstreams}
                        onChange={(e) => setDoh((d) => ({ ...d, doh_upstreams: e.target.value }))}
                        placeholder={'https://1.1.1.1/dns-query\nhttps://dns.google/dns-query'}
                      />
                    </div>
                    <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', alignItems: 'flex-end', marginBottom: 14 }}>
                      <div>
                        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>每用户 QPS 上限</div>
                        <InputNumber
                          min={1}
                          max={1000}
                          style={{ width: 140 }}
                          value={doh.doh_max_qps}
                          onChange={(v) => setDoh((d) => ({ ...d, doh_max_qps: Number(v) || 30 }))}
                        />
                      </div>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, paddingBottom: 4 }}>
                        <Switch
                          checked={!!doh.doh_log_queries}
                          onChange={(v) => setDoh((d) => ({ ...d, doh_log_queries: v }))}
                          size="small"
                        />
                        <span style={{ fontSize: 13 }}>记录查询日志（默认关闭）</span>
                      </div>
                    </div>
                    <div style={{ marginBottom: 16 }}>
                      <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
                        示例 URL（实际每位用户的 uuid 不同）
                      </div>
                      <Typography.Text code style={{ fontSize: 12 }}>
                        {`https://${String(doh.doh_domain || 'proxypilot.yangzifun.org')
                          .replace(/^https?:\/\//, '')
                          .replace(/\/+$/, '')}/dns-query/<uuid>`}
                      </Typography.Text>
                    </div>
                    <Button type="primary" icon={<SaveOutlined />} loading={dohSaving} onClick={saveDoh}>
                      保存 DoH 配置
                    </Button>
                  </div>

                  <div style={{ marginTop: 24 }}>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap', marginBottom: 12 }}>
                      <div>
                        <div className="pp-section-title">DoH 使用统计</div>
                        <div className="pp-muted" style={{ fontSize: 12, marginTop: 2 }}>
                          按 UUID 聚合；近 30 天与近 24 小时（接口按小时表统计，无自然日粒度）
                        </div>
                      </div>
                      <Button icon={<ReloadOutlined />} onClick={refreshDohStats} loading={dohStatsLoading}>
                        刷新统计
                      </Button>
                    </div>

                    {dohStatsError ? (
                      <ErrorBlock error={dohStatsError} onRetry={refreshDohStats} title="DoH 统计读取失败" />
                    ) : null}

                    <Spin spinning={dohStatsLoading}>
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(170px, 1fr))', gap: 12, marginBottom: 16 }}>
                        <StatCard
                          icon={<ApiOutlined />}
                          label="近 30 天查询量"
                          value={fmtNum(sumDohQueries(dohRows30))}
                          accent="var(--pp-chart-1)"
                        />
                        <StatCard
                          icon={<ThunderboltOutlined />}
                          label="近 24 小时查询量"
                          value={fmtNum(sumDohQueries(dohRows24))}
                          accent="var(--pp-chart-3)"
                          hint="接口无自然日粒度"
                        />
                        <StatCard
                          icon={<SafetyCertificateOutlined />}
                          label="活跃 UUID 数"
                          value={fmtNum(dohRows30.filter((r) => (Number(r.queries) || 0) > 0).length)}
                          accent="var(--pp-chart-4)"
                        />
                        <StatCard
                          icon={<CloudOutlined />}
                          label="近 30 天字节数"
                          value={fmtBytes(sumDohBytes(dohRows30))}
                          accent="var(--pp-chart-6)"
                        />
                      </div>

                      <Table
                        className="pp-table"
                        rowKey="uuid"
                        size="small"
                        loading={dohStatsLoading}
                        dataSource={dohTopRows}
                        pagination={false}
                        locale={{ emptyText: '近 30 天暂无 DoH 查询' }}
                        columns={[
                          {
                            title: 'UUID',
                            dataIndex: 'uuid',
                            render: (v) => (
                              <span className="pp-mono" title={v}>
                                {shortUuid(v)}
                              </span>
                            ),
                          },
                          {
                            title: '查询量',
                            dataIndex: 'queries',
                            width: 100,
                            align: 'right',
                            render: (v) => fmtNum(v),
                          },
                          {
                            title: '字节',
                            width: 110,
                            align: 'right',
                            render: (_, r) => fmtBytes((Number(r.bytes_in) || 0) + (Number(r.bytes_out) || 0)),
                          },
                          {
                            title: '最近使用',
                            dataIndex: 'last_used',
                            width: 170,
                            render: (v) => (v ? fmtTime(v) : '—'),
                          },
                        ]}
                      />
                    </Spin>
                  </div>
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

