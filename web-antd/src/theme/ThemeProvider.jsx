/* =================================================================
 *  ThemeProvider · 全局唯一 ConfigProvider + 主题上下文
 *  · 站点主题（light/emerald/pulse/horizon/glass）驱动 AntD token 与 --pp-* 变量
 *  · 子应用（后台 App / 用户中心 Portal）都在同一 ConfigProvider 下，避免双层主题
 * ================================================================= */
import React, { createContext, useContext, useEffect, useMemo, useState } from 'react';
import { ConfigProvider } from 'antd';
import zhCNraw from 'antd/locale/zh_CN';
import { buildAntdTheme, getThemeVars, isDarkTheme, SITE_THEMES, DEFAULT_THEME } from './tokens';
import { getTheme, applyTheme, subscribeTheme } from './store';

const zhCN = zhCNraw && zhCNraw.default && zhCNraw.default.Modal ? zhCNraw.default : zhCNraw;

const ThemeCtx = createContext(null);

function buildContextValue(name) {
  const themeName = SITE_THEMES[name] ? name : DEFAULT_THEME;
  const theme = SITE_THEMES[themeName];
  const vars = getThemeVars(themeName);
  const tokens = buildAntdTheme(themeName);
  const colors = {
    bg: vars['--pp-bg'],
    bgElevated: vars['--pp-bg-elevated'],
    surface: vars['--pp-surface'],
    surfaceMuted: vars['--pp-surface-muted'],
    border: vars['--pp-border'],
    text: vars['--pp-text'],
    textSecondary: vars['--pp-text-secondary'],
    textTertiary: vars['--pp-text-tertiary'],
    primary: vars['--pp-primary'],
    primarySoft: vars['--pp-primary-soft'],
    success: vars['--pp-success'],
    warning: vars['--pp-warning'],
    danger: vars['--pp-danger'],
    info: vars['--pp-info'],
    tableHeaderBg: vars['--pp-table-header-bg'],
    mask: vars['--pp-mask'],
    radiusMd: vars['--pp-radius-md'],
  };
  const palette = Array.from({ length: 8 }, (_, i) => vars[`--pp-chart-${i + 1}`]);
  return {
    name: themeName,
    label: theme.label,
    isDark: isDarkTheme(themeName),
    colors,
    palette,
    vars,
    tokens,
  };
}

export function ThemeProvider({ children }) {
  const [name, setName] = useState(() => getTheme());

  useEffect(() => {
    applyTheme(name);
    return subscribeTheme((t) => {
      setName(t);
      applyTheme(t);
    });
    // 仅挂载时订阅；主题变化通过 subscribeTheme 回调推送
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const value = useMemo(() => buildContextValue(name), [name]);
  const antdTheme = useMemo(() => ({ token: value.tokens.token, components: value.tokens.components }), [value]);

  return (
    <ThemeCtx.Provider value={value}>
      <ConfigProvider locale={zhCN} theme={antdTheme} button={{ autoInsertSpace: false }}>
        {children}
      </ConfigProvider>
    </ThemeCtx.Provider>
  );
}

/** 读取当前主题：{ name, label, isDark, colors, palette, vars, tokens } */
export function useAppTheme() {
  const ctx = useContext(ThemeCtx);
  if (ctx) return ctx;
  // 无 Provider（测试/边缘场景）时返回当前持久化主题的静态值，避免崩溃
  return useMemo(() => buildContextValue(getTheme()), []);
}

export default ThemeProvider;