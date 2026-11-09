import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Col,
  Empty,
  Modal,
  Input,
  InputNumber,
  Popconfirm,
  Progress,
  Row,
  Space,
  Select,
  Switch,
  Table,
  Tag,
  Tooltip,
  Upload,
  App as AntdApp,
} from 'antd';
import {
  CloudDownloadOutlined,
  CloudServerOutlined,
  CodeOutlined,
  DeleteOutlined,
  EditOutlined,
  FolderOpenOutlined,
  InboxOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { api, apiPost, apiDelete } from '../api';
import { useAsync, usePolling } from '../hooks';
import { ErrorBlock, PageCard, StatCard } from '../components';
import RemoteShell from '../components/RemoteShell';
import { fmtBytes, fmtNum, fmtUptime, levelColor } from '../format';

const { Dragger } = Upload;

// 直接 fetch：api() 会把 body JSON 序列化，无法传 FormData
async function uploadFile(formData) {
  const token = (typeof localStorage !== 'undefined' && localStorage.getItem('token')) || '';
  const res = await fetch('/api/local/upload', {
    method: 'POST',
    headers: token ? { Authorization: 'Bearer ' + token } : {},
    body: formData,
    credentials: 'same-origin',
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok || data.success === false) throw new Error(data.error || '上传失败');
  return data;
}

/** 本机（控制服务器）管理：状态、SSH 安全防护、cloudflared、端口、进程、上传、终端 */
export default function LocalServer() {
  const { message } = AntdApp.useApp();
  const [overview, setOverview] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState('');
  const [termOpen, setTermOpen] = useState(false);

  const load = useCallback(async () => {
    try {
      const res = await api('local/overview');
      setOverview((res && res.data) || null);
      setError(null);
    } catch (e) {
      setError(e);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);
  usePolling(() => load().catch(() => {}), 5000);

  const procs = useAsync(() => api('local/processes'), []);
  const ports = useAsync(() => api('local/ports'), []);
  const files = useAsync(() => api('local/files'), []);
  const firewall = useAsync(() => api('local/firewall'), []);

  const m = (overview && overview.metrics) || {};
  const services = m.services || {};
  const versions = m.versions || {};
  const guardOn = !!(overview && Number(overview.ssh_guard) === 1);
  const cfState = String(services.cloudflared || 'not-installed');
  const warpState = String(services['warp-svc'] || 'not-installed');
  const warpVersion = String(versions['warp-svc'] || versions['warp'] || '');
  const cfVersion = String(versions.cloudflared || '');

  const toggleGuard = async () => {
    if (!overview) return;
    const enable = !guardOn;
    setBusy('guard');
    try {
      const res = await apiPost(`servers/${overview.id}/ssh-guard`, { enabled: enable });
      message.success((res && res.message) || `已下发${enable ? '开启' : '关闭'} SSH 安全防护`);
      setTimeout(() => load().catch(() => {}), 1500);
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  const updateWarp = async () => {
    if (!overview) return;
    setBusy('warp');
    try {
      const res = await apiPost('jobs', { kind: 'warp.update', params: { server_id: overview.id } });
      message.success((res && res.message) || '已下发 WARP 更新，进度见右上角「任务」');
      setTimeout(() => load().catch(() => {}), 3000);
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  const updateCloudflared = async () => {
    if (!overview) return;
    setBusy('cf');
    try {
      const res = await apiPost('jobs', { kind: 'cloudflared.update', params: { server_ids: [overview.id] } });
      message.success((res && res.message) || '已创建更新任务，进度见右上角「任务」');
      setTimeout(() => load().catch(() => {}), 2000);
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  const [newPort, setNewPort] = useState(null);
  const [newProto, setNewProto] = useState('tcp');
  const [newSrc, setNewSrc] = useState('');
  const [editRule, setEditRule] = useState(null);
  const fwRows = useMemo(
    () => ((firewall.data && firewall.data.data) || []).map((x) => ({ key: String(x.id), ...x })),
    [firewall.data]
  );

  const addRule = async (action) => {
    const spec = String(newPort || '').trim();
    if (!/^\d{1,5}(-\d{1,5})?$/.test(spec)) {
      message.warning('端口格式：8080 或 8000-8100');
      return;
    }
    setBusy('fw');
    try {
      const res = await apiPost('local/firewall', {
        port_spec: spec,
        protocol: newProto,
        action,
        src_cidr: String(newSrc || '').trim(),
      });
      message.success((res && res.message) || '已提交');
      setNewPort(null);
      setNewSrc('');
      firewall.reload();
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  const saveEdit = async (values) => {
    if (!editRule) return;
    setBusy('fw');
    try {
      const res = await apiPut('local/firewall/' + editRule.id, values);
      message.success((res && res.message) || '规则已更新');
      setEditRule(null);
      firewall.reload();
    } catch (e) {
      message.error(e.message || '更新失败');
    } finally {
      setBusy('');
    }
  };

  const syncPorts = async () => {
    setBusy('fwsync');
    try {
      const res = await apiPost('local/firewall/sync', {});
      message.success((res && res.message) || '已同步监听端口');
      firewall.reload();
    } catch (e) {
      message.error(e.message || '同步失败');
    } finally {
      setBusy('');
    }
  };

  const removeRule = async (id) => {
    try {
      await apiDelete('local/firewall/' + id);
      message.success('已删除规则');
      firewall.reload();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const removeFile = async (name) => {
    try {
      await apiDelete(`local/files/${encodeURIComponent(name)}`);
      message.success(`已删除 ${name}`);
      files.reload();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const procRows = useMemo(
    () => ((procs.data && procs.data.data) || []).map((p, i) => ({ key: `${p.pid}-${i}`, ...p })),
    [procs.data]
  );
  const portRows = useMemo(
    () => ((ports.data && ports.data.data) || []).map((p) => ({ key: `${p.bind}:${p.port}`, ...p })),
    [ports.data]
  );
  const fileRows = useMemo(
    () => ((files.data && files.data.data) || []).map((f) => ({ key: f.name, ...f })),
    [files.data]
  );

  if (error) {
    return <ErrorBlock error={error} onRetry={load} title="读取本机信息失败" />;
  }

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={12} lg={4}>
          <StatCard icon={<ThunderboltOutlined />} label="CPU" value={`${Math.round(Number(m.cpuPercent) || 0)}%`} accent="#ef4444" />
        </Col>
        <Col xs={12} lg={4}>
          <StatCard icon={<CloudServerOutlined />} label="内存" value={`${Math.round(Number(m.memPercent) || 0)}%`} accent="#10b981" />
        </Col>
        <Col xs={12} lg={4}>
          <StatCard icon={<CloudServerOutlined />} label="磁盘" value={`${Math.round(Number(m.diskPercent) || 0)}%`} accent="#0ea5e9" />
        </Col>
        <Col xs={12} lg={4}>
          <StatCard icon={<Badge status="processing" />} label="负载 1m" value={fmtNum(Number((m.load && m.load.one) ?? m.load1) || 0)} accent="#8b5cf6" />
        </Col>
        <Col xs={12} lg={4}>
          <StatCard icon={<CodeOutlined />} label="进程数" value={fmtNum(Number(m.procs ?? (m.proc && m.proc.total)) || 0)} accent="#f59e0b" />
        </Col>
        <Col xs={12} lg={4}>
          <StatCard icon={<CloudDownloadOutlined />} label="运行时长" value={fmtUptime(m.uptimeSec)} accent="#06b6d4" />
        </Col>
      </Row>

      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">安全与隧道</span>}
          extra={
            <Space>
              <Button icon={<ReloadOutlined />} onClick={() => load()} loading={busy === 'reload'}>
                刷新
              </Button>
              <Button type="primary" icon={<CodeOutlined />} onClick={() => setTermOpen(true)}>
                远程终端
              </Button>
            </Space>
          }
        >
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <Alert
              type={guardOn ? 'success' : 'warning'}
              showIcon
              message={guardOn ? 'SSH 安全防护已开启' : 'SSH 安全防护未开启'}
              description={
                guardOn
                  ? '仅允许 100.96.0.0/12（WARP/组网管理网）与 10.99.0.0/16（自建组网）访问 SSH，其他来源将被丢弃。'
                  : '当前所有 IP 均可尝试 SSH 登录；建议开启后仅允许组网网段访问。'
              }
              action={
                <Popconfirm
                  title={guardOn ? '关闭 SSH 安全防护？' : '开启 SSH 安全防护？'}
                  okText={guardOn ? '关闭' : '开启'}
                  okButtonProps={guardOn ? { danger: true } : undefined}
                  onConfirm={toggleGuard}
                >
                  <Switch checked={guardOn} loading={busy === 'guard'} />
                </Popconfirm>
              }
            />
            <Row gutter={[12, 12]}>
              <Col xs={24} md={12}>
                <div className="pp-svc-row" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', border: '1px solid #eef2f7', borderRadius: 10, background: '#fbfcfe' }}>
                  <i className={`pp-dot ${cfState === 'active' ? 'online' : cfState === 'not-installed' ? 'unknown' : 'warn'}`} />
                  <span style={{ fontWeight: 500 }}>cloudflared</span>
                  {cfVersion ? <span className="pp-mono pp-muted" style={{ fontSize: 12 }}>v{cfVersion}</span> : null}
                  <Tag color={cfState === 'active' ? 'success' : cfState === 'not-installed' ? 'default' : 'warning'} style={{ marginInlineEnd: 0 }}>
                    {cfState === 'active' ? '运行' : cfState === 'not-installed' ? '未安装' : cfState}
                  </Tag>
                  <span style={{ marginLeft: 'auto' }}>
                    <Popconfirm
                      title="更新 cloudflared？"
                      description="会下载最新版本并重启隧道服务。"
                      okText="更新"
                      onConfirm={updateCloudflared}
                    >
                      <Button size="small" type="link" loading={busy === 'cf'} disabled={cfState === 'not-installed'}>
                        更新
                      </Button>
                    </Popconfirm>
                  </span>
                </div>
              </Col>
              <Col xs={24} md={12}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', border: '1px solid #eef2f7', borderRadius: 10, background: '#fbfcfe' }}>
                  <i className={`pp-dot ${warpState === 'active' ? 'online' : warpState === 'not-installed' ? 'unknown' : 'warn'}`} />
                  <span style={{ fontWeight: 500 }}>WARP 客户端</span>
                  {warpVersion ? <span className="pp-mono pp-muted" style={{ fontSize: 12 }}>v{warpVersion}</span> : null}
                  <Tag color={warpState === 'active' ? 'success' : warpState === 'not-installed' ? 'default' : 'warning'} style={{ marginInlineEnd: 0 }}>
                    {warpState === 'active' ? '运行' : warpState === 'not-installed' ? '未安装' : warpState}
                  </Tag>
                  <span style={{ marginLeft: 'auto' }}>
                    <Popconfirm
                      title="更新 WARP 客户端？"
                      description="通过包管理器升级 cloudflare-warp 并重启 warp-svc；组网连接会短暂中断。"
                      okText="更新"
                      onConfirm={updateWarp}
                    >
                      <Button size="small" type="link" loading={busy === 'warp'} disabled={warpState === 'not-installed'}>
                        更新
                      </Button>
                    </Popconfirm>
                  </span>
                </div>
              </Col>
              <Col xs={24} md={12}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', border: '1px solid #eef2f7', borderRadius: 10, background: '#fbfcfe' }}>
                  <i className="pp-dot online" />
                  <span style={{ fontWeight: 500 }}>面板服务</span>
                  <Tag color="success" style={{ marginInlineEnd: 0 }}>运行中</Tag>
                  <span className="pp-muted" style={{ marginLeft: 'auto', fontSize: 12 }}>
                    {overview ? `#${overview.id} · ${overview.host || '127.0.0.1'}` : '—'}
                  </span>
                </div>
              </Col>
            </Row>
          </Space>
        </PageCard>
      </div>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24}>
          <PageCard
            title={<span className="pp-section-title">端口开放 / 关闭</span>}
            bodyStyle={{ padding: 0 }}
          >
            {/* 单独一排的操作区，避免挤在标题栏里换行 */}
            <div
              className="pp-fw-toolbar"
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'center',
                gap: 8,
                padding: '10px 12px',
                borderBottom: '1px solid #f1f5f9',
                background: '#fbfcfe',
              }}
            >
              <Input
                placeholder="目的端口：8080 或 8000-8100"
                style={{ width: 210 }}
                value={newPort}
                onChange={(e) => setNewPort(e.target.value)}
                onPressEnter={() => addRule('internal')}
              />
              <Select
                value={newProto}
                onChange={setNewProto}
                style={{ width: 88 }}
                options={[
                  { value: 'tcp', label: 'TCP' },
                  { value: 'udp', label: 'UDP' },
                ]}
              />
              <Input
                placeholder="来源 IP/CIDR（留空 = 任意）"
                style={{ width: 220 }}
                value={newSrc}
                onChange={(e) => setNewSrc(e.target.value)}
                onPressEnter={() => addRule('internal')}
              />
              <span style={{ width: 1, height: 22, background: '#e2e8f0', margin: '0 2px' }} />
              <Button type="primary" icon={<PlusOutlined />} loading={busy === 'fw'} onClick={() => addRule('open')}>
                对外开放
              </Button>
              <Button loading={busy === 'fw'} onClick={() => addRule('internal')}>
                仅内网
              </Button>
              <Button loading={busy === 'fw'} onClick={() => addRule('loopback')}>
                仅本机
              </Button>
              <Button danger loading={busy === 'fw'} onClick={() => addRule('close')}>
                关闭
              </Button>
              <Button icon={<ReloadOutlined />} loading={busy === 'fwsync'} onClick={syncPorts}>
                同步监听
              </Button>
            </div>
            <Table
              size="small"
              pagination={false}
              scroll={{ y: 300 }}
              loading={firewall.loading}
              dataSource={fwRows}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有端口规则" /> }}
              columns={[
                {
                  title: '目的端口',
                  dataIndex: 'port',
                  width: 118,
                  render: (v, r) => (
                    <span className="pp-mono">
                      {r.port_end > r.port ? v + '-' + r.port_end : v}/{String(r.protocol).toUpperCase()}
                    </span>
                  ),
                },
                {
                  title: '来源',
                  dataIndex: 'src_cidr',
                  width: 168,
                  render: (v) => (v ? <span className="pp-mono" style={{ fontSize: 12 }}>{v}</span> : <span className="pp-muted">任意</span>),
                },
                {
                  title: '对外',
                  dataIndex: 'action',
                  width: 96,
                  render: (v) => {
                    if (v === 'close') return <Tag color="red" style={{ marginInlineEnd: 0 }}>已关闭</Tag>;
                    if (v === 'loopback') return <Tag color="purple" style={{ marginInlineEnd: 0 }}>仅本机</Tag>;
                    if (v === 'internal') return <Tag color="orange" style={{ marginInlineEnd: 0 }}>仅内网</Tag>;
                    return <Tag color="green" style={{ marginInlineEnd: 0 }}>公网开放</Tag>;
                  },
                },
                {
                  title: '监听',
                  dataIndex: 'listening',
                  width: 72,
                  render: (v) =>
                    v ? (
                      <Tag color="blue" style={{ marginInlineEnd: 0 }}>监听中</Tag>
                    ) : (
                      <span className="pp-muted" style={{ fontSize: 12 }}>未监听</span>
                    ),
                },
                { title: '备注', dataIndex: 'note', ellipsis: true, render: (v) => v || <span className="pp-muted">—</span> },
                {
                  title: '',
                  width: 92,
                  render: (_, r) => (
                    <Space size={2}>
                      <Button size="small" type="link" icon={<EditOutlined />} onClick={() => setEditRule(r)} />
                      <Popconfirm title={`删除 ${r.port}/${r.protocol} 规则？`} okText="删除" okButtonProps={{ danger: true }} onConfirm={() => removeRule(r.id)}>
                        <Button size="small" type="link" danger icon={<DeleteOutlined />} />
                      </Popconfirm>
                    </Space>
                  ),
                },
              ]}
            />
            <div className="pp-muted" style={{ fontSize: 11.5, padding: '8px 12px', borderTop: '1px solid #f1f5f9' }}>
              「同步监听」把 ss 探测到的端口导入，默认「仅内网」（组网 100.96.0.0/12 + 回环可访问）；「对外开放」才允许公网直连；「关闭」显式 DROP。规则落在独立链 PP_PORTS，开机自动恢复。
            </div>
          </PageCard>
        </Col>
        <Col xs={24}>
          <PageCard
            title={<span className="pp-section-title">运行程序（Top 20）</span>}
            extra={<Button size="small" icon={<ReloadOutlined />} onClick={() => procs.reload()} />}
            bodyStyle={{ padding: 0 }}
          >
            <Table
              size="small"
              pagination={false}
              scroll={{ y: 260 }}
              loading={procs.loading}
              dataSource={procRows}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未读取到进程" /> }}
              columns={[
                { title: 'PID', dataIndex: 'pid', width: 80, render: (v) => <span className="pp-mono">{v}</span> },
                { title: '进程', dataIndex: 'name', ellipsis: true },
                {
                  title: 'CPU',
                  dataIndex: 'cpu',
                  width: 74,
                  render: (v) => <span className="pp-mono" style={{ color: levelColor(v) }}>{Number(v).toFixed(1)}%</span>,
                },
                { title: '内存', dataIndex: 'mem', width: 74, render: (v) => <span className="pp-mono">{Number(v).toFixed(1)}%</span> },
                { title: 'RSS', dataIndex: 'rss_mb', width: 86, render: (v) => <span className="pp-mono">{fmtBytes(v * 1024 * 1024)}</span> },
              ]}
            />
          </PageCard>
        </Col>
      </Row>

      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">文件上传</span>}
          extra={<span className="pp-muted" style={{ fontSize: 12 }}>保存到 /opt/proxypilot/uploads</span>}
        >
          <Dragger
            multiple
            showUploadList={false}
            customRequest={async ({ file, onSuccess, onError }) => {
              try {
                const fd = new FormData();
                fd.append('file', file);
                const res = await uploadFile(fd);
                message.success((res && res.message) || `已上传 ${file.name}`);
                onSuccess && onSuccess(res);
                files.reload();
              } catch (e) {
                message.error(e.message || '上传失败');
                onError && onError(e);
              }
            }}
          >
            <p className="ant-upload-drag-icon">
              <InboxOutlined />
            </p>
            <p className="ant-upload-text">把文件拖到这里，或点击选择</p>
            <p className="ant-upload-hint">单文件最大 256 MB</p>
          </Dragger>
          <Table
            style={{ marginTop: 12 }}
            size="small"
            pagination={false}
            loading={files.loading}
            dataSource={fileRows}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有上传文件" /> }}
            columns={[
              {
                title: '文件名',
                dataIndex: 'name',
                render: (v) => (
                  <span className="pp-mono">
                    <FolderOpenOutlined style={{ marginInlineEnd: 6 }} />
                    {v}
                  </span>
                ),
              },
              { title: '大小', dataIndex: 'size', width: 110, render: (v) => <span className="pp-mono">{fmtBytes(v)}</span> },
              {
                title: '上传时间',
                dataIndex: 'mod',
                width: 170,
                render: (v) => <span className="pp-muted">{new Date(Number(v) || 0).toLocaleString()}</span>,
              },
              {
                title: '操作',
                width: 80,
                render: (_, row) => (
                  <Popconfirm title={`删除 ${row.name}？`} okText="删除" okButtonProps={{ danger: true }} onConfirm={() => removeFile(row.name)}>
                    <Button size="small" type="link" danger icon={<DeleteOutlined />} />
                  </Popconfirm>
                ),
              },
            ]}
          />
        </PageCard>
      </div>

      <Modal
        open={!!editRule}
        title="编辑端口规则"
        onCancel={() => setEditRule(null)}
        onOk={() => {
          const form = document.getElementById('pp-rule-edit');
          if (!form) return;
          const val = (n) => (form.querySelector('[name="' + n + '"]') || {}).value || '';
          saveEdit({
            port_spec: val('port_spec'),
            protocol: val('protocol'),
            action: val('action'),
            src_cidr: val('src_cidr'),
            note: val('note'),
          });
        }}
        okText="保存"
        confirmLoading={busy === 'fw'}
        destroyOnClose
      >
        <div id="pp-rule-edit">
          <Space direction="vertical" size={10} style={{ width: '100%' }}>
            <Input name="port_spec" defaultValue={editRule ? (editRule.port_end > editRule.port ? editRule.port + '-' + editRule.port_end : String(editRule.port)) : ''} addonBefore="目的端口" />
            <Select name="protocol" defaultValue={(editRule && editRule.protocol) || 'tcp'} style={{ width: '100%' }}
              options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} />
            <Select name="action" defaultValue={(editRule && editRule.action) || 'internal'} style={{ width: '100%' }}
              options={[
                { value: 'open', label: '对外开放（任意来源）' },
                { value: 'internal', label: '仅内网（组网 100.96.0.0/12 + 本机）' },
                { value: 'loopback', label: '仅本机（127.0.0.1）' },
                { value: 'close', label: '关闭（DROP）' },
              ]} />
            <Input name="src_cidr" defaultValue={(editRule && editRule.src_cidr) || ''} placeholder="来源 CIDR（留空=任意）" addonBefore="来源" />
            <Input name="note" defaultValue={(editRule && editRule.note) || ''} placeholder="备注" addonBefore="备注" />
          </Space>
        </div>
      </Modal>

      {overview ? (
        <RemoteShell
          server={{ id: overview.id, name: overview.name || '本机（控制服务器）', status: 'online', last_status: 'online' }}
          open={termOpen}
          onClose={() => setTermOpen(false)}
        />
      ) : null}
    </div>
  );
}
