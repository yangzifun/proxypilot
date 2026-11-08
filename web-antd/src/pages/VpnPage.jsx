import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Form, Input, Space, Table, Tag, Typography } from 'antd';
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

// 组网：节点通过 vpncenter.yzfn.top 与中心服务器建立的自建组网。
// 只展示已加入组网的服务器；组网与 VPN 绑定解耦，组网内网可用于内部服务。
export default function VpnPage() {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [mesh, setMesh] = useState({ server: {}, nodes: [], total_servers: 0, joined: 0 });
  const [settings, setSettings] = useState(null);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const [m, s] = await Promise.all([api('admin/vpn/mesh'), api('admin/vpn/settings')]);
      setMesh(m && m.nodes ? m : { server: {}, nodes: [], total_servers: 0, joined: 0 });
      const d = s && s.data ? s.data : s || {};
      setSettings(d);
      form.setFieldsValue({
        server_mesh_domain: d.server_mesh_domain || 'vpncenter.yzfn.top',
        server_mesh_ip4: d.server_mesh_ip4 || '10.99.0.1',
        mesh_cidr4: d.mesh_cidr4 || '10.99.0.0/16',
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

  const columns = [
    { title: '服务器', dataIndex: 'name' },
    { title: '组网 IP', dataIndex: 'mesh_ip4', width: 140, render: (v) => v || '—' },
    { title: '组网域名', dataIndex: 'domain', width: 200, render: (v) => v || '—' },
    {
      title: '状态', dataIndex: 'state', width: 90,
      render: (v) => <Tag color={v === 'up' ? 'green' : 'red'}>{v === 'up' ? 'up' : 'down'}</Tag>,
    },
    { title: 'RTT', dataIndex: 'rtt_ms', width: 90, render: (v) => (v ? `${v} ms` : '—') },
    { title: '最近心跳', dataIndex: 'last_seen', width: 170, render: fmtTime },
    { title: 'Agent', dataIndex: 'agent_version', width: 120, render: (v) => v || '—' },
  ];

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <PageCard
        title="自建组网设置"
        extra={(
          <Space>
            <Tag color="blue">中心 {server.mesh_domain || 'vpncenter.yzfn.top'}</Tag>
            <Tag>已加入 {mesh.joined || 0}/{mesh.total_servers || 0}</Tag>
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
          <Form.Item>
            <Button type="primary" htmlType="submit" icon={<SaveOutlined />}>保存</Button>
          </Form.Item>
        </Form>
        <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
          节点通过中心域名与中心服务器建立自建组网（不依赖 WARP）。组网后的内网 IP 可用于
          内网服务与 Agent 数据同步；组网与 VPN 节点绑定相互独立，未绑定 VPN 的服务器也可以加入。
          Agent 内网通信/同步开关在「系统设置 → 高级设置」中配置。
        </Typography.Paragraph>
      </PageCard>

      <PageCard title="已加入组网的服务器">
        <Table
          rowKey="id"
          size="small"
          pagination={{ pageSize: 12 }}
          loading={loading}
          dataSource={rows}
          columns={columns}
          locale={{ emptyText: '还没有服务器加入自建组网（等待 Agent 上报组网 IP）' }}
        />
      </PageCard>
    </Space>
  );
}
