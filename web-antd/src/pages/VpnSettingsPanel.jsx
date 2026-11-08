import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Form, Input, InputNumber, Space, Switch, Tag } from 'antd';
import { SaveOutlined } from '@ant-design/icons';
import { api, apiPut } from '../api';
import { PageCard } from '../components';

// VPN 协议配置：放在「协议管理」中，只保留与协议相关的参数。
// 节点同步通道在「系统设置」中维护。
export default function VpnSettingsPanel() {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [settings, setSettings] = useState(null);

  const load = async () => {
    setLoading(true);
    try {
      const res = await api('admin/vpn/settings');
      const d = res && res.data ? res.data : {};
      setSettings(d);
      form.setFieldsValue({
        enabled: !!d.enabled,
        mtu: d.mtu || 1280,
        token_ttl_seconds: d.token_ttl_seconds || 600,
        client_cidr4: d.client_cidr4 || '10.88.0.0/16',
      });
    } catch (e) {
      message.error(e.message || '加载 VPN 配置失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = async () => {
    const values = await form.validateFields();
    try {
      const res = await apiPut('admin/vpn/settings', { ...(settings || {}), ...values });
      setSettings(res && res.data ? res.data : { ...(settings || {}), ...values });
      message.success('VPN 协议配置已保存');
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };

  return (
    <PageCard
      title="VPN（MASQUE / HTTP3）"
      loading={loading}
      extra={<Tag color="blue">连接令牌 Ed25519 · 用户端与节点端隔离</Tag>}
    >
      <Form form={form} layout="vertical" style={{ maxWidth: 720 }}>
        <Form.Item name="enabled" label="启用 VPN" valuePropName="checked">
          <Switch checkedChildren="开" unCheckedChildren="关" />
        </Form.Item>
        <Space size={16} wrap>
          <Form.Item name="mtu" label="隧道 MTU">
            <InputNumber min={576} max={1500} />
          </Form.Item>
          <Form.Item name="token_ttl_seconds" label="连接令牌 TTL（秒）">
            <InputNumber min={60} max={86400} />
          </Form.Item>
        </Space>
        <Form.Item name="client_cidr4" label="用户端地址池" extra="每个节点从中分配 /24 子网">
          <Input placeholder="10.88.0.0/16" />
        </Form.Item>
        <Button type="primary" icon={<SaveOutlined />} onClick={save}>保存</Button>
      </Form>
    </PageCard>
  );
}

