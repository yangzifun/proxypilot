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
  servers: {
    title: '服务器管理',
    subtitle: 'Agent 接入、接入密钥与任务队列',
    icon: <CloudServerOutlined />,
    group: '运维',
    accent: '#0ea5e9',
  },
  normal: {
    title: '普通节点',
    subtitle: '协议模板与节点绑定',
    icon: <NodeIndexOutlined />,
    group: '运维',
    accent: '#8b5cf6',
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
  status: {
    title: '服务器监控',
    subtitle: '在线率、资源占用与服务器明细',
    icon: <LineChartOutlined />,
    group: '运维',
    accent: '#f59e0b',
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
    superOnly: true,
  },
  backup: {
    title: '备份管理',
    subtitle: '数据库 / 桶文件 / Agent 分发导出与导入',
    icon: <CloudDownloadOutlined />,
    group: '系统',
    accent: '#16a34a',
    superOnly: true,
  },
  sec: {
    title: '安全中心',
    subtitle: '修改密码、双因素认证与账号安全',
    icon: <SafetyCertificateOutlined />,
    group: '系统',
    accent: '#10b981',
  },
};

export const NAV_ORDER = ['dash', 'uuids', 'tpls', 'servers', 'normal', 'mtproto', 'masque', 'status', 'logs', 'attachments', 'sys', 'backup', 'sec'];

export const NAV_GROUPS = [
  { group: '业务', keys: ['dash', 'uuids', 'tpls'] },
  { group: '运维', keys: ['servers', 'normal', 'mtproto', 'masque', 'status'] },
  { group: '系统', keys: ['logs', 'attachments', 'sys', 'backup', 'sec'] },
];

export function menuItems(isSuper) {
  return NAV_GROUPS.map((g) => {
    const children = g.keys
      .filter((k) => PAGE_META[k])
      .filter((k) => !PAGE_META[k].superOnly || isSuper)
      .map((k) => ({ key: k, icon: PAGE_META[k].icon, label: PAGE_META[k].title }));
    return { type: 'group', key: 'g-' + g.group, label: g.group, children };
  }).filter((g) => g.children.length);
}
