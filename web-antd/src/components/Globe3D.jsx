import React, { useEffect, useRef } from 'react';
import * as THREE from 'three';

/**
 * WebGL 3D 地球（three.js）
 *  - world.svg 绘制到 Canvas 后重着色为球面贴图
 *  - 大气层 Fresnel 光晕
 *  - 服务器经纬度转球面坐标，光点呼吸
 *  - 拖拽旋转 + 自动自转，点击光点回调
 */
export default function Globe3D({ servers = [], onPick, height = 340 }) {
  const hostRef = useRef(null);
  const stateRef = useRef({});
  const onPickRef = useRef(onPick);
  useEffect(() => { onPickRef.current = onPick; }, [onPick]);
  useEffect(() => { if (stateRef.current.rebuildMarkers) stateRef.current.rebuildMarkers(servers); }, [servers]);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return undefined;

    let renderer;
    try {
      renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
    } catch (e) {
      host.innerHTML = '<div style="color:#94a3b8;font-size:12px;padding:20px">当前浏览器不支持 WebGL</div>';
      return undefined;
    }
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    renderer.setSize(host.clientWidth, host.clientHeight);
    host.appendChild(renderer.domElement);

    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(38, host.clientWidth / host.clientHeight, 0.1, 100);
    camera.position.set(0, 0.25, 3.2);

    const group = new THREE.Group();
    scene.add(group);

    scene.add(new THREE.AmbientLight(0xbfdbfe, 1.1));
    const dir = new THREE.DirectionalLight(0xffffff, 1.5);
    dir.position.set(-2, 1.6, 2.4);
    scene.add(dir);
    const rim = new THREE.DirectionalLight(0x38bdf8, 0.7);
    rim.position.set(2, -1, -2);
    scene.add(rim);

    // ------ 贴图：把 world.svg 重着色成海洋深蓝 / 陆地青绿 ------
    const texCanvas = document.createElement('canvas');
    texCanvas.width = 2048;
    texCanvas.height = 1024;
    const tctx = texCanvas.getContext('2d');
    tctx.fillStyle = '#0b2a4a';
    tctx.fillRect(0, 0, texCanvas.width, texCanvas.height);

    const sphereGeo = new THREE.SphereGeometry(1, 96, 64);
    const sphereMat = new THREE.MeshPhongMaterial({ color: 0x0d2f52, shininess: 12, specular: 0x224466 });
    const sphere = new THREE.Mesh(sphereGeo, sphereMat);
    group.add(sphere);

    const img = new Image();
    img.crossOrigin = 'anonymous';
    img.onload = () => {
      tctx.drawImage(img, 0, 0, texCanvas.width, texCanvas.height);
      const data = tctx.getImageData(0, 0, texCanvas.width, texCanvas.height);
      const px = data.data;
      for (let i = 0; i < px.length; i += 4) {
        const lum = (px[i] * 0.299 + px[i + 1] * 0.587 + px[i + 2] * 0.114) / 255;
        // world.svg：背景 #f7f9fb（亮），陆地 #dfe6ee（略暗），用亮度阈值区分
        if (lum < 0.945) {
          // 陆地：青绿渐变，纬度越高越亮
          const y = Math.floor(i / 4 / texCanvas.width);
          const t = 1 - y / texCanvas.height;
          px[i] = 10 + 14 * t;
          px[i + 1] = 52 + 40 * t;
          px[i + 2] = 66 + 44 * t;
        } else {
          px[i] = 4;
          px[i + 1] = 16;
          px[i + 2] = 32;
        }
      }
      tctx.putImageData(data, 0, 0);
      const tex = new THREE.CanvasTexture(texCanvas);
      tex.colorSpace = THREE.SRGBColorSpace;
      tex.anisotropy = renderer.capabilities.getMaxAnisotropy();
      sphereMat.map = tex;
      sphereMat.color = new THREE.Color(0xffffff);
      sphereMat.needsUpdate = true;
    };
    img.src = '/assets/world.svg';

    // ------ 大气层 ------
    const atmoMat = new THREE.ShaderMaterial({
      transparent: true,
      side: THREE.BackSide,
      blending: THREE.AdditiveBlending,
      uniforms: { glowColor: { value: new THREE.Color(0x38bdf8) } },
      vertexShader: `varying vec3 vNormal; void main(){ vNormal = normalize(normalMatrix * normal); gl_Position = projectionMatrix * modelViewMatrix * vec4(position,1.0); }`,
      fragmentShader: `varying vec3 vNormal; uniform vec3 glowColor; void main(){ float intensity = pow(0.72 - dot(vNormal, vec3(0.0,0.0,1.0)), 3.0); gl_FragColor = vec4(glowColor, 1.0) * intensity; }`,
    });
    const atmo = new THREE.Mesh(new THREE.SphereGeometry(1.14, 96, 64), atmoMat);
    scene.add(atmo);
    // ------ 经纬网格（全息线条） ------
    const gridMat = new THREE.LineBasicMaterial({ color: 0x38bdf8, transparent: true, opacity: 0.16, blending: THREE.AdditiveBlending });
    const gridGroup = new THREE.Group();
    for (let lon = -180; lon < 180; lon += 15) {
      const pts = [];
      for (let lat = -90; lat <= 90; lat += 4) {
        const phi = ((90 - lat) * Math.PI) / 180;
        const theta = ((lon + 180) * Math.PI) / 180;
        pts.push(new THREE.Vector3(-Math.sin(phi) * Math.cos(theta), Math.cos(phi), Math.sin(phi) * Math.sin(theta)));
      }
      gridGroup.add(new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), gridMat));
    }
    for (let lat = -75; lat <= 75; lat += 15) {
      const pts = [];
      const phi = ((90 - lat) * Math.PI) / 180;
      for (let lon = -180; lon <= 180; lon += 4) {
        const theta = ((lon + 180) * Math.PI) / 180;
        pts.push(new THREE.Vector3(-Math.sin(phi) * Math.cos(theta), Math.cos(phi), Math.sin(phi) * Math.sin(theta)));
      }
      gridGroup.add(new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), gridMat));
    }
    group.add(gridGroup);

    // ------ 星点背景 ------
    const starGeo = new THREE.BufferGeometry();
    const starPos = new Float32Array(900 * 3);
    for (let i = 0; i < 900; i += 1) {
      const r = 2.6 + Math.random() * 1.8;
      const th = Math.random() * Math.PI * 2;
      const ph = Math.acos(2 * Math.random() - 1);
      starPos[i * 3] = r * Math.sin(ph) * Math.cos(th);
      starPos[i * 3 + 1] = r * Math.cos(ph);
      starPos[i * 3 + 2] = r * Math.sin(ph) * Math.sin(th);
    }
    starGeo.setAttribute('position', new THREE.BufferAttribute(starPos, 3));
    const stars = new THREE.Points(starGeo, new THREE.PointsMaterial({ color: 0x7dd3fc, size: 0.012, transparent: true, opacity: 0.7, blending: THREE.AdditiveBlending }));
    scene.add(stars);

    // ------ 旋转光环 ------
    const rings = [];
    [[1.28, 0.0025, 0.35], [1.42, 0.0018, -0.5]].forEach(([radius, speed, tilt]) => {
      const ring = new THREE.Mesh(
        new THREE.TorusGeometry(radius, 0.0035, 8, 160),
        new THREE.MeshBasicMaterial({ color: 0x38bdf8, transparent: true, opacity: 0.35, blending: THREE.AdditiveBlending }),
      );
      ring.rotation.x = Math.PI / 2 + tilt;
      ring.userData.speed = speed;
      scene.add(ring);
      rings.push(ring);
    });

    // ------ 服务器光点（可增量重建，避免整球重建） ------
    const markers = [];
    const markerGroup = new THREE.Group();
    group.add(markerGroup);
    const rebuildMarkers = (list) => {
      for (const m of markers) {
        markerGroup.remove(m);
        m.geometry.dispose();
        m.material.dispose();
      }
      markers.length = 0;
      for (const s of list || []) {
        const lat = Number(s.ip_lat);
        const lon = Number(s.ip_lon);
        if (s.ip_lat === null || s.ip_lon === null || !Number.isFinite(lat) || !Number.isFinite(lon)) continue;
        const phi = ((90 - lat) * Math.PI) / 180;
        const theta = ((lon + 180) * Math.PI) / 180;
        const r = 1.012;
        const pos = new THREE.Vector3(
          -r * Math.sin(phi) * Math.cos(theta),
          r * Math.cos(phi),
          r * Math.sin(phi) * Math.sin(theta),
        );
        const online = s.last_status === 'online' || s.status === 'online';
        const color = online ? 0x22c55e : s.last_status === 'unknown' ? 0xcbd5e1 : 0xf43f5e;
        const mesh = new THREE.Mesh(
          new THREE.SphereGeometry(0.013, 12, 12),
          new THREE.MeshBasicMaterial({ color }),
        );
        mesh.position.copy(pos);
        mesh.userData = { server: s };
        markerGroup.add(mesh);
        markers.push(mesh);
      }
    };
    rebuildMarkers(servers);
    stateRef.current.rebuildMarkers = rebuildMarkers;

    // 初始朝向：经度 105° 附近（东亚）
    group.rotation.y = (105 * Math.PI) / 180;

    // ------ 交互 ------
    let dragging = false;
    let lastX = 0;
    let spin = true;
    const raycaster = new THREE.Raycaster();
    const pointer = new THREE.Vector2();

    const onDown = (e) => {
      dragging = true;
      spin = false;
      lastX = e.clientX;
      renderer.domElement.setPointerCapture?.(e.pointerId);
    };
    const onMove = (e) => {
      if (!dragging) return;
      group.rotation.y += (e.clientX - lastX) * 0.005;
      lastX = e.clientX;
    };
    const onUp = (e) => {
      if (!dragging) return;
      dragging = false;
      const rect = renderer.domElement.getBoundingClientRect();
      pointer.x = ((e.clientX - rect.left) / rect.width) * 2 - 1;
      pointer.y = -((e.clientY - rect.top) / rect.height) * 2 + 1;
      raycaster.setFromCamera(pointer, camera);
      const hit = raycaster.intersectObjects(markers, false)[0];
      if (hit && onPickRef.current) onPickRef.current(hit.object.userData.server);
      setTimeout(() => { spin = true; }, 2500);
    };
    const el = renderer.domElement;
    el.addEventListener('pointerdown', onDown);
    el.addEventListener('pointermove', onMove);
    el.addEventListener('pointerup', onUp);

    const onResize = () => {
      const w = host.clientWidth;
      const h = host.clientHeight;
      if (w === 0 || h === 0) return;
      renderer.setSize(w, h);
      camera.aspect = w / h;
      camera.updateProjectionMatrix();
    };
    const ro = new ResizeObserver(onResize);
    ro.observe(host);

    let raf = 0;
    const clock = new THREE.Clock();
    const animate = () => {
      raf = requestAnimationFrame(animate);
      const t = clock.getElapsedTime();
      if (spin && !dragging) group.rotation.y += 0.0016;
      const pulse = 1 + Math.sin(t * 2.4) * 0.25;
      markers.forEach((m) => m.scale.setScalar(pulse));
      atmo.scale.setScalar(1 + Math.sin(t * 1.2) * 0.006);
      rings.forEach((r) => { r.rotation.z += r.userData.speed; });
      renderer.render(scene, camera);
    };
    animate();

    stateRef.current = {};
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      el.removeEventListener('pointerdown', onDown);
      el.removeEventListener('pointermove', onMove);
      el.removeEventListener('pointerup', onUp);
      renderer.dispose();
      sphereGeo.dispose();
      sphereMat.dispose();
      markers.forEach((m) => { m.geometry.dispose(); m.material.dispose(); });
      if (host.contains(el)) host.removeChild(el);
    };
  }, []); // 只初始化一次，服务器变化走 rebuildMarkers

  return (
    <div
      ref={hostRef}
      className="pp-globe3d"
      style={{ width: '100%', height, position: 'relative', borderRadius: 14, overflow: 'hidden', background: 'radial-gradient(60% 60% at 50% 45%, #0b2545 0%, #071528 60%, #050d18 100%)' }}
    />
  );
}
