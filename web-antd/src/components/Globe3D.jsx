import React, { useEffect, useRef } from 'react';
import * as THREE from 'three';

/**
 * WebGL 3D 地球（three.js，重写版）
 *  - world.svg 重着色为球面贴图（海洋深蓝 / 陆地暗青）
 *  - 大气光晕 + 经纬网格 + 旋转光环 + 扫描带
 *  - 服务器光点：Points（固定像素大小），从 props 实时重建
 *  - 拖拽旋转 / 自动自转 / 点击光点
 */
export default function Globe3D({ servers = [], onPick, height = 320 }) {
  const hostRef = useRef(null);
  const apiRef = useRef({});
  const serversRef = useRef(servers);
  const pickRef = useRef(onPick);

  serversRef.current = servers;
  pickRef.current = onPick;

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return undefined;

    let renderer;
    try {
      renderer = new THREE.WebGLRenderer({ antialias: false, alpha: true, powerPreference: 'high-performance' });
    } catch (e) {
      host.innerHTML = '<div style="color:#94a3b8;font-size:12px;padding:16px">当前浏览器不支持 WebGL</div>';
      return undefined;
    }
    // 高 DPI 下 2 倍分辨率会让像素量翻 4 倍，是卡顿主因；1.5 倍视觉几乎无差别
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5));
    renderer.setSize(host.clientWidth, host.clientHeight);
    host.appendChild(renderer.domElement);

    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(40, host.clientWidth / host.clientHeight, 0.1, 100);
    camera.position.set(0, 0.15, 3.1);

    const group = new THREE.Group();
    scene.add(group);

    scene.add(new THREE.AmbientLight(0xffffff, 1.2));
    const key = new THREE.DirectionalLight(0xffffff, 1.4);
    key.position.set(-2, 1.5, 2.2);
    scene.add(key);

    // 球体
    const sphereMat = new THREE.MeshPhongMaterial({ color: 0x0b2a4a, shininess: 8, specular: 0x1e3a5f });
    const sphere = new THREE.Mesh(new THREE.SphereGeometry(1, 64, 44), sphereMat);
    group.add(sphere);

    // 点阵球面（参考 emerald 主题的粒子地球）
    const dotCount = 2000;
    const dotPositions = new Float32Array(dotCount * 3);
    for (let i = 0; i < dotCount; i += 1) {
      const u = Math.random() * 2 - 1;
      const th = Math.random() * Math.PI * 2;
      const r = 1.004;
      const s = Math.sqrt(1 - u * u);
      dotPositions[i * 3] = r * s * Math.cos(th);
      dotPositions[i * 3 + 1] = r * u;
      dotPositions[i * 3 + 2] = r * s * Math.sin(th);
    }
    const dotGeo = new THREE.BufferGeometry();
    dotGeo.setAttribute('position', new THREE.BufferAttribute(dotPositions, 3));
    const dotCloud = new THREE.Points(dotGeo, new THREE.PointsMaterial({
      color: 0x67e8f9, size: 0.006, transparent: true, opacity: 0.55,
      depthWrite: false, blending: THREE.AdditiveBlending,
    }));
    group.add(dotCloud);

    // 贴图重着色
    const canvas = document.createElement('canvas');
    canvas.width = 1440;
    canvas.height = 720;
    const cctx = canvas.getContext('2d');
    cctx.fillStyle = '#04121f';
    cctx.fillRect(0, 0, canvas.width, canvas.height);
    const img = new Image();
    // 把矢量图按给定经纬范围铺到任意画布上（支持经度环绕）
    const paintMap = (ctx, w, h, topLat, botLat, lonOff) => {
      ctx.fillStyle = '#04121f';
      ctx.fillRect(0, 0, w, h);
      const y0 = ((90 - topLat) / 180) * h;
      const y1 = ((90 - botLat) / 180) * h;
      const shift = (lonOff / 360) * w;
      ctx.drawImage(img, shift, y0, w, y1 - y0);
      if (shift > 0) ctx.drawImage(img, shift - w, y0, w, y1 - y0);
      if (shift < 0) ctx.drawImage(img, shift + w, y0, w, y1 - y0);
    };

    /**
     * 自动校准：world.svg 并非标准等距圆柱投影，直接铺满会让大陆与经纬度错位。
     * 做法是把已知服务器的经纬度投到贴图上，统计"落在陆地上的点数"，
     * 在一组候选参数里选出命中率最高的投影范围。
     */
    const calibrate = () => {
      const list = (serversRef.current || []).filter(
        (s) => Number.isFinite(Number(s.ip_lat)) && Number.isFinite(Number(s.ip_lon)),
      );
      if (!list.length) return { topLat: 84, botLat: -72, lonOff: 0 };

      const W = 360;
      const H = 180;
      const probe = document.createElement('canvas');
      probe.width = W;
      probe.height = H;
      const pctx = probe.getContext('2d', { willReadFrequently: true });

      let best = { topLat: 84, botLat: -72, lonOff: 0, score: -1 };
      for (const lonOff of [-40, -30, -20, -10, 0, 10, 20, 30, 40]) {
        for (const topLat of [90, 86, 82, 78, 74]) {
          for (const botLat of [-90, -82, -74, -66, -58]) {
            paintMap(pctx, W, H, topLat, botLat, lonOff);
            const d = pctx.getImageData(0, 0, W, H).data;
            let score = 0;
            for (const s of list) {
              const lat = Number(s.ip_lat);
              const lon = Number(s.ip_lon);
              const u = (lon + 180 + lonOff + 540) % 360 - 180;
              const x = Math.round(((((lon + 180 + lonOff) % 360) + 360) % 360) / 360 * (W - 1));
              const y = Math.round(((90 - lat) / 180) * (H - 1));
              if (x < 0 || x >= W || y < 0 || y >= H) continue;
              const i = (y * W + x) * 4;
              const lum = (d[i] * 0.299 + d[i + 1] * 0.587 + d[i + 2] * 0.114) / 255;
              if (lum < 0.86) score += 1;   // 深色即陆地
            }
            if (score > best.score) best = { topLat, botLat, lonOff, score };
          }
        }
      }
      return best;
    };

    img.onload = () => {
      // 贴图已是标准等距圆柱投影，直接满幅铺设即可精确对齐
      let params = { topLat: 90, botLat: -90, lonOff: 0 };
      // 无需校准：world.svg 由 Natural Earth 按等距圆柱生成
      paintMap(cctx, canvas.width, canvas.height, params.topLat, params.botLat, params.lonOff);
      const data = cctx.getImageData(0, 0, canvas.width, canvas.height);
      const px = data.data;
      for (let i = 0; i < px.length; i += 4) {
        const lum = (px[i] * 0.299 + px[i + 1] * 0.587 + px[i + 2] * 0.114) / 255;
        if (lum < 0.945) {
          px[i] = 12; px[i + 1] = 62; px[i + 2] = 78;
        } else {
          px[i] = 5; px[i + 1] = 18; px[i + 2] = 34;
        }
      }
      cctx.putImageData(data, 0, 0);
      const tex = new THREE.CanvasTexture(canvas);
      tex.colorSpace = THREE.SRGBColorSpace;
      sphereMat.map = tex;
      sphereMat.color = new THREE.Color(0xffffff);
      sphereMat.needsUpdate = true;
      apiRef.current.mapParams = params;
    };
    img.src = '/assets/world.svg';

    // 经纬网格
    const gridMat = new THREE.LineBasicMaterial({ color: 0x38bdf8, transparent: true, opacity: 0.16, blending: THREE.AdditiveBlending });
    const grid = new THREE.Group();
    for (let lon = -180; lon < 180; lon += 15) {
      const pts = [];
      for (let lat = -90; lat <= 90; lat += 4) {
        const phi = ((90 - lat) * Math.PI) / 180;
        const theta = ((lon + 180) * Math.PI) / 180;
        pts.push(new THREE.Vector3(-Math.sin(phi) * Math.cos(theta), Math.cos(phi), Math.sin(phi) * Math.sin(theta)));
      }
      grid.add(new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), gridMat));
    }
    for (let lat = -75; lat <= 75; lat += 15) {
      const pts = [];
      const phi = ((90 - lat) * Math.PI) / 180;
      for (let lon = -180; lon <= 180; lon += 4) {
        const theta = ((lon + 180) * Math.PI) / 180;
        pts.push(new THREE.Vector3(-Math.sin(phi) * Math.cos(theta), Math.cos(phi), Math.sin(phi) * Math.sin(theta)));
      }
      grid.add(new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), gridMat));
    }
    group.add(grid);

    // 大气
    const atmo = new THREE.Mesh(
      new THREE.SphereGeometry(1.12, 40, 28),
      new THREE.MeshBasicMaterial({ color: 0x38bdf8, transparent: true, opacity: 0.1, side: THREE.BackSide, blending: THREE.AdditiveBlending }),
    );
    scene.add(atmo);

    // 光环
    const rings = [];
    [[1.28, 0.0022, 0.3], [1.4, 0.0016, -0.45]].forEach(([r, speed]) => {
      const ring = new THREE.Mesh(
        new THREE.TorusGeometry(r, 0.003, 6, 96),
        new THREE.MeshBasicMaterial({ color: 0x38bdf8, transparent: true, opacity: 0.3, blending: THREE.AdditiveBlending }),
      );
      ring.rotation.x = Math.PI / 2 + 0.3;
      ring.userData.speed = speed;
      scene.add(ring);
      rings.push(ring);
    });

    // 扫描带
    const scan = new THREE.Mesh(
      new THREE.TorusGeometry(1, 0.005, 6, 96),
      new THREE.MeshBasicMaterial({ color: 0x67e8f9, transparent: true, opacity: 0.5, blending: THREE.AdditiveBlending }),
    );
    scan.rotation.x = Math.PI / 2;
    scene.add(scan);

    // ---------------- 服务器光点 ----------------
    const glowCanvas = document.createElement('canvas');
    glowCanvas.width = 64; glowCanvas.height = 64;
    const gctx = glowCanvas.getContext('2d');
    const grad = gctx.createRadialGradient(32, 32, 0, 32, 32, 32);
    grad.addColorStop(0, 'rgba(255,255,255,1)');
    grad.addColorStop(0.4, 'rgba(255,255,255,0.7)');
    grad.addColorStop(1, 'rgba(255,255,255,0)');
    gctx.fillStyle = grad;
    gctx.fillRect(0, 0, 64, 64);
    const dotTex = new THREE.CanvasTexture(glowCanvas);

    let points = null;
    const rebuildPoints = () => {
      const list = serversRef.current || [];
      const positions = [];
      const colors = [];
      for (const s of list) {
        const lat = Number(s.ip_lat);
        const lon = Number(s.ip_lon);
        if (!Number.isFinite(lat) || !Number.isFinite(lon)) continue;
        const phi = ((90 - lat) * Math.PI) / 180;
        const theta = ((lon + 180) * Math.PI) / 180;
        const r = 1.012;
        positions.push(-r * Math.sin(phi) * Math.cos(theta), r * Math.cos(phi), r * Math.sin(phi) * Math.sin(theta));
        const online = s.last_status === 'online' || s.status === 'online';
        const c = new THREE.Color(online ? 0x22c55e : 0xf43f5e);
        colors.push(c.r, c.g, c.b);
      }
      if (points) {
        group.remove(points);
        points.geometry.dispose();
        points.material.dispose();
        points = null;
      }
      if (!positions.length) return;
      const geo = new THREE.BufferGeometry();
      geo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
      geo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
      const mat = new THREE.PointsMaterial({
        size: 13,
        sizeAttenuation: false,
        vertexColors: true,
        map: dotTex,
        transparent: true,
        // 关键修复：开启深度测试，否则背面光点会透过地球显示，看起来"位置不准"
        depthTest: true,
        depthWrite: false,
        alphaTest: 0.02,
      });
      points = new THREE.Points(geo, mat);
      points.renderOrder = 10;
      group.add(points);
    };
    rebuildPoints();
    apiRef.current.rebuild = rebuildPoints;

    group.rotation.y = (105 * Math.PI) / 180;

    // 交互
    let dragging = false;
    let lastX = 0;
    let spin = true;
    const raycaster = new THREE.Raycaster();
    raycaster.params.Points.threshold = 0.05;
    const pointer = new THREE.Vector2();

    const el = renderer.domElement;
    const onDown = (e) => { dragging = true; spin = false; lastX = e.clientX; el.setPointerCapture?.(e.pointerId); };
    const onMove = (e) => { if (!dragging) return; group.rotation.y += (e.clientX - lastX) * 0.006; lastX = e.clientX; };
    const onUp = (e) => {
      dragging = false;
      if (!points) return;
      const rect = el.getBoundingClientRect();
      pointer.x = ((e.clientX - rect.left) / rect.width) * 2 - 1;
      pointer.y = -((e.clientY - rect.top) / rect.height) * 2 + 1;
      raycaster.setFromCamera(pointer, camera);
      const hit = raycaster.intersectObject(points, false)[0];
      if (hit && pickRef.current) {
        const geoList = points.geometry.getAttribute('position').count;
        const s = (serversRef.current || []).filter((x) => Number.isFinite(Number(x.ip_lat)) && Number.isFinite(Number(x.ip_lon)))[hit.index];
        if (s) pickRef.current(s);
      }
      setTimeout(() => { spin = true; }, 2000);
    };
    el.addEventListener('pointerdown', onDown);
    el.addEventListener('pointermove', onMove);
    el.addEventListener('pointerup', onUp);

    const onResize = () => {
      const w = host.clientWidth;
      const h = host.clientHeight;
      if (!w || !h) return;
      renderer.setSize(w, h);
      camera.aspect = w / h;
      camera.updateProjectionMatrix();
    };
    const ro = new ResizeObserver(onResize);
    ro.observe(host);

    let raf = 0;
    let visible = true;
    const io = new IntersectionObserver((entries) => {
      visible = entries[0] ? entries[0].isIntersecting : true;
    }, { threshold: 0.01 });
    io.observe(host);
    const clock = new THREE.Clock();
    // 交互性能：限到 ~30fps，页面不可见时彻底停渲染，避免与页面重渲染抢主线程
    const FRAME_MS = 33;
    let lastDraw = 0;
    const loop = (now) => {
      raf = requestAnimationFrame(loop);
      if (document.hidden || !visible) return;
      if (now - lastDraw < FRAME_MS) return;
      lastDraw = now;
      const t = clock.getElapsedTime();
      if (spin && !dragging) group.rotation.y += 0.0015;
      rings.forEach((r) => { r.rotation.z += r.userData.speed; });
      const sy = Math.sin(t * 0.55) * 0.92;
      const sr = Math.sqrt(Math.max(0.02, 1 - sy * sy));
      scan.position.y = sy;
      scan.scale.set(sr, sr, 1);
      renderer.render(scene, camera);
    };
    loop();

    return () => {
      cancelAnimationFrame(raf);
      io.disconnect();
      ro.disconnect();
      el.removeEventListener('pointerdown', onDown);
      el.removeEventListener('pointermove', onMove);
      el.removeEventListener('pointerup', onUp);
      renderer.dispose();
      sphere.geometry.dispose();
      sphereMat.dispose();
      if (points) { points.geometry.dispose(); points.material.dispose(); }
      dotTex.dispose();
      if (host.contains(el)) host.removeChild(el);
    };
  }, []);

  // servers 变化时重建光点
  useEffect(() => {
    if (apiRef.current.rebuild) apiRef.current.rebuild();
  }, [servers]);

  return (
    <div
      ref={hostRef}
      className="pp-globe3d"
      style={{ width: '100%', height, position: 'relative', borderRadius: 12, overflow: 'hidden', background: 'transparent' }}
    />
  );
}
