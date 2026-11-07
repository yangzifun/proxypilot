import { useEffect, useState } from 'react';

const SCALE_KEY = 'pp.uiScale';
const SCALE_EVENT = 'pp-scale-change';

export const SCALE_OPTIONS = [
  { value: 1, label: '100%' },
  { value: 0.95, label: '95%' },
  { value: 0.92, label: '92%' },
  { value: 0.88, label: '88%' },
  { value: 0.84, label: '84%' },
  { value: 0.8, label: '80%' },
];

export function getScale() {
  try {
    const v = Number(localStorage.getItem(SCALE_KEY));
    if (v >= 0.6 && v <= 1.2) return v;
  } catch (e) {
    /* ignore */
  }
  return 0.92; // 默认略缩小，视觉更舒适
}

export function applyScale(scale) {
  const v = Number(scale) || 1;
  const root = document.documentElement;
  // 用 zoom 做等比缩放：浏览器会同时缩放布局与文字，避免 px 单位的样式失效
  root.style.zoom = String(v);
  root.style.setProperty('--pp-zoom', String(v));
  // Firefox 不支持 zoom，退回 transform 方案
  if (typeof root.style.zoom === 'undefined') {
    document.body.style.transform = 'scale(' + v + ')';
    document.body.style.transformOrigin = 'top left';
    document.body.style.width = 100 / v + '%';
  }
}

export function setScale(scale) {
  try {
    localStorage.setItem(SCALE_KEY, String(scale));
  } catch (e) {
    /* ignore */
  }
  applyScale(scale);
  window.dispatchEvent(new CustomEvent(SCALE_EVENT, { detail: scale }));
}

export function subscribeScale(fn) {
  const handler = (e) => fn(e.detail);
  window.addEventListener(SCALE_EVENT, handler);
  return () => window.removeEventListener(SCALE_EVENT, handler);
}

/** 缩放选择器用的受控 hook */
export function useScale() {
  const [scale, setLocal] = useState(getScale);
  useEffect(() => subscribeScale((v) => setLocal(v)), []);
  return [scale, (v) => { setScale(v); setLocal(v); }];
}
