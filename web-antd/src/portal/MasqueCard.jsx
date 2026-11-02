import React, { useCallback, useEffect, useState } from 'react';
import { Alert, App as AntdApp, Button, Skeleton, Tag } from 'antd';
import { CloudDownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { portalApi, portalReq } from './portalApi';

/**
 * 用户中心 · MASQUE（Cloudflare WARP）卡片
 * 仅展示代理配置，不进入 sing-box 订阅。
 */
export default function MasqueCard({ mobile = false }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ loading: true, error: null, data: null });
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await portalApi('portal/masque');
      setState({ loading: false, error: null, data: (res && res.data) || null });
    } catch (e) {
      setState({ loading: false, error: e, data: null });
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const download = async () => {
    setBusy(true);
    try {
      const { text } = await portalReq('/api/portal/masque/config');
      const blob = new Blob([text], { type: 'text/yaml;charset=utf-8' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = 'masque-warp.yaml';
      document.body.appendChild(a);
      a.click();
      setTimeout(() => {
        URL.revokeObjectURL(a.href);
        a.remove();
      }, 1000);
      message.success('已生成配置，请导入 mihomo Alpha 内核');
    } catch (e) {
      message.error(e.message || '下载失败');
    } finally {
      setBusy(false);
    }
  };

  const data = state.data || {};

  return (
    <div style={{ padding: mobile ? 0 : 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginBottom: 10 }}>
        <div>
          <b>WARP</b>
          <span className="pp-muted" style={{ marginLeft: 8, fontSize: 12 }}>
            Cloudflare WARP · 需要 mihomo Alpha
          </span>
        </div>
        <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
          刷新
        </Button>
      </div>

      {state.loading && !state.data ? (
        <Skeleton active paragraph={{ rows: 2 }} />
      ) : state.error ? (
        <Alert type="error" showIcon message={state.error.message || '加载失败'} />
      ) : !data.enabled ? (
        <Alert type="info" showIcon message="管理员还没有启用 WARP" />
      ) : !data.granted ? (
        <Alert type="info" showIcon message="管理员还没有为你的账号授权 WARP" />
      ) : (
        <div style={{ border: '1px solid #eef2f7', borderRadius: 12, padding: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <Tag color="green" style={{ marginInlineEnd: 0 }}>已授权</Tag>
            <span style={{ fontSize: 12, color: '#64748b', fontFamily: 'monospace' }}>
              {data.warp_ipv4 || data.warp_ipv6 || ''}
            </span>
          </div>
          <div style={{ fontSize: 12, color: '#94a3b8', lineHeight: 1.7, marginBottom: 10 }}>
            WARP 出口 IP 由 Cloudflare 分配，免费版不支持选择国家。配置文件包含私钥，请勿转发。
          </div>
          <Button type="primary" icon={<CloudDownloadOutlined />} loading={busy} onClick={download}>
            下载 mihomo 配置
          </Button>
        </div>
      )}
    </div>
  );
}
