import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ApartmentOutlined,
  CloudUploadOutlined,
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  KeyOutlined,
  LinkOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  SwapOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Checkbox,
  Col,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
  Radio,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { api } from '../api';
import GroupDetail from './GroupDetail';
import { useAsync, readForm } from '../hooks';
import { CopyText, ErrorBlock, IconAction, JsonView, PageCard, StatCard, StatusPill, Toolbar } from '../components';
import { fmtAgo, fmtNum, fmtTime, shortUuid } from '../format';
import { chartPalette } from '../theme';

const STATUS_OPTIONS = [
  { label: '正常', value: 'active', color: 'success' },
  { label: '暂停', value: 'suspended', color: 'warning' },
  { label: '停用', value: 'inactive', color: 'default' },
];

const statusTag = (status) => {
  const s = STATUS_OPTIONS.find((o) => o.value === String(status || 'active'));
  return (
    <Tag color={s ? s.color : 'default'} style={{ marginInlineEnd: 0 }}>
      {s ? s.label : status || '未知'}
    </Tag>
  );
};

export default function Groups() {
  const { message, modal } = AntdApp.useApp();
  const [q, setQ] = useState('');
  const [status, setStatus] = useState('');
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(10);
  const [sort, setSort] = useState({ field: '', order: '' });
  const [compose, setCompose] = useState(null); // { mode:'create'|'edit', row }
  const [detail, setDetail] = useState(null); // uuid
  const [pushOpen, setPushOpen] = useState(false);
  const [rotateRow, setRotateRow] = useState(null); // 要更换 UUID 的行
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const { data, loading, error, reload } = useAsync(
    () => api('uuids', { query: { page, size, q, status, sort: sort.field, order: sort.order } }),
    [page, size, q, status, sort.field, sort.order]
  );

  const rows = (data && data.data) || [];
  const total = (data && data.total) || 0;

  const totals = useMemo(() => {
    const nt = rows.reduce((s, r) => s + (Number(r.count) || 0), 0);
    const ac = rows.reduce((s, r) => s + (Number(r.access_count) || 0), 0);
    return { nt, ac };
  }, [rows]);

  const openCreate = () => {
    setCompose({ mode: 'create', row: null });
    form.resetFields();
    form.setFieldsValue({ status: 'active' });
  };

  const openEdit = (row) => {
    setCompose({ mode: 'edit', row });
    form.setFieldsValue({
      uuid: row.uuid,
      owner: row.owner || '',
      email: row.email || '',
      contact_info: row.contact_info || '',
      status: row.status || 'active',
      remark: row.remark || '',
    });
  };

  const submit = async () => {
    const values = await readForm(form);
    if (!values) return;
    setSaving(true);
    try {
      const isEdit = compose.mode === 'edit';
      const res = await api('uuids', { method: isEdit ? 'PUT' : 'POST', body: values });
      setCompose(null);
      reload().catch(() => {});
      if (!isEdit && res && res.initial_password) {
        showPassword({ ...res, isNew: true });
      } else {
        message.success(isEdit ? '用户已更新' : '用户已创建');
      }
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const remove = (row) => {
    modal.confirm({
      title: '删除用户？',
      content: (
        <div style={{ fontSize: 13, lineHeight: 1.8 }}>
          将删除 <b className="pp-mono">{shortUuid(row.uuid)}</b> 的全部节点配置与元信息，
          <b style={{ color: '#f43f5e' }}>并从所有节点上移除该 UUID 的授权</b>。此操作不可撤销。
        </div>
      ),
      okText: '确认删除',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          const res = await api('uuids', { method: 'DELETE', body: { uuid: row.uuid } });
          message.success(
            `已删除 ${res.deleted_configs || 0} 条配置，节点同步 ${res.nodes_updated || 0} 台`
          );
          reload().catch(() => {});
        } catch (e) {
          message.error(e.message || '删除失败');
          throw e;
        }
      },
    });
  };

  const columns = [
    {
      title: 'UUID',
      dataIndex: 'uuid',
      width: 268,
      sorter: true,
      render: (v) => <CopyText value={v} display={<span className="pp-mono">{v}</span>} tip="点击复制 UUID" />,
    },
    { title: '所有者', dataIndex: 'owner', width: 120, render: (v) => v || <span className="pp-muted">—</span> },
    {
      title: '邮箱',
      dataIndex: 'email',
      width: 190,
      ellipsis: true,
      render: (v) => (v ? <span style={{ color: '#475569' }}>{v}</span> : <span className="pp-muted">—</span>),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 92,
      render: (v) => statusTag(v),
    },
    {
      title: '节点数',
      dataIndex: 'count',
      width: 90,
      align: 'right',
      sorter: true,
      render: (v) => <b style={{ fontVariantNumeric: 'tabular-nums' }}>{fmtNum(v)}</b>,
    },
    {
      title: '访问',
      dataIndex: 'access_count',
      width: 120,
      align: 'right',
      render: (v, row) => (
        <Tooltip title={`最后访问：${fmtTime(row.last_accessed)}`}>
          <span style={{ fontVariantNumeric: 'tabular-nums' }}>{fmtNum(v)}</span>
        </Tooltip>
      ),
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 132,
      sorter: true,
      render: (v) => <span className="pp-muted pp-nowrap">{fmtAgo(v)}</span>,
    },
    {
      title: '操作',
      width: 192,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0}>
          <IconAction title="详情" icon={<EyeOutlined />} onClick={() => setDetail(row.uuid)} />
          <IconAction
            title="复制 Mihomo 订阅"
            icon={<LinkOutlined />}
            onClick={() => {
              const url = `${window.location.origin}/sub/mihomo/${row.uuid}`;
              navigator.clipboard?.writeText(url);
              message.success('已复制 Mihomo 订阅链接');
            }}
          />
          <IconAction title="更换 UUID" icon={<SwapOutlined />} onClick={() => setRotateRow(row)} />
          <IconAction title="编辑" icon={<EditOutlined />} onClick={() => openEdit(row)} />
          <IconAction title="删除" icon={<DeleteOutlined />} danger onClick={() => remove(row)} />
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={8}>
          <StatCard icon={<ApartmentOutlined />} label="用户总数" value={fmtNum(total)} accent={chartPalette[1]} />
        </Col>
        <Col xs={24} sm={8}>
          <StatCard
            icon={<ApartmentOutlined />}
            label="本页节点合计"
            value={fmtNum(totals.nt)}
            accent={chartPalette[0]}
            hint={`当前第 ${page} 页 · ${rows.length} 个用户`}
          />
        </Col>
        <Col xs={24} sm={8}>
          <StatCard
            icon={<EyeOutlined />}
            label="本页访问合计"
            value={fmtNum(totals.ac)}
            unit="次"
            accent={chartPalette[2]}
          />
        </Col>
      </Row>

      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">订阅用户</span>}
          extra={
            <Space>
              <Button icon={<CloudUploadOutlined />} onClick={() => setPushOpen(true)}>
                批量推送
              </Button>
              <Button icon={<ReloadOutlined />} onClick={() => reload().catch(() => {})} loading={loading}>
                刷新
              </Button>
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                新建用户
              </Button>
            </Space>
          }
          bodyStyle={{ padding: 0 }}
        >
          <div style={{ padding: '16px 18px 4px' }}>
            <Toolbar
              left={
                <Space wrap>
                  <Input
                    allowClear
                    prefix={<SearchOutlined style={{ color: '#94a3b8' }} />}
                    placeholder="搜索 UUID / 所有者 / 邮箱 / 备注"
                    value={q}
                    onChange={(e) => {
                      setQ(e.target.value);
                      setPage(1);
                    }}
                    style={{ width: 300 }}
                  />
                  <Select
                    allowClear
                    placeholder="全部状态"
                    value={status || undefined}
                    onChange={(v) => {
                      setStatus(v || '');
                      setPage(1);
                    }}
                    options={STATUS_OPTIONS.map((o) => ({ label: o.label, value: o.value }))}
                    style={{ width: 130 }}
                  />
                </Space>
              }
            />
          </div>

          {error ? (
            <div style={{ padding: 18 }}>
              <ErrorBlock error={error} onRetry={() => reload().catch(() => {})} />
            </div>
          ) : null}

          <Table
            className="pp-table"
            rowKey="uuid"
            size="medium"
            columns={columns}
            dataSource={rows}
            loading={loading}
            scroll={{ x: 1282 }}
            onChange={(pg, filters, sorter) => {
              if (sorter && sorter.field) {
                setSort({
                  field: sorter.field,
                  order: sorter.order === 'ascend' ? 'asc' : sorter.order === 'descend' ? 'desc' : '',
                });
                setPage(1);
              }
            }}
            pagination={{
              current: page,
              pageSize: size,
              total,
              showSizeChanger: true,
              pageSizeOptions: [10, 20, 50, 100],
              showTotal: (t) => `共 ${t} 个用户`,
              onChange: (p, s) => {
                setPage(p);
                setSize(s);
              },
            }}
          />
        </PageCard>
      </div>

      {/* ---------- 新建 / 编辑 ---------- */}
      <Modal
        open={!!compose}
        title={compose && compose.mode === 'edit' ? '编辑用户' : '新建用户'}
        onCancel={() => setCompose(null)}
        onOk={submit}
        confirmLoading={saving}
        okText="保存"
        width={560}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 14 }}>
          <Form.Item
            name="uuid"
            label="UUID"
            rules={[
              { required: true, message: '请填写 UUID' },
              {
                pattern: /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/,
                message: '请填写标准 UUID 格式（8-4-4-4-12）',
              },
            ]}
          >
            <Input
              className="pp-mono"
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              disabled={compose && compose.mode === 'edit'}
            />
          </Form.Item>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name="owner" label="所有者">
                <Input placeholder="可为空" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="email"
                label="绑定邮箱"
                extra="一个邮箱只能绑定一个 UUID（用于用户端邮箱登录）"
              >
                <Input placeholder="user@example.com" />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name="status" label="状态">
                <Select options={STATUS_OPTIONS.map((o) => ({ label: o.label, value: o.value }))} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="contact_info" label="联系方式">
                <Input placeholder="TG / QQ 等，可为空" />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="remark" label="备注">
            <Input.TextArea rows={2} placeholder="用途 / 到期时间等" />
          </Form.Item>
        </Form>
      </Modal>

      {/* ---------- 详情 ---------- */}
      <GroupDetail uuid={detail} onClose={() => setDetail(null)} />
      <BatchPushModal open={pushOpen} onClose={() => setPushOpen(false)} />
      <RotateUuidModal
        open={!!rotateRow}
        row={rotateRow}
        onClose={() => setRotateRow(null)}
        onRotated={(res) => {
          const oldUuid = (res && res.old_uuid) || (rotateRow && rotateRow.uuid) || '';
          reload().catch(() => {});
          // 详情抽屉如果还停在旧 UUID，直接关掉，避免继续拉旧 uuid 报错
          if (detail && oldUuid && detail === oldUuid) setDetail(null);
        }}
      />
    </div>
  );
}

/* ---------------- 批量推送：UUID × 服务器 ---------------- */
function BatchPushModal({ open, onClose }) {
  const { message } = AntdApp.useApp();
  const [servers, setServers] = useState([]);
  const [uuids, setUuids] = useState([]);
  const [selServers, setSelServers] = useState([]);
  const [selUuids, setSelUuids] = useState([]);
  const [push, setPush] = useState(true);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open) return;
    setSelServers([]);
    setSelUuids([]);
    setPush(true);
    setLoading(true);
    Promise.all([api('servers'), api('uuids', { query: { all: 1, size: 1000 } })])
      .then(([srv, uu]) => {
        setServers((srv && srv.data) || []);
        setUuids((uu && uu.data) || []);
      })
      .catch((e) => message.error(e.message || '加载列表失败'))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const submit = async () => {
    if (!selUuids.length) {
      message.warning('请至少选择一个 UUID');
      return;
    }
    if (!selServers.length) {
      message.warning('请至少选择一台服务器');
      return;
    }
    setBusy(true);
    try {
      const res = await api('singbox/push', {
        method: 'POST',
        body: { uuids: selUuids, server_ids: selServers, push },
      });
      const failed = (res.data || []).filter((x) => x.ok === false);
      if (failed.length) message.warning(`已推送 ${res.ok_count || 0}/${res.total || 0}，${failed.length} 台失败`);
      else message.success(`已推送 ${res.ok_count || 0}/${res.total || 0}`);
      onClose();
    } catch (e) {
      message.error(e.message || '推送失败');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title="批量推送节点配置"
      onCancel={onClose}
      onOk={submit}
      confirmLoading={busy}
      okText="开始推送"
      width={620}
      destroyOnHidden
    >
      <Alert
        type="info"
        showIcon
        style={{ borderRadius: 10, margin: '8px 0 16px' }}
        message="把选中的 UUID 推到选中服务器的 sing-box 节点"
        description="服务端会重渲染受影响节点并下发，Agent 下次轮询时落盘重启；未授权过的 UUID 会自动补上授权。"
      />
      <Space orientation="vertical" style={{ width: '100%' }} size={14}>
        <div>
          <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>服务器（已选 {selServers.length}）</div>
          <Select
            mode="multiple"
            allowClear
            loading={loading}
            placeholder="选择要推送的服务器"
            style={{ width: '100%' }}
            value={selServers}
            onChange={setSelServers}
            optionFilterProp="label"
            options={servers.map((s) => ({
              value: s.id,
              label: `${s.name}${s.last_status === 'online' ? ' · 在线' : ''}`,
            }))}
          />
        </div>
        <div>
          <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>UUID（已选 {selUuids.length}）</div>
          <Select
            mode="multiple"
            allowClear
            loading={loading}
            placeholder="选择要推送的 UUID"
            style={{ width: '100%' }}
            value={selUuids}
            onChange={setSelUuids}
            optionFilterProp="label"
            maxTagCount={4}
            options={uuids.map((u) => ({
              value: u.uuid,
              label: `${u.owner || '未命名'} · ${shortUuid(u.uuid)}`,
            }))}
          />
        </div>
        <div>
          <span style={{ fontSize: 13, color: '#64748b', marginRight: 8 }}>保存后立即下发</span>
          <Switch size="small" checked={push} onChange={setPush} />
        </div>
      </Space>
    </Modal>
  );
}

/* ---------------- 更换 UUID：自动/自定义 + 二次确认 + 新订阅链接 ---------------- */
const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

/** 订阅链接按后端规则拼接；rotate 响应里带 sub 时以响应为准。 */
function rotateSubLinks(newUuid, sub) {
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  const u = encodeURIComponent(String(newUuid || ''));
  const s = sub || {};
  return [
    { key: 'raw', label: '原始订阅', url: s.raw || `${origin}/sub/${u}` },
    { key: 'batch', label: '批量配置', url: s.batch || `${origin}/batch-configs/${u}` },
    { key: 'clash', label: 'Clash 订阅', url: s.clash || `${origin}/clash-config?uuid=${u}` },
  ];
}

function RotateUuidModal({ open, row, onClose, onRotated }) {
  const { message } = AntdApp.useApp();
  const [mode, setMode] = useState('auto'); // auto=服务端生成 / custom=自定义
  const [newUuid, setNewUuid] = useState('');
  const [confirmText, setConfirmText] = useState('');
  const [agree, setAgree] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [result, setResult] = useState(null);
  const oldUuid = row ? String(row.uuid || '') : '';

  useEffect(() => {
    if (!open) return;
    setMode('auto');
    setNewUuid('');
    setConfirmText('');
    setAgree(false);
    setBusy(false);
    setErr('');
    setResult(null);
  }, [open]);

  const custom = mode === 'custom';
  const confirmOk = agree || (!!confirmText.trim() && confirmText.trim().toLowerCase() === oldUuid.toLowerCase());

  const submit = async () => {
    if (!oldUuid) return;
    if (custom) {
      const v = newUuid.trim();
      if (!UUID_RE.test(v)) {
        setErr('新 UUID 格式不对，应为 8-4-4-4-12 的标准 UUID');
        return;
      }
      if (v.toLowerCase() === oldUuid.toLowerCase()) {
        setErr('新 UUID 不能与旧 UUID 相同');
        return;
      }
    }
    if (!confirmOk) {
      setErr('为防止误操作：请粘贴旧 UUID，或勾选「我确认旧链接与旧配置将立即失效」');
      return;
    }
    setBusy(true);
    setErr('');
    try {
      const body = { confirm: oldUuid };
      if (custom) body.new_uuid = newUuid.trim();
      const res = await api(`uuids/${encodeURIComponent(oldUuid)}/rotate`, { method: 'POST', body });
      setResult(res || { success: true });
      if (onRotated) onRotated(res || {});
      message.success((res && res.message) || 'UUID 已更换，旧链接与旧配置已失效');
    } catch (e) {
      setErr(e.message || '更换失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const done = !!(result && (result.success || result.new_uuid));
  const resultUuid = done ? String((result && result.new_uuid) || '') : '';
  const links = done ? rotateSubLinks(resultUuid, result && result.sub) : [];
  const stats = done
    ? [
        { k: 'configs_updated', label: '更新配置' },
        { k: 'tables_updated', label: '更新数据表' },
        { k: 'nodes_updated', label: '更新节点' },
        { k: 'nodes_deployed', label: '已下发节点' },
      ].filter((it) => result && result[it.k] !== undefined && result[it.k] !== null)
    : [];

  return (
    <Modal
      open={open}
      title={done ? 'UUID 已更换' : '更换 UUID'}
      onCancel={onClose}
      footer={done ? <Button type="primary" onClick={onClose}>完成</Button> : undefined}
      onOk={done ? undefined : submit}
      okText="确认更换"
      cancelText="取消"
      okButtonProps={done ? undefined : { danger: true }}
      confirmLoading={busy}
      width={620}
      centered
      destroyOnHidden
    >
      {done ? (
        <div style={{ paddingTop: 4 }}>
          <Alert
            type="success"
            showIcon
            style={{ borderRadius: 10, marginBottom: 14 }}
            message="更换成功，旧 UUID 已作废"
            description="旧订阅链接与旧节点配置已全部失效；客户端还在用旧链接会立即拉不到配置。请把新链接发给用户重新导入。"
          />
          <Descriptions column={1} size="small" bordered style={{ marginBottom: 14 }}>
            <Descriptions.Item label="旧 UUID">
              <span className="pp-mono" style={{ fontSize: 12 }}>{oldUuid}</span>
            </Descriptions.Item>
            <Descriptions.Item label="新 UUID">
              <CopyText
                value={resultUuid}
                display={<span className="pp-mono" style={{ fontSize: 12 }}>{resultUuid || '—'}</span>}
                tip="点击复制新 UUID"
              />
            </Descriptions.Item>
          </Descriptions>
          <div style={{ fontSize: 13, color: '#64748b', marginBottom: 8 }}>新订阅链接</div>
          <Space orientation="vertical" size={8} style={{ width: '100%' }}>
            {links.map((it) => (
              <div key={it.key} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Tag color="blue" style={{ marginInlineEnd: 0, flex: 'none' }}>{it.label}</Tag>
                <span className="pp-mono" style={{ flex: 1, minWidth: 0, fontSize: 12, wordBreak: 'break-all' }}>{it.url}</span>
                <CopyText value={it.url} display={<Button size="small" icon={<CopyOutlined />}>复制</Button>} />
              </div>
            ))}
          </Space>
          {stats.length ? (
            <div style={{ marginTop: 14, fontSize: 12, color: '#64748b' }}>
              {stats.map((it) => `${it.label} ${Number(result[it.k]) || 0}`).join(' · ')}
            </div>
          ) : null}
          {result && result.warning ? (
            <Alert type="warning" showIcon style={{ marginTop: 14 }} message={String(result.warning)} />
          ) : null}
          <div style={{ marginTop: 12, color: '#94a3b8', fontSize: 12 }}>
            列表已自动刷新；详情抽屉如停在旧 UUID 已关闭。
          </div>
        </div>
      ) : (
        <div style={{ paddingTop: 4 }}>
          <Alert
            type="warning"
            showIcon
            style={{ borderRadius: 10, marginBottom: 14 }}
            message="更换后旧链接与旧配置立即失效"
            description="旧 UUID 的订阅链接、节点配置与各节点授权会被替换成新 UUID；用户必须重新导入新订阅链接。此操作不可撤销。"
          />
          <Descriptions column={1} size="small" bordered style={{ marginBottom: 14 }}>
            <Descriptions.Item label="旧 UUID">
              <CopyText
                value={oldUuid}
                display={<span className="pp-mono" style={{ fontSize: 12 }}>{oldUuid || '—'}</span>}
                tip="点击复制旧 UUID"
              />
            </Descriptions.Item>
            {row && (row.owner || row.email) ? (
              <Descriptions.Item label="用户">
                {[row.owner, row.email].filter(Boolean).join(' · ')}
              </Descriptions.Item>
            ) : null}
          </Descriptions>

          <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>新 UUID</div>
          <Radio.Group
            value={mode}
            onChange={(e) => { setMode(e.target.value); setErr(''); }}
            style={{ marginBottom: 10 }}
          >
            <Radio value="auto">自动生成（推荐）</Radio>
            <Radio value="custom">自定义</Radio>
          </Radio.Group>
          {custom ? (
            <Input
              className="pp-mono"
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              value={newUuid}
              onChange={(e) => { setNewUuid(e.target.value); setErr(''); }}
              allowClear
            />
          ) : (
            <div style={{ fontSize: 12, color: '#94a3b8' }}>提交后由服务端生成一个全新的 UUID（v4），无需手工填写。</div>
          )}

          <div style={{ fontSize: 13, color: '#64748b', margin: '16px 0 6px' }}>二次确认（防止误操作）</div>
          <Input
            className="pp-mono"
            placeholder="粘贴旧 UUID"
            value={confirmText}
            onChange={(e) => { setConfirmText(e.target.value); setErr(''); }}
          />
          <div style={{ marginTop: 8 }}>
            <Checkbox checked={agree} onChange={(e) => setAgree(e.target.checked)}>
              我确认旧链接与旧配置将立即失效，用户需要重新导入
            </Checkbox>
          </div>
          {err ? <Alert type="error" showIcon style={{ marginTop: 12 }} message={err} /> : null}
        </div>
      )}
    </Modal>
  );
}

