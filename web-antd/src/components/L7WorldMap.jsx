/* =================================================================
 *  L7WorldMap —— 服务器「全球分布」地图（AntV L7 + MapLibre 公共底图）
 *
 *  · 底图：https://demotiles.maplibre.org/style.json（MapLibre 官方
 *    demo 样式，公开可用、不需要任何 key / token）
 *  · 点位：PointLayer，按状态着色（在线 / 离线 / 未知），白描边
 *  · 交互：悬浮显示 Popup（名称 + 地区 + 状态），点击回调 onPick(server)
 *  · 同一经纬度的多台服务器合并成一个点，点大小随台数 18~22 递增
 *  · 初始化失败 / 没有经纬度时降级为 antd Empty 提示，不影响整页
 *
 *  依赖按子包引入（与 @antv/l7 是同一套代码），避免把用不到的
 *  mapbox-gl / maplibre-gl 适配器一起打进 app.js。
 * ================================================================= */
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Empty, Spin } from 'antd';
import { Scene } from '@antv/l7-scene';
import { PointLayer } from '@antv/l7-layers';
import { Popup } from '@antv/l7-component/es/popup/popup';
/*
 * 用 MapLibre 适配器（同样来自 @antv/l7-maps）而不是默认的 Map：
 * 默认 Map 只是 L7 自己的空白坐标系，不会渲染 style.json 底图；
 * MapLibre 适配器才能真正加载公开的 MapLibre 样式，且无需 key/token。
 */
import { MapLibre } from '@antv/l7-maps';
/* L7 2.29 的包内没有 @antv/l7/dist/l7.css，组件样式在 l7-component 的 es/css 里 */
import '@antv/l7-component/es/css/index.css';
/* MapLibre 底图自身的布局 / 控件样式 */
import 'maplibre-gl/dist/maplibre-gl.css';

/**
 * 与站点视觉一致的浅色底图：OpenFreeMap 的 Positron 样式
 * （浅灰底 + 低饱和水系/边界，无需 key/token）。
 */
export const MAP_STYLE = 'https://tiles.openfreemap.org/styles/positron';

const STATUS_META = {
  online: { color: '#10b981', label: '在线' },
  offline: { color: '#f43f5e', label: '离线' },
  unknown: { color: '#cbd5e1', label: '未知' },
};

const statusOf = (v) => (v === 'online' || v === 'offline' ? v : 'unknown');
const coordKey = (lat, lon) => `${Number(lat).toFixed(2)},${Number(lon).toFixed(2)}`;
const escapeHtml = (v) =>
  String(v === null || v === undefined ? '' : v).replace(/[&<>"']/g, (c) => {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });

/**
 * servers → L7 点数据
 * 同一 0.01° 内的坐标合并为一个点，颜色取「在线 > 离线 > 未知」的优先级，
 * 点大小 18 / 20 / 22 表示 1 / 2 / 3+ 台。
 */
function toPoints(servers) {
  const groups = new Map();
  let missing = 0;
  (servers || []).forEach((s) => {
    const lat = Number(s.ip_lat);
    const lon = Number(s.ip_lon);
    const bad =
      s.ip_lat === null ||
      s.ip_lat === undefined ||
      s.ip_lon === null ||
      s.ip_lon === undefined ||
      !Number.isFinite(lat) ||
      !Number.isFinite(lon) ||
      (lat === 0 && lon === 0);
    if (bad) {
      missing += 1;
      return;
    }
    const key = coordKey(lat, lon);
    const hit = groups.get(key);
    if (hit) {
      hit.items.push(s);
      const st = statusOf(s.status);
      if (st === 'online' || (st === 'offline' && hit.status === 'unknown')) hit.status = st;
      return;
    }
    groups.set(key, { key, lat, lon, items: [s], status: statusOf(s.status) });
  });

  const list = [...groups.values()].map((g) => {
    const head = g.items[0];
    const region =
      [String(head.ip_country || '').trim(), String(head.ip_region || '').trim()].filter(Boolean).join(' ') || '未知地区';
    return {
      key: g.key,
      sid: g.key,
      lng: g.lon,
      lat: g.lat,
      status: g.status,
      color: STATUS_META[g.status].color,
      size: Math.min(20, 15 + (g.items.length - 1) * 2),
      count: g.items.length,
      name: String(head.name || `#${head.id}`),
      region,
      server: head,
    };
  });

  return { list, missing, byKey: new Map(list.map((p) => [p.key, p])) };
}

function popupHtml(p) {
  const meta = STATUS_META[p.status] || STATUS_META.unknown;
  const title = p.count > 1 ? `${p.name} 等 ${p.count} 台` : p.name;
  return (
    '<div style="font-family:inherit;line-height:1.6;min-width:120px">' +
    `<div style="font-size:13px;font-weight:600;color:#0f172a">${escapeHtml(title)}</div>` +
    `<div style="font-size:12px;color:#64748b;margin-top:2px">${escapeHtml(p.region)}</div>` +
    '<div style="display:flex;align-items:center;gap:6px;font-size:12px;color:#334155;margin-top:4px">' +
    `<span style="width:8px;height:8px;border-radius:50%;background:${meta.color};display:inline-block"></span>${meta.label}` +
    (p.count > 1 ? '<span style="color:#94a3b8">· 点击查看第一台</span>' : '') +
    '</div>' +
    '</div>'
  );
}

export default function L7WorldMap({ servers = [], onPick, height = 360, fill = false }) {
  const hostRef = useRef(null);
  const sceneRef = useRef(null);
  const layerRef = useRef(null);
  const popupRef = useRef(null);
  const pointsRef = useRef({ list: [], missing: 0, byKey: new Map() });
  const pickRef = useRef(onPick);

  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState('');
  const [slow, setSlow] = useState(false);
  const [retry, setRetry] = useState(0);

  pickRef.current = onPick;

  const data = useMemo(() => toPoints(servers), [servers]);
  pointsRef.current = data;
  const hasPoints = data.list.length > 0;

  const pointOfFeature = (feature) => {
    if (!feature) return null;
    const byKey = pointsRef.current.byKey;
    if (feature.sid !== undefined && feature.sid !== null) {
      const hit = byKey.get(String(feature.sid));
      if (hit) return hit;
    }
    const lat = feature.lat !== undefined ? feature.lat : feature.latitude;
    const lng = feature.lng !== undefined ? feature.lng : feature.longitude;
    if (!Number.isFinite(Number(lat)) || !Number.isFinite(Number(lng))) return null;
    return byKey.get(coordKey(lat, lng)) || null;
  };

  const showPopup = (feature) => {
    const popup = popupRef.current;
    const p = pointOfFeature(feature);
    if (!popup || !p) return;
    try {
      popup.setLnglat([p.lng, p.lat]).setHTML(popupHtml(p));
    } catch (e) {
      /* Popup 定位失败不应影响地图 */
    }
  };

  const hidePopup = () => {
    const popup = popupRef.current;
    if (!popup) return;
    try {
      popup.hide();
    } catch (e) {
      /* ignore */
    }
  };

  /* 建场景：只在「有点位」时创建，卸载 / 无可展示点位时销毁 */
  useEffect(() => {
    if (!hasPoints) return undefined;
    const host = hostRef.current;
    if (!host) return undefined;

    let disposed = false;
    let scene = null;
    let ro = null;
    setReady(false);
    setFailed('');
    setSlow(false);

    const slowTimer = window.setTimeout(() => {
      if (!disposed) setSlow(true);
    }, 12000);

    try {
      scene = new Scene({
        id: host,
        logoVisible: false,
        map: new MapLibre({
          style: MAP_STYLE,
          center: [20, 25],
          zoom: 2.1,
          minZoom: 1.4,
          maxZoom: 6,
          renderWorldCopies: true,
          maxBounds: [[-190, -60], [190, 80]],
          pitch: 0,
          rotation: 0,
          interactive: true,
          scrollZoom: true, // 滚轮缩放
          dragRotate: false, // 关闭右键拖拽旋转
          pitchWithRotate: false, // 关闭随旋转产生的倾斜
          touchPitch: false, // 关闭双指倾斜
          attributionControl: false,
        }),
      });
    } catch (e) {
      window.clearTimeout(slowTimer);
      setFailed((e && e.message) || '地图初始化失败');
      return undefined;
    }

    sceneRef.current = scene;

    /* 容器宽度变化（侧栏折叠/窗口缩放/数据加载后重排）时同步地图尺寸 */
    try {
      const raw = scene.map;
      ro = new ResizeObserver(() => {
        try {
          raw.resize();
        } catch (e) {
          /* ignore */
        }
      });
      ro.observe(host);

    } catch (e) {
      /* ResizeObserver 不可用时忽略 */
    }

    const boot = () => {
      if (disposed) return;
      /* 触摸屏保留双指缩放，但禁掉旋转 */
      try {
        const raw = scene.map;
        if (raw && raw.touchZoomRotate && raw.touchZoomRotate.disableRotation) raw.touchZoomRotate.disableRotation();
      } catch (e) {
        /* ignore */
      }
      try {
        const layer = new PointLayer({ zIndex: 10, name: 'server-points' })
          .source(pointsRef.current.list, { parser: { type: 'json', x: 'lng', y: 'lat' } })
          .shape('circle')
          .color('color', (c) => c)
          .size('size', (n) => n)
          .style({ stroke: '#ffffff', strokeWidth: 2, opacity: 1, strokeOpacity: 1 });

        layer.on('mouseenter', (e) => showPopup(e && e.feature));
        layer.on('mouseout', hidePopup);
        layer.on('click', (e) => {
          const p = pointOfFeature(e && e.feature);
          if (p && p.server && typeof pickRef.current === 'function') pickRef.current(p.server);
        });

        scene.addLayer(layer);
        layerRef.current = layer;

        const popup = new Popup({ closeButton: false, closeOnClick: false, autoClose: false, offsets: [0, -10] });
        popupRef.current = popup;
        scene.addPopup(popup);

        setReady(true);
        setSlow(false);
        window.clearTimeout(slowTimer);
      } catch (e) {
        setFailed((e && e.message) || '地图图层初始化失败');
      }
    };

    if (scene.loaded) boot();
    else scene.on('loaded', boot);

    return () => {
      disposed = true;
      window.clearTimeout(slowTimer);
      layerRef.current = null;
      popupRef.current = null;
      sceneRef.current = null;
      try {
        scene.destroy();
      } catch (e) {
        /* 销毁失败不应影响页面 */
      }
      /* L7 的 destroy 不一定清掉自己塞进容器里的 DOM，重试时手动兜底 */
      try {
        if (ro && ro.disconnect) ro.disconnect();
      } catch (e) {
        /* ignore */
      }
      try {
        if (host) host.innerHTML = '';
      } catch (e) {
        /* ignore */
      }
    };
  }, [hasPoints, retry]);

  /* 轮询刷新：不重建场景，只更新图层数据 */
  useEffect(() => {
    const layer = layerRef.current;
    if (!layer) return;
    try {
      layer.setData(data.list);
    } catch (e) {
      /* 数据更新失败保留上一次点位 */
    }
  }, [data]);

  const shellStyle = {
    position: 'relative',
    width: '100%',
    height: fill ? '100%' : height,
    minHeight: fill ? 300 : undefined,
    borderRadius: 14,
    border: '1px solid #eef2f7',
    background: '#f6f7fb',
    overflow: 'hidden',
  };

  if (!servers.length) {
    return (
      <div style={{ ...shellStyle, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有服务器" />
      </div>
    );
  }

  if (!hasPoints) {
    return (
      <div style={{ ...shellStyle, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="服务器还没有经纬度信息，暂时无法在地图上定位"
        />
      </div>
    );
  }

  return (
    <div style={shellStyle}>
      {/* L7 直接往这个 div 里挂地图 DOM，React 不渲染它的子节点 */}
      <div ref={hostRef} style={{ position: 'absolute', inset: 0 }} aria-label="服务器全球分布地图" />

      {!ready && !failed ? (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            zIndex: 4,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            gap: 10,
            background: 'rgba(248, 250, 252, 0.86)',
          }}
        >
          {slow ? (
            <>
              <span style={{ fontSize: 12, color: '#94a3b8' }}>底图加载较慢，请检查网络后重试</span>
              <Button size="small" onClick={() => setRetry((n) => n + 1)}>
                重试
              </Button>
            </>
          ) : (
            <>
              <Spin size="small" />
              <span style={{ fontSize: 12, color: '#94a3b8' }}>地图加载中…</span>
            </>
          )}
        </div>
      ) : null}

      {failed ? (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            zIndex: 5,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: '#f6f7fb',
            padding: 16,
          }}
        >
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={<span style={{ fontSize: 12, color: '#94a3b8' }}>地图加载失败：{failed}</span>}
          >
            <Button size="small" onClick={() => setRetry((n) => n + 1)}>
              重试
            </Button>
          </Empty>
        </div>
      ) : null}

      {ready && data.missing > 0 ? (
        <div
          style={{
            position: 'absolute',
            left: 10,
            bottom: 8,
            zIndex: 3,
            padding: '2px 8px',
            borderRadius: 8,
            border: '1px solid #eef2f7',
            background: 'rgba(255, 255, 255, 0.92)',
            fontSize: 11,
            color: '#94a3b8',
            pointerEvents: 'none',
            whiteSpace: 'nowrap',
          }}
        >
          另有 {data.missing} 台缺少经纬度，未显示
        </div>
      ) : null}
    </div>
  );
}
