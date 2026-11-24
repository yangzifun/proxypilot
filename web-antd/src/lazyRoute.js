/* =================================================================
 *  懒加载路由的 chunk 失效恢复
 *  滚动部署后旧 index.html/app.js 仍引用旧 hash 的 chunks/*.js，旧文件被删除后
 *  动态 import 会失败。这里在 chunk 加载失败时自动刷新一次（10 秒窗口，防死循环），
 *  第二次失败交给 PageErrorBoundary 展示友好提示。
 * ================================================================= */
import { lazy } from 'react';

export const CHUNK_RELOAD_KEY = 'pp.chunk.reload.at';
export const CHUNK_RELOAD_WINDOW_MS = 10000;

const CHUNK_ERROR_RE = /Failed to fetch dynamically imported module|Importing a module script failed|ChunkLoadError|Loading chunk [\w.-]+ failed|error loading dynamically imported module/i;

/** 判断是否为浏览器/打包器返回的“chunk 加载失败”。 */
export function isChunkLoadError(error) {
  if (!error) return false;
  const name = String(error.name || '');
  const msg = String(
    error.message
    || (error.reason && (error.reason.message || error.reason))
    || error
    || ''
  );
  return /^ChunkLoadError$/i.test(name) || CHUNK_ERROR_RE.test(msg);
}

function getSessionStorage() {
  try {
    return typeof window !== 'undefined' ? window.sessionStorage : null;
  } catch (e) {
    return null;
  }
}

/** 10 秒内是否已经因 chunk 失败自动刷新过。 */
export function hasRecentChunkReload(now = Date.now()) {
  const storage = getSessionStorage();
  if (!storage) return false;
  try {
    const at = Number(storage.getItem(CHUNK_RELOAD_KEY));
    return Number.isFinite(at) && at > 0 && now - at < CHUNK_RELOAD_WINDOW_MS;
  } catch (e) {
    return false;
  }
}

export function markChunkReload(now = Date.now()) {
  const storage = getSessionStorage();
  if (!storage) return false;
  try {
    storage.setItem(CHUNK_RELOAD_KEY, String(now));
    return true;
  } catch (e) {
    return false;
  }
}

export function clearChunkReloadMark() {
  const storage = getSessionStorage();
  if (!storage) return;
  try {
    storage.removeItem(CHUNK_RELOAD_KEY);
  } catch (e) {
    /* ignore */
  }
}

/**
 * 首次 chunk 失败时自动 reload；10 秒内已经 reload 过则不再 reload。
 * @returns {boolean} 是否触发了 reload
 */
export function recoverChunkLoad() {
  if (hasRecentChunkReload()) return false;
  markChunkReload();
  try {
    if (typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
      window.location.reload();
      return true;
    }
  } catch (e) {
    /* ignore */
  }
  return false;
}

/**
 * React.lazy 的 chunk 失败重试包装：
 * - 第一次 chunk 失败：标记 sessionStorage + reload，返回永不 resolve 的 Promise，
 *   保持 Suspense loading，避免错误边界闪烁；浏览器刷新后加载新版本。
 * - 10 秒窗口内再次失败（reload 后仍失败）：直接 throw，交给 PageErrorBoundary 显示友好提示。
 */
export function lazyWithRetry(loader) {
  return lazy(async () => {
    try {
      const mod = await loader();
      // 成功加载新版本后清掉标记，后续部署仍可再次自动恢复。
      clearChunkReloadMark();
      return mod;
    } catch (error) {
      if (isChunkLoadError(error) && !hasRecentChunkReload()) {
        markChunkReload();
        try {
          if (typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
            window.location.reload();
            return new Promise(() => {});
          }
        } catch (e) {
          /* reload 失败则继续 throw，由错误边界兜底 */
        }
      }
      throw error;
    }
  });
}