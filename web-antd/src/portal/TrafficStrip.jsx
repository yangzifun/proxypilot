import React, { useEffect, useState } from 'react';
import { Tag } from 'antd';
import { portalReq } from './portalApi';
import { fmtAgo, fmtBytes } from '../format';

/**
 * 用户中心：当前 UUID 的流量用量（今日 / 近 7 天 / 累计）。
 * 数据来自 /api/portal/traffic（只允许查看登录邮箱名下的 UUID）。
 */
export default function TrafficStrip({ uuid }) {
  const [state, setState] = useState({ loading: true, data: null, error: null });

  useEffect(() => {
    if (!uuid) return undefined;
    let alive = true;
    (async () => {
      try {
        const res = await portalReq(`/api/portal/traffic?uuid=${encodeURIComponent(uuid)}`);
        if (alive) setState({ loading: false, data: res.data || {}, error: null });
      } catch (e) {
        if (alive) setState({ loading: false, data: null, error: e });
      }
    })();
    return () => {
      alive = false;
    };
  }, [uuid]);

  if (!uuid || state.error) return null;
  const d = state.data || {};
  const today = d.today || {};
  const week = d.week || {};
  const total = d.total || {};

  const item = (label, value, hint) => (
    <div style={{ flex: '1 1 120px', minWidth: 120 }}>
      <div style={{ fontSize: 12, color: '#94a3b8' }}>{label}</div>
      <div style={{ fontSize: 18, fontWeight: 600, color: '#0f172a', lineHeight: 1.4 }}>{value}</div>
      {hint ? <div style={{ fontSize: 11, color: '#94a3b8' }}>{hint}</div> : null}
    </div>
  );

  return (
    <div
      style={{
        margin: '0 0 14px',
        padding: '14px 18px',
        background: '#fff',
        border: '1px solid #eef2f7',
        borderRadius: 14,
        display: 'flex',
        gap: 18,
        flexWrap: 'wrap',
        alignItems: 'center',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 120 }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: '#0f172a' }}>我的流量</span>
        {d.active ? <Tag color="success">在线</Tag> : <Tag>离线</Tag>}
      </div>
      {item('今日', fmtBytes(today.total || 0), `↑${fmtBytes(today.up || 0)} / ↓${fmtBytes(today.down || 0)}`)}
      {item('近 7 天', fmtBytes(week.total || 0), `↑${fmtBytes(week.up || 0)} / ↓${fmtBytes(week.down || 0)}`)}
      {item('累计', fmtBytes(total.total || 0), `↑${fmtBytes(total.up || 0)} / ↓${fmtBytes(total.down || 0)}`)}
      <div style={{ fontSize: 11, color: '#94a3b8', marginLeft: 'auto' }}>
        {d.last_active_at ? `最近活跃 ${fmtAgo(d.last_active_at)}` : '暂无流量记录'}
      </div>
    </div>
  );
}
