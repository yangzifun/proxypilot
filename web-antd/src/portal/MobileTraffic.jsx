import React, { useCallback, useEffect, useState } from 'react';
import { Button } from 'antd-mobile';
import { portalReq } from './portalApi';
import { fmtAgo, fmtBytes } from '../format';

/**
 * 移动端「我的流量」：今日 / 近 7 天 / 累计 + 在线状态。
 * 数据来自 /api/portal/traffic，仅允许查看登录邮箱名下 UUID。
 */
export default function MobileTraffic({ uuid }) {
  const [state, setState] = useState({ loading: true, data: null, error: null });

  const load = useCallback(async () => {
    if (!uuid) return;
    setState((s) => ({ ...s, loading: true }));
    try {
      const res = await portalReq(`/api/portal/traffic?uuid=${encodeURIComponent(uuid)}`);
      setState({ loading: false, data: res.data || {}, error: null });
    } catch (e) {
      setState({ loading: false, data: null, error: e });
    }
  }, [uuid]);

  useEffect(() => {
    load();
  }, [load]);

  if (!uuid || state.error) return null;
  const d = state.data || {};
  const today = d.today || {};
  const week = d.week || {};
  const total = d.total || {};

  return (
    <div className="mtraffic">
      <div className="mtraffic-head">
        <span className="mtraffic-title">我的流量</span>
        <span className={d.active ? 'mtraffic-on' : 'mtraffic-off'}>{d.active ? '在线' : '离线'}</span>
        <Button size="mini" fill="none" loading={state.loading} onClick={load} className="mtraffic-refresh">
          刷新
        </Button>
      </div>
      <div className="mtraffic-grid">
        <div>
          <b>{fmtBytes(today.total || 0)}</b>
          <span>今日</span>
        </div>
        <div>
          <b>{fmtBytes(week.total || 0)}</b>
          <span>近 7 天</span>
        </div>
        <div>
          <b>{fmtBytes(total.total || 0)}</b>
          <span>累计</span>
        </div>
      </div>
      <div className="mtraffic-foot">
        {d.last_active_at ? `最近活跃 ${fmtAgo(d.last_active_at)}` : '暂无流量记录'}
      </div>
    </div>
  );
}
