/* =================================================================
 *  移动端「配置管理」：搜索 + CapsuleTabs 协议筛选 + List + 下拉刷新 + 加载更多
 *  · 只读（节点由管理员维护）
 *  · 右上角入口打开「外部订阅源」弹层（新增 / 启停 / 删除）
 * ================================================================= */
import React, { useEffect, useState } from 'react';
import {
  Button, CapsuleTabs, Empty, List, PullToRefresh, SearchBar, SpinLoading, Tag, Toast,
} from 'antd-mobile';
import { CopyOutlined, LinkOutlined, ReloadOutlined, RightOutlined } from '@ant-design/icons';
import { useUuidConfigs } from './hooks/usePortalData';
import MobileSourcesPopup from './MobileSources';

const PROTO_COLOR = {
  vless: 'primary',
  vmess: 'success',
  trojan: 'warning',
  hysteria2: 'danger',
  tuic: 'primary',
  anytls: 'success',
  shadowsocks: 'default',
  snell: 'default',
};

/* 与后端 getProtocol() 的取值保持一致（ss 链接入库为 shadowsocks） */
const TABS = [
  { key: 'all', title: '全部' },
  { key: 'vless', title: 'VLESS' },
  { key: 'vmess', title: 'VMess' },
  { key: 'trojan', title: 'Trojan' },
  { key: 'hysteria2', title: 'Hysteria2' },
  { key: 'tuic', title: 'TUIC' },
  { key: 'shadowsocks', title: 'SS' },
  { key: 'anytls', title: 'AnyTLS' },
  { key: 'snell', title: 'Snell' },
];

export default function MobileConfigs({ uuid, onUnauthorized }) {
  const h = useUuidConfigs(uuid, onUnauthorized);
  const [tab, setTab] = useState('all');
  const [q, setQ] = useState('');
  const [srcOpen, setSrcOpen] = useState(false);

  /* 切协议 → 清空搜索、回到第一页 */
  useEffect(() => {
    setQ('');
    h.setFilters({ q: '', protocol: tab });
    h.setPage(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  /* 输入停顿 350ms 自动搜索（回车会立即触发一次） */
  useEffect(() => {
    const t = setTimeout(() => {
      if (q.trim() === String(h.filters.q || '').trim()) return;
      h.setFilters((s) => ({ ...s, q }));
      h.setPage(1);
    }, 350);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q]);

  const submit = (v) => {
    setQ(v);
    h.setFilters((s) => ({ ...s, q: v }));
    h.setPage(1);
  };

  const copy = async (text) => {
    try {
      await navigator.clipboard.writeText(text);
      Toast.show({ content: '已复制' });
    } catch (e) {
      Toast.show({ content: '复制失败，请长按选择' });
    }
  };

  const hasMore = h.list.length < h.total;

  return (
    <div className="mcfg">
      <SearchBar
        className="mcfg-search"
        placeholder="搜索备注或配置内容"
        value={q}
        onChange={setQ}
        onSearch={submit}
        onClear={() => submit('')}
      />

      <div className="mcfg-src" role="button" tabIndex={0} onClick={() => setSrcOpen(true)}>
        <span className="mcfg-src-icon">
          <LinkOutlined />
        </span>
        <span className="mcfg-src-text">
          <b>外部订阅源</b>
          <i>合并其它订阅地址</i>
        </span>
        <RightOutlined className="mcfg-src-arrow" />
      </div>

      <CapsuleTabs activeKey={tab} onChange={setTab} className="mcfg-tabs">
        {TABS.map((t) => (
          <CapsuleTabs.Tab key={t.key} title={t.title} />
        ))}
      </CapsuleTabs>

      <div className="mcfg-head">
        <span className="mcfg-count">共 {h.total} 条</span>
        <Button size="mini" fill="none" onClick={h.reload} className="mcfg-refresh">
          <ReloadOutlined /> 刷新
        </Button>
      </div>

      <PullToRefresh onRefresh={h.reload}>
        {h.loading && !h.list.length ? (
          <div className="mstat-loading">
            <SpinLoading color="primary" />
          </div>
        ) : h.error ? (
          <div className="ppm-tip">
            {h.error.message || '加载失败'}
            <div style={{ marginTop: 10 }}>
              <Button size="small" onClick={h.reload}>
                重试
              </Button>
            </div>
          </div>
        ) : h.list.length ? (
          <List className="mcfg-list">
            {h.list.map((c) => {
              const proto = String(c.protocol || '').toLowerCase();
              return (
                <List.Item
                  key={c.id}
                  extra={
                    <button
                      type="button"
                      className="mcfg-copy"
                      aria-label="复制配置"
                      onClick={(e) => {
                        e.stopPropagation();
                        copy(c.config_data || '');
                      }}
                    >
                      <CopyOutlined />
                    </button>
                  }
                >
                  <div className="mcfg-row">
                    <Tag color={PROTO_COLOR[proto] || 'default'} className="mcfg-tag">
                      {String(c.protocol || '—').toUpperCase()}
                    </Tag>
                    <span className="mcfg-remark">{c.remark || '未命名节点'}</span>
                  </div>
                  <div className="mcfg-url">{String(c.config_data || '')}</div>
                </List.Item>
              );
            })}
          </List>
        ) : (
          <Empty description={String(h.filters.q || '').trim() ? '没有匹配的节点' : '这个 UUID 还没有节点'} />
        )}
      </PullToRefresh>

      {hasMore ? (
        <Button block fill="none" loading={h.loading} onClick={() => h.setPage(h.page + 1)} className="mcfg-more">
          加载更多
        </Button>
      ) : null}

      <MobileSourcesPopup uuid={uuid} visible={srcOpen} onClose={() => setSrcOpen(false)} />
    </div>
  );
}
