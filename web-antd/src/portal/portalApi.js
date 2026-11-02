/* =================================================================
 *  用户端 API 客户端
 *  · 令牌存在 localStorage 的 pp_portal_token（与老用户中心一致，互通）
 *  · 只开放 /api/portal/* 这几个接口
 * ================================================================= */
const TOKEN_KEY = 'pp_portal_token';

export function portalToken() {
  try {
    return localStorage.getItem(TOKEN_KEY) || '';
  } catch (e) {
    return '';
  }
}

export function portalSetToken(t) {
  try {
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
  } catch (e) {
    /* ignore */
  }
}

export class PortalError extends Error {
  constructor(message, status) {
    super(message);
    this.name = 'PortalError';
    this.status = status;
  }
}

/**
 * 直接请求 /manage/* 等「非 /api 前缀」的用户端接口（同样带 portal token）
 * 返回 { data, text, status }
 */
export async function portalReq(path, opts = {}) {
  const { method = 'GET', body } = opts;
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const t = portalToken();
  if (t) headers.Authorization = 'Bearer ' + t;

  let res;
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
    });
  } catch (e) {
    throw new PortalError('网络连接失败，请检查网络后重试', 0);
  }

  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch (e) {
      data = null;
    }
  }
  if (!res.ok) {
    if (res.status === 401) portalSetToken('');
    throw new PortalError((data && data.error) || `请求失败（HTTP ${res.status}）`, res.status);
  }
  return { data, text, status: res.status, headers: res.headers };
}

export async function portalApi(path, opts = {}) {
  const { method = 'GET', body, auth = true } = opts;
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (auth) {
    const t = portalToken();
    if (t) headers.Authorization = 'Bearer ' + t;
  }

  let res;
  try {
    res = await fetch('/api/' + String(path).replace(/^\/+/, ''), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
    });
  } catch (e) {
    throw new PortalError('网络连接失败，请检查网络后重试', 0);
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
    if (res.status === 401 && auth) portalSetToken('');
    throw new PortalError((data && data.error) || `请求失败（HTTP ${res.status}）`, res.status);
  }
  if (data && data.error) throw new PortalError(data.error, res.status);
  return data;
}
