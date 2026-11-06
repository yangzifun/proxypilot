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
  ReloadOutlined,
  SearchOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { api } from '../api';
import { useAsync } from '../hooks';
import { ErrorBlock, PageCard, StatCard } from '../components';
import { AreaChart } from '../charts';
import { fmtAgo, fmtBytes, fmtTime } from '../format';

const SORTS = [
  { value: 'today', label: '今日流量' },
  { value: 'total', label: '区间流量' },
  { value: 'all', label: '累计流量' },
  { value: 'active', label: '最近活跃' },
];

const ONLINE_WINDOW_MS = 3 * 60 * 1000;

/**
 * 业务 · 用户流量：以「用户」为主视角，展示在线状态与各时间口径的用量。
 * 数据来自 /api/stats/traffic/users（sg 节点统计聚合）。
 */
export default function UserTraffic() {
  const { message, modal } = AntdApp.useApp();
  const [range, setRange] = useState('7d');
  const [trendDays, setTrendDays] = useState(14);
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
  const series = useAsync(() => api(`stats/traffic/series?days=${trendDays}`), [trendDays]);

  useEffect(() => {
    const t = setInterval(() => users.reload().catch(() => {}), 60000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
      render: (on) => (on ? <Tag color="green">在线</Tag> : <Tag>离线</Tag>),
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
    <div>
      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={6}>
          <StatCard
            icon={<ThunderboltOutlined />}
            label="在线用户"
            value={summary.online}
            hint="近 3 分钟有流量"
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

      <PageCard
        title={<span className="pp-section-title">流量趋势</span>}
        extra={
          <Space size={8}>
            <span className="pp-muted" style={{ fontSize: 12 }}>全体用户 · 每日汇总</span>
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
              <span className="pp-muted">最近活跃 {fmtAgo(detail.data.last_active_at)}</span>
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
            {detail.data.nodes && detail.data.nodes.length ? (
              <div style={{ marginTop: 16 }}>
                <div className="pp-muted" style={{ fontSize: 12, marginBottom: 6 }}>按节点分布</div>
                <Table
                  className="pp-table"
                  rowKey={(r) => `${r.node_id}`}
                  size="small"
                  dataSource={detail.data.nodes}
                  pagination={false}
                  columns={[
                    { title: '节点', dataIndex: 'node_name', render: (v) => v || '已删除节点' },
                    { title: '上行', dataIndex: 'up', width: 120, align: 'right', render: (v) => fmtBytes(v) },
                    { title: '下行', dataIndex: 'down', width: 120, align: 'right', render: (v) => fmtBytes(v) },
                    { title: '合计', dataIndex: 'total', width: 120, align: 'right', render: (v) => <b>{fmtBytes(v)}</b> },
                  ]}
                />
              </div>
            ) : null}
          </>
        ) : null}
      </Modal>
    </div>
  );
}
