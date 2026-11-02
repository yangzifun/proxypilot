/* =================================================================
 *  格式化小工具（时间 / 数字 / 字节 / 时长）
 * ================================================================= */
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';

dayjs.extend(relativeTime);

const nf = new Intl.NumberFormat('zh-CN');

export function fmtNum(n) {
  if (n === null || n === undefined || n === '') return '—';
  const v = Number(n);
  if (!Number.isFinite(v)) return String(n);
  return nf.format(v);
}

/** 万级缩写：12000 → 1.2万 */
export function fmtCompact(n) {
  if (n === null || n === undefined || n === '') return '—';
  const v = Number(n);
  if (!Number.isFinite(v)) return String(n);
  if (Math.abs(v) >= 1e8) return (v / 1e8).toFixed(1).replace(/\.0$/, '') + '亿';
  if (Math.abs(v) >= 1e4) return (v / 1e4).toFixed(1).replace(/\.0$/, '') + '万';
  return nf.format(v);
}

/** 毫秒时间戳 / ISO 字符串 → 2026-10-28 15:30 */
export function fmtTime(value, fallback = '—') {
  if (value === null || value === undefined || value === '' || value === 0) return fallback;
  const d = typeof value === 'number' ? dayjs(value) : dayjs(String(value));
  return d.isValid() ? d.format('YYYY-MM-DD HH:mm') : fallback;
}

export function fmtDate(value, fallback = '—') {
  if (!value) return fallback;
  const d = typeof value === 'number' ? dayjs(value) : dayjs(String(value));
  return d.isValid() ? d.format('YYYY-MM-DD') : fallback;
}

/** 相对时间：3 分钟前 */
export function fmtAgo(value, fallback = '—') {
  if (!value || value === 0) return fallback;
  const d = typeof value === 'number' ? dayjs(value) : dayjs(String(value));
  if (!d.isValid()) return fallback;
  const diff = Date.now() - d.valueOf();
  if (diff < 0) return d.format('MM-DD HH:mm');
  if (diff < 60 * 1000) return '刚刚';
  return d.fromNow();
}

export function fmtBytes(bytes) {
  const n = Number(bytes) || 0;
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
  return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}

/** 秒 → 12天 3小时 */
export function fmtUptime(sec) {
  const s = Number(sec) || 0;
  if (s <= 0) return '—';
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分`;
  return `${m} 分`;
}

export function clampPct(n) {
  const v = Number(n);
  if (!Number.isFinite(v)) return 0;
  return Math.min(100, Math.max(0, v));
}

/** 0~100 数值 → 颜色（绿 / 琥珀 / 玫红） */
export function levelColor(pct) {
  const v = Number(pct) || 0;
  if (v >= 90) return '#f43f5e';
  if (v >= 75) return '#f59e0b';
  if (v >= 50) return '#0ea5e9';
  return '#10b981';
}

export function shortUuid(uuid) {
  const s = String(uuid || '');
  if (s.length <= 16) return s;
  return s.slice(0, 8) + '…' + s.slice(-4);
}

/**
 * 服务器地址拆成 IPv4 / IPv6 两个槽位。
 * 后端三列：last_ip4 / last_ip6（Agent 上报的本机地址）、last_ip（Agent 连 CF 时的出口地址）。
 * 老 Agent 只报 last_ip + last_ip6 → 出口是 IPv6 时把 IPv4 丢了，所以这里做兼容：
 *   · last_ip4 优先；没有就看 last_ip 是不是 IPv4；
 *   · last_ip6 优先；没有就看 last_ip 是不是 IPv6。
 * 返回 { v4, v6 }，各自可能为空串。
 */
export function splitIps(row) {
  const s = row || {};
  const raw = String(s.last_ip || '').trim();
  const rawIs6 = raw.includes(':');
  const v4 = String(s.last_ip4 || '').trim() || (!rawIs6 ? raw : '');
  const v6 = String(s.last_ip6 || '').trim() || (rawIs6 ? raw : '');
  return { v4, v6 };
}

/** 把 [{date,total,subscription,api_generation}] 转成图表行（按日期升序） */
export function dailyRows(daily) {
  const rows = Array.isArray(daily) ? daily.slice() : [];
  rows.sort((a, b) => String(a.date).localeCompare(String(b.date)));
  return rows.map((r) => ({
    label: String(r.date || '').slice(5),
    date: r.date,
    total: Number(r.total) || 0,
    subscription: Number(r.subscription) || 0,
    api_generation: Number(r.api_generation) || 0,
    unique_uuids: Number(r.unique_uuids) || 0,
  }));
}
