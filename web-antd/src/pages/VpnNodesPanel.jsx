import React, { useEffect, useState } from 'react';
import {
  App as AntdApp,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
} from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, apiDelete, apiPost, apiPut } from '../api';
import { PageCard } from '../components';

function fmtTime(v) {
  if (!v) return '—';
  const n = typeof v === 'number' ? v : Date.parse(v);
  if (!n || Number.isNaN(n)) return '—';
  try {
    return new Date(n).toLocaleString();
  } catch (e) {
    return '—';
  }
}

function stateTag(state) {
  const map = {
    active: ['green', '已启用'],
    pending: ['gold', '待审批'],
    disabled: ['default', '已停用'],
  };
  const [color, text] = map[state] || ['default', state || '—'];
  return <Tag color={color}>{text}</Tag>;
}

const EMPTY_NODE = {
  server_id: undefined,
  name: '',
  region: '',
  domain: '',
  endpoint: '',
  cert_mode: 'selfsigned',
  listen_port: 443,
  capacity: 500,
};

// VPN 节点绑定：在「节点绑定」页面创建/审批 VPN 节点，并给用户授权。
export default function VpnNodesPanel() {
  const { message } = AntdApp.useApp();
  const [nodeForm] = Form.useForm();
  const [grantForm] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [nodes, setNodes] = useState([]);
  const [nodeOpen, setNodeOpen] = useState(false);
  const [editingNode, setEditingNode] = useState(null);
  const [grantOpen, setGrantOpen] = useState(false);
  const [grantNode, setGrantNode] = useState(null);
  const [grants, setGrants] = useState([]);

  const load = async () => {
    setLoading(true);
    try {
      const res = await api('admin/vpn/nodes');
      setNodes((res && res.nodes) || []);
    } catch (e) {
      message.error(e.message || '加载 VPN 节点失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openNode = (node) => {
    setEditingNode(node || null);
    nodeForm.setFieldsValue(node ? {
      server_id: node.server_id,
      name: node.name,
      region: node.region,
      domain: node.domain || '',
      endpoint: node.endpoint,
      cert_mode: node.cert_mode || 'selfsigned',
      listen_port: node.listen_port || 443,
      capacity: node.capacity || 500,
    } : EMPTY_NODE);
    setNodeOpen(true);
  };

  const submitNode = async () => {
    const values = await nodeForm.validateFields();
    const payload = { ...values, domain: String(values.domain || '').trim().toLowerCase() };
    if (!String(payload.endpoint || '').trim()) {
      payload.endpoint = `${payload.domain}:${payload.listen_port || 443}`;
    }
    try {
      if (editingNode) {
        await apiPut(`admin/vpn/nodes/${editingNode.id}`, payload);
        message.success('节点已更新');
      } else {
        await apiPost('admin/vpn/nodes', payload);
        message.success('节点已创建，请点「启用」分配组网 IP');
      }
      setNodeOpen(false);
      load();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };

  const nodeAction = async (node, action) => {
    try {
      await apiPost(`admin/vpn/nodes/${node.id}/action`, { action });
      message.success(action === 'disable' ? '节点已停用' : '节点已启用');
      load();
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const deployNode = async (node) => {
    try {
      const res = await apiPost(`admin/vpn/nodes/${node.id}/deploy`, {});
      message.success(`部署任务已下发 #${res.task_id}`);
    } catch (e) {
      message.error(e.message || '部署失败');
    }
  };

  const removeNode = async (node) => {
    try {
      await apiDelete(`admin/vpn/nodes/${node.id}`);
      message.success('节点已删除');
      load();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const openGrants = async (node) => {
    setGrantNode(node);
    setGrantOpen(true);
    grantForm.resetFields();
    await loadGrants(node.id);
  };

  const loadGrants = async (nodeId) => {
    try {
      const res = await api(`admin/vpn/nodes/${nodeId}/grants`);
      setGrants((res && res.grants) || []);
    } catch (e) {
      message.error(e.message || '加载授权失败');
    }
  };

  const addGrant = async () => {
    const values = await grantForm.validateFields();
    const expiresAt = values.days > 0 ? Math.floor(Date.now() / 1000) + values.days * 86400 : 0;
    try {
      await apiPost(`admin/vpn/nodes/${grantNode.id}/grants`, {
        subject_id: values.subject_id.trim(),
        expires_at: expiresAt,
        max_devices: values.max_devices || 1,
        quota_bytes: values.quota_gb ? Math.round(values.quota_gb * 1024 * 1024 * 1024) : 0,
      });
      message.success('已绑定用户');
      grantForm.resetFields();
      await loadGrants(grantNode.id);
    } catch (e) {
      message.error(e.message || '绑定失败');
    }
  };

  const revokeGrant = async (g) => {
    try {
      await apiDelete(`admin/vpn/grants/${g.id}`);
      message.success('已解绑');
      await loadGrants(grantNode.id);
    } catch (e) {
      message.error(e.message || '解绑失败');
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '名称', dataIndex: 'name', width: 140 },
    { title: '服务器', dataIndex: 'server_id', width: 90 },
    { title: '接入地址', dataIndex: 'endpoint', width: 180, render: (v) => v || '—' },
    { title: '组网 IP', dataIndex: 'mesh_ip4', width: 120, render: (v) => v || '—' },
    { title: '客户端子网', dataIndex: 'client_cidr4', width: 130, render: (v) => v || '—' },
    { title: '状态', dataIndex: 'state', width: 90, render: stateTag },
    { title: '心跳', dataIndex: 'last_seen', width: 160, render: fmtTime },
    {
      title: '操作',
      key: 'op',
      render: (_, node) => (
        <Space size={4} wrap>
          {node.state !== 'active' ? (
            <Button size="small" type="link" onClick={() => nodeAction(node, 'approve')}>启用</Button>
          ) : (
            <Button size="small" type="link" danger onClick={() => nodeAction(node, 'disable')}>停用</Button>
          )}
          <Button size="small" type="link" onClick={() => openGrants(node)}>授权用户</Button>
          <Button size="small" type="link" onClick={() => deployNode(node)}>部署</Button>
          <Button size="small" type="link" onClick={() => openNode(node)}>编辑</Button>
          <Popconfirm title="删除该 VPN 节点？" onConfirm={() => removeNode(node)}>
            <Button size="small" type="link" danger>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const grantColumns = [
    { title: '用户 UUID', dataIndex: 'subject_id' },
    { title: '状态', dataIndex: 'enabled', width: 80, render: (v) => (v ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>) },
    { title: '到期', dataIndex: 'expires_at', width: 160, render: fmtTime },
    { title: '并发设备', dataIndex: 'max_devices', width: 90 },
    {
      title: '操作', key: 'op', width: 90,
      render: (_, g) => (
        <Popconfirm title="解除该绑定？" onConfirm={() => revokeGrant(g)}>
          <Button size="small" type="link" danger>解绑</Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <PageCard
      title="VPN 节点绑定"
      extra={(
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => openNode(null)}>新建 VPN 节点</Button>
        </Space>
      )}
    >
      <Table
        rowKey="id"
        size="small"
        loading={loading}
        columns={columns}
        dataSource={nodes}
        pagination={{ pageSize: 8 }}
        locale={{ emptyText: '还没有 VPN 节点，先创建并启用' }}
      />

      <Modal
        title={editingNode ? `编辑 VPN 节点 #${editingNode.id}` : '新建 VPN 节点'}
        open={nodeOpen}
        onCancel={() => setNodeOpen(false)}
        onOk={submitNode}
        destroyOnClose
      >
        <Form form={nodeForm} layout="vertical" initialValues={EMPTY_NODE}>
          <Form.Item name="server_id" label="服务器 ID（servers.id）" rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input placeholder="台北-01" />
          </Form.Item>
          <Form.Item name="region" label="区域">
            <Input placeholder="TW" />
          </Form.Item>
          <Form.Item
            name="domain"
            label="节点域名"
            extra="必填，用于签发证书与客户端连接，例如 tw1.vpn.example.com"
            rules={[{ required: true, message: '请填写节点域名' }]}
          >
            <Input placeholder="tw1.vpn.example.com" />
          </Form.Item>
          <Form.Item name="endpoint" label="客户端接入地址（UDP）" extra="留空自动使用 域名:端口">
            <Input placeholder="留空 = 域名:443" />
          </Form.Item>
          <Form.Item name="cert_mode" label="证书模式">
            <Select
              options={[
                { value: 'selfsigned', label: '自签（当前）' },
                { value: 'internal_ca', label: '平台内部 CA（待实现）' },
                { value: 'acme', label: '公网 ACME（待实现）' },
              ]}
            />
          </Form.Item>
          <Space size={16}>
            <Form.Item name="listen_port" label="监听端口">
              <InputNumber min={1} max={65535} />
            </Form.Item>
            <Form.Item name="capacity" label="最大会话">
              <InputNumber min={1} max={100000} />
            </Form.Item>
          </Space>
        </Form>
      </Modal>

      <Modal
        title={grantNode ? `授权用户 · ${grantNode.name || grantNode.id}` : '授权用户'}
        open={grantOpen}
        onCancel={() => setGrantOpen(false)}
        footer={null}
        width={720}
        destroyOnClose
      >
        <Form form={grantForm} layout="inline" style={{ marginBottom: 12 }}>
          <Form.Item name="subject_id" rules={[{ required: true, message: '填写用户 UUID' }]}>
            <Input placeholder="用户 UUID" style={{ width: 240 }} />
          </Form.Item>
          <Form.Item name="days">
            <InputNumber min={0} placeholder="有效天数" style={{ width: 100 }} />
          </Form.Item>
          <Form.Item name="max_devices">
            <InputNumber min={1} max={50} placeholder="并发设备" style={{ width: 100 }} />
          </Form.Item>
          <Form.Item name="quota_gb">
            <InputNumber min={0} placeholder="配额 GB" style={{ width: 100 }} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" onClick={addGrant}>绑定</Button>
          </Form.Item>
        </Form>
        <Table
          rowKey="id"
          size="small"
          columns={grantColumns}
          dataSource={grants}
          pagination={false}
          locale={{ emptyText: '未绑定任何用户（用户端不会看到该节点）' }}
        />
      </Modal>
    </PageCard>
  );
}
