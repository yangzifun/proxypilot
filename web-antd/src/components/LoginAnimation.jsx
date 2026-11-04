/* =================================================================
 *  LoginAnimation —— 登录页左侧动态插画（纯 SVG + CSS，无外部资源）
 *  · 中央「控制台」卡片 + 均衡器柱状动画
 *  · 周围节点脉冲、数据包沿连线流动、轨道缓慢旋转
 * ================================================================= */
import React from 'react';

const NODES = [
  { x: 92, y: 118, color: '#38bdf8', delay: '0s' },
  { x: 100, y: 268, color: '#22c55e', delay: '0.8s' },
  { x: 468, y: 116, color: '#a78bfa', delay: '1.4s' },
  { x: 462, y: 262, color: '#38bdf8', delay: '2s' },
  { x: 280, y: 58, color: '#f59e0b', delay: '0.4s' },
];

const LINKS = [
  'M100 118 C 156 128 196 152 240 172',
  'M108 262 C 160 250 198 226 238 206',
  'M460 118 C 404 130 366 154 322 172',
  'M456 258 C 402 248 364 226 324 206',
  'M280 66 C 280 96 280 120 280 146',
];

const BARS = [
  { x: 250, d: '0s' },
  { x: 268, d: '0.25s' },
  { x: 286, d: '0.5s' },
  { x: 304, d: '0.75s' },
];

export default function LoginAnimation() {
  return (
    <div className="pp-anim" aria-hidden="true">
      <div className="pp-anim-glow" />
      <svg className="pp-anim-svg" viewBox="0 0 560 330" role="presentation">
        <defs>
          <linearGradient id="ppAnimLink" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#6366f1" stopOpacity="0.15" />
            <stop offset="55%" stopColor="#818cf8" stopOpacity="0.75" />
            <stop offset="100%" stopColor="#6366f1" stopOpacity="0.15" />
          </linearGradient>
          <linearGradient id="ppAnimCard" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#1e293b" />
            <stop offset="100%" stopColor="#0f172a" />
          </linearGradient>
          <radialGradient id="ppAnimHalo">
            <stop offset="0%" stopColor="#6366f1" stopOpacity="0.35" />
            <stop offset="100%" stopColor="#6366f1" stopOpacity="0" />
          </radialGradient>
        </defs>

        <circle className="pp-anim-halo" cx="280" cy="176" r="150" fill="url(#ppAnimHalo)" />
        <circle className="pp-anim-orbit" cx="280" cy="176" r="128" />
        <circle className="pp-anim-orbit pp-anim-orbit-slow" cx="280" cy="176" r="168" />

        {LINKS.map((d, i) => (
          <g key={d}>
            <path className="pp-anim-link" d={d} />
            <circle className="pp-anim-packet" r="3.2" fill={NODES[i % NODES.length].color}>
              <animateMotion dur={`${2.6 + i * 0.35}s`} begin={`${i * 0.3}s`} repeatCount="indefinite" path={d} />
            </circle>
          </g>
        ))}

        {NODES.map((n) => (
          <g key={`${n.x}-${n.y}`} className="pp-anim-node" style={{ animationDelay: n.delay }}>
            <circle className="pp-anim-node-halo" cx={n.x} cy={n.y} r="18" fill={n.color} />
            <circle className="pp-anim-node-core" cx={n.x} cy={n.y} r="7" fill={n.color} />
            <circle className="pp-anim-node-ring" cx={n.x} cy={n.y} r="12" stroke={n.color} />
          </g>
        ))}

        <g className="pp-anim-card">
          <rect x="222" y="132" width="116" height="104" rx="20" fill="url(#ppAnimCard)" stroke="rgba(129,140,248,.55)" />
          <rect x="222" y="132" width="116" height="104" rx="20" fill="none" stroke="url(#ppAnimLink)" strokeWidth="1.2" />
          <rect x="238" y="150" width="44" height="6" rx="3" fill="rgba(148,163,184,.45)" />
          <rect x="238" y="164" width="28" height="6" rx="3" fill="rgba(148,163,184,.25)" />
          {BARS.map((b) => (
            <rect key={b.x} className="pp-anim-bar" x={b.x} y="212" width="9" height="26" rx="4" style={{ animationDelay: b.d }} />
          ))}
          <rect x="238" y="190" width="84" height="6" rx="3" fill="rgba(99,102,241,.35)" />
          <circle className="pp-anim-led" cx="322" cy="152" r="4" fill="#22c55e" />
        </g>
      </svg>
    </div>
  );
}
