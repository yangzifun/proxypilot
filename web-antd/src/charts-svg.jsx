/* =================================================================
 *  charts.jsx — 轻量自绘 SVG 图表
 *
 *  为什么不用 @ant-design/plots / echarts：
 *    前端要整体内联进 Cloudflare Worker（脚本体积有上限），
 *    antd 已经占了大部分预算；G2 / echarts 动辄 +1MB。
 *    这里用 ~300 行 SVG 实现面积图 / 柱状图 / 环形图 / 仪表盘 / 迷你走势图，
 *    配色直接继承 antd 主题 token，观感与组件库统一，且零外部请求。
 * ================================================================= */
import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { chartAxisColor, chartGridColor, chartPalette } from './theme';

const nf = new Intl.NumberFormat('zh-CN');

function fmtCompact(n) {
  const v = Number(n) || 0;
  if (Math.abs(v) >= 1e8) return (v / 1e8).toFixed(1).replace(/\.0$/, '') + '亿';
  if (Math.abs(v) >= 1e4) return (v / 1e4).toFixed(1).replace(/\.0$/, '') + '万';
  return nf.format(v);
}

/* ---------------- 基础工具 ---------------- */

/** 监听容器宽度，图表等比自适应 */
function useWidth() {
  const ref = useRef(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return undefined;
    const measure = () => setW(el.clientWidth || 0);
    measure();
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', measure);
      return () => window.removeEventListener('resize', measure);
    }
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, w];
}

/** 把任意 max 收成「好看」的刻度步长 */
function niceStep(raw) {
  if (!(raw > 0)) return 1;
  const exp = Math.floor(Math.log10(raw));
  const base = Math.pow(10, exp);
  const n = raw / base;
  const mult = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10;
  return mult * base;
}

/** Catmull-Rom 转贝塞尔，得到平滑折线；控制点纵向夹紧避免过冲 */
function smoothPath(pts, clampTop, clampBottom) {
  if (!pts.length) return '';
  if (pts.length === 1) return `M${pts[0][0]},${pts[0][1]}`;
  const cy = (v) => Math.min(clampBottom, Math.max(clampTop, v));
  let d = `M${pts[0][0]},${pts[0][1]}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[i - 1] || pts[i];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[i + 2] || p2;
    const c1x = p1[0] + (p2[0] - p0[0]) / 6;
    const c1y = cy(p1[1] + (p2[1] - p0[1]) / 6);
    const c2x = p2[0] - (p3[0] - p1[0]) / 6;
    const c2y = cy(p2[1] - (p3[1] - p1[1]) / 6);
    d += ` C${c1x.toFixed(2)},${c1y.toFixed(2)} ${c2x.toFixed(2)},${c2y.toFixed(2)} ${p2[0].toFixed(2)},${p2[1].toFixed(2)}`;
  }
  return d;
}

function polar(cx, cy, r, angleDeg) {
  const a = ((angleDeg - 90) * Math.PI) / 180;
  return [cx + r * Math.cos(a), cy + r * Math.sin(a)];
}

function arcPath(cx, cy, r, startDeg, endDeg) {
  const [x1, y1] = polar(cx, cy, r, startDeg);
  const [x2, y2] = polar(cx, cy, r, endDeg);
  const large = Math.abs(endDeg - startDeg) > 180 ? 1 : 0;
  const sweep = endDeg > startDeg ? 1 : 0;
  return `M${x1.toFixed(2)},${y1.toFixed(2)} A${r},${r} 0 ${large} ${sweep} ${x2.toFixed(2)},${y2.toFixed(2)}`;
}

/* ---------------- 图例 ---------------- */

export function ChartLegend({ items, style }) {
  return (
    <div className="pp-legend" style={style}>
      {items.map((it) => (
        <span className="pp-legend-item" key={it.name}>
          <i className="pp-legend-dot" style={{ background: it.color }} />
          {it.name}
          {it.value !== undefined && <b className="pp-legend-val">{it.value}</b>}
        </span>
      ))}
    </div>
  );
}

/* ---------------- 面积图 ---------------- */

export function AreaChart({
  data,
  series,
  height = 260,
  unit = '',
  formatValue = fmtCompact,
  minTickGap = 56,
}) {
  const [ref, w] = useWidth();
  const uid = useId().replace(/[^a-zA-Z0-9]/g, '');
  const [hover, setHover] = useState(null);

  const pad = { top: 16, right: 16, bottom: 26, left: 46 };
  const innerW = Math.max(0, w - pad.left - pad.right);
  const innerH = Math.max(0, height - pad.top - pad.bottom);

  const maxValue = useMemo(() => {
    let m = 0;
    (data || []).forEach((row) => {
      series.forEach((s) => {
        m = Math.max(m, Number(row[s.key]) || 0);
      });
    });
    return m;
  }, [data, series]);

  const step = niceStep(Math.max(1, maxValue) / 4);
  const top = step * 4;

  const n = (data || []).length;
  const xAt = useCallback(
    (i) => (n <= 1 ? pad.left + innerW / 2 : pad.left + (innerW * i) / (n - 1)),
    [n, innerW, pad.left]
  );
  const yAt = useCallback(
    (v) => pad.top + innerH - (Math.min(Math.max(Number(v) || 0, 0), top) / top) * innerH,
    [innerH, top, pad.top]
  );

  const ticks = [0, 1, 2, 3, 4].map((i) => i * step);
  const xTickEvery = Math.max(1, Math.ceil(minTickGap / (innerW / Math.max(1, n - 1))));

  const handleMove = (e) => {
    if (!n) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const x = e.clientX - rect.left;
    if (innerW <= 0) return;
    const ratio = (x - pad.left) / innerW;
    const idx = Math.round(ratio * Math.max(1, n - 1));
    setHover(Math.max(0, Math.min(n - 1, idx)));
  };

  const hoverRow = hover !== null && data ? data[hover] : null;

  return (
    <div className="pp-chart" ref={ref} style={{ height }}>
      {w > 0 && (
        <svg
          width={w}
          height={height}
          role="img"
          aria-label="趋势面积图"
          onMouseMove={handleMove}
          onMouseLeave={() => setHover(null)}
          style={{ display: 'block', overflow: 'visible' }}
        >
          <defs>
            {series.map((s, si) => (
              <linearGradient key={s.key} id={`${uid}-g${si}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color || chartPalette[si]} stopOpacity="0.26" />
                <stop offset="100%" stopColor={s.color || chartPalette[si]} stopOpacity="0.01" />
              </linearGradient>
            ))}
          </defs>


          {ticks.map((t) => (
            <g key={t}>
              <line
                x1={pad.left}
                x2={pad.left + innerW}
                y1={yAt(t)}
                y2={yAt(t)}
                stroke={chartGridColor}
                strokeWidth="1"
                strokeDasharray={t === 0 ? undefined : '3 4'}
              />
              <text x={pad.left - 10} y={yAt(t) + 4} textAnchor="end" fontSize="11" fill={chartAxisColor}>
                {formatValue(t)}
              </text>
            </g>
          ))}

          {(data || []).map((row, i) => {
            // 最后一个标签如果离前一个太近就不再画，避免「10-26 29」糊在一起
            if (i !== 0 && i !== n - 1 && i % xTickEvery !== 0) return null;
            if (i === n - 1) {
              const prev = Math.floor((n - 1) / xTickEvery) * xTickEvery;
              const gap = (i - prev) * (innerW / Math.max(1, n - 1));
              if (prev !== i && gap < minTickGap * 0.9) return null;
            }
            return (
              <text
                key={row.label + i}
                x={xAt(i)}
                y={height - 6}
                textAnchor={i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle'}
                fontSize="11"
                fill={chartAxisColor}
              >
                {row.label}
              </text>
            );
          })}

          {series.map((s, si) => {
            const color = s.color || chartPalette[si];
            const pts = (data || []).map((row, i) => [xAt(i), yAt(row[s.key])]);
            if (!pts.length) return null;
            const line = smoothPath(pts, pad.top, pad.top + innerH);
            const area = `${line} L${pts[pts.length - 1][0]},${pad.top + innerH} L${pts[0][0]},${pad.top + innerH} Z`;
            return (
              <g key={s.key}>
                <path d={area} fill={`url(#${uid}-g${si})`} />
                <path
                  d={line}
                  fill="none"
                  stroke={color}
                  strokeWidth="2.2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </g>
            );
          })}

          {hoverRow && (
            <g>
              <line
                x1={xAt(hover)}
                x2={xAt(hover)}
                y1={pad.top}
                y2={pad.top + innerH}
                stroke="#94a3b8"
                strokeWidth="1"
                strokeDasharray="3 3"
              />
              {series.map((s, si) => (
                <circle
                  key={s.key}
                  cx={xAt(hover)}
                  cy={yAt(hoverRow[s.key])}
                  r="4.5"
                  fill="#fff"
                  stroke={s.color || chartPalette[si]}
                  strokeWidth="2.5"
                />
              ))}
            </g>
          )}
        </svg>
      )}

      {hoverRow && (
        <div
          className="pp-tooltip"
          style={{
            left: Math.min(Math.max(xAt(hover), 74), Math.max(74, w - 74)),
            top: 4,
          }}
        >
          <div className="pp-tooltip-title">{hoverRow.fullLabel || hoverRow.label}</div>
          {series.map((s, si) => (
            <div className="pp-tooltip-row" key={s.key}>
              <i style={{ background: s.color || chartPalette[si] }} />
              <span>{s.name}</span>
              <b>
                {nf.format(Number(hoverRow[s.key]) || 0)}
                {unit}
              </b>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/* ---------------- 柱状图 ---------------- */

export function ColumnChart({ data, series, height = 240, unit = '', formatValue = fmtCompact, referenceLine, yMax }) {
  const [ref, w] = useWidth();
  const uid = useId().replace(/[^a-zA-Z0-9]/g, '');
  const [hover, setHover] = useState(null);

  const pad = { top: 18, right: 12, bottom: 30, left: 46 };
  const innerW = Math.max(0, w - pad.left - pad.right);
  const innerH = Math.max(0, height - pad.top - pad.bottom);

  const maxValue = useMemo(() => {
    let m = 0;
    (data || []).forEach((row) => {
      series.forEach((s) => {
        m = Math.max(m, Number(row[s.key]) || 0);
      });
    });
    return m;
  }, [data, series]);

  const step = niceStep(Math.max(1, maxValue) / 4);
  const top = Number(yMax) > 0 ? Number(yMax) : step * 4;
  const n = (data || []).length;
  const slot = n ? innerW / n : innerW;
  const groupW = Math.min(slot * 0.62, 46);
  const barW = Math.max(5, (groupW - (series.length - 1) * 4) / series.length);
  const ticks = Number(yMax) > 0 ? [0, 1, 2, 3, 4, 5].map((i) => (Number(yMax) * i) / 5) : [0, 1, 2, 3, 4].map((i) => i * step);
  const yAt = (v) => pad.top + innerH - (Math.min(Number(v) || 0, top) / top) * innerH;
  const labelEvery = Math.max(1, Math.ceil(n / Math.max(1, Math.floor(innerW / 62))));

  return (
    <div className="pp-chart" ref={ref} style={{ height }}>
      {w > 0 && (
        <svg width={w} height={height} role="img" aria-label="柱状图" style={{ display: 'block', overflow: 'visible' }}>
          <defs>
            {series.map((s, si) => (
              <linearGradient key={s.key} id={`${uid}-c${si}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color || chartPalette[si]} stopOpacity="0.95" />
                <stop offset="100%" stopColor={s.color || chartPalette[si]} stopOpacity="0.55" />
              </linearGradient>
            ))}
          </defs>
          {Number(referenceLine) > 0 && Number(referenceLine) <= top ? (
            <g>
              <line
                x1={pad.left}
                x2={pad.left + innerW}
                y1={yAt(Number(referenceLine))}
                y2={yAt(Number(referenceLine))}
                stroke="#f59e0b"
                strokeWidth="1"
                strokeDasharray="4 4"
              />
              <text
                x={pad.left + innerW}
                y={yAt(Number(referenceLine)) - 4}
                textAnchor="end"
                fontSize="11"
                fill="#f59e0b"
              >
                {referenceLine}
              </text>
            </g>
          ) : null}


          {ticks.map((t) => (
            <g key={t}>
              <line
                x1={pad.left}
                x2={pad.left + innerW}
                y1={yAt(t)}
                y2={yAt(t)}
                stroke={chartGridColor}
                strokeDasharray={t === 0 ? undefined : '3 4'}
              />
              <text x={pad.left - 10} y={yAt(t) + 4} textAnchor="end" fontSize="11" fill={chartAxisColor}>
                {formatValue(t)}
              </text>
            </g>
          ))}

          {(data || []).map((row, i) => {
            const cx = pad.left + slot * i + slot / 2;
            const active = hover === i;
            return (
              <g
                key={(row.label || '') + i}
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover(null)}
              >
                <rect
                  x={pad.left + slot * i}
                  y={pad.top}
                  width={slot}
                  height={innerH}
                  fill={active ? 'rgba(79,70,229,0.05)' : 'transparent'}
                />
                {series.map((s, si) => {
                  const v = Number(row[s.key]) || 0;
                  const y = yAt(v);
                  const h = Math.max(0, pad.top + innerH - y);
                  const x =
                    cx - groupW / 2 + si * (barW + 4) + Math.max(0, (groupW - series.length * (barW + 4) + 4) / 2);
                  return (
                    <rect
                      key={s.key}
                      x={x}
                      y={y}
                      width={barW}
                      height={h}
                      rx={Math.min(4, barW / 2)}
                      fill={`url(#${uid}-c${si})`}
                    />
                  );
                })}
                {(i % labelEvery === 0 || n <= 8) && (
                  <text x={cx} y={height - 8} textAnchor="middle" fontSize="11" fill={chartAxisColor}>
                    {row.label}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
      )}

      {hover !== null && data[hover] && (
        <div
          className="pp-tooltip"
          style={{
            left: Math.min(
              Math.max(pad.left + slot * hover + slot / 2, 74),
              Math.max(74, w - 74)
            ),
            top: 4,
          }}
        >
          <div className="pp-tooltip-title">{data[hover].fullLabel || data[hover].label}</div>
          {series.map((s, si) => (
            <div className="pp-tooltip-row" key={s.key}>
              <i style={{ background: s.color || chartPalette[si] }} />
              <span>{s.name}</span>
              <b>
                {nf.format(Number(data[hover][s.key]) || 0)}
                {unit}
              </b>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/* ---------------- 环形图 ---------------- */

export function DonutChart({ data, size = 168, thickness = 18, centerLabel = '总计', centerValue }) {
  const uid = useId().replace(/[^a-zA-Z0-9]/g, '');
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => setMounted(true), 40);
    return () => clearTimeout(t);
  }, []);

  const items = (data || []).filter((d) => (Number(d.value) || 0) > 0);
  const total = items.reduce((s, d) => s + (Number(d.value) || 0), 0);
  const r = size / 2 - thickness / 2 - 2;
  const c = 2 * Math.PI * r;
  const cx = size / 2;
  let acc = 0;

  return (
    <div className="pp-donut">
      <div className="pp-donut-canvas" style={{ width: size, height: size }}>
        <svg width={size} height={size} role="img" aria-label="环形占比图">
          <circle cx={cx} cy={cx} r={r} fill="none" stroke="#f1f5f9" strokeWidth={thickness} />
          {items.map((d, i) => {
            const frac = total ? (Number(d.value) || 0) / total : 0;
            const dash = mounted ? frac * c : 0;
            const offset = -(acc / (total || 1)) * c;
            acc += Number(d.value) || 0;
            return (
              <circle
                key={d.name || i}
                cx={cx}
                cy={cx}
                r={r}
                fill="none"
                stroke={d.color || chartPalette[i]}
                strokeWidth={thickness}
                strokeLinecap="butt"
                strokeDasharray={`${dash} ${c}`}
                strokeDashoffset={offset}
                transform={`rotate(-90 ${cx} ${cx})`}
                style={{ transition: 'stroke-dasharray 700ms cubic-bezier(.22,.61,.36,1)' }}
              />
            );
          })}
        </svg>
        <div className="pp-donut-center">
          <div className="pp-donut-value">{nf.format(Number(centerValue !== undefined ? centerValue : total) || 0)}</div>
          <div className="pp-donut-label">{centerLabel}</div>
        </div>
      </div>
      <div className="pp-donut-legend">
        {(data || []).map((d, i) => {
          const v = Number(d.value) || 0;
          const pct = total ? ((v / total) * 100).toFixed(1) : '0.0';
          return (
            <div className="pp-donut-row" key={d.name || i}>
              <i style={{ background: d.color || chartPalette[i] }} />
              <span className="pp-donut-name">{d.name}</span>
              <b className="pp-donut-num">{nf.format(v)}</b>
              <em className="pp-donut-pct">{pct}%</em>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/* ---------------- 仪表盘 ---------------- */

export function GaugeChart({ value = 0, max = 100, label = '', suffix = '%', color, size = 168 }) {
  const pct = Math.min(100, Math.max(0, max ? (Number(value) / max) * 100 : 0));
  const r = size / 2 - 14;
  const cx = size / 2;
  const cy = size / 2 + 6;
  const start = -118;
  const end = 118;
  const sweep = ((end - start) * pct) / 100;
  const stroke = color || (pct >= 90 ? '#f43f5e' : pct >= 75 ? '#f59e0b' : pct >= 50 ? '#0ea5e9' : '#10b981');

  return (
    <div className="pp-gauge" style={{ width: size }}>
      <svg width={size} height={size * 0.82} viewBox={`0 0 ${size} ${size * 0.82}`} role="img" aria-label={label}>
        <path d={arcPath(cx, cy, r, start, end)} fill="none" stroke="#f1f5f9" strokeWidth="12" strokeLinecap="round" />
        <path
          d={arcPath(cx, cy, r, start, start + sweep)}
          fill="none"
          stroke={stroke}
          strokeWidth="12"
          strokeLinecap="round"
          style={{ transition: 'd 600ms ease' }}
        />
      </svg>
      <div className="pp-gauge-text">
        <div className="pp-gauge-value" style={{ color: stroke }}>
          {fmtCompact(value)}
          <small>{suffix}</small>
        </div>
        <div className="pp-gauge-label">{label}</div>
      </div>
    </div>
  );
}

/* ---------------- 迷你走势 ---------------- */

export function Sparkline({ values, color = chartPalette[0], height = 34, fill = true }) {
  const uid = useId().replace(/[^a-zA-Z0-9]/g, '');
  const data = Array.isArray(values) ? values.map((v) => Number(v) || 0) : [];
  if (data.length < 2) return <div style={{ height }} />;
  const max = Math.max(...data, 1);
  const min = Math.min(...data, 0);
  const span = max - min || 1;
  const W = 100;
  const pts = data.map((v, i) => [
    (W * i) / (data.length - 1),
    height - 3 - ((v - min) / span) * (height - 6),
  ]);
  const line = smoothPath(pts, 0, height);
  return (
    <svg
      width="100%"
      height={height}
      viewBox={`0 0 ${W} ${height}`}
      preserveAspectRatio="none"
      style={{ display: 'block' }}
      aria-hidden="true"
    >
      <defs>
        <linearGradient id={`${uid}-s`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      {fill && <path d={`${line} L${W},${height} L0,${height} Z`} fill={`url(#${uid}-s)`} />}
      <path d={line} fill="none" stroke={color} strokeWidth="1.6" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

/* ---------------- 横向条形（标签 + 进度） ---------------- */

export function BarList({ items, unit = '' }) {
  const max = Math.max(1, ...items.map((i) => Number(i.value) || 0));
  return (
    <div className="pp-barlist">
      {items.map((it, i) => {
        const v = Number(it.value) || 0;
        const w = (v / max) * 100;
        return (
          <div className="pp-barlist-row" key={it.label + i}>
            <div className="pp-barlist-top">
              <span className="pp-barlist-name" title={it.label}>
                {it.label}
              </span>
              <b className="pp-barlist-val">
                {nf.format(v)}
                {unit}
              </b>
            </div>
            <div className="pp-barlist-track">
              <i
                style={{
                  width: Math.max(2, w) + '%',
                  background: it.color || chartPalette[i % chartPalette.length],
                }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}

export { chartPalette };
