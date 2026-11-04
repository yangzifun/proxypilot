import React, { useEffect, useMemo, useState } from 'react';
import { App as AntdApp, Button, Form, Input, Modal, Select, Space, Table, Tag } from 'antd';
import { CloudUploadOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
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
  const [check, setCheck] = useState({ open: false, loading: false, data: [], fixing: false });
  const [applyingAll, setApplyingAll] = useState(false);
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
        mh: (mh.data || []).map((x) => ({ value: x.id, label: x.name + (x.mode === 'server' ? ' · 服务端' : ' · 订阅') })),
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

  const applyNode = async (row) => {
    try {
      const res = await apiPost(`/api/nodes/${row.server_id}/apply`, {});
      const results = res.results || [];
      const failed = results.filter((x) => x.ok === false);
      if (failed.length) message.warning(`下发完成，${failed.length} 个服务失败：${failed.map((f) => f.kind).join('、')}`);
      else message.success('已下发：' + results.map((x) => x.kind).join('、'));
      load();
    } catch (e) {
      message.error(e.message || '下发失败');
    }
  };
  const applyAll = async () => {
    const targets = rows.filter((r) => r.sb_id || r.mh_id || r.mp_id);
    if (!targets.length) {
      message.warning('没有可下发的节点');
      return;
    }
    setApplyingAll(true);
    let ok = 0;
    let fail = 0;
    for (const row of targets) {
      try {
        await apiPost(`/api/nodes/${row.server_id}/apply`, {});
        ok += 1;
      } catch (e) {
        fail += 1;
      }
    }
    setApplyingAll(false);
    if (fail) message.warning(`一键下发完成：成功 ${ok}，失败 ${fail}`);
    else message.success(`一键下发完成：${ok} 台服务器`);
    load();
  };
  const runCheck = async () => {
    setCheck((c) => ({ ...c, open: true, loading: true }));
    try {
      const res = await apiGet('/api/nodes/check');
      setCheck({ open: true, loading: false, data: res.data || [], fixing: false });
    } catch (e) {
      setCheck({ open: true, loading: false, data: [], fixing: false });
      message.error(e.message || '检查失败');
    }
  };
  const fixCheck = async () => {
    setCheck((c) => ({ ...c, fixing: true }));
    try {
      const res = await apiPost('/api/nodes/check/fix', {});
      message.success(`已修复 ${res.fixed || 0} 项（并集补齐 sing-box / mihomo 授权）`);
      await runCheck();
      load();
    } catch (e) {
      message.error(e.message || '修复失败');
      setCheck((c) => ({ ...c, fixing: false }));
    }
  };
  const submit = async () => {
    const v = await form.validateFields();
    const tasks = [];
    if (v.sb_tpl) tasks.push(['singbox', '/api/singbox/nodes', {
      name: (v.sb_name || v.name || '').trim(), server_id: v.server_id, template_id: v.sb_tpl,
      domain: v.sb_domain || v.domain || '', cdn_domain: v.sb_cdn || '', port_base: v.sb_port || 0,
    }]);
    if (v.mh_tpl) tasks.push(['mihomo', '/api/mihomo/nodes', {
      name: (v.mh_name || v.name || '').trim(), server_id: v.server_id, template_id: v.mh_tpl,
      domain: v.mh_domain || v.domain || '', port_base: v.mh_port || 0,
    }]);
    if (v.mp_tpl) tasks.push(['MTProto', '/api/mtproto/nodes', {
      name: (v.mp_name || v.name || '').trim(), server_id: v.server_id, template_id: v.mp_tpl,
      domain: v.mp_domain || v.domain || '', port: v.mp_port || 0,
    }]);
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
        <Space size={0}>
          <Button size="small" type="link" onClick={() => applyNode(r)}>下发</Button>
          <Button
            size="small"
            type="link"
            onClick={() => {
              const row = r;
              setEditRow(row);
              form.setFieldsValue({ server_id: row.server_id, name: row.node_name, domain: row.domain || '', sb_domain: row.domain || '', sb_cdn: row.sb_cdn || '', mh_domain: row.domain || '', mp_domain: row.domain || '', sb_tpl: row.sb_tpl || undefined, mh_tpl: row.mh_tpl || undefined, mp_tpl: row.mp_tpl || undefined });
              setOpen(true);
            }}
          >
            编辑
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <PageCard
      title="节点绑定"
      extra={
        <Space>
          <Button type="primary" icon={<CloudUploadOutlined />} loading={applyingAll} onClick={applyAll}>
            一键下发
          </Button>
          <Button onClick={runCheck}>UUID 一致性检查</Button>
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditRow(null); form.resetFields(); setOpen(true); }}>新建绑定</Button>
        </Space>
      }
    >
      <Table rowKey="server_id" size="middle" loading={loading} dataSource={rows} columns={columns} scroll={{ x: 1550 }} pagination={{ pageSize: 10, hideOnSinglePage: true }} />

      <Modal
        title="UUID 一致性检查"
        open={check.open}
        onCancel={() => setCheck((c) => ({ ...c, open: false }))}
        footer={
          <Space>
            <Button type="primary" loading={check.fixing} onClick={fixCheck} disabled={!check.data.length}>
              自动修复
            </Button>
            <Button onClick={() => setCheck((c) => ({ ...c, open: false }))}>关闭</Button>
          </Space>
        }
        width={780}
      >
        <Table
          rowKey="server_id"
          size="small"
          loading={check.loading}
          dataSource={check.data}
          pagination={false}
          locale={{ emptyText: '未发现不一致' }}
          columns={[
            { title: '服务器', dataIndex: 'server_name', width: 160 },
            { title: 'sing-box 缺', dataIndex: 'missing_sb', render: (v) => (v || []).length },
            { title: 'mihomo 缺', dataIndex: 'missing_mh', render: (v) => (v || []).length },
            { title: 'MTProto 缺', dataIndex: 'missing_mp', render: (v) => (v || []).length },
          ]}
        />
        <div className="pp-muted" style={{ fontSize: 12, marginTop: 8 }}>
          MTProto 缺失不会自动创建客户端，请在「用户管理 → 授权节点」中重新勾选该节点。
        </div>
      </Modal>

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
          <Form.Item noStyle shouldUpdate={(a, b) => a.sb_tpl !== b.sb_tpl}>
            {({ getFieldValue }) =>
              getFieldValue('sb_tpl') ? (
                <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                  <Form.Item name="sb_name" label="sing-box 节点名" style={{ width: 200 }}>
                    <Input placeholder="留空自动生成" />
                  </Form.Item>
                  <Form.Item name="sb_domain" label="域名" style={{ width: 240 }}>
                    <Input placeholder="hk.example.com" />
                  </Form.Item>
                  <Form.Item name="sb_cdn" label="CDN 域名" style={{ width: 240 }}>
                    <Input placeholder="cdn.example.com（可选）" />
                  </Form.Item>
                  <Form.Item name="sb_port" label="端口基址" style={{ width: 140 }}>
                    <Input placeholder="0 = 跟随模板" />
                  </Form.Item>
                </div>
              ) : null
            }
          </Form.Item>
          <Form.Item name="mh_tpl" label="mihomo 模板（仅服务端模板）">
            <Select allowClear options={tpl.mh} placeholder="不绑定 mihomo" />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(a, b) => a.mh_tpl !== b.mh_tpl}>
            {({ getFieldValue }) =>
              getFieldValue('mh_tpl') ? (
                <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                  <Form.Item name="mh_name" label="mihomo 节点名" style={{ width: 200 }}>
                    <Input placeholder="留空自动生成" />
                  </Form.Item>
                  <Form.Item name="mh_domain" label="域名" style={{ width: 240 }}>
                    <Input placeholder="hk.example.com" />
                  </Form.Item>
                  <Form.Item name="mh_port" label="端口基址" style={{ width: 140 }}>
                    <Input placeholder="0 = 跟随模板" />
                  </Form.Item>
                </div>
              ) : null
            }
          </Form.Item>
          <Form.Item name="mp_tpl" label="MTProto 模板">
            <Select allowClear options={tpl.mp} placeholder="不绑定 MTProto" />
          </Form.Item>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Form.Item name="mp_name" label="MTProto 节点名" style={{ width: 200 }}>
              <Input placeholder="留空自动生成" />
            </Form.Item>
            <Form.Item
              name="mp_domain"
              label="MTProto 伪装域名"
              style={{ width: 260 }}
              dependencies={['mp_tpl']}
              rules={[
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    if (!getFieldValue('mp_tpl') || String(value || '').trim()) return Promise.resolve();
                    return Promise.reject(new Error('请填写 MTProto 伪装域名'));
                  },
                }),
              ]}
            >
              <Input placeholder="www.microsoft.com" />
            </Form.Item>
            <Form.Item name="mp_port" label="端口" style={{ width: 140 }}>
              <Input placeholder="0 = 跟随模板" />
            </Form.Item>
          </div>
        </Form>
      </Modal>
    </PageCard>
  );
}
