import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Space, Table, Tag, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { api } from '../api';
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

// 组网：只读展示自建组网状态；组网设置统一在「系统设置 → 高级设置」。
export default function VpnPage() {
  const { message } = AntdApp.useApp();
  const [mesh, setMesh] = useState({ server: {}, nodes: [], total_servers: 0, joined: 0 });
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const m = await api('admin/vpn/mesh');
      setMesh(m && m.nodes ? m : { server: {}, nodes: [], total_servers: 0, joined: 0 });
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
    <PageCard
      title="自建组网"
      extra={(
        <Space>
          <Tag color="blue">中心 {server.mesh_domain || 'vpncenter.yzfn.top'}</Tag>
          <Tag>中心组网 IP {server.mesh_ip4 || '10.99.0.1'}</Tag>
          <Tag>网段 {server.mesh_cidr4 || '10.99.0.0/16'}</Tag>
          <Tag>已加入 {mesh.joined || 0}/{mesh.total_servers || 0}</Tag>
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
        </Space>
      )}
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        节点通过中心域名与中心服务器建立自建组网，组网内网 IP 可用于内网服务与数据同步；
        组网与 VPN 节点绑定相互独立。组网参数请在「系统设置 → 高级设置」中配置。
      </Typography.Paragraph>
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
  );
}
