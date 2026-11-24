/* =================================================================
 *  clientError.js · 前端错误上报（轻量、静默、可去重）
 *  · 仅依赖浏览器全局 fetch，失败静默，绝不抛异常影响页面
 *  · 同一条错误（mode + message + 栈前两行）30 秒内去重
 *  · 每页面生命周期最多上报 20 条
 *  · POST /api/client-errors，keepalive 以便页面卸载时仍可送达
 * ================================================================= */

const DEDUPE_WINDOW_MS = 30 * 1000;
const MAX_REPORTS = 20;
const MAX_DEDUPE_KEYS = 200;

let sentCount = 0;
const sentAt = new Map(); // key -> timestamp

/** 根据 boot 数据或当前路径推断 admin/portal */
export function detectClientMode() {
  try {
    if (typeof window !== 'undefined' && window.__PP_BOOT__ && window.__PP_BOOT__.mode) {
      return window.__PP_BOOT__.mode === 'portal' ? 'portal' : 'admin';
    }
    if (typeof window !== 'undefined' && window.location) {
      return /^\/(me\/?)?$/.test(String(window.location.pathname || '')) ? 'portal' : 'admin';
    }
  } catch (e) {
    /* ignore */
  }
  return 'admin';
}

function safeLocation() {
  try {
    if (typeof window !== 'undefined' && window.location) {
      return {
        href: String(window.location.href || '').slice(0, 600),
        pathname: String(window.location.pathname || ''),
        page: String(window.location.pathname || '') + String(window.location.hash || ''),
      };
    }
  } catch (e) {
    /* ignore */
  }
  return { href: '', pathname: '', page: '' };
}

function pruneDedupe(now) {
  if (sentAt.size <= MAX_DEDUPE_KEYS) return;
  for (const [key, ts] of sentAt) {
    if (now - ts >= DEDUPE_WINDOW_MS) sentAt.delete(key);
  }
}

/**
 * 上报一条前端错误。
 * @param {{message?: string, stack?: string, page?: string, mode?: string, source?: string}} input
 * @returns {boolean} 是否真正发起上报（被去重/超限/失败时 false）
 */
export function reportClientError(input) {
  try {
    if (sentCount >= MAX_REPORTS) return false;
    const err = input && typeof input === 'object' ? input : { message: String(input || '') };
    const message = String(err.message || '未知前端错误').slice(0, 2000);
    const stack = String(err.stack || '').slice(0, 4000);
    const mode = err.mode === 'portal' || err.mode === 'admin' ? err.mode : detectClientMode();
    const loc = safeLocation();
    const page = String(err.page || loc.page || '').slice(0, 300);

    const now = Date.now();
    // 去重键：同一页面 + 相同错误消息（30s 内只报一次；不比较栈行号，避免同一错误
    // 因来源/位置行号不同被重复上报，也保证 boundary 与 window 全局上报可互相去重）
    const key = mode + '|' + page + '|' + message;
    const last = sentAt.get(key);
    if (last && now - last < DEDUPE_WINDOW_MS) return false;
    pruneDedupe(now);
    sentAt.set(key, now);
    sentCount += 1;

    const payload = {
      message,
      stack,
      mode,
      page,
      source: String(err.source || 'manual').slice(0, 40),
      href: loc.href,
      pathname: loc.pathname,
      userAgent: typeof navigator !== 'undefined' ? String(navigator.userAgent || '').slice(0, 500) : '',
      at: new Date(now).toISOString(),
    };

    if (typeof fetch === 'function') {
      try {
        const p = fetch('/api/client-errors', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
          keepalive: true,
        });
        if (p && typeof p.catch === 'function') p.catch(() => {});
      } catch (e) {
        /* 上报失败静默 */
      }
    }
    return true;
  } catch (e) {
    return false;
  }
}

/**
 * 安装全局 window.error / unhandledrejection 监听（幂等）。
 * 与 PageErrorBoundary 的错误通过 message+stack 去重，不会重复上报同一条。
 */
export function installGlobalErrorReporting() {
  try {
    if (typeof window === 'undefined' || !window.addEventListener) return false;
    if (window.__PP_CLIENT_ERROR_REPORTING_INSTALLED__) return true;
    window.__PP_CLIENT_ERROR_REPORTING_INSTALLED__ = true;

    window.addEventListener('error', (event) => {
      try {
        const err = (event && event.error) || null;
        reportClientError({
          message: (err && (err.message || String(err))) || (event && event.message) || 'window error',
          stack: err && err.stack ? String(err.stack) : '',
          source: 'window.error',
        });
      } catch (e) {
        /* ignore */
      }
    });

    window.addEventListener('unhandledrejection', (event) => {
      try {
        const reason = event && event.reason;
        reportClientError({
          message: (reason && (reason.message || String(reason))) || 'unhandled rejection',
          stack: reason && reason.stack ? String(reason.stack) : '',
          source: 'unhandledrejection',
        });
      } catch (e) {
        /* ignore */
      }
    });
    return true;
  } catch (e) {
    return false;
  }
}