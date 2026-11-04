/* =================================================================
 *  导航定义：菜单分组 + 每个页面的标题 / 副标题 / 图标
 * ================================================================= */
import React from 'react';
import {
  ApartmentOutlined,
  CloudDownloadOutlined,
  CloudServerOutlined,
  CloudSyncOutlined,
  DashboardOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  LineChartOutlined,
  NodeIndexOutlined,
  ReadOutlined,
  PartitionOutlined,
  SendOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
} from '@ant-design/icons';

export const PAGE_META = {
  dash: {
    title: '系统概览',
    subtitle: '访问统计、趋势与最新订阅',
    icon: <DashboardOutlined />,
    group: '业务',
    accent: '#4f46e5',
  },
  uuids: {
    title: '用户管理',
    subtitle: '按用户管理节点配置、授权与状态',
    icon: <ApartmentOutlined />,
    group: '业务',
    accent: '#06b6d4',
  },
  tpls: {
    title: '订阅模板',
    subtitle: 'Clash / Mihomo 模板与对象存储文件',
    icon: <FileTextOutlined />,
    group: '业务',
    accent: '#10b981',
  },
  userst: {
    title: '用户流量',
    subtitle: '在线用户 · 今日 / 区间 / 累计用量',
    icon: <LineChartOutlined />,
    group: '业务',
    accent: '#f59e0b',
  },
  servers: {
    title: '服务器',
    subtitle: '在线率、资源监控与 Agent 管理',
    icon: <CloudServerOutlined />,
    group: '运维',
    accent: '#0ea5e9',
  },
  bindings: {
    title: '节点绑定',
    subtitle: '一台服务器同时绑定 sing-box / mihomo / MTProto',
    icon: <ApartmentOutlined />,
    group: '节点运维',
    accent: '#0ea5e9',
  },
  tplhub: {
    title: '协议模板',
    subtitle: 'sing-box / mihomo / MTProto 模板配置',
    icon: <ReadOutlined />,
    group: '节点运维',
    accent: '#8b5cf6',
  },
  normal: {
    title: 'singbox',
    subtitle: 'sing-box 节点 · 协议模板与节点绑定',
    icon: <NodeIndexOutlined />,
    group: '节点运维',
    accent: '#8b5cf6',
  },
  mihomo: {
    title: 'mihomo',
    subtitle: 'mihomo 模板管理 · 按服务器绑定节点',
    icon: <PartitionOutlined />,
    group: '节点运维',
    accent: '#10b981',
  },
  mtproto: {
    title: 'MTProto',
    subtitle: '节点绑定与 MTProto 协议模板',
    icon: <SendOutlined />,
    group: '运维',
    accent: '#0ea5e9',
  },
  masque: {
    title: 'WARP',
    subtitle: 'Cloudflare WARP · 按 UUID 授权',
    icon: <CloudSyncOutlined />,
    group: '运维',
    accent: '#22c55e',
  },
  logs: {
    title: '日志管理',
    subtitle: '下发记录、访问日志与清理维护',
    icon: <FileSearchOutlined />,
    group: '系统',
    accent: '#f97316',
  },
  sys: {
    title: '系统管理',
    subtitle: '管理员账号、API 密钥与系统设置',
    icon: <SettingOutlined />,
    group: '系统',
    accent: '#f43f5e',
    superOnly: true,
  },
  attachments: {
    title: '附件管理',
    subtitle: 'R2 对象存储（含 agent）上传 / 下载 / 删除',
    icon: <FolderOpenOutlined />,
    group: '系统',
    accent: '#0ea5e9',
  },
  backup: {
    title: '备份管理',
    subtitle: '数据库 / 桶文件 / Agent 分发导出与导入',
    icon: <CloudDownloadOutlined />,
    group: '系统',
    accent: '#16a34a',
  },
  sec: {
    title: '安全中心',
    subtitle: '修改密码、双因素认证与账号安全',
    icon: <SafetyCertificateOutlined />,
    group: '系统',
    accent: '#10b981',
  },
};

export const NAV_ORDER = ['dash', 'uuids', 'tpls', 'userst', 'servers', 'normal', 'mtproto', 'masque', 'logs', 'attachments', 'sys', 'backup', 'sec'];

export const NAV_GROUPS = [
  { group: '业务', keys: ['dash', 'uuids', 'tpls', 'userst'] },
  { group: '服务器运维', keys: ['servers'] },
  { group: '节点运维', keys: ['bindings', 'tplhub', 'masque'] },
  { group: '系统', keys: ['logs', 'attachments', 'sys', 'backup', 'sec'] },
];

export function menuItems(isSuper) {
  const items = [];
  for (const g of NAV_GROUPS) {
    const children = g.keys
      .filter((k) => PAGE_META[k])
      .filter((k) => !PAGE_META[k].superOnly || isSuper)
      .map((k) => ({ key: k, icon: PAGE_META[k].icon, label: PAGE_META[k].title }));
    items.push(...children);
  }
  return items;
}
