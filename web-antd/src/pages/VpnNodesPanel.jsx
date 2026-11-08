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

// VPN 节点绑定：只负责节点本身；用户授权在「用户管理 → 授权服务」中完成。
export default function VpnNodesPanel() {
  const { message } = AntdApp.useApp();
  const [nodeForm] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [nodes, setNodes] = useState([]);
  const [serverNames, setServerNames] = useState({});
  const [nodeOpen, setNodeOpen] = useState(false);
  const [editingNode, setEditingNode] = useState(null);

  const load = async () => {
    setLoading(true);
    try {
      const [res, bindings] = await Promise.all([
        api('admin/vpn/nodes'),
        api('nodes/bindings').catch(() => null),
      ]);
      setNodes((res && res.nodes) || []);
      const map = {};
      for (const row of (bindings && bindings.data) || []) {
        if (row.server_id) map[row.server_id] = row.server_name || '';
      }
      setServerNames(map);
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

  const removeNode = async (node) => {
    try {
      await apiDelete(`admin/vpn/nodes/${node.id}`);
      message.success('节点已删除');
      load();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '名称', dataIndex: 'name', width: 150 },
    {
      title: '服务器', dataIndex: 'server_id', width: 160,
      render: (v) => serverNames[v] || `#${v}`,
    },
    { title: '域名', dataIndex: 'domain', width: 220, render: (v) => v || '—' },
    { title: '接入地址', dataIndex: 'endpoint', width: 210, render: (v) => v || '—' },
    { title: '客户端子网', dataIndex: 'client_cidr4', width: 130, render: (v) => v || '—' },
    { title: '状态', dataIndex: 'state', width: 90, render: stateTag },
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
          <Button size="small" type="link" onClick={() => openNode(node)}>编辑</Button>
          <Popconfirm title="删除该 VPN 节点？" onConfirm={() => removeNode(node)}>
            <Button size="small" type="link" danger>删除</Button>
          </Popconfirm>
        </Space>
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
    </PageCard>
  );
}
