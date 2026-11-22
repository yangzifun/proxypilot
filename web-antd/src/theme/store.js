/* 主题持久化与切换（与旧 theme.js 行为兼容） */
import { SITE_THEMES, applyThemeVars, DEFAULT_THEME } from './tokens';

const THEME_KEY = 'pp.theme';
const THEME_EVENT = 'pp-theme-change';

const SITE_CLASSES = ['pp-site-theme-light', 'pp-site-theme-emerald', 'pp-site-theme-pulse', 'pp-site-theme-horizon', 'pp-site-theme-glass'];

export function getTheme() {
  try {
    const t = localStorage.getItem(THEME_KEY) || DEFAULT_THEME;
    return SITE_THEMES[t] ? t : DEFAULT_THEME;
  } catch (e) {
    return DEFAULT_THEME;
  }
}

export function applyTheme(theme) {
  if (typeof document === 'undefined') return;
  const t = SITE_THEMES[theme] ? theme : DEFAULT_THEME;
  const body = document.body;
  if (body && body.classList) {
    SITE_CLASSES.forEach((c) => body.classList.remove(c));
    body.classList.add('pp-site-theme-' + t);
  }
  applyThemeVars(t);
}

export function setTheme(theme) {
  const t = SITE_THEMES[theme] ? theme : DEFAULT_THEME;
  try {
    localStorage.setItem(THEME_KEY, t);
  } catch (e) {
    /* 隐私模式忽略 */
  }
  applyTheme(t);
  try {
    if (typeof window !== 'undefined' && typeof window.CustomEvent === 'function') {
      window.dispatchEvent(new window.CustomEvent(THEME_EVENT, { detail: t }));
    }
  } catch (e) {
    /* ignore */
  }
}

export function subscribeTheme(fn) {
  if (typeof window === 'undefined') return () => {};
  const handler = (e) => fn(e.detail || getTheme());
  window.addEventListener(THEME_EVENT, handler);
  return () => window.removeEventListener(THEME_EVENT, handler);
}