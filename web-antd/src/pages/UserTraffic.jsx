import React, { useEffect, useMemo, useState } from 'react';
import {
  App as AntdApp,
  Button,
  Col,
  Empty,
  Input,
  DatePicker,
  Modal,
  Segmented,
  Row,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
} from 'antd';
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  DeleteOutlined,
  NodeIndexOutlined,
  ReloadOutlined,
  SearchOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { api } from '../api';
import { useAsync } from '../hooks';
import { ErrorBlock, PageCard, StatCard } from '../components';
import { AreaChart } from '../charts';
import FlowGraph from '../components/FlowGraph';
import { fmtAgo, fmtBytes, fmtTime } from '../format';

const SORTS = [
  { value: 'today', label: '今日流量' },
  { value: 'total', label: '区间流量' },
  { value: 'all', label: '累计流量' },
  { value: 'active', label: '最近活跃' },
];

const ONLINE_WINDOW_MS = 2 * 60 * 1000;

/**
 * 业务 · 用户流量：以「用户」为主视角，展示在线状态与各时间口径的用量。
 * 数据来自 /api/stats/traffic/users（sg 节点统计聚合）。
 */

/* ---------------- 用户 → 服务器 流量流向总览 ---------------- */
function FlowOverview({ data, loading }) {
  const users = (data && data.users) || [];
  const nodes = (data && data.nodes) || [];
  const edges = (data && data.edges) || [];

  if (loading && !users.length) {
    return <div className="pp-muted" style={{ padding: 24, textAlign: 'center' }}>正在统计流向…</div>;
  }
  if (!users.length || !nodes.length) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="近 24 小时还没有可统计的用户流量" />;
  }

  const rowH = 46;
  const topY = 36;
  const lastY = topY + (Math.max(users.length, nodes.length) - 1) * rowH;
  const h = lastY + 60;
  const maxTotal = Math.max(...edges.map((e) => Number(e.total) || 0), 1);
  const grand = users.reduce((a, u) => a + (Number(u.total) || 0), 0) || 1;

  const userY = new Map(users.map((u, i) => [u.uuid, topY + i * rowH]));
  const nodeY = new Map(nodes.map((n, i) => [String(n.node_id), topY + i * rowH]));

  return (
    <svg className="pp-flow-svg" viewBox={'0 0 900 ' + h} style={{ width: '100%', height: 'auto', display: 'block' }}>
      <defs>
        <linearGradient id="ppFlowOvGrad" x1="0" x2="1">
          <stop offset="0%" stopColor="#818cf8" stopOpacity="0.25" />
          <stop offset="100%" stopColor="#22d3ee" stopOpacity="0.85" />
        </linearGradient>
      </defs>

      {users.map((u) => {
        const y = userY.get(u.uuid);
        const pct = ((Number(u.total) || 0) / grand) * 100;
        return (
          <g key={u.uuid}>
            <circle cx="46" cy={y} r="12" fill={u.active ? '#10b981' : '#cbd5e1'} opacity="0.9" />
            <text x="66" y={y + 2} fontSize="12" fill="#0f172a">{String(u.label || u.uuid).slice(0, 10)}</text>
            <text x="66" y={y + 17} fontSize="10.5" fill="#94a3b8">{fmtBytes(u.total)} · {pct.toFixed(0)}%</text>
          </g>
        );
      })}

      {edges.map((e) => {
        const uy = userY.get(e.uuid);
        const ny = nodeY.get(String(e.node_id));
        if (uy === undefined || ny === undefined) return null;
        const w = 1 + (Number(e.total) / maxTotal) * 5;
        const d = 'M62 ' + uy + ' C 300 ' + uy + ', 340 ' + ny + ', 600 ' + ny;
        return (
          <g key={e.uuid + '-' + e.node_id}>
            <path d={d} fill="none" stroke={e.active ? 'url(#ppFlowOvGrad)' : '#e2e8f0'}
              strokeWidth={w} strokeLinecap="round" opacity={e.active ? 0.9 : 0.6} />
            {e.active ? (
              <circle r="3.2" fill="#22d3ee">
                <animateMotion dur={(2 + (1 - Number(e.total) / maxTotal) * 2).toFixed(2) + 's'} repeatCount="indefinite" path={d} />
              </circle>
            ) : null}
          </g>
        );
      })}

      {nodes.map((n) => {
        const y = nodeY.get(String(n.node_id));
        return (
          <g key={n.node_id}>
            <circle cx="612" cy={y} r="9" fill="#0ea5e9" opacity="0.85" />
            <text x="628" y={y + 2} fontSize="12" fill="#0f172a">{String(n.label || ('#' + n.node_id)).slice(0, 18)}</text>
            <text x="628" y={y + 17} fontSize="10.5" fill="#94a3b8">{fmtBytes(n.total)}</text>
          </g>
        );
      })}
    </svg>
  );
}

export default function UserTraffic() {
  const { message, modal } = AntdApp.useApp();
  const [range, setRange] = useState('7d');
  const [trendDays, setTrendDays] = useState(14);
  const [trendUuid, setTrendUuid] = useState('');
  const [nodeWindow, setNodeWindow] = useState('week');
  const [rateMinutes, setRateMinutes] = useState(60);
  const [q, setQ] = useState('');
  const [sortBy, setSortBy] = useState('today');
  const [onlyOnline, setOnlyOnline] = useState(false);
  const [detail, setDetail] = useState(null);
  const [detailRange, setDetailRange] = useState('7d');
  const [detailCustom, setDetailCustom] = useState(null);
  const [nodeTraffic, setNodeTraffic] = useState({ singbox: [], mihomo: [] });
  const [mhUsers, setMhUsers] = useState([]);
  const [nodeLoading, setNodeLoading] = useState(false);
  const loadMhUsers = () => {
    api('mihomo/traffic/summary')
      .then((res) => setMhUsers(res.data || []))
      .catch(() => setMhUsers([]));
  };
  const loadNodeTraffic = () => {
    setNodeLoading(true);
    api('traffic/nodes')
      .then((res) => setNodeTraffic(res.data || { singbox: [], mihomo: [] }))
      .catch(() => setNodeTraffic({ singbox: [], mihomo: [] }))
      .finally(() => setNodeLoading(false));
  };
  useEffect(() => {
    loadNodeTraffic();
    loadMhUsers();
  }, [range]);

  const users = useAsync(() => api(`stats/traffic/users?range=${range}`), [range]);
  const series = useAsync(() => api(`stats/traffic/series?days=${trendDays}${trendUuid ? '&uuid=' + encodeURIComponent(trendUuid) : ''}`), [trendDays, trendUuid]);
  const rate = useAsync(() => api(`stats/traffic/rate?minutes=${rateMinutes}`), [rateMinutes]);
  const growth = useAsync(() => api('stats/users/growth?days=30'), []);
  const protocols = useAsync(() => api('stats/protocols'), []);
  const trafficFlow = useAsync(() => api('stats/traffic/flow?hours=24'), []);
  useEffect(() => {
    const id = setInterval(() => { rate.reload(); }, 10000);
    return () => clearInterval(id);
  }, [rateMinutes]);

  useEffect(() => {
    const id = setInterval(() => {
      users.reload().catch(() => {});
      loadNodeTraffic();
      series.reload().catch(() => {});
      growth.reload().catch(() => {});
      protocols.reload().catch(() => {});
    }, 30000);
    return () => clearInterval(id);
  }, [range, trendDays, trendUuid]);

  useEffect(() => {
    const t = setInterval(() => users.reload().catch(() => {}), 30000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range]);

  const rows = (users.data && users.data.data) || [];

  const filtered = useMemo(() => {
    const kw = q.trim().toLowerCase();
    let list = rows;
    if (onlyOnline) list = list.filter((r) => r.active);
    if (kw) {
      list = list.filter(
        (r) =>
          String(r.uuid || '').toLowerCase().includes(kw) ||
          String(r.owner || '').toLowerCase().includes(kw) ||
          String(r.email || '').toLowerCase().includes(kw)
      );
    }
    const key = {
      today: (r) => Number(r.total_today) || 0,
      total: (r) => Number(r.total) || 0,
      all: (r) => Number(r.total_all) || 0,
      active: (r) => Number(r.last_active_at) || 0,
    }[sortBy] || ((r) => Number(r.total_today) || 0);
    return list.slice().sort((a, b) => key(b) - key(a));
  }, [rows, q, sortBy, onlyOnline]);

  const summary = useMemo(() => {
    let online = 0;
    let today = 0;
    let rangeSum = 0;
    let all = 0;
    rows.forEach((r) => {
      if (r.active) online += 1;
      today += Number(r.total_today) || 0;
      rangeSum += Number(r.total) || 0;
      all += Number(r.total_all) || 0;
    });
    return { online, today, rangeSum, all, total: rows.length };
  }, [rows]);

  const clearRecords = () => {
    modal.confirm({
      title: '清理流量统计记录？',
      content: '将清空全部用户的流量与连接记录（不影响授权与节点配置）。',
      okText: '清理',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          const res = await api('stats/traffic/clear', { method: 'POST', body: {} });
          const c = res.cleared || {};
          message.success(
            `已清理：用户 ${c.user_traffic || 0} 条、小时聚合 ${c.user_traffic_hourly || 0} 条、连接 ${c.node_connections || 0} 条`
          );
          users.reload();
        } catch (e) {
          message.error(e.message || '清理失败');
        }
      },
    });
  };

  const clearUser = (uuid) => {
    modal.confirm({
      title: '清理该用户的流量记录？',
      content: uuid,
      okText: '清理',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          const res = await api('stats/traffic/clear', { method: 'POST', body: { uuid } });
          const c = res.cleared || {};
          message.success(`已清理：用户 ${c.user_traffic || 0} 条、小时聚合 ${c.user_traffic_hourly || 0} 条`);
          setDetail(null);
          users.reload();
        } catch (e) {
          message.error(e.message || '清理失败');
        }
      },
    });
  };

  const loadDetail = async (uuid, r, custom) => {
    setDetail({ uuid, loading: true, data: null });
    try {
      const qs = new URLSearchParams();
      if (r === 'custom' && custom && custom[0] && custom[1]) {
        qs.set('from', String(custom[0].valueOf()));
        qs.set('to', String(custom[1].valueOf()));
      } else {
        qs.set('range', r || '7d');
      }
      const res = await api(`stats/traffic/users/${uuid}?${qs.toString()}`);
      setDetail({ uuid, loading: false, data: res });
    } catch (e) {
      message.error(e.message || '读取详情失败');
      setDetail(null);
    }
  };

  const openDetail = (row) => {
    const useCustom = detailRange === 'custom' && detailCustom;
    if (detailRange === 'custom' && !useCustom) setDetailRange('7d');
    loadDetail(row.uuid, useCustom ? 'custom' : (detailRange === 'custom' ? '7d' : detailRange), detailCustom);
  };

  const columns = [
    {
      title: '用户',
      dataIndex: 'uuid',
      width: 280,
      render: (v, row) => (
        <Space direction="vertical" size={0}>
          <span className="pp-mono" style={{ fontSize: 12 }}>{v}</span>
          <span className="pp-muted" style={{ fontSize: 11.5 }}>
            {row.owner || '未绑定归属'}{row.email ? ' · ' + row.email : ''}
          </span>
        </Space>
      ),
    },
    {
      title: '状态',
      dataIndex: 'active',
      width: 110,
      render: (on) => (on ? <Tag color="green">活跃</Tag> : <Tag>空闲</Tag>),
    },
    {
      title: '今日',
      dataIndex: 'total_today',
      width: 110,
      align: 'right',
      render: (v) => <b>{fmtBytes(v)}</b>,
    },
    { title: '上行', dataIndex: 'up', width: 110, align: 'right', render: (v) => fmtBytes(v) },
    { title: '下行', dataIndex: 'down', width: 110, align: 'right', render: (v) => fmtBytes(v) },
    {
      title: '累计',
      dataIndex: 'total_all',
      width: 110,
      align: 'right',
      render: (v) => <span className="pp-muted">{fmtBytes(v)}</span>,
    },
    {
      title: '最近活跃',
      dataIndex: 'last_active_at',
      width: 150,
      render: (v, row) =>
        row.active ? (
          <Tag color="green">活动中 · {fmtAgo(v)}</Tag>
        ) : (
          <span className="pp-muted">{v ? fmtAgo(v) : '—'}</span>
        ),
    },
    {
      title: '操作',
      width: 90,
      render: (_, row) => (
        <Button type="link" size="small" onClick={() => openDetail(row)}>
          详情
        </Button>
      ),
    },
  ];

  const detailSeries = useMemo(() => {
    const series = (detail && detail.data && detail.data.series) || [];
    const span = series.length
      ? Number(series[series.length - 1].hour_ts) - Number(series[0].hour_ts)
      : 0;
    const dayMode = span > 3 * 86400000;
    return series.map((p) => ({
      label: dayMode ? fmtTime(p.hour_ts).slice(5, 10) : fmtTime(p.hour_ts).slice(5, 16),
      fullLabel: fmtTime(p.hour_ts),
      up: Number(p.up) || 0,
      down: Number(p.down) || 0,
    }));
  }, [detail]);

  const tableProps = {
    className: 'pp-table',
    rowKey: 'uuid',
    size: 'small',
    loading: users.loading,
    columns,
    pagination: { pageSize: 20, showTotal: (t) => `共 ${t} 个用户` },
    locale: {
      emptyText: (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="还没有用户流量：节点下发启用统计后，等待一个采样周期（约 30 秒）"
        />
      ),
    },
  };

  return (
    <div className="pp-traffic-tight">
      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={6}>
          <StatCard
            icon={<ThunderboltOutlined />}
            label="活跃用户"
            value={summary.online}
            hint="近 2 分钟有流量"
            accent="#10b981"
          />
        </Col>
        <Col xs={12} md={6}>
          <StatCard
            icon={<TeamOutlined />}
            label="今日流量"
            value={fmtBytes(summary.today)}
            hint={`共 ${summary.total} 个用户有记录`}
            accent="#f59e0b"
          />
        </Col>
        <Col xs={12} md={6}>
          <StatCard
            icon={<ArrowUpOutlined />}
            label="区间流量"
            value={fmtBytes(summary.rangeSum)}
            hint={range === '24h' ? '24 小时' : range === '30d' ? '30 天' : '7 天'}
            accent="#8b5cf6"
          />
        </Col>
        <Col xs={12} md={6}>
          <StatCard
            icon={<ArrowDownOutlined />}
            label="累计流量"
            value={fmtBytes(summary.all)}
            hint="自统计启用以来"
            accent="#0ea5e9"
          />
        </Col>
      </Row>

      <div className="pp-chart-row">
      <PageCard
        title={<span className="pp-section-title">协议分布</span>}
        extra={<span className="pp-muted" style={{ fontSize: 12 }}>按 UUID 数量</span>}
        style={{ marginBottom: 16 }}
      >
        {protocols.error ? (
          <ErrorBlock error={protocols.error} onRetry={() => protocols.reload()} />
        ) : !protocols.data || !(protocols.data.data || []).length ? (
          <Empty description="暂无协议数据" image={Empty.PRESENTED_IMAGE_SIMPLE} />
        ) : (() => {
          const colors = ['#10b981', '#0ea5e9', '#8b5cf6', '#f59e0b', '#ef4444', '#14b8a6'];
          const list = (protocols.data.data || []).slice(0, 6);
          const total = list.reduce((s, x) => s + Number(x.count || 0), 0) || 1;
          let acc = 0;
          const R = 54;
          const C = 2 * Math.PI * R;
          return (
            <div style={{ display: 'flex', alignItems: 'center', gap: 36, flexWrap: 'wrap' }}>
              <svg width="140" height="140" viewBox="0 0 140 140">
                <circle cx="70" cy="70" r={R} fill="none" stroke="#eef2f7" strokeWidth="16" />
                {list.map((x, i) => {
                  const frac = Number(x.count || 0) / total;
                  const dash = frac * C;
                  const el = (
                    <circle
                      key={x.protocol}
                      cx="70" cy="70" r={R} fill="none"
                      stroke={colors[i % colors.length]} strokeWidth="16"
                      strokeDasharray={dash + ' ' + (C - dash)}
                      strokeDashoffset={-acc}
                      transform="rotate(-90 70 70)"
                    />
                  );
                  acc += dash;
                  return el;
                })}
                <text x="70" y="74" textAnchor="middle" style={{ fontSize: 18, fontWeight: 700, fill: '#0f172a' }}>{total}</text>
              </svg>
              <div style={{ display: 'grid', gap: 8 }}>
                {list.map((x, i) => (
                  <div key={x.protocol} style={{ fontSize: 13 }}>
                    <span style={{ display: 'inline-block', width: 10, height: 10, borderRadius: 3, background: colors[i % colors.length], marginRight: 8 }} />
                    {String(x.protocol || 'unknown').toUpperCase()} <b>{x.count}</b>
                    <span className="pp-muted" style={{ marginLeft: 6 }}>{((Number(x.count || 0) / total) * 100).toFixed(0)}%</span>
                  </div>
                ))}
              </div>
            </div>
          );
        })()}
      </PageCard>

      <PageCard
        title={<span className="pp-section-title">用户增长（近 30 天）</span>}
        extra={<span className="pp-muted" style={{ fontSize: 12 }}>按创建时间每日新增</span>}
        style={{ marginBottom: 16 }}
      >
        {growth.error ? (
          <ErrorBlock error={growth.error} onRetry={() => growth.reload()} />
        ) : !growth.data || !(growth.data.data || []).length ? (
          <Empty description="暂无新增用户数据" image={Empty.PRESENTED_IMAGE_SIMPLE} />
        ) : (
          <AreaChart
            data={growth.data.data}
            series={[{ key: 'value', name: '新增用户', color: '#10b981' }]}
            height={200}
            formatValue={(v) => String(v) + ' 人'}
          />
        )}
      </PageCard>

      <PageCard
        title={<span className="pp-section-title">用户在线分布</span>}
        extra={<span className="pp-muted" style={{ fontSize: 12 }}>近 2 分钟有流量视为活跃</span>}
        style={{ marginBottom: 16 }}
      >
        {(() => {
          const list = (users.data && users.data.data) || [];
          const online = list.filter((u) => u.active).length;
          const offline = Math.max(0, list.length - online);
          const total = online + offline;
          const pct = total ? (online / total) * 100 : 0;
          const r = 54;
          const c = 2 * Math.PI * r;
          return (
            <div style={{ display: 'flex', alignItems: 'center', gap: 36, flexWrap: 'wrap', padding: '4px 0' }}>
              <svg width="140" height="140" viewBox="0 0 140 140">
                <circle cx="70" cy="70" r={r} fill="none" stroke="#eef2f7" strokeWidth="16" />
                <circle
                  cx="70" cy="70" r={r} fill="none" stroke="#10b981" strokeWidth="16"
                  strokeDasharray={c} strokeDashoffset={c - (pct / 100) * c}
                  strokeLinecap="round" transform="rotate(-90 70 70)"
                />
                <text x="70" y="66" textAnchor="middle" style={{ fontSize: 22, fontWeight: 700, fill: '#065f46' }}>{online}</text>
                <text x="70" y="86" textAnchor="middle" style={{ fontSize: 12, fill: '#6b7280' }}>在线</text>
              </svg>
              <div style={{ display: 'grid', gap: 10 }}>
                <div><span style={{ display: 'inline-block', width: 10, height: 10, borderRadius: 5, background: '#10b981', marginRight: 8 }} />在线 <b>{online}</b></div>
                <div><span style={{ display: 'inline-block', width: 10, height: 10, borderRadius: 5, background: '#cbd5e1', marginRight: 8 }} />离线 <b>{offline}</b></div>
                <div className="pp-muted" style={{ fontSize: 12 }}>合计 {total} 个用户 · 活跃率 {total ? pct.toFixed(0) : 0}%</div>
              </div>
            </div>
          );
        })()}
      </PageCard>

      </div>


      <div className="pp-pair-row">
      <PageCard
        title={<span className="pp-section-title">流量趋势</span>}
        extra={
          <Space size={8} wrap>
            <Select
              size="small"
              allowClear
              showSearch
              optionFilterProp="label"
              placeholder="全体用户"
              style={{ width: 200 }}
              value={trendUuid || undefined}
              onChange={(v) => setTrendUuid(v || '')}
              options={((users.data && users.data.data) || []).map((u) => ({
                value: u.uuid,
                label: u.owner || u.remark || u.email || String(u.uuid).slice(0, 8),
              }))}
            />
            <Segmented
              size="small"
              value={trendDays}
              onChange={setTrendDays}
              options={[{ label: '7 天', value: 7 }, { label: '14 天', value: 14 }, { label: '30 天', value: 30 }]}
            />
          </Space>
        }
        style={{ marginBottom: 16 }}
      >
        {series.error ? (
          <ErrorBlock error={series.error} onRetry={() => series.reload()} />
        ) : series.loading && !series.data ? (
          <div style={{ padding: 40, textAlign: 'center' }} className="pp-muted">读取中…</div>
        ) : (
          <AreaChart
            data={(series.data && series.data.data) || []}
            series={[
              { key: 'down', name: '下行', color: '#0ea5e9' },
              { key: 'up', name: '上行', color: '#8b5cf6' },
            ]}
            height={240}
            formatValue={fmtBytes}
          />
        )}
      </PageCard>

      <PageCard
        title={<span className="pp-section-title">全站实时速率</span>}
        extra={
          <Space size={8}>
            <span className="pp-muted" style={{ fontSize: 12 }}>全站速率 · 每 10 秒刷新</span>
            <Segmented
              size="small"
              value={rateMinutes}
              onChange={setRateMinutes}
              options={[{ label: '15 分钟', value: 15 }, { label: '60 分钟', value: 60 }, { label: '3 小时', value: 180 }]}
            />
          </Space>
        }
        style={{ marginBottom: 16 }}
      >
        {rate.error ? (
          <ErrorBlock error={rate.error} onRetry={() => rate.reload()} />
        ) : !rate.data || !(rate.data.data || []).length ? (
          <Empty description="暂无速率数据" image={Empty.PRESENTED_IMAGE_SIMPLE} />
        ) : (
          <AreaChart
            data={rate.data.data}
            series={[
              { key: 'down', name: '下行', color: '#0ea5e9' },
              { key: 'up', name: '上行', color: '#8b5cf6' },
            ]}
            height={200}
            formatValue={(v) => fmtBytes(v) + '/s'}
          />
        )}
      </PageCard>

      </div>

      <div className="pp-pair-row">
      <PageCard
        title={<span className="pp-section-title">Top 10 用户（当前区间）</span>}
        extra={<span className="pp-muted" style={{ fontSize: 12 }}>按上下行合计排序</span>}
        style={{ marginBottom: 16 }}
      >
        {(() => {
          const list = ((users.data && users.data.data) || [])
            .map((u) => ({ ...u, _sum: Number(u.up || 0) + Number(u.down || 0) }))
            .filter((u) => u._sum > 0)
            .sort((a, b) => b._sum - a._sum)
            .slice(0, 10);
          if (!list.length) {
            return <Empty description="当前区间没有流量数据" image={Empty.PRESENTED_IMAGE_SIMPLE} />;
          }
          const max = list[0]._sum || 1;
          return (
            <div style={{ display: 'grid', gap: 10 }}>
              {list.map((u, i) => (
                <div key={u.uuid} style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  <span className="pp-muted" style={{ width: 22, textAlign: 'right', fontSize: 12 }}>{i + 1}</span>
                  <span style={{ width: 210, fontSize: 12.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {u.owner || u.remark || u.email || String(u.uuid).slice(0, 8)}
                  </span>
                  <div style={{ flex: 1, height: 8, background: '#eef2f7', borderRadius: 4, overflow: 'hidden' }}>
                    <div style={{ width: (u._sum / max) * 100 + '%', height: '100%', borderRadius: 4, background: 'linear-gradient(90deg,#38bdf8,#8b5cf6)' }} />
                  </div>
                  <span className="pp-mono" style={{ width: 96, textAlign: 'right', fontSize: 12 }}>{fmtBytes(u._sum)}</span>
                </div>
              ))}
            </div>
          );
        })()}
      </PageCard>

      <PageCard
        title={<span className="pp-section-title">节点流量 Top 8</span>}
        extra={
          <Space size={8}>
            <span className="pp-muted" style={{ fontSize: 12 }}>sing-box + mihomo 汇总</span>
            <Segmented
              size="small"
              value={nodeWindow}
              onChange={setNodeWindow}
              options={[{ label: '今日', value: 'today' }, { label: '7 天', value: 'week' }, { label: '累计', value: 'total' }]}
            />
          </Space>
        }
        style={{ marginBottom: 16 }}
      >
        {(() => {
          const raw = nodeTraffic || {};
          const list = [...(raw.singbox || []), ...(raw.mihomo || [])]
            .map((n) => ({ name: n.name || n.server_name || ('#' + n.id), value: Number(n[nodeWindow] || 0) }))
            .filter((n) => n.value > 0)
            .sort((a, b) => b.value - a.value)
            .slice(0, 8);
          if (!list.length) {
            return <Empty description="暂无节点流量数据" image={Empty.PRESENTED_IMAGE_SIMPLE} />;
          }
          const max = list[0].value || 1;
          return (
            <div style={{ display: 'grid', gap: 10 }}>
              {list.map((n, i) => (
                <div key={n.name + i} style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  <span className="pp-muted" style={{ width: 22, textAlign: 'right', fontSize: 12 }}>{i + 1}</span>
                  <span style={{ width: 210, fontSize: 12.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{n.name}</span>
                  <div style={{ flex: 1, height: 8, background: '#eef2f7', borderRadius: 4, overflow: 'hidden' }}>
                    <div style={{ width: (n.value / max) * 100 + '%', height: '100%', borderRadius: 4, background: 'linear-gradient(90deg,#10b981,#22d3ee)' }} />
                  </div>
                  <span className="pp-mono" style={{ width: 96, textAlign: 'right', fontSize: 12 }}>{fmtBytes(n.value)}</span>
                </div>
              ))}
            </div>
          );
        })()}
      </PageCard>

      </div>

      <PageCard
        title={<span className="pp-section-title">用户流量与在线状态</span>}
        extra={
          <Space wrap>
            <Select
              style={{ width: 120 }}
              value={range}
              onChange={setRange}
              options={[
                { value: '24h', label: '24 小时' },
                { value: '7d', label: '7 天' },
                { value: '30d', label: '30 天' },
              ]}
            />
            <Button
              icon={<ReloadOutlined />}
              loading={users.loading}
              onClick={() => users.reload().catch(() => {})}
            >
              刷新
            </Button>
            <Button danger icon={<DeleteOutlined />} onClick={clearRecords}>
              清理记录
            </Button>
          </Space>
        }
        bodyStyle={{ paddingTop: 8 }}
      >
        {users.error ? (
          <ErrorBlock error={users.error} onRetry={() => users.reload().catch(() => {})} />
        ) : null}
        <Tabs
          defaultActiveKey="all"
          items={[
            {
              key: 'all',
              label: `全部用户（${summary.total}）`,
              children: (
                <>
                  <Space wrap style={{ marginBottom: 12 }}>
                    <Input
                      allowClear
                      prefix={<SearchOutlined />}
                      placeholder="搜索 UUID / 归属 / 邮箱"
                      style={{ width: 280 }}
                      value={q}
                      onChange={(e) => setQ(e.target.value)}
                    />
                    <span className="pp-muted" style={{ fontSize: 12 }}>排序</span>
                    <Select style={{ width: 140 }} value={sortBy} onChange={setSortBy} options={SORTS} />
                  </Space>
                  <Table {...tableProps} dataSource={filtered} />
                </>
              ),
            },
            {
              key: 'nodes',
              label: '按节点',
              children: (
                <>
                  <Space wrap style={{ marginBottom: 12 }}>
                    <Button size="small" icon={<ReloadOutlined />} loading={nodeLoading} onClick={loadNodeTraffic}>
                      刷新节点流量
                    </Button>
                    <span className="pp-muted" style={{ fontSize: 12 }}>
                      统计 sing-box 节点与 mihomo 绑定的今日 / 近 7 天 / 累计流量
                    </span>
                  </Space>
                  <div className="pp-section-title" style={{ margin: '4px 0 8px' }}>sing-box 节点</div>
                  <Table
                    rowKey={(r) => 'sb-' + r.id}
                    size="middle"
                    loading={nodeLoading}
                    pagination={{ pageSize: 10, hideOnSinglePage: true }}
                    dataSource={nodeTraffic.singbox}
                    columns={[
                      { title: '节点', dataIndex: 'name', ellipsis: true },
                      { title: '服务器', dataIndex: 'server_name', ellipsis: true },
                      { title: '今日', dataIndex: 'today', width: 120, render: (v) => fmtBytes(v || 0) },
                      { title: '近 7 天', dataIndex: 'week', width: 120, render: (v) => fmtBytes(v || 0) },
                      { title: '累计', dataIndex: 'total', width: 130, render: (v) => fmtBytes(v || 0) },
                      { title: '活跃 UUID', dataIndex: 'online', width: 110 },
                    ]}
                  />
                  <div className="pp-section-title" style={{ margin: '18px 0 8px' }}>mihomo 绑定</div>
                  <Table
                    rowKey={(r) => 'mh-' + r.id}
                    size="middle"
                    loading={nodeLoading}
                    pagination={{ pageSize: 10, hideOnSinglePage: true }}
                    dataSource={nodeTraffic.mihomo}
                    columns={[
                      { title: '绑定', dataIndex: 'name', ellipsis: true },
                      { title: '服务器', dataIndex: 'server_name', ellipsis: true },
                      { title: '模板', dataIndex: 'template_name', ellipsis: true, render: (v) => v || '—' },
                      { title: '今日', dataIndex: 'traffic_today', width: 120, render: (v) => fmtBytes(v || 0) },
                      { title: '近 7 天', dataIndex: 'traffic_week', width: 120, render: (v) => fmtBytes(v || 0) },
                      { title: '累计', dataIndex: 'traffic_total', width: 130, render: (v) => fmtBytes(v || 0) },
                      { title: '在线 UUID', dataIndex: 'online', width: 110 },
                    ]}
                  />
                  <div className="pp-section-title" style={{ margin: '18px 0 8px' }}>mihomo 节点 · 按用户</div>
                  <Table
                    rowKey="uuid"
                    size="middle"
                    pagination={{ pageSize: 10, hideOnSinglePage: true }}
                    dataSource={mhUsers}
                    locale={{ emptyText: '暂无 mihomo 用户流量（需先下发并产生连接）' }}
                    columns={[
                      { title: '用户', dataIndex: 'remark', ellipsis: true, render: (v, r) => v || r.owner || String(r.uuid).slice(0, 8) },
                      { title: '今日上行', dataIndex: 'today_up', width: 110, render: (v) => fmtBytes(Number(v) || 0) },
                      { title: '今日下行', dataIndex: 'today_down', width: 110, render: (v) => fmtBytes(Number(v) || 0) },
                      { title: '近 7 天', width: 110, render: (_, r) => fmtBytes((Number(r.week_up) || 0) + (Number(r.week_down) || 0)) },
                      { title: '累计', width: 120, render: (_, r) => fmtBytes((Number(r.up_total) || 0) + (Number(r.down_total) || 0)) },
                      { title: '在线', dataIndex: 'online', width: 80, render: (v) => (v ? <Tag color="green">在线</Tag> : <Tag>离线</Tag>) },
                    ]}
                  />
                </>
              ),
            },
            {
              key: 'flow',
              label: (
                <span>
                  <NodeIndexOutlined /> 流量流向
                </span>
              ),
              children: (
                <>
                  <div className="pp-muted" style={{ fontSize: 12, marginBottom: 10 }}>
                    多用户 → 多服务器的流量方向；仅显示近 {Math.round(ONLINE_WINDOW_MS / 60000)} 分钟活跃的用户，线宽表示流量大小，视图固定自动适配。
                  </div>
                  {trafficFlow.loading && !(trafficFlow.data && trafficFlow.data.users) ? (
                    <div className="pp-muted" style={{ padding: 40, textAlign: 'center' }}>正在统计流向…</div>
                  ) : (
                    <FlowGraph
                      users={(trafficFlow.data && trafficFlow.data.users) || []}
                      nodes={(trafficFlow.data && trafficFlow.data.nodes) || []}
                      edges={(trafficFlow.data && trafficFlow.data.edges) || []}
                      height={520}
                      emptyText="当前没有活跃用户在产生流量"
                    />
                  )}
                </>
              ),
            },
            {
              key: 'online',
              label: `在线用户（${summary.online}）`,
              children: (
                <>
                  <div className="pp-muted" style={{ fontSize: 12, marginBottom: 10 }}>
                    在线口径：最近 {Math.round(ONLINE_WINDOW_MS / 60000)} 分钟内有流量增量（sing-box 不提供按用户的连接明细）
                  </div>
                  <Table
                    {...tableProps}
                    dataSource={rows
                      .filter((r) => r.active)
                      .slice()
                      .sort((a, b) => Number(b.last_active_at) - Number(a.last_active_at))}
                    locale={{ emptyText: '当前没有在线用户' }}
                  />
                </>
              ),
            },
          ]}
        />
      </PageCard>

      <Modal
        open={!!detail}
        title={detail ? `用户用量 · ${detail.uuid}` : ''}
        onCancel={() => setDetail(null)}
        footer={
          <Space>
            <Button danger onClick={() => (detail && detail.uuid ? clearUser(detail.uuid) : null)}>
              清理该用户记录
            </Button>
            <Button onClick={() => setDetail(null)}>关闭</Button>
          </Space>
        }
        width={860}
      >
        <Space wrap style={{ marginBottom: 12 }}>
          <Segmented
            value={detailRange}
            onChange={(v) => {
              setDetailRange(v);
              if (v !== 'custom' && detail && detail.uuid) loadDetail(detail.uuid, v);
            }}
            options={[
              { label: '24 小时', value: '24h' },
              { label: '7 天', value: '7d' },
              { label: '30 天', value: '30d' },
              { label: '自定义', value: 'custom' },
            ]}
          />
          {detailRange === 'custom' ? (
            <>
              <DatePicker.RangePicker showTime onChange={(v) => setDetailCustom(v)} />
              <Button
                size="small"
                type="primary"
                disabled={!detailCustom}
                onClick={() => detail && detail.uuid && loadDetail(detail.uuid, 'custom', detailCustom)}
              >
                应用
              </Button>
            </>
          ) : null}
        </Space>
        {detail && detail.loading ? (
          <div style={{ padding: 40, textAlign: 'center' }} className="pp-muted">读取中…</div>
        ) : detail && detail.data ? (
          <>
            <Space size={24} style={{ marginBottom: 8 }}>
              <span>上行 <b>{fmtBytes(detail.data.up)}</b></span>
              <span>下行 <b>{fmtBytes(detail.data.down)}</b></span>
              <span className="pp-muted">最近活跃 {fmtActiveAt(detail.data.last_active_at)}</span>
            </Space>
            {detailSeries.length ? (
              <AreaChart
                data={detailSeries}
                series={[
                  { key: 'down', name: '下行', color: '#0ea5e9' },
                  { key: 'up', name: '上行', color: '#8b5cf6' },
                ]}
                height={240}
                formatValue={fmtBytes}
              />
            ) : (
              <Empty description="该时间窗口没有流量记录" image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
            {(() => {
              const nodes = detail.data.nodes || [];
              if (!nodes.length) return null;
              const recent = new Set((detail.data.recent_nodes || []).map((x) => x.node_id));
              const ccOf = (name) => {
                const n = String(name || '');
                const map = [
                  ['香港', 'hk'], ['台湾', 'tw'], ['台北', 'tw'], ['彰化', 'tw'],
                  ['东京', 'jp'], ['日本', 'jp'], ['新加坡', 'sg'], ['狮城', 'sg'],
                  ['洛杉矶', 'us'], ['美国', 'us'], ['德国', 'de'], ['法兰克福', 'de'],
                  ['马来西亚', 'my'], ['吉隆坡', 'my'], ['雅加达', 'id'], ['印尼', 'id'],
                  ['曼谷', 'th'], ['泰国', 'th'], ['韩国', 'kr'], ['首尔', 'kr'],
                  ['英国', 'gb'], ['伦敦', 'gb'], ['法国', 'fr'], ['荷兰', 'nl'],
                ];
                for (const [k, v] of map) if (n.includes(k)) return v;
                return '';
              };
              const shown = nodes.slice(0, 12);
              // G6 流向图数据（单个用户 → 多个节点）
              const flowUsers = [{
                uuid: String(detail.data.uuid),
                label: String(detail.data.owner || detail.data.email || detail.data.uuid).slice(0, 12),
                total: shown.reduce((a, n) => a + (Number(n.total) || 0), 0),
              }];
              const flowNodes = shown.map((n) => ({
                node_id: n.node_id,
                label: String(n.node_name || ('#' + n.node_id)),
                total: n.total,
              }));
              const flowEdges = shown.map((n) => ({
                uuid: String(detail.data.uuid),
                node_id: n.node_id,
                total: Number(n.total) || 0,
              }));
              const rowH = 44;
              const topY = 34;
              const lastY = topY + Math.max(0, shown.length - 1) * rowH;
              const midY = (topY + lastY) / 2;
              const h = lastY + 62;
              const label = detail.data.owner || detail.data.email || String(detail.data.uuid).slice(0, 8);
              const short = String(label).slice(0, 6);
              return (
                <div style={{ marginTop: 8, marginBottom: 0 }}>
                  <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
                    用户流向（绿=近 2 分钟活跃，动态光点表示实时流量）
                  </div>
                  <FlowGraph users={flowUsers} nodes={flowNodes} edges={flowEdges} height={Math.max(220, shown.length * 46 + 80)} emptyText="该用户近期没有节点流量" />

                </div>
              );
            })()}
          </>
        ) : null}
      </Modal>
    </div>
  );
}

function fmtActiveAt(ts) {
  if (!ts) return '—';
  const gap = Date.now() - Number(ts);
  if (gap < 0) return '刚刚';
  const s = Math.floor(gap / 1000);
  if (s < 10) return '刚刚';
  if (s < 60) return s + ' 秒前';
  const m = Math.floor(s / 60);
  if (m < 60) return m + ' 分钟前';
  const h = Math.floor(m / 60);
  if (h < 24) return h + ' 小时前';
  return Math.floor(h / 24) + ' 天前';
}
