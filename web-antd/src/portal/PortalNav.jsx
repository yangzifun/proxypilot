/* =================================================================
 *  移动端底部导航（PC 不显示）
 *  我的订阅 / 配置管理 / 访问统计
 * ================================================================= */
import React from 'react';
import { AppstoreOutlined, BarChartOutlined, SettingOutlined } from '@ant-design/icons';

export default function PortalNav({ active, onHome, onManage, onStats, manageDisabled }) {
  const items = [
    { key: 'home', icon: <AppstoreOutlined />, label: '我的订阅', onClick: onHome },
    { key: 'manage', icon: <SettingOutlined />, label: '配置管理', onClick: onManage, disabled: manageDisabled },
    { key: 'stats', icon: <BarChartOutlined />, label: '访问统计', onClick: onStats, disabled: manageDisabled },
  ];
  return (
    <nav className="pp-portal-nav">
      {items.map((it) => (
        <button
          key={it.key}
          type="button"
          className={`pp-nav-item${active === it.key ? ' is-active' : ''}`}
          onClick={it.onClick}
          disabled={it.disabled}
        >
          {it.icon}
          <span>{it.label}</span>
        </button>
      ))}
    </nav>
  );
}
