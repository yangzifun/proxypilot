import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { App as AntdApp, Button, InputNumber, Modal, Progress, Select, Space, Switch, Table, Tag } from 'antd';
import { CloudDownloadOutlined, ReloadOutlined, SettingOutlined } from '@ant-design/icons';
import { api, apiPost } from '../api';
import { PageCard } from '../components';
import { fmtBytes } from '../format';

const MODE_LABEL = { both: '上行+下行', rx: '仅上行', tx: '仅下行' };
const STATE_TAG = {
  normal: <Tag color="success">正常</Tag>,
  warning: <Tag color="warning">接近上限</Tag>,
  exceeded: <Tag color="error">已超限</Tag>,
};

export default function TrafficLimits() {
  const { message } = AntdApp.useApp();
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [modal, setModal] = useState({ open: false, row: null });
  const [form, setForm] = useState({});

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api('servers/traffic-limits');
      setRows(res.data || []);
    } catch (e) {
      message.error(e.message || '加载失败');
    } finally {
      setLoading(false);
    }
  }, [message]);

  useEffect(() => {
    load();
  }, [load]);

  const openEdit = (row) => {
    setModal({ open: true, row });
    setForm({
      enabled: !!row.enabled,
      mode: row.mode || 'both',
      limit_gb: row.limit_bytes ? Number((row.limit_bytes / 1024 ** 3).toFixed(2)) : 100,
      reset_day: row.reset_day || 1,
      reset_hour: row.reset_hour || 0,
      action: row.action || 'notify',
      warn_percent: row.warn_percent || 80,
    });
  };

  const save = async () => {
    if (!modal.row) return;
    try {
      await api(`servers/${modal.row.server_id}/traffic-limit`, {
        method: 'PUT',
        body: {
          enabled: form.enabled ? 1 : 0,
          mode: form.mode,
          limit_bytes: Math.round((Number(form.limit_gb) || 0) * 1024 ** 3),
          reset_day: Number(form.reset_day) || 1,
          reset_hour: Number(form.reset_hour) || 0,
          action: form.action,
          warn_percent: Number(form.warn_percent) || 80,
        },
      });
      message.success('已保存');
      setModal({ open: false, row: null });
      load();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };

  const resetNow = async () => {
    if (!modal.row) return;
    try {
      await apiPost(`servers/${modal.row.server_id}/traffic-limit/reset`, {});
      message.success('已重置本周期（累计计数器不受影响）');
      setModal({ open: false, row: null });
      load();
    } catch (e) {
      message.error(e.message || '重置失败');
    }
  };

  const unsuspend = async (row) => {
    try {
      await apiPost(`servers/${row.server_id}/traffic-limit/unsuspend`, {});
      message.success('已解除暂停');
      load();
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const usedOf = (row) => {
    if (row.mode === 'rx') return Number(row.rx_bytes) || 0;
    if (row.mode === 'tx') return Number(row.tx_bytes) || 0;
    return (Number(row.rx_bytes) || 0) + (Number(row.tx_bytes) || 0);
  };

  const columns = useMemo(() => [
    { title: '服务器', dataIndex: 'name', render: (v, row) => <span>{v || `#${row.server_id}`}</span> },
    { title: '状态', dataIndex: 'state', width: 100, render: (v) => STATE_TAG[v] || STATE_TAG.normal },
    { title: '上行', dataIndex: 'rx_bytes', width: 110, render: (v) => fmtBytes(v) },
    { title: '下行', dataIndex: 'tx_bytes', width: 110, render: (v) => fmtBytes(v) },
    {
      title: '本周期用量 / 上限', width: 260,
      render: (_, row) => {
        const used = usedOf(row);
        const limit = Number(row.limit_bytes) || 0;
        const pct = limit > 0 ? Math.min(100, (used * 100) / limit) : 0;
        return (
          <div>
            <div style={{ fontSize: 12 }}>
              {fmtBytes(used)} / {limit > 0 ? fmtBytes(limit) : '未设置'}
              {row.enabled ? '' : ' （未启用）'}
            </div>
            {limit > 0 ? <Progress percent={Number(pct.toFixed(1))} size="small" status={row.state === 'exceeded' ? 'exception' : pct >= 80 ? 'active' : 'normal'} /> : null}
          </div>
        );
      },
    },
    {
      title: '计费方向', width: 110, render: (_, row) => (MODE_LABEL[row.mode] || '上行+下行'),
    },
    {
      title: '重置周期', width: 140, render: (_, row) => `每月 ${row.reset_day || 1} 日 ${String(row.reset_hour || 0).padStart(2, '0')}:00`,
    },
    {
      title: '超限动作', width: 120, render: (_, row) => (row.action === 'suspend' ? <Tag color="red">暂停节点</Tag> : <Tag>仅告警</Tag>),
    },
    {
      title: '操作', width: 180,
      render: (_, row) => (
        <Space size={0}>
          <Button size="small" type="link" icon={<SettingOutlined />} onClick={() => openEdit(row)}>设置</Button>
          {row.state === 'exceeded' ? <Button size="small" type="link" onClick={() => unsuspend(row)}>解除暂停</Button> : null}
        </Space>
      ),
    },
  ], [rows]);

  return (
    <PageCard
      title={<span><CloudDownloadOutlined /> 流量限额</span>}
      extra={<Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>}
    >
      <div style={{ padding: '0 16px 8px' }} className="pp-muted">
        按服务器统计上行/下行累计流量，支持设置上限与按月重置；超限后可仅告警或暂停节点（暂停后该节点不再出现在用户订阅中）。
      </div>
      <Table rowKey="server_id" size="medium" loading={loading} dataSource={rows} columns={columns} pagination={false} />

      <Modal
        title={`流量限制 · ${(modal.row && modal.row.name) || ''}`}
        open={modal.open}
        onCancel={() => setModal({ open: false, row: null })}
        onOk={save}
        okText="保存"
        destroyOnHidden
        footer={(_, { OkBtn, CancelBtn }) => (
          <Space>
            <Button danger onClick={resetNow}>重置本周期</Button>
            <CancelBtn />
            <OkBtn />
          </Space>
        )}
      >
        <div style={{ display: 'grid', gap: 12 }}>
          <div className="pp-muted">
            本周期：↑ {fmtBytes(modal.row?.rx_bytes)} / ↓ {fmtBytes(modal.row?.tx_bytes)}
            {modal.row?.state === 'exceeded' ? '（已超限）' : modal.row?.state === 'warning' ? '（接近上限）' : ''}
          </div>
          <div>启用上限：<Switch checked={!!form.enabled} onChange={(v) => setForm((f) => ({ ...f, enabled: v }))} /></div>
          <div>
            计费方向：
            <Select value={form.mode} style={{ width: 160, marginLeft: 8 }} onChange={(v) => setForm((f) => ({ ...f, mode: v }))}
              options={[{ value: 'both', label: '上行+下行' }, { value: 'rx', label: '仅上行' }, { value: 'tx', label: '仅下行' }]} />
          </div>
          <div>
            流量上限：
            <InputNumber min={0} value={form.limit_gb} style={{ width: 140, margin: '0 8px' }} onChange={(v) => setForm((f) => ({ ...f, limit_gb: v }))} /> GB
          </div>
          <div>
            重置日期：
            <InputNumber min={1} max={31} value={form.reset_day} style={{ width: 80, margin: '0 8px' }} onChange={(v) => setForm((f) => ({ ...f, reset_day: v }))} /> 日
            <InputNumber min={0} max={23} value={form.reset_hour} style={{ width: 80, margin: '0 8px' }} onChange={(v) => setForm((f) => ({ ...f, reset_hour: v }))} /> 时
          </div>
          <div>
            超限动作：
            <Select value={form.action} style={{ width: 200, marginLeft: 8 }} onChange={(v) => setForm((f) => ({ ...f, action: v }))}
              options={[{ value: 'notify', label: '仅告警' }, { value: 'suspend', label: '暂停节点（订阅移除）' }]} />
          </div>
          <div>
            告警阈值：
            <InputNumber min={1} max={100} value={form.warn_percent} style={{ width: 90, margin: '0 8px' }} onChange={(v) => setForm((f) => ({ ...f, warn_percent: v }))} /> %
          </div>
        </div>
      </Modal>
    </PageCard>
  );
}
