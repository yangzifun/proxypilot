/* =================================================================
 *  Ant Design 主题：把品牌色 / 圆角 / 字体统一收口
 *  图表配色也从这里取，保证图和组件是同一套视觉语言
 * ================================================================= */
export const brand = {
  primary: '#4f46e5',
  primarySoft: '#eef2ff',
  cyan: '#06b6d4',
  green: '#10b981',
  amber: '#f59e0b',
  rose: '#f43f5e',
  violet: '#8b5cf6',
  sky: '#0ea5e9',
  orange: '#f97316',
  slate900: '#0f172a',
  slate700: '#334155',
  slate500: '#64748b',
  slate200: '#e2e8f0',
  line: '#eef2f7',
};

/* 品牌 Logo（对象存储上的白底方形图，直接用圆角裁切当图标） */
export const LOGO_URL = 'https://s3.yangzifun.org/logo.png';

export const chartPalette = [
  brand.primary,
  brand.cyan,
  brand.amber,
  brand.green,
  brand.rose,
  brand.violet,
  brand.sky,
  brand.orange,
];

const fontFamily =
  "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Hiragino Sans GB', " +
  "'Microsoft YaHei', 'Helvetica Neue', Arial, sans-serif";

export const themeConfig = {
  token: {
    colorPrimary: brand.primary,
    colorInfo: brand.primary,
    colorSuccess: brand.green,
    colorWarning: brand.amber,
    colorError: brand.rose,
    colorLink: brand.primary,
    colorBgLayout: '#f6f7fb',
    colorTextBase: '#1e293b',
    borderRadius: 10,
    borderRadiusLG: 14,
    controlHeight: 36,
    fontFamily,
    fontSize: 14,
    wireframe: false,
    boxShadowSecondary:
      '0 6px 24px -8px rgba(15, 23, 42, 0.12), 0 2px 6px -2px rgba(15, 23, 42, 0.06)',
  },
  components: {
    Layout: {
      siderBg: brand.slate900,
      triggerBg: '#151f33',
      headerBg: '#ffffff',
      bodyBg: '#f6f7fb',
      headerHeight: 64,
    },
    Menu: {
      darkItemBg: 'transparent',
      darkSubMenuItemBg: 'transparent',
      darkPopupBg: '#101a2c',
      darkItemColor: '#94a3b8',
      darkItemHoverBg: 'rgba(148, 163, 184, 0.10)',
      darkItemSelectedBg: 'rgba(99, 102, 241, 0.20)',
      darkItemSelectedColor: '#e0e7ff',
      darkGroupTitleColor: '#5b6b85',
      itemHeight: 40,
      itemMarginBlock: 2,
      itemBorderRadius: 10,
      iconSize: 16,
    },
    Card: {
      borderRadiusLG: 14,
      headerBg: 'transparent',
      headerFontSize: 15,
      boxShadowTertiary: '0 1px 2px rgba(15, 23, 42, 0.04)',
    },
    Table: {
      headerBg: '#f8fafc',
      headerColor: '#475569',
      headerSplitColor: 'transparent',
      rowHoverBg: '#f8fafc',
      borderColor: brand.line,
      cellPaddingBlock: 10,
      headerBorderRadius: 10,
    },
    Button: {
      primaryShadow: '0 6px 16px rgba(79, 70, 229, 0.20)',
      fontWeight: 500,
    },
    Statistic: {
      contentFontSize: 26,
      titleFontSize: 13,
    },
    Tag: {
      defaultBg: '#f1f5f9',
      defaultColor: '#475569',
      borderRadiusSM: 6,
    },
    Input: { paddingBlock: 6 },
    Modal: { borderRadiusLG: 16 },
    Drawer: { colorBgElevated: '#ffffff' },
    Segmented: { itemSelectedBg: '#ffffff' },
    Tabs: { horizontalItemPadding: '10px 0' },
  },
};

/* 图表里复用的坐标轴/网格样式 */
export const chartAxisColor = '#94a3b8';
export const chartGridColor = '#eef2f7';

/* ---- 全站主题切换 ---- */
const THEME_KEY = 'pp.theme';
const THEME_EVENT = 'pp-theme-change';

export function getTheme() {
  try {
    return localStorage.getItem(THEME_KEY) || 'light';
  } catch (e) {
    return 'light';
  }
}

export function applyTheme(theme) {
  const t = theme || 'light';
  document.body.classList.remove(
    'pp-site-theme-light', 'pp-site-theme-emerald', 'pp-site-theme-pulse',
    'pp-site-theme-horizon', 'pp-site-theme-glass',
  );
  document.body.classList.add('pp-site-theme-' + t);
}

export function setTheme(theme) {
  const t = theme || 'light';
  try {
    localStorage.setItem(THEME_KEY, t);
  } catch (e) {
    /* ignore */
  }
  applyTheme(t);
  window.dispatchEvent(new CustomEvent(THEME_EVENT, { detail: t }));
}

export function subscribeTheme(fn) {
  const handler = (e) => fn(e.detail || getTheme());
  window.addEventListener(THEME_EVENT, handler);
  return () => window.removeEventListener(THEME_EVENT, handler);
}

export const THEME_OPTIONS = [
  { value: 'light', label: '默认' },
  { value: 'emerald', label: '翠绿' },
  { value: 'pulse', label: '极简' },
  { value: 'horizon', label: '扁平' },
  { value: 'glass', label: '毛玻璃' },
];