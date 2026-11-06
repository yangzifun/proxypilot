import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Tooltip } from 'antd';

/**
 * 3D 地球视图（SVG/HTML 实现，无需 WebGL）：
 *  - 用 /assets/world.svg 作为球面贴图，横向平移模拟自转
 *  - 经纬度按正射投影换算成屏幕坐标，仅显示朝向观测者的一面
 *  - 支持拖拽旋转、悬停显示服务器信息、点击回调
 */
export default function GlobeMap({ servers = [], onPick, height = 360 }) {
  const wrapRef = useRef(null);
  const [size, setSize] = useState(320);
  const [centerLon, setCenterLon] = useState(105);
  const [dragging, setDragging] = useState(false);
  const spinRef = useRef(true);
  const dragRef = useRef(null);

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return undefined;
    const ro = new ResizeObserver(() => {
      const b = el.getBoundingClientRect();
      setSize(Math.max(180, Math.min(b.width, b.height) - 8));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    let raf = 0;
    let last = performance.now();
    const tick = (now) => {
      const dt = (now - last) / 1000;
      last = now;
      if (!dragRef.current && spinRef.current) {
        setCenterLon((v) => (v + dt * 6) % 360);
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, []);

  const pts = useMemo(() => {
    const R = size / 2 - 4;
    return servers.map((s) => {
      const lat = Number(s.ip_lat);
      const lon = Number(s.ip_lon);
      if (s.ip_lat === null || s.ip_lon === null || !Number.isFinite(lat) || !Number.isFinite(lon)) return null;
      const theta = ((lon - centerLon) * Math.PI) / 180;
      const phi = (lat * Math.PI) / 180;
      const alpha = Math.cos(phi) * Math.cos(theta); // >0 表示朝向观察者
      if (alpha <= 0) return null;
      const x = R * Math.cos(phi) * Math.sin(theta);
      const y = -R * Math.sin(phi);
      return { server: s, x, y, scale: 0.65 + 0.35 * alpha };
    }).filter(Boolean);
  }, [servers, centerLon, size]);

  // 贴图与经纬度对齐：world.svg 为等距圆柱投影，x=(lon+180)/360*width
  let textureX = size / 2 - (((centerLon + 180) / 360) * size);
  textureX = ((textureX % size) + size) % size;
  if (textureX > 0) textureX -= size;

  const onPointerDown = (e) => {
    dragRef.current = { x: e.clientX, lon: centerLon };
    setDragging(true);
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };
  const onPointerMove = (e) => {
    if (!dragRef.current) return;
    const dx = e.clientX - dragRef.current.x;
    setCenterLon(dragRef.current.lon - dx * 0.4);
  };
  const endDrag = () => {
    dragRef.current = null;
    setDragging(false);
  };

  return (
    <div className="pp-globe-wrap" ref={wrapRef} style={{ height }}>
      <div
        className="pp-globe"
        style={{ width: size, height: size, cursor: dragging ? 'grabbing' : 'grab' }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerLeave={endDrag}
      >
        <div className="pp-globe-tex" style={{ transform: `translateX(${textureX}px)` }}>
          <img src="/assets/world.svg" alt="" draggable={false} />
          <img src="/assets/world.svg" alt="" draggable={false} />
        </div>
        <div className="pp-globe-shade" />
        {pts.map(({ server, x, y, scale }) => {
          const online = server.last_status === 'online' || server.status === 'online';
          const color = online ? '#22c55e' : server.last_status === 'unknown' ? '#cbd5e1' : '#f43f5e';
          return (
            <Tooltip key={server.id} title={`${server.name || server.id} · ${online ? '在线' : '离线'}`} mouseEnterDelay={0.1}>
              <span
                className="pp-globe-marker"
                style={{
                  left: `calc(50% + ${x}px)`,
                  top: `calc(50% + ${y}px)`,
                  width: 10 * scale,
                  height: 10 * scale,
                  background: color,
                  boxShadow: `0 0 0 ${2 * scale}px rgba(255,255,255,0.18), 0 0 10px ${color}`,
                }}
                onClick={(e) => {
                  e.stopPropagation();
                  if (onPick) onPick(server);
                }}
              />
            </Tooltip>
          );
        })}
      </div>
      <div className="pp-globe-hint">拖拽旋转 · 点击光点查看服务器</div>
    </div>
  );
}
