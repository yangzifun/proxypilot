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
import { CloudDownloadOutlined, PlusOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { api, apiDelete, apiPost, apiPut, getToken } from '../api';
import { PageCard } from '../components';

const WARP_DEFAULTS = {
  enabled: false,
  sni: 'zt-masque.cloudflareclient.com',
  server_ips: [
    '162.159.198.1',
    '162.159.198.2',
    '162.159.199.1',
    '162.159.199.2',
    '2606:4700:103::1',
    '2606:4700:103::2',
    '2606:4700:104::1',
    '2606:4700:104::2',
  ],
  ports: [443, 500, 1701, 4500, 4443, 8443, 8095],
  dns: ['1.1.1.1', '2606:4700:4700::1111'],
  mtu: 1280,
};

function fmtTime(v) {
  const n = Number(v || 0);
  if (!n) return '—';
  try {
    return new Date(n).toLocaleString();
  } catch (e) {
    return '—';
  }
}

function statusOf(profile) {
  if (!profile) return 'none';
  return profile.status || 'none';
}

export default function MasquePage() {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [settings, setSettings] = useState(null);
  const [profiles, setProfiles] = useState([]);
  const [uuids, setUuids] = useState([]);
  const [q, setQ] = useState('');
  const [busyUuid, setBusyUuid] = useState('');

  const loadSettings = async () => {
    const res = await api('masque/warp/settings');
    const data = res && res.data ? res.data : {};
    setSettings(data);
    form.setFieldsValue({
      enabled: !!data.enabled,
      sni: data.sni || 'zt-masque.cloudflareclient.com',
      server_ips: data.server_ips || WARP_DEFAULTS.server_ips,
      ports: data.ports || WARP_DEFAULTS.ports,
      mtu: data.mtu || 1280,
      dns: data.dns || WARP_DEFAULTS.dns,
    });
  };

  const loadProfiles = async () => {
    const res = await api('masque/warp/profiles');
    setProfiles(Array.isArray(res && res.data) ? res.data : []);
  };

  const loadUuids = async () => {
    const res = await api('uuids', { query: { all: 1, size: 1000 } });
    setUuids(Array.isArray(res && res.data) ? res.data : []);
  };

  const reload = async () => {
    setLoading(true);
    try {
      await Promise.all([loadSettings(), loadProfiles(), loadUuids()]);
    } catch (e) {
      message.error(e.message || '加载 WARP 失败');
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

  const authorize = async (target) => {
    const v = String(target || '').trim();
    if (!v) return;
    setBusyUuid(v);
    try {
      const res = await apiPost('masque/warp/profiles', { uuid: v });
      message.success(res && res.reused ? '该 UUID 已有授权' : '已授权并注册 WARP');
      await Promise.all([loadProfiles(), loadUuids()]);
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
      a.download = `warp-${row.uuid}.yaml`;
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

  const profileMap = useMemo(() => {
    const m = {};
    for (const p of profiles) m[p.uuid] = p;
    return m;
  }, [profiles]);

  const rows = useMemo(() => {
    const kw = String(q || '').trim().toLowerCase();
    return (uuids || [])
      .map((u) => ({ ...u, profile: profileMap[u.uuid] || null }))
      .filter((u) => {
        if (!kw) return true;
        return [u.uuid, u.owner, u.email, u.remark].some((x) => String(x || '').toLowerCase().includes(kw));
      });
  }, [uuids, profileMap, q]);

  const authorizedCount = profiles.filter((p) => p.status === 'active').length;

  const columns = useMemo(
    () => [
      {
        title: 'UUID / 用户',
        dataIndex: 'uuid',
        width: 340,
        render: (v, row) => (
          <div>
            <div style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</div>
            <div style={{ color: '#94a3b8', fontSize: 12 }}>
              {row.email || row.owner || row.remark || '—'}
            </div>
          </div>
        ),
      },
      {
        title: 'WARP 状态',
        width: 110,
        render: (_, row) => {
          const s = statusOf(row.profile);
          return (
            <Tag color={s === 'active' ? 'green' : s === 'revoked' ? 'default' : s === 'error' ? 'red' : 'blue'}>
              {s === 'active' ? '已授权' : s === 'revoked' ? '已撤销' : s === 'error' ? '异常' : '未授权'}
            </Tag>
          );
        },
      },
      {
        title: 'WARP 地址',
        width: 230,
        render: (_, row) => (
          <div style={{ fontFamily: 'monospace', fontSize: 12 }}>
            <div>{row.profile?.warp_ipv4 || '—'}</div>
            <div style={{ color: '#94a3b8' }}>{row.profile?.warp_ipv6 || ''}</div>
          </div>
        ),
      },
      { title: '授权时间', width: 170, render: (_, row) => fmtTime(row.profile?.created_at) },
      { title: '授权人', width: 100, render: (_, row) => row.profile?.authorized_by || '—' },
      {
        title: '操作',
        width: 320,
        render: (_, row) => {
          const active = statusOf(row.profile) === 'active';
          return (
            <Space size={6} wrap>
              {!active ? (
                <Button
                  size="small"
                  type="primary"
                  icon={<PlusOutlined />}
                  loading={busyUuid === row.uuid}
                  onClick={() => authorize(row.uuid)}
                >
                  {row.profile ? '重新授权' : '授权'}
                </Button>
              ) : null}
              {active ? (
                <>
                  <Button
                    size="small"
                    icon={<CloudDownloadOutlined />}
                    disabled={busyUuid === row.uuid}
                    onClick={() => download(row)}
                  >
                    下载 YAML
                  </Button>
                  <Button
                    size="small"
                    icon={<ReloadOutlined />}
                    loading={busyUuid === row.uuid}
                    onClick={() => refresh(row)}
                  >
                    刷新
                  </Button>
                  <Popconfirm title="撤销后旧配置将失效，确认？" onConfirm={() => revoke(row)}>
                    <Button size="small" danger disabled={busyUuid === row.uuid}>
                      撤销
                    </Button>
                  </Popconfirm>
                </>
              ) : null}
            </Space>
          );
        },
      },
    ],
    [busyUuid]
  );

  const settingsTab = (
    <Form form={form} layout="vertical" style={{ maxWidth: 760 }} initialValues={WARP_DEFAULTS}>
      <Form.Item name="enabled" label="启用 WARP" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Form.Item name="sni" label="MASQUE SNI" extra="客户端 TLS ServerName，默认 zt-masque.cloudflareclient.com">
        <Input placeholder="zt-masque.cloudflareclient.com" />
      </Form.Item>
      <Form.Item name="server_ips" label="MASQUE 接入地址（IP）" extra="Cloudflare MASQUE Anycast，可填多个用于客户端测速切换">
        <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="162.159.198.2" />
      </Form.Item>
      <Form.Item name="ports" label="MASQUE 端口" extra="默认 443 / 500 / 1701 / 4500 / 4443 / 8443 / 8095">
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
          style={{ width: 360 }}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="搜索 UUID / 邮箱 / 所有者"
          prefix={<SearchOutlined style={{ color: '#94a3b8' }} />}
          allowClear
        />
        <span style={{ color: '#64748b', fontSize: 13 }}>
          共 {uuids.length} 个 UUID，已授权 {authorizedCount} 个
        </span>
        <Button icon={<ReloadOutlined />} onClick={reload} loading={loading}>
          刷新列表
        </Button>
      </Space>
      <Table
        rowKey="uuid"
        size="small"
        loading={loading}
        columns={columns}
        dataSource={rows}
        scroll={{ x: 1150 }}
        pagination={{ pageSize: 20, showSizeChanger: false }}
      />
    </div>
  );

  return (
    <PageCard title="WARP" subtitle="Cloudflare WARP · 按 UUID 授权，节点无需部署">
      <Card styles={{ body: { paddingTop: 8 } }}>
        <Tabs
          items={[
            { key: 'grants', label: '用户授权', children: grantsTab },
            { key: 'settings', label: 'WARP 设置', children: settingsTab, forceRender: true },
          ]}
        />
      </Card>
    </PageCard>
  );
}
