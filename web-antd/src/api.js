/* =================================================================
 *  API 客户端：统一 JWT（localStorage: token，与 worker.js 老后台共用同一份会话）
 *  · 401 → 清会话 + 广播 unauthorized，由 AuthProvider 弹回登录页
 *  · 非 2xx 或响应体带 error 字段 → 抛 ApiError
 * ================================================================= */

const TOKEN_KEY = 'token';
const TOKEN_EXP_KEY = 'token_exp';
export const authBus = new EventTarget();

export class ApiError extends Error {
  constructor(message, status, payload) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.payload = payload;
  }
}

export function getToken() {
  try {
    return localStorage.getItem(TOKEN_KEY) || '';
  } catch (e) {
    return '';
  }
}

export function setToken(token, ttlMs) {
  try {
    if (token) {
      localStorage.setItem(TOKEN_KEY, token);
      const exp = Date.now() + (ttlMs || 24 * 3600 * 1000);
      localStorage.setItem(TOKEN_EXP_KEY, String(exp));
    } else {
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(TOKEN_EXP_KEY);
    }
  } catch (e) {
    /* 隐私模式下 localStorage 不可用，退化为内存态 */
  }
}

export function clearSession() {
  try {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(TOKEN_EXP_KEY);
    localStorage.removeItem('user');
  } catch (e) {
    /* ignore */
  }
}

/** 不带 Authorization 的公开请求（登录、验证码等） */
export async function publicApi(path, opts = {}) {
  return request(path, opts, false);
}

export async function api(path, opts = {}) {
  return request(path, opts, true);
}

export const apiGet = (path, query) => api(path, { query });
export const apiPost = (path, body) => api(path, { method: 'POST', body });
export const apiPut = (path, body) => api(path, { method: 'PUT', body });
export const apiDelete = (path, query) => api(path, { method: 'DELETE', query });

/** 少数接口不在 /api 下（如 /manage/uuid-sub-sources） */
export function apiRaw(path, opts = {}) {
  return request(path, opts, true, true);
}

async function request(path, opts, withAuth, absolute) {
  const { method = 'GET', body, query, signal } = opts || {};
  const raw = String(path || '');
  let url = absolute || raw.startsWith('/') ? raw : '/api/' + raw.replace(/^\/+/, '');

  if (query) {
    const qs = new URLSearchParams();
    Object.entries(query).forEach(([k, v]) => {
      if (v === undefined || v === null || v === '') return;
      qs.append(k, String(v));
    });
    const s = qs.toString();
    if (s) url += (url.indexOf('?') >= 0 ? '&' : '?') + s;
  }

  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (withAuth) {
    const token = getToken();
    if (token) headers.Authorization = 'Bearer ' + token;
  }

  let res;
  try {
    res = await fetch(url, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
      credentials: 'same-origin',
    });
  } catch (e) {
    if (e && e.name === 'AbortError') throw e;
    throw new ApiError('网络连接失败，请检查网络后重试', 0);
  }

  if (res.status === 401 && withAuth) {
    clearSession();
    authBus.dispatchEvent(new Event('unauthorized'));
    throw new ApiError('登录已过期，请重新登录', 401);
  }

  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch (e) {
      data = { error: text.slice(0, 200) };
    }
  }

  if (!res.ok) {
    throw new ApiError((data && data.error) || `请求失败（HTTP ${res.status}）`, res.status, data);
  }
  if (data && typeof data === 'object' && data.error) {
    throw new ApiError(data.error, res.status, data);
  }
  return data;
}
