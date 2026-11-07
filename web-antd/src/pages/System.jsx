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

  const userRows = (users.data && users.data.users) || [];


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
          <StatCard
            icon={<CheckCircleOutlined />}
            label="已绑定邮箱"
            value={fmtNum(userRows.filter((u) => String(u.email || '').trim()).length)}
            accent={chartPalette[5]}
          />
        </Col>
      </Row>

      <div style={{ marginTop: 16 }}>
        <UsersTab rows={userRows} state={users} self={user} superAdmin={superAdmin} />
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

/** 管理员账号（独立页面，已移除 API 密钥）。 */
export function AdminUsersPage() {
  return <SystemAccounts />;
}

/** 系统设置（独立页面）。 */
export function SystemSettingsPage() {
  return <Settings />;
}
