import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ApiOutlined,
  ApartmentOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  LinkOutlined,
  PlusOutlined,
  ReloadOutlined,
  SaveOutlined,
  SafetyCertificateOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiRaw } from '../api';
import { readForm } from '../hooks';
import { CopyText, EmptyBlock, ErrorBlock, IconAction, JsonView, Toolbar } from '../components';
import { fmtAgo, fmtNum, fmtTime, shortUuid } from '../format';

const STATUS = {
  active: { color: 'success', text: '正常' },
  suspended: { color: 'warning', text: '暂停' },
  inactive: { color: 'default', text: '停用' },
};

/**
 * 用户详情：概览 / 节点配置（增删改·批量·一键清理）/ 授权节点 / 外部订阅源
 */
export default function GroupDetail({ uuid, onClose }) {
  const { message } = AntdApp.useApp();
  const [row, setRow] = useState(null);
  const [loadingRow, setLoadingRow] = useState(false);
  const [err, setErr] = useState(null);
  const [tab, setTab] = useState('overview');

  const loadRow = useCallback(async () => {
    if (!uuid) return;
    setLoadingRow(true);
    setErr(null);
    try {
      const res = await api('uuids', { query: { page: 1, size: 1000, q: uuid } });
      setRow((res.data || []).find((r) => r.uuid === uuid) || null);
    } catch (e) {
      setErr(e);
    } finally {
      setLoadingRow(false);
    }
  }, [uuid]);

  useEffect(() => {
    if (!uuid) {
      setRow(null);
      setTab('overview');
      return;
    }
    loadRow();
  }, [uuid, loadRow]);

  return (
    <Drawer
      open={!!uuid}
      onClose={onClose}
      width={920}
      title={
        <span className="pp-section-title">
          <ApartmentOutlined style={{ color: '#4f46e5' }} />
          用户详情
        </span>
      }
      extra={
        uuid ? (
          <Button size="small" icon={<ReloadOutlined />} onClick={loadRow} loading={loadingRow}>
            刷新
          </Button>
        ) : null
      }
    >
      {err ? <ErrorBlock error={err} onRetry={loadRow} /> : null}

      <Tabs
        activeKey={tab}
        onChange={setTab}
        destroyOnHidden
        items={[
          {
            key: 'overview',
            label: (<span><ApartmentOutlined /> 概览</span>),
            children: <Overview row={row} uuid={uuid} />,
          },
          {
            key: 'configs',
            label: (<span><UnorderedListOutlined /> 节点配置</span>),
            children: <Configs uuid={uuid} onChanged={loadRow} />,
          },
          {
            key: 'nodes',
            label: (<span><SafetyCertificateOutlined /> 授权服务</span>),
            children: <AuthorizeNodes uuid={uuid} onChanged={loadRow} />,
          },
          {
            key: 'subs',
            label: (<span><LinkOutlined /> 外部订阅源</span>),
            children: <SubSources uuid={uuid} />,
          },
        ]}
      />
    </Drawer>
  );
}

/* ---------------- 概览 ---------------- */
function Overview({ row, uuid }) {
  const st = STATUS[String(row && row.status)] || { color: 'default', text: '未知' };
  return (
    <>
      <Descriptions
        size="small"
        column={2}
        bordered
        items={[
          {
            key: 'uuid',
            label: 'UUID',
            span: 2,
            children: <CopyText value={uuid || ''} display={<span className="pp-mono">{uuid}</span>} />,
          },
          { key: 'owner', label: '所有者', children: (row && row.owner) || '—' },
          { key: 'status', label: '状态', children: <Tag color={st.color}>{st.text}</Tag> },
          { key: 'email', label: '邮箱', children: (row && row.email) || '—' },
          { key: 'contact', label: '联系方式', children: (row && row.contact_info) || '—' },
          { key: 'count', label: '节点数', children: fmtNum(row && row.count) },
          { key: 'access', label: '访问次数', children: fmtNum(row && row.access_count) },
          { key: 'created', label: '创建时间', children: fmtTime(row && row.created_at) },
          { key: 'updated', label: '更新时间', children: fmtTime(row && row.updated_at) },
          { key: 'remark', label: '备注', span: 2, children: (row && row.remark) || '—' },
        ]}
      />
      <Alert
        type="info"
        showIcon
        style={{ marginTop: 16, borderRadius: 10 }}
        message="删除用户会级联清理"
        description="删除用户时，会同时删除该用户的全部节点配置，并从所有节点上移除该 UUID 的授权。"
      />
    </>
  );
}

/* ---------------- 节点配置 ---------------- */
function Configs({ uuid, onChanged }) {
  const { message, modal } = AntdApp.useApp();
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(10);
  const [state, setState] = useState({ data: [], total: 0, loading: false, error: null, mtp: [] });
  const [selected, setSelected] = useState([]);
  const [edit, setEdit] = useState(null); // { mode:'create'|'edit', row }
  const [preview, setPreview] = useState(null);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    if (!uuid) return;
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await api('uuid-configs', { query: { uuid, page, size } });
      setState({ data: (res && res.data) || [], total: (res && res.total) || 0, loading: false, error: null, mtp: (res && res.mtproto) || [] });
      setSelected([]);
    } catch (e) {
      setState({ data: [], total: 0, loading: false, error: e, mtp: [] });
    }
  }, [uuid, page, size]);

  useEffect(() => {
    load();
  }, [load]);

  const copyMtp = async (text) => {
    try {
      await navigator.clipboard.writeText(text || '');
      message.success('已复制 MTProto 链接');
    } catch (e) {
      message.info(text || '');
    }
  };

  const openCreate = () => {
    setEdit({ mode: 'create', row: null });
    form.resetFields();
  };

  const openEdit = (row) => {
    setEdit({ mode: 'edit', row });
    form.setFieldsValue({ config_data: row.config_data || '' });
  };

  const save = async () => {
    const v = await readForm(form);
    if (!v) return;
    setSaving(true);
    try {
      if (edit.mode === 'create') {
        await api('uuid-configs', { method: 'POST', body: { uuid, config_data: v.config_data } });
        message.success('已添加（多行会按行拆成多条）');
      } else {
        await api('uuid-configs', { method: 'PUT', body: { id: edit.row.id, config_data: v.config_data } });
        message.success('配置已更新');
      }
      setEdit(null);
      load();
      if (onChanged) onChanged();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const removeOne = async (row) => {
    try {
      const res = await api('uuid-configs', { method: 'DELETE', body: { id: row.id } });
      message.success(`已删除${res.nodes_revoked ? `，并解除 ${res.nodes_revoked} 个节点授权` : ''}`);
      load();
      if (onChanged) onChanged();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const removeSelected = async () => {
    if (!selected.length) return;
    try {
      const res = await api('uuid-configs', { method: 'DELETE', body: { ids: selected } });
      message.success(`已删除 ${res.deleted || selected.length} 条${res.nodes_revoked ? `，解除 ${res.nodes_revoked} 个节点授权` : ''}`);
      load();
      if (onChanged) onChanged();
    } catch (e) {
      message.error(e.message || '批量删除失败');
    }
  };

  const clearAll = () => {
    modal.confirm({
      title: '一键清理该 UUID 的全部配置？',
      content: (
        <div style={{ fontSize: 13, lineHeight: 1.8 }}>
          将删除该用户的全部节点配置，并<b style={{ color: '#f43f5e' }}>解除所有节点对该 UUID 的授权</b>，然后按剩余配置重新下发。
        </div>
      ),
      okText: '确认清理',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          const res = await api('uuid-configs', { method: 'DELETE', body: { uuid } });
          message.success(`已删除 ${res.deleted || 0} 条，解除授权节点 ${res.nodes_revoked || 0} 台`);
          load();
          if (onChanged) onChanged();
        } catch (e) {
          message.error(e.message || '清理失败');
        }
      },
    });
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 66 },
    { title: '协议', dataIndex: 'protocol', width: 100, render: (v) => <Tag color="blue">{v || 'unknown'}</Tag> },
    {
      title: '配置',
      dataIndex: 'config_data',
      ellipsis: true,
      render: (v) => <span className="pp-mono" style={{ color: '#475569' }}>{String(v || '').slice(0, 88)}</span>,
    },
    { title: '备注', dataIndex: 'remark', width: 110, ellipsis: true, render: (v) => v || <span className="pp-muted">—</span> },
    { title: '更新时间', dataIndex: 'updated_at', width: 116, render: (v) => <span className="pp-muted">{fmtAgo(v)}</span> },
    {
      title: '操作',
      width: 110,
      render: (_, r) => (
        <Space size={0}>
          <IconAction title="查看" icon={<EyeOutlined />} onClick={() => setPreview(r)} />
          <IconAction title="编辑" icon={<EditOutlined />} onClick={() => openEdit(r)} />
          <Popconfirm title="删除这条配置？" okText="删除" okButtonProps={{ danger: true }} onConfirm={() => removeOne(r)}>
            <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Toolbar
        left={
          <Space wrap>
            <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openCreate}>
              添加配置
            </Button>
            <Popconfirm
              title={`删除选中的 ${selected.length} 条？`}
              okText="删除"
              okButtonProps={{ danger: true }}
              disabled={!selected.length}
              onConfirm={removeSelected}
            >
              <Button size="small" danger icon={<DeleteOutlined />} disabled={!selected.length}>
                删除选中{selected.length ? `（${selected.length}）` : ''}
              </Button>
            </Popconfirm>
            <Button size="small" danger type="dashed" onClick={clearAll}>
              一键清理全部
            </Button>
            <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
              刷新
            </Button>
          </Space>
        }
        right={<span className="pp-muted" style={{ fontSize: 12 }}>共 {fmtNum(state.total)} 条</span>}
      />

      {state.error ? <ErrorBlock error={state.error} onRetry={load} /> : null}

      <Table
        className="pp-table"
        rowKey="id"
        size="small"
        loading={state.loading}
        dataSource={state.data}
        columns={columns}
        rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys) }}
        pagination={{
          current: page,
          pageSize: size,
          total: state.total,
          size: 'small',
          showSizeChanger: true,
          pageSizeOptions: [10, 20, 50],
          onChange: (p, s) => {
            setPage(p);
            setSize(s);
          },
        }}
        locale={{ emptyText: <EmptyBlock text="该用户还没有节点配置" /> }}
      />

      {state.mtp && state.mtp.length ? (
        <div style={{ marginTop: 18 }}>
          <div className="pp-muted" style={{ fontSize: 12, marginBottom: 8 }}>
            MTProto 配置（{state.mtp.length}）· 与该用户的 sing-box 节点一起计入节点配置
          </div>
          <Table
            className="pp-table"
            size="small"
            dataSource={state.mtp}
            pagination={false}
            columns={[
              { title: '节点', dataIndex: 'node_name', width: 150, render: (v) => <b>{v}</b> },
              {
                title: '主机',
                dataIndex: 'host',
                width: 200,
                render: (v, r) => <span className="pp-mono">{v || '—'}:{r.port || 0}</span>,
              },
              { title: '密钥', dataIndex: 'secret_hint', width: 170, render: (v) => <span className="pp-mono">{v}</span> },
              {
                title: '状态',
                dataIndex: 'status',
                width: 90,
                render: (v) => (v === 'active' ? <Tag color="success">启用</Tag> : <Tag>已撤销</Tag>),
              },
              { title: '创建时间', dataIndex: 'created_at', width: 110, render: (v) => <span className="pp-muted">{v ? fmtAgo(v) : '—'}</span> },
              {
                title: '操作',
                width: 110,
                render: (_, r) => (
                  <Button type="link" size="small" onClick={() => copyMtp(r.tg)}>
                    复制链接
                  </Button>
                ),
              },
            ]}
            rowKey={(r) => `mtp-${r.id}`}
          />
        </div>
      ) : null}

      <Modal
        open={!!edit}
        title={edit && edit.mode === 'edit' ? `编辑配置 #${edit.row.id}` : '添加节点配置'}
        onCancel={() => setEdit(null)}
        onOk={save}
        confirmLoading={saving}
        okText="保存"
        width={680}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 12 }}>
          <Form.Item
            name="config_data"
            label="节点链接"
            rules={[{ required: true, message: '请粘贴节点链接' }]}
            extra={
              edit && edit.mode === 'create'
                ? '支持一次粘贴多行，每行会单独生成一条配置（自动识别协议）'
                : undefined
            }
          >
            <Input.TextArea
              rows={edit && edit.mode === 'create' ? 8 : 6}
              className="pp-mono"
              spellCheck={false}
              placeholder={'vless://uuid@host:443?type=ws&security=tls#hk\nhysteria2://pass@host:443#jp'}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal open={!!preview} onCancel={() => setPreview(null)} footer={null} width={700} title="节点配置原文">
        {preview ? <JsonView value={String(preview.config_data || '')} maxHeight={460} /> : null}
      </Modal>
    </div>
  );
}

/* ---------------- 授权服务 ---------------- */
function AuthorizeNodes({ uuid, onChanged }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ data: [], loading: false, error: null });
  const [selected, setSelected] = useState([]);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);

  const load = useCallback(async () => {
    if (!uuid) return;
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await api(`uuids/${uuid}/services`);
      const data = (res && res.data) || [];
      setState({ data, loading: false, error: null });
      setSelected(data.filter((n) => n.authorized).map((n) => `${n.kind}:${n.id}`));
      setDirty(false);
    } catch (e) {
      setState({ data: [], loading: false, error: e });
    }
  }, [uuid]);

  useEffect(() => {
    load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    try {
      const services = selected.map((k) => {
        const [kind, id] = String(k).split(':');
        return { kind, id: Number(id) };
      });
      const res = await api(`uuids/${uuid}/services`, { method: 'PUT', body: { services } });
      const failed = (res.results || []).filter((r) => r.ok === false);
      if (failed.length) message.warning(`已提交 ${res.changed || 0} 个变更，${failed.length} 个服务处理失败`);
      else message.success(`已保存，${res.changed || 0} 个服务授权发生变化`);
      load();
      if (onChanged) onChanged();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const columns = [
    {
      title: '类型',
      dataIndex: 'kind',
      width: 110,
      render: (v) =>
        v === 'mtproto' ? (
          <Tag color="volcano">MTProto</Tag>
        ) : v === 'mihomo' ? (
          <Tag color="purple">mihomo</Tag>
        ) : (
          <Tag color="geekblue">sing-box</Tag>
        ),
    },
    { title: '服务', dataIndex: 'name', width: 170, render: (v) => <b>{v}</b> },
    { title: '服务器', dataIndex: 'server_name', width: 150 },
    {
      title: '模板',
      dataIndex: 'template_name',
      width: 150,
      render: (v) => (v ? <Tag color="purple">{v}</Tag> : <span className="pp-muted">内置默认</span>),
    },
    { title: '当前授权用户数', dataIndex: 'users', width: 130, align: 'right', render: (v) => fmtNum(v) },
    {
      title: '状态',
      dataIndex: 'authorized',
      width: 100,
      render: (v) => (v ? <Tag color="success">已授权</Tag> : <Tag>未授权</Tag>),
    },
  ];

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ borderRadius: 10, marginBottom: 12 }}
        message="勾选即授权，取消勾选即撤销"
        description="授权覆盖该服务器上的 sing-box / mihomo / MTProto 三种服务：勾选后自动把 UUID 写入对应节点并重渲染下发（mihomo 下次下发生效）；变更同时刷新该 UUID 的订阅配置。"
      />
      <Toolbar
        left={
          <Space>
            <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
              刷新
            </Button>
            <Button
              size="small"
              type="primary"
              icon={<SaveOutlined />}
              disabled={!dirty}
              loading={saving}
              onClick={save}
            >
              保存授权
            </Button>
          </Space>
        }
        right={
          <span className="pp-muted" style={{ fontSize: 12 }}>
            已选 {selected.length} / {state.data.length}
          </span>
        }
      />
      {state.error ? <ErrorBlock error={state.error} onRetry={load} /> : null}
      <Table
        className="pp-table"
        rowKey={(row) => `${row.kind}:${row.id}`}
        size="small"
        loading={state.loading}
        dataSource={state.data}
        columns={columns}
        pagination={false}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: (keys) => {
            setSelected(keys);
            setDirty(true);
          },
        }}
        locale={{ emptyText: '暂无 sing-box / mihomo / MTProto 服务' }}
      />
    </div>
  );
}

/* ---------------- 外部订阅源 ---------------- */
function SubSources({ uuid }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ data: [], loading: false, error: null });
  const [addOpen, setAddOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    if (!uuid) return;
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await apiRaw('/manage/uuid-sub-sources', { query: { uuid } });
      setState({ data: (res && res.data) || [], loading: false, error: null });
    } catch (e) {
      setState({ data: [], loading: false, error: e });
    }
  }, [uuid]);

  useEffect(() => {
    load();
  }, [load]);

  const add = async () => {
    const v = await readForm(form);
    if (!v) return;
    setSaving(true);
    try {
      await apiRaw('/manage/uuid-sub-sources', {
        method: 'POST',
        body: { uuid, url: v.url, remark: v.remark || '' },
      });
      message.success('外部订阅源已添加');
      setAddOpen(false);
      form.resetFields();
      load();
    } catch (e) {
      message.error(e.message || '添加失败');
    } finally {
      setSaving(false);
    }
  };

  const toggle = async (row, enabled) => {
    try {
      await apiRaw('/manage/uuid-sub-sources', { method: 'PUT', body: { id: row.id, enabled: enabled ? 1 : 0 } });
      message.success(enabled ? '已启用' : '已停用');
      load();
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const remove = async (row) => {
    try {
      await apiRaw('/manage/uuid-sub-sources', { method: 'DELETE', body: { id: row.id } });
      message.success('已删除');
      load();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const columns = [
    {
      title: '订阅地址',
      dataIndex: 'url',
      ellipsis: true,
      render: (v) => (
        <Tooltip title={v}>
          <a href={v} target="_blank" rel="noreferrer" className="pp-mono">
            {v}
          </a>
        </Tooltip>
      ),
    },
    { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v) => v || <span className="pp-muted">—</span> },
    {
      title: '启用',
      dataIndex: 'enabled',
      width: 80,
      render: (v, row) => <Switch size="small" checked={Number(v) === 1} onChange={(c) => toggle(row, c)} />,
    },
    { title: '创建时间', dataIndex: 'created_at', width: 150, render: (v) => <span className="pp-muted">{fmtTime(v)}</span> },
    {
      title: '操作',
      width: 80,
      render: (_, row) => (
        <Popconfirm title="删除该外部订阅源？" okText="删除" okButtonProps={{ danger: true }} onConfirm={() => remove(row)}>
          <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
        </Popconfirm>
      ),
    },
  ];

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ borderRadius: 10, marginBottom: 12 }}
        message="外部订阅会合并进本用户的订阅输出"
        description="需要公网可访问的 http/https 地址；单个用户最多绑定 10 个，合并节点上限 500。"
      />
      <Toolbar
        left={
          <Space>
            <Button type="primary" size="small" icon={<PlusOutlined />} onClick={() => setAddOpen(true)}>
              添加订阅源
            </Button>
            <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
              刷新
            </Button>
          </Space>
        }
        right={<span className="pp-muted" style={{ fontSize: 12 }}>{state.data.length} 个</span>}
      />
      {state.error ? (
        <ErrorBlock error={state.error} onRetry={load} title="读取外部订阅源失败" />
      ) : (
        <Table
          className="pp-table"
          rowKey="id"
          size="small"
          loading={state.loading}
          dataSource={state.data}
          columns={columns}
          pagination={false}
          locale={{ emptyText: <EmptyBlock text="还没有绑定外部订阅源" /> }}
        />
      )}

      <Modal
        open={addOpen}
        title="添加外部订阅源"
        onCancel={() => setAddOpen(false)}
        onOk={add}
        confirmLoading={saving}
        okText="添加"
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 12 }}>
          <Form.Item
            name="url"
            label="订阅地址"
            rules={[
              { required: true, message: '请填写地址' },
              { pattern: /^https?:\/\//i, message: '需以 http:// 或 https:// 开头' },
            ]}
          >
            <Input placeholder="https://example.com/sub?token=xxx" prefix={<ApiOutlined style={{ color: '#94a3b8' }} />} />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input placeholder="来源说明，可为空" maxLength={100} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
