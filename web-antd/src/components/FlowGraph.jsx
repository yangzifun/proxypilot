import React, { useEffect, useRef, useState } from 'react';
import { Empty } from 'antd';

/**
 * 基于 AntV G6 的流向图：左侧用户 → 右侧节点。
 * G6 体积较大，通过 /g6-app.js 按需加载，不进入主包。
 */

let g6Promise = null;
function loadG6() {
  if (typeof window !== 'undefined' && window.__PP_G6__) return Promise.resolve(window.__PP_G6__);
  if (!g6Promise) {
    g6Promise = new Promise((resolve, reject) => {
      const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};
      const s = document.createElement('script');
      s.src = '/g6-app.js' + (boot.assetVersion ? '?v=' + boot.assetVersion : '');
      s.async = true;
      s.onload = () => (window.__PP_G6__ ? resolve(window.__PP_G6__) : reject(new Error('G6 初始化失败')));
      s.onerror = () => reject(new Error('G6 加载失败'));
      document.head.appendChild(s);
    });
  }
  return g6Promise;
}

// 虚线偏移循环，形成"数据在流动"的视觉效果
function startDashFlow(graph, edgeIds, isAlive) {
  if (!edgeIds.length) return () => {};
  let offset = 0;
  let raf = 0;
  let stopped = false;
  const step = () => {
    if (stopped || !isAlive()) return;
    offset = (offset - 0.6) % 24;
    for (const id of edgeIds) {
      try {
        const el = graph.getElement ? graph.getElement(id) : null;
        if (el && el.attr) el.attr({ lineDashOffset: offset });
      } catch (e) {
        /* 单个元素失败忽略 */
      }
    }
    raf = requestAnimationFrame(step);
  };
  raf = requestAnimationFrame(step);
  return () => {
    stopped = true;
    cancelAnimationFrame(raf);
  };
}

/** 从节点名推断国家码，用于国旗图标 */
function countryCodeOf(name) {
  const s = String(name || '');
  const map = [
    ['香港', 'hk'], ['台湾', 'tw'], ['台北', 'tw'], ['彰化', 'tw'], ['日本', 'jp'], ['东京', 'jp'],
    ['大阪', 'jp'], ['新加坡', 'sg'], ['美国', 'us'], ['洛杉矶', 'us'], ['圣何塞', 'us'], ['西雅图', 'us'],
    ['纽约', 'us'], ['德国', 'de'], ['法兰克福', 'de'], ['马来西亚', 'my'], ['吉隆坡', 'my'],
    ['印度尼西亚', 'id'], ['印尼', 'id'], ['雅加达', 'id'], ['泰国', 'th'], ['曼谷', 'th'],
    ['韩国', 'kr'], ['首尔', 'kr'], ['英国', 'gb'], ['伦敦', 'gb'], ['法国', 'fr'], ['荷兰', 'nl'],
  ];
  for (const [k, v] of map) if (s.includes(k)) return v;
  return '';
}

const PALETTE = ['#6366f1', '#06b6d4', '#10b981', '#f59e0b', '#ec4899', '#8b5cf6', '#0ea5e9', '#f43f5e'];

export default function FlowGraph({ users = [], nodes = [], edges = [], height = 420, emptyText = '暂无流向数据' }) {
  const hostRef = useRef(null);
  const graphRef = useRef(null);
  const [error, setError] = useState(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let alive = true;
    if (!users.length || !nodes.length) return undefined;

    loadG6()
      .then(({ Graph }) => {
        if (!alive || !hostRef.current) return;
        // 清理旧实例
        if (graphRef.current) {
          try {
            graphRef.current.destroy();
          } catch (e) {
            /* ignore */
          }
          graphRef.current = null;
        }
        hostRef.current.innerHTML = '';

        const colorOf = new Map(users.map((u, i) => [String(u.uuid || u.key || i), PALETTE[i % PALETTE.length]]));
        const maxTotal = Math.max(...edges.map((e) => Number(e.total) || 0), 1);

        const gNodes = [];
        users.forEach((u, i) => {
          const id = 'u:' + String(u.uuid || u.key || i);
          gNodes.push({
            id,
            data: { label: String(u.label || u.uuid || '').slice(0, 12), value: u.total, kind: 'user', color: colorOf.get(String(u.uuid || u.key || i)) },
            style: { size: [150, 40], radius: 10, fill: '#ffffff', stroke: '#e2e8f0', lineWidth: 1 },
          });
        });
        nodes.forEach((n) => {
          const id = 'n:' + String(n.node_id);
          const cc = countryCodeOf(n.label);
          gNodes.push({
            id,
            data: { label: String(n.label || '').slice(0, 14), value: n.total, kind: 'node', color: '#0ea5e9' },
            style: {
              size: [172, 40], radius: 10, fill: '#f8fafc', stroke: '#dbeafe', lineWidth: 1,
              ...(cc ? { iconSrc: '/api/flag/' + cc, iconWidth: 15, iconHeight: 11, iconOffsetX: -64 } : {}),
            },
          });
        });

        const gEdges = edges
          .map((e, i) => {
            const src = 'u:' + String(e.uuid);
            const dst = 'n:' + String(e.node_id);
            if (!colorOf.has(String(e.uuid))) return null;
            return {
              id: 'e' + i,
              source: src,
              target: dst,
              data: {
                color: colorOf.get(String(e.uuid)),
                width: 1.5 + (Number(e.total) / maxTotal) * 9,
                total: e.total,
              },
            };
          })
          .filter(Boolean);

        const graph = new Graph({
          container: hostRef.current,
          width: hostRef.current.clientWidth || 900,
          height,
          autoFit: 'view',
          padding: 24,
          data: { nodes: gNodes, edges: gEdges },
          node: {
            style: {
              labelOffsetX: (d) => (d.style && d.style.iconSrc ? 20 : 0),
              labelText: (d) => d.data.label,
              labelFill: '#0f172a',
              labelFontSize: 12,
              labelPlacement: 'center',
              cursor: 'default',
            },
          },
          edge: {
            type: 'quadratic',
            style: {
              stroke: (d) => d.data.color,
              lineWidth: (d) => d.data.width,
              opacity: 0.7,
              lineDash: [6, 6],
              endArrow: true,
              endArrowType: 'triangle',
              endArrowSize: 8,
              curveOffset: 24,
            },
          },
          layout: {
            type: 'dagre',
            rankdir: 'LR',
            nodesep: 14,
            ranksep: 220,
            ranker: 'tight-tree',
          },
          behaviors: ['hover-activate'],
        });
        graphRef.current = graph;
        Promise.resolve(graph.render())
          .then(() => {
            if (!alive) return;
            setReady(true);
            startDashFlow(graph, gEdges.map((e) => e.id), () => alive);
          })
          .catch((e) => {
            if (alive) setError(e && e.message ? e.message : 'G6 渲染失败');
          });
      })
      .catch((e) => {
        if (alive) setError(e && e.message ? e.message : 'G6 加载失败');
      });

    return () => {
      alive = false;
      if (graphRef.current) {
        try {
          graphRef.current.destroy();
        } catch (e) {
          /* ignore */
        }
        graphRef.current = null;
      }
    };
  }, [users, nodes, edges, height]);

  if (!users.length || !nodes.length) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={emptyText} />;
  }
  if (error) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={'流向图加载失败：' + error} />;
  }

  return (
    <div>
      <div
        ref={hostRef}
        style={{
          width: '100%',
          height,
          borderRadius: 12,
          background: 'linear-gradient(180deg, #fbfdff 0%, #ffffff 100%)',
          border: '1px solid #f1f5f9',
          opacity: ready ? 1 : 0.35,
          transition: 'opacity .25s ease',
        }}
      />
      <div className="pp-muted" style={{ fontSize: 11.5, marginTop: 6, textAlign: 'center' }}>
        仅统计近 2 分钟有流量的活跃用户 · 线宽表示流量大小 · 位置固定，自动适配画布
      </div>
    </div>
  );
}
