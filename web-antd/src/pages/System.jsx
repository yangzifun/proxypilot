import React, { useEffect, useMemo, useState } from 'react';
import {
  ApiOutlined,
  CheckCircleOutlined,
  DeleteOutlined,
  EditOutlined,
  KeyOutlined,
  LockOutlined,
  MailOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SyncOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Checkbox,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { api } from '../api';
import { useAsync, readForm } from '../hooks';
import { useAuth } from '../auth';
import { CopyText, ErrorBlock, IconAction, PageCard, StatCard, Toolbar } from '../components';
import EmailBindModal from '../components/EmailBind';
import Settings from './Settings';
import { fmtAgo, fmtNum, fmtTime } from '../format';
import { chartPalette } from '../theme';

const PERMISSIONS = [
  { value: 'groups:read', label: '配置分组 · 读取' },
  { value: 'groups:write', label: '配置分组 · 写入' },
  { value: 'configs:read', label: '节点配置 · 读取' },
  { value: 'configs:write', label: '节点配置 · 写入' },
  { value: 'templates:read', label: '模板 · 读取' },
  { value: 'templates:write', label: '模板 · 写入' },
];

function SystemAccounts() {
  const { user, superAdmin } = useAuth();
  const users = useAsync(() => api('admin/users'), []);
  const keys = useAsync(() => api('apikeys'), []);

  const userRows = (users.data && users.data.users) || [];
  const keyRows = (keys.data && keys.data.data) || [];

  const activeKeys = useMemo(() => keyRows.filter((k) => Number(k.enabled) === 1).length, [keyRows]);

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={12} lg={6}>
          <StatCard icon={<TeamOutlined />} label="管理员账号" value={fmtNum(userRows.length)} accent={chartPalette[0]} />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
            icon={<SafetyCertificateOutlined />}
            label="已开 MFA"
            value={fmtNum(userRows.filter((u) => Number(u.mfa_enabled) === 1).length)}
            accent={chartPalette[3]}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard icon={<KeyOutlined />} label="API 密钥" value={fmtNum(keyRows.length)} accent={chartPalette[2]} />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard icon={<CheckCircleOutlined />} label="启用中" value={fmtNum(activeKeys)} accent={chartPalette[5]} />
        </Col>
      </Row>

      <div style={{ marginTop: 16 }}>
        <Tabs
          defaultActiveKey="users"
          items={[
            { key: 'users', label: <span><UserOutlined /> 用户管理</span>, children: <UsersTab rows={userRows} state={users} self={user} superAdmin={superAdmin} /> },
            { key: 'keys', label: <span><KeyOutlined /> API 密钥</span>, children: <KeysTab rows={keyRows} state={keys} /> },
          ]}
        />
      </div>
    </div>
  );
}

/* ---------------- 用户 ---------------- */
function UsersTab({ rows, state, self, superAdmin }) {
  const { message } = AntdApp.useApp();
  const [compose, setCompose] = useState(null); // { mode, row }
  const [pwFor, setPwFor] = useState(null);
  const [emailFor, setEmailFor] = useState(null);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();
  const [pwForm] = Form.useForm();
  const [emailForm] = Form.useForm();

  const create = async () => {
    const values = await readForm(form);
    if (!values) return;
    setSaving(true);
    try {
      await api('admin/users', { method: 'POST', body: values });
      message.success('账号已创建');
      setCompose(null);
      form.resetFields();
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  const resetPassword = async () => {
    const values = await readForm(pwForm);
    if (!values) return;
    setSaving(true);
    try {
      await api('admin/users', { method: 'PUT', body: { username: pwFor.username, newPassword: values.newPassword } });
      message.success('密码已重置');
      setPwFor(null);
      pwForm.resetFields();
    } catch (e) {
      message.error(e.message || '重置失败');
    } finally {
      setSaving(false);
    }
  };

  const resetMfa = async (row) => {
    try {
      await api('admin/users', { method: 'PUT', body: { username: row.username, mfaReset: true } });
      message.success(`已清除 ${row.username} 的 MFA`);
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const remove = async (row) => {
    try {
      await api('admin/users', { method: 'DELETE', body: { username: row.username } });
      message.success('账号已删除');
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 64 },
    {
      title: '用户名',
      dataIndex: 'username',
      width: 180,
      render: (v) => (
        <Space size={6}>
          <b>{v}</b>
          {v === self ? <Tag color="geekblue" style={{ marginInlineEnd: 0 }}>当前账号</Tag> : null}
          {v === (window.__PP_BOOT__ && window.__PP_BOOT__.superAdmin) ? (
            <Tag color="gold" style={{ marginInlineEnd: 0 }}>超管</Tag>
          ) : null}
        </Space>
      ),
    },
    {
      title: '绑定邮箱',
      dataIndex: 'email',
      width: 240,
      render: (v, row) =>
        v ? (
          <Space size={6}>
            <span style={{ color: '#475569' }}>{v}</span>
            {Number(row.email_verified) === 1 ? (
              <Tooltip title={fmtTime(row.email_verified_at)}>
                <Tag color="success" style={{ marginInlineEnd: 0 }}>已验证</Tag>
              </Tooltip>
            ) : (
              <Tag style={{ marginInlineEnd: 0 }}>未验证</Tag>
            )}
          </Space>
        ) : (
          <span className="pp-muted">未绑定</span>
        ),
    },
    {
      title: 'MFA',
      dataIndex: 'mfa_enabled',
      width: 100,
      render: (v) => (Number(v) === 1 ? <Tag color="success">已启用</Tag> : <Tag>未启用</Tag>),
    },
    {
      title: '最近 MFA 登录',
      dataIndex: 'last_mfa_login',
      width: 150,
      render: (v) => <span className="pp-muted">{v ? fmtAgo(v) : '—'}</span>,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v) => <span className="pp-muted">{fmtTime(v)}</span>,
    },
    {
      title: '操作',
      width: 170,
      render: (_, row) => (
        <Space size={0}>
          <IconAction
            title="重置密码"
            icon={<LockOutlined />}
            onClick={() => {
              setPwFor(row);
              pwForm.resetFields();
            }}
          />
          <IconAction title="绑定邮箱" icon={<MailOutlined />} onClick={() => setEmailFor(row)} />
          <Popconfirm title="清除该账号的 MFA？" okText="清除" onConfirm={() => resetMfa(row)}>
            <Button
              type="text"
              size="small"
              icon={<SafetyCertificateOutlined />}
              disabled={Number(row.mfa_enabled) !== 1}
              aria-label="重置 MFA"
            />
          </Popconfirm>
          <Popconfirm
            title="删除账号？"
            okText="删除"
            okButtonProps={{ danger: true }}
            disabled={row.username === self}
            onConfirm={() => remove(row)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} disabled={row.username === self} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <PageCard
      title={<span className="pp-section-title">管理员账号</span>}
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => state.reload().catch(() => {})} loading={state.loading}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { setCompose(true); form.resetFields(); }}>
            新建账号
          </Button>
        </Space>
      }
      bodyStyle={{ padding: 0 }}
    >
      {state.error ? (
        <div style={{ padding: 18 }}>
          <ErrorBlock error={state.error} onRetry={() => state.reload().catch(() => {})} />
        </div>
      ) : null}
      <Table
        className="pp-table"
        rowKey="username"
        size="medium"
        loading={state.loading}
        dataSource={rows}
        columns={columns}
        scroll={{ x: 1240 }}
        pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 个账号` }}
      />

      <Modal
        open={!!compose}
        title="新建管理员账号"
        onCancel={() => setCompose(null)}
        onOk={create}
        confirmLoading={saving}
        okText="创建"
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 14 }}>
          <Form.Item
            name="username"
            label="用户名"
            rules={[
              { required: true, message: '请填写用户名' },
              { pattern: /^[A-Za-z0-9._@-]{1,64}$/, message: '仅支持字母、数字与 . _ @ -' },
            ]}
          >
            <Input placeholder="例如：ops-01" />
          </Form.Item>
          <Form.Item name="password" label="初始密码" rules={[{ required: true, min: 6, message: '至少 6 位' }]}>
            <Input.Password placeholder="至少 6 位" />
          </Form.Item>
          <Form.Item name="email" label="邮箱（可选）" rules={[{ type: 'email', message: '邮箱格式不正确' }]}>
            <Input placeholder="用于邮箱验证码登录" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={!!pwFor}
        title={pwFor ? `重置密码 · ${pwFor.username}` : ''}
        onCancel={() => setPwFor(null)}
        onOk={resetPassword}
        confirmLoading={saving}
        okText="重置"
        destroyOnHidden
      >
        <Form form={pwForm} layout="vertical" requiredMark={false} style={{ marginTop: 14 }}>
          <Form.Item name="newPassword" label="新密码" rules={[{ required: true, min: 6, message: '至少 6 位' }]}>
            <Input.Password placeholder="至少 6 位" />
          </Form.Item>
        </Form>
      </Modal>

      <EmailBindModal
        open={!!emailFor}
        username={emailFor ? emailFor.username : ''}
        email={emailFor ? emailFor.email : ''}
        verified={emailFor ? Number(emailFor.email_verified) === 1 : false}
        canEdit={!!emailFor && (emailFor.username === self || self === superAdmin)}
        directBind={!!emailFor && self === superAdmin && emailFor.username !== self}
        onClose={() => setEmailFor(null)}
        onChanged={() => state.reload().catch(() => {})}
      />
    </PageCard>
  );
}

/* ---------------- API 密钥 ---------------- */
function KeysTab({ rows, state }) {
  const { message, modal } = AntdApp.useApp();
  const [compose, setCompose] = useState(false);
  const [saving, setSaving] = useState(false);
  const [fresh, setFresh] = useState(null); // { key_name, api_key }
  const [permFor, setPermFor] = useState(null);
  const [exampleFor, setExampleFor] = useState(null);
  const [form] = Form.useForm();
  const [permForm] = Form.useForm();

  const savePerm = async () => {
    const v = await readForm(permForm);
    if (!v) return;
    setSaving(true);
    try {
      await api(`apikeys/${permFor.id}`, {
        method: 'PUT',
        body: { key_name: v.key_name, permissions: v.permissions },
      });
      message.success('密钥权限已更新');
      setPermFor(null);
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const create = async () => {
    const values = await readForm(form);
    if (!values) return;
    setSaving(true);
    try {
      const res = await api('apikeys', {
        method: 'POST',
        body: {
          key_name: values.key_name,
          permissions: values.permissions,
          expires_in_days: values.expires_in_days || 0,
        },
      });
      setCompose(false);
      form.resetFields();
      setFresh({ key_name: res.key_name, api_key: res.api_key, permissions: res.permissions });
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  const rotate = (row) => {
    modal.confirm({
      title: `轮换密钥「${row.key_name}」？`,
      content: '旧密钥立即失效，使用该密钥的脚本需要同步更新。',
      okText: '轮换',
      onOk: async () => {
        try {
          const res = await api(`apikeys/${row.id}/rotate`, { method: 'POST', body: {} });
          setFresh({ key_name: row.key_name, api_key: res.api_key, permissions: row.permissions });
          state.reload().catch(() => {});
        } catch (e) {
          message.error(e.message || '轮换失败');
        }
      },
    });
  };

  const toggle = async (row, enabled) => {
    try {
      await api(`apikeys/${row.id}`, { method: 'PUT', body: { enabled: enabled ? 1 : 0 } });
      message.success(enabled ? '密钥已启用' : '密钥已停用');
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const remove = async (row) => {
    try {
      await api(`apikeys/${row.id}`, { method: 'DELETE' });
      message.success('密钥已删除');
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 64 },
    {
      title: '名称',
      dataIndex: 'key_name',
      width: 180,
      render: (v) => <b>{v}</b>,
    },
    {
      title: '密钥指纹',
      dataIndex: 'key_hint',
      width: 150,
      render: (v) => <span className="pp-mono pp-muted">{v || '—'}</span>,
    },
    {
      title: '权限',
      dataIndex: 'permissions_text',
      width: 300,
      render: (v) => {
        const text = String(v || '');
        if (!text) return <span className="pp-muted">—</span>;
        return (
          <Space size={4} wrap>
            {text
              .split(/[，,]/)
              .map((s) => s.trim())
              .filter(Boolean)
              .slice(0, 6)
              .map((s) => (
                <Tag key={s} style={{ marginInlineEnd: 0, fontSize: 11.5 }}>
                  {s}
                </Tag>
              ))}
          </Space>
        );
      },
    },
    {
      title: '有效期',
      dataIndex: 'expires_at',
      width: 130,
      render: (v) =>
        Number(v) ? (
          <span className="pp-muted">{fmtTime(v)}</span>
        ) : (
          <Tag color="cyan" style={{ marginInlineEnd: 0 }}>长期</Tag>
        ),
    },
    {
      title: '调用次数',
      dataIndex: 'usage_count',
      width: 100,
      align: 'right',
      render: (v, row) => (
        <Tooltip title={row.last_used_at ? `最后使用 ${fmtTime(row.last_used_at)}` : '从未使用'}>
          <span style={{ fontVariantNumeric: 'tabular-nums' }}>{fmtNum(v)}</span>
        </Tooltip>
      ),
    },
    {
      title: '启用',
      dataIndex: 'enabled',
      width: 80,
      render: (v, row) => (
        <Switch size="small" checked={Number(v) === 1} onChange={(c) => toggle(row, c)} />
      ),
    },
    {
      title: '操作',
      width: 150,
      render: (_, row) => (
        <Space size={0}>
          <IconAction
            title="权限"
            icon={<EditOutlined />}
            onClick={() => {
              setPermFor(row);
              permForm.setFieldsValue({
                key_name: row.key_name,
                permissions: String(row.permissions || '')
                  .split(',')
                  .map((s) => s.trim())
                  .filter(Boolean),
              });
            }}
          />
          <IconAction title="调用示例" icon={<ApiOutlined />} onClick={() => setExampleFor(row)} />
          <IconAction title="轮换密钥" icon={<SyncOutlined />} onClick={() => rotate(row)} />
          <Popconfirm title="删除密钥？" okText="删除" okButtonProps={{ danger: true }} onConfirm={() => remove(row)}>
            <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <PageCard
      title={<span className="pp-section-title">API 密钥</span>}
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => state.reload().catch(() => {})} loading={state.loading}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCompose(true)}>
            新建密钥
          </Button>
        </Space>
      }
      bodyStyle={{ padding: 0 }}
    >
      <div style={{ padding: '14px 18px 0' }}>
        <Alert
          type="warning"
          showIcon
          style={{ borderRadius: 10, marginBottom: 14 }}
          message="密钥只在创建/轮换时显示一次"
          description="调用方式：请求头 X-API-Key。密钥仅能访问「配置分组 / 配置管理 / 模板」三类接口。"
        />
      </div>
      {state.error ? (
        <div style={{ padding: 18 }}>
          <ErrorBlock error={state.error} onRetry={() => state.reload().catch(() => {})} />
        </div>
      ) : null}
      <Table
        className="pp-table"
        rowKey="id"
        size="medium"
        loading={state.loading}
        dataSource={rows}
        columns={columns}
        scroll={{ x: 1240 }}
        pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 个密钥` }}
      />

      <Modal
        open={compose}
        title="新建 API 密钥"
        onCancel={() => setCompose(false)}
        onOk={create}
        confirmLoading={saving}
        okText="创建"
        destroyOnHidden
      >
        <Form
          form={form}
          layout="vertical"
          requiredMark={false}
          style={{ marginTop: 14 }}
          initialValues={{ permissions: ['groups:read'], expires_in_days: 0 }}
          preserve={false}
        >
          <Form.Item
            name="key_name"
            label="密钥名称"
            rules={[{ required: true, message: '请填写名称' }, { max: 60, message: '最多 60 字符' }]}
          >
            <Input placeholder="例如：ci-deploy" />
          </Form.Item>
          <Form.Item name="permissions" label="权限" rules={[{ required: true, message: '至少选择一项权限' }]}>
            <Checkbox.Group style={{ width: '100%' }}>
              <Row gutter={[8, 8]}>
                {PERMISSIONS.map((p) => (
                  <Col span={12} key={p.value}>
                    <Checkbox value={p.value}>{p.label}</Checkbox>
                  </Col>
                ))}
              </Row>
            </Checkbox.Group>
          </Form.Item>
          <Form.Item name="expires_in_days" label="有效期（天，0 = 长期）" initialValue={0}>
            <InputNumber min={0} max={3650} style={{ width: '100%' }} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={!!fresh}
        title="密钥已生成"
        onCancel={() => setFresh(null)}
        footer={
          <Button type="primary" onClick={() => setFresh(null)}>
            我已保存
          </Button>
        }
      >
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 16, borderRadius: 10 }}
          message="这是唯一一次显示明文密钥"
          description="关闭后无法再次查看，只能轮换。请立即复制保存到安全位置。"
        />
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
          {fresh && fresh.key_name}
        </div>
        <CopyText
          value={(fresh && fresh.api_key) || ''}
          display={<span className="pp-mono" style={{ wordBreak: 'break-all' }}>{fresh && fresh.api_key}</span>}
        />
      </Modal>

      {/* 权限编辑 */}
      <Modal
        open={!!permFor}
        title={permFor ? `编辑密钥权限 · ${permFor.key_name}` : ''}
        onCancel={() => setPermFor(null)}
        onOk={savePerm}
        confirmLoading={saving}
        okText="保存"
        destroyOnHidden
      >
        <Form form={permForm} layout="vertical" requiredMark={false} style={{ marginTop: 14 }}>
          <Form.Item name="key_name" label="密钥名称" rules={[{ required: true, message: '请填写名称' }, { max: 60 }]}>
            <Input />
          </Form.Item>
          <Form.Item name="permissions" label="权限" rules={[{ required: true, message: '至少选择一项权限' }]}>
            <Checkbox.Group style={{ width: '100%' }}>
              <Row gutter={[8, 8]}>
                {PERMISSIONS.map((p) => (
                  <Col span={12} key={p.value}>
                    <Checkbox value={p.value}>{p.label}</Checkbox>
                  </Col>
                ))}
              </Row>
            </Checkbox.Group>
          </Form.Item>
        </Form>
      </Modal>

      {/* 调用示例 */}
      <Modal
        open={!!exampleFor}
        title={exampleFor ? `调用示例 · ${exampleFor.key_name}` : ''}
        onCancel={() => setExampleFor(null)}
        footer={
          <Space>
            <CopyText value={exampleFor ? apiKeyExample(exampleFor) : ''} display={<Button>复制示例</Button>} />
            <Button type="primary" onClick={() => setExampleFor(null)}>
              关闭
            </Button>
          </Space>
        }
        width={720}
      >
        <pre className="pp-json" style={{ maxHeight: 420, whiteSpace: 'pre-wrap' }}>
          {exampleFor ? apiKeyExample(exampleFor) : ''}
        </pre>
      </Modal>
    </PageCard>
  );
}

/** 生成 X-API-Key 调用示例（纯前端拼，不消耗接口） */
function apiKeyExample(row) {
  const base = (typeof window !== 'undefined' && window.location.origin) || 'https://panel.example.com';
  return [
    '# X-API-Key 调用示例',
    '# 当前密钥：' + (row.key_hint || '***') + '（明文只在创建 / 轮换时显示一次）',
    '',
    '# 1) 读取配置分组（需要 groups:read）',
    'curl -s "' + base + '/api/uuids?page=1&size=10" -H "X-API-Key: <你的密钥>"',
    '',
    '# 2) 新增节点配置（需要 configs:write）',
    'curl -s -X POST "' + base + '/api/uuid-configs" \\',
    '  -H "X-API-Key: <你的密钥>" -H "Content-Type: application/json" \\',
    '  -d \'{"uuid":"11111111-1111-4111-8111-111111111111","config_data":"vless://uuid@host:443?type=ws#hk"}\'',
    '',
    '# 3) 读取订阅模板（需要 templates:read）',
    'curl -s "' + base + '/api/templates" -H "X-API-Key: <你的密钥>"',
  ].join('\n');
}

/** 系统管理：账号与密钥 + 系统设置合并为一个入口。 */
export default function System({ initialTab = 'accounts' }) {
  const [tab, setTab] = useState(initialTab || 'accounts');

  useEffect(() => {
    setTab(initialTab || 'accounts');
  }, [initialTab]);

  return (
    <Tabs
      activeKey={tab}
      onChange={setTab}
      items={[
        {
          key: 'accounts',
          label: '账号与密钥',
          children: <SystemAccounts />,
        },
        {
          key: 'settings',
          label: '系统设置',
          children: <Settings embedded />,
        },
      ]}
    />
  );
}
