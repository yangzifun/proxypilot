/* =================================================================
 *  服务器（原「服务器管理」+「服务器监控」真正合并）
 *  · 卡片 / 表格两种布局，展示同一组字段，共享同一份多选
 *  · 批量动作只在页面工具条（选中后出现）
 *  · 单台动作全部收进「服务器详情」抽屉（ServerDrawer）
 * ================================================================= */
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  CheckCircleOutlined,
  CloudDownloadOutlined,
  CloudServerOutlined,
  DisconnectOutlined,
  FullscreenOutlined,
  PlusOutlined,
  PoweroffOutlined,
  QuestionCircleOutlined,
  ReloadOutlined,
  AppstoreOutlined,
  TableOutlined,
  SearchOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Checkbox,
  Col,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Progress,
  Row,
  Segmented,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiPost, getToken } from '../api';
import { useAsync, usePolling, readForm } from '../hooks';
import { CopyText, ErrorBlock, JsonView, PageCard, StatCard, StatusPill } from '../components';
import { watchBatch } from '../components/JobCenter';
import { WorldMap } from './Monitor';
import Globe3D from '../components/Globe3D';
import ServerDrawer from '../components/ServerDrawer';
import ServerUnlockModal from '../components/ServerUnlockModal';
import { ServerCard as FullServerCard } from './Monitor';
import { fmtAgo, fmtBytes, fmtNum, fmtTime, fmtUptime, levelColor, splitIps } from '../format';
import { chartPalette, getTheme, setTheme, subscribeTheme, THEME_OPTIONS } from '../theme';

const VIEW_KEY = 'pp.servers.view';

const readView = () => {
  try {
    return localStorage.getItem(VIEW_KEY) === 'table' ? 'table' : 'cards';
  } catch (e) {
    return 'cards';
  }
};

const STATUS_OPTIONS = [
  { label: '全部节点', value: 'all' },
  { label: '在线', value: 'online' },
  { label: '离线', value: 'offline' },
  { label: '未知', value: 'unknown' },
];

const toNum = (v) => (v === null || v === undefined || v === '' ? null : Number(v));

/** servers/status 的 metrics（旧版字段 fallback 到列表里的 last_metrics） */
function realtimeOf(rt, row) {
  const m = (rt && rt.metrics) || row.last_metrics || {};
  return {
    status: (rt && rt.status) || row.last_status || 'unknown',
    cpu: toNum(m.cpuPercent),
    mem: toNum(m.memPercent),
    disk: toNum(m.diskPercent),
    net: m.net || {},
    conn: m.conn || {},
    proc: m.proc || {},
    load1: toNum(m.load1),
    ssh: m.ssh || null,
    agent_version: (rt && rt.agent_version) || row.agent_version || '',
    last_seen: (rt && rt.agent_last_seen) || row.agent_last_seen || 0,
    last_ip4: (rt && rt.last_ip4) || row.last_ip4 || '',
    last_ip6: (rt && rt.last_ip6) || row.last_ip6 || '',
  };
}

function mergeServer(row, rt) {
  const live = realtimeOf(rt, row);
  return {
    ...row,
    ...live,
    last_ip4: live.last_ip4,
    last_ip6: live.last_ip6,
    agent_last_seen: live.last_seen,
    agent_version: live.agent_version,
  };
}

const isLocal = (row) => row.link_mode === 'local';

const geoText = (row) =>
  [String(row.ip_country || '').trim(), String(row.ip_region || '').trim()].filter(Boolean).join(' ') || '—';

const pctText = (v) => (v === null || v === undefined || !Number.isFinite(Number(v)) ? '—' : `${Math.round(Number(v))}%`);

const netText = (row) => {
  const net = row.net || {};
  if (net.rxRate == null && net.txRate == null) return '—';
  return `↓ ${fmtBytes(net.rxRate)}/s ↑ ${fmtBytes(net.txRate)}/s`;
};

const connText = (row) => (row.conn && row.conn.total != null ? fmtNum(row.conn.total) : '—');
const procText = (row) => (row.proc && row.proc.total != null ? fmtNum(row.proc.total) : '—');
const loadText = (row) => (row.load1 == null ? '—' : Number(row.load1).toFixed(2));

function MiniRing({ value, label }) {
  const pct = Math.max(0, Math.min(100, Math.round(Number(value) || 0)));
  const color = pct >= 90 ? '#f43f5e' : pct >= 75 ? '#f59e0b' : pct >= 50 ? '#0ea5e9' : '#10b981';
  return (
    <div className="pp-ov-ring">
      <Progress
        type="circle"
        size={92}
        percent={pct}
        strokeWidth={7}
        strokeColor={color}
        format={() => <span style={{ fontSize: 14, fontWeight: 600 }}>{pct}%</span>}
      />
      <div className="pp-ov-ring-label">{label}</div>
    </div>
  );
}

function Pct({ value }) {
  const v = toNum(value);
  if (v === null || !Number.isFinite(v)) return <span className="pp-muted">—</span>;
  return (
    <span style={{ color: levelColor(v), fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>
      {Math.round(v)}%
    </span>
  );
}

function IpText({ row }) {
  const { v4, v6 } = splitIps(row);
  if (!v4 && !v6) {
    return <span className="pp-mono" style={{ color: '#334155', fontSize: 12 }}>{row.host || '—'}</span>;
  }
  return (
    <div className="pp-mono" style={{ fontSize: 12, lineHeight: 1.5, color: '#334155' }}>
      {v4 ? <div>{v4}</div> : null}
      {v6 ? <div style={{ color: '#64748b' }}>{v6}</div> : null}
    </div>
  );
}

function AgentVersion({ row }) {
  if (isLocal(row)) {
    return <Tag color="purple" style={{ marginInlineEnd: 0 }}>本地直控</Tag>;
  }
  return row.agent_version ? (
    <Tag color="blue" style={{ marginInlineEnd: 0 }}>{row.agent_version}</Tag>
  ) : (
    <span className="pp-muted">未上报</span>
  );
}

function Heartbeat({ row }) {
  return (
    <Tooltip title={row.last_seen ? fmtTime(row.last_seen) : 'Agent 尚未上报心跳'}>
      <span className="pp-muted pp-nowrap">{row.last_seen ? fmtAgo(row.last_seen) : '—'}</span>
    </Tooltip>
  );
}

function NodeConn({ node }) {
  if (!node) return <span className="pp-muted">—</span>;
  if (node.stale) {
    return (
      <span className="pp-nowrap">
        <span className="pp-muted">—</span>
        <Tag style={{ marginInlineStart: 6 }}>未上报</Tag>
      </span>
    );
  }
  return (
    <span className="pp-nowrap">
      {fmtNum(node.conn_count)}
      {node.error ? <Tag color="orange" style={{ marginInlineStart: 6 }}>采集异常</Tag> : null}
    </span>
  );
}

/* ---------------- 国旗 ---------------- */

function countryCodeOf(v) {
  const s = String(v || '').trim();
  if (!s) return '';
  const CN2ISO = { 中国: 'CN', 美国: 'US', 香港: 'HK', 台湾: 'TW', 日本: 'JP', 韩国: 'KR', 新加坡: 'SG', 马来西亚: 'MY', 印度尼西亚: 'ID', 印度: 'IN', 德国: 'DE', 法国: 'FR', 英国: 'GB', 荷兰: 'NL', 加拿大: 'CA', 澳大利亚: 'AU', 俄罗斯: 'RU', 泰国: 'TH', 越南: 'VN', 菲律宾: 'PH', 土耳其: 'TR', 巴西: 'BR', 阿联酋: 'AE' };
  if (CN2ISO[s]) return CN2ISO[s];
  const cnHit = Object.keys(CN2ISO).find((k) => s.includes(k));
  if (cnHit) return CN2ISO[cnHit];
  if (/^[A-Za-z]{2}$/.test(s)) return s.toUpperCase();
  const cps = Array.from(s);
  const isRi = (cp) => cp >= 0x1f1e6 && cp <= 0x1f1ff;
  for (let i = 0; i + 1 < cps.length; i += 1) {
    const a = cps[i].codePointAt(0);
    const b = cps[i + 1].codePointAt(0);
    if (isRi(a) && isRi(b)) return String.fromCharCode(a - 0x1f1e6 + 65, b - 0x1f1e6 + 65);
  }
  return s.toUpperCase().slice(0, 2);
}

function CountryFlag({ code }) {
  const cc = String(code || '').trim().toUpperCase();
  const [failed, setFailed] = useState(false);
  if (!/^[A-Z]{2}$/.test(cc)) return null;
  if (failed) return <span className="pp-cc">{cc}</span>;
  return (
    <img
      className="pp-flag"
      src={`/api/flag/${cc.toLowerCase()}`}
      alt={cc}
      title={cc}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}

function Region({ row }) {
  return (
    <span className="pp-srv-geo">
      <CountryFlag code={countryCodeOf(row.ip_country)} />
      <span>{geoText(row)}</span>
    </span>
  );
}

/* ---------------- 卡片 ---------------- */

function RingGauge({ percent, label }) {
  const has = percent !== null && percent !== undefined && Number.isFinite(Number(percent));
  const v = has ? Math.max(0, Math.min(100, Math.round(Number(percent)))) : 0;
  return (
    <div className="pp-srv-ring">
      <Progress
        type="circle"
        size={58}
        percent={v}
        strokeWidth={7}
        strokeColor={has ? levelColor(v) : '#e2e8f0'}
        format={() => <span style={{ fontSize: 13, fontWeight: 600 }}>{has ? `${v}%` : '—'}</span>}
      />
      <div className="pp-srv-ring-label">{label}</div>
    </div>
  );
}

function ServerCard({ row, node, checked, onCheck, onOpen }) {
  const dot = row.status === 'online' ? 'online' : row.status === 'offline' ? 'offline' : 'unknown';
  return (
    <div
      className={`pp-srv-card${row.status === 'offline' ? ' is-down' : ''}${row.status !== 'online' && row.status !== 'offline' ? ' is-offline' : ''}`}
      role="button"
      tabIndex={0}
      onClick={() => onOpen(row)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') onOpen(row);
      }}
    >
      <div className="pp-srv-head">
        <span className="pp-srv-name">
          <span
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => e.stopPropagation()}
            style={{ display: 'inline-flex', alignItems: 'center' }}
          >
            <Checkbox
              checked={checked}
              onChange={(e) => onCheck(row.id, e.target.checked)}
              aria-label={`选择 ${row.name || '#' + row.id}`}
            />
          </span>
          <i className={`pp-dot ${dot}`} />
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{row.name || `#${row.id}`}</span>
          {isLocal(row) ? <Tag color="purple" style={{ marginInlineEnd: 0 }}>本地</Tag> : null}
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, flex: '0 0 auto' }}>
          <Tooltip title={row.last_error || undefined}>
            <span>
              <StatusPill status={row.status} />
            </span>
          </Tooltip>
          <span className="pp-srv-link">详情 ›</span>
        </span>
      </div>

      <div className="pp-srv-sub">
        <Region row={row} />
        <IpText row={row} />
      </div>

      <div className="pp-srv-gauges">
        <RingGauge percent={row.cpu} label="CPU" />
        <RingGauge percent={row.mem} label="内存" />
        <RingGauge percent={row.disk} label="磁盘" />
      </div>

      <div className="pp-srv-kv">
        <div>
          <span>实时网络 ↓ / ↑</span>
          <span>{netText(row)}</span>
        </div>
        <div>
          <span>连接数</span>
          <span>{connText(row)}</span>
        </div>
        <div>
          <span>进程数</span>
          <span>{procText(row)}</span>
        </div>
        <div>
          <span>负载 1</span>
          <span>{loadText(row)}</span>
        </div>
        <div>
          <span>Agent 版本</span>
          <span><AgentVersion row={row} /></span>
        </div>
        <div>
          <span>最后心跳</span>
          <span><Heartbeat row={row} /></span>
        </div>
        <div>
          <span>节点连接数</span>
          <span><NodeConn node={node} /></span>
        </div>
      </div>
    </div>
  );
}

/* ---------------- 页面 ---------------- */

export default function ServersUnified() {
  const { message } = AntdApp.useApp();
  const [view, setView] = useState(readView);
  const [q, setQ] = useState('');
  const [mapOpen, setMapOpen] = useState(false);
  const [status, setStatus] = useState('');
  const [auto, setAuto] = useState(true);
  const [pageTheme, setPageTheme] = useState(() => getTheme());
  useEffect(() => subscribeTheme((t) => setPageTheme(t)), []);
  const changeTheme = (v) => { setTheme(v); };
  const [sseStatus, setSseStatus] = useState({});
  const [, forceTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => forceTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);
  const [selected, setSelected] = useState([]);
  const [detailId, setDetailId] = useState(null);
  const [compose, setCompose] = useState(false);
  const [saving, setSaving] = useState(false);
  const [created, setCreated] = useState(null);
  const [checking, setChecking] = useState(false);
  const [pushing, setPushing] = useState(false);
  const [updatedAt, setUpdatedAt] = useState(Date.now());
  const [form] = Form.useForm();
  const [unlockFor, setUnlockFor] = useState(null);

  /* 管理字段（名称 / 地区 / Agent / 心跳 …） */
  const list = useAsync(() => api('servers', { query: { q, status } }), [q, status]);
  /* 实时监控字段（CPU / 内存 / 磁盘 / 网络 / 磁盘 IO / 连接 / 进程 / 负载 / SSH） */
  const live = useAsync(() => api('servers/status'), []);
  /* 完整监控字段（机型 / 系统 / 运行时长 / 累计网络 / 磁盘 IO / 节点统计） */
  const monitor = useAsync(() => api('servers/monitor'), []);
  const traffic = useAsync(() => api('servers/traffic-limits'), []);
  const trafficMap = useMemo(() => {
    const m = {};
    for (const x of (traffic.data && traffic.data.data) || []) m[x.server_id] = x;
    return m;
  }, [traffic.data]);
  /* 节点连接数（按 server_id 聚合） */
  const nodes = useAsync(() => api('stats/traffic/nodes'), []);

  const refreshLive = useCallback(
    () =>
      live
        .reload()
        .then(() => setUpdatedAt(Date.now()))
        .catch(() => {}),
    [live.reload]
  );

  useEffect(() => {
    if (!auto) return undefined;
    const token = getToken();
    if (!token) return undefined;
    let es;
    try {
      es = new EventSource(`/api/servers/stream?token=${encodeURIComponent(token)}`);
      es.onmessage = (ev) => {
        try {
          const arr = JSON.parse(ev.data);
          if (Array.isArray(arr)) {
            const m = {};
            for (const it of arr) m[it.id] = it;
            setSseStatus(m);
          }
        } catch (e) { /* ignore */ }
      };
      es.onerror = () => {};
    } catch (e) {
      es = null;
    }
    return () => { if (es) es.close(); };
  }, [auto]);
  usePolling(() => refreshLive(), auto ? 2000 : 0);
  usePolling(() => monitor.reload().catch(() => {}), auto ? 2000 : 0);
  usePolling(() => nodes.reload().catch(() => {}), auto ? 15000 : 0);

  const liveById = useMemo(() => {
    const map = new Map();
    ((live.data && live.data.data) || []).forEach((x) => map.set(Number(x.id), x));
    for (const [id, v] of Object.entries(sseStatus || {})) map.set(Number(id), v);
    return map;
  }, [live.data, sseStatus]);

  const monitorById = useMemo(() => {
    const map = new Map();
    ((monitor.data && monitor.data.servers) || []).forEach((x) => map.set(Number(x.id), x));
    return map;
  }, [monitor.data]);

  const nodeById = useMemo(() => {
    const map = new Map();
    ((nodes.data && nodes.data.data) || []).forEach((n) => {
      const sid = Number(n.server_id) || 0;
      if (!sid) return;
      const cur = map.get(sid) || { conn_count: 0, down_rate: 0, up_rate: 0, down_total: 0, up_total: 0, error: false, stale: true };
      cur.conn_count += Number(n.conn_count) || 0;
      cur.down_rate += Number(n.down_rate) || 0;
      cur.up_rate += Number(n.up_rate) || 0;
      cur.down_total += Number(n.down_total) || 0;
      cur.up_total += Number(n.up_total) || 0;
      if (n.error) cur.error = true;
      if (n.stale === false) cur.stale = false;
      map.set(sid, cur);
    });
    return map;
  }, [nodes.data]);

  const rows = useMemo(() => {
    const listRows = (list.data && list.data.data) || [];
    return listRows
      .map((s) => mergeServer(s, liveById.get(Number(s.id))))
      .map((r) => ({ ...r, ...(monitorById.get(Number(r.id)) || {}) }))
      .filter((r) => !status || r.status === status);
  }, [list.data, liveById, status]);

  /* 服务器被删除 / 被筛选掉后，清掉失效的选中项 */
  useEffect(() => {
    setSelected((prev) => {
      const alive = new Set(rows.map((r) => Number(r.id)));
      const next = prev.filter((id) => alive.has(Number(id)));
      return next.length === prev.length ? prev : next;
    });
  }, [rows]);

  const counts = useMemo(() => {
    const c = { online: 0, offline: 0, unknown: 0 };
    rows.forEach((r) => {
      if (r.status === 'online') c.online += 1;
      else if (r.status === 'offline') c.offline += 1;
      else c.unknown += 1;
    });
    return c;
  }, [rows]);

  const avg = useMemo(() => {
    const live = rows.filter((r) => r.status === 'online');
    const pick = live.length ? live : rows;
    if (!pick.length) return { cpu: 0, mem: 0, disk: 0 };
    const sum = (k) => pick.reduce((acc, r) => acc + (Number(r[k]) || 0), 0) / pick.length;
    return { cpu: sum('cpu'), mem: sum('mem'), disk: sum('disk') };
  }, [rows]);

  const summary = (monitor.data && monitor.data.summary) || {};

  const changeView = (v) => {
    setView(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch (e) {
      /* ignore */
    }
  };

  const refreshAll = () => {
    setUpdatedAt(Date.now());
    list.reload().catch(() => {});
    refreshLive();
    nodes.reload().catch(() => {});
  };

  const toggleOne = (id, checked) => {
    setSelected((prev) => {
      const key = Number(id);
      if (checked) return prev.some((x) => Number(x) === key) ? prev : [...prev, id];
      return prev.filter((x) => Number(x) !== key);
    });
  };

  /* ---------------- 批量动作（工具条） ---------------- */

  const batchProbe = async () => {
    if (!selected.length) return;
    setChecking(true);
    let ok = 0;
    for (const id of selected) {
      try {
        await apiPost(`servers/${id}/test`, {});
        ok += 1;
      } catch (e) {
        /* 单台失败不影响其它 */
      }
    }
    setChecking(false);
    message.success(`已发起 ${ok}/${selected.length} 台探测`);
    refreshLive();
  };

  const batchPushAgent = async () => {
    setPushing(true);
    try {
      const r = await api('agent/push-update', { method: 'POST', body: { server_ids: selected } });
      if (watchBatch(r)) {
        message.success(`已创建后台更新任务 #${r.job_id}（${r.total || selected.length} 台），进度见右上角「任务」`);
        setSelected([]);
        return;
      }
      const results = (r && r.results) || [];
      const ok = results.filter((x) => x.ok && !x.skipped);
      const skipped = results.filter((x) => x.skipped);
      const bad = results.filter((x) => !x.ok);
      let msg = `已排队 ${ok.length} 台${r && r.latest ? `（最新 ${r.latest}）` : ''}`;
      if (skipped.length) msg += `，${skipped.length} 台已是最新`;
      if (bad.length) msg += `，失败：${bad.map((x) => `${x.name}（${x.error || '未知错误'}）`).join('，')}`;
      if (bad.length) message.error(msg);
      else message.success(msg);
      setSelected([]);
      list.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '推送失败');
    } finally {
      setPushing(false);
    }
  };

  const batchSsh = async (enable) => {
    if (!selected.length) return;
    try {
      const res = await apiPost('jobs', {
        kind: 'ssh.toggle',
        params: { server_ids: selected, enable: !!enable },
      });
      if (watchBatch(res)) {
        message.success(`已创建后台任务 #${res.job_id}（${selected.length} 台，${enable ? '启用' : '停用'} SSH）`);
      } else {
        message.success((res && res.message) || `已下发 ${enable ? '启用' : '停用'} SSH`);
      }
      setSelected([]);
      setTimeout(() => refreshLive(), 1500);
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const batchReboot = async () => {
    if (!selected.length) return;
    let ok = 0;
    for (const id of selected) {
      try {
        await apiPost(`servers/${id}/reboot`, {});
        ok += 1;
      } catch (e) {
        /* 单台失败不影响其它 */
      }
    }
    if (ok) message.success(`已向 ${ok}/${selected.length} 台服务器下发重启指令`);
    else message.error('重启指令下发失败');
    setSelected([]);
    setTimeout(() => refreshLive(), 2000);
  };

  /* ---------------- 添加服务器 ---------------- */

  const create = async () => {
    const values = await readForm(form);
    if (!values) return;
    setSaving(true);
    try {
      const res = await api('servers', { method: 'POST', body: values });
      message.success('服务器已创建，请复制安装命令到目标机器执行');
      setCompose(false);
      form.resetFields();
      setCreated({
        title: `服务器「${values.name}」的接入命令`,
        command: res.install_command,
        token: res.agent_token,
        note: res.message,
      });
      list.reload().catch(() => {});
      refreshLive();
    } catch (e) {
      message.error(e.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  const allChecked = rows.length > 0 && selected.length === rows.length;
  const someChecked = selected.length > 0 && selected.length < rows.length;

  /* ---------------- 表格列（与卡片字段一一对应） ---------------- */

  const histRef = useRef({ rx: [], tx: [], sum: [] });
  const [, setHistTick] = useState(0);
  useEffect(() => {
    const rx = Number(summary.rx_rate) || 0;
    const tx = Number(summary.tx_rate) || 0;
    const h = histRef.current;
    h.rx = [...h.rx.slice(-23), rx];
    h.tx = [...h.tx.slice(-23), tx];
    h.sum = [...h.sum.slice(-23), rx + tx];
    setHistTick((n) => n + 1);
  }, [summary.rx_rate, summary.tx_rate]);

  const columns = [
    {
      title: '流量剩余',
      width: 150,
      render: (_, row) => {
        const x = trafficMap[row.id];
        if (!x || !x.enabled || Number(x.limit_bytes) <= 0) return <span className="pp-muted">未设置</span>;
        const used = Number(x.mode === 'rx' ? x.rx_bytes : x.mode === 'tx' ? x.tx_bytes : Number(x.rx_bytes) + Number(x.tx_bytes));
        const limit = Number(x.limit_bytes);
        const left = Math.max(0, limit - used);
        const pct = (left * 100) / limit;
        const color = pct <= 10 ? '#f43f5e' : pct <= 20 ? '#f59e0b' : '#16a34a';
        return (
          <div>
            <div style={{ fontSize: 12, color, fontWeight: 600 }}>{fmtBytes(left)}（{pct.toFixed(1)}%）</div>
            <div className="pp-muted" style={{ fontSize: 11 }}>已用 {fmtBytes(used)} / {fmtBytes(limit)}</div>
          </div>
        );
      },
    },
    {
      title: '名称',
      dataIndex: 'name',
      width: 190,
      fixed: 'left',
      render: (v, row) => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontWeight: 600, color: '#0f172a' }}>
          {v || `#${row.id}`}
          {isLocal(row) ? <Tag color="purple" style={{ marginInlineEnd: 0 }}>本地</Tag> : null}
        </span>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 96,
      render: (v, row) => (
        <Tooltip title={row.last_error || undefined}>
          <span>
            <StatusPill status={v} />
          </span>
        </Tooltip>
      ),
    },
    {
      title: '地区',
      width: 150,
      render: (_, row) => <Region row={row} />,
    },
    {
      title: 'IP / 主机',
      width: 192,
      render: (_, row) => <IpText row={row} />,
    },
    {
      title: 'CPU',
      dataIndex: 'cpu',
      width: 82,
      align: 'center',
      render: (v) => <Pct value={v} />,
    },
    {
      title: '内存',
      dataIndex: 'mem',
      width: 82,
      align: 'center',
      render: (v) => <Pct value={v} />,
    },
    {
      title: '磁盘',
      dataIndex: 'disk',
      width: 82,
      align: 'center',
      render: (v) => <Pct value={v} />,
    },
    {
      title: '实时网络 ↓ / ↑',
      width: 186,
      render: (_, row) => <span className="pp-mono pp-nowrap" style={{ fontSize: 12 }}>{netText(row)}</span>,
    },
    {
      title: '累计网络 ↓ / ↑',
      width: 170,
      render: (_, row) => {
        const net = row.net || {};
        const rx = net.rxBytes ?? net.rx_bytes;
        const tx = net.txBytes ?? net.tx_bytes;
        return <span className="pp-mono pp-nowrap" style={{ fontSize: 12 }}>↓ {fmtBytes(rx)} ↑ {fmtBytes(tx)}</span>;
      },
    },
    {
      title: '磁盘实时 读 / 写',
      width: 160,
      render: (_, row) => {
        const io = row.disk_io || {};
        return <span className="pp-mono pp-nowrap" style={{ fontSize: 12 }}>{fmtBytes(io.readRate)}/s / {fmtBytes(io.writeRate)}/s</span>;
      },
    },
    {
      title: '磁盘累计 读 / 写',
      width: 170,
      render: (_, row) => {
        const io = row.disk_io || {};
        return <span className="pp-mono pp-nowrap" style={{ fontSize: 12 }}>{fmtBytes(io.readBytes ?? io.read_bytes)} / {fmtBytes(io.writeBytes ?? io.write_bytes)}</span>;
      },
    },
    {
      title: '连接数',
      width: 86,
      align: 'center',
      render: (_, row) => <span className="pp-nowrap">{connText(row)}</span>,
    },
    {
      title: '进程数',
      width: 86,
      align: 'center',
      render: (_, row) => <span className="pp-nowrap">{procText(row)}</span>,
    },
    {
      title: '负载 1 / 5 / 15',
      width: 132,
      align: 'center',
      render: (_, row) => (
        <span className="pp-nowrap" style={{ fontVariantNumeric: 'tabular-nums', fontSize: 12 }}>
          {Number(row.load1 || 0).toFixed(2)} / {Number(row.load5 || 0).toFixed(2)} / {Number(row.load15 || 0).toFixed(2)}
        </span>
      ),
    },
    {
      title: '运行时长',
      width: 110,
      render: (_, row) => <span className="pp-muted pp-nowrap">{fmtUptime(Number(row.uptime_sec || 0))}</span>,
    },
    {
      title: 'Agent 版本',
      width: 128,
      render: (_, row) => <AgentVersion row={row} />,
    },
    {
      title: '最后心跳',
      width: 118,
      render: (_, row) => <Heartbeat row={row} />,
    },
    {
      title: '节点连接数',
      width: 118,
      align: 'center',
      render: (_, row) => <NodeConn node={nodeById.get(Number(row.id))} />,
    },
    {
      title: '节点实时 ↓ / ↑',
      width: 150,
      render: (_, row) => {
        const n = nodeById.get(Number(row.id));
        return <span className="pp-mono pp-nowrap" style={{ fontSize: 12 }}>{fmtBytes(n && n.down_rate)}/s / {fmtBytes(n && n.up_rate)}/s</span>;
      },
    },
    {
      title: '节点累计 ↓ / ↑',
      width: 160,
      render: (_, row) => {
        const n = nodeById.get(Number(row.id));
        return <span className="pp-mono pp-nowrap" style={{ fontSize: 12 }}>{fmtBytes(n && n.down_total)} / {fmtBytes(n && n.up_total)}</span>;
      },
    },
    {
      title: '详情',
      width: 72,
      fixed: 'right',
      render: (_, row) => (
        <Button type="link" size="small" onClick={() => setDetailId(row.id)}>
          详情
        </Button>
      ),
    },
  ];

  return (
    <div className={'pp-servers-theme-' + pageTheme}>
      {list.error ? (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message="监控服务错误"
          description={'连接服务器失败，请检查网络后重试：' + ((list.error && list.error.message) || '')}
        />
      ) : null}
      <PageCard
        title={<span className="pp-section-title">服务器概览</span>}
        extra={<span className="pp-muted" style={{ fontSize: 12 }}>更新 {fmtTime(updatedAt, '').slice(11, 19) || '—'}</span>}
        bodyStyle={{ padding: 0 }}
      >
        <div className="pp-overview-grid">
          <div className="pp-ov-top">
              <div className="pp-ov-tiles">
                {[
                  { label: '在线', value: fmtNum(counts.online), hint: rows.length ? `在线率 ${((counts.online / rows.length) * 100).toFixed(0)}% · 共 ${rows.length} 台` : '—', chart: 'online' },
                  { label: '离线', value: fmtNum(counts.offline), chart: 'offline' },
                  { label: '未接入', value: fmtNum(counts.unknown), hint: 'Agent 未上报心跳', chart: 'unknown' },
                  { label: '实时下行', value: fmtBytes(summary.rx_rate || 0), unit: '/s', chart: 'rx' },
                  { label: '实时上行', value: fmtBytes(summary.tx_rate || 0), unit: '/s', chart: 'tx' },
                  { label: '集群总流量', value: fmtBytes((summary.rx_rate || 0) + (summary.tx_rate || 0)), unit: '/s', hint: `下行 + 上行 · ${rows.length} 台服务器`, chart: 'sum' },
                ].map((it) => (
                  <div key={it.label} className="pp-ov-tile">
                    <div className="pp-ov-label">{it.label}</div>
                    <div className="pp-ov-value">
                      {it.value}
                      {it.unit ? <span className="pp-ov-unit">{it.unit}</span> : null}
                    </div>
                    {renderOvChart(it.chart, {
                      online: counts.online,
                      offline: counts.offline,
                      unknown: counts.unknown,
                      total: rows.length,
                      hist: histRef.current,
                    })}
                    {it.hint ? <div className="pp-ov-hint">{it.hint}</div> : null}
                  </div>
                ))}
              </div>
  
              <div className="pp-ov-foot">
                服务异常 <b>{fmtNum(summary.failed_services || 0)}</b> · 未安装 {fmtNum(summary.not_installed || 0)} 项
              </div>
          </div>

          <div className="pp-ov-bottom">
            <div className="pp-ov-res">
              <div className="pp-ov-sec-head">
                <b>资源均值</b>
                <span>仅在线节点</span>
              </div>
              <div className="pp-ov-avg">
                <div className="pp-ov-gauges">
                  <div className="pp-ov-gauge"><MiniRing value={avg.cpu} label="CPU" /></div>
                  <div className="pp-ov-gauge"><MiniRing value={avg.mem} label="内存" /></div>
                </div>
                <div className="pp-ov-disk">
                  <div className="pp-ov-disk-head">
                    <span>磁盘均值</span>
                    <b style={{ color: levelColor(avg.disk) }}>{Number(avg.disk || 0).toFixed(0)}%</b>
                  </div>
                  <Progress percent={Math.round(Number(avg.disk || 0))} showInfo={false} strokeColor={levelColor(avg.disk)} />
                </div>
              </div>
            </div>

            <div className="pp-ov-map">
              <div className="pp-ov-sec-head">
                <b>全球分布</b>
                <span className="pp-ov-map-extra">
                  {[
                    { label: '在线', color: '#10b981' },
                    { label: '离线', color: '#f43f5e' },
                    { label: '未知', color: '#cbd5e1' },
                  ].map((it) => (
                    <span key={it.label} style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                      <i
                        style={{
                          display: 'inline-block',
                          width: 8,
                          height: 8,
                          borderRadius: '50%',
                          background: it.color,
                          boxShadow: '0 0 0 1px rgba(15, 23, 42, 0.06)',
                        }}
                      />
                      {it.label}
                    </span>
                  ))}
                  <Button size="small" type="link" icon={<FullscreenOutlined />} onClick={() => setMapOpen(true)}>大屏</Button>
                </span>
              </div>
              <div className="pp-ov-map-box">
                <Globe3D servers={rows} onPick={(r) => setDetailId(r.id)} height="100%" />
              </div>
            </div>
          </div>
        </div>
      </PageCard>

      <Modal
        title="全球分布 · 大屏"
        open={mapOpen}
        onCancel={() => setMapOpen(false)}
        footer={null}
        width="96vw"
        style={{ top: 16 }}
        styles={{ body: { height: '84vh', padding: 8 } }}
        destroyOnHidden
      >
        <div className="pp-ov-map-box" style={{ height: '100%' }}>
          <WorldMap large zoomable servers={rows} onPick={(r) => { setMapOpen(false); setDetailId(r.id); }} />
        </div>
      </Modal>


      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">服务器列表</span>}
          extra={
            <span className="pp-muted" style={{ fontSize: 12 }}>
              共 {rows.length} 台 · 更新 {fmtTime(updatedAt, '').slice(11, 19) || '—'}
            </span>
          }
          bodyStyle={{ padding: 0 }}
        >
          <div style={{ padding: '16px 18px 0' }}>
            <div className="pp-toolbar">
              <Input
                allowClear
                prefix={<SearchOutlined style={{ color: '#94a3b8' }} />}
                placeholder="搜索名称 / IP / 标签"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                style={{ width: 240 }}
              />
              <Segmented
                value={status || 'all'}
                onChange={(v) => setStatus(v === 'all' ? '' : v)}
                options={STATUS_OPTIONS}
              />
              <Checkbox
                checked={allChecked}
                indeterminate={someChecked}
                onChange={(e) => setSelected(e.target.checked ? rows.map((r) => r.id) : [])}
              >
                全选
              </Checkbox>
              <div className="pp-toolbar-right">
                <Space size={8} wrap>
                  <Segmented
                    size="small"
                    value={pageTheme}
                    onChange={changeTheme}
                    options={THEME_OPTIONS}
                  />
                  <Tooltip title="每 5 秒只刷新监控数据（资源 / 网络 / 连接），不重新拉取列表">
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
                      <Switch size="small" checked={auto} onChange={setAuto} />
                      实时推送
                    </span>
                  </Tooltip>
                  <Button icon={<ReloadOutlined />} onClick={refreshAll} loading={list.loading}>
                    刷新
                  </Button>
                  <Segmented
                    value={view}
                    onChange={changeView}
                    options={[
                      { label: <Tooltip title="卡片视图"><AppstoreOutlined /></Tooltip>, value: 'cards' },
                      { label: <Tooltip title="表格视图"><TableOutlined /></Tooltip>, value: 'table' },
                    ]}
                  />
                  <Button type="primary" icon={<PlusOutlined />} onClick={() => setCompose(true)}>
                    添加服务器
                  </Button>
                </Space>
              </div>
            </div>

            {selected.length ? (
              <div
                className="pp-toolbar"
                style={{
                  marginTop: 4,
                  padding: '8px 10px',
                  border: '1px solid #eef2f7',
                  borderRadius: 10,
                  background: '#f8fafc',
                }}
              >
                <span style={{ fontSize: 13 }}>
                  已选 <b>{selected.length}</b> 台
                </span>
                <Button icon={<SyncOutlined />} loading={checking} onClick={batchProbe}>
                  探测
                </Button>
                <Popconfirm
                  title={`向选中的 ${selected.length} 台推送 Agent 更新？`}
                  okText="推送"
                  cancelText="取消"
                  onConfirm={batchPushAgent}
                >
                  <Button icon={<CloudDownloadOutlined />} loading={pushing}>
                    更新 Agent
                  </Button>
                </Popconfirm>
                <Popconfirm title={`启用选中 ${selected.length} 台的 SSH？`} onConfirm={() => batchSsh(true)}>
                  <Button>启用 SSH</Button>
                </Popconfirm>
                <Popconfirm
                  title={`停用选中 ${selected.length} 台的 SSH？`}
                  description="确认 Agent 在线；停用后只能从面板再启用"
                  okText="停用"
                  okButtonProps={{ danger: true }}
                  onConfirm={() => batchSsh(false)}
                >
                  <Button danger>停用 SSH</Button>
                </Popconfirm>
                <Popconfirm
                  title={`重启选中的 ${selected.length} 台服务器？`}
                  description="重启期间服务器会离线，Agent 与代理连接会短暂中断。"
                  okText="重启"
                  okButtonProps={{ danger: true }}
                  onConfirm={batchReboot}
                >
                  <Button danger icon={<PoweroffOutlined />}>
                    重启服务器
                  </Button>
                </Popconfirm>
                <Button type="link" onClick={() => setSelected([])}>
                  取消选择
                </Button>
                <div className="pp-toolbar-right">
                  <span className="pp-muted" style={{ fontSize: 12 }}>
                    批量动作
                  </span>
                </div>
              </div>
            ) : null}
          </div>

          {list.error ? (
            <div style={{ padding: 18 }}>
              <ErrorBlock error={list.error} onRetry={refreshAll} />
            </div>
          ) : null}

          {view === 'cards' ? (
            <div style={{ padding: '16px 18px 18px' }}>
              {rows.length ? (
                <Row gutter={[12, 12]}>
                  {rows.map((row) => (
                    <Col key={row.id} xs={24} sm={12} xl={8} xxl={6}>
                      <FullServerCard
                        s={row}
                        node={nodeById.get(Number(row.id))}
                        onPick={(r) => setDetailId(r.id)}
                        onUnlock={(s) => setUnlockFor(s)}
                        leading={
                          <Checkbox
                            checked={selected.some((id) => Number(id) === Number(row.id))}
                            onChange={(e) => toggleOne(row.id, e.target.checked)}
                            aria-label={`选择 ${row.name || '#' + row.id}`}
                          />
                        }
                      />
                    </Col>
                  ))}
                </Row>
              ) : (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={list.loading ? '正在加载…' : '没有符合条件的服务器'}
                  style={{ padding: '40px 0' }}
                />
              )}
            </div>
          ) : (
            <Table
              className="pp-table"
              rowKey="id"
              size="medium"
              columns={columns}
              dataSource={rows}
              loading={list.loading}
              rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys) }}
              scroll={{ x: 1800 }}
              pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 台` }}
            />
          )}
        </PageCard>
      </div>

      <ServerUnlockModal server={unlockFor} onClose={() => setUnlockFor(null)} />

      <ServerDrawer
        serverId={detailId}
        onClose={() => setDetailId(null)}
        onChanged={() => {
          list.reload().catch(() => {});
          refreshLive();
        }}
      />

      {/* 添加服务器 */}
      <Modal
        open={compose}
        title="接入新服务器"
        onCancel={() => setCompose(false)}
        onOk={create}
        confirmLoading={saving}
        okText="创建并生成安装命令"
        width={560}
        destroyOnHidden
      >
        <Alert
          type="info"
          showIcon
          style={{ margin: '10px 0 16px', borderRadius: 10 }}
          message="Agent 只做出站连接"
          description="服务器无需开放任何管理端口。创建后把安装命令贴到目标机器执行即可。"
        />
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ enabled: true }}>
          <Form.Item name="name" label="服务器名称" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如：hk-01" />
          </Form.Item>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name="tags" label="标签">
                <Input placeholder="香港,核心" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="allow_exec" label="允许远程命令" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="remark" label="备注">
            <Input.TextArea rows={2} placeholder="用途说明" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 新建服务器的安装命令 */}
      <Modal
        open={!!created}
        title={created && created.title}
        onCancel={() => setCreated(null)}
        footer={
          <Button type="primary" onClick={() => setCreated(null)}>
            知道了
          </Button>
        }
        width={720}
      >
        {created && created.token ? (
          <div style={{ marginBottom: 14 }}>
            <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
              接入密钥（仅本次显示，请立即保存）
            </div>
            <CopyText value={created.token} display={<span className="pp-mono">{created.token}</span>} />
          </div>
        ) : null}
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
          在目标服务器上执行
        </div>
        <JsonView value={String((created && created.command) || '')} maxHeight={260} />
        {created && created.note ? (
          <div style={{ marginTop: 10, fontSize: 12, color: '#94a3b8', lineHeight: 1.7 }}>{created.note}</div>
        ) : null}
      </Modal>
    </div>
  );
}

function OvRing({ pct = 0, color = '#22c55e', size = 36 }) {
  const r = (size - 6) / 2;
  const c = 2 * Math.PI * r;
  const v = Math.max(0, Math.min(100, pct));
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} style={{ display: 'block' }}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="#eef2f7" strokeWidth="3" />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke={color}
        strokeWidth="3"
        strokeDasharray={c}
        strokeDashoffset={c - (v / 100) * c}
        strokeLinecap="round"
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
      />
    </svg>
  );
}

function OvSpark({ data = [], color = '#38bdf8', height = 26 }) {
  const arr = (data || []).slice(-24);
  if (arr.length < 2) return <div style={{ height }} />;
  const max = Math.max(...arr, 1);
  const w = 100;
  const h = height;
  const pts = arr.map((v, i) => [(i / (arr.length - 1)) * w, h - (v / max) * (h - 4) - 2]);
  const line = pts.map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ');
  const area = `0,${h} ${line} ${w},${h}`;
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" style={{ width: '100%', height, display: 'block' }}>
      <polygon points={area} fill={color} opacity="0.12" />
      <polyline points={line} fill="none" stroke={color} strokeWidth="1.4" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

function renderOvChart(kind, ctx) {
  if (!kind) return null;
  const total = ctx.total || 0;
  const pct = (n) => (total ? (n / total) * 100 : 0);
  if (kind === 'online') return <div className="pp-ov-chart"><OvRing pct={pct(ctx.online)} color="#22c55e" /></div>;
  if (kind === 'offline') return <div className="pp-ov-chart"><OvRing pct={pct(ctx.offline)} color="#f43f5e" /></div>;
  if (kind === 'unknown') return <div className="pp-ov-chart"><OvRing pct={pct(ctx.unknown)} color="#cbd5e1" /></div>;
  if (kind === 'rx') return <div className="pp-ov-chart"><OvSpark data={ctx.hist.rx} color="#38bdf8" /></div>;
  if (kind === 'tx') return <div className="pp-ov-chart"><OvSpark data={ctx.hist.tx} color="#a78bfa" /></div>;
  if (kind === 'sum') return <div className="pp-ov-chart"><OvSpark data={ctx.hist.sum} color="#22c55e" /></div>;
  return null;
}