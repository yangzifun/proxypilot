/* =================================================================
 *  导航定义：菜单分组 + 每个页面的标题 / 副标题 / 图标
 * ================================================================= */
import React from 'react';
import {
  AppstoreOutlined,
  CloudServerOutlined,
  CloudSyncOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  DeploymentUnitOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  GlobalOutlined,
  LineChartOutlined,
  NodeIndexOutlined,
  PartitionOutlined,
  ProfileOutlined,
  SafetyCertificateOutlined,
  SendOutlined,
  SettingOutlined,
  TeamOutlined,
  ToolOutlined,
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
    icon: <TeamOutlined />,
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
    icon: <DeploymentUnitOutlined />,
    group: '节点运维',
    accent: '#0ea5e9',
  },
  tplhub: {
    title: '协议模板',
    subtitle: 'sing-box / mihomo / MTProto 模板配置',
    icon: <ProfileOutlined />,
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
  cdnpref: {
    title: 'CDN 优选',
    subtitle: 'CDN 节点优选地址池与订阅替换',
    icon: <GlobalOutlined />,
    group: '节点运维',
    accent: '#0ea5e9',
  },
  tlimit: {
    title: '流量限额',
    subtitle: '按服务器设置上行/下行流量上限与按月重置',
    icon: <ToolOutlined />,
    group: '服务器运维',
    accent: '#0ea5e9',
  },
  logs: {
    title: '日志管理',
    subtitle: '下发记录、访问日志与清理维护',
    icon: <FileSearchOutlined />,
    group: '系统',
    accent: '#f97316',
  },
  local: {
    title: '本机服务器',
    subtitle: '控制服务器：SSH 防护、隧道、端口、进程与文件',
    icon: <CloudServerOutlined />,
    group: '系统',
    accent: '#0ea5e9',
    superOnly: true,
  },
  sysusers: {
    title: '用户管理',
    subtitle: '管理员账号、邮箱与双因素认证',
    icon: <TeamOutlined />,
    group: '系统',
    accent: '#6366f1',
    superOnly: true,
  },
  syssettings: {
    title: '系统设置',
    subtitle: '站点、订阅、Agent 与通知等全局配置',
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
    icon: <DatabaseOutlined />,
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

export const NAV_ORDER = ['dash', 'uuids', 'userst', 'tpls', 'tplhub', 'servers', 'bindings', 'cdnpref', 'masque', 'logs', 'attachments', 'backup', 'sec', 'sysusers', 'syssettings'];

/** 侧边栏结构：置顶单页 + 3 个可折叠分组，按日常使用频率排序 */
export const NAV_GROUPS = [
  {
    key: 'biz',
    title: '用户与业务',
    icon: <TeamOutlined />,
    keys: ['uuids', 'userst', 'tpls', 'tplhub'],
  },
  {
    key: 'net',
    title: '节点与网络',
    icon: <DeploymentUnitOutlined />,
    keys: ['servers', 'bindings', 'cdnpref', 'masque'],
  },
  {
    key: 'ops',
    title: '运维与安全',
    icon: <ToolOutlined />,
    keys: ['local', 'logs', 'attachments', 'backup', 'sec', 'sysusers', 'syssettings'],
  },
];

/** 不属于任何分组、直接置顶展示的页面 */
export const NAV_TOP = ['dash'];

/** 当前页面所属分组 key */
export function groupOf(activeKey) {
  const hit = NAV_GROUPS.find((g) => g.keys.includes(activeKey));
  return hit ? hit.key : null;
}

export function menuItems(isSuper, collapsed = false) {
  const visible = (k) => PAGE_META[k] && (!PAGE_META[k].superOnly || isSuper);
  const leaf = (k) => ({ key: k, icon: PAGE_META[k].icon, label: PAGE_META[k].title });

  const items = NAV_TOP.filter(visible).map(leaf);
  for (const g of NAV_GROUPS) {
    const children = g.keys.filter(visible).map(leaf);
    if (!children.length) continue;
    items.push({
      key: g.key,
      icon: g.icon,
      label: g.title,
      // 折叠状态下分组标题不可见，改用分组图标 + 弹出子菜单
      children,
    });
  }
  return items;
}
