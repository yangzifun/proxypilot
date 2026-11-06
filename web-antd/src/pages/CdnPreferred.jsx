import React, { useCallback, useEffect, useState } from 'react';
import { App as AntdApp, AutoComplete, Button, Input, Modal, Popconfirm, Space, Switch, Table, Tag } from 'antd';
import { CheckCircleOutlined, CloudDownloadOutlined, DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { apiDelete, apiGet, apiPost, apiPut } from '../api';
import { PageCard } from '../components';

export default function CdnPreferred() {
  const { message } = AntdApp.useApp();
  const [rows, setRows] = useState([]);
  const [groups, setGroups] = useState([]);
  const [group, setGroup] = useState('default');
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState('');
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [importGroup, setImportGroup] = useState('default');
  const [enabled, setEnabled] = useState(false);
  const [editRow, setEditRow] = useState(null);
  const [editForm, setEditForm] = useState({ address: '', port: 443, remark: '' });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiGet('/api/cdn-preferred', { group });
      setRows(res.data || []);
      const g = await apiGet('/api/cdn-preferred/groups');
      setGroups(g.data || []);
      const en = await apiGet('/api/cdn-preferred/enabled');
      setEnabled(!!en.enabled);
    } catch (e) {
      message.error(e.message || '加载失败');
    } finally {
      setLoading(false);
    }
  }, [group, message]);

  useEffect(() => {
    load();
  }, [load]);

  const toggle = async (row, enabled) => {
    try {
      await apiPut(`/api/cdn-preferred/${row.id}`, { enabled });
      setRows((rs) => rs.map((r) => (r.id === row.id ? { ...r, enabled: enabled ? 1 : 0 } : r)));
      message.success(enabled ? '已启用' : '已停用');
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const move = async (row, delta) => {
    const idx = rows.findIndex((r) => r.id === row.id);
    const target = idx + delta;
    if (idx < 0 || target < 0 || target >= rows.length) return;
    const next = [...rows];
    [next[idx], next[target]] = [next[target], next[idx]];
    setRows(next);
    try {
      await apiPut(`/api/cdn-preferred/${row.id}`, { sort: target });
      await apiPut(`/api/cdn-preferred/${next[idx].id}`, { sort: idx });
      load();
    } catch (e) {
      message.error(e.message || '排序失败');
    }
  };

  const remove = async (row) => {
    try {
      await apiDelete(`/api/cdn-preferred/${row.id}`);
      message.success('已删除');
      load();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const openEdit = (row) => {
    setEditRow(row);
    setEditForm({ address: row.address, port: row.port || 443, remark: row.remark || '' });
  };

  const saveEdit = async () => {
    if (!editRow) return;
    const addr = String(editForm.address || '').trim();
    if (!addr) {
      message.warning('地址不能为空');
      return;
    }
    const port = Number(editForm.port) || 443;
    setBusy('edit');
    try {
      await apiPut(`/api/cdn-preferred/${editRow.id}`, { address: addr, port, remark: String(editForm.remark || '').trim() });
      message.success('已保存');
      setEditRow(null);
      load();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setBusy('');
    }
  };

  const doImport = async () => {
    if (!importText.trim()) {
      message.warning('请粘贴优选地址');
      return;
    }
    setBusy('import');
    try {
      const res = await apiPost('/api/cdn-preferred/import', { group_name: importGroup.trim() || 'default', text: importText });
      message.success(`导入完成：新增 ${res.added || 0} 条，跳过 ${res.skipped || 0} 条`);
      setImportOpen(false);
      setImportText('');
      setGroup(importGroup.trim() || 'default');
      load();
    } catch (e) {
      message.error(e.message || '导入失败');
    } finally {
      setBusy('');
    }
  };

  const toggleGlobal = async (next) => {
    setEnabled(next);
    try {
      await apiPut('/api/cdn-preferred/enabled', { enabled: next });
      message.success(next ? '已启用：CDN 节点订阅将使用优选地址' : '已关闭优选替换');
    } catch (e) {
      setEnabled(!next);
      message.error(e.message || '保存失败');
    }
  };

  const checkAll = async () => {
    setBusy('check');
    try {
      const res = await apiPost('/api/cdn-preferred/check', {});
      const list = res.data || [];
      const ok = list.filter((x) => x.ok).length;
      message.success(`检测完成：可用 ${ok} / ${list.length}`);
      load();
    } catch (e) {
      message.error(e.message || '检测失败');
    } finally {
      setBusy('');
    }
  };

  const addOne = async () => {
    let value = '';
    Modal.confirm({
      title: '新增优选地址',
      content: (
        <div>
          <div className="pp-muted" style={{ fontSize: 12, marginBottom: 6 }}>格式：host:port#备注（端口可省略，默认 443）</div>
          <Input placeholder="162.159.198.1:443#甬哥优选" onChange={(e) => { value = e.target.value; }} />
        </div>
      ),
      onOk: async () => {
        if (!value.trim()) return;
        await apiPost('/api/cdn-preferred', { group_name: group, address: value.split('#')[0].trim().split(':')[0], port: 443, remark: value.split('#')[1] || '' });
        load();
      },
    });
  };

  const columns = [
    {
      title: '排序', width: 90, render: (_, row, idx) => (
        <Space size={0}>
          <Button size="small" type="text" disabled={idx === 0} onClick={() => move(row, -1)}>↑</Button>
          <Button size="small" type="text" disabled={idx === rows.length - 1} onClick={() => move(row, 1)}>↓</Button>
        </Space>
      ),
    },
    { title: '地址', dataIndex: 'address', render: (v) => <span className="pp-mono">{v}</span> },
    { title: '端口', dataIndex: 'port', width: 80 },
    { title: '备注', dataIndex: 'remark', ellipsis: true, render: (v) => v || '—' },
    {
      title: '状态', dataIndex: 'enabled', width: 100,
      render: (v, row) => (
        <Tag color={v ? 'success' : 'default'} style={{ cursor: 'pointer' }} onClick={() => toggle(row, !v)}>
          {v ? '已启用' : '已停用'}
        </Tag>
      ),
    },
    {
      title: '最近检测', width: 160,
      render: (_, row) => (row.last_check_at
        ? <span className="pp-muted">{row.last_check_ok ? `可用 · ${row.last_latency_ms}ms` : '不可用'}</span>
        : <span className="pp-muted">未检测</span>),
    },
    {
      title: '操作', width: 150,
      render: (_, row) => (
        <Space size={0}>
          <Button size="small" type="link" icon={<EditOutlined />} onClick={() => openEdit(row)}>编辑</Button>
          <Popconfirm title="删除该地址？" onConfirm={() => remove(row)}>
            <Button size="small" type="link" danger icon={<DeleteOutlined />}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const groupOptions = (groups.length ? groups : [{ group_name: 'default' }]).map((g) => ({ value: g.group_name }));

  return (
    <PageCard
      title={<span><CloudDownloadOutlined /> CDN 优选</span>}
      extra={
        <Space wrap>
          <Switch checked={enabled} onChange={toggleGlobal} checkedChildren="替换已启用" unCheckedChildren="替换已关闭" />
          <AutoComplete options={groupOptions} value={group} onChange={setGroup} style={{ width: 160 }} placeholder="分组" />
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
          <Button icon={<CheckCircleOutlined />} loading={busy === 'check'} onClick={checkAll}>一键检测</Button>
          <Button onClick={() => { setImportGroup(group); setImportOpen(true); }}>批量导入</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={addOne}>新增</Button>
        </Space>
      }
    >
      <div style={{ padding: '0 16px 8px' }} className="pp-muted">
        启用后，CDN 版本节点在用户订阅中会按列表展开为多个优选地址，SNI 与 Host 仍保持节点 CDN 域名。
        当前分组：<b>{group}</b>，共 {rows.length} 条，启用 {rows.filter((r) => r.enabled).length} 条。
      </div>
      <Table className="pp-table" rowKey="id" size="medium" loading={loading} dataSource={rows} columns={columns} pagination={false} />

      <Modal
        title="编辑优选地址"
        open={!!editRow}
        onCancel={() => setEditRow(null)}
        onOk={saveEdit}
        confirmLoading={busy === 'edit'}
        okText="保存"
        destroyOnHidden
      >
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 8 }}>修改后立即生效，已刷新订阅的客户端下次更新即可看到。</div>
        <div style={{ display: 'grid', gap: 10 }}>
          <Input addonBefore="地址" value={editForm.address} onChange={(e) => setEditForm((f) => ({ ...f, address: e.target.value }))} placeholder="162.159.198.1 或 yg1.ygkkk.dpdns.org" />
          <Input addonBefore="端口" type="number" value={editForm.port} onChange={(e) => setEditForm((f) => ({ ...f, port: e.target.value }))} />
          <Input addonBefore="备注" value={editForm.remark} onChange={(e) => setEditForm((f) => ({ ...f, remark: e.target.value }))} placeholder="甬哥优选 | 备注" />
        </div>
      </Modal>

      <Modal title="批量导入优选地址" open={importOpen} onCancel={() => setImportOpen(false)} onOk={doImport} confirmLoading={busy === 'import'} width={640} destroyOnHidden>
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 8 }}>
          每行一条，支持 <code>host:port#备注</code>，端口省略默认 443；重复地址自动跳过。
        </div>
        <Input placeholder="分组" value={importGroup} onChange={(e) => setImportGroup(e.target.value)} style={{ marginBottom: 8 }} />
        <Input.TextArea rows={12} value={importText} onChange={(e) => setImportText(e.target.value)}
          placeholder={'162.159.198.1:443#甬哥优选\nyg1.ygkkk.dpdns.org:443#甬哥优选'} />
      </Modal>
    </PageCard>
  );
}
