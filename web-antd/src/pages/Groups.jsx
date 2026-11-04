import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ApartmentOutlined,
  CloudUploadOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Col,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
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
      await api('uuids', { method: isEdit ? 'PUT' : 'POST', body: values });
      message.success(isEdit ? '用户已更新' : '用户已创建');
      setCompose(null);
      reload().catch(() => {});
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
      width: 150,
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
          title={<span className="pp-section-title">用户管理</span>}
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
            scroll={{ x: 1240 }}
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

