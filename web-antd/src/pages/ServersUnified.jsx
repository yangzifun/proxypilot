/* =================================================================
 *  服务器（合并原「服务器管理」+「服务器监控」）
 *  · 默认卡片视图（监控卡），可切换表格视图（管理）
 *  · 节点资源占用已并入各服务器卡片
 * ================================================================= */
import React, { useState } from 'react';
import { Segmented } from 'antd';
import Monitor from './Monitor';
import Servers from './Servers';

const VIEW_KEY = 'pp.servers.view';

const readView = () => {
  try {
    return localStorage.getItem(VIEW_KEY) === 'table' ? 'table' : 'cards';
  } catch (e) {
    return 'cards';
  }
};

export default function ServersUnified() {
  const [view, setView] = useState(readView);

  const change = (v) => {
    setView(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch (e) {
      /* ignore */
    }
  };

  return (
    <div>
      <div className="pp-toolbar" style={{ marginBottom: 8 }}>
        <Segmented
          value={view}
          onChange={change}
          options={[
            { label: '卡片视图', value: 'cards' },
            { label: '表格视图', value: 'table' },
          ]}
        />
        <span className="pp-muted" style={{ fontSize: 12 }}>
          {view === 'cards'
            ? '资源、网络、连接与节点统计；点击卡片查看详情'
            : 'Agent 版本 / SSH / 重启 / 更新 / 批量操作'}
        </span>
      </div>
      {view === 'cards' ? <Monitor embedded /> : <Servers embedded />}
    </div>
  );
}
