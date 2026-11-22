/* =================================================================
 *  ProxyPilot 主题（兼容层）
 *  · 旧 API 保持不变：brand / chartPalette / themeConfig / chartAxisColor /
 *    chartGridColor / THEME_OPTIONS / THEME_TOKENS / getTheme / setTheme /
 *    subscribeTheme / applyTheme
 *  · 新主题基础设施：theme/tokens（语义变量 + AntD 动态主题）、theme/store、
 *    theme/ThemeProvider（useAppTheme）
 * ================================================================= */
import { buildAntdTheme } from './theme/tokens';

export {
  DEFAULT_THEME,
  SEMANTIC_VARS,
  SITE_THEMES,
  SERVERS_THEMES,
  SITE_THEME_NAMES,
  SERVERS_THEME_NAMES,
  THEME_OPTIONS,
  SERVERS_THEME_OPTIONS,
  THEME_TOKENS,
  buildAntdTheme,
  getThemeVars,
  getServersThemeVars,
  applyThemeVars,
  isDarkTheme,
} from './theme/tokens';

export { getTheme, setTheme, applyTheme, subscribeTheme } from './theme/store';
export { ThemeProvider, useAppTheme } from './theme/ThemeProvider';

/* ---- 品牌色与图表配色（旧引用保持兼容） ---- */
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

export const LOGO_URL = '/favicon.svg';

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

/* 图表坐标轴/网格颜色：默认（light）值；图表组件可继续直接引用 */
export const chartAxisColor = '#94a3b8';
export const chartGridColor = '#eef2f7';

/* AntD 静态主题（旧导出兼容；实际运行时由 ThemeProvider 动态生成） */
export const themeConfig = buildAntdTheme('light');