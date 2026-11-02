import React, { useEffect, useMemo, useState } from 'react';
import {
  App as AntdApp,
  Button,
  Col,
  Empty,
  Input,
  Modal,
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
  const { message } = AntdApp.useApp();
  const [range, setRange] = useState('7d');
  const [q, setQ] = useState('');
  const [sortBy, setSortBy] = useState('today');
  const [onlyOnline, setOnlyOnline] = useState(false);
  const [detail, setDetail] = useState(null);

  const users = useAsync(() => api(`stats/traffic/users?range=${range}`), [range]);

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

  const openDetail = async (row) => {
    setDetail({ uuid: row.uuid, loading: true, data: null });
    try {
      const res = await api(`stats/traffic/users/${row.uuid}?range=${range}`);
      setDetail({ uuid: row.uuid, loading: false, data: res });
    } catch (e) {
      message.error(e.message || '读取详情失败');
      setDetail(null);
    }
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
    return series.map((p) => ({
      label: fmtTime(p.hour_ts).slice(5, 16),
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
        footer={<Button onClick={() => setDetail(null)}>关闭</Button>}
        width={860}
      >
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
