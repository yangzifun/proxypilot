import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Empty } from 'antd';
import { fmtBytes } from '../format';

/**
 * 用户 → 节点 流量流向图（轻量 SVG 自绘，替代原 AntV G6 实现）。
 *
 * 为什么不再用 G6：
 * - 现状只设了 style.size/radius 没设 type，G6 默认节点是 circle，iconSrc 偏移 -64 的
 *   国旗与居中 label 直接重叠，右侧节点也变成圆形；
 * - G6 v5 的注册自定义节点需要 BaseNode 等内部 API，而项目只通过 src/g6-entry.js 暴露了
 *   Graph/register 等少数接口，扩展成本高、版本行为不确定；
 * - 自绘 SVG 可以精确控制「方形卡片 + 服务器图标 + 国旗 + 名字」的间距与截断，虚线流动
 *   用 CSS animation、光点用 offset-path 动画，均由浏览器调度，无需 rAF 轮询，卸载即停，
 *   同时去掉 /g6-app.js 的异步加载与加载失败态。
 *
 * props 契约保持不变：
 *   users: [{ uuid, label, total, active? }]
 *   nodes: [{ node_id, label, total }]
 *   edges: [{ uuid, node_id, total }]
 *   height: 期望的最小画布高度（行数多、卡片放不下时会纵向增高，避免把文字压小）
 */

const PALETTE = ['#6366f1', '#06b6d4', '#10b981', '#f59e0b', '#ec4899', '#8b5cf6', '#0ea5e9', '#f43f5e'];

// 尊重系统「减少动态效果」设置：关闭虚线动画与移动光点。
const REDUCED_MOTION = (() => {
  try {
    return typeof window !== 'undefined' && !!window.matchMedia
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  } catch (e) {
    return false;
  }
})();

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

// 光点沿连线运动的 CSS 动画。写在这里而不是 styles.css，是为了让 FlowGraph 自包含；
// 用 offset-path + offset-distance（浏览器原生动画，不需要 rAF），组件卸载即停。
const FLOW_DOT_CSS = '@keyframes ppFlowDot { from { offset-distance: 0%; } to { offset-distance: 100%; } }';

function ensureFlowDotStyle() {
  if (typeof document === 'undefined' || !document.head) return;
  if (document.getElementById('pp-flow-dot-css')) return;
  const el = document.createElement('style');
  el.id = 'pp-flow-dot-css';
  el.textContent = FLOW_DOT_CSS;
  document.head.appendChild(el);
}

/** 从节点名推断国家码，用于国旗图标（与后端 /api/flag/{cc} 配合） */
function countryCodeOf(name) {
  const s = String(name || '');
  const map = [
    ['香港', 'hk'], ['台湾', 'tw'], ['台北', 'tw'], ['彰化', 'tw'], ['日本', 'jp'], ['东京', 'jp'],
    ['大阪', 'jp'], ['新加坡', 'sg'], ['狮城', 'sg'], ['美国', 'us'], ['洛杉矶', 'us'], ['圣何塞', 'us'],
    ['西雅图', 'us'], ['纽约', 'us'], ['德国', 'de'], ['法兰克福', 'de'], ['马来西亚', 'my'], ['吉隆坡', 'my'],
    ['印度尼西亚', 'id'], ['印尼', 'id'], ['雅加达', 'id'], ['泰国', 'th'], ['曼谷', 'th'],
    ['韩国', 'kr'], ['首尔', 'kr'], ['英国', 'gb'], ['伦敦', 'gb'], ['法国', 'fr'], ['巴黎', 'fr'],
    ['荷兰', 'nl'], ['阿姆斯特丹', 'nl'], ['澳大利亚', 'au'], ['澳洲', 'au'], ['悉尼', 'au'],
    ['加拿大', 'ca'], ['多伦多', 'ca'], ['温哥华', 'ca'], ['迪拜', 'ae'], ['莫斯科', 'ru'],
    ['马尼拉', 'ph'], ['菲律宾', 'ph'], ['越南', 'vn'], ['河内', 'vn'],
  ];
  for (const [k, v] of map) if (s.includes(k)) return v;
  return '';
}

// 估算文本宽度：中文/全角按 1em、其余按 0.56em。SVG 没有 text-overflow，
// 只能按估算宽度截断，保证文字不越出卡片。
function estimateTextWidth(text, fontSize) {
  let w = 0;
  for (const ch of String(text)) {
    const code = ch.codePointAt(0);
    const wide = (code >= 0x1100 && code <= 0x115f)
      || (code >= 0x2e80 && code <= 0xa4cf)
      || (code >= 0xac00 && code <= 0xd7a3)
      || (code >= 0xf900 && code <= 0xfaff)
      || (code >= 0xfe30 && code <= 0xfe6f)
      || (code >= 0xff00 && code <= 0xff60)
      || (code >= 0xffe0 && code <= 0xffe6);
    w += wide ? fontSize : fontSize * 0.56;
  }
  return w;
}

function truncateText(text, maxPx, fontSize) {
  const s = String(text || '');
  if (!s) return '';
  if (estimateTextWidth(s, fontSize) <= maxPx) return s;
  const ellipsisW = fontSize * 0.9;
  let out = '';
  let used = 0;
  for (const ch of s) {
    const cw = estimateTextWidth(ch, fontSize);
    if (used + cw + ellipsisW > maxPx) break;
    out += ch;
    used += cw;
  }
  return (out || '') + '…';
}

/** 服务器机架风格图标（纯 SVG 线条，颜色跟随主题） */
function rackIcon(w, h) {
  return (
    <g>
      <rect x={0.5} y={0.5} width={Math.max(0, w - 1)} height={Math.max(0, h - 1)} rx={4}
        fill="#eef2ff" stroke="#6366f1" strokeWidth={1.3} />
      {[0.3, 0.52, 0.74].map((f) => (
        <g key={f}>
          <line x1={w * 0.17} y1={h * f} x2={w * 0.66} y2={h * f}
            stroke="#a5b4fc" strokeWidth={1.6} strokeLinecap="round" />
          <circle cx={w * 0.81} cy={h * f} r={1.25} fill="#10b981" />
        </g>
      ))}
    </g>
  );
}

/**
 * 计算整张图的位置。坐标系直接用容器像素宽度（不做 viewBox 缩放），
 * 这样字号、图标、卡片都是真实像素，随容器宽度变化重新布局。
 */
function buildFlowLayout(users, nodes, edges, height, width) {
  const userList = users || [];
  const nodeList = nodes || [];
  const edgeList = edges || [];

  const rows = Math.max(userList.length, nodeList.length, 1);
  const wantHeight = Math.max(160, Number(height) || 420);
  const W = Math.max(320, Math.round(Number(width) || 0) || 900);

  // 方形节点卡边长 96~112：行数少时用 112，行数多时缩到 96；仍放不下则纵向增高。
  const padY = clamp(Math.round(wantHeight * 0.05), 18, 28);
  const rowGap = rows > 6 ? 12 : 14;
  const rawSide = (wantHeight - 2 * padY - (rows - 1) * rowGap) / rows;
  const side = Math.round(clamp(rawSide, 96, 112));
  const contentH = rows * side + (rows - 1) * rowGap;
  const H = Math.max(wantHeight, contentH + 2 * padY);
  const top = (H - contentH) / 2;

  const marginX = W < 520 ? 12 : 16;
  const userW = Math.round(clamp(W * 0.24, 96, 150));
  const userH = 40;
  const userX = marginX;
  const nodeX = W - marginX - side;

  // 两端列都按各自数量在内容高度内均分；只有一个时垂直居中。
  const centerAt = (i, count) => {
    if (count <= 1) return H / 2;
    return top + side / 2 + i * ((contentH - side) / (count - 1));
  };
  // 单用户且行数多到必须纵向增高时（例如单用户详情 Modal），把用户卡放到首行附近，
  // 保证打开时第一屏能看到「来源」，而不是只看到从画布下方钻出来的连线。
  const userAlignTop = userList.length === 1 && H > wantHeight * 1.15;

  const colorOf = new Map();
  const userCards = userList.map((u, i) => {
    const key = String(u.uuid ?? u.key ?? ('u' + i));
    const paletteIdx = i % PALETTE.length;
    const color = PALETTE[paletteIdx];
    colorOf.set(key, { color, paletteIdx });
    const full = String(u.label || u.uuid || '').trim() || '未命名用户';
    const cy = userAlignTop ? top + side / 2 : centerAt(i, userList.length);
    return {
      key,
      paletteIdx,
      color,
      cy,
      x: userX,
      y: cy - userH / 2,
      w: userW,
      h: userH,
      label: truncateText(full, userW - 34, 12),
      title: `${full} · ${fmtBytes(u.total)}`,
    };
  });
  const userByKey = new Map(userCards.map((u) => [u.key, u]));

  const nodeCards = nodeList.map((n, i) => {
    const key = String(n.node_id ?? ('n' + i));
    const cy = centerAt(i, nodeList.length);
    const full = String(n.label || ('#' + key)).trim();
    const cc = countryCodeOf(full);
    const nameFont = side >= 104 ? 11 : 10.5;
    const flagW = side >= 104 ? 16 : 15;
    const flagH = side >= 104 ? 11 : 10.5;
    const gap = 8;
    const nameMax = side - 14 - (cc ? flagW + gap : 0);
    return {
      key,
      cy,
      x: nodeX,
      y: cy - side / 2,
      side,
      total: Number(n.total) || 0,
      full,
      cc,
      flagW,
      flagH,
      gap,
      nameFont,
      name: truncateText(full, nameMax, nameFont),
      title: `${full} · ${fmtBytes(n.total)}`,
    };
  });
  const nodeByKey = new Map(nodeCards.map((n) => [n.key, n]));

  // 合并重复边（按 用户|节点 聚合一次），只保留两端都存在的连线。
  const maxTotal = Math.max(1, ...edgeList.map((e) => Number(e.total) || 0));
  const merged = new Map();
  for (const e of edgeList) {
    const uk = String(e.uuid);
    const nk = String(e.node_id);
    if (!userByKey.has(uk) || !nodeByKey.has(nk)) continue;
    const k = uk + '|' + nk;
    const prev = merged.get(k);
    merged.set(k, { uk, nk, total: (prev ? prev.total : 0) + (Number(e.total) || 0) });
  }

  const links = [];
  for (const { uk, nk, total } of merged.values()) {
    const user = userByKey.get(uk);
    const node = nodeByKey.get(nk);
    const x1 = user.x + user.w;
    const y1 = user.cy;
    const x2 = node.x - 4;
    const y2 = node.cy;
    const dx = clamp((x2 - x1) * 0.45, 36, 240);
    const ratio = total / maxTotal;
    links.push({
      key: uk + '|' + nk,
      d: `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`,
      color: user.color,
      markerIdx: user.paletteIdx,
      width: Math.round((1.3 + ratio * 5.2) * 10) / 10,
      total,
      ratio,
      dot: false,
    });
  }

  // 主要连线（流量最大的前 5 条）额外加移动光点/辉光，避免满屏光点。
  const dotCount = Math.min(5, links.length);
  links.slice().sort((a, b) => b.total - a.total).slice(0, dotCount).forEach((l, i) => {
    const target = links.find((x) => x.key === l.key);
    if (!target) return;
    target.dot = true;
    target.dotDur = (2 + (1 - target.ratio) * 2).toFixed(2);
    target.dotBegin = (i * 0.45).toFixed(2);
  });

  return {
    W,
    H,
    side,
    userCards,
    nodeCards,
    links,
    markerIdxs: Array.from(new Set(userCards.map((u) => u.paletteIdx))),
  };
}

export default function FlowGraph({ users = [], nodes = [], edges = [], height = 420, emptyText = '暂无流向数据' }) {
  const hostRef = useRef(null);
  const [width, setWidth] = useState(0);
  const [ready, setReady] = useState(false);

  // 用 ResizeObserver 跟随容器宽度（Modal 打开、Tabs 切换、窗口缩放都会触发）。
  useLayoutEffect(() => {
    const el = hostRef.current;
    if (!el) return undefined;
    const measure = () => {
      const w = Math.round(el.clientWidth || (el.getBoundingClientRect && el.getBoundingClientRect().width) || 0);
      if (w > 0) setWidth((prev) => (prev === w ? prev : w));
    };
    measure();
    let ro = null;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(measure);
      ro.observe(el);
      return () => ro.disconnect();
    }
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, []);

  useEffect(() => {
    ensureFlowDotStyle();
    const t = window.setTimeout(() => setReady(true), 0);
    return () => window.clearTimeout(t);
  }, []);

  const layout = useMemo(() => {
    try {
      return buildFlowLayout(users, nodes, edges, height, width);
    } catch (e) {
      return { error: (e && e.message) || '流向图渲染失败' };
    }
  }, [users, nodes, edges, height, width]);

  if (!users.length || !nodes.length) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={emptyText} />;
  }
  if (layout.error) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={'流向图加载失败：' + layout.error} />;
  }

  const { W, H, userCards, nodeCards, links, markerIdxs } = layout;

  return (
    <div>
      <div
        ref={hostRef}
        style={{
          width: '100%',
          borderRadius: 12,
          background: 'linear-gradient(180deg, #fbfdff 0%, #ffffff 100%)',
          border: '1px solid #f1f5f9',
          overflow: 'hidden',
          opacity: ready ? 1 : 0.35,
          transition: 'opacity .25s ease',
        }}
      >
        {width > 0 ? (
          <svg
            width={W}
            height={H}
            viewBox={'0 0 ' + W + ' ' + H}
            style={{ display: 'block', width: '100%', height: 'auto' }}
            role="img"
            aria-label={'用户到服务器的流量流向图，共 ' + users.length + ' 个用户、' + nodes.length + ' 个节点'}
          >
            <defs>
              {markerIdxs.map((idx) => (
                <marker
                  key={idx}
                  id={'ppFlowArrow-' + idx}
                  markerWidth="9"
                  markerHeight="9"
                  refX="8"
                  refY="4.5"
                  orient="auto"
                  markerUnits="userSpaceOnUse"
                >
                  <path d="M0,0 L9,4.5 L0,9 Z" fill={PALETTE[idx]} opacity="0.85" />
                </marker>
              ))}
            </defs>

            {/* 连线：虚线 + CSS 流动动画（styles.css 的 .pp-flow-dash / ppFlowDash） */}
            <g>
              {links.map((l) => (
                <path
                  key={l.key}
                  d={l.d}
                  fill="none"
                  stroke={l.color}
                  strokeWidth={l.width}
                  strokeLinecap="round"
                  strokeDasharray="7 7"
                  opacity="0.72"
                  markerEnd={'url(#ppFlowArrow-' + l.markerIdx + ')'}
                  className="pp-flow-dash"
                  style={REDUCED_MOTION ? { animation: 'none' } : undefined}
                />
              ))}
            </g>

            {/* 主要连线的移动光点：offset-path + offset-distance 动画，沿各自路径运动 */}
            {REDUCED_MOTION ? null : (
              <g pointerEvents="none">
                {links.filter((l) => l.dot).map((l) => {
                  const dotStyle = {
                    offsetPath: 'path("' + l.d + '")',
                    offsetDistance: '0%',
                    offsetRotate: '0deg',
                    animation: 'ppFlowDot ' + l.dotDur + 's linear infinite',
                    animationDelay: '-' + l.dotBegin + 's',
                  };
                  return (
                    <g key={'dot-' + l.key}>
                      <circle r="5.5" fill={l.color} opacity="0.14" style={dotStyle} />
                      <circle r="2.4" fill={l.color} style={dotStyle} />
                    </g>
                  );
                })}
              </g>
            )}

            {/* 左侧用户节点：圆角矩形文字卡 */}
            <g>
              {userCards.map((u) => (
                <g key={'u-' + u.key}>
                  <title>{u.title}</title>
                  <rect x={u.x} y={u.y} width={u.w} height={u.h} rx={10}
                    fill="#ffffff" stroke="#e2e8f0" strokeWidth="1" />
                  <rect x={u.x + 7} y={u.y + 9} width="3.5" height={u.h - 18} rx="1.75" fill={u.color} />
                  <text x={u.x + u.w / 2 + 2} y={u.cy} textAnchor="middle" dominantBaseline="central"
                    fontSize="12" fill="#0f172a">{u.label}</text>
                </g>
              ))}
            </g>

            {/* 右侧节点：方形卡片（服务器图标 + 国旗 + 节点名 + 流量） */}
            <g>
              {nodeCards.map((n) => {
                const cardCx = n.x + n.side / 2;
                const cardTop = n.y;
                const iconW = n.side >= 104 ? 34 : 30;
                const iconH = n.side >= 104 ? 28 : 25;
                const iconX = cardCx - iconW / 2;
                const iconY = cardTop + Math.round(n.side * 0.11);
                const nameCy = cardTop + n.side * 0.63;
                const bytesCy = cardTop + n.side * 0.82;
                const flagW = n.cc ? n.flagW : 0;
                const flagGap = n.cc ? n.gap : 0;
                const textW = estimateTextWidth(n.name, n.nameFont);
                const rowX = cardCx - (flagW + flagGap + textW) / 2;
                return (
                  <g key={'n-' + n.key}>
                    <title>{n.title}</title>
                    {/* 轻投影 + 卡片本体 */}
                    <rect x={n.x} y={n.y + 2} width={n.side} height={n.side} rx={12}
                      fill="#0f172a" opacity="0.04" />
                    <rect x={n.x} y={n.y} width={n.side} height={n.side} rx={12}
                      fill="#ffffff" stroke="#dbeafe" strokeWidth="1" />
                    {/* 服务器机架图标 */}
                    <g transform={'translate(' + iconX + ',' + iconY + ')'}>{rackIcon(iconW, iconH)}</g>
                    {/* 国旗 + 节点名：水平排列，间距 8，按可用宽度截断 */}
                    {n.cc ? (
                      <image
                        href={'/api/flag/' + n.cc}
                        x={rowX}
                        y={nameCy - n.flagH / 2}
                        width={n.flagW}
                        height={n.flagH}
                        preserveAspectRatio="none"
                      />
                    ) : null}
                    <text
                      x={rowX + flagW + flagGap}
                      y={nameCy}
                      dominantBaseline="central"
                      fontSize={n.nameFont}
                      fill="#0f172a"
                    >{n.name}</text>
                    <text x={cardCx} y={bytesCy} textAnchor="middle" dominantBaseline="central"
                      fontSize="9.5" fill="#94a3b8">{fmtBytes(n.total)}</text>
                  </g>
                );
              })}
            </g>
          </svg>
        ) : null}
      </div>
      <div className="pp-muted" style={{ fontSize: 11.5, marginTop: 6, textAlign: 'center' }}>
        线宽表示流量大小 · 虚线流动与光点表示实时流向 · 视图随容器宽度自动适配
      </div>
    </div>
  );
}