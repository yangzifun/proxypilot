import React, { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Skeleton, Tag } from 'antd';
import { GlobalOutlined, ReloadOutlined } from '@ant-design/icons';
import { portalApi } from './portalApi';
import { CopyText } from '../components';
import { fmtAgo, fmtNum } from '../format';

/**
 * 用户中心 · DoH（DNS over HTTPS）卡片
 * 展示当前账号的 DoH 链接、启用状态、近 30 天查询量与最近使用时间。
 * 接口：GET /api/portal/doh
 */
export default function DohCard({ mobile = false }) {
  const [state, setState] = useState({ loading: true, error: null, data: null });

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await portalApi('portal/doh');
      setState({ loading: false, error: null, data: (res && res.data) || res || null });
    } catch (e) {
      setState({ loading: false, error: e, data: null });
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const data = state.data || {};
  const enabled = !!(data.enabled === undefined ? data.doh_enabled : data.enabled);
  const bound = data.bound === undefined && data.granted === undefined ? true : !!(data.bound !== false && data.granted !== false);
  const uuid = String(data.uuid || '');
  const domain = String(data.domain || data.doh_domain || '').replace(/^https?:\/\//, '').replace(/\/+$/, '');
  const url = String(data.url || data.doh_url || (domain && uuid ? `https://${domain}/dns-query/${uuid}` : '') || '');
  const queries30d = Number(
    data.queries_30d !== undefined
      ? data.queries_30d
      : data.query_count_30d !== undefined
        ? data.query_count_30d
        : data.queries !== undefined
          ? data.queries
          : data.count_30d || 0
  ) || 0;
  const lastUsed = data.last_used_at || data.last_query_at || data.last_used || '';

  return (
    <div style={{ padding: mobile ? 0 : 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginBottom: 10 }}>
        <div>
          <b>DoH</b>
          <span className="pp-muted" style={{ marginLeft: 8, fontSize: 12 }}>
            DNS over HTTPS · 加密解析
          </span>
        </div>
        <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
          刷新
        </Button>
      </div>

      {state.loading && !state.data ? (
        <Skeleton active paragraph={{ rows: 2 }} />
      ) : state.error ? (
        <Alert type="error" showIcon message={state.error.message || 'DoH 加载失败'} />
      ) : !enabled ? (
        <Alert type="info" showIcon message="管理员还没有开启 DoH 服务" />
      ) : !bound || !url ? (
        <Alert type="info" showIcon message="你的账号还没有 DoH 链接，请联系管理员绑定 UUID" />
      ) : (
        <div style={{ border: '1px solid #eef2f7', borderRadius: 12, padding: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <Tag color="green" style={{ marginInlineEnd: 0 }}>
              <GlobalOutlined /> 已启用
            </Tag>
            <span className="pp-muted" style={{ fontSize: 12 }}>
              仅面板服务器提供 · 每位用户独立链接
            </span>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12, color: '#64748b' }}>DoH URL</span>
            <CopyText value={url} />
          </div>
          <div style={{ fontSize: 12, color: '#64748b', marginBottom: 8 }}>
            近 30 天查询 <b>{fmtNum(queries30d)}</b> 次 · 最近使用{' '}
            {lastUsed ? <span title={String(lastUsed)}>{fmtAgo(lastUsed)}</span> : '暂无记录'}
          </div>
          <div style={{ fontSize: 12, color: '#94a3b8', lineHeight: 1.7 }}>
            使用说明：Chrome / Android「私人 DNS」/ AdGuard Home 等客户端填入上面的完整 URL（含 /dns-query/&lt;uuid&gt;）。
            该链接含个人标识，请勿公开分享；需管理员把 DoH 域名解析到面板并启用 HTTPS。
          </div>
        </div>
      )}
    </div>
  );
}