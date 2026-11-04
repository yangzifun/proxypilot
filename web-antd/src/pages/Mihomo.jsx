import React, { useEffect, useMemo, useState } from 'react';
import {
  CloudDownloadOutlined,
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import {
  App as AntdApp,
  Button,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiDelete, apiGet, apiPost, apiPut } from '../api';
import { PageCard } from '../components';
import { fmtTime } from '../format';

const { TextArea } = Input;

/** 默认 mihomo 模板：proxies 下方保留一个空行，生成时会替换为实际节点 */
export const DEFAULT_MIHOMO_TEMPLATE = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: info
ipv6: false
unified-delay: true
tcp-concurrent: true
external-controller: 127.0.0.1:9090

dns:
  enable: true
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  nameserver:
    - https://223.5.5.5/dns-query
    - https://1.1.1.1/dns-query
  fallback:
    - https://8.8.8.8/dns-query

proxies:

proxy-groups:
  - name: Proxy
    type: select
    proxies:
      - Auto
      - DIRECT
  - name: Auto
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    tolerance: 50
    proxies:
      - {{PROXY_NAMES}}

rules:
  - GEOIP,LAN,DIRECT,no-resolve
  - GEOIP,CN,DIRECT
  - MATCH,Proxy
`;
const downloadText = (name, text) => {
  const blob = new Blob([text], { type: 'text/yaml;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name || 'mihomo.yaml';
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
};

export default function Mihomo() {
  const { message } = AntdApp.useApp();
  const [tab, setTab] = useState('templates');
  const [loading, setLoading] = useState(false);
  const [templates, setTemplates] = useState([]);
  const [bindings, setBindings] = useState([]);
  const [servers, setServers] = useState([]);
  const [uuids, setUuids] = useState([]);

  const [tplOpen, setTplOpen] = useState(false);
  const [tplEditing, setTplEditing] = useState(null);
  const [tplForm] = Form.useForm();

  const [bindOpen, setBindOpen] = useState(false);
  const [bindEditing, setBindEditing] = useState(null);
  const [bindForm] = Form.useForm();

  const [versions, setVersions] = useState({ open: false, template: null, list: [], loading: false, content: '' });
  const [preview, setPreview] = useState(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  const loadTemplates = async () => {
    const res = await apiGet('/api/mihomo/templates');
    setTemplates(res.data || []);
  };
  const loadBindings = async () => {
    const res = await apiGet('/api/mihomo/nodes');
    setBindings(res.data || []);
  };
  const loadAll = async () => {
    setLoading(true);
    try {
      await Promise.all([loadTemplates(), loadBindings()]);
    } catch (e) {
      message.error(e.message || '加载失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadAll();
    apiGet('/api/servers')
      .then((res) => setServers(res.data || []))
      .catch(() => setServers([]));
    apiGet('/api/singbox/uuids')
      .then((res) => setUuids(res.data || []))
      .catch(() => setUuids([]));
  }, []);

  const serverOptions = useMemo(
    () => servers.map((s) => ({ value: s.id, label: `${s.name || `#${s.id}`}${s.host ? ` · ${s.host}` : ''}` })),
    [servers]
  );
  const templateOptions = useMemo(
    () => templates.map((t) => ({ value: t.id, label: t.name })),
    [templates]
  );
  const uuidOptions = useMemo(
    () =>
      uuids.map((u) => ({
        value: u.uuid,
        label: `${u.remark || u.owner || (u.email || '').split('@')[0] || u.uuid.slice(0, 8)} · ${String(u.uuid).slice(0, 8)}…`,
      })),
    [uuids]
  );

  const loadVersions = async (row) => {
    setVersions({ open: true, template: row, list: [], loading: true, content: '' });
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}/versions`);
      setVersions((v) => ({ ...v, list: res.data || [], loading: false }));
    } catch (e) {
      message.error(e.message || '读取版本失败');
      setVersions((v) => ({ ...v, loading: false }));
    }
  };
  const viewVersion = async (vid) => {
    const tpl = versions.template;
    if (!tpl) return;
    try {
      const res = await apiGet(`/api/mihomo/templates/${tpl.id}/versions/${vid}`);
      setPreview({ title: `${tpl.name} · 版本 #${vid}`, yaml: res.data.content, templateOnly: true });
    } catch (e) {
      message.error(e.message || '读取版本失败');
    }
  };
  const restoreVersion = async (vid) => {
    const tpl = versions.template;
    if (!tpl) return;
    try {
      await apiPost(`/api/mihomo/templates/${tpl.id}/versions/${vid}/restore`, {});
      message.success('已回滚到该版本');
      loadTemplates();
      loadVersions(tpl);
    } catch (e) {
      message.error(e.message || '回滚失败');
    }
  };

  const openCreateTpl = () => {
    setTplEditing(null);
    tplForm.setFieldsValue({ name: '', remark: '', content: DEFAULT_MIHOMO_TEMPLATE });
    setTplOpen(true);
  };
  const openEditTpl = async (row) => {
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}`);
      setTplEditing(res.data);
      tplForm.setFieldsValue({ name: res.data.name, remark: res.data.remark, content: res.data.content });
      setTplOpen(true);
    } catch (e) {
      message.error(e.message || '读取模板失败');
    }
  };
  const saveTpl = async () => {
    const v = await tplForm.validateFields();
    try {
      if (tplEditing) await apiPut(`/api/mihomo/templates/${tplEditing.id}`, v);
      else await apiPost('/api/mihomo/templates', v);
      message.success(tplEditing ? '模板已保存' : '模板已创建');
      setTplOpen(false);
      loadTemplates();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };
  const removeTpl = async (row) => {
    try {
      await apiDelete(`/api/mihomo/templates/${row.id}`);
      message.success('已删除');
      loadTemplates();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const openCreateBind = () => {
    setBindEditing(null);
    bindForm.setFieldsValue({ name: '', server_id: undefined, template_id: undefined, uuid_list: [], enabled: true, remark: '' });
    setBindOpen(true);
  };
  const openEditBind = (row) => {
    setBindEditing(row);
    bindForm.setFieldsValue({
      name: row.name,
      server_id: row.server_id,
      template_id: row.template_id,
      uuid_list: row.uuid_list || [],
      enabled: !!row.enabled,
      remark: row.remark || '',
    });
    setBindOpen(true);
  };
  const saveBind = async () => {
    const v = await bindForm.validateFields();
    const body = { ...v, uuid_list: v.uuid_list || [] };
    try {
      if (bindEditing) await apiPut(`/api/mihomo/nodes/${bindEditing.id}`, body);
      else await apiPost('/api/mihomo/nodes', body);
      message.success(bindEditing ? '绑定已保存' : '绑定已创建');
      setBindOpen(false);
      loadBindings();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };
  const removeBind = async (row) => {
    try {
      await apiDelete(`/api/mihomo/nodes/${row.id}`);
      message.success('已删除');
      loadBindings();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const openPreview = async (row) => {
    setPreview({ title: row.name, loading: true });
    setPreviewLoading(true);
    try {
      const res = await apiGet(`/api/mihomo/nodes/${row.id}/preview`);
      setPreview({ title: row.name, ...res.data });
    } catch (e) {
      message.error(e.message || '生成配置失败');
      setPreview({ title: row.name, yaml: `# 生成失败：${e.message || '未知错误'}` });
    } finally {
      setPreviewLoading(false);
    }
  };

  const tplColumns = [
    { title: '模板名称', dataIndex: 'name', ellipsis: true },
    { title: '备注', dataIndex: 'remark', ellipsis: true, render: (v) => v || '—' },
    { title: '内容长度', dataIndex: 'content_len', width: 110, render: (v) => `${v || 0} 字符` },
    { title: '更新时间', dataIndex: 'updated_at', width: 170, render: (v) => fmtTime(v) },
    {
      title: '操作',
      width: 280,
      render: (_, row) => (
        <Space size={4}>
          <Button size="small" type="link" icon={<EditOutlined />} onClick={() => openEditTpl(row)}>
            编辑
          </Button>
          <Button
            size="small"
            type="link"
            icon={<EyeOutlined />}
            onClick={() => setPreview({ title: row.name, yaml: '', loading: true }) || openTplPreview(row)}
          >
            预览
          </Button>
          <Button size="small" type="link" onClick={() => loadVersions(row)}>
            版本
          </Button>
          <Popconfirm title="删除该模板？" onConfirm={() => removeTpl(row)}>
            <Button size="small" type="link" danger icon={<DeleteOutlined />}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const openTplPreview = async (row) => {
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}`);
      setPreview({ title: row.name, yaml: res.data.content, templateOnly: true });
    } catch (e) {
      message.error(e.message || '读取模板失败');
    }
  };

  const bindColumns = [
    { title: '绑定名称', dataIndex: 'name', ellipsis: true },
    { title: '服务器', dataIndex: 'server_name', ellipsis: true, render: (v, r) => v || `#${r.server_id}` },
    { title: '模板', dataIndex: 'template_name', ellipsis: true, render: (v) => v || '—' },
    { title: 'UUID 数', dataIndex: 'uuid_count', width: 100, render: (v) => v || 0 },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 90,
      render: (v) => (v ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>),
    },
    { title: '更新时间', dataIndex: 'updated_at', width: 170, render: (v) => fmtTime(v) },
    {
      title: '操作',
      width: 230,
      render: (_, row) => (
        <Space size={4}>
          <Button size="small" type="link" icon={<EyeOutlined />} onClick={() => openPreview(row)}>
            预览配置
          </Button>
          <Button size="small" type="link" icon={<EditOutlined />} onClick={() => openEditBind(row)}>
            编辑
          </Button>
          <Popconfirm title="删除该绑定？" onConfirm={() => removeBind(row)}>
            <Button size="small" type="link" danger icon={<DeleteOutlined />}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <PageCard
      title="mihomo"
      subtitle="mihomo（Clash.Meta）模板管理与节点绑定，按服务器 + UUID 生成完整 YAML 配置"
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={loadAll} loading={loading}>
            刷新
          </Button>
          {tab === 'templates' ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreateTpl}>
              新建模板
            </Button>
          ) : (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreateBind}>
              新建绑定
            </Button>
          )}
        </Space>
      }
    >
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'templates',
            label: `模板管理（${templates.length}）`,
            children: (
              <Table
                rowKey="id"
                size="middle"
                loading={loading}
                dataSource={templates}
                columns={tplColumns}
                pagination={{ pageSize: 10, hideOnSinglePage: true }}
              />
            ),
          },
          {
            key: 'bindings',
            label: `节点绑定（${bindings.length}）`,
            children: (
              <Table
                rowKey="id"
                size="middle"
                loading={loading}
                dataSource={bindings}
                columns={bindColumns}
                pagination={{ pageSize: 10, hideOnSinglePage: true }}
              />
            ),
          },
        ]}
      />

      <Modal
        title={tplEditing ? `编辑模板 · ${tplEditing.name}` : '新建 mihomo 模板'}
        open={tplOpen}
        onCancel={() => setTplOpen(false)}
        onOk={saveTpl}
        okText="保存"
        width={860}
        destroyOnClose
      >
        <Form form={tplForm} layout="vertical">
          <Space size={16} style={{ width: '100%' }}>
            <Form.Item name="name" label="模板名称" rules={[{ required: true, message: '请填写名称' }]} style={{ width: 320 }}>
              <Input placeholder="例如：默认 mihomo 模板" maxLength={100} />
            </Form.Item>
          </Space>
          <Form.Item name="remark" label="备注">
            <Input placeholder="可选，例如：含广告拦截规则" maxLength={200} />
          </Form.Item>
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 6 }}>
            <Button size="small" onClick={() => tplForm.setFieldsValue({ content: DEFAULT_MIHOMO_TEMPLATE })}>
              填入默认配置
            </Button>
          </div>
          <Form.Item
            name="content"
            label="YAML 模板（proxies: 下方留空行；分组里可用 {{PROXY_NAMES}} 占位）"
            rules={[{ required: true, message: '请填写模板内容' }]}
          >
            <TextArea autoSize={{ minRows: 16, maxRows: 26 }} style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12.5 }} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={bindEditing ? `编辑绑定 · ${bindEditing.name}` : '新建 mihomo 节点绑定'}
        open={bindOpen}
        onCancel={() => setBindOpen(false)}
        onOk={saveBind}
        okText="保存"
        width={680}
        destroyOnClose
      >
        <Form form={bindForm} layout="vertical">
          <Form.Item name="name" label="绑定名称" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如：HK-01 mihomo" maxLength={100} />
          </Form.Item>
          <Form.Item name="server_id" label="服务器（绑定该服务器上的 sing-box 节点）" rules={[{ required: true, message: '请选择服务器' }]}>
            <Select showSearch optionFilterProp="label" options={serverOptions} placeholder="选择服务器" />
          </Form.Item>
          <Form.Item name="template_id" label="mihomo 模板" rules={[{ required: true, message: '请选择模板' }]}>
            <Select showSearch optionFilterProp="label" options={templateOptions} placeholder="选择模板" />
          </Form.Item>
          <Form.Item name="uuid_list" label="UUID（不选则使用模板中的全部用户）">
            <Select
              mode="multiple"
              allowClear
              showSearch
              optionFilterProp="label"
              options={uuidOptions}
              placeholder="选择 UUID"
              maxTagCount="responsive"
            />
          </Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input placeholder="可选" maxLength={200} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`版本历史 · ${versions.template?.name || ''}`}
        open={versions.open}
        onCancel={() => setVersions({ open: false, template: null, list: [], loading: false, content: '' })}
        footer={null}
        width={760}
      >
        <Table
          rowKey="id"
          size="small"
          loading={versions.loading}
          dataSource={versions.list}
          pagination={{ pageSize: 8, hideOnSinglePage: true }}
          columns={[
            { title: '版本', dataIndex: 'id', width: 90, render: (v) => '#' + v },
            { title: '名称', dataIndex: 'name', ellipsis: true },
            { title: '内容长度', dataIndex: 'content_len', width: 110, render: (v) => (v || 0) + ' 字符' },
            { title: '操作人', dataIndex: 'actor', width: 150, ellipsis: true, render: (v) => v || '—' },
            { title: '时间', dataIndex: 'created_at', width: 170, render: (v) => fmtTime(v) },
            {
              title: '操作',
              width: 160,
              render: (_, row) => (
                <Space size={4}>
                  <Button size="small" type="link" onClick={() => viewVersion(row.id)}>
                    查看
                  </Button>
                  <Popconfirm title="回滚到该版本？" onConfirm={() => restoreVersion(row.id)}>
                    <Button size="small" type="link">
                      回滚
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Modal>

      <Modal
        title={`配置预览 · ${preview?.title || ''}`}
        open={!!preview}
        onCancel={() => setPreview(null)}
        width={900}
        footer={
          <Space>
            <Button
              icon={<CopyOutlined />}
              disabled={!preview?.yaml}
              onClick={() => {
                navigator.clipboard?.writeText(preview.yaml);
                message.success('已复制');
              }}
            >
              复制
            </Button>
            <Button
              type="primary"
              icon={<CloudDownloadOutlined />}
              disabled={!preview?.yaml}
              onClick={() => downloadText(preview.filename || `${preview.title || 'mihomo'}.yaml`, preview.yaml)}
            >
              下载
            </Button>
            <Button onClick={() => setPreview(null)}>关闭</Button>
          </Space>
        }
      >
        {preview?.templateOnly ? null : preview && !preview.templateOnly ? (
          <div style={{ marginBottom: 10, color: '#64748b', fontSize: 12.5 }}>
            服务器 {preview.server || '—'} · 模板 {preview.template || '—'} · UUID {preview.uuid_count || 0} · 节点 {preview.proxy_count || 0}
            {preview.skipped ? ` · 跳过 ${preview.skipped}` : ''}
          </div>
        ) : null}
        <pre
          style={{
            margin: 0,
            maxHeight: '60vh',
            overflow: 'auto',
            background: '#0f172a',
            color: '#e2e8f0',
            borderRadius: 10,
            padding: 14,
            fontSize: 12,
            lineHeight: 1.55,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
          }}
        >
          {previewLoading ? '生成中…' : preview?.yaml || ''}
        </pre>
      </Modal>
    </PageCard>
  );
}
