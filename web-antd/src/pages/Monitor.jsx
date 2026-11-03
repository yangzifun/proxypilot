import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertOutlined,
  ArrowDownOutlined,
  ArrowUpOutlined,
  BellOutlined,
  CheckCircleOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  DisconnectOutlined,
  EnvironmentOutlined,
  ExpandOutlined,
  LinuxOutlined,
  AppleOutlined,
  WindowsOutlined,
  ReloadOutlined,
  ThunderboltOutlined,
  ZoomInOutlined,
  ZoomOutOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Badge,
  Button,
  Col,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Progress,
  Row,
  Segmented,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { api } from '../api';
import { useAsync, usePolling, readForm } from '../hooks';
import { ChartLegend, ColumnChart, DonutChart, GaugeChart, HAS_CANVAS, ScatterPlot } from '../charts';
import { ErrorBlock, PageCard, StatCard, StatusPill } from '../components';
import ServerDrawer from '../components/ServerDrawer';
import { fmtAgo, fmtBytes, fmtNum, fmtTime, fmtUptime, levelColor, splitIps } from '../format';
import { countryCentroid } from '../geo';
import { chartPalette } from '../theme';
import ServerUnlockModal from '../components/ServerUnlockModal';
import OsIcon from '../components/osIcons';

const MAP_LAT_TOP = 84;
const MAP_LAT_BOTTOM = -58;

const SERVICE_STATE = {
  active: ['online', '运行'],
  running: ['online', '运行'],
  inactive: ['warn', '停止'],
  failed: ['offline', '失败'],
  'not-installed': ['unknown', '未安装'],
  unknown: ['unknown', '未知'],
  offline: ['offline', '离线'],
};

const stateText = (s) => {
  const hit = SERVICE_STATE[String(s || '').toLowerCase()];
  return hit ? hit[1] : s || '—';
};

/** 与 worker.js 的 mapXY 保持同一套等距圆柱投影 */
function mapXY(lat, lon) {
  const left = (Number(lon) + 180) / 360;
  const top = (MAP_LAT_TOP - Number(lat)) / (MAP_LAT_TOP - MAP_LAT_BOTTOM);
  return {
    left: Math.max(0, Math.min(100, left * 100)),
    top: Math.max(0, Math.min(100, top * 100)),
  };
}

export default function Monitor({ embedded = false }) {
  const [auto, setAuto] = useState(true);
  const [detailId, setDetailId] = useState(null);
  const [unlockFor, setUnlockFor] = useState(null);

  const { data, loading, error, reload } = useAsync(() => api('servers/monitor'), []);
  // 节点连接与采集统计（sing-box 节点，按 server_id 合并进服务器卡片）
  const nodeStats = useAsync(() => api('stats/traffic/nodes'), []);
  const nodeRows = (nodeStats.data && nodeStats.data.data) || [];
  const nodeByServer = useMemo(() => {
    const m = new Map();
    nodeRows.forEach((n) => {
      if (n.server_id) m.set(Number(n.server_id), n);
    });
    return m;
  }, [nodeRows]);

  usePolling(() => reload().catch(() => {}), auto ? 5000 : 0);
  usePolling(() => nodeStats.reload().catch(() => {}), auto ? 15000 : 0);

  const servers = (data && data.servers) || [];
  const summary = (data && data.summary) || {};

  const avg = useMemo(() => {
    const live = servers.filter((s) => s.status === 'online');
    const pick = live.length ? live : servers;
    if (!pick.length) return { cpu: 0, mem: 0, disk: 0 };
    const sum = (k) => pick.reduce((acc, s) => acc + (Number(s[k]) || 0), 0) / pick.length;
    return { cpu: sum('cpu_percent'), mem: sum('mem_percent'), disk: sum('disk_percent') };
  }, [servers]);

  const perServer = useMemo(
    () =>
      servers.map((s) => ({
        label: (s.name || `#${s.id}`).slice(0, 12),
        fullLabel: s.name || `#${s.id}`,
        cpu: Number(s.cpu_percent) || 0,
        mem: Number(s.mem_percent) || 0,
        disk: Number(s.disk_percent) || 0,
      })),
    [servers]
  );

  const online = summary.online || servers.filter((s) => s.status === 'online').length;
  const offline = summary.offline || servers.filter((s) => s.status === 'offline').length;
  const unknown = Math.max(0, servers.length - online - offline);
  const failedList = summary.failed_list || [];
  const offlineServerIds = new Set(servers.filter((s) => s.status === 'offline').map((s) => Number(s.id)));
  const activeFailedList = failedList.filter((f) => !offlineServerIds.has(Number(f.server_id)));
  const showSkeleton = loading && !data;
  const alertsCount = activeFailedList.length + offline;

  /* 告警只在「确实有告警」时弹一次，关掉后可用顶部按钮再看 */
  const [alertsOpen, setAlertsOpen] = useState(false);
  const [alertsNotified, setAlertsNotified] = useState(false);
  /* 全球分布大图弹窗 */
  const [mapOpen, setMapOpen] = useState(false);
  useEffect(() => {
    if (!alertsNotified && !showSkeleton && alertsCount > 0) {
      setAlertsOpen(true);
      setAlertsNotified(true);
    }
  }, [alertsCount, alertsNotified, showSkeleton]);

  return (
    <div>
      <div className="pp-toolbar">
        <Segmented
          value={auto ? 'auto' : 'manual'}
          onChange={(v) => setAuto(v === 'auto')}
          options={[
            { label: '实时刷新 5s', value: 'auto' },
            { label: '手动', value: 'manual' },
          ]}
        />
        <span className="pp-muted" style={{ fontSize: 12 }}>
          {loading ? '正在采集…' : `最后更新 ${fmtTime(Date.now())}`}
        </span>
        <div className="pp-toolbar-right">
          {alertsCount > 0 ? (
            <Button danger icon={<AlertOutlined />} onClick={() => setAlertsOpen(true)}>
              告警 {alertsCount}
            </Button>
          ) : null}
          <Button icon={<ReloadOutlined />} onClick={() => reload().catch(() => {})} loading={loading}>
            刷新
          </Button>
        </div>
      </div>

      {error ? (
        <div style={{ marginBottom: 16 }}>
          <ErrorBlock error={error} onRetry={() => reload().catch(() => {})} title="监控数据加载失败" />
        </div>
      ) : null}

      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={8}>
          <StatCard
            icon={<ArrowDownOutlined />}
            label="实时下行"
            value={fmtBytes(summary.rx_rate || 0)}
            unit="/s"
            hint={`${online} 台在线 · 5 秒刷新`}
            accent="#0ea5e9"
          />
        </Col>
        <Col xs={12} md={8}>
          <StatCard
            icon={<ArrowUpOutlined />}
            label="实时上行"
            value={fmtBytes(summary.tx_rate || 0)}
            unit="/s"
            hint={`${online} 台在线 · 5 秒刷新`}
            accent="#8b5cf6"
          />
        </Col>
        <Col xs={24} md={8}>
          <StatCard
            icon={<ThunderboltOutlined />}
            label="集群总流量"
            value={fmtBytes((summary.rx_rate || 0) + (summary.tx_rate || 0))}
            unit="/s"
            hint={`下行 + 上行 · ${servers.length} 台服务器`}
            accent="#f59e0b"
          />
        </Col>
      </Row>
      {/* 顶部三张卡用 grid 排：同一行内自动等高（左二 + 右地图） */}
      <div className="pp-mon-top-grid">
        <PageCard
          className="pp-mon-top"
          title={<span className="pp-section-title"><DashboardOutlined style={{ color: '#4f46e5' }} />集群在线率</span>}
          extra={<span className="pp-muted" style={{ fontSize: 12 }}>在线率 {servers.length ? ((online / servers.length) * 100).toFixed(0) : 0}%</span>}
          loading={showSkeleton}
        >
          <div className="pp-mon-fill">
            <div className="pp-cluster-chart">
              <DonutChart
                size={156}
                centerLabel="台服务器"
                centerValue={servers.length}
                data={[
                  { name: '在线', value: online, color: chartPalette[3] },
                  { name: '离线', value: offline, color: chartPalette[4] },
                  { name: '未知', value: unknown, color: '#cbd5e1' },
                ]}
              />
            </div>
            {/* 服务器/在线/离线/未知 已在环图 + 图例里，这里只补服务异常与未安装项 */}
            <div className="pp-cluster-foot">
              服务异常 <b style={{ color: summary.failed_services ? '#f59e0b' : '#0f172a' }}>{fmtNum(summary.failed_services || 0)}</b>
              <span className="pp-muted">· 未安装 {fmtNum(summary.not_installed || 0)} 项</span>
            </div>
          </div>
        </PageCard>

        <PageCard
          className="pp-mon-top"
          title={<span className="pp-section-title"><ThunderboltOutlined style={{ color: '#f59e0b' }} />资源均值</span>}
          extra={<span className="pp-muted" style={{ fontSize: 12 }}>仅在线节点</span>}
          loading={showSkeleton}
        >
          <div className="pp-mon-fill">
            <div className="pp-avg-body">
              <Row gutter={8} style={{ textAlign: 'center' }}>
                <Col span={12}>
                  <GaugeChart value={avg.cpu} label="CPU" size={148} />
                </Col>
                <Col span={12}>
                  <GaugeChart value={avg.mem} label="内存" size={148} />
                </Col>
              </Row>
              <Row gutter={8} style={{ marginTop: 8 }}>
                <Col span={24}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, color: '#64748b' }}>
                    <span>磁盘均值</span>
                    <b style={{ color: levelColor(avg.disk) }}>{avg.disk.toFixed(0)}%</b>
                  </div>
                  <Progress percent={Math.round(avg.disk)} showInfo={false} strokeColor={levelColor(avg.disk)} />
                </Col>
              </Row>
            </div>
          </div>
        </PageCard>

        <PageCard
          className="pp-map-card"
          title={<span className="pp-section-title"><EnvironmentOutlined style={{ color: '#06b6d4' }} />全球分布</span>}
          extra={
            <Space size={12}>
              <ChartLegend
                items={[
                  { name: '在线', color: '#10b981' },
                  { name: '离线', color: '#f43f5e' },
                  { name: '未知', color: '#cbd5e1' },
                ]}
              />
              <Button size="small" icon={<ExpandOutlined />} onClick={() => setMapOpen(true)}>
                查看大图
              </Button>
            </Space>
          }
          loading={showSkeleton}
        >
          <WorldMap servers={servers} onPick={(s) => setDetailId(s.id)} onExpand={() => setMapOpen(true)} />
        </PageCard>
      </div>

      {/* 全球分布大图：点卡片或「查看大图」打开，每个地点带地区标签 */}
      <Modal
        className="pp-map-modal"
        open={mapOpen}
        title={<span><EnvironmentOutlined style={{ color: '#06b6d4', marginRight: 6 }} />全球分布</span>}
        onCancel={() => setMapOpen(false)}
        footer={<Button onClick={() => setMapOpen(false)}>关闭</Button>}
        width={1120}
        centered
        destroyOnHidden
      >
        <WorldMap
          large
          zoomable
          servers={servers}
          onPick={(s) => {
            setMapOpen(false);
            setDetailId(s.id);
          }}
        />
      </Modal>

      {/* 告警改成弹窗：只有确实存在告警时才出现 */}
      <Modal
        open={alertsOpen}
        title={
          <span>
            <AlertOutlined style={{ color: '#f43f5e', marginRight: 6 }} />
            告警（{activeFailedList.length + offline}）
          </span>
        }
        onCancel={() => setAlertsOpen(false)}
        footer={<Button onClick={() => setAlertsOpen(false)}>关闭</Button>}
        width={560}
        centered
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, maxHeight: 420, overflow: 'auto' }}>
          {servers
            .filter((s) => s.status === 'offline')
            .map((s) => (
              <div key={'off-' + s.id} style={{ display: 'flex', gap: 8, alignItems: 'center', fontSize: 13 }}>
                <i className="pp-dot offline" />
                <b>{s.name}</b>
                <span className="pp-muted">离线</span>
                <Button
                  type="link"
                  size="small"
                  style={{ marginLeft: 'auto' }}
                  onClick={() => {
                    setAlertsOpen(false);
                    setDetailId(s.id);
                  }}
                >
                  详情
                </Button>
              </div>
            ))}
          {activeFailedList.map((f, i) => (
            <div key={'f-' + i} style={{ display: 'flex', gap: 8, alignItems: 'center', fontSize: 13 }}>
              <i className="pp-dot warn" />
              <b>{f.server_name}</b>
              <span className="pp-muted">{f.service}</span>
              <Tag color="warning" style={{ marginLeft: 'auto', marginInlineEnd: 0 }}>{stateText(f.state)}</Tag>
            </div>
          ))}
        </div>
      </Modal>

      {embedded ? null : (
      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">各节点资源占用</span>}
          extra={
            <ChartLegend
              items={[
                { name: 'CPU', color: chartPalette[0] },
                { name: '内存', color: chartPalette[1] },
                { name: '磁盘', color: chartPalette[2] },
              ]}
            />
          }
          loading={showSkeleton}
        >
          {perServer.length ? (
            <ColumnChart
              data={perServer}
              series={[
                { key: 'cpu', name: 'CPU', color: chartPalette[0] },
                { key: 'mem', name: '内存', color: chartPalette[1] },
                { key: 'disk', name: '磁盘', color: chartPalette[2] },
              ]}
              height={248}
              unit="%"
              formatValue={(v) => String(Math.round(v))}
              referenceLine={80}
              yMax={100}
            />
          ) : (
            <div style={{ height: 248 }}>
              <Empty description="还没有服务器上报指标" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ paddingTop: 60 }} />
            </div>
          )}
        </PageCard>
      </div>
      )}


      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">服务器明细</span>}
          extra={<span className="pp-muted" style={{ fontSize: 12 }}>{servers.length} 台 · 点击卡片看详情</span>}
          bodyStyle={{ padding: 16 }}
          loading={showSkeleton}
        >
          <ServerCards servers={servers} nodeByServer={nodeByServer} onPick={(s) => setDetailId(s.id)} onUnlock={setUnlockFor} />
        </PageCard>
      </div>

      <ServerDrawer serverId={detailId} onClose={() => setDetailId(null)} onChanged={() => reload().catch(() => {})} />
      <ServerUnlockModal server={unlockFor} onClose={() => setUnlockFor(null)} />
    </div>
  );
}

/* 国家标识：兼容「HK」这种国家码，也兼容 🇭🇰 这种国旗 emoji（Windows 不显示国旗，
   会把两个 Regional Indicator 回退成两个字母）——统一取出两位国家码，用色块渲染 */
function countryCodeOf(v) {
  const s = String(v || '').trim();
  if (!s) return '';
  if (/^[A-Za-z]{2}$/.test(s)) return s.toUpperCase();
  const cps = Array.from(s);
  const isRi = (cp) => cp >= 0x1f1e6 && cp <= 0x1f1ff;
  // 单个或多个国旗 emoji：取第一个
  for (let i = 0; i + 1 < cps.length; i += 1) {
    const a = cps[i].codePointAt(0);
    const b = cps[i + 1].codePointAt(0);
    if (isRi(a) && isRi(b)) return String.fromCharCode(a - 0x1f1e6 + 65, b - 0x1f1e6 + 65);
  }
  return s.toUpperCase().slice(0, 2);
}

/* 国旗图片（与老版一致：flagcdn.com/w40/<cc>.png）
   Windows 不渲染国旗 emoji，所以统一用图片；CDN 挂了则回退成国家码色块 */
function CountryFlag({ code }) {
  const cc = String(code || '').trim().toUpperCase();
  const [failed, setFailed] = useState(false);
  if (!/^[A-Z]{2}$/.test(cc)) return null;
  if (failed) return <span className="pp-cc">{cc}</span>;
  return (
    <img
      className="pp-flag"
      src={`https://flagcdn.com/w40/${cc.toLowerCase()}.png`}
      alt={cc}
      title={cc}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}

function ServerCards({ servers, nodeByServer, onPick, onUnlock }) {
  if (!servers.length) {
    return <Empty description="还没有服务器" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ padding: '26px 0' }} />;
  }
  return (
    <Row gutter={[12, 12]}>
      {servers.map((s) => (
        <Col key={s.id} xs={24} sm={12} xl={8} xxl={6}>
          <ServerCard
            s={s}
            node={nodeByServer ? nodeByServer.get(Number(s.id)) : null}
            onPick={onPick}
            onUnlock={onUnlock}
          />
        </Col>
      ))}
    </Row>
  );
}


/** 根据 Agent 上报的系统名称返回发行版图标与简称。 */
function osBadge(osName) {
  const name = String(osName || '').toLowerCase();
  if (!name) return null;
  const full = String(osName);
  if (name.includes('windows')) return { icon: <WindowsOutlined />, label: 'Windows', color: '#2563eb', full };
  if (name.includes('mac os') || name.includes('macos') || name.includes('darwin')) {
    return { icon: <AppleOutlined />, label: 'macOS', color: '#334155', full };
  }
  const distros = [
    ['ubuntu', 'Ubuntu', 'ubuntu', '#e95420'],
    ['debian', 'Debian', 'debian', '#a80030'],
    ['centos', 'CentOS', 'centos', '#932279'],
    ['rocky', 'Rocky', 'rockylinux', '#10b981'],
    ['alma', 'AlmaLinux', 'almalinux', '#0f4266'],
    ['fedora', 'Fedora', 'fedora', '#294172'],
    ['arch', 'Arch', 'archlinux', '#1793d1'],
    ['alpine', 'Alpine', 'alpinelinux', '#0d597f'],
    ['opensuse', 'openSUSE', 'opensuse', '#73ba25'],
    ['amazon', 'Amazon Linux', '', '#ff9900'],
    ['oracle', 'Oracle Linux', 'oracle', '#c74634'],
    ['linux', 'Linux', '', '#f59e0b'],
  ];
  const hit = distros.find(([key]) => name.includes(key));
  return {
    icon: hit && hit[2] ? <OsIcon name={hit[2]} /> : <LinuxOutlined />,
    label: hit ? hit[1] : 'Linux',
    color: hit ? hit[3] : '#f59e0b',
    full,
  };
}

function ServerCard({ s, node, onPick, onUnlock }) {
  const net = s.net || {};
  const io = s.disk_io || {};
  const conn = s.conn || {};
  const proc = s.proc || {};
  const offline = s.status !== 'online';
  const dot = s.status === 'online' ? 'online' : s.status === 'offline' ? 'offline' : 'unknown';
  const cc = countryCodeOf(s.ip_country);
  const region = String(s.ip_region || '').trim();
  const { v4, v6 } = splitIps(s);
  const memSub = s.mem_total_mb ? `${fmtNum(s.mem_used_mb)} / ${fmtNum(s.mem_total_mb)} MB` : '';
  const hostname = s.hostname || (s.metrics && s.metrics.hostname) || '';
  const uptimeSec = Number(s.uptime_sec || (s.metrics && s.metrics.uptimeSec) || 0);
  const lastSeen = Number(s.last_seen || s.last_check_at || s.agent_last_seen || 0);
  const os = osBadge(s.os || (s.metrics && s.metrics.os));
  const bad = Object.entries(s.services || {}).filter(
    ([, v]) => !['active', 'running', 'inactive', 'not-installed'].includes(String(v).toLowerCase())
  );

  return (
    <div
      className={`pp-srv-card${s.status === 'offline' ? ' is-down' : ''}${offline && s.status !== 'offline' ? ' is-offline' : ''}`}
      role="button"
      tabIndex={0}
      onClick={() => onPick(s)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') onPick(s);
      }}
    >
      <div className="pp-srv-head">
        <span className="pp-srv-name">
          <i className={`pp-dot ${dot}`} />
          {s.name || `#${s.id}`}
        </span>
        <span style={{ display: 'inline-flex', gap: 10, alignItems: 'center' }}>
          <span className="pp-srv-link" onClick={(e) => { e.stopPropagation(); if (onUnlock) onUnlock(s); }}>解锁详情</span>
          <span className="pp-srv-link">详情 ›</span>
        </span>
      </div>

      <div className="pp-srv-sub">
        {cc || region ? (
          <div className="pp-srv-geo">
            <CountryFlag code={cc} />
            {region ? <span>{region}</span> : null}
          </div>
        ) : null}
        <div className="pp-mono pp-srv-ip">IPv4 {v4 || '—'}</div>
        <div className="pp-mono pp-srv-ip">IPv6 {v6 || '—'}</div>
        <div className="pp-srv-host-row">
          <span>{hostname || '—'}</span>
          {os ? (
            <Tooltip title={os.full}>
              <span className="pp-srv-os" style={{ color: os.color }}>
                {os.icon}
                <span>{os.label}</span>
              </span>
            </Tooltip>
          ) : null}
        </div>
        <div>
          运行 {fmtUptime(uptimeSec)} · 心跳 {lastSeen ? fmtAgo(lastSeen) : '从未'}
        </div>
      </div>

      <div className="pp-srv-gauges">
        <RingGauge percent={s.cpu_percent} label="CPU" sub={`${s.cores || 1} 核`} />
        <RingGauge percent={s.mem_percent} label="内存" sub={memSub} />
        <RingGauge percent={s.disk_percent} label="磁盘" sub="" />
      </div>

      <div className="pp-srv-kv">
        <div>
          <span>实时网络 ↓ / ↑</span>
          <span>
            {fmtBytes(net.rxRate)}/s / {fmtBytes(net.txRate)}/s
          </span>
        </div>
        <div>
          <span>累计网络 ↓ / ↑</span>
          <span>
            {fmtBytes(net.rxBytes ?? net.rx_bytes)} / {fmtBytes(net.txBytes ?? net.tx_bytes)}
          </span>
        </div>
        <div>
          <span>磁盘实时 读 / 写</span>
          <span>
            {fmtBytes(io.readRate)}/s / {fmtBytes(io.writeRate)}/s
          </span>
        </div>
        <div>
          <span>磁盘累计 读 / 写</span>
          <span>
            {fmtBytes(io.readBytes ?? io.read_bytes)} / {fmtBytes(io.writeBytes ?? io.write_bytes)}
          </span>
        </div>
        <div>
          <span>连接</span>
          <span>{conn.total == null ? '—' : fmtNum(conn.total)}</span>
        </div>
        <div>
          <span>进程</span>
          <span>{proc.total == null ? '—' : fmtNum(proc.total)}</span>
        </div>
        <div>
          <span>负载 1 / 5 / 15</span>
          <span>
            {Number(s.load1 || 0).toFixed(2)} / {Number(s.load5 || 0).toFixed(2)} / {Number(s.load15 || 0).toFixed(2)}
          </span>
        </div>
        {node ? (
          <>
            <div>
              <span>sing-box 节点连接</span>
              <span>
                {fmtNum(node.conn_count)}
                {node.error ? (
                  <Tag color="orange" style={{ marginInlineStart: 6 }}>采集异常</Tag>
                ) : null}
              </span>
            </div>
            <div>
              <span>节点实时 ↓ / ↑</span>
              <span>
                {fmtBytes(node.down_rate)}/s / {fmtBytes(node.up_rate)}/s
              </span>
            </div>
            <div>
              <span>节点累计 ↓ / ↑</span>
              <span>
                {fmtBytes(node.down_total)} / {fmtBytes(node.up_total)}
              </span>
            </div>
          </>
        ) : null}
      </div>

      {bad.length ? <div className="pp-srv-bad">服务异常：{bad.map(([k]) => k).join('、')}</div> : null}
    </div>
  );
}

function RingGauge({ percent, label, sub }) {
  const v = Math.max(0, Math.min(100, Math.round(Number(percent) || 0)));
  return (
    <div className="pp-srv-ring">
      <Progress
        type="circle"
        size={62}
        percent={v}
        strokeWidth={7}
        strokeColor={levelColor(v)}
        format={() => <span style={{ fontSize: 13, fontWeight: 600 }}>{v}%</span>}
      />
      <div className="pp-srv-ring-label">{label}</div>
      {sub ? (
        <div className="pp-srv-ring-sub" title={sub}>
          {sub}
        </div>
      ) : null}
    </div>
  );
}

/* ---------------- 世界地图 ---------------- */
function WorldMap({ servers, onPick, large = false, onExpand, zoomable = false }) {
  const [hover, setHover] = useState(null);
  const [hoverCluster, setHoverCluster] = useState(null);
  const mapRef = useRef(null);
  const dragRef = useRef(null);
  const movedRef = useRef(false);
  const [box, setBox] = useState({ w: 0, h: 0 });
  const [view, setView] = useState({ s: 1, x: 0, y: 0 });

  /**
   * 定位优先级：
   *  1) 服务端已解析出的经纬度（ip_lat / ip_lon）
   *  2) 只有国家码时，退回国家中心点（与 worker.js 的 mapPointOf 同一策略）
   * 同一国家的多台服务器会叠在一个点上，按 id 做一点点稳定偏移，避免互相盖住。
   */
  const pts = servers
    .map((s) => {
      const lat = Number(s.ip_lat);
      const lon = Number(s.ip_lon);
      const hasGeo = s.ip_lat !== null && s.ip_lon !== null && Number.isFinite(lat) && Number.isFinite(lon);
      let base = null;
      let approx = false;
      if (hasGeo) {
        base = { lat, lon };
      } else {
        const c = countryCentroid(s.ip_country);
        if (c) {
          base = { lat: c[0], lon: c[1] };
          approx = true;
        }
      }
      if (!base) return null;
      const xy = mapXY(base.lat, base.lon);
      const jx = approx ? (((Number(s.id) || 0) % 5) - 2) * 0.55 : 0;
      const jy = approx ? ((((Number(s.id) || 0) * 7) % 5) - 2) * 0.55 : 0;
      return {
        s,
        approx,
        left: Math.max(0, Math.min(100, xy.left + jx)),
        top: Math.max(0, Math.min(100, xy.top + jy)),
      };
    })
    .filter(Boolean);

  const placedIds = new Set(pts.map((p) => p.s.id));
  const unplaced = servers.filter((s) => !placedIds.has(s.id));
  const approxCount = pts.filter((p) => p.approx).length;

  /* 小图：把邻近点合并成带数量的气泡，解决 18 台服务器挤在东亚看不清的问题 */
  const clusters = (() => {
    if (large) return [];
    const groups = [];
    for (const p of pts) {
      const hit = groups.find((g) => Math.abs(g.x - p.left) < 2.4 && Math.abs(g.y - p.top) < 2.4);
      if (hit) {
        hit.items.push(p);
        hit.x = hit.items.reduce((a, i) => a + i.left, 0) / hit.items.length;
        hit.y = hit.items.reduce((a, i) => a + i.top, 0) / hit.items.length;
      } else {
        groups.push({ x: p.left, y: p.top, items: [p] });
      }
    }
    return groups.map((g) => {
      const online = g.items.filter((i) => i.s.status === 'online').length;
      const offline = g.items.filter((i) => i.s.status === 'offline').length;
      return {
        key: g.items.map((i) => i.s.id).join('-'),
        x: g.x,
        y: g.y,
        items: g.items,
        count: g.items.length,
        online,
        offline,
        status: online ? 'online' : offline ? 'offline' : 'unknown',
      };
    });
  })();

  /* 小图下方补一排地区数量，地图看不清时也能直接读 */
  const regionTop = (() => {
    const m = new Map();
    for (const p of pts) {
      const k = String(p.s.ip_region || '').trim() || countryCodeOf(p.s.ip_country) || '未知';
      m.set(k, (m.get(k) || 0) + 1);
    }
    return [...m.entries()].sort((a, b) => b[1] - a[1]).slice(0, 6);
  })();

  /* 量一下地图实际尺寸，标签按像素排布局（缩放到百分比会失真） */
  useEffect(() => {
    const el = mapRef.current;
    if (!el) return undefined;
    const measure = () => {
      const r = el.getBoundingClientRect();
      setBox({ w: Math.round(r.width) || 1000, h: Math.round(r.height) || 400 });
    };
    measure();
    let ro = null;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(measure);
      ro.observe(el);
    }
    window.addEventListener('resize', measure);
    return () => {
      if (ro) ro.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, [large]);

  const W = box.w || 1000;
  const H = box.h || 400;
  /* 内容百分比坐标 → 当前缩放/平移后的屏幕坐标（给标签、引线、悬停卡片用） */
  const toVX = (pct) => view.x + (Number(pct) / 100) * W * view.s;
  const toVY = (pct) => view.y + (Number(pct) / 100) * H * view.s;

  const clampView = (v) => {
    if (v.s <= 1.001) return { s: 1, x: 0, y: 0 };
    const minX = W - W * v.s;
    const minY = H - H * v.s;
    return { s: v.s, x: Math.min(0, Math.max(minX, v.x)), y: Math.min(0, Math.max(minY, v.y)) };
  };

  /* 滚轮缩放（React 的 onWheel 是 passive，必须挂原生监听才能 preventDefault） */
  useEffect(() => {
    const el = mapRef.current;
    if (!el || !zoomable) return undefined;
    const onWheel = (e) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      const cx = e.clientX - r.left;
      const cy = e.clientY - r.top;
      setView((v) => {
        const next = Math.min(4, Math.max(1, v.s * (e.deltaY > 0 ? 1 / 1.18 : 1.18)));
        if (next === 1) return { s: 1, x: 0, y: 0 };
        const k = next / v.s;
        return clampView({ s: next, x: cx - k * (cx - v.x), y: cy - k * (cy - v.y) });
      });
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [zoomable, W, H]);

  const zoomBy = (factor) =>
    setView((v) => {
      const next = Math.min(4, Math.max(1, v.s * factor));
      if (next === 1) return { s: 1, x: 0, y: 0 };
      const k = next / v.s;
      return clampView({ s: next, x: W / 2 - k * (W / 2 - v.x), y: H / 2 - k * (H / 2 - v.y) });
    });

  const onMouseDown = (e) => {
    if (!zoomable || view.s <= 1) return;
    dragRef.current = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y };
    movedRef.current = false;
  };
  const onMouseMove = (e) => {
    const d = dragRef.current;
    if (!d) return;
    const dx = e.clientX - d.x;
    const dy = e.clientY - d.y;
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) movedRef.current = true;
    setView(clampView({ s: view.s, x: d.vx + dx, y: d.vy + dy }));
  };
  const onMouseUp = () => {
    dragRef.current = null;
  };
  const onMapClick = () => {
    if (movedRef.current) {
      movedRef.current = false;
      return;
    }
    if (onExpand) onExpand();
  };

  /* 地区名直接贴在地图点位上（纯文字 + 白色描边），不画卡片、不画引线 */
  const placeLabels = (() => {
    if (!pts.length) return [];
    const groups = new Map();
    pts.forEach((p) => {
      const text =
        String(p.s.ip_region || '').trim() ||
        String(p.s.ip_country || '').trim().toUpperCase() ||
        '未知';
      const cur = groups.get(text) || { text, n: 0, left: 0, top: 0 };
      cur.n += 1;
      cur.left += p.left;
      cur.top += p.top;
      groups.set(text, cur);
    });
    const textWidth = (text) =>
      Array.from(text).reduce((w, ch) => w + (/[\u2e80-\u9fff]/.test(ch) ? 11 : 6), 0) + 4;
    const dots = pts.map((p) => ({ x: toVX(p.left), y: toVY(p.top) }));
    const placed = [];
    const items = [...groups.values()]
      .map((g) => ({ key: g.text, n: g.n, x: toVX(g.left / g.n), y: toVY(g.top / g.n) }))
      .filter((it) => it.x > -40 && it.x < W + 40 && it.y > -20 && it.y < H + 20)
      .sort((a, b) => a.x - b.x);

    items.forEach((it) => {
      const text = it.key;
      const w = Math.ceil(textWidth(text));
      const h = 14;
      const candidates = [
        [10, -16],
        [-w - 10, -16],
        [10, 6],
        [-w - 10, 6],
        [0, -24],
        [0, 16],
      ];
      let chosen = null;
      for (const [dx, dy] of candidates) {
        const x = Math.min(Math.max(2, it.x + dx), W - w - 2);
        const y = Math.min(Math.max(2, it.y + dy), H - h - 2);
        const hitsLabel = placed.some((p) => !(x + w < p.x || x > p.x + p.w || y + h < p.y || y > p.y + p.h));
        const hitsDot = dots.some((d) => d.x > x - 6 && d.x < x + w + 6 && d.y > y - 6 && d.y < y + h + 6);
        if (!hitsLabel && !hitsDot) {
          chosen = { x, y };
          break;
        }
      }
      if (!chosen) {
        /* 密集区域向上/向下找空行，尽量不和已有文字重叠 */
        for (let step = 1; step <= 10 && !chosen; step += 1) {
          for (const dir of [-1, 1]) {
            const x = Math.min(Math.max(2, it.x + (dir > 0 ? 10 : -w - 10)), W - w - 2);
            const y = Math.min(Math.max(2, it.y - 16 + dir * step * (h + 4)), H - h - 2);
            const hitsLabel = placed.some((p) => !(x + w < p.x || x > p.x + p.w || y + h < p.y || y > p.y + p.h));
            if (!hitsLabel) {
              chosen = { x, y };
              break;
            }
          }
        }
      }
      if (!chosen) {
        chosen = {
          x: Math.min(Math.max(2, it.x + 10), W - w - 2),
          y: Math.min(Math.max(2, it.y - 16), H - h - 2),
        };
      }
      placed.push({ key: it.key, text, x: chosen.x, y: chosen.y, w, h });
    });
    return placed;
  })();

  if (!servers.length) {
    return <Empty description="还没有服务器" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ padding: '40px 0' }} />;
  }

  return (
    <div>
      <div
        ref={mapRef}
        className={`pp-map${large ? ' pp-map-lg' : ''}${onExpand ? ' pp-map-clickable' : ''}${zoomable ? ' pp-map-zoomable' : ''}${view.s > 1 ? ' is-zoomed' : ''}`}
        onClick={onMapClick}
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={onMouseUp}
        onMouseLeave={onMouseUp}
      >
        <div
          className="pp-map-stage"
          style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.s})` }}
        >
          <img src="/assets/world.svg" alt="世界地图" className="pp-map-bg" draggable={false} />

          {!large ? (
            clusters.map((c) => (
              <button
                key={c.key}
                type="button"
                className={`pp-map-cluster ${c.status}${c.count === 1 ? ' is-single' : ''}`}
                style={{ left: `${c.x}%`, top: `${c.y}%` }}
                onMouseEnter={() => {
                  if (c.count === 1) {
                    setHover(c.items[0]);
                    setHoverCluster(null);
                  } else {
                    setHover(null);
                    setHoverCluster(c);
                  }
                }}
                onMouseLeave={() => {
                  setHover(null);
                  setHoverCluster(null);
                }}
                onClick={(e) => {
                  e.stopPropagation();
                  if (movedRef.current) return;
                  if (c.count > 1 && onExpand) onExpand();
                  else onPick(c.items[0].s);
                }}
                aria-label={c.count > 1 ? `${c.count} 台服务器` : c.items[0].s.name}
                title={c.count > 1 ? `${c.count} 台服务器，点击查看大图` : c.items[0].s.name}
              >
                {c.count > 1 ? c.count : ''}
              </button>
            ))
          ) : HAS_CANVAS ? (
            <>
              <div className="pp-map-scatter">
                <ScatterPlot
                  size={large ? 12 : 20}
                  points={pts.map((p) => ({
                    x: p.left * 10,
                    y: (100 - p.top) * 4,
                    status:
                      p.s.status === 'online' ? 'online' : p.s.status === 'offline' ? 'offline' : 'unknown',
                    name: p.s.name,
                  }))}
                />
              </div>
              {pts.map((p) => (
                <button
                  key={p.s.id}
                  type="button"
                  className="pp-map-hit"
                  style={{ left: `${p.left}%`, top: `${p.top}%` }}
                  onMouseEnter={() => setHover(p)}
                  onMouseLeave={() => setHover(null)}
                  onClick={(e) => {
                    e.stopPropagation();
                    if (movedRef.current) return;
                    onPick(p.s);
                  }}
                  aria-label={p.s.name}
                />
              ))}
            </>
          ) : (
            pts.map((p) => {
              const cls = p.s.status === 'online' ? 'online' : p.s.status === 'offline' ? 'offline' : 'unknown';
              return (
                <button
                  key={p.s.id}
                  type="button"
                  className={`pp-map-dot ${cls}`}
                  style={{ left: `${p.left}%`, top: `${p.top}%` }}
                  onMouseEnter={() => setHover(p)}
                  onMouseLeave={() => setHover(null)}
                  onClick={(e) => {
                    e.stopPropagation();
                    if (movedRef.current) return;
                    onPick(p.s);
                  }}
                  aria-label={p.s.name}
                />
              );
            })
          )}
        </div>

        {/* 悬停卡片放在缩放层外面：文字始终保持 11px，不会被放得很夸张 */}
        <div className="pp-map-overlay">
          {large ? placeLabels.map((l) => (
            <span key={l.key} className="pp-map-place" style={{ left: `${l.x}px`, top: `${l.y}px` }}>
              {l.text}
            </span>
          )) : null}
          {hoverCluster ? (
            <div
              className="pp-tooltip pp-map-tip"
              style={{
                left: `${Math.min(Math.max(toVX(hoverCluster.x), 110), W - 110)}px`,
                top: `${Math.min(Math.max(toVY(hoverCluster.y) - 8, 12), H - 12)}px`,
              }}
            >
              <div className="pp-tooltip-title">{hoverCluster.count} 台服务器</div>
              {hoverCluster.items.slice(0, 4).map((it) => (
                <div className="pp-tooltip-row" key={it.s.id}>
                  <i style={{ background: it.s.status === 'online' ? '#10b981' : it.s.status === 'offline' ? '#f43f5e' : '#cbd5e1' }} />
                  <span>{it.s.name}</span>
                  <b>{String(it.s.ip_region || '').trim() || countryCodeOf(it.s.ip_country) || '—'}</b>
                </div>
              ))}
              {hoverCluster.count > 4 ? (
                <div className="pp-tooltip-row"><span>还有 {hoverCluster.count - 4} 台…</span></div>
              ) : null}
              <div style={{ marginTop: 6, fontSize: 11, color: '#94a3b8' }}>点击查看大图</div>
            </div>
          ) : null}
          {hover ? (
            <div
              className="pp-tooltip pp-map-tip"
              style={{
                left: `${Math.min(Math.max(toVX(hover.left), 96), W - 96)}px`,
                top: `${Math.min(Math.max(toVY(hover.top) - 8, 12), H - 12)}px`,
              }}
            >
              <div className="pp-tooltip-title">{hover.s.name}</div>
              <div className="pp-tooltip-row">
                <i style={{ background: hover.s.status === 'online' ? '#10b981' : hover.s.status === 'offline' ? '#f43f5e' : '#cbd5e1' }} />
                <span>{hover.s.status === 'online' ? '在线' : hover.s.status === 'offline' ? '离线' : '未知'}</span>
                <b>{countryCodeOf(hover.s.ip_country) || '—'}</b>
              </div>
              <div className="pp-tooltip-row">
                <span>地区</span>
                <b>
                  {String(hover.s.ip_region || '').trim() ||
                    countryCodeOf(hover.s.ip_country) ||
                    '—'}
                </b>
              </div>
              <div className="pp-tooltip-row">
                <span>CPU</span>
                <b>{Math.round(Number(hover.s.cpu_percent) || 0)}%</b>
              </div>
              <div className="pp-tooltip-row">
                <span>内存</span>
                <b>{Math.round(Number(hover.s.mem_percent) || 0)}%</b>
              </div>
              {hover.approx ? (
                <div className="pp-tooltip-row" style={{ marginTop: 4 }}>
                  <span style={{ color: '#94a3b8', fontSize: 11.5 }}>按国家中心点估算位置</span>
                </div>
              ) : null}
            </div>
          ) : null}
        </div>

        {zoomable ? (
          <div
            className="pp-map-zoom"
            onClick={(e) => e.stopPropagation()}
            onMouseDown={(e) => e.stopPropagation()}
            onWheel={(e) => e.stopPropagation()}
          >
            <button type="button" onClick={() => zoomBy(1 / 1.35)} aria-label="缩小" title="缩小">
              <ZoomOutOutlined />
            </button>
            <span className="pp-map-zoom-val">{Math.round(view.s * 100)}%</span>
            <button type="button" onClick={() => zoomBy(1.35)} aria-label="放大" title="放大">
              <ZoomInOutlined />
            </button>
            <button
              type="button"
              onClick={() => setView({ s: 1, x: 0, y: 0 })}
              aria-label="重置缩放"
              title="重置"
            >
              <ReloadOutlined />
            </button>
          </div>
        ) : null}
      </div>

      <div style={{ marginTop: 10, fontSize: 12, color: '#94a3b8', lineHeight: 1.7 }}>
        已标出 {pts.length} 台
        {approxCount ? `（其中 ${approxCount} 台按国家中心点估算）` : ''}
        {unplaced.length ? ` · ${unplaced.length} 台无地区信息（${unplaced.map((s) => s.name || `#${s.id}`).join('、')}）` : ''}
        {pts.length ? (large ? ' · 悬停圆点查看信息 · 点击圆点查看详情' : ' · 圆点数字为台数，点击查看大图') : ''}
        {zoomable ? ' · 滚轮缩放 / 拖动平移' : ''}
        {onExpand ? ' · 点击地图查看大图' : ''}
      </div>
      {!large && regionTop.length ? (
        <div className="pp-map-regions">
          {regionTop.map(([k, c]) => (
            <span key={k} title={k}>{k}<b>{c}</b></span>
          ))}
        </div>
      ) : null}
    </div>
  );
}
