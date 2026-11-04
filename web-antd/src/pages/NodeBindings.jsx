import React, { useEffect, useMemo, useState } from 'react';
import { App as AntdApp, Button, Form, Input, Modal, Select, Space, Table, Tag } from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, apiGet, apiPost } from '../api';
import { PageCard } from '../components';
import { fmtTime } from '../format';

export default function NodeBindings() {
  const { message } = AntdApp.useApp();
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [tpl, setTpl] = useState({ sb: [], mh: [], mp: [] });
  const [open, setOpen] = useState(false);
  const [editRow, setEditRow] = useState(null);
  const [form] = Form.useForm();

  const load = async () => {
    setLoading(true);
    try {
      const res = await apiGet('/api/nodes/bindings');
      setRows(res.data || []);
    } catch (e) {
      message.error(e.message || '加载失败');
    } finally {
      setLoading(false);
    }
  };
  const loadTemplates = async () => {
    try {
      const [sb, mh, mp] = await Promise.all([
        apiGet('/api/singbox/templates'),
        apiGet('/api/mihomo/templates'),
        apiGet('/api/mtproto/templates'),
      ]);
      setTpl({
        sb: (sb.data || []).map((x) => ({ value: x.id, label: x.name })),
        mh: (mh.data || []).filter((x) => x.mode === 'server').map((x) => ({ value: x.id, label: x.name })),
        mp: (mp.data || []).map((x) => ({ value: x.id, label: x.name })),
      });
    } catch (e) {
      /* ignore */
    }
  };
  useEffect(() => {
    load();
    loadTemplates();
  }, []);

  const options = rows.map((r) => ({ value: r.server_id, label: `${r.server_name}${r.host ? ` · ${r.host}` : ''}` }));

  const submit = async () => {
    const v = await form.validateFields();
    const tasks = [];
    if (v.sb_tpl) tasks.push(['singbox', '/api/singbox/nodes', { name: v.name + '-sb', server_id: v.server_id, template_id: v.sb_tpl, domain: v.domain || '' }]);
    if (v.mh_tpl) tasks.push(['mihomo', '/api/mihomo/nodes', { name: v.name + '-mh', server_id: v.server_id, template_id: v.mh_tpl, domain: v.domain || '' }]);
    if (v.mp_tpl) tasks.push(['MTProto', '/api/mtproto/nodes', { name: v.name + '-mp', server_id: v.server_id, template_id: v.mp_tpl, domain: v.domain || '' }]);
    if (!tasks.length) {
      message.warning('请至少选择一个服务的模板');
      return;
    }
    const results = [];
    const idByKind = editRow
      ? { 'singbox': editRow.sb_id, 'mihomo': editRow.mh_id, MTProto: editRow.mp_id }
      : {};
    for (const [label, url, body] of tasks) {
      const existingId = idByKind[label];
      try {
        if (existingId) await api(`${url.replace('/api/', '')}/${existingId}`, { method: 'PUT', body });
        else await apiPost(url, body);
        results.push(`${label} ${existingId ? '更新' : '绑定'}成功`);
      } catch (e) {
        results.push(`${label} 失败：${e.message || '未知错误'}`);
      }
    }
    message.info(results.join('；'));
    setOpen(false);
    form.resetFields();
    load();
  };

  const columns = [
    { title: 'ID', dataIndex: 'server_id', width: 70 },
    { title: '节点', dataIndex: 'node_name', width: 150, ellipsis: true },
    {
      title: '服务器',
      width: 180,
      render: (_, r) => (
        <div>
          <div>{r.server_name}</div>
          <div className="pp-muted pp-mono" style={{ fontSize: 12 }}>{r.host || ''}</div>
        </div>
      ),
    },
    {
      title: 'singbox 模板',
      width: 150,
      render: (_, r) => (r.sb_id ? <Tag color="geekblue">{r.sb_tpl_name || '内置默认'}</Tag> : <span className="pp-muted">未绑定</span>),
    },
    {
      title: 'mihomo 模板',
      width: 150,
      render: (_, r) => (r.mh_id ? <Tag color="purple">{r.mh_tpl_name || '—'}</Tag> : <span className="pp-muted">未绑定</span>),
    },
    {
      title: 'MTProto 模板',
      width: 150,
      render: (_, r) => (r.mp_id ? <Tag color="volcano">{r.mp_tpl_name || '—'}</Tag> : <span className="pp-muted">未绑定</span>),
    },
    { title: '域名', dataIndex: 'domain', width: 170, ellipsis: true, render: (v) => v || <span className="pp-muted">—</span> },
    {
      title: '授权 UUID',
      width: 160,
      render: (_, r) => (
        <span className="pp-muted" style={{ fontSize: 12 }}>
          sb {r.sb_uuids || 0} / mh {r.mh_uuids || 0} / mp {r.mp_uuids || 0}
        </span>
      ),
    },
    {
      title: '状态',
      width: 150,
      render: (_, r) => {
        const total = (r.sb_id ? 1 : 0) + (r.mh_id ? 1 : 0) + (r.mp_id ? 1 : 0);
        const on = (r.sb_id && r.sb_enabled ? 1 : 0) + (r.mh_id && r.mh_enabled ? 1 : 0) + (r.mp_id && r.mp_enabled ? 1 : 0);
        if (!total) return <Tag>未绑定</Tag>;
        if (on === total) return <Tag color="success">{total} 服务 · 启用</Tag>;
        return <Tag color="orange">{total} 服务 · 启用 {on}</Tag>;
      },
    },
    { title: '最新下发', dataIndex: 'last_apply_at', width: 160, render: (v) => (v ? fmtTime(v) : <span className="pp-muted">未下发</span>) },
    {
      title: '操作',
      width: 110,
      render: (_, r) => (
        <Button
          size="small"
          type="link"
          onClick={() => {
            setEditRow(r);
            form.setFieldsValue({ server_id: r.server_id, name: r.node_name, domain: r.domain || '', sb_tpl: r.sb_tpl || undefined, mh_tpl: r.mh_tpl || undefined, mp_tpl: r.mp_tpl || undefined });
            setOpen(true);
          }}
        >
          绑定服务
        </Button>
      ),
    },
  ];

  return (
    <PageCard
      title="节点绑定"
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditRow(null); form.resetFields(); setOpen(true); }}>新建绑定</Button>
        </Space>
      }
    >
      <Table rowKey="server_id" size="middle" loading={loading} dataSource={rows} columns={columns} scroll={{ x: 1550 }} pagination={{ pageSize: 10, hideOnSinglePage: true }} />

      <Modal title={editRow ? '编辑节点服务' : '绑定节点服务'} open={open} onCancel={() => setOpen(false)} onOk={submit} width={560} destroyOnClose>
        <Form form={form} layout="vertical">
          <Form.Item name="server_id" label="服务器" rules={[{ required: true, message: '请选择服务器' }]}>
            <Select showSearch optionFilterProp="label" options={options} placeholder="选择服务器" />
          </Form.Item>

          <Form.Item name="name" label="节点名称前缀" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如 hk-01" />
          </Form.Item>
          <Form.Item name="domain" label="域名（可选）">
            <Input placeholder="hk.example.com" />
          </Form.Item>
          <Form.Item name="sb_tpl" label="sing-box 模板">
            <Select allowClear options={tpl.sb} placeholder="不绑定 sing-box" />
          </Form.Item>
          <Form.Item name="mh_tpl" label="mihomo 模板（仅服务端模板）">
            <Select allowClear options={tpl.mh} placeholder="不绑定 mihomo" />
          </Form.Item>
          <Form.Item name="mp_tpl" label="MTProto 模板">
            <Select allowClear options={tpl.mp} placeholder="不绑定 MTProto" />
          </Form.Item>
        </Form>
      </Modal>
    </PageCard>
  );
}
