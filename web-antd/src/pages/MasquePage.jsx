import React, { useEffect, useMemo, useState } from 'react';
import {
  App as AntdApp,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
} from 'antd';
import { CloudDownloadOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, apiDelete, apiPost, apiPut, getToken } from '../api';
import { PageCard } from '../components';

const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

function fmtTime(v) {
  const n = Number(v || 0);
  if (!n) return '—';
  try {
    return new Date(n).toLocaleString();
  } catch (e) {
    return '—';
  }
}

export default function MasquePage() {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [settings, setSettings] = useState(null);
  const [profiles, setProfiles] = useState([]);
  const [uuid, setUuid] = useState('');
  const [busyUuid, setBusyUuid] = useState('');

  const loadSettings = async () => {
    const res = await api('masque/warp/settings');
    const data = res && res.data ? res.data : {};
    setSettings(data);
    form.setFieldsValue({
      enabled: !!data.enabled,
      sni: data.sni || 'zt-masque.cloudflareclient.com',
      server_ips: data.server_ips || [],
      ports: data.ports || [],
      mtu: data.mtu || 1280,
      dns: data.dns || [],
    });
  };

  const loadProfiles = async () => {
    const res = await api('masque/warp/profiles');
    setProfiles(Array.isArray(res && res.data) ? res.data : []);
  };

  const reload = async () => {
    setLoading(true);
    try {
      await Promise.all([loadSettings(), loadProfiles()]);
    } catch (e) {
      message.error(e.message || '加载 MASQUE 失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = async () => {
    try {
      const v = await form.validateFields();
      setSaving(true);
      await apiPut('masque/warp/settings', {
        enabled: !!v.enabled,
        sni: String(v.sni || '').trim(),
        server_ips: (v.server_ips || []).map((x) => String(x).trim()).filter(Boolean),
        ports: (v.ports || []).map((x) => Number(x)).filter((x) => x > 0),
        mtu: Number(v.mtu || 1280),
        dns: (v.dns || []).map((x) => String(x).trim()).filter(Boolean),
      });
      message.success('已保存');
      await loadSettings();
    } catch (e) {
      if (e && e.errorFields) return;
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const authorize = async () => {
    const v = String(uuid || '').trim();
    if (!UUID_RE.test(v)) {
      message.error('请输入正确的 UUID');
      return;
    }
    setBusyUuid(v);
    try {
      const res = await apiPost('masque/warp/profiles', { uuid: v });
      message.success(res && res.reused ? '该 UUID 已有授权' : '已授权并注册 WARP');
      setUuid('');
      await loadProfiles();
    } catch (e) {
      message.error(e.message || '授权失败');
    } finally {
      setBusyUuid('');
    }
  };

  const refresh = async (row) => {
    setBusyUuid(row.uuid);
    try {
      await apiPost(`masque/warp/profiles/${row.uuid}/refresh`, {});
      message.success('已重新注册 WARP');
      await loadProfiles();
    } catch (e) {
      message.error(e.message || '刷新失败');
    } finally {
      setBusyUuid('');
    }
  };

  const revoke = async (row) => {
    setBusyUuid(row.uuid);
    try {
      const res = await apiDelete(`masque/warp/profiles/${row.uuid}`);
      if (res && res.warning) message.warning(res.warning);
      else message.success('已撤销');
      await loadProfiles();
    } catch (e) {
      message.error(e.message || '撤销失败');
    } finally {
      setBusyUuid('');
    }
  };

  const download = async (row) => {
    setBusyUuid(row.uuid);
    try {
      const res = await fetch(`/api/masque/warp/profiles/${row.uuid}/config`, {
        headers: { Authorization: 'Bearer ' + getToken() },
        credentials: 'same-origin',
      });
      if (!res.ok) {
        const text = await res.text();
        let msg = text;
        try {
          msg = JSON.parse(text).error || text;
        } catch (e) {
          /* keep */
        }
        throw new Error(msg || `下载失败（HTTP ${res.status}）`);
      }
      const text = await res.text();
      const blob = new Blob([text], { type: 'text/yaml;charset=utf-8' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = `masque-${row.uuid}.yaml`;
      document.body.appendChild(a);
      a.click();
      setTimeout(() => {
        URL.revokeObjectURL(a.href);
        a.remove();
      }, 1000);
    } catch (e) {
      message.error(e.message || '下载失败');
    } finally {
      setBusyUuid('');
    }
  };

  const columns = useMemo(
    () => [
      {
        title: 'UUID',
        dataIndex: 'uuid',
        width: 300,
        render: (v, row) => (
          <div>
            <div style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</div>
            <div style={{ color: '#94a3b8', fontSize: 12 }}>{row.email || row.owner || '—'}</div>
          </div>
        ),
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 100,
        render: (v) => (
          <Tag color={v === 'active' ? 'green' : v === 'revoked' ? 'default' : 'red'}>
            {v === 'active' ? '已授权' : v === 'revoked' ? '已撤销' : '异常'}
          </Tag>
        ),
      },
      {
        title: 'WARP 地址',
        width: 220,
        render: (_, row) => (
          <div style={{ fontFamily: 'monospace', fontSize: 12 }}>
            <div>{row.warp_ipv4 || '—'}</div>
            <div style={{ color: '#94a3b8' }}>{row.warp_ipv6 || ''}</div>
          </div>
        ),
      },
      { title: '授权时间', dataIndex: 'created_at', width: 170, render: fmtTime },
      { title: '授权人', dataIndex: 'authorized_by', width: 100, render: (v) => v || '—' },
      {
        title: '操作',
        width: 300,
        render: (_, row) => (
          <Space size={6} wrap>
            <Button
              size="small"
              icon={<CloudDownloadOutlined />}
              disabled={row.status !== 'active' || busyUuid === row.uuid}
              onClick={() => download(row)}
            >
              下载 YAML
            </Button>
            <Button
              size="small"
              icon={<ReloadOutlined />}
              loading={busyUuid === row.uuid}
              disabled={row.status !== 'active'}
              onClick={() => refresh(row)}
            >
              刷新
            </Button>
            <Popconfirm title="撤销后旧配置将失效，确认？" onConfirm={() => revoke(row)}>
              <Button size="small" danger disabled={row.status !== 'active' || busyUuid === row.uuid}>
                撤销
              </Button>
            </Popconfirm>
          </Space>
        ),
      },
    ],
    [busyUuid]
  );

  const settingsTab = (
    <Form form={form} layout="vertical" style={{ maxWidth: 760 }}>
      <Form.Item name="enabled" label="启用 MASQUE (WARP)" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Form.Item name="sni" label="MASQUE SNI" extra="客户端 TLS ServerName，默认 zt-masque.cloudflareclient.com">
        <Input placeholder="zt-masque.cloudflareclient.com" />
      </Form.Item>
      <Form.Item name="server_ips" label="接入地址（IP）" extra="Cloudflare MASQUE Anycast，可填多个用于客户端测速切换">
        <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="162.159.198.2" />
      </Form.Item>
      <Form.Item name="ports" label="端口" extra="默认 443 / 500 / 1701 / 4500 / 4443 / 8443 / 8095">
        <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="443" />
      </Form.Item>
      <Form.Item name="dns" label="隧道内 DNS">
        <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="1.1.1.1" />
      </Form.Item>
      <Form.Item name="mtu" label="MTU">
        <InputNumber min={1000} max={1500} style={{ width: 160 }} />
      </Form.Item>
      <Button type="primary" loading={saving} onClick={save}>
        保存设置
      </Button>
    </Form>
  );

  const grantsTab = (
    <div>
      <Space style={{ marginBottom: 14 }} wrap>
        <Input
          style={{ width: 420 }}
          value={uuid}
          onChange={(e) => setUuid(e.target.value)}
          placeholder="输入要授权的 UUID"
          allowClear
        />
        <Button type="primary" icon={<PlusOutlined />} loading={!!busyUuid} onClick={authorize}>
          授权并注册
        </Button>
        <Button icon={<ReloadOutlined />} onClick={reload} loading={loading}>
          刷新列表
        </Button>
      </Space>
      <Table
        rowKey="uuid"
        size="small"
        loading={loading}
        columns={columns}
        dataSource={profiles}
        pagination={{ pageSize: 20, showSizeChanger: false }}
      />
    </div>
  );

  return (
    <PageCard title="MASQUE" subtitle="Cloudflare WARP · 按 UUID 授权，节点无需部署">
      <Card styles={{ body: { paddingTop: 8 } }}>
        <Tabs
          items={[
            { key: 'grants', label: '用户授权', children: grantsTab },
            { key: 'settings', label: 'WARP 设置', children: settingsTab },
          ]}
        />
      </Card>
    </PageCard>
  );
}
