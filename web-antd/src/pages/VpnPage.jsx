import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Form, Input, Select, Space, Switch, Table, Tag, Typography } from 'antd';
import { ReloadOutlined, SaveOutlined } from '@ant-design/icons';
import { api, apiPut } from '../api';
import { PageCard } from '../components';

function fmtTime(v) {
  const n = Number(v || 0);
  if (!n) return '—';
  try {
    return new Date(n).toLocaleString();
  } catch (e) {
    return '—';
  }
}

function transportLabel(v) {
  if (v === 'warp') return 'WARP（优先）';
  if (v === 'masque') return '自建管理网';
  if (v === 'auto') return '自动';
  return v || '—';
}

// 组网：所有服务器 ↔ 服务端的组网状态与设置。
// 组网与 VPN 绑定解耦：服务器不一定绑定 VPN 节点，但都应上报组网/Agent 状态。
export default function VpnPage() {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [mesh, setMesh] = useState({ server: {}, nodes: [] });
  const [settings, setSettings] = useState(null);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const [m, s] = await Promise.all([api('admin/vpn/mesh'), api('admin/vpn/settings')]);
      setMesh(m && m.nodes ? m : { server: {}, nodes: [] });
      const d = s && s.data ? s.data : s || {};
      setSettings(d);
      form.setFieldsValue({
        server_mesh_domain: d.server_mesh_domain || 'vpncenter.yzfn.top',
        server_mesh_ip4: d.server_mesh_ip4 || '10.99.0.1',
        mesh_cidr4: d.mesh_cidr4 || '10.99.0.0/16',
        management_transport: d.management_transport || 'warp',
        masque_fallback: d.masque_fallback !== false,
      });
    } catch (e) {
      message.error(e.message || '加载组网状态失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const saveSettings = async () => {
    const values = await form.validateFields();
    try {
      const res = await apiPut('admin/vpn/settings', { ...(settings || {}), ...values });
      setSettings(res && res.data ? res.data : { ...(settings || {}), ...values });
      message.success('组网设置已保存');
      load();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };

  const server = mesh.server || {};
  const rows = mesh.nodes || [];
  const online = rows.filter((x) => x.link_state === 'up').length;

  const columns = [
    { title: '服务器', dataIndex: 'name' },
    { title: '组网 IP', dataIndex: 'mesh_ip4', width: 140, render: (v) => v || '—' },
    {
      title: 'Agent', dataIndex: 'agent_version', width: 150,
      render: (v, row) => (v ? <span>{v} · {row.agent_status || '—'}</span> : (row.agent_status || '—')),
    },
    { title: '同步通道', dataIndex: 'transport', width: 140, render: transportLabel },
    {
      title: '链路', dataIndex: 'link_state', width: 90,
      render: (v) => <Tag color={v === 'up' ? 'green' : 'red'}>{v === 'up' ? 'up' : 'down'}</Tag>,
    },
    { title: 'RTT', dataIndex: 'rtt_ms', width: 90, render: (v) => (v ? `${v} ms` : '—') },
    { title: '最近心跳', dataIndex: 'last_seen', width: 170, render: fmtTime },
    {
      title: 'VPN 节点', dataIndex: 'vpn_domain', width: 220,
      render: (v, row) => (row.vpn_node_id ? <span>{v || `#${row.vpn_node_id}`} · {row.vpn_state || '—'}</span> : '—'),
    },
  ];

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <PageCard
        title="组网设置"
        extra={(
          <Space>
            <Tag>中心域名 {server.mesh_domain || 'vpncenter.yzfn.top'}</Tag>
            <Tag>节点 {rows.length}</Tag>
            <Tag color="green">在线 {online}</Tag>
            <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
          </Space>
        )}
      >
        <Form form={form} layout="inline" onFinish={saveSettings}>
          <Form.Item name="server_mesh_domain" label="中心域名">
            <Input placeholder="vpncenter.yzfn.top" style={{ width: 220 }} />
          </Form.Item>
          <Form.Item name="server_mesh_ip4" label="中心组网 IP">
            <Input placeholder="10.99.0.1" style={{ width: 150 }} />
          </Form.Item>
          <Form.Item name="mesh_cidr4" label="组网网段">
            <Input placeholder="10.99.0.0/16" style={{ width: 150 }} />
          </Form.Item>
          <Form.Item name="management_transport" label="同步通道">
            <Select
              style={{ width: 180 }}
              options={[
                { value: 'warp', label: 'WARP（优先）' },
                { value: 'masque', label: '自建管理网' },
                { value: 'auto', label: '自动' },
              ]}
            />
          </Form.Item>
          <Form.Item name="masque_fallback" label="WARP 不可用回落" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" icon={<SaveOutlined />}>保存</Button>
          </Form.Item>
        </Form>
        <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
          组网用于服务端与节点端数据同步与探活，用户端不接入该网段；所有服务器都应上报组网状态，
          只有绑定了 VPN 节点的服务器才会显示域名与 VPN 状态。
        </Typography.Paragraph>
      </PageCard>

      <PageCard title="服务器组网状态">
        <Table
          rowKey="id"
          size="small"
          pagination={{ pageSize: 12 }}
          loading={loading}
          dataSource={rows}
          columns={columns}
          locale={{ emptyText: '还没有服务器组网数据' }}
        />
      </PageCard>
    </Space>
  );
}
