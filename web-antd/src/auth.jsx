/* =================================================================
 *  认证上下文
 *  · 会话仍是 worker.js 签发的那枚 JWT（localStorage: token），与老后台互通
 *  · 登录方式：账号密码(+TOTP) / 邮箱验证码(+TOTP) / OIDC 单点
 * ================================================================= */
import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { authBus, clearSession, getToken, publicApi, setToken } from './api';

const AuthCtx = createContext(null);

const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};
const DAY_MS = 24 * 3600 * 1000;

/** 解析 JWT payload（只用于取用户名/角色，签名校验由 Worker 负责） */
export function decodeJwt(token) {
  try {
    const part = String(token || '').split('.')[1];
    if (!part) return null;
    const b64 = part.replace(/-/g, '+').replace(/_/g, '/');
    const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4);
    const bin = atob(padded);
    // 不依赖 TextDecoder：把字节串转成 %XX 再用 decodeURIComponent 解 UTF-8
    let pct = '';
    for (let i = 0; i < bin.length; i++) {
      pct += '%' + ('00' + bin.charCodeAt(i).toString(16)).slice(-2);
    }
    return JSON.parse(decodeURIComponent(pct));
  } catch (e) {
    return null;
  }
}

export function AuthProvider({ children }) {
  const [token, setTokenState] = useState(() => getToken());
  const [unauthorizedTick, setUnauthorizedTick] = useState(0);

  useEffect(() => {
    const onUnauthorized = () => {
      setTokenState('');
      setUnauthorizedTick((n) => n + 1);
    };
    authBus.addEventListener('unauthorized', onUnauthorized);
    return () => authBus.removeEventListener('unauthorized', onUnauthorized);
  }, []);

  // 多标签页同步
  useEffect(() => {
    const onStorage = (e) => {
      if (e.key === 'token') setTokenState(e.newValue || '');
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, []);

  const payload = useMemo(() => (token ? decodeJwt(token) : null), [token]);
  const user = payload && payload.sub ? String(payload.sub) : '';
  const superAdmin = String(boot.superAdmin || 'admin');
  const isSuper = !!user && user === superAdmin;

  const applySession = useCallback((nextToken, ttlMs) => {
    setToken(nextToken, ttlMs || DAY_MS);
    setTokenState(nextToken || '');
  }, []);

  /* ---------- 账号密码 ---------- */
  const loginPassword = useCallback(
    async ({ username, password, totpCode, turnstileToken, mfaToken }) => {
      const stepTwo = !!totpCode;
      const body = stepTwo
        ? { username, mfa_token: mfaToken || '', totp_code: totpCode, step: 'verify_totp' }
        : { username, password, cf_turnstile_response: turnstileToken || '' };
      const res = await publicApi('login', { method: 'POST', body });
      if (res && res.requires_mfa && !res.token) {
        return { needMfa: true, mode: 'totp', mfaToken: res.mfa_token || '' };
      }
      if (res && res.token) {
        applySession(res.token, res.token_ttl_ms);
        return { ok: true };
      }
      throw new Error('登录响应异常');
    },
    [applySession]
  );

  /* ---------- 邮箱验证码 ---------- */  const sendEmailCode = useCallback(async ({ email, turnstileToken }) => {
    return publicApi('login/email-code', {
      method: 'POST',
      body: { email, cf_turnstile_response: turnstileToken || '' },
    });
  }, []);

  const verifyEmailCode = useCallback(
    async ({ email, code }) => {
      const res = await publicApi('login/email-code/verify', { method: 'POST', body: { email, code } });
      if (res && res.token) {
        applySession(res.token, res.token_ttl_ms);
        return { ok: true };
      }
      if (res && res.requires_mfa) {
        return { needMfa: true, mode: 'email', ticket: res.email_login_token };
      }
      throw new Error('登录响应异常');
    },
    [applySession]
  );

  const verifyEmailMfa = useCallback(
    async ({ ticket, totpCode, backupCode }) => {      const res = await publicApi('login/email-code/mfa', {
        method: 'POST',
        body: { email_login_token: ticket, totp_code: totpCode || '', backup_code: backupCode || '' },
      });
      if (res && res.token) {
        applySession(res.token, res.token_ttl_ms);
        return { ok: true };
      }
      throw new Error('登录响应异常');
    },
    [applySession]
  );

  /* ---------- 备份码登录（密码 + 备份码代替 TOTP） ---------- */
  const loginWithBackup = useCallback(
    async ({ mfaToken, backupCode }) => {
      const res = await publicApi('mfa/login-with-backup', {
        method: 'POST',
        body: { mfa_token: mfaToken || '', backup_code: backupCode },
      });
      if (res && res.token) {
        applySession(res.token, res.token_ttl_ms);
        return { ok: true };
      }
      throw new Error('登录响应异常');
    },
    [applySession]
  );

  const logout = useCallback(() => {
    clearSession();
    setTokenState('');
    if (window.location.hash && window.location.hash !== '#dash') {
      window.location.hash = '#dash';
    }
  }, []);

  const value = useMemo(
    () => ({
      token,
      user,
      superAdmin,
      isSuper,
      ready: true,
      unauthorizedTick,
      loginPassword,
      sendEmailCode,
      verifyEmailCode,
      verifyEmailMfa,
      loginWithBackup,
      logout,
    }),
    [
      token,
      user,
      superAdmin,
      isSuper,
      unauthorizedTick,
      loginPassword,
      sendEmailCode,
      verifyEmailCode,
      verifyEmailMfa,
      loginWithBackup,
      logout,
    ]
  );

  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthCtx);
  if (!ctx) throw new Error('useAuth 必须在 AuthProvider 内使用');
  return ctx;
}
