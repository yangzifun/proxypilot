import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ClockCircleOutlined,
  DeleteOutlined,
  FileSearchOutlined,
  HistoryOutlined,
  ReloadOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  App as AntdApp,
  Button,
  Col,
  Descriptions,
  Input,
  Modal,
  Popconfirm,
  Row,
  Segmented,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
} from 'antd';
import { api } from '../api';
import { useAsync } from '../hooks';
import { ColumnChart } from '../charts';
import { EmptyBlock, ErrorBlock, PageCard, StatCard, StatusPill } from '../components';
import { fmtAgo, fmtNum, fmtTime } from '../format';
import { chartPalette } from '../theme';

const DAY_OPTIONS = [
  { label: '保留最近 30 天', value: 30 },
  { label: '保留最近 90 天', value: 90 },
  { label: '保留最近 180 天', value: 180 },
  { label: '保留最近 365 天', value: 365 },
];

export default function Logs() {
  return (
    <Tabs
      defaultActiveKey="summary"
      items={[
        { key: 'summary', label: <span><FileSearchOutlined /> 访问日志</span>, children: <LogSummary /> },
        { key: 'push', label: <span><HistoryOutlined /> 下发记录</span>, children: <PushLogs /> },
      ]}
    />
  );
}

/* ---------------- 访问日志概览 + 清理 ---------------- */
function LogSummary() {
  const { message } = AntdApp.useApp();
  const { data, loading, error, reload } = useAsync(() => api('logs/summary'), []);

  const [days, setDays] = useState(90);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);

  const older = (data && data.older_than) || {};
  const chartData = useMemo(
    () => [
      { label: '30 天前', fullLabel: '早于 30 天', rows: Number(older['30d']) || 0 },
      { label: '90 天前', fullLabel: '早于 90 天', rows: Number(older['90d']) || 0 },
      { label: '180 天前', fullLabel: '早于 180 天', rows: Number(older['180d']) || 0 },
      { label: '365 天前', fullLabel: '早于 365 天', rows: Number(older['365d']) || 0 },
    ],
    [older]
  );

  const willDelete = useMemo(() => {
    const map = { 30: '30d', 90: '90d', 180: '180d', 365: '365d' };
    return Number(older[map[days]]) || 0;
  }, [days, older]);

  const cleanup = async () => {
    if (!password) {
      message.warning('请输入当前登录密码以确认清理');
      return;
    }
    setBusy(true);
    try {
      const res = await api('logs/cleanup', { method: 'POST', body: { days, password } });
      message.success(
        `已删除 ${fmtNum(res.deleted)} 条，剩余 ${fmtNum(res.remaining)} 条${
          res.capped ? '（本轮达到上限，可再执行一次）' : ''
        }`
      );
      setPassword('');
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '清理失败');
    } finally {
      setBusy(false);
    }
  };

  const total = (data && data.total) || 0;
  const sub = (data && data.subscription) || 0;
  const gen = (data && data.api_generation) || 0;
  const showSkeleton = loading && !data;

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={12} lg={6}>
          <StatCard icon={<FileSearchOutlined />} label="日志总量" value={fmtNum(total)} accent={chartPalette[0]} />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard icon={<ThunderboltOutlined />} label="订阅访问" value={fmtNum(sub)} accent={chartPalette[3]} hint={total ? `${((sub / total) * 100).toFixed(1)}%` : '—'} />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard icon={<ThunderboltOutlined />} label="网页生成" value={fmtNum(gen)} accent={chartPalette[2]} hint={total ? `${((gen / total) * 100).toFixed(1)}%` : '—'} />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
            icon={<ClockCircleOutlined />}
            label="时间跨度"
            value={data && data.oldest ? fmtAgo(data.oldest) : '—'}
            accent={chartPalette[5]}
            hint={data && data.newest ? `最新 ${fmtTime(data.newest)}` : '—'}
          />
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={14}>
          <PageCard
            title={<span className="pp-section-title">按保留期的可删行数</span>}
            extra={
              <Button icon={<ReloadOutlined />} size="small" onClick={() => reload().catch(() => {})} loading={loading}>
                刷新
              </Button>
            }
            loading={showSkeleton}
          >
            {error ? <ErrorBlock error={error} onRetry={() => reload().catch(() => {})} /> : null}
            <ColumnChart
              data={chartData}
              series={[{ key: 'rows', name: '可删除行数', color: chartPalette[0] }]}
              height={236}
              unit=" 行"
            />
            <div style={{ marginTop: 8, fontSize: 12, color: '#94a3b8' }}>
              这里显示「清空到某个截止时间」会删掉多少行，实际执行前请复核。
            </div>
          </PageCard>
        </Col>

        <Col xs={24} xl={10}>
          <PageCard title={<span className="pp-section-title">清理访问日志</span>} loading={showSkeleton}>
            <Descriptions
              size="small"
              column={1}
              items={[
                { key: 'total', label: '当前总量', children: fmtNum(total) },
                { key: 'oldest', label: '最早记录', children: fmtTime(data && data.oldest) },
                { key: 'newest', label: '最新记录', children: fmtTime(data && data.newest) },
              ]}
              style={{ marginBottom: 16 }}
            />
            <Space orientation="vertical" style={{ width: '100%' }} size={12}>
              <Select value={days} onChange={setDays} options={DAY_OPTIONS} style={{ width: '100%' }} />
              <Input.Password
                placeholder="当前登录密码（清理是危险操作，需要二次确认）"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
              />
              <div
                style={{
                  background: willDelete ? 'rgba(244,63,94,.06)' : 'rgba(16,185,129,.07)',
                  border: `1px solid ${willDelete ? 'rgba(244,63,94,.18)' : 'rgba(16,185,129,.2)'}`,
                  color: willDelete ? '#be123c' : '#047857',
                  borderRadius: 10,
                  padding: '10px 12px',
                  fontSize: 12.5,
                  lineHeight: 1.7,
                }}
              >
                将删除早于「保留最近 {days} 天」的日志，预计 <b>{fmtNum(willDelete)}</b> 条。
                删除不可撤销，建议先导出需要的记录。
              </div>
              <Popconfirm
                title="确认清理访问日志？"
                description={`将删除 ${fmtNum(willDelete)} 条记录，且不可恢复。`}
                okText="确认清理"
                okButtonProps={{ danger: true }}
                onConfirm={cleanup}
                disabled={!willDelete}
              >
                <Button
                  danger
                  type="primary"
                  block
                  icon={<DeleteOutlined />}
                  loading={busy}
                  disabled={!willDelete}
                >
                  执行清理
                </Button>
              </Popconfirm>
            </Space>
          </PageCard>
        </Col>
      </Row>
    </div>
  );
}

/* ---------------- 下发 / 执行日志 ---------------- */
const LOG_MODULES = [
  { value: 'all', label: '全部动作' },
  { value: 'mihomo', label: 'mihomo' },
  { value: 'singbox', label: 'singbox' },
  { value: 'uuid-verify', label: 'UUID 巡检' },
  { value: 'cloudflared', label: 'cloudflared' },
  { value: 'agent', label: 'agent' },
];

function PushLogs() {
  const [limit, setLimit] = useState(50);
  const [mod, setMod] = useState('all');
  const [state, setState] = useState({ data: [], loading: false, error: null });
  const [detail, setDetail] = useState(null);

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await api('node-configs/logs', { query: { limit } });
      setState({ data: (res && res.data) || [], loading: false, error: null });
    } catch (e) {
      setState({ data: [], loading: false, error: e });
    }
  }, [limit]);

  useEffect(() => {
    load();
  }, [load]);

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 72 },
    {
      title: '动作',
      dataIndex: 'action',
      width: 150,
      render: (v) => <Tag color="geekblue">{v || '—'}</Tag>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 96,
      render: (v) => <StatusPill status={v} />,
    },
    {
      title: '服务器',
      dataIndex: 'server_name',
      width: 150,
      render: (v) => v || <span className="pp-muted">—</span>,
    },
    {
      title: '配置',
      dataIndex: 'config_name',
      width: 150,
      ellipsis: true,
      render: (v) => v || <span className="pp-muted">—</span>,
    },
    {
      title: '信息',
      dataIndex: 'message',
      ellipsis: true,
      render: (v) => (
        <span className="pp-mono" style={{ fontSize: 12, color: '#64748b' }}>
          {String(v || '—').slice(0, 140)}
        </span>
      ),
    },
    {
      title: '时间',
      dataIndex: 'created_at',
      width: 156,
      render: (v) => <span className="pp-muted">{fmtTime(v)}</span>,
    },
    {
      title: '',
      width: 62,
      render: (_, row) => (
        <Button type="link" size="small" onClick={() => setDetail(row)}>
          详情
        </Button>
      ),
    },
  ];

  return (
    <PageCard
      title={<span className="pp-section-title">节点下发 / 命令执行日志</span>}
      extra={
        <Space>
          <Select value={mod} onChange={setMod} options={LOG_MODULES} style={{ width: 140 }} />
          <Segmented
            size="small"
            value={limit}
            onChange={(v) => setLimit(Number(v))}
            options={[
              { label: '50', value: 50 },
              { label: '100', value: 100 },
              { label: '200', value: 200 },
            ]}
          />
          <Button icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
            刷新
          </Button>
        </Space>
      }
      bodyStyle={{ padding: 0 }}
    >
      {state.error ? (
        <div style={{ padding: 18 }}>
          <ErrorBlock error={state.error} onRetry={load} />
        </div>
      ) : null}
      <Table
        className="pp-table"
        rowKey="id"
        size="medium"
        loading={state.loading}
        dataSource={state.data.filter((x) => mod === 'all' || String(x.action || '').startsWith(mod))}
        columns={columns}
        scroll={{ x: 1240 }}
        pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 条` }}
        locale={{ emptyText: <EmptyBlock text="还没有下发记录" /> }}
      />

      <Modal
        open={!!detail}
        title={detail ? `记录 #${detail.id} · ${detail.action || ''}` : ''}
        onCancel={() => setDetail(null)}
        footer={<Button onClick={() => setDetail(null)}>关闭</Button>}
        width={720}
      >
        {detail ? (
          <Descriptions
            size="small"
            column={1}
            bordered
            items={[
              { key: 'a', label: '动作', children: detail.action || '—' },
              { key: 's', label: '状态', children: <StatusPill status={detail.status} /> },
              { key: 'srv', label: '服务器', children: detail.server_name || `#${detail.server_id || '—'}` },
              { key: 'cfg', label: '配置', children: detail.config_name || `#${detail.config_id || '—'}` },
              { key: 't', label: '时间', children: fmtTime(detail.created_at) },
              {
                key: 'm',
                label: '信息',
                children: (
                  <pre
                    className="pp-json"
                    style={{ maxHeight: 260, whiteSpace: 'pre-wrap', margin: 0 }}
                  >
                    {String(detail.message || '—')}
                  </pre>
                ),
              },
            ]}
          />
        ) : null}
      </Modal>
    </PageCard>
  );
}
