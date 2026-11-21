import React, { useEffect, useMemo, useState } from 'react';
import { App as AntdApp, Button, Form, Input, Modal, Select, Space, Table, Tag, Tooltip } from 'antd';
import { CloudUploadOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, apiGet, apiPost } from '../api';
import { PageCard } from '../components';
import CdnDomainsField, { collectCdnEntries, normalizeCdnEntries, cdnTagItems } from '../components/CdnDomainsField';
import { fmtTime } from '../format';
import VpnNodesPanel from './VpnNodesPanel';

/* 节点统一域名校验：允许域名或 IPv4，拒绝协议/路径/端口/空格 */
const NODE_DOMAIN_RE = /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i;
const NODE_IPV4_RE = /^(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)$/;
export function validateNodeDomain(value) {
  const raw = String(value == null ? '' : value).trim();
  if (!raw) return '请填写域名（可填域名或 IPv4）';
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(raw)) return '只填写域名或 IP，不要带协议';
  if (raw.includes('/')) return '只填写域名或 IP，不要带路径';
  if (raw.includes(':') || /\s/.test(raw)) return '只填写域名或 IP，不要带端口或空格';
  const v = raw.toLowerCase();
  if (NODE_IPV4_RE.test(v)) return '';
  if (!NODE_DOMAIN_RE.test(v)) return '域名格式不正确（例如 hk.example.com 或 203.0.113.10）';
  return '';
}

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
    // 校验失败时 antd 已展示字段错误，这里静默返回（避免未处理的 Promise rejection）
    const v = await form.validateFields().catch(() => null);
    if (!v) return;
    let cdnEntries = [];
    try {
      cdnEntries = collectCdnEntries(v.cdn_entries || []);
    } catch (e) {
      message.error(e.message || 'CDN 配置不正确');
      return;
    }
    // 兼容旧字段：只写启用中的域名；第一个启用域名写 cdn_domain（不再区分主/备）。
    // 名称随 cdn_entries 保存；编辑回填时 normalizeCdnEntries(row) 优先读取
    // 行数据里的 sb_cdn_entries / cdn_entries，再回退 sb_cdn/sb_cdn_domains。
    const enabledCdnEntries = cdnEntries.filter((x) => x.enabled !== false);
    const firstEnabledCdnDomain = (enabledCdnEntries[0] || {}).domain || '';
    const tasks = [];
    // 所有协议统一使用全局域名（sing-box/mihomo 写 domain；MTProto 写 host，伪装域名/端口由模板决定）
    const globalDomain = String(v.domain || '').trim();
    if (v.sb_tpl) tasks.push(['singbox', '/api/singbox/nodes', {
      name: (v.sb_name || v.name || '').trim(), server_id: v.server_id, template_id: v.sb_tpl,
      domain: globalDomain,
      // 多 CDN：cdn_entries 为主（含 enabled，不写 primary），旧字段只写启用域名
      cdn_entries: cdnEntries,
      cdn_domains: enabledCdnEntries.map((x) => x.domain),
      cdn_domain: firstEnabledCdnDomain,
    }]);
    if (v.mh_tpl) tasks.push(['mihomo', '/api/mihomo/nodes', {
      name: (v.mh_name || v.name || '').trim(), server_id: v.server_id, template_id: v.mh_tpl,
      domain: globalDomain,
    }]);
    if (v.mp_tpl) tasks.push(['MTProto', '/api/mtproto/nodes', {
      name: (v.mp_name || v.name || '').trim(), server_id: v.server_id, template_id: v.mp_tpl,
      // MTProto 不再发 domain/port：伪装域名与端口统一由模板决定
      host: globalDomain,
    }]);
    if (!tasks.length) {
      message.warning('请至少选择一个服务的模板');
      return;
    }
    const results = [];
    const idByKind = editRow
      ? { 'singbox': editRow.sb_id, 'mihomo': editRow.mh_id, MTProto: editRow.mp_id }
      : {};
    let sbNodeId = editRow ? editRow.sb_id : 0;
    for (const [label, url, body] of tasks) {
      const existingId = idByKind[label];
      try {
        let res;
        if (existingId) res = await api(`${url.replace('/api/', '')}/${existingId}`, { method: 'PUT', body });
        else res = await apiPost(url, body);
        if (label === 'singbox' && !sbNodeId) {
          sbNodeId = Number(res?.data?.id || res?.id || res?.data?.node?.id || 0) || 0;
        }
        results.push(`${label} ${existingId ? '更新' : '绑定'}成功`);
      } catch (e) {
        results.push(`${label} 失败：${e.message || '未知错误'}`);
      }
    }
    const failed = results.filter((x) => x.includes('失败'));
    // CDN 是否有变化：编辑时对比原回填值，新建时只要填了 CDN 就算有变化
    const cdnChanged = editRow
      ? JSON.stringify(normalizeCdnEntries(editRow)) !== JSON.stringify(cdnEntries)
      : cdnEntries.length > 0;
    if (failed.length) {
      // 部分失败：保留弹窗便于修正；同时刷新列表（成功的服务已保存）
      message.warning(results.join('；'));
      await load();
      return;
    }
    let tip = results.join('；');
    if (cdnChanged) {
      // CDN 变更后立即刷新订阅配置（不依赖手动“下发”）；只处理本节点，失败不回滚节点保存。
      try {
        await apiPost('/api/singbox/reconcile', sbNodeId ? { force: true, node_id: sbNodeId } : { force: false });
        tip += '；CDN 已保存并刷新订阅配置';
      } catch (e) {
        tip += '；CDN 已保存，但订阅配置刷新失败，请稍后重新下发';
      }
    }
    message.success(tip);
    // 先关闭/清空，再等待列表刷新完成，确保再次编辑拿到最新行（含 CDN）
    setOpen(false);
    setEditRow(null);
    form.resetFields();
    await load();
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
      title: '模板',
      width: 320,
      render: (_, r) => {
        const items = [];
        if (r.sb_id) items.push({ key: 'sb', label: `sing-box · ${r.sb_tpl_name || '内置默认'}`, color: 'geekblue' });
        if (r.mh_id) items.push({ key: 'mh', label: `mihomo · ${r.mh_tpl_name || '—'}`, color: 'purple' });
        if (r.mp_id) items.push({ key: 'mp', label: `MTProto · ${r.mp_tpl_name || '默认模板'}`, color: 'volcano' });
        if (!items.length) return <span className="pp-muted">未绑定</span>;
        return (
          <Space size={4} wrap>
            {items.map((it) => (
              <Tag key={it.key} color={it.color} style={{ marginInlineEnd: 0 }}>
                {it.label}
              </Tag>
            ))}
          </Space>
        );
      },
    },
    { title: '域名', dataIndex: 'domain', width: 170, ellipsis: true, render: (v) => v || <span className="pp-muted">—</span> },
    {
      title: 'CDN',
      width: 200,
      render: (_, r) => {
        const tags = cdnTagItems(r);
        if (!tags.length) return <span className="pp-muted">—</span>;
        return (
          <Space size={4} wrap>
            {tags.map((t) => (
              <Tooltip key={t.key} title={t.tooltip}>
                <Tag
                  color={t.enabled ? t.color : 'default'}
                  style={{
                    marginInlineEnd: 0,
                    border: 'none',
                    backgroundColor: t.enabled ? t.color : '#e2e8f0',
                    borderColor: t.enabled ? t.color : '#e2e8f0',
                    color: t.enabled ? '#fff' : '#64748b',
                    fontWeight: 500,
                  }}
                >
                  {t.label}
                  {t.enabled ? '' : '（停用）'}
                </Tag>
              </Tooltip>
            ))}
          </Space>
        );
      },
    },
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
              // 先清空上一次编辑残留（尤其 CDN 列表），再回填当前行
              form.resetFields();
              setEditRow(row);
              form.setFieldsValue({
                server_id: row.server_id,
                name: row.node_name,
                domain: row.domain || '',
                cdn_entries: normalizeCdnEntries(row),
                sb_tpl: row.sb_tpl || undefined,
                mh_tpl: row.mh_tpl || undefined,
                mp_tpl: row.mp_tpl || undefined,
              });
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
    <>
      <PageCard
      title="代理节点绑定"
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
      <Table rowKey="server_id" size="middle" loading={loading} dataSource={rows} columns={columns} scroll={{ x: 1570 }} pagination={{ pageSize: 10, hideOnSinglePage: true }} />

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
          MTProto 缺失不会自动创建客户端，请在「订阅用户 → 授权节点」中重新勾选该节点。
        </div>
      </Modal>

      <Modal
        title={editRow ? '编辑节点服务' : '绑定节点服务'}
        open={open}
        onCancel={() => {
          setOpen(false);
          setEditRow(null);
          form.resetFields();
        }}
        onOk={submit}
        width={560}
        destroyOnClose
      >
        <Form form={form} layout="vertical">
          <Form.Item name="server_id" label="服务器" rules={[{ required: true, message: '请选择服务器' }]}>
            <Select showSearch optionFilterProp="label" options={options} placeholder="选择服务器" />
          </Form.Item>

          <Form.Item name="name" label="节点名称前缀" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如 hk-01" />
          </Form.Item>
          <Form.Item
            name="domain"
            label="域名（必填，可填域名或 IP）"
            rules={[
              { required: true, whitespace: true, message: '请填写域名（可填域名或 IPv4）' },
              {
                validator: (_, val) => {
                  const err = validateNodeDomain(val);
                  return err ? Promise.reject(new Error(err)) : Promise.resolve();
                },
              },
            ]}
            extra="所有协议（sing-box / mihomo / MTProto）统一使用该域名；可填域名或 IPv4，不要带协议/路径/端口"
          >
            <Input placeholder="hk.example.com 或 203.0.113.10" />
          </Form.Item>
          <Form.Item name="sb_tpl" label="sing-box 模板">
            <Select allowClear options={tpl.sb} placeholder="不绑定 sing-box" />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(a, b) => a.sb_tpl !== b.sb_tpl}>
            {({ getFieldValue }) =>
              getFieldValue('sb_tpl') ? (
                <>
                  <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                    <Form.Item name="sb_name" label="sing-box 节点名" style={{ width: 200 }}>
                      <Input placeholder="留空自动生成" />
                    </Form.Item>
                  </div>
                  <CdnDomainsField name="cdn_entries" label="CDN 域名" />
                </>
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
                </div>
              ) : null
            }
          </Form.Item>
          <Form.Item
            name="mp_tpl"
            label="MTProto 模板"
            extra="伪装域名与端口由 MTProto 模板统一设定，节点不再单独填写"
          >
            <Select allowClear options={tpl.mp} placeholder="不绑定 MTProto" />
          </Form.Item>
          <Form.Item name="mp_name" label="MTProto 节点名" style={{ width: 240 }}>
            <Input placeholder="留空自动生成" />
          </Form.Item>
        </Form>
      </Modal>
      </PageCard>
      <div style={{ marginTop: 16 }}>
        <VpnNodesPanel />
      </div>
    </>
  );
}
