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
  SyncOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { api, apiPost, apiDelete } from '../api';
import { useAsync, usePolling } from '../hooks';
import { ErrorBlock, PageCard, StatCard } from '../components';
import RemoteShell from '../components/RemoteShell';
import { fmtBytes, fmtNum, fmtTime, fmtUptime, levelColor, versionStatus } from '../format';

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

/** 本机（控制服务器）管理：状态、cloudflared、端口、进程、上传、终端（不做 SSH 限制） */
export default function LocalServer() {
  const { message } = AntdApp.useApp();
  const [overview, setOverview] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState('');
  const [termOpen, setTermOpen] = useState(false);
  // 最新可用版本（后端缓存 30min；refresh=1 强制刷新）；失败不阻塞页面
  const [latest, setLatest] = useState({ loading: true, data: null, error: null });

  const load = useCallback(async () => {
    try {
      const res = await api('local/overview');
      setOverview((res && res.data) || null);
      setError(null);
    } catch (e) {
      setError(e);
    }
  }, []);

  /** 拉取 cloudflared / WARP 的最新版本；返回是否成功（失败时页面仍显示已装版本）。 */
  const loadLatest = useCallback(async (refresh = false) => {
    setLatest((s) => ({ ...s, loading: true }));
    try {
      const res = await api('versions/latest', refresh ? { query: { refresh: 1 } } : undefined);
      setLatest({ loading: false, data: (res && res.data) || {}, error: null });
      return true;
    } catch (e) {
      setLatest({ loading: false, data: null, error: e });
      return false;
    }
  }, []);

  useEffect(() => {
    load();
    loadLatest(false);
  }, [load, loadLatest]);
  usePolling(() => load().catch(() => {}), 5000);

  const procs = useAsync(() => api('local/processes'), []);
  const ports = useAsync(() => api('local/ports'), []);
  const files = useAsync(() => api('local/files'), []);
  const firewall = useAsync(() => api('local/firewall'), []);

  const m = (overview && overview.metrics) || {};
  const services = m.services || {};
  const versions = m.versions || {};
  const cfState = String(services.cloudflared || 'not-installed');
  const warpState = String(services['warp-svc'] || 'not-installed');
  const warpVersion = String(versions['warp-svc'] || versions['warp'] || '');
  const cfVersion = String(versions.cloudflared || '');

  // 最新版本：cloudflared / warp-svc 各有 latest/error；单项失败只影响对应行
  const latestData = latest.data || {};
  const latestCf = latestData.cloudflared || null;
  const latestWarp = latestData['warp-svc'] || null;
  const cfLatest = String((latestCf && latestCf.latest) || '');
  const warpLatest = String((latestWarp && latestWarp.latest) || '');
  const apiErrText = latest.error ? latest.error.message || String(latest.error) : '';
  const cfLatestErr = String((latestCf && latestCf.error) || apiErrText || '');
  const warpLatestErr = String((latestWarp && latestWarp.error) || apiErrText || '');
  const cfUpgrade = versionStatus(cfVersion, cfLatest);
  const warpUpgrade = versionStatus(warpVersion, warpLatest);

  // 本机服务更新（cloudflared / warp-svc）是异步任务：POST 启动，轮询 GET 进度；
  // 与节点 Agent 任务完全解耦，避免误触发全部节点。
  const pollLocalUpdate = useCallback(async (service) => {
    for (let i = 0; i < 150; i += 1) {
      await new Promise((r) => setTimeout(r, 2000));
      const r = await api(`local/services/${service}/update`);
      if (!r || !r.running) {
        const last = (r && r.last) || null;
        if (last && last.ok) message.success(last.message || `${service} 更新完成`);
        else if (last) message.error(last.message || `${service} 更新失败`);
        else message.warning(`${service} 更新状态未知，请刷新查看`);
        load().catch(() => {});
        setTimeout(() => load().catch(() => {}), 6000);
        return last;
      }
    }
    message.warning('更新仍在进行，请稍后刷新查看');
    return null;
  }, [message, load]);

  const updateCloudflared = async () => {
    if (!overview) return;
    setBusy('cf');
    try {
      const res = await apiPost('local/services/cloudflared/update', {});
      if (res && res.started === false) {
        message.info(res.message || '正在更新中，请稍候');
      } else {
        message.success('已开始更新本机 cloudflared（不会更新节点服务器）');
      }
      await pollLocalUpdate('cloudflared');
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  // WARP 客户端更新：走本机专用异步接口（与节点 Agent 任务解耦），
  // 绝不再调 /api/jobs，避免误更新全部节点。
  const updateWarp = async () => {
    if (!overview) return;
    setBusy('warp');
    try {
      const res = await apiPost('local/services/warp-svc/update', {});
      if (res && res.started === false) {
        message.info(res.message || '正在更新中，请稍候');
      } else {
        message.success('已开始更新本机 WARP 客户端（仅本机，不会更新节点服务器）');
      }
      await pollLocalUpdate('warp-svc');
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
              <Button
                icon={<SyncOutlined />}
                loading={latest.loading}
                onClick={async () => {
                  const ok = await loadLatest(true);
                  if (ok) message.success('已检查并刷新最新版本信息');
                }}
              >
                检查更新
              </Button>
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
              type="info"
              showIcon
              message="本机（控制服务器）不做 SSH 来源限制"
              description="控制服务器是运维入口，SSH 防护只作用于节点服务器；本机保持所有来源可登录，避免误操作锁死面板。"
            />
            <Row gutter={[12, 12]}>
              <Col xs={24} md={12}>
                <div className="pp-svc-row" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', border: '1px solid #eef2f7', borderRadius: 10, background: '#fbfcfe', flexWrap: 'wrap' }}>
                  <i className={`pp-dot ${cfState === 'active' ? 'online' : cfState === 'not-installed' ? 'unknown' : 'warn'}`} />
                  <span style={{ fontWeight: 500 }}>cloudflared</span>
                  <VersionPair
                    installed={cfVersion}
                    latest={cfLatest}
                    error={cfLatestErr}
                    loading={latest.loading}
                    source={latestCf && latestCf.source}
                    checkedAt={latestCf && latestCf.checked_at}
                    onRetry={() => loadLatest(true)}
                  />
                  <Tag color={cfState === 'active' ? 'success' : cfState === 'not-installed' ? 'default' : 'warning'} style={{ marginInlineEnd: 0 }}>
                    {cfState === 'active' ? '运行' : cfState === 'not-installed' ? '未安装' : cfState}
                  </Tag>
                  <VersionTag status={cfUpgrade} installed={cfVersion} latest={cfLatest} />
                  <span style={{ marginLeft: 'auto' }}>
                    <Popconfirm
                      title="更新本机 cloudflared？"
                      description="仅下载最新版本并重启本机（控制服务器）的隧道服务，不会更新节点服务器。"
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
                <div className="pp-svc-row" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', border: '1px solid #eef2f7', borderRadius: 10, background: '#fbfcfe', flexWrap: 'wrap' }}>
                  <i className={`pp-dot ${warpState === 'active' ? 'online' : warpState === 'not-installed' ? 'unknown' : 'warn'}`} />
                  <span style={{ fontWeight: 500 }}>WARP 客户端</span>
                  <VersionPair
                    installed={warpVersion}
                    latest={warpLatest}
                    error={warpLatestErr}
                    loading={latest.loading}
                    source={latestWarp && latestWarp.source}
                    checkedAt={latestWarp && latestWarp.checked_at}
                    onRetry={() => loadLatest(true)}
                  />
                  <Tag color={warpState === 'active' ? 'success' : warpState === 'not-installed' ? 'default' : 'warning'} style={{ marginInlineEnd: 0 }}>
                    {warpState === 'active' ? '运行' : warpState === 'not-installed' ? '未安装' : warpState}
                  </Tag>
                  <VersionTag status={warpUpgrade} installed={warpVersion} latest={warpLatest} />
                  <span style={{ marginLeft: 'auto' }}>
                    <Popconfirm
                      title="更新本机 WARP 客户端？"
                      description="仅升级本机（控制服务器）的 cloudflare-warp 并重启 warp-svc；不会更新任何节点服务器，组网连接会短暂中断。"
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
          local
          server={{ name: overview.name || '本机（控制服务器）', status: 'online', last_status: 'online' }}
          open={termOpen}
          onClose={() => setTermOpen(false)}
        />
      ) : null}
    </div>
  );
}

/**
 * 版本展示：已装 v<installed> · 最新 v<latest>。
 * 最新获取失败时只显示已装版本，用虚线下划线 + 悬停展示 error，且可点击重试（refresh=1）；
 * 成功时悬停显示数据来源与检查时间（checked_at 命中缓存时为首次查询时间）。
 */
function VersionPair({ installed, latest, error, loading, source, checkedAt, onRetry }) {
  if (!installed) {
    return <span className="pp-muted" style={{ fontSize: 12 }}>{loading ? '读取版本中…' : '版本未知'}</span>;
  }
  const metaTip = [source ? `来源 ${source}` : '', checkedAt ? `检查于 ${fmtTime(checkedAt)}` : '']
    .filter(Boolean)
    .join(' · ');
  return (
    <Space size={5} wrap>
      <span className="pp-mono pp-muted" style={{ fontSize: 12 }}>已装 v{installed}</span>
      {latest ? (
        <Tooltip title={metaTip || undefined}>
          <span className="pp-mono pp-muted" style={{ fontSize: 12 }}>· 最新 v{latest}</span>
        </Tooltip>
      ) : error ? (
        <Tooltip title={`最新版本获取失败：${error}（点击重试，强制刷新）`}>
          <span
            role="button"
            tabIndex={0}
            onClick={() => onRetry && onRetry()}
            onKeyDown={(e) => { if (e.key === 'Enter' && onRetry) onRetry(); }}
            style={{ fontSize: 12, color: '#d97706', borderBottom: '1px dotted #d97706', cursor: 'pointer' }}
          >
            {loading ? '检查最新版本中…' : '最新查询失败'}
          </span>
        </Tooltip>
      ) : null}
    </Space>
  );
}

/** 升级状态标签：已装或最新任一缺失时不显示，避免误报。 */
function VersionTag({ status, installed, latest }) {
  if (!installed || !latest) return null;
  if (status === 'update') return <Tag color="warning" style={{ marginInlineEnd: 0 }}>可升级</Tag>;
  if (status === 'latest') return <Tag color="success" style={{ marginInlineEnd: 0 }}>已最新</Tag>;
  return <Tag style={{ marginInlineEnd: 0 }}>未知</Tag>;
}
