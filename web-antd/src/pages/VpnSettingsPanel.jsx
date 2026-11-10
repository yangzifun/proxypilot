import React, { useEffect, useState } from 'react';
import { App as AntdApp, Alert, Button, Form, Input, InputNumber, Select, Space, Switch, Tag } from 'antd';
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
      const d = res && res.data ? res.data : (res || {});
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
        <Space size={16} wrap align="start">
          <Form.Item
            name="mtu"
            label="隧道 MTU"
            rules={[
              { required: true, message: '请填写隧道 MTU' },
              {
                type: 'number',
                min: 576,
                max: 1500,
                message: 'MTU 必须在 576 ~ 1500 之间',
              },
            ]}
            extra={
              <span style={{ fontSize: 12 }}>
                范围 <b>576 ~ 1500</b>，默认 <b>1380</b>。客户端直接采用此值。
                跨境链路建议 1280 ~ 1380；小于 1280 浪费带宽，大于 1500 无意义。
              </span>
            }
          >
            <InputNumber min={576} max={1500} step={4} style={{ width: 160 }} placeholder="1380" />
          </Form.Item>
          <Form.Item
            name="token_ttl_seconds"
            label="连接令牌 TTL（秒）"
            rules={[
              { required: true, message: '请填写令牌有效期' },
              {
                type: 'number',
                min: 60,
                max: 86400,
                message: 'TTL 必须在 60 ~ 86400 秒之间（1 分钟 ~ 24 小时）',
              },
            ]}
            extra={
              <span style={{ fontSize: 12 }}>
                范围 <b>60 ~ 86400</b> 秒，默认 <b>600</b>。
                票据只在建立隧道时使用，600 秒（10 分钟）足够；
                设得过长会放大泄露风险，过短可能在握手未完成时失效。
              </span>
            }
          >
            <InputNumber min={60} max={86400} step={60} style={{ width: 160 }} placeholder="600" />
          </Form.Item>
        </Space>

        <Form.Item
          name="datagram_enabled"
          label="UDP 走 DATAGRAM（双通道）"
          valuePropName="checked"
          extra={
            <span style={{ fontSize: 12 }}>
              开启后 UDP 流量（DNS、QUIC 网页、视频通话、游戏）走 <b>QUIC DATAGRAM</b>，
              TCP 仍走可靠流。大流量时小包不再被队头阻塞拖累。
              关闭即全部走可靠流（等同旧版行为），出问题时可用它一键回退。
            </span>
          }
        >
          <Switch checkedChildren="开" unCheckedChildren="关" />
        </Form.Item>

        <Space size={16} wrap align="start">
          <Form.Item
            name="fec_mode"
            label="DATAGRAM 前向纠错"
            rules={[{ required: true, message: '请选择纠错模式' }]}
            extra={
              <span style={{ fontSize: 12 }}>
                DATAGRAM 不重传，丢了就没了。<b>xor</b> 为分组异或校验：
                组内丢任意 1 个可无损还原，代价是 1/每组包数 的带宽。
                仅作用于 DATAGRAM 通道，不影响可靠流。
              </span>
            }
          >
            <Select style={{ width: 160 }}
              options={[
                { value: 'off', label: '关闭' },
                { value: 'xor', label: 'xor（分组校验）' },
              ]} />
          </Form.Item>
          <Form.Item
            name="fec_group"
            label="每组包数"
            rules={[
              { required: true, message: '请填写每组包数' },
              { type: 'number', min: 2, max: 32, message: '每组包数必须在 2 ~ 32 之间' },
            ]}
            extra={
              <span style={{ fontSize: 12 }}>
                范围 <b>2 ~ 32</b>，默认 <b>8</b>。越小越抗丢包、开销越大
                （K=4 开销 25%，K=8 开销 12.5%）。仅在纠错开启时生效。
              </span>
            }
          >
            <InputNumber min={2} max={32} style={{ width: 160 }} placeholder="8" />
          </Form.Item>
        </Space>

        <Form.Item
          name="client_cidr4"
          label="用户端地址池"
          rules={[
            { required: true, message: '请填写用户端地址池' },
            {
              pattern: /^\d{1,3}(\.\d{1,3}){3}\/(1[6-9]|2[0-9]|30)$/,
              message: '请填写形如 10.88.0.0/16 的网段，掩码需在 /16 ~ /30 之间',
            },
          ]}
          extra={
            <span style={{ fontSize: 12 }}>
              每个节点从中分配一个 <b>/24</b> 子网，因此至少要能容纳「节点数 × 256」个地址。
              默认 <b>10.88.0.0/16</b> 可支持 256 个节点；填 /26 或更小会导致节点分不到子网。
            </span>
          }
        >
          <Input placeholder="10.88.0.0/16" style={{ maxWidth: 320 }} />
        </Form.Item>

        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="改完 MTU / 地址池后需要重新下发节点才生效"
          description="这些值会写进各节点的 vpn.json（TUN 地址、MTU、子网）。保存后请到「节点与网络 → 协议管理 → VPN 节点」逐个点「部署下发」。连接令牌 TTL 无需下发，保存即生效。"
        />

        <Button type="primary" icon={<SaveOutlined />} onClick={save}>保存</Button>
      </Form>
    </PageCard>
  );
}

