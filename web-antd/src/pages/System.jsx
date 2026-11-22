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
  const { user, superAdmin, isSuper, logout } = useAuth();
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
        <UsersTab rows={userRows} state={users} self={user} superAdmin={superAdmin} isSuper={isSuper} onLogout={logout} />
      </div>
    </div>
  );
}

/* ---------------- 用户 ---------------- */
function UsersTab({ rows, state, self, superAdmin, isSuper, onLogout }) {
  const { message } = AntdApp.useApp();
  const [compose, setCompose] = useState(null); // { mode, row }
  const [pwFor, setPwFor] = useState(null);
  const [emailFor, setEmailFor] = useState(null);
  const [renameFor, setRenameFor] = useState(null);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();
  const [pwForm] = Form.useForm();
  const [emailForm] = Form.useForm();
  const [renameForm] = Form.useForm();

  // 角色行判定（兼容后端 0/1 与 bool）：root 行不可降级
  const isRootRow = (row) => !!(row && (row.is_root === true || row.is_root === 1 || row.is_root === '1' || String(row.username || '') === superAdmin));
  const isSuperRow = (row) => !!(row && (row.is_super === true || row.is_super === 1 || row.is_super === '1'));

  const toggleSuper = async (row) => {
    const next = !isSuperRow(row);
    try {
      await api('admin/users', { method: 'PUT', body: { username: row.username, is_super: next } });
      message.success(next ? `已将 ${row.username} 设为超级管理员` : `已取消 ${row.username} 的超级管理员`);
      state.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '角色修改失败');
    }
  };

  // 修改用户名（root 不可改；改当前账号后需重新登录）
  const renameUser = async () => {
    const values = await readForm(renameForm);
    if (!values) return;
    const target = renameFor;
    if (!target) return;
    const newUsername = String(values.newUsername || '').trim();
    if (newUsername === target.username) {
      message.warning('新用户名与当前用户名相同');
      return;
    }
    setSaving(true);
    try {
      const res = await api('admin/users', { method: 'PUT', body: { username: target.username, newUsername } });
      setRenameFor(null);
      renameForm.resetFields();
      const needRelogin = !!(res && res.relogin_required) || target.username === self || isRootRow(target);
      if (needRelogin) {
        message.warning('用户名已修改，请重新登录');
        if (onLogout) onLogout();
      } else {
        message.success(`用户名已修改：${target.username} → ${newUsername}`);
        state.reload().catch(() => {});
      }
    } catch (e) {
      message.error(e.message || '修改用户名失败');
    } finally {
      setSaving(false);
    }
  };

  const create = async () => {
    const values = await readForm(form);
    if (!values) return;
    setSaving(true);
    try {
      await api('admin/users', { method: 'POST', body: { ...values, is_super: !!values.is_super } });
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
      render: (v, row) => {
        const root = isRootRow(row);
        const sup = isSuperRow(row);
        return (
          <Space size={6}>
            <b>{v}</b>
            {v === self ? <Tag color="geekblue" style={{ marginInlineEnd: 0 }}>当前账号</Tag> : null}
            {root ? (
              <Tag color="red" style={{ marginInlineEnd: 0 }}>超管（根）</Tag>
            ) : sup ? (
              <Tag color="gold" style={{ marginInlineEnd: 0 }}>超管</Tag>
            ) : (
              <Tag style={{ marginInlineEnd: 0 }}>管理员</Tag>
            )}
          </Space>
        );
      },
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
      width: 340,
      render: (_, row) => (
        <Space size={0}>
          {isSuper ? (
            isRootRow(row) ? (
              <Tooltip title="PP_SUPER_ADMIN 根超管不可降级">
                <Button type="text" size="small" disabled>
                  根超管
                </Button>
              </Tooltip>
            ) : (
              <Button type="text" size="small" onClick={() => toggleSuper(row)}>
                {isSuperRow(row) ? '取消超管' : '设为超管'}
              </Button>
            )
          ) : null}
          {isSuper ? (
            <Button
              type="text"
              size="small"
              onClick={() => {
                setRenameFor(row);
                renameForm.resetFields();
              }}
            >
              改名
            </Button>
          ) : null}
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
          <Form.Item
            name="is_super"
            label="设为超级管理员"
            valuePropName="checked"
            extra="超级管理员可管理其它管理员账号；创建后也可在列表中调整"
          >
            <Switch />
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

      <Modal
        open={!!renameFor}
        title={renameFor ? `修改用户名 · ${renameFor.username}` : ''}
        onCancel={() => {
          setRenameFor(null);
          renameForm.resetFields();
        }}
        onOk={renameUser}
        confirmLoading={saving}
        okText="保存"
        destroyOnHidden
      >
        <Form form={renameForm} layout="vertical" requiredMark={false} style={{ marginTop: 14 }}>
          <Form.Item label="当前用户名">
            <Input value={renameFor ? renameFor.username : ''} disabled />
          </Form.Item>
          <Form.Item
            name="newUsername"
            label="新用户名"
            rules={[
              { required: true, message: '请填写新用户名' },
              { pattern: /^[A-Za-z0-9._@-]{1,64}$/, message: '仅支持字母、数字与 . _ @ -（1-64 位）' },
            ]}
          >
            <Input placeholder="例如：ops-02" />
          </Form.Item>
          <div className="pp-muted" style={{ fontSize: 12, lineHeight: 1.7 }}>
            修改后该账号需使用新用户名登录；若修改的是根超管或当前登录账号，保存后会退出登录并要求重新登录。
          </div>
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
