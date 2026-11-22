import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  CloudOutlined,
  CodeOutlined,
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
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { api } from '../api';
import { useAsync, readForm } from '../hooks';
import { CopyText, ErrorBlock, IconAction, PageCard, StatCard, Toolbar } from '../components';
import { fmtAgo, fmtBytes, fmtTime } from '../format';
import { chartPalette } from '../theme';
import TemplateEditor from './TemplateEditor';
import TemplateListEditor from '../components/TemplateEditor/TemplateListEditor';
import StorageSettings from '../components/StorageSettings';

/** 规范化域名列表文件名：自动补 .list，返回 rule/<name>.list */
function normalizeListKey(name) {
  let n = String(name || '').trim().replace(/^\/+/, '');
  if (!n) return '';
  if (!/\.list$/i.test(n)) n = `${n}.list`;
  return `rule/${n}`;
}
function bucketKeyFromUrl(url) {
  const raw = String(url || '').trim();
  if (!raw) return '';
  try {
    const u = new URL(raw);
    return decodeURIComponent(u.pathname.replace(/^\/+/, ''));
  } catch (e) {
    return '';
  }
}

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
  const [editor, setEditor] = useState(null); // 当前打开内容编辑器的模板行
  const [listEditorOpen, setListEditorOpen] = useState(false);
  const [listEditorKey, setListEditorKey] = useState('');
  const [createListOpen, setCreateListOpen] = useState(false);
  const [creatingList, setCreatingList] = useState(false);
  const [createListForm] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const { data, loading, error, reload } = useAsync(
    () => api('templates/manage', { query: { page, size, q } }),
    [page, size, q]
  );

  const rows = (data && data.data) || [];
  const total = (data && data.total) || 0;

  const defaults = useMemo(() => rows.filter((r) => Number(r.is_default) === 1).length, [rows]);

  // 存储桶附件：rule/configs/ 下的 YAML 文件，作为「模板地址」下拉选项
  const attachments = useAsync(() => api('attachments', { query: { prefix: 'rule/configs/' } }), []);
  // 模板列表下方的域名列表（rule/*.list）管理卡片
  const ruleLists = useAsync(() => api('attachments', { query: { prefix: 'rule/' } }), []);
  // CDN 公共地址（对象存储状态），用于展示 CDN 域名与模板完整 URL
  const cdnStatus = useAsync(() => api('attachments/status'), []);
  const cdnBase = String((cdnStatus.data && cdnStatus.data.cdn_base_url) || '').replace(/\/+$/, '');
  const ruleRows = useMemo(() => {
    return ((ruleLists.data && ruleLists.data.files) || [])
      .filter((f) => /\.list$/i.test(String((f && f.key) || '')))
      .map((f) => ({
        key: f.key,
        name: f.name || String(f.key || '').split('/').pop(),
        size: f.size,
        cdn_url: f.cdn_url,
        updated_at: f.updated_at,
      }));
  }, [ruleLists.data]);
  const openListEditor = (key) => {
    setListEditorKey(key || '');
    setListEditorOpen(true);
  };

  // 新建 rule/*.list：写入 R2 后刷新列表并自动打开 List 编辑器选中新文件
  const createList = async () => {
    const values = await readForm(createListForm);
    if (!values) return;
    const key = normalizeListKey(values.name);
    if (!key) return;
    if (ruleRows.some((r) => String(r.key).toLowerCase() === key.toLowerCase())) {
      message.error('该列表已存在，请换一个文件名');
      return;
    }
    setCreatingList(true);
    try {
      const content = String(values.content || '').trim()
        ? String(values.content)
        : `# ${key.split('/').pop()} 域名列表\n\n`;
      await api('attachments/content', { method: 'PUT', body: { key, content, content_type: 'text/plain' } });
      message.success(`已创建 ${key}`);
      setCreateListOpen(false);
      createListForm.resetFields();
      ruleLists.reload().catch(() => {});
      setListEditorKey(key);
      setListEditorOpen(true);
    } catch (e) {
      message.error(e.message || '创建列表失败（请检查对象存储 R2 配置）');
    } finally {
      setCreatingList(false);
    }
  };
  const fileOptions = useMemo(() => {
    const list = ((attachments.data && attachments.data.files) || [])
      .filter((f) => /\.(ya?ml)$/i.test(String((f && f.key) || '')))
      .map((f) => ({ value: f.key, label: f.key }));
    const current = compose && compose.row ? compose.row.bucket_key || bucketKeyFromUrl(compose.row.url) : '';
    if (current && !list.some((o) => o.value === current)) list.unshift({ value: current, label: current });
    return list;
  }, [attachments.data, compose]);

  const openCreate = () => {
    setCompose({ mode: 'create', row: null });
    form.resetFields();
  };

  const openEdit = (row) => {
    setCompose({ mode: 'edit', row });
    form.setFieldsValue({
      name: row.name,
      bucket_key: row.bucket_key || bucketKeyFromUrl(row.url),
      description: row.description || '',
    });
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
      title: '存储桶文件',
      dataIndex: 'bucket_key',
      ellipsis: true,
      render: (v, row) => {
        const key = v || bucketKeyFromUrl(row.url);
        if (!key) return <span className="pp-muted">—</span>;
        const cdnUrl = row.cdn_url || (cdnBase ? `${cdnBase}/${String(key).replace(/^\/+/, '')}` : '');
        return (
          <div>
            <Space size={6}>
              <Tooltip title={key}>
                <span className="pp-mono" style={{ fontSize: 12 }}>{key}</span>
              </Tooltip>
              <CopyText value={key} display={<span style={{ fontSize: 12 }}>复制</span>} tip="复制存储桶路径" />
            </Space>
            {cdnUrl ? (
              <div style={{ marginTop: 2, display: 'flex', alignItems: 'center', gap: 6 }}>
                <Tooltip title={cdnUrl}>
                  <a
                    href={cdnUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="pp-mono pp-muted"
                    style={{ fontSize: 11.5, maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  >
                    {cdnUrl}
                  </a>
                </Tooltip>
                <CopyText value={cdnUrl} display={<span style={{ fontSize: 11.5 }}>复制</span>} tip="复制 CDN 地址" />
              </div>
            ) : null}
          </div>
        );
      },
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
          <span className="pp-nowrap" style={{ color: 'var(--pp-text)', fontVariantNumeric: 'tabular-nums' }}>
            {v ? fmtTime(v) : '—'}
          </span>
        </Tooltip>
      ),
    },
    {
      title: '操作',
      width: 236,
      render: (_, row) => (
        <Space size={0}>
          <Button
            size="small"
            type="link"
            icon={<CodeOutlined />}
            style={{ paddingInline: 4 }}
            onClick={() => setEditor(row)}
          >
            编辑内容
          </Button>
          <IconAction
            title="设为默认"
            icon={<StarOutlined />}
            disabled={Number(row.is_default) === 1}
            onClick={() => setDefault(row)}
          />
          <IconAction title="编辑元数据" icon={<EditOutlined />} onClick={() => openEdit(row)} />
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

      <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <span className="pp-muted" style={{ fontSize: 12 }}>CDN 域名</span>
        {cdnBase ? (
          <>
            <Tag color="purple">{cdnBase}</Tag>
            <CopyText value={cdnBase} display={<span style={{ fontSize: 12 }}>复制</span>} tip="复制 CDN 域名" />
          </>
        ) : (
          <span className="pp-muted" style={{ fontSize: 12 }}>未配置（可在「对象存储设置」填写 CDN 公共地址）</span>
        )}
      </div>

      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">Clash / Mihomo 订阅模板</span>}
          extra={
            <Space>
              <StorageSettings onSaved={() => { cdnStatus.reload().catch(() => {}); }} />
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
                  prefix={<SearchOutlined style={{ color: 'var(--pp-text)' }} />}
                  placeholder="搜索模板名称 / 存储桶文件 / 描述"
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

      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">域名列表（rule/*.list）</span>}
          extra={
            <Space>
              <span className="pp-muted" style={{ fontSize: 12 }}>共 {ruleRows.length} 个</span>
              <Button
                icon={<ReloadOutlined />}
                onClick={() => ruleLists.reload().catch(() => {})}
                loading={ruleLists.loading}
              >
                刷新
              </Button>
              <Button
                type="primary"
                icon={<PlusOutlined />}
                onClick={() => {
                  setCreateListOpen(true);
                  createListForm.resetFields();
                }}
              >
                新建列表
              </Button>
            </Space>
          }
          bodyStyle={{ padding: 0 }}
        >
          {ruleLists.error ? (
            <div style={{ padding: 18 }}>
              <ErrorBlock
                error={ruleLists.error}
                onRetry={() => ruleLists.reload().catch(() => {})}
                title="域名列表读取失败（请检查 R2 存储桶配置）"
              />
            </div>
          ) : (
            <Table
              className="pp-table"
              rowKey="key"
              size="medium"
              loading={ruleLists.loading}
              dataSource={ruleRows}
              pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 个` }}
              locale={{ emptyText: 'rule/ 下没有 .list 文件' }}
              columns={[
                {
                  title: '文件',
                  dataIndex: 'key',
                  render: (v) => (
                    <span className="pp-mono" style={{ color: 'var(--pp-text)' }}>
                      <CloudOutlined style={{ color: 'var(--pp-text)', marginRight: 8 }} />
                      {v}
                    </span>
                  ),
                },
                {
                  title: '大小',
                  dataIndex: 'size',
                  width: 110,
                  align: 'right',
                  render: (v) => fmtBytes(Number(v) || 0),
                },
                {
                  title: 'CDN URL',
                  dataIndex: 'cdn_url',
                  render: (v) => {
                    if (!v) return <span className="pp-muted">—</span>;
                    return (
                      <Space size={6}>
                        <a href={v} target="_blank" rel="noreferrer" className="pp-mono" style={{ fontSize: 12 }}>
                          <LinkOutlined /> 打开
                        </a>
                        <CopyText value={v} display={<span style={{ fontSize: 12 }}>复制</span>} tip="复制 CDN 地址" />
                      </Space>
                    );
                  },
                },
                {
                  title: '操作',
                  width: 90,
                  render: (_, row) => (
                    <Button
                      type="link"
                      size="small"
                      icon={<EditOutlined />}
                      style={{ paddingInline: 4 }}
                      onClick={() => openListEditor(row.key)}
                    >
                      编辑
                    </Button>
                  ),
                },
              ]}
            />
          )}
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
            name="bucket_key"
            label="存储桶文件"
            rules={[{ required: true, message: '请选择存储桶文件' }]}
            extra={
              attachments.error
                ? '附件列表加载失败：' + (attachments.error.message || '未知错误')
                : '从 rule/configs/ 选择 YAML 文件；可在「对象存储设置」上传/编辑'
            }
          >
            <Select
              showSearch
              optionFilterProp="label"
              placeholder="选择 rule/configs/ 下的 YAML 文件"
              options={fileOptions}
              loading={attachments.loading}
              notFoundContent={attachments.loading ? '加载中…' : '未找到 YAML 文件，请先在对象存储中上传'}
            />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input.TextArea rows={2} placeholder="用途说明，可为空" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 新建域名列表（rule/*.list） */}
      <Modal
        open={createListOpen}
        title="新建域名列表"
        onCancel={() => setCreateListOpen(false)}
        onOk={createList}
        confirmLoading={creatingList}
        okText="创建"
        destroyOnHidden
        width={560}
      >
        <Form form={createListForm} layout="vertical" requiredMark={false} style={{ marginTop: 14 }}>
          <Form.Item
            name="name"
            label="文件名"
            rules={[
              { required: true, message: '请填写文件名' },
              { pattern: /^[A-Za-z0-9._-]{1,64}$/, message: '仅支持字母、数字与 . _ -（1-64 位）' },
              {
                validator: (_, v) => {
                  const key = normalizeListKey(v);
                  if (!key) return Promise.resolve();
                  const exists = ruleRows.some((r) => String(r.key).toLowerCase() === key.toLowerCase());
                  return exists ? Promise.reject(new Error('该列表已存在')) : Promise.resolve();
                },
              },
            ]}
            extra="保存后写入 rule/<文件名>.list；输入已带 .list 时不会重复拼接"
          >
            <Input placeholder="例如：custom（自动补 .list）" maxLength={64} />
          </Form.Item>
          <Form.Item name="content" label="初始内容（可选）">
            <Input.TextArea
              rows={6}
              placeholder={'# custom 域名列表\n# 每行一条：DOMAIN-SUFFIX,example.com 或裸域名'}
            />
          </Form.Item>
          <div className="pp-muted" style={{ fontSize: 12, lineHeight: 1.7 }}>
            创建成功后会打开 List 编辑器并选中该文件，可继续添加域名。
          </div>
        </Form>
      </Modal>

      {/* 内容编辑器（全屏 Drawer）：解析分区表单 + 原始 YAML + 校验/预览/发布/版本 */}
      <TemplateListEditor
        open={listEditorOpen}
        initialKey={listEditorKey}
        onClose={() => setListEditorOpen(false)}
        onSaved={() => ruleLists.reload().catch(() => {})}
      />

      <TemplateEditor
        id={editor ? editor.id : undefined}
        open={!!editor}
        onClose={() => setEditor(null)}
        onChanged={() => reload().catch(() => {})}
      />
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
              prefix={<SearchOutlined style={{ color: 'var(--pp-text)' }} />}
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
                  <span className="pp-mono" style={{ color: 'var(--pp-text)' }}>
                    <CloudOutlined style={{ color: 'var(--pp-text)', marginRight: 8 }} />
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
          <div style={{ padding: 26, textAlign: 'center', color: 'var(--pp-text)' }}>读取中…</div>
        ) : (
          <Input.TextArea
            className="pp-mono"
            value={editing ? editing.content : ''}
            onChange={(e) => setEditing((s) => ({ ...s, content: e.target.value }))}
            autoSize={{ minRows: 16, maxRows: 24 }}
            spellCheck={false}
            style={{ fontSize: 12.5, lineHeight: 1.7, background: 'var(--pp-code-bg)', color: 'var(--pp-code-text)' }}
          />
        )}
        <div style={{ marginTop: 8, fontSize: 12, color: 'var(--pp-text)' }}>
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
          <div style={{ fontSize: 13, color: 'var(--pp-text)', marginBottom: 6 }}>
            文件路径（需在 {state.root || '允许'} 前缀下）
          </div>
          <Input
            className="pp-mono"
            value={creating ? creating.key : ''}
            onChange={(e) => setCreating((s) => ({ ...s, key: e.target.value }))}
            placeholder="rule/clash/new.yaml"
          />
          <div style={{ fontSize: 13, color: 'var(--pp-text)', margin: '14px 0 6px' }}>初始内容</div>
          <Input.TextArea
            className="pp-mono"
            rows={10}
            value={creating ? creating.content : ''}
            onChange={(e) => setCreating((s) => ({ ...s, content: e.target.value }))}
            spellCheck={false}
            placeholder={'# 可留空，创建后再编辑\n'}
            style={{ fontSize: 12.5, lineHeight: 1.7, background: 'var(--pp-code-bg)', color: 'var(--pp-code-text)' }}
          />
        </div>
      </Modal>
    </div>
  );
}
