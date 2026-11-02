import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  CloudDownloadOutlined,
  CloudServerOutlined,
  CloudUploadOutlined,
  DatabaseOutlined,
  ReloadOutlined,
  SyncOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Collapse,
  Descriptions,
  Drawer,
  Empty,
  Popconfirm,
  Progress,
  Row,
  Col,
  Space,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiPost } from '../api';
import { AreaChart, ChartLegend, Sparkline } from '../charts';
import { ErrorBlock, JsonView, StatCard } from '../components';
import { chartPalette } from '../theme';
import { fmtAgo, fmtNum, fmtTime, fmtUptime, levelColor, splitIps } from '../format';

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
 * 服务器详情抽屉：资源折线 / 服务状态（可重启）/ 运行信息 / 下发配置 / Agent 升级
 */
export default function ServerDrawer({ serverId, onClose, onChanged }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ data: null, metrics: [], configs: [], loading: false, error: null });
  const [busy, setBusy] = useState('');

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
    if (serverId) load();
    else setState({ data: null, metrics: [], configs: [], loading: false, error: null });
  }, [serverId, load]);

  const d = state.data;
  const m = (d && d.last_metrics) || {};
  const sshInfo = m.ssh || null;
  const sshState = (() => {
    if (!sshInfo) return null;
    if (sshInfo.enabled === 'not-installed') return { text: '未安装', color: 'default' };
    const enabled = sshInfo.enabled === 'enabled';
    const active = sshInfo.active === 'active';
    if (enabled && active) return { text: '已启用', color: 'green' };
    if (enabled) return { text: '已启用（未运行）', color: 'orange' };
    if (active) return { text: '运行中（未自启）', color: 'gold' };
    return { text: '已停用', color: 'orange' };
  })();
  /* 只关注这几个服务的安装 / 运行状态，其余历史探针不再展示 */
  const MONITOR_SERVICES = ['sing-box', 'xray', 'mtg', 'cloudflared'];
  const services = useMemo(() => {
    const map = m.services || {};
    const vers = m.versions || {};
    if (!Object.keys(map).length) return [];
    return MONITOR_SERVICES.map((name) => ({
      name,
      state: String(map[name] || 'not-installed'),
      version: String(vers[name] || ''),
    }));
  }, [m.services, m.versions]);

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

  const updateCloudflared = async () => {
    setBusy('cf-update');
    try {
      const res = await apiPost(`servers/${serverId}/update-cloudflared`, {});
      message.success(res.message || 'cloudflared 更新任务已下发');
    } catch (e) {
      message.error(e.message || '更新 cloudflared 失败');
    } finally {
      setBusy('');
    }
  };

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

  const statusText = SVC_TEXT[String(d && d.last_status || 'unknown')] || ['unknown', '未知'];

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
        <Space>
          <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
            刷新
          </Button>
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
                label: '地址',
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
              { key: 'geo', label: '地区', children: d.ip_country ? `${d.ip_country} ${d.ip_region || ''}` : '—' },
              { key: 'agent', label: 'Agent', children: d.agent_version ? <Tag color="blue">{d.agent_version}</Tag> : '—' },
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
            <span className="pp-muted" style={{ fontSize: 12 }}>只监控 sing-box / xray / mtg / cloudflared（版本由 Agent 上报）；点「重启」会排队一条 restart 任务</span>
            <span style={{ marginLeft: 'auto' }}>
              {sshState ? (
                <Tag color={sshState.color} style={{ marginInlineEnd: 0 }}>
                  SSH {sshState.text}
                </Tag>
              ) : (
                <span className="pp-muted" style={{ fontSize: 12 }}>SSH 状态待 Agent ≥ 1.5.1 上报</span>
              )}
            </span>
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
                    <span style={{ fontWeight: 500 }}>{svc.name}</span>
                    {svc.version ? <span className="pp-mono pp-muted" style={{ fontSize: 12 }}>v{svc.version}</span> : null}
                    <Tag color={meta[0] === 'online' ? 'success' : meta[0] === 'offline' ? 'error' : 'default'} style={{ marginInlineEnd: 0 }}>
                      {meta[1]}
                    </Tag>
                    <Button
                      size="small"
                      type="link"
                      style={{ marginLeft: 'auto' }}
                      disabled={svc.state === 'not-installed'}
                      loading={busy === 'svc-' + svc.name}
                      onClick={() => restartService(svc)}
                    >
                      重启
                    </Button>
                    {svc.name === 'cloudflared' ? (
                      <Tooltip title="下载最新 cloudflared，替换后自动重启服务">
                        <Button
                          size="small"
                          type="link"
                          icon={<CloudDownloadOutlined />}
                          loading={busy === 'cf-update'}
                          onClick={updateCloudflared}
                        >
                          更新
                        </Button>
                      </Tooltip>
                    ) : null}
                  </div>
                );
              })}
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
            locale={{ emptyText: '没有节点配置（sing-box / xray 配置由「节点管理」下发）' }}
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
        </>
      ) : null}
    </Drawer>
  );
}
