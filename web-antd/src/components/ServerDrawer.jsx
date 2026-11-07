import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  CloudDownloadOutlined,
  CloudServerOutlined,
  CloudUploadOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  DisconnectOutlined,
  EditOutlined,
  SendOutlined,
  KeyOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  SyncOutlined,
  ThunderboltOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Collapse,
  Col,
  Descriptions,
  Drawer,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Row,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiPost } from '../api';
import { readForm } from '../hooks';
import { AreaChart, ChartLegend } from '../charts';
import { CopyText, ErrorBlock, JsonView, StatCard, StatusPill } from '../components';
import { watchBatch } from './JobCenter';
import RemoteShell from './RemoteShell';
import { chartPalette } from '../theme';
import { fmtAgo, fmtBytes, fmtTime, fmtUptime, levelColor, splitIps } from '../format';

const SVC_TEXT = {
  online: ['online', '在线'],
  active: ['online', '运行'],
  running: ['online', '运行'],
  inactive: ['warn', '停止'],
  failed: ['offline', '失败'],
  'not-installed': ['unknown', '未安装'],
  unknown: ['unknown', '未知'],
  offline: ['offline', '离线'],
};

/**
 * 服务器详情抽屉：资源折线 / 快捷操作（单台全部动作） / 服务状态 / 运行信息 / 下发配置
 */
export default function ServerDrawer({ serverId, onClose, onChanged }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ data: null, metrics: [], configs: [], loading: false, error: null });
  const [busy, setBusy] = useState('');
  const [install, setInstall] = useState(null);
  const [execOpen, setExecOpen] = useState(false);
  const [remoteOpen, setRemoteOpen] = useState(false);
  // 终端打开期间暂停服务器状态轮询：优先保证终端链路，关闭后补一次刷新
  const pausedRef = useRef(false);
  const remoteOpenRef = useRef(false);
  remoteOpenRef.current = remoteOpen;
  const [tasksOpen, setTasksOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [traffic, setTraffic] = useState(null);
  const [trafficOpen, setTrafficOpen] = useState(false);
  const [trafficForm, setTrafficForm] = useState({});

  const load = useCallback(async () => {
    if (!serverId) return;
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await api(`servers/${serverId}`);
      setState({
        data: (res && res.data) || null,
        metrics: (res && res.metrics_recent) || [],
        configs: (res && res.node_configs) || [],
        loading: false,
        error: null,
      });
    } catch (e) {
      setState({ data: null, metrics: [], configs: [], loading: false, error: e });
    }
  }, [serverId]);

  useEffect(() => {
    if (!serverId) return undefined;
    const timer = setInterval(() => {
      if (remoteOpenRef.current) return; // 终端打开时不刷新状态
      load().catch(() => {});
    }, 3000);
    return () => clearInterval(timer);
  }, [serverId, load]);
  useEffect(() => {
    if (serverId) load();
    else setState({ data: null, metrics: [], configs: [], loading: false, error: null });
  }, [serverId, load]);

  const loadTraffic = useCallback(async () => {
    if (!serverId) { setTraffic(null); return; }
    try {
      const res = await api(`servers/${serverId}/traffic-limit`);
      setTraffic(res || null);
    } catch (e) {
      setTraffic(null);
    }
  }, [serverId]);

  useEffect(() => { loadTraffic(); }, [loadTraffic]);

  const openTraffic = () => {
    const l = (traffic && traffic.limit) || {};
    setTrafficForm({
      enabled: !!l.enabled,
      mode: l.mode || 'both',
      limit_gb: l.limit_bytes ? Number((l.limit_bytes / 1024 ** 3).toFixed(2)) : 100,
      reset_day: l.reset_day || 1,
      reset_hour: l.reset_hour || 0,
      warn_percent: l.warn_percent || 80,
    });
    setTrafficOpen(true);
  };

  const saveTraffic = async () => {
    try {
      await api(`servers/${serverId}/traffic-limit`, {
        method: 'PUT',
        body: {
          enabled: trafficForm.enabled ? 1 : 0,
          mode: trafficForm.mode,
          limit_bytes: Math.round((Number(trafficForm.limit_gb) || 0) * 1024 ** 3),
          reset_day: Number(trafficForm.reset_day) || 1,
          reset_hour: Number(trafficForm.reset_hour) || 0,
          action: 'notify',
          warn_percent: Number(trafficForm.warn_percent) || 80,
        },
      });
      message.success('流量限制已保存');
      setTrafficOpen(false);
      loadTraffic();
      if (onChanged) onChanged();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };

  const resetTraffic = async () => {
    try {
      await api(`servers/${serverId}/traffic-limit/reset`, { method: 'POST', body: {} });
      message.success('已重置本周期');
      setTrafficOpen(false);
      loadTraffic();
    } catch (e) {
      message.error(e.message || '重置失败');
    }
  };
  /* 切换服务器时关掉上一台留下的弹窗 */
  useEffect(() => {
    setInstall(null);
    setExecOpen(false);
    setTasksOpen(false);
    setEditOpen(false);
  }, [serverId]);

  const d = state.data;
  const m = (d && d.last_metrics) || {};
  const sshInfo = m.ssh || null;
  const ssh = (() => {
    if (!sshInfo) return null;
    if (sshInfo.enabled === 'not-installed') {
      return { text: '未安装', color: 'default', toggleToEnable: true };
    }
    const enabled = sshInfo.enabled === 'enabled';
    const active = sshInfo.active === 'active';
    if (enabled && active) return { text: '已启用', color: 'green', toggleToEnable: false };
    if (enabled) return { text: '已启用（未运行）', color: 'orange', toggleToEnable: false };
    if (active) return { text: '运行中（未自启）', color: 'gold', toggleToEnable: false };
    return { text: '已停用', color: 'orange', toggleToEnable: true };
  })();
  const local = !!d && d.link_mode === 'local';

  /* 只关注这几个服务的安装 / 运行状态，其余历史探针不再展示 */
  // 展示顺序：代理引擎 → 隧道 → 组网客户端
  const MONITOR_SERVICES = ['sing-box', 'mihomo', 'mtg', 'cloudflared', 'warp-svc', 'proxypilot-agent'];
  const SERVICE_LABELS = { 'warp-svc': 'WARP', 'proxypilot-agent': 'Agent', cloudflared: 'cloudflared' };
  const services = useMemo(() => {
    const map = m.services || {};
    const vers = m.versions || {};
    if (!Object.keys(map).length) return [];
    return MONITOR_SERVICES.map((name) => {
      const state = String(map[name] || 'not-installed');
      // 版本：优先组件自报，其次用 Agent 心跳里的版本兜底
      let version = String(vers[name] || '');
      if (!version && name === 'proxypilot-agent') version = String((d && d.agent_version) || m.agent_version || '');
      return { name, label: SERVICE_LABELS[name] || name, state, version };
    });
  }, [m.services, m.versions, m.agent_version, d && d.agent_version]);

  const chartRows = useMemo(
    () =>
      (state.metrics || []).map((x) => ({
        label: fmtTime(x.created_at, '').slice(11, 16) || '—',
        cpu: Number(x.cpu_percent) || 0,
        mem: Number(x.mem_percent) || 0,
      })),
    [state.metrics]
  );

  const latencies = useMemo(() => (state.metrics || []).map((x) => Number(x.latency_ms) || 0), [state.metrics]);

  const restartService = async (svc) => {
    setBusy('svc-' + svc.name);
    try {
      await apiPost('agent/tasks', { server_id: serverId, type: 'restart', service_name: svc.name });
      message.success(`已排队重启「${svc.name}」，Agent 下次轮询时执行`);
    } catch (e) {
      message.error(e.message || '排入任务失败');
    } finally {
      setBusy('');
    }
  };

  /* ---------------- 快捷操作（单台） ---------------- */

  const rebootServer = async () => {
    setBusy('reboot');
    try {
      const res = await apiPost(`servers/${serverId}/reboot`, {});
      message.success((res && res.message) || '重启指令已下发');
      onChanged && onChanged();
    } catch (e) {
      message.error(e.message || '重启失败');
    } finally {
      setBusy('');
    }
  };

  const pushAgent = async () => {
    setBusy('agent');
    try {
      const r = await api('agent/push-update', { method: 'POST', body: { server_ids: [serverId] } });
      if (watchBatch(r)) {
        message.success('已创建后台更新任务 #' + r.job_id + '，进度见右上角「任务」');
        return;
      }
      const x = ((r && r.results) || [])[0] || {};
      if (!x.ok) message.error(x.error || '推送失败');
      else if (x.skipped) message.info(`已是最新（${(r && r.latest) || ''}）`);
      else message.success(`已排队更新到 ${(r && r.latest) || ''}`);
      load().catch(() => {});
      onChanged && onChanged();
    } catch (e) {
      message.error(e.message || '推送失败');
    } finally {
      setBusy('');
    }
  };

  const updateCloudflared = async () => {
    setBusy('cloudflared');
    try {
      const r = await apiPost(`servers/${serverId}/update-cloudflared`, {});
      if (r && r.job_id) {
        message.success(`已创建后台任务 #${r.job_id}（更新 cloudflared），进度见右上角「任务」`);
      } else {
        message.success((r && r.message) || 'cloudflared 更新任务已下发');
      }
    } catch (e) {
      message.error(e.message || '更新 cloudflared 失败');
    } finally {
      setBusy('');
    }
  };

  const sshGuardOn = !!(d && Number(d.ssh_guard) === 1);

  const updateWarp = async () => {
    setBusy('warp');
    try {
      const res = await apiPost('jobs', { kind: 'warp.update', params: { server_id: serverId } });
      if (res && res.job_id) {
        message.success(`已创建 WARP 更新任务 #${res.job_id}，进度见右上角「任务」`);
      } else {
        message.success((res && res.message) || '已下发 WARP 更新');
      }
      setTimeout(() => load().catch(() => {}), 3000);
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  const toggleSshGuard = async () => {
    if (!d) return;
    const enable = !sshGuardOn;
    setBusy('ssh');
    try {
      const res = await apiPost(`servers/${serverId}/ssh-guard`, { enabled: enable });
      message.success((res && res.message) || `已下发${enable ? '开启' : '关闭'} SSH 安全防护`);
      setTimeout(() => {
        load().catch(() => {});
        onChanged && onChanged();
      }, 1500);
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  const rotateKey = async () => {
    setBusy('key');
    try {
      const res = await apiPost(`servers/${serverId}/agent-token`, {});
      setInstall({
        title: `服务器「${d.name}」的新接入密钥`,
        command: res.install_command,
        token: res.agent_token,
        note: res.message,
      });
      load().catch(() => {});
      onChanged && onChanged();
    } catch (e) {
      setInstall({
        title: `服务器「${d ? d.name : ''}」`,
        error: e.message,
        note: e.payload && e.payload.token_hint ? `当前密钥指纹：${e.payload.token_hint}` : '',
      });
    } finally {
      setBusy('');
    }
  };

  const removeServer = async () => {
    setBusy('delete');
    try {
      await api(`servers/${serverId}`, { method: 'DELETE' });
      message.success('服务器已删除');
      onClose && onClose();
      onChanged && onChanged();
    } catch (e) {
      message.error(e.message || '删除失败');
    } finally {
      setBusy('');
    }
  };

  const statusText = SVC_TEXT[String((d && d.last_status) || 'unknown')] || ['unknown', '未知'];

  return (
    <Drawer
      open={!!serverId}
      onClose={onClose}
      width={860}
      title={
        <span className="pp-section-title">
          <CloudServerOutlined style={{ color: '#0ea5e9' }} />
          {d ? d.name : '服务器详情'}
        </span>
      }
      extra={
        <Space size={4}>
          <Popconfirm title={`重启服务器「${d ? d.name : ''}」？`} okText="重启" okButtonProps={{ danger: true }} disabled={!d} onConfirm={rebootServer}>
            <Tooltip title="重启服务器">
              <Button size="small" danger icon={<PoweroffOutlined />} loading={busy === 'reboot'} disabled={!d} aria-label="重启服务器" />
            </Tooltip>
          </Popconfirm>
          <Popconfirm title="安装 Agent 并轮换接入密钥？" okText="安装并轮换" disabled={!d || local} onConfirm={rotateKey}>
            <Tooltip title="安装 / 轮换 Agent 密钥">
              <Button size="small" icon={<KeyOutlined />} loading={busy === 'key'} disabled={!d || local} aria-label="安装或轮换密钥" />
            </Tooltip>
          </Popconfirm>
          <Tooltip title="实时终端（交互式）">
            <Button
              size="small"
              icon={<CodeOutlined />}
              disabled={!d}
              onClick={() => {
                if (!d.allow_exec) message.warning('该服务器未开启「允许远程命令」');
                else setRemoteOpen(true);
              }}
              aria-label="实时终端"
            />
          </Tooltip>
          <Tooltip title="队列命令（异步执行）">
            <Button
              size="small"
              icon={<SendOutlined />}
              disabled={!d}
              onClick={() => {
                if (!d.allow_exec) message.warning('该服务器未开启「允许远程命令」');
                else setExecOpen(true);
              }}
              aria-label="队列命令"
            />
          </Tooltip>
          <Tooltip title="任务队列">
            <Button size="small" icon={<UnorderedListOutlined />} onClick={() => setTasksOpen(true)} disabled={!d} aria-label="任务队列" />
          </Tooltip>
          <Tooltip title="编辑">
            <Button size="small" icon={<EditOutlined />} onClick={() => setEditOpen(true)} disabled={!d} aria-label="编辑" />
          </Tooltip>
          <Popconfirm title="删除该服务器？" description="删除后 Agent 与节点配置都会解除绑定。" okText="删除" okButtonProps={{ danger: true }} disabled={!d} onConfirm={removeServer}>
            <Tooltip title="删除服务器">
              <Button size="small" danger icon={<DeleteOutlined />} loading={busy === 'delete'} disabled={!d} aria-label="删除" />
            </Tooltip>
          </Popconfirm>
          <Tooltip title="刷新">
            <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading} aria-label="刷新" />
          </Tooltip>
        </Space>
      }
    >
      {state.error ? <ErrorBlock error={state.error} onRetry={load} /> : null}

      {d ? (
        <>
          <Row gutter={[12, 12]}>
            <Col span={6}>
              <StatCard icon={<ThunderboltOutlined />} label="CPU" value={`${Math.round(Number(m.cpuPercent) || 0)}%`} accent={levelColor(m.cpuPercent)} />
            </Col>
            <Col span={6}>
              <StatCard icon={<DatabaseOutlined />} label="内存" value={`${Math.round(Number(m.memPercent) || 0)}%`} accent={levelColor(m.memPercent)} />
            </Col>
            <Col span={6}>
              <StatCard icon={<DatabaseOutlined />} label="磁盘" value={`${Math.round(Number(m.diskPercent) || 0)}%`} accent={levelColor(m.diskPercent)} />
            </Col>
            <Col span={6}>
              <StatCard
                icon={<SyncOutlined />}
                label="延迟"
                value={d.last_latency_ms ? `${d.last_latency_ms} ms` : '—'}
                accent={chartPalette[5]}
                spark={latencies.length > 1 ? latencies : undefined}
              />
            </Col>
          </Row>

          <div style={{ marginTop: 14, border: '1px solid #eef2f7', borderRadius: 12, padding: '10px 14px', background: '#fbfdff' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
              <b style={{ fontSize: 13 }}>流量限额</b>
              {traffic && traffic.state === 'exceeded' ? <Tag color="error">已超限</Tag> : null}
              {traffic && traffic.state === 'warning' ? <Tag color="warning">接近上限</Tag> : null}
              <Button size="small" type="link" style={{ marginLeft: 'auto', padding: 0 }} onClick={openTraffic}>设置</Button>
            </div>
            {(() => {
              const l = (traffic && traffic.limit) || {};
              const enabled = !!l.enabled && Number(l.limit_bytes) > 0;
              const rx = Number(traffic && traffic.rx_bytes) || 0;
              const tx = Number(traffic && traffic.tx_bytes) || 0;
              const used = l.mode === 'rx' ? rx : l.mode === 'tx' ? tx : rx + tx;
              const limit = Number(l.limit_bytes) || 0;
              const left = Math.max(0, limit - used);
              return (
                <div style={{ fontSize: 12.5, color: '#475569', lineHeight: 1.9 }}>
                  <div>本周期：↑ {fmtBytes(rx)} / ↓ {fmtBytes(tx)}，合计 {fmtBytes(used)}</div>
                  {enabled ? (
                    <div>
                      上限 {fmtBytes(limit)}，剩余 <b style={{ color: left / limit <= 0.1 ? '#f43f5e' : left / limit <= 0.2 ? '#f59e0b' : '#16a34a' }}>{fmtBytes(left)}（{((left / limit) * 100).toFixed(1)}%）</b>
                    </div>
                  ) : (
                    <div className="pp-muted">未设置上限，点右上「设置」启用</div>
                  )}
                  <div className="pp-muted">
                    每月 {l.reset_day || 1} 日 {String(l.reset_hour || 0).padStart(2, '0')}:00 重置
                    {traffic && traffic.next_reset ? ' · 下次 ' + new Date(Number(traffic.next_reset)).toLocaleString() : ''}
                  </div>
                </div>
              );
            })()}
          </div>
          <Descriptions
            size="small"
            column={2}
            bordered
            style={{ marginTop: 16 }}
            items={[
              {
                key: 'st',
                label: '状态',
                children: (
                  <span className="pp-nowrap">
                    <i className={`pp-dot ${statusText[0]}`} />
                    {statusText[1]}
                    {d.last_status === 'offline' && d.last_error ? <span className="pp-muted">（{d.last_error}）</span> : null}
                  </span>
                ),
              },
              {
                key: 'ip',
                label: '公网 IP',
                children: (() => {
                  const { v4, v6 } = splitIps(d);
                  if (!v4 && !v6) return <span className="pp-mono">{d.host || '—'}</span>;
                  return (
                    <span className="pp-mono">
                      {v4 ? <span>IPv4 {v4}</span> : null}
                      {v4 && v6 ? <br /> : null}
                      {v6 ? <span>IPv6 {v6}</span> : null}
                    </span>
                  );
                })(),
              },
              {
                key: 'ip_private',
                label: '内网 / 组网 IP',
                children: (() => {
                  const lan = String(d.last_ip_private || '').trim();
                  const m4 = String(d.last_mesh_ip4 || '').trim();
                  const m6 = String(d.last_mesh_ip6 || '').trim();
                  if (!lan && !m4 && !m6) return <span className="pp-mono">—</span>;
                  return (
                    <span className="pp-mono">
                      {lan ? <span>{lan}</span> : null}
                      {m4 ? (
                        <span>
                          {lan ? <br /> : null}
                          <span className="pp-muted">WARP </span>IPv4 {m4}
                        </span>
                      ) : null}
                      {m6 ? (
                        <span>
                          {lan || m4 ? <br /> : null}
                          <span className="pp-muted">WARP </span>IPv6 {m6}
                        </span>
                      ) : null}
                    </span>
                  );
                })(),
              },
              { key: 'geo', label: '地区', children: d.ip_country ? `${d.ip_country} ${d.ip_region || ''}` : '—' },
              { key: 'seen', label: '最后心跳', children: d.agent_last_seen ? `${fmtAgo(d.agent_last_seen)}（${fmtTime(d.agent_last_seen)}）` : '—' },
              { key: 'poll', label: '轮询间隔', children: `${d.agent_next_poll_sec || '—'} 秒` },
              { key: 'host', label: '主机名', children: m.hostname || '—' },
              { key: 'os', label: '系统', children: m.os || d.os_info || '—' },
              { key: 'kernel', label: '内核', children: m.kernel || '—' },
              { key: 'up', label: '运行时长', children: fmtUptime(m.uptimeSec) },
              { key: 'tags', label: '标签', children: d.tags || '—' },
              { key: 'remark', label: '备注', children: d.remark || '—' },
            ]}
          />

          <div style={{ margin: '20px 0 8px', display: 'flex', alignItems: 'center', gap: 8 }}>
            <b style={{ fontSize: 14 }}>资源趋势</b>
            <span className="pp-muted" style={{ fontSize: 12 }}>最近 {state.metrics.length} 个采样</span>
            <ChartLegend
              style={{ marginLeft: 'auto' }}
              items={[
                { name: 'CPU', color: chartPalette[0] },
                { name: '内存', color: chartPalette[1] },
              ]}
            />
          </div>
          {chartRows.length > 1 ? (
            <AreaChart
              data={chartRows}
              series={[
                { key: 'cpu', name: 'CPU', color: chartPalette[0] },
                { key: 'mem', name: '内存', color: chartPalette[1] },
              ]}
              height={200}
              unit="%"
              formatValue={(v) => String(Math.round(v))}
            />
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有采样数据" style={{ padding: '26px 0' }} />
          )}

          <div style={{ margin: '20px 0 8px', display: 'flex', alignItems: 'center', gap: 8 }}>
            <b style={{ fontSize: 14 }}>服务状态</b>
            <span className="pp-muted" style={{ fontSize: 12 }}>sing-box / mihomo / mtg / cloudflared / WARP / Agent 的启停与更新在对应行右侧；SSH 安全防护限制来源网段</span>
          </div>
          {services.length ? (
            <Space orientation="vertical" style={{ width: '100%' }} size={8}>
              {services.map((svc) => {
                const meta = SVC_TEXT[svc.state] || ['unknown', svc.state];
                return (
                  <div
                    key={svc.name}
                    className="pp-svc-row"
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 10,
                      padding: '8px 12px',
                      border: '1px solid #eef2f7',
                      borderRadius: 10,
                      background: '#fbfcfe',
                    }}
                  >
                    <i className={`pp-dot ${meta[0]}`} />
                    <span style={{ fontWeight: 500 }}>{svc.label || svc.name}</span>
                    {svc.version ? <span className="pp-mono pp-muted" style={{ fontSize: 12 }}>v{svc.version}</span> : null}
                    <Tag color={meta[0] === 'online' ? 'success' : meta[0] === 'offline' ? 'error' : 'default'} style={{ marginInlineEnd: 0 }}>
                      {meta[1]}
                    </Tag>
                    <span style={{ marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                      {svc.name === 'proxypilot-agent' && !local && svc.state !== 'not-installed' ? (
                        <Popconfirm
                          title={`向「${d.name}」推送 Agent 更新？`}
                          description={d.agent_update && d.agent_latest ? `当前 ${d.agent_version || '未知'} → ${d.agent_latest}` : '会创建后台更新任务，进度见右上角「任务」。'}
                          okText="推送"
                          onConfirm={pushAgent}
                        >
                          <Button size="small" type="link" style={{ padding: 0 }} loading={busy === 'agent'}>更新</Button>
                        </Popconfirm>
                      ) : null}
                      {svc.name === 'warp-svc' && svc.state !== 'not-installed' ? (
                        <Popconfirm
                          title="更新 WARP 客户端？"
                          description="通过系统包管理器升级 cloudflare-warp 并重启 warp-svc；组网连接会短暂中断后自动恢复。"
                          okText="更新"
                          onConfirm={updateWarp}
                        >
                          <Button size="small" type="link" icon={<CloudDownloadOutlined />} loading={busy === 'warp'}>
                            更新
                          </Button>
                        </Popconfirm>
                      ) : null}
                      {svc.name === 'cloudflared' && svc.state !== 'not-installed' ? (
                        <Popconfirm
                          title="更新 cloudflared？"
                          description="下载最新版本，替换 systemd 实际使用的二进制并自动重启；会创建后台任务。"
                          okText="更新"
                          onConfirm={updateCloudflared}
                        >
                          <Button size="small" type="link" icon={<CloudDownloadOutlined />} loading={busy === 'cloudflared'}>
                            更新
                          </Button>
                        </Popconfirm>
                      ) : null}
                      <Button
                        size="small"
                        type="link"
                        disabled={svc.state === 'not-installed'}
                        loading={busy === 'svc-' + svc.name}
                        onClick={() => restartService(svc)}
                      >
                        重启
                      </Button>
                    </span>
                  </div>
                );
              })}
              <div
                className="pp-svc-row"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 10,
                  padding: '8px 12px',
                  border: '1px solid #eef2f7',
                  borderRadius: 10,
                  background: '#fbfcfe',
                }}
              >
                <i className={`pp-dot ${sshGuardOn ? 'online' : 'warn'}`} />
                <span style={{ fontWeight: 500 }}>SSH 安全防护</span>
                <Tag color={sshGuardOn ? 'green' : 'orange'} style={{ marginInlineEnd: 0 }}>
                  {sshGuardOn ? '仅组网网段可访问' : '允许所有 IP'}
                </Tag>
                <span style={{ marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 8 }}>
                  <Popconfirm
                    title={sshGuardOn ? '关闭 SSH 安全防护？' : '开启 SSH 安全防护？'}
                    description={
                      sshGuardOn
                        ? '关闭后所有 IP 均可尝试 SSH 登录，仅保留密码/密钥强度防护。'
                        : '开启后仅允许 100.96.0.0/12 与 2606:4700:cf1:1000::/64 访问 SSH，其他来源会被丢弃。'
                    }
                    okText={sshGuardOn ? '关闭' : '开启'}
                    okButtonProps={sshGuardOn ? { danger: true } : undefined}
                    onConfirm={toggleSshGuard}
                  >
                    <Switch size="small" checked={sshGuardOn} loading={busy === 'ssh'} />
                  </Popconfirm>
                </span>
              </div>
            </Space>
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该服务器还没上报服务状态" style={{ padding: '20px 0' }} />
          )}

          <div style={{ margin: '20px 0 8px' }}>
            <b style={{ fontSize: 14 }}>下发配置</b>
          </div>
          <Table
            className="pp-table"
            rowKey="id"
            size="small"
            dataSource={state.configs}
            pagination={false}
            locale={{ emptyText: '没有节点配置（sing-box 配置由「节点管理」下发）' }}
            columns={[
              { title: 'ID', dataIndex: 'id', width: 60 },
              { title: '名称', dataIndex: 'name', width: 180 },
              { title: '引擎', dataIndex: 'engine', width: 100, render: (v) => <Tag color="geekblue">{v || '—'}</Tag> },
              { title: '目标路径', dataIndex: 'target_path', ellipsis: true, render: (v) => <span className="pp-mono" style={{ fontSize: 12 }}>{v || '—'}</span> },
              { title: '服务', dataIndex: 'service_name', width: 120, render: (v) => v || '—' },
              {
                title: '状态',
                dataIndex: 'last_push_status',
                width: 96,
                render: (v) => (v ? <Tag color={v === 'success' ? 'success' : 'error'}>{v}</Tag> : <span className="pp-muted">未下发</span>),
              },
            ]}
          />

          <Collapse
            ghost
            style={{ marginTop: 14 }}
            items={[
              {
                key: 'raw',
                label: <span style={{ fontSize: 13, color: '#64748b' }}>原始指标（网络 / 连接 / 进程 / 磁盘 IO）</span>,
                children: (
                  <JsonView
                    value={{
                      net: m.net,
                      conn: m.conn,
                      diskIo: m.diskIo,
                      proc: m.proc,
                      load: { load1: m.load1, load5: m.load5, load15: m.load15 },
                      cores: m.cores,
                      mem: { used: m.memUsedMb, total: m.memTotalMb },
                    }}
                    maxHeight={320}
                  />
                ),
              },
            ]}
          />

          {/* 安装命令 / 密钥 */}
          <RemoteShell
        server={d}
        open={remoteOpen}
        onClose={() => {
          setRemoteOpen(false);
          load().catch(() => {});
        }}
      />

      <Modal
            open={!!install}
            title={install && install.title}
            onCancel={() => setInstall(null)}
            footer={
              <Button type="primary" onClick={() => setInstall(null)}>
                知道了
              </Button>
            }
            width={720}
            destroyOnHidden
          >
            {install && install.error ? (
              <Alert type="warning" showIcon message={install.error} description={install.note || undefined} />
            ) : (
              <>
                {install && install.token ? (
                  <div style={{ marginBottom: 14 }}>
                    <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
                      接入密钥（仅本次显示，请立即保存）
                    </div>
                    <CopyText value={install.token} display={<span className="pp-mono">{install.token}</span>} />
                  </div>
                ) : null}
                <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
                  在目标服务器上执行
                </div>
                <JsonView value={String((install && install.command) || '')} maxHeight={260} />
                {install && install.note ? (
                  <div style={{ marginTop: 10, fontSize: 12, color: '#94a3b8', lineHeight: 1.7 }}>{install.note}</div>
                ) : null}
              </>
            )}
          </Modal>

          <ExecModal
            server={d}
            open={execOpen}
            onClose={() => setExecOpen(false)}
            onOpenTasks={() => setTasksOpen(true)}
          />

          <TasksModal row={tasksOpen ? d : null} onClose={() => setTasksOpen(false)} />

          <ServerEditModal
            row={editOpen ? d : null}
            onClose={() => setEditOpen(false)}
            onSaved={() => {
              setEditOpen(false);
              load().catch(() => {});
              onChanged && onChanged();
            }}
          />
        </>
      ) : null}
      <Modal
        title="流量限额"
        open={trafficOpen}
        onCancel={() => setTrafficOpen(false)}
        onOk={saveTraffic}
        okText="保存"
        destroyOnHidden
        footer={(_, { OkBtn, CancelBtn }) => (
          <Space>
            <Button danger onClick={resetTraffic}>重置本周期</Button>
            <CancelBtn />
            <OkBtn />
          </Space>
        )}
      >
        <div style={{ display: 'grid', gap: 12 }}>
          <div className="pp-muted" style={{ fontSize: 12 }}>
            超限仅在管理后台提醒并发送邮件，不会影响用户订阅，也不会停止节点服务。
          </div>
          <div>启用上限：<Switch checked={!!trafficForm.enabled} onChange={(v) => setTrafficForm((f) => ({ ...f, enabled: v }))} /></div>
          <div>
            计费方向：
            <Select value={trafficForm.mode} style={{ width: 160, marginLeft: 8 }} onChange={(v) => setTrafficForm((f) => ({ ...f, mode: v }))}
              options={[{ value: 'both', label: '上行+下行' }, { value: 'rx', label: '仅上行' }, { value: 'tx', label: '仅下行' }]} />
          </div>
          <div>
            流量上限：
            <InputNumber min={0} value={trafficForm.limit_gb} style={{ width: 140, margin: '0 8px' }} onChange={(v) => setTrafficForm((f) => ({ ...f, limit_gb: v }))} /> GB
          </div>
          <div>
            重置日期：
            <InputNumber min={1} max={31} value={trafficForm.reset_day} style={{ width: 80, margin: '0 8px' }} onChange={(v) => setTrafficForm((f) => ({ ...f, reset_day: v }))} /> 日
            <InputNumber min={0} max={23} value={trafficForm.reset_hour} style={{ width: 80, margin: '0 8px' }} onChange={(v) => setTrafficForm((f) => ({ ...f, reset_hour: v }))} /> 时
          </div>
          <div>
            告警阈值：
            <InputNumber min={1} max={100} value={trafficForm.warn_percent} style={{ width: 90, margin: '0 8px' }} onChange={(v) => setTrafficForm((f) => ({ ...f, warn_percent: v }))} /> %
          </div>
        </div>
      </Modal>    </Drawer>
  );
}

/* ---------------- 命令执行 ---------------- */
function ExecModal({ server, open, onClose, onOpenTasks }) {
  const { message } = AntdApp.useApp();
  const [command, setCommand] = useState('');
  const [timeoutSec, setTimeoutSec] = useState(30);
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState(null);

  useEffect(() => {
    if (open) {
      setCommand('');
      setTimeoutSec(30);
      setResult(null);
    }
  }, [open]);

  const submit = async () => {
    const cmd = command.trim();
    if (!cmd) {
      message.warning('请输入要执行的命令');
      return;
    }
    setSending(true);
    try {
      const res = await apiPost(`servers/${server.id}/exec`, {
        command: cmd,
        timeout: Math.round(Number(timeoutSec) || 30) * 1000,
      });
      setResult(res || {});
      message.success((res && res.message) || '命令已排队');
    } catch (e) {
      message.error(e.message || '执行失败');
    } finally {
      setSending(false);
    }
  };

  return (
    <Modal
      open={open}
      title={`命令执行 · ${(server && server.name) || ''}`}
      onCancel={onClose}
      width={720}
      destroyOnHidden
      footer={
        <Space>
          {result ? <Button onClick={() => { onClose(); if (onOpenTasks) onOpenTasks(); }}>任务队列</Button> : null}
          <Button onClick={onClose}>关闭</Button>
          <Button type="primary" loading={sending} onClick={submit}>
            下发命令
          </Button>
        </Space>
      }
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12, borderRadius: 10 }}
        message="命令由 Agent 在下次轮询时执行"
        description="排队后可在「任务队列」查看执行状态与输出；超时时间到达后任务会被标记失败。"
      />
      <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
        命令
      </div>
      <Input.TextArea
        rows={4}
        value={command}
        onChange={(e) => setCommand(e.target.value)}
        placeholder="例如：systemctl status sing-box"
        className="pp-mono"
      />
      <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
        <span className="pp-muted" style={{ fontSize: 12 }}>
          超时（秒，2–60）
        </span>
        <InputNumber min={2} max={60} value={timeoutSec} onChange={(v) => setTimeoutSec(v || 30)} />
      </div>
      {result ? (
        <Alert
          style={{ marginTop: 12 }}
          type="success"
          showIcon
          message={result.message || '已下发'}
          description={result.task_id ? `任务 #${result.task_id}${result.duplicate ? '（重复任务已合并）' : ''}` : undefined}
        />
      ) : null}
    </Modal>
  );
}

/* ---------------- 编辑服务器 ---------------- */
function ServerEditModal({ row, onClose, onSaved }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);

  const initIdRef = useRef(null);
  useEffect(() => {
    if (!row) {
      initIdRef.current = null;
      return;
    }
    // 同一个服务器再次刷新数据时不要重置表单，否则用户的开关改动会被覆盖
    if (initIdRef.current === row.id) return;
    initIdRef.current = row.id;
    form.setFieldsValue({
      name: row.name,
      host: row.host || '',
      tags: row.tags || '',
      remark: row.remark || '',
      enabled: !!row.enabled,
      allow_exec: !!row.allow_exec,
      metrics_every: row.metrics_every || 1,
      agent_next_poll_sec: row.agent_next_poll_sec || 10,
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row]);

  const save = async () => {
    const v = await readForm(form);
    if (!v) return;
    setSaving(true);
    try {
      await api(`servers/${row.id}`, { method: 'PUT', body: v });
      message.success('服务器已更新');
      onSaved();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={!!row}
      title={row ? `编辑服务器 · ${row.name}` : ''}
      onCancel={onClose}
      onOk={save}
      confirmLoading={saving}
      okText="保存"
      width={600}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 12 }}>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="name" label="名称" rules={[{ required: true, message: '请填写名称' }]}>
              <Input maxLength={64} />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item name="host" label="机器地址" extra="可留空（通常由 Agent 上报）">
              <Input placeholder="1.2.3.4 或 host.example.com" />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="tags" label="标签">
              <Input placeholder="香港,核心" maxLength={128} />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item name="agent_next_poll_sec" label="轮询间隔（秒）">
              <InputNumber min={2} max={900} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item name="metrics_every" label="每 N 次采指标">
              <InputNumber min={1} max={10} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={12}>
          <Col span={8}>
            <Form.Item name="enabled" label="启用" valuePropName="checked">
              <Switch size="small" />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item name="allow_exec" label="允许远程命令" valuePropName="checked">
              <Switch size="small" />
            </Form.Item>
          </Col>
        </Row>
        <Form.Item name="remark" label="备注">
          <Input.TextArea rows={2} maxLength={500} />
        </Form.Item>
      </Form>
    </Modal>
  );
}

/* ---------------- 任务队列 ---------------- */
function TasksModal({ row, onClose }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ data: [], loading: false, error: null });

  const load = useCallback(async () => {
    if (!row) return;
    setState({ data: [], loading: true, error: null });
    try {
      const res = await api(`servers/${row.id}/tasks`, { query: { limit: 30 } });
      setState({ data: (res && res.data) || [], loading: false, error: null });
    } catch (e) {
      setState({ data: [], loading: false, error: e });
    }
  }, [row]);

  useEffect(() => {
    if (row) load();
  }, [row, load]);

  const cancel = async (task) => {
    try {
      await api(`agent/tasks/${task.id}`, { method: 'DELETE' });
      message.success('任务已取消');
      load();
    } catch (e) {
      message.error(e.message || '取消失败');
    }
  };

  return (
    <Modal
      open={!!row}
      title={row ? `任务队列 · ${row.name}` : '任务队列'}
      onCancel={onClose}
      footer={<Button onClick={onClose}>关闭</Button>}
      width={760}
    >
      {state.error ? <ErrorBlock error={state.error} onRetry={load} /> : null}
      <Table
        rowKey={(r) => r.id || r.task_id || Math.random()}
        size="small"
        loading={state.loading}
        dataSource={state.data}
        pagination={{ pageSize: 10, size: 'small' }}
        locale={{ emptyText: '暂无任务' }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 64 },
          {
            title: '类型',
            width: 110,
            render: (_, r) => <Tag color="geekblue">{r.type || r.action || r.task_type || '—'}</Tag>,
          },
          {
            title: '状态',
            width: 96,
            render: (_, r) => <StatusPill status={r.status} />,
          },
          {
            title: '更新时间',
            width: 150,
            render: (_, r) => <span className="pp-muted">{fmtTime(r.updated_at || r.created_at)}</span>,
          },
          {
            title: '结果',
            ellipsis: true,
            render: (_, r) => (
              <span className="pp-mono" style={{ color: '#64748b', fontSize: 12 }}>
                {String(r.result || r.message || r.error || r.output || '—').slice(0, 120)}
              </span>
            ),
          },
          {
            title: '',
            width: 72,
            render: (_, r) =>
              String(r.status) === 'pending' ? (
                <Popconfirm title="取消该任务？" okText="取消任务" onConfirm={() => cancel(r)}>
                  <Button type="link" size="small" danger>
                    取消
                  </Button>
                </Popconfirm>
              ) : null,
          },
        ]}
      />
    </Modal>
  );
}
