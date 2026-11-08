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

function transportLabel(v) {
  if (v === 'warp') return 'WARP（优先）';
  if (v === 'masque') return '自建管理网';
  if (v === 'auto') return '自动';
  return v || '—';
}

// 组网：仅展示服务端 ↔ 节点端的自建管理网状态。
// 用户端不接入该网络；WARP 优先，自建管理网作为备用。
export default function VpnPage() {
  const { message } = AntdApp.useApp();
  const [mesh, setMesh] = useState({ server: {}, nodes: [] });
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const m = await api('admin/vpn/mesh');
      setMesh(m && m.nodes ? m : { server: {}, nodes: [] });
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
  const nodes = mesh.nodes || [];
  const online = nodes.filter((x) => x.link_state === 'up').length;

  const columns = [
    { title: '节点', dataIndex: 'name' },
    { title: '所属服务器', dataIndex: 'server_id', width: 110 },
    { title: '组网 IP', dataIndex: 'mesh_ip4', width: 130, render: (v) => v || '—' },
    { title: '同步通道', dataIndex: 'transport', width: 140, render: transportLabel },
    {
      title: '链路', dataIndex: 'link_state', width: 90,
      render: (v) => <Tag color={v === 'up' ? 'green' : 'red'}>{v === 'up' ? 'up' : 'down'}</Tag>,
    },
    { title: 'RTT', dataIndex: 'rtt_ms', width: 90, render: (v) => (v ? `${v} ms` : '—') },
    { title: '最近心跳', dataIndex: 'last_seen', width: 180, render: fmtTime },
  ];

  return (
    <PageCard
      title="服务端 ↔ 节点端自建组网"
      extra={<Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>}
    >
      <Space wrap size={8} style={{ marginBottom: 12 }}>
        <Tag color="blue">中心域名 vpncenter.yzfn.top</Tag>
        <Tag>中心 IP {server.mesh_ip4 || '10.99.0.1'}</Tag>
        <Tag>网段 {server.mesh_cidr4 || '10.99.0.0/16'}</Tag>
        <Tag color="geekblue">通道 {transportLabel(server.transport)}</Tag>
        <Tag>节点 {nodes.length}</Tag>
        <Tag color="green">在线 {online}</Tag>
      </Space>
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        该网络仅用于服务端与节点端之间的数据同步与探活，用户端不接入、也不能访问该网段。
        组网后节点可使用组网 IP 与中心同步；优先使用 WARP，自建管理网作为备用通道。
      </Typography.Paragraph>
      <Table
        rowKey="id"
        size="small"
        pagination={false}
        loading={loading}
        dataSource={nodes}
        columns={columns}
        locale={{ emptyText: '还没有已组网的节点' }}
      />
    </PageCard>
  );
}
