/* =================================================================
 *  通用 hooks：数据拉取 / 轮询 / 哈希路由 / Turnstile
 * ================================================================= */
import { useCallback, useEffect, useRef, useState } from 'react';

/** 统一的数据请求状态机：{ data, loading, error, reload } */
export function useAsync(fn, deps = [], options = {}) {
  const { immediate = true } = options;
  const [state, setState] = useState({ data: null, loading: immediate, error: null });
  const seq = useRef(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  const run = useCallback(async () => {
    const id = ++seq.current;
    setState((s) => ({ data: s.data, loading: true, error: null }));
    try {
      const data = await fnRef.current();
      if (id === seq.current) setState({ data, loading: false, error: null });
      return data;
    } catch (e) {
      if (id === seq.current) setState({ data: null, loading: false, error: e });
      throw e;
    }
  }, []);

  useEffect(() => {
    if (immediate) run().catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { ...state, reload: run, setData: (d) => setState({ data: d, loading: false, error: null }) };
}

/** 定时轮询（页面不可见时暂停） */export function usePolling(callback, intervalMs) {
  const cbRef = useRef(callback);
  cbRef.current = callback;
  useEffect(() => {
    if (!intervalMs || intervalMs <= 0) return undefined;
    let timer = null;
    const tick = () => {
      if (document.visibilityState === 'visible') {
        try {
          cbRef.current();
        } catch (e) {
          /* ignore */
        }
      }
    };
    timer = setInterval(tick, intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
}

/**
 * 表单校验：字段不合法时 antd 会 reject，这里吞掉并返回 null，
 * 避免每个 submit 都冒出 unhandled rejection（错误提示仍由 antd 显示在表单上）。
 */
export async function readForm(form) {
  try {
    return await form.validateFields();
  } catch (e) {
    return null;
  }
}

/** 极简哈希路由：/admin#uuids */
export function useHashRoute(fallback = 'dash') {
  const read = () => {
    const raw = String(window.location.hash || '').replace(/^#\/?/, '').trim();
    return raw || fallback;
  };
  const [route, setRoute] = useState(read);
  useEffect(() => {
    const onChange = () => setRoute(read());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fallback]);
  const navigate = useCallback((key) => {
    if (window.location.hash !== '#' + key) window.location.hash = key;
    else setRoute(key);
  }, []);
  return [route, navigate];
}

/**
 * Cloudflare Turnstile：配置了 site key 才加载，否则视为免验证。
 *
 * ⚠️ 取 token 一律用 getToken()（内部调 turnstile.getResponse），而不是依赖 callback
 * 存进 state —— 与服务端 verifyTurnstileRequired 的语义对齐：token 为空会直接 400
 * 「请完成人机验证」，所以必须在提交那一刻现取，和老后台的 turnstileToken() 一致。
 *
 * 用法：把 slotRef 挂到一个 <div> 上；该 div 重新挂载时会自动重建 widget。
 */
export function useTurnstile(siteKey) {
  const configured = !!siteKey && String(siteKey).indexOf('__') !== 0;
  const [slotEl, setSlotEl] = useState(null);
  const [nonce, setNonce] = useState(0);
  const widgetRef = useRef(null);
  const [token, setToken] = useState('');
  const [status, setStatus] = useState(configured ? 'loading' : 'off');

  // 回调 ref：节点挂载/卸载都能感知，避免「widget 渲染时 slot 还不存在」
  const slotRef = useCallback((node) => {
    setSlotEl(node || null);
  }, []);

  useEffect(() => {
    if (!configured || !slotEl) return undefined;
    let cancelled = false;
    let tries = 0;

    const render = () => {
      if (cancelled || widgetRef.current !== null) return;
      const ts = typeof window !== 'undefined' ? window.turnstile : null;
      if (!ts || typeof ts.render !== 'function') {
        if (tries > 120) {
          setStatus('error'); // ~12s 还没加载出来就报错，给出重试入口
          return;
        }
        tries += 1;
        setTimeout(render, 100);
        return;
      }
      try {
        // 只用 sitekey，不加 theme/size：与老后台完全一致的默认参数最稳
        widgetRef.current = ts.render(slotEl, {
          sitekey: siteKey,
          callback: (t) => {
            setToken(t || '');
            setStatus('ready');
          },
          'expired-callback': () => setToken(''),
          'error-callback': () => setStatus('error'),
        });
        setStatus((s) => (s === 'error' ? s : 'ready'));
      } catch (e) {
        setStatus('error');
      }
    };

    if (!window.turnstile) {
      let tag = document.getElementById('cf-turnstile-sdk');
      if (!tag) {
        tag = document.createElement('script');
        tag.id = 'cf-turnstile-sdk';
        tag.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
        tag.async = true;
        tag.defer = true;
        document.head.appendChild(tag);
      }
    }
    render();

    return () => {
      cancelled = true;
      if (widgetRef.current !== null && window.turnstile) {
        try {
          window.turnstile.remove(widgetRef.current);
        } catch (e) {
          /* ignore */
        }
      }
      widgetRef.current = null;
    };
  }, [configured, siteKey, slotEl, nonce]);

  /** 提交时现取 token（最可靠） */
  const getToken = useCallback(() => {
    if (!configured) return '';
    try {
      const v = window.turnstile.getResponse(widgetRef.current);
      if (v) return v;
    } catch (e) {
      /* 取不到就退回 callback 里存的值 */
    }
    return token || '';
  }, [configured, token]);

  const reset = useCallback(() => {
    setToken('');
    try {
      if (widgetRef.current !== null && window.turnstile) window.turnstile.reset(widgetRef.current);
    } catch (e) {
      /* ignore */
    }
  }, []);

  const reload = useCallback(() => {
    setToken('');
    setStatus('loading');
    try {
      if (widgetRef.current !== null && window.turnstile) window.turnstile.remove(widgetRef.current);
    } catch (e) {
      /* ignore */
    }
    widgetRef.current = null;
    setNonce((n) => n + 1);
  }, []);

  return { token, getToken, reset, reload, slotRef, configured, status };
}
