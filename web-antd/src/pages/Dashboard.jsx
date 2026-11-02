import React, { useMemo, useState } from 'react';
import {
  ApartmentOutlined,
  ArrowRightOutlined,
  EyeOutlined,
  FileTextOutlined,
  ReloadOutlined,
  RiseOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Button, Col, Divider, Empty, Row, Segmented, Skeleton, Space, Tag, Timeline, Tooltip } from 'antd';
import { api } from '../api';
import { useAsync } from '../hooks';
import { AreaChart, BarList, ChartLegend, DonutChart } from '../charts';
import { ErrorBlock, MiniStat, PageCard, StatCard } from '../components';
import { dailyRows, fmtAgo, fmtCompact, fmtNum, fmtTime, shortUuid } from '../format';
import { chartPalette } from '../theme';

const TYPE_TAG = {
  subscription: { color: 'geekblue', text: '订阅访问' },
  'api-generation': { color: 'orange', text: '网页生成' },
};

export default function Dashboard() {
  const [days, setDays] = useState(30);
  const [view, setView] = useState('split');

  const { data, loading, error, reload } = useAsync(
    () => api('stats', { query: { days, latest: 12 } }),
    [days]
  );

  const a = (data && data.access_stats) || {};
  const daily = useMemo(() => dailyRows(a.daily_stats), [a.daily_stats]);

  const series = useMemo(() => {
    if (view === 'total') {
      return [{ key: 'total', name: '总访问', color: chartPalette[0] }];
    }
    return [
      { key: 'subscription', name: '订阅访问', color: chartPalette[3] },
      { key: 'api_generation', name: '网页生成', color: chartPalette[2] },
    ];
  }, [view]);

  const popular = (a.popular_uuids || []).map((p, i) => ({
    label: p.owner ? `${p.owner} · ${shortUuid(p.uuid)}` : shortUuid(p.uuid),
    value: Number(p.access_count) || 0,
    color: chartPalette[i % chartPalette.length],
  }));

  const spark = daily.map((d) => d.total);
  const failed = a.success === false;
  const showSkeleton = loading && !data;

  return (
    <div>
      <div className="pp-toolbar">
        <Segmented
          value={String(days)}
          onChange={(v) => setDays(Number(v))}
          options={[
            { label: '近 7 天', value: '7' },
            { label: '近 30 天', value: '30' },
            { label: '近 90 天', value: '90' },
          ]}
        />
        <div className="pp-toolbar-right">
          <Button icon={<ReloadOutlined />} onClick={() => reload().catch(() => {})} loading={loading}>
            刷新
          </Button>
        </div>
      </div>

      {error ? (
        <div style={{ marginBottom: 16 }}>
          <ErrorBlock error={error} onRetry={() => reload().catch(() => {})} />
        </div>
      ) : null}
      {failed ? (
        <div style={{ marginBottom: 16 }}>
          <ErrorBlock error={{ message: a.error || '统计接口返回失败' }} onRetry={() => reload().catch(() => {})} />
        </div>
      ) : null}

      {/* ---------- 指标卡 ---------- */}
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            icon={<ApartmentOutlined />}
            label="用户管理"
            value={showSkeleton ? '—' : fmtNum(data && data.uuids)}
            accent={chartPalette[0]}
            hint="按 UUID 划分的配置分组"
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            icon={<FileTextOutlined />}
            label="订阅模板"
            value={showSkeleton ? '—' : fmtNum(data && data.templates)}
            accent={chartPalette[3]}
            hint="Clash / Mihomo 模板数量"
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            icon={<ThunderboltOutlined />}
            label="访问总量"
            value={showSkeleton ? '—' : fmtCompact(a.total_requests)}
            unit="次"
            accent={chartPalette[1]}
            hint={`统计窗口：近 ${days} 天`}
            spark={spark}
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            icon={<RiseOutlined />}
            label="今日访问"
            value={showSkeleton ? '—' : fmtNum(a.today_total)}
            unit="次"
            accent={chartPalette[2]}
            hint={`订阅 ${fmtNum(a.today_subscription)} · 生成 ${fmtNum(a.today_apigen)}`}
          />
        </Col>
      </Row>

      {/* ---------- 趋势 + 构成 ---------- */}
      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={16}>
          <PageCard
            title={
              <span className="pp-section-title">
                <EyeOutlined style={{ color: '#4f46e5' }} />
                访问趋势
              </span>
            }
            extra={
              <Segmented
                size="small"
                value={view}
                onChange={setView}
                options={[
                  { label: '分类型', value: 'split' },
                  { label: '总访问', value: 'total' },
                ]}
              />
            }
            loading={showSkeleton}
            bodyStyle={{ paddingTop: 12 }}
          >
            {daily.length ? (
              <>
                <AreaChart data={daily} series={series} height={266} unit=" 次" />
                <Divider style={{ margin: '12px 0 10px' }} />
                <ChartLegend items={series.map((s) => ({ name: s.name, color: s.color }))} />
              </>
            ) : (
              <div style={{ height: 300 }}>
                <Empty description="统计窗口内还没有访问记录" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ paddingTop: 80 }} />
              </div>
            )}
          </PageCard>
        </Col>

        <Col xs={24} xl={8}>
          <PageCard
            title={
              <span className="pp-section-title">
                <ThunderboltOutlined style={{ color: '#06b6d4' }} />
                访问构成
              </span>
            }
            loading={showSkeleton}
          >
            <DonutChart
              data={[
                { name: '订阅访问', value: a.subscription_requests || 0, color: chartPalette[3] },
                { name: '网页生成', value: a.api_generation_requests || 0, color: chartPalette[2] },
              ]}
              centerLabel="总请求"
              centerValue={a.total_requests || 0}
              size={158}
            />
            <Divider style={{ margin: '16px 0' }} />
            <Row gutter={[12, 16]}>
              <Col span={12}>
                <MiniStat label="独立订阅 UUID" value={fmtNum(a.unique_uuids)} />
              </Col>
              <Col span={12}>
                <MiniStat label="今日订阅" value={fmtNum(a.today_subscription)} accent={chartPalette[3]} />
              </Col>
              <Col span={12}>
                <MiniStat label="今日网页生成" value={fmtNum(a.today_apigen)} accent={chartPalette[2]} />
              </Col>
              <Col span={12}>
                <MiniStat
                  label={`近 ${days} 天前`}
                  value={fmtCompact(Math.max(0, (a.total_requests || 0) - (a.today_total || 0)))}
                />
              </Col>
            </Row>
          </PageCard>
        </Col>
      </Row>

      {/* ---------- 热门 + 最新 ---------- */}
      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={12}>
          <PageCard
            title={<span className="pp-section-title">热门订阅 Top 15</span>}
            extra={<span className="pp-muted" style={{ fontSize: 12 }}>按累计访问</span>}
            loading={showSkeleton}
          >
            {popular.length ? (
              <BarList items={popular} unit=" 次" />
            ) : (
              <div style={{ height: 220 }}>
                <Empty description="暂无访问数据" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ paddingTop: 54 }} />
              </div>
            )}
          </PageCard>
        </Col>

        <Col xs={24} xl={12}>
          <PageCard
            title={<span className="pp-section-title">最新订阅</span>}
            extra={<span className="pp-muted" style={{ fontSize: 12 }}>最近 12 条</span>}
            loading={showSkeleton}
          >
            {(a.latest_logs || []).length ? (
              <Timeline
                style={{ marginTop: 6 }}
                items={(a.latest_logs || []).map((log, i) => {
                  const tag = TYPE_TAG[log.query_type] || { color: 'default', text: log.query_type || '访问' };
                  return {
                    key: i,
                    color: log.query_type === 'subscription' ? 'blue' : 'orange',
                    children: (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                        <Tag color={tag.color} style={{ marginInlineEnd: 0 }}>
                          {tag.text}
                        </Tag>
                        <Tooltip title={log.uuid}>
                          <span className="pp-mono" style={{ color: '#334155' }}>
                            {shortUuid(log.uuid)}
                          </span>
                        </Tooltip>
                        {log.owner ? <span className="pp-muted">{log.owner}</span> : null}
                        <span className="pp-muted" style={{ marginLeft: 'auto', fontSize: 12 }}>
                          {fmtAgo(log.created_at)}
                        </span>
                      </div>
                    ),
                  };
                })}
              />
            ) : (
              <div style={{ height: 220 }}>
                <Empty description="暂无访问记录" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ paddingTop: 54 }} />
              </div>
            )}
          </PageCard>
        </Col>
      </Row>

      <div style={{ marginTop: 14, color: '#a7b3c4', fontSize: 12, display: 'flex', gap: 6, alignItems: 'center' }}>
        <ArrowRightOutlined />
        数据来源：<code>/api/stats?days={days}</code>，统计窗口 {days} 天 · 最后刷新{' '}
        {fmtTime(Date.now(), '') || '—'}
        <Space />
      </div>
    </div>
  );
}
