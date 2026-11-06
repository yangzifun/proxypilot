import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  CloudOutlined,
  DeleteOutlined,
  EditOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  PlusOutlined,
  ReloadOutlined,
  SaveOutlined,
  StarOutlined,
  SearchOutlined,
  LinkOutlined,
} from '@ant-design/icons';
import {
  App as AntdApp,
  Button,
  Col,
  Form,
  Input,
  Modal,
  Popconfirm,
  Row,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { safeHttpUrl } from '../safeUrl';
import { api } from '../api';
import { useAsync, readForm } from '../hooks';
import { ErrorBlock, IconAction, PageCard, StatCard, Toolbar } from '../components';
import { fmtAgo, fmtBytes, fmtTime } from '../format';
import { chartPalette } from '../theme';

export default function Templates() {
  return (
    <Tabs
      defaultActiveKey="list"
      items={[
        { key: 'list', label: <span><FileTextOutlined /> 订阅模板</span>, children: <TemplateList /> },
      ]}
    />
  );
}

/* =================================================================
 *  模板列表
 * ================================================================= */
function TemplateList() {
  const { message } = AntdApp.useApp();
  const [q, setQ] = useState('');
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(10);
  const [compose, setCompose] = useState(null);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const { data, loading, error, reload } = useAsync(
    () => api('templates/manage', { query: { page, size, q } }),
    [page, size, q]
  );

  const rows = (data && data.data) || [];
  const total = (data && data.total) || 0;

  const defaults = useMemo(() => rows.filter((r) => Number(r.is_default) === 1).length, [rows]);

  const openCreate = () => {
    setCompose({ mode: 'create', row: null });
    form.resetFields();
  };

  const openEdit = (row) => {
    setCompose({ mode: 'edit', row });
    form.setFieldsValue({ name: row.name, url: row.url, description: row.description || '' });
  };

  const submit = async () => {
    const values = await readForm(form);
    if (!values) return;
    setSaving(true);
    try {
      if (compose.mode === 'edit') {
        await api('templates/manage', { method: 'PUT', body: { id: compose.row.id, ...values } });
        message.success('模板已更新');
      } else {
        await api('templates/manage', { method: 'POST', body: values });
        message.success('模板已创建');
      }
      setCompose(null);
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const setDefault = async (row) => {
    try {
      await api('templates/manage/set-default', { method: 'POST', body: { id: row.id } });
      message.success(`已把「${row.name}」设为默认模板`);
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '设置失败');
    }
  };

  const remove = async (row) => {
    try {
      await api('templates/manage', { method: 'DELETE', body: { id: row.id } });
      message.success('模板已删除');
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 64 },
    {
      title: '模板名称',
      dataIndex: 'name',
      width: 200,
      render: (v, row) => (
        <Space size={6}>
          <b>{v}</b>
          {Number(row.is_default) === 1 ? (
            <Tag color="gold" style={{ marginInlineEnd: 0 }} icon={<StarOutlined />}>
              默认
            </Tag>
          ) : null}
        </Space>
      ),
    },
    {
      title: '模板地址',
      dataIndex: 'url',
      ellipsis: true,
      render: (v) =>
        v ? (
          <Tooltip title={v}>
            <a href={safeHttpUrl(v) || undefined} target="_blank" rel="noreferrer" className="pp-mono">
              {v}
            </a>
          </Tooltip>
        ) : (
          <span className="pp-muted">—</span>
        ),
    },
    {
      title: '描述',
      dataIndex: 'description',
      width: 240,
      ellipsis: true,
      render: (v) => v || <span className="pp-muted">—</span>,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 176,
      render: (v) => (
        <Tooltip title={v ? `${fmtAgo(v)}（${fmtTime(v)}）` : '—'}>
          <span className="pp-nowrap" style={{ color: '#475569', fontVariantNumeric: 'tabular-nums' }}>
            {v ? fmtTime(v) : '—'}
          </span>
        </Tooltip>
      ),
    },
    {
      title: '操作',
      width: 130,
      render: (_, row) => (
        <Space size={0}>
          <IconAction
            title="设为默认"
            icon={<StarOutlined />}
            disabled={Number(row.is_default) === 1}
            onClick={() => setDefault(row)}
          />
          <IconAction title="编辑" icon={<EditOutlined />} onClick={() => openEdit(row)} />
          <Popconfirm
            title="删除该模板？"
            description="已使用该模板的订阅不会自动切换。"
            okText="删除"
            okButtonProps={{ danger: true }}
            onConfirm={() => remove(row)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={8}>
          <StatCard icon={<FileTextOutlined />} label="模板总数" value={total} accent={chartPalette[3]} />
        </Col>
        <Col xs={24} sm={8}>
          <StatCard
            icon={<StarOutlined />}
            label="本页默认模板"
            value={defaults}
            accent={chartPalette[2]}
            hint="默认模板用于未指定模板的订阅"
          />
        </Col>
        <Col xs={24} sm={8}>
          <StatCard icon={<LinkOutlined />} label="本页条数" value={rows.length} accent={chartPalette[1]} />
        </Col>
      </Row>

      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">Clash / Mihomo 订阅模板</span>}
          extra={
            <Space>
              <Button icon={<ReloadOutlined />} onClick={() => reload().catch(() => {})} loading={loading}>
                刷新
              </Button>
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                新建模板
              </Button>
            </Space>
          }
          bodyStyle={{ padding: 0 }}
        >
          <div style={{ padding: '16px 18px 4px' }}>
            <Toolbar
              left={
                <Input
                  allowClear
                  prefix={<SearchOutlined style={{ color: '#94a3b8' }} />}
                  placeholder="搜索模板名称 / 地址 / 描述"
                  value={q}
                  onChange={(e) => {
                    setQ(e.target.value);
                    setPage(1);
                  }}
                  style={{ width: 320 }}
                />
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
            rowKey="id"
            size="medium"
            columns={columns}
            dataSource={rows}
            loading={loading}
            scroll={{ x: 1240 }}
            pagination={{
              current: page,
              pageSize: size,
              total,
              showSizeChanger: true,
              showTotal: (t) => `共 ${t} 个模板`,
              onChange: (p, s) => {
                setPage(p);
                setSize(s);
              },
            }}
          />
        </PageCard>
      </div>

      <Modal
        open={!!compose}
        title={compose && compose.mode === 'edit' ? '编辑模板' : '新建模板'}
        onCancel={() => setCompose(null)}
        onOk={submit}
        confirmLoading={saving}
        okText="保存"
        destroyOnHidden
        width={560}
      >
        <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 14 }}>
          <Form.Item name="name" label="模板名称" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如：默认 Clash 模板" maxLength={100} />
          </Form.Item>
          <Form.Item
            name="url"
            label="模板地址"
            rules={[
              { required: true, message: '请填写模板 URL' },
              { pattern: /^https?:\/\//i, message: '需以 http:// 或 https:// 开头' },
            ]}
          >
            <Input placeholder="https://s3.example.com/rule/clash.yaml" />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input.TextArea rows={2} placeholder="用途说明，可为空" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}

/* =================================================================
 *  对象存储文件浏览（模板 / 地区配置在线编辑）
 * ================================================================= */
function BucketBrowser() {
  const { message } = AntdApp.useApp();
  const [prefix, setPrefix] = useState('');
  const [state, setState] = useState({ files: [], root: '', loading: false, error: null });
  const [editing, setEditing] = useState(null); // { key, content }
  const [creating, setCreating] = useState(null); // { key, content }
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await api('bucket/files', { query: { prefix } });
      setState({ files: (res && res.files) || [], root: (res && res.root) || '', loading: false, error: null });
      if (!prefix && res && res.root) setPrefix(res.root);
    } catch (e) {
      setState({ files: [], root: '', loading: false, error: e });
    }
  }, [prefix]);

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openFile = async (file) => {
    setEditing({ key: file.key, content: '', loading: true, size: file.size });
    try {
      const res = await api('bucket/file', { query: { key: file.key } });
      setEditing({ key: file.key, content: (res && res.content) || '', loading: false, size: res && res.size });
    } catch (e) {
      message.error(e.message || '读取文件失败');
      setEditing(null);
    }
  };

  const save = async () => {
    if (!editing) return;
    setSaving(true);
    try {
      await api('bucket/file', { method: 'PUT', body: { key: editing.key, content: editing.content } });
      message.success('已保存到对象存储');
      setEditing(null);
      load();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <PageCard
        title={<span className="pp-section-title">对象存储文件</span>}
        extra={
          <Space>
            <Input
              placeholder={prefix || '前缀，例如 rule/'}
              value={prefix}
              onChange={(e) => setPrefix(e.target.value)}
              style={{ width: 240 }}
              onPressEnter={() => load()}
              prefix={<SearchOutlined style={{ color: '#94a3b8' }} />}
            />
            <Button icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
              读取
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setCreating({ key: (state.root || 'rule') + '/', content: '' })}
            >
              新建文件
            </Button>
          </Space>
        }
        bodyStyle={{ padding: 0 }}
      >
        {state.error ? (
          <div style={{ padding: 18 }}>
            <ErrorBlock error={state.error} onRetry={load} title="对象存储不可用" />
          </div>
        ) : (
          <Table
            className="pp-table"
            rowKey="key"
            size="medium"
            loading={state.loading}
            dataSource={state.files}
            pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 个文件` }}
            locale={{ emptyText: '该前缀下没有文件（需要配置对象存储）' }}
            columns={[
              {
                title: '文件',
                dataIndex: 'key',
                render: (v) => (
                  <span className="pp-mono" style={{ color: '#334155' }}>
                    <CloudOutlined style={{ color: '#94a3b8', marginRight: 8 }} />
                    {v}
                  </span>
                ),
              },
              {
                title: '大小',
                dataIndex: 'size',
                width: 110,
                align: 'right',
                render: (v) => fmtBytes(v),
              },
              {
                title: '上传时间',
                dataIndex: 'uploaded',
                width: 180,
                render: (v) => <span className="pp-muted">{fmtTime(v)}</span>,
              },
              {
                title: '操作',
                width: 80,
                render: (_, row) => <IconAction title="查看/编辑" icon={<EditOutlined />} onClick={() => openFile(row)} />,
              },
            ]}
          />
        )}
      </PageCard>

      <Modal
        open={!!editing}
        title={<span className="pp-mono">{editing && editing.key}</span>}
        onCancel={() => setEditing(null)}
        width={860}
        okText="写入对象存储"
        okButtonProps={{ icon: <SaveOutlined />, disabled: !!editing?.loading }}
        confirmLoading={saving}
        onOk={save}
      >
        {editing && editing.loading ? (
          <div style={{ padding: 26, textAlign: 'center', color: '#94a3b8' }}>读取中…</div>
        ) : (
          <Input.TextArea
            className="pp-mono"
            value={editing ? editing.content : ''}
            onChange={(e) => setEditing((s) => ({ ...s, content: e.target.value }))}
            autoSize={{ minRows: 16, maxRows: 24 }}
            spellCheck={false}
            style={{ fontSize: 12.5, lineHeight: 1.7 }}
          />
        )}
        <div style={{ marginTop: 8, fontSize: 12, color: '#94a3b8' }}>
          单文件上限 512KB；改动会立即影响引用该文件的模板与规则集。
        </div>
      </Modal>

      {/* 新建文件 */}
      <Modal
        open={!!creating}
        title="新建存储桶文件"
        onCancel={() => setCreating(null)}
        width={760}
        okText="创建"
        confirmLoading={saving}
        onOk={async () => {
          if (!creating) return;
          const key = String(creating.key || '').trim();
          if (!key) {
            message.warning('请填写文件路径');
            return;
          }
          setSaving(true);
          try {
            await api('bucket/file', { method: 'PUT', body: { key, content: creating.content || '' } });
            message.success('文件已创建');
            setCreating(null);
            load();
          } catch (e) {
            message.error(e.message || '创建失败');
          } finally {
            setSaving(false);
          }
        }}
      >
        <div style={{ marginTop: 12 }}>
          <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>
            文件路径（需在 {state.root || '允许'} 前缀下）
          </div>
          <Input
            className="pp-mono"
            value={creating ? creating.key : ''}
            onChange={(e) => setCreating((s) => ({ ...s, key: e.target.value }))}
            placeholder="rule/clash/new.yaml"
          />
          <div style={{ fontSize: 13, color: '#64748b', margin: '14px 0 6px' }}>初始内容</div>
          <Input.TextArea
            className="pp-mono"
            rows={10}
            value={creating ? creating.content : ''}
            onChange={(e) => setCreating((s) => ({ ...s, content: e.target.value }))}
            spellCheck={false}
            placeholder={'# 可留空，创建后再编辑\n'}
            style={{ fontSize: 12.5, lineHeight: 1.7 }}
          />
        </div>
      </Modal>
    </div>
  );
}
