/* =================================================================
 *  协议模板编辑器（对齐老后台的 JSON 驱动方式）
 *
 *  设计要点：
 *   · 不再逐个堆协议参数（端口基线 / WARP / 证书路径…），只保留：
 *     模板名称、备注、引擎开关、各引擎的配置文件正文、实时解析结果
 *   · 保存统一走 POST /api/singbox/templates/from-config —— 后端从 JSON 里
 *     反推端口 / 协议开关 / WS 路径 / REALITY 参数 / 用户，id>0 表示更新
 *   · 「生成当前配置」与「填入示例」都由后端渲染（保证密钥是真随机、格式合法）
 * ================================================================= */
import React, { useEffect, useMemo, useState } from 'react';
import { Alert, App as AntdApp, AutoComplete, Button, Checkbox, Col, Empty, Input, InputNumber, Modal, Row, Select, Space, Spin, Tag } from 'antd';
import { FileTextOutlined, ReloadOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { api } from '../api';

/* ---------------- 客户端解析 sb.json（与老后台 singboxParseSbText 同语义） ---------------- */
export function parseSbText(text) {
  const cfg = JSON.parse(String(text || '{}'));
  const list = [];
  for (const ib of cfg.inbounds || []) {
    const row = {
      type: ib.type || '?',
      tag: ib.tag || '',
      port: ib.listen_port || 0,
      listen: ib.listen || '',
      users: (ib.users || []).length,
      extra: [],
    };
    if (ib.type === 'shadowsocks') {
      row.extra.push('方法 ' + (ib.method || '-'));
      row.extra.push('密钥 ' + (ib.password ? '已设置' : '缺失'));
    }
    if (ib.tls && ib.tls.enabled) {
      row.extra.push('TLS 开');
      if (ib.tls.alpn) row.extra.push('ALPN ' + (ib.tls.alpn || []).join('/'));
      if (ib.tls.server_name) row.extra.push('SNI ' + ib.tls.server_name);
      if (ib.tls.reality && ib.tls.reality.enabled) {
        const hs = ib.tls.reality.handshake || {};
        row.extra.push('REALITY ' + (hs.server || '?') + ':' + (hs.server_port || 443));
        row.extra.push('shortId ' + ((ib.tls.reality.short_id || [])[0] || '-'));
      }
    }
    if (ib.transport && ib.transport.type === 'ws') row.extra.push('WS ' + (ib.transport.path || '/'));
    if (Number(ib.up_mbps) || Number(ib.down_mbps)) {
      row.extra.push('上行/下行 ' + (ib.up_mbps || 0) + '/' + (ib.down_mbps || 0) + ' Mbps');
    }
    list.push(row);
  }
  const warp = (cfg.endpoints || []).filter((e) => e && e.type === 'wireguard');
  return { inbounds: list, warp: warp.length, outbounds: (cfg.outbounds || []).length };
}

function randomUuid() {
  try {
    if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID();
  } catch (e) {
    /* ignore */
  }
  const hex = '0123456789abcdef';
  let out = '';
  for (let i = 0; i < 36; i++) {
    if (i === 8 || i === 13 || i === 18 || i === 23) out += '-';
    else if (i === 14) out += '4';
    else if (i === 19) out += hex[((Math.random() * 4) | 0) + 8];
    else out += hex[(Math.random() * 16) | 0];
  }
  return out;
}

/* ---------------- 解析结果：按协议渲染可编辑字段，改动实时写回 sb.json ---------------- */
const SS_METHODS = [
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
  '2022-blake3-chacha20-poly1305',
  'aes-256-gcm',
  'aes-128-gcm',
  'chacha20-ietf-poly1305',
];

/* 每种入站协议暴露哪些字段（没有列出的协议只给通用的标签 / 端口 / 监听） */
const PROTO_UI = {
  shadowsocks: {
    hint: 'Shadowsocks 2022：兼容性最好，客户端直接导入 SS 链接；用户凭据在下发时按授权 UUID 注入，模板里不需要配置。',
    fields: [
      { key: 'method', label: '加密方法', type: 'select', options: SS_METHODS, hint: '2022-blake3 系列要求 32 字节 base64 密钥；老客户端可选 aes-256-gcm' },
    ],
  },
  vless: {
    hint: 'VLESS：REALITY 模式用 xtls-rprx-vision flow，WS 模式走 TLS + WebSocket；UUID 由节点授权时注入。',
    tls: { sni: true, alpn: true, reality: true },
    transport: true,
  },
  vmess: {
    hint: 'VMess：传统协议，客户端兼容性好；UUID 由节点授权时注入。',
    fields: [{ key: 'alter_id', label: 'alterId', type: 'number', hint: '通常填 0；老客户端可能要求 64 以内' }],
    tls: { sni: true, alpn: true },
    transport: true,
  },
  trojan: {
    hint: 'Trojan：伪装成正常 HTTPS；用户密码由节点授权时注入。',
    tls: { sni: true, alpn: true },
    transport: true,
  },
  hysteria2: {
    hint: 'Hysteria2：基于 QUIC（UDP），弱网表现好；需在防火墙放行 UDP 端口，可设置上下行限速。',
    fields: [
      { key: 'up_mbps', label: '上行 Mbps', type: 'number', hint: '服务端限速，0 / 留空表示不限' },
      { key: 'down_mbps', label: '下行 Mbps', type: 'number', hint: '服务端限速，0 / 留空表示不限' },
    ],
    tls: { sni: true, alpn: true },
  },
  anytls: {
    hint: 'AnyTLS：TLS 伪装 + 多路复用；密码由节点授权时注入（取 UUID）。',
    tls: { sni: true, alpn: true },
  },
  tuic: {
    hint: 'TUIC v5：基于 QUIC；UUID 与密码由节点授权时注入。',
    tls: { sni: true, alpn: true },
  },
  shadowtls: {
    hint: 'ShadowTLS：只做 TLS 握手伪装，通常作为其它协议的入口。',
    fields: [{ key: 'version', label: '版本', type: 'number', hint: '对应 ShadowTLS 协议版本，常用 3' }],
    tls: { sni: true },
  },
};

function Hint({ children }) {
  if (!children) return null;
  return (
    <div className="pp-muted" style={{ fontSize: 11.5, lineHeight: 1.6, marginTop: 2 }}>
      {children}
    </div>
  );
}

function randomB64(n = 32) {
  const bytes = new Uint8Array(n);
  try {
    crypto.getRandomValues(bytes);
  } catch (e) {
    for (let i = 0; i < n; i++) bytes[i] = Math.floor(Math.random() * 256);
  }
  let s = '';
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  try {
    return btoa(s);
  } catch (e) {
    return '';
  }
}

function InboundEditor({ raw, index, last, mutate }) {
  const type = raw.type || '';
  const ui = PROTO_UI[type] || {};
  const tls = raw.tls && raw.tls.enabled ? raw.tls : null;
  const reality = tls && tls.reality && tls.reality.enabled ? tls.reality : null;
  const transport = raw.transport || null;
  const isWs = type === 'vless' && !!transport && transport.type === 'ws';

  const setTop = (key, val) => mutate((ib) => {
    if (val === undefined || val === '') delete ib[key];
    else ib[key] = val;
  });
  const setNested = (parent, key, val) => mutate((ib) => {
    if (!ib[parent]) ib[parent] = {};
    if (val === undefined || val === '') delete ib[parent][key];
    else ib[parent][key] = val;
  });

  return (
    <div
      style={{
        padding: '10px 12px',
        borderBottom: last ? 'none' : '1px dashed #eef2f7',
        background: index % 2 ? '#fbfcfe' : '#fff',
      }}
    >
      {/* 协议说明 */}
      {ui.hint ? (
        <div className="pp-muted" style={{ fontSize: 12, lineHeight: 1.7, marginBottom: 6 }}>
          {ui.hint}
        </div>
      ) : null}

      {/* 通用：标签 / 端口 / 监听 */}
      <Row gutter={8} align="middle" wrap>
        <Col flex="none">
          <Tag color="geekblue" style={{ marginInlineEnd: 0 }}>{type || '?'}</Tag>
        </Col>
        <Col flex="none"><span className="pp-muted" style={{ fontSize: 12 }}>标签</span></Col>
        <Col flex="150px">
          <Input size="small" value={raw.tag || ''} placeholder="tag" onChange={(e) => setTop('tag', e.target.value)} />
        </Col>
        <Col flex="none"><span className="pp-muted" style={{ fontSize: 12 }}>端口</span></Col>
        <Col flex="92px">
          <InputNumber
            size="small"
            min={1}
            max={65535}
            value={Number(raw.listen_port) || undefined}
            onChange={(v) => setTop('listen_port', Number(v) || undefined)}
            style={{ width: '100%' }}
          />
        </Col>
        <Col flex="none"><span className="pp-muted" style={{ fontSize: 12 }}>监听</span></Col>
        <Col flex="110px">
          <Input size="small" value={raw.listen || ''} placeholder="::" onChange={(e) => setTop('listen', e.target.value)} />
        </Col>
      </Row>
      <Hint>标签只用于 sing-box 内部识别；端口是客户端连接端口，保存时会反推为模板端口基线；监听留空默认 ::（IPv4+IPv6）。</Hint>

      {/* 协议专属字段 */}
      {ui.fields && ui.fields.length ? (
        <Row gutter={8} align="top" style={{ marginTop: 8 }} wrap>
          {ui.fields.map((f) => (
            <Col key={f.key} flex="1 1 190px">
              <div className="pp-muted" style={{ fontSize: 11.5 }}>{f.label}</div>
              {f.type === 'select' ? (
                <Select
                  size="small"
                  value={raw[f.key] || undefined}
                  options={(f.options || []).map((o) => ({ value: o, label: o }))}
                  onChange={(v) => setTop(f.key, v)}
                  style={{ width: '100%', marginTop: 2 }}
                />
              ) : f.type === 'number' ? (
                <InputNumber
                  size="small"
                  value={Number(raw[f.key]) || undefined}
                  onChange={(v) => setTop(f.key, Number(v) || undefined)}
                  style={{ width: '100%', marginTop: 2 }}
                />
              ) : (
                <Input size="small" value={raw[f.key] || ''} onChange={(e) => setTop(f.key, e.target.value)} style={{ marginTop: 2 }} />
              )}
              <Hint>{f.hint}</Hint>
            </Col>
          ))}
        </Row>
      ) : null}

      {/* WS 的 SNI / ALPN / Host 由节点绑定信息在渲染与链接阶段自动填充 */}
      {isWs ? (
        <div className="pp-muted" style={{ fontSize: 12, lineHeight: 1.7, marginTop: 6 }}>
          服务端不限制 SNI（证书按证书路径提供）；ALPN 固定 h2,http/1.1（兼容 CDN）；
          客户端链接里的 SNI / Host 自动使用节点域名（CDN 链接用 CDN 域名），无需在模板里配置。
        </div>
      ) : null}

      {/* TLS / REALITY */}
      {tls && !isWs ? (
        <Row gutter={8} align="top" style={{ marginTop: 8 }} wrap>
          <Col flex="1 1 220px">
            <div className="pp-muted" style={{ fontSize: 11.5 }}>TLS SNI（server_name）</div>
            <Input
              size="small"
              value={tls.server_name || ''}
              placeholder="example.com"
              onChange={(e) => setNested('tls', 'server_name', e.target.value)}
              style={{ marginTop: 2 }}
            />
            <Hint>客户端看到的域名，必须能完成 TLS 握手；建议选与服务器 IP 归属地看起来合理的站点。</Hint>
          </Col>
          <Col flex="1 1 220px">
            <div className="pp-muted" style={{ fontSize: 11.5 }}>ALPN（逗号分隔）</div>
            <Input
              size="small"
              value={(tls.alpn || []).join(',')}
              placeholder="h2,http/1.1"
              onChange={(e) => mutate((ib) => {
                const arr = e.target.value.split(',').map((x) => x.trim()).filter(Boolean);
                if (!ib.tls) ib.tls = {};
                if (arr.length) ib.tls.alpn = arr;
                else delete ib.tls.alpn;
              })}
              style={{ marginTop: 2 }}
            />
            <Hint>留空用协议默认；hysteria2 常用 h3，VLESS WS 用 h2,http/1.1。</Hint>
          </Col>
          {reality ? (
            <>
              <Col flex="1 1 200px">
                <div className="pp-muted" style={{ fontSize: 11.5 }}>REALITY 握手目标</div>
                <Input
                  size="small"
                  value={(reality.handshake && reality.handshake.server) || ''}
                  placeholder="www.microsoft.com"
                  onChange={(e) => mutate((ib) => {
                    if (!ib.tls.reality.handshake) ib.tls.reality.handshake = {};
                    ib.tls.reality.handshake.server = e.target.value;
                  })}
                  style={{ marginTop: 2 }}
                />
                <Hint>真实 TLS 站点，客户端会与它握手完成伪装；一般用 www.microsoft.com 等大站。</Hint>
              </Col>
              <Col flex="1 1 120px">
                <div className="pp-muted" style={{ fontSize: 11.5 }}>握手端口</div>
                <InputNumber
                  size="small"
                  min={1}
                  max={65535}
                  value={Number(reality.handshake && reality.handshake.server_port) || 443}
                  onChange={(v) => mutate((ib) => {
                    if (!ib.tls.reality.handshake) ib.tls.reality.handshake = {};
                    ib.tls.reality.handshake.server_port = Number(v) || 443;
                  })}
                  style={{ width: '100%', marginTop: 2 }}
                />
              </Col>
              <Col flex="1 1 200px">
                <div className="pp-muted" style={{ fontSize: 11.5 }}>shortId（逗号分隔）</div>
                <Input
                  size="small"
                  value={(reality.short_id || []).join(',')}
                  placeholder="0123456789abcdef"
                  onChange={(e) => mutate((ib) => {
                    const arr = e.target.value.split(',').map((x) => x.trim()).filter(Boolean);
                    if (arr.length) ib.tls.reality.short_id = arr;
                    else delete ib.tls.reality.short_id;
                  })}
                  style={{ marginTop: 2 }}
                />
                <Hint>0–16 位十六进制；多个用逗号分隔，客户端匹配任意一个即可。</Hint>
              </Col>
            </>
          ) : null}
        </Row>
      ) : null}

      {/* 传输层 */}
      {ui.transport && transport ? (
        <Row gutter={8} align="top" style={{ marginTop: 8 }} wrap>
          <Col flex="none">
            <Tag style={{ marginInlineEnd: 0, marginTop: 4 }}>{transport.type || 'transport'}</Tag>
          </Col>
          {transport.type === 'ws' ? (
            <Col flex="1 1 320px">
              <div className="pp-muted" style={{ fontSize: 11.5 }}>WS 路径</div>
              <Input
                size="small"
                value={transport.path || '/'}
                onChange={(e) => setNested('transport', 'path', e.target.value)}
                style={{ marginTop: 2 }}
              />
              <Hint>需要与客户端填写的路径完全一致；建议用带随机串的路径，降低被探测概率。Host 头由节点域名 / CDN 域名自动生成。</Hint>
            </Col>
          ) : null}
          {transport.type === 'grpc' ? (
            <Col flex="1 1 240px">
              <div className="pp-muted" style={{ fontSize: 11.5 }}>gRPC serviceName</div>
              <Input
                size="small"
                value={transport.service_name || ''}
                onChange={(e) => setNested('transport', 'service_name', e.target.value)}
                style={{ marginTop: 2 }}
              />
              <Hint>需与客户端一致；留空时客户端按默认 service 处理。</Hint>
            </Col>
          ) : null}
          {transport.type === 'http' || transport.type === 'httpupgrade' ? (
            <Col flex="1 1 240px">
              <div className="pp-muted" style={{ fontSize: 11.5 }}>路径</div>
              <Input
                size="small"
                value={transport.path || '/'}
                onChange={(e) => setNested('transport', 'path', e.target.value)}
                style={{ marginTop: 2 }}
              />
              <Hint>需与客户端一致；保存时不会反推该路径。</Hint>
            </Col>
          ) : null}
        </Row>
      ) : null}

    </div>
  );
}

export default function SingboxTemplateEditor({ state, onClose, onSaved }) {
  const { message } = AntdApp.useApp();
  const [name, setName] = useState('');
  const [remark, setRemark] = useState('');
  const [engSb, setEngSb] = useState(true);
  const [engMq, setEngMq] = useState(false);
  const [sb, setSb] = useState('');
  const [mqPath, setMqPath] = useState('');
  const [sbVer, setSbVer] = useState('');
  const [sbVers, setSbVers] = useState([]);
  const [busy, setBusy] = useState('');
  const [loading, setLoading] = useState(false);

  const open = !!state;
  const isEdit = !!state && state.mode === 'edit';

  const parsed = useMemo(() => {
    if (!String(sb).trim()) return { inbounds: [], warp: 0, outbounds: 0, error: '' };
    try {
      return { ...parseSbText(sb), error: '' };
    } catch (e) {
      return { inbounds: [], warp: 0, outbounds: 0, error: e.message };
    }
  }, [sb]);

  const inboundsRaw = useMemo(() => {
    if (parsed.error) return [];
    try {
      const obj = JSON.parse(sb);
      return Array.isArray(obj.inbounds) ? obj.inbounds : [];
    } catch (e) {
      return [];
    }
  }, [sb, parsed.error]);

  /** 修改某个入站并立刻写回 sb.json（保持 2 空格缩进） */
  const mutateInbound = (idx, fn) => {
    let obj;
    try {
      obj = JSON.parse(sb);
    } catch (e) {
      message.error('当前 sb.json 不是合法 JSON，先修正后再编辑');
      return;
    }
    const ib = obj.inbounds && obj.inbounds[idx];
    if (!ib) return;
    fn(ib, obj);
    setSb(JSON.stringify(obj, null, 2));
  };

  const applyFiles = (files) => {
    const pick = (n) => (files || []).find((f) => f.name === n);
    const s = pick('sb.json');
    if (s) setSb(s.content);
  };

  /** 后端渲染一份配置（用来「生成当前配置」/「填入示例」） */
  const render = async (mode) => {
    setBusy(mode);
    try {
      let body;
      if (mode === 'current' && isEdit) {
        const res = await api(`singbox/templates/${state.row.id}`);
        body = { ...(res.data || {}), name: 'preview' };
      } else {
        body = {
          name: 'preview',
          // 模板不保存真实用户：这里只是占位，实际下发时按节点授权的 UUID 注入
          users_json: JSON.stringify([{ name: 'user01', uuid: randomUuid(), ss_password: randomB64(32) }]),
          port_base: 13700,
          enable_singbox: engSb ? 1 : 0,
          enable_masque: engMq ? 1 : 0,
          warp_enabled: 0,
          // 示例里带一个 CDN 域名，示例配置中会出现 vless-ws-cdn（127.0.0.1:13707，明文 WS）
          cdn_domain: 'cdn.example.com',
        };
      }
      const res = await api('singbox/templates/preview', { method: 'POST', body });
      const files = (res && res.files) || [];
      applyFiles(files);
      message.success(mode === 'current' ? '已生成当前配置' : '已填入示例配置');
    } catch (e) {
      message.error(e.message || '生成失败');
    } finally {
      setBusy('');
    }
  };

  useEffect(() => {
    let alive = true;
    api('singbox/releases?engine=singbox').then((r) => { if (alive) setSbVers((r && r.data) || []); }).catch(() => {});
    return () => { alive = false; };
  }, []);

  useEffect(() => {
    if (!state) return;
    setName(state.row ? state.row.name || '' : '');
    setRemark(state.row ? state.row.remark || '' : '');
    setMqPath('');
    setSb('');
    if (isEdit) {
      setLoading(true);
      api(`singbox/templates/${state.row.id}`)
        .then(async (res) => {
          const r = (res && res.data) || {};
          setName(r.name || '');
          setRemark(r.remark || '');
          setEngSb(Number(r.enable_singbox) !== 0);
          setEngMq(Number(r.enable_masque) === 1);
          setMqPath(r.masque_uri_path || '');
          setSbVer(r.singbox_version || '');
          const prev = await api('singbox/templates/preview', { method: 'POST', body: { ...r, name: 'preview' } });
          applyFiles((prev && prev.files) || []);
        })
        .catch((e) => message.error(e.message || '读取模板失败'))
        .finally(() => setLoading(false));
    } else {
      setEngSb(true);
      setEngMq(false);
      render('sample');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const save = async () => {
    if (!name.trim()) {
      message.warning('模板名称不能为空');
      return;
    }
    if (!engSb) {
      message.warning('请启用 sing-box');
      return;
    }
    if (!sb.trim()) {
      message.warning('sb.json 不能为空');
      return;
    }
    // V13.6：MASQUE 走 sb.json 的 masque-server endpoint，不再需要独立 masque.json
    setBusy('save');
    try {
      await api('singbox/templates/from-config', {
        method: 'POST',
        body: {
          id: isEdit ? state.row.id : 0,
          name: name.trim(),
          remark: remark.trim(),
          sb_json: sb,
          enable_singbox: 1,
          enable_masque: engMq ? 1 : 0,
          masque_uri_path: engMq ? mqPath.trim() : '',
          singbox_version: sbVer.trim(),
          warp_engines: 'singbox',
        },
      });
      message.success(isEdit ? '模板已更新' : '模板已创建');
      onSaved();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setBusy('');
    }
  };

  const versionOptions = (list) => (Array.isArray(list) ? list : []).map((r) => {
    const tag = String(r.tag || '');
    const v = tag.replace(/^v/i, '');
    return { value: v, label: v + (r.prerelease ? ' · 预览版' : ' · 正式版') };
  });

  const sectionTitle = (text, extra) => (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, margin: '16px 0 8px' }}>
      <span style={{ fontSize: 13, fontWeight: 600, color: '#334155' }}>{text}</span>
      <span className="pp-muted" style={{ fontSize: 12 }}>{extra}</span>
    </div>
  );

  return (
    <Modal
      open={open}
      title={isEdit ? `编辑协议模板 #${state.row.id}` : '新建协议模板'}
      onCancel={onClose}
      width={980}
      destroyOnHidden
      maskClosable={false}
      footer={
        <Space>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" loading={busy === 'save'} onClick={save}>
            保存
          </Button>
        </Space>
      }
    >
      <Spin spinning={loading}>
        <Row gutter={12} style={{ marginTop: 12 }}>
          <Col span={14}>
            <div className="pp-field-label">模板名称</div>
            <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} placeholder="例如：内置默认" />
          </Col>
          <Col span={10}>
            <div className="pp-field-label">备注</div>
            <Input value={remark} onChange={(e) => setRemark(e.target.value)} maxLength={200} placeholder="默认配置" />
          </Col>
        </Row>

        <div style={{ margin: '18px 0 8px', display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 13, color: '#475569' }}>
            服务端引擎<span className="pp-muted">（V15.6 起只用 sing-box；Xray 已下线）</span>
          </span>
          <Space size={18} style={{ marginLeft: 'auto' }}>
            <Checkbox checked={engSb} onChange={(e) => setEngSb(e.target.checked)}>
              启用 sing-box
            </Checkbox>
            <Checkbox checked={engMq} onChange={(e) => setEngMq(e.target.checked)}>
              启用 MASQUE（sing-box endpoint，端口 13708）
            </Checkbox>
          </Space>
        </div>

        {engSb ? (
          <>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, margin: '14px 0 8px' }}>
              <span style={{ fontSize: 13, color: '#475569' }}>
                sb.json<span className="pp-muted">（sing-box；保存时自动反推端口 / 协议开关 / WS 路径 / REALITY 参数 / 用户）</span>
              </span>
              <Space style={{ marginLeft: 'auto' }}>
                <Button size="small" loading={busy === 'current'} onClick={() => render('current')}>
                  生成当前配置
                </Button>
                <Button size="small" loading={busy === 'sample'} onClick={() => render('sample')}>
                  填入示例
                </Button>
              </Space>
            </div>
            <Input.TextArea
              value={sb}
              onChange={(e) => setSb(e.target.value)}
              rows={16}
              className="pp-mono"
              spellCheck={false}
              placeholder='{"log":{...},"inbounds":[...]}'
              style={{ fontSize: 12.5, lineHeight: 1.7 }}
            />
          </>
        ) : null}

        {engMq ? (
          <div style={{ marginTop: 12 }}>
            {sectionTitle('MASQUE（sing-box masque-server endpoint）', '（端口 13708；需要 sing-box >= 1.15.0）')}
            <Input
              value={mqPath}
              onChange={(e) => setMqPath(e.target.value)}
              placeholder="/.well-known/masque/ip/{target}/{ipproto}/"
              className="pp-mono"
            />
            <div className="pp-muted" style={{ fontSize: 12, marginTop: 6, lineHeight: 1.8 }}>
              保存后由后端把 <code>masque-server</code> 写进 sb.json 的 <code>endpoints</code>（TLS 复用模板证书、用户在下发时按授权 UUID 注入）；
              不再单独下发 masque.json，也不再起独立进程。默认模板不启用。
            </div>
          </div>
        ) : null}

        <Row gutter={12} style={{ marginTop: 10 }}>
          <Col span={12}>
            <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
              sing-box 版本（留空 = 使用「组件分发基址」里的版本，自动跟随存储桶；也可手填正式版 / 预览版 tag）
            </div>
            <AutoComplete
              value={sbVer}
              onChange={setSbVer}
              options={versionOptions(sbVers)}
              allowClear
              placeholder="如 1.14.4 / 1.15.0-alpha.13"
              style={{ width: '100%' }}
            />
          </Col>
        </Row>

        <div style={{ margin: '16px 0 8px', display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: '#334155' }}>解析结果</span>
          <span className="pp-muted" style={{ fontSize: 12 }}>
            （按上面的 sb.json / MASQUE 实时解析；编辑会写回对应配置，用户凭据由节点授权注入）
          </span>
          {parsed.warp ? <Tag color="cyan">WARP 端点 {parsed.warp} 个</Tag> : null}
          {parsed.outbounds ? <Tag>出站 {parsed.outbounds} 个</Tag> : null}
          {engMq ? <Tag color="geekblue">MASQUE 端点 1 个</Tag> : null}
        </div>

        {engSb ? (
          parsed.error ? (
            <Alert type="error" showIcon style={{ borderRadius: 10 }} message={`sb.json 解析失败：${parsed.error}`} />
          ) : parsed.inbounds.length === 0 ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={<span className="pp-muted">sb.json 没有 inbounds</span>}
              style={{ padding: '14px 0' }}
            />
          ) : (
            <div style={{ border: '1px solid #eef2f7', borderRadius: 10, overflow: 'hidden' }}>
              {parsed.inbounds.map((r, i) => (
                <InboundEditor
                  key={i}
                  raw={inboundsRaw[i] || {}}
                  index={i}
                  last={i === parsed.inbounds.length - 1}
                  mutate={(fn) => mutateInbound(i, fn)}
                />
              ))}
            </div>
          )
        ) : null}


        {engMq ? (
          <div
            style={{
              marginTop: engSb ? 12 : 0,
              border: '1px solid #eef2f7',
              borderRadius: 10,
              padding: '10px 12px',
              background: '#fbfcfe',
            }}
          >
            <Row gutter={8} align="middle" wrap>
              <Col flex="none"><Tag color="geekblue" style={{ marginInlineEnd: 0 }}>masque</Tag></Col>
              <Col flex="none"><span className="pp-muted" style={{ fontSize: 12 }}>masque-server</span></Col>
              <Col flex="none"><span className="pp-muted" style={{ fontSize: 12 }}>端口</span></Col>
              <Col flex="none"><Input size="small" value="13708" disabled style={{ width: 92 }} /></Col>
              <Col flex="none"><Tag>TLS 复用模板证书</Tag></Col>
              <Col flex="auto" style={{ textAlign: 'right', minWidth: 60 }}>
                <span className="pp-muted" style={{ fontSize: 12 }}>用户按授权 UUID 注入</span>
              </Col>
            </Row>
            <Row gutter={8} align="middle" style={{ marginTop: 8 }}>
              <Col flex="1 1 420px">
                <Input
                  size="small"
                  addonBefore="路径"
                  className="pp-mono"
                  value={mqPath}
                  placeholder="/.well-known/masque/ip/{target}/{ipproto}/"
                  onChange={(e) => setMqPath(e.target.value)}
                />
                <Hint>保存后由后端写入 sb.json 的 endpoints；需要 sing-box ≥ 1.15.0，且不会单独起进程。</Hint>
              </Col>
            </Row>
          </div>
        ) : null}

        <div style={{ marginTop: 14, fontSize: 12, color: '#94a3b8', lineHeight: 1.8 }}>
          保存后由后端解析 JSON 生成模板参数；端口基线、WARP 凭据等已不再在界面上暴露 ——
          需要时直接在配置里写，或由模板默认值处理。
        </div>
      </Spin>
    </Modal>
  );
}
