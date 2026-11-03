/* =================================================================
 *  移动端「访问统计」：KPI 指标块 + SVG 趋势/构成图 + 最近访问
 *  支持下拉刷新与失败重试（客户端拉取订阅才会产生记录）
 * ================================================================= */
import React from 'react';
import { Button, Grid, List, PullToRefresh, SpinLoading } from 'antd-mobile';
import {
  ApiOutlined, CalendarOutlined, CloudDownloadOutlined, EyeOutlined, ReloadOutlined,
} from '@ant-design/icons';
import { AreaChart, DonutChart } from '../charts';
import MobileTraffic from './MobileTraffic';
import { useUuidStats } from './hooks/usePortalData';
import { fmtNum, fmtTime, shortUuid } from '../format';

const PALETTE = { sub: '#10b981', api: '#f59e0b' };

const KPIS = [
  { key: 'total', label: '总访问', tone: 'indigo', icon: <EyeOutlined />, field: 'total_access' },
  { key: 'today', label: '今日访问', tone: 'emerald', icon: <CalendarOutlined />, field: 'today' },
  { key: 'sub', label: '订阅拉取', tone: 'amber', icon: <CloudDownloadOutlined />, field: 'subscription_count' },
  { key: 'api', label: '接口生成', tone: 'violet', icon: <ApiOutlined />, field: 'apigen_count' },
];

export default function MobileStats({ uuid }) {
  const { loading, data, error, reload } = useUuidStats(uuid);
  const s = data || {};

  const daily = Array.isArray(s.daily_stats) ? s.daily_stats : [];
  /* 注意：AreaChart 读的是行上的扁平字段（row[key]），不是 row.values */
  const rows = daily.map((d) => ({
    label: String(d.date || '').slice(5),
    sub: Number(d.subscription || 0),
    api: Number(d.api_generation || 0),
  }));
  const areaSeries = [
    { key: 'sub', name: '订阅拉取', color: PALETTE.sub },
    { key: 'api', name: '接口生成', color: PALETTE.api },
  ];
  const donut = [
    { name: '订阅拉取', value: Number(s.subscription_count || 0), color: PALETTE.sub },
    { name: '接口生成', value: Number(s.apigen_count || 0), color: PALETTE.api },
  ];
  const logs = Array.isArray(s.recent_logs) ? s.recent_logs.slice(0, 8) : [];

  const valueOf = (k) => {
    if (k.field === 'today') return Number(s.today_total ?? s.today_access ?? 0);
    return Number(s[k.field] || 0);
  };

  return (
    <PullToRefresh onRefresh={reload}>
      <div className="mstat">
        <MobileTraffic uuid={uuid} />
        <div className="mstat-head">
          <span className="mstat-head-text">近 30 天 · 客户端拉取订阅才会记录</span>
          <Button size="mini" fill="none" onClick={reload} loading={loading} className="mstat-refresh">
            <ReloadOutlined /> 刷新
          </Button>
        </div>

        {loading && !data ? (
          <div className="mstat-loading">
            <SpinLoading color="primary" />
          </div>
        ) : error ? (
          <div className="mstat-error">
            <div>{error.message || '统计加载失败'}</div>
            <Button size="small" onClick={reload} className="mstat-retry">
              重试
            </Button>
          </div>
        ) : (
          <>
            <Grid columns={2} gap={10} className="mstat-grid">
              {KPIS.map((it) => (
                <Grid.Item key={it.key}>
                  <div className={`mstat-kpi tone-${it.tone}`}>
                    <div className="mstat-kpi-head">
                      <span className="mstat-kpi-icon">{it.icon}</span>
                      <span className="mstat-kpi-label">{it.label}</span>
                    </div>
                    <div className="mstat-kpi-value">{fmtNum(valueOf(it))}</div>
                  </div>
                </Grid.Item>
              ))}
            </Grid>

            <section className="mstat-panel">
              <div className="mstat-panel-head">
                <span className="mstat-panel-title">近 30 天拉取趋势</span>
                <span className="mstat-panel-unit">单位：次</span>
              </div>
              {rows.length ? (
                <AreaChart data={rows} series={areaSeries} height={170} unit=" 次" />
              ) : (
                <div className="mstat-empty">近 30 天没有访问记录</div>
              )}
            </section>

            <section className="mstat-panel">
              <div className="mstat-panel-head">
                <span className="mstat-panel-title">访问构成</span>
              </div>
              <DonutChart
                data={donut}
                size={150}
                centerLabel="总访问"
                centerValue={Number(s.total_access || 0)}
              />
            </section>

            <List header="最近访问" className="mstat-logs">
              {logs.length ? (
                logs.map((l, i) => {
                  const isSub = l.query_type === 'subscription';
                  return (
                    <List.Item
                      key={l.id || i}
                      extra={
                        <span className="mstat-ip">
                          {l.client_ip && l.client_ip !== 'unknown' ? l.client_ip : '—'}
                        </span>
                      }
                    >
                      <div className="mstat-log-line">
                        <span className={`mstat-type${isSub ? ' is-sub' : ' is-api'}`}>
                          {isSub ? '订阅拉取' : '接口生成'}
                        </span>
                        <span className="mstat-time">{fmtTime(l.created_at)}</span>
                      </div>
                      <div className="mstat-ua">{shortUuid(l.user_agent || '') || '未知客户端'}</div>
                    </List.Item>
                  );
                })
              ) : (
                <List.Item>暂无访问记录</List.Item>
              )}
            </List>
          </>
        )}
      </div>
    </PullToRefresh>
  );
}
