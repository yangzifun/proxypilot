/* =================================================================
 *  服务器实时终端（WebSocket + Agent 常驻长连接隧道 + PTY）
 *  · 打开即连 wss://<本站>/api/shell/browser/<id>?token=<JWT>
 *  · 服务端保存会话；Agent 下一次 poll 拿到 session 后主动拨号，
 *    两端在 DO 里配对，输入输出按字节双向转发（真正的交互式 shell）
 *  · 没有 canvas 的环境（jsdom / 极简浏览器）退化成 <pre> 输出，不阻塞连接
 * ================================================================= */
import React, { useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Space, Tag, Tooltip } from 'antd';
import { ClearOutlined, CodeOutlined, ReloadOutlined } from '@ant-design/icons';
import '@xterm/xterm/css/xterm.css';
import { api, getToken } from '../api';

let xtermPromise = null;
/** xterm 独立分包，只在首次打开远程终端时加载。 */
function loadXterm() {
  if (typeof window !== 'undefined' && window.__PP_XTERM__) return Promise.resolve(window.__PP_XTERM__);
  if (!xtermPromise) {
    xtermPromise = new Promise((resolve, reject) => {
      const boot = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};
      const script = document.createElement('script');
      script.src = '/xterm-app.js' + (boot.assetVersion ? '?v=' + boot.assetVersion : '');
      script.onload = () => {
        if (window.__PP_XTERM__) resolve(window.__PP_XTERM__);
        else reject(new Error('xterm 初始化失败'));
      };
      script.onerror = () => reject(new Error('xterm 资源加载失败'));
      document.head.appendChild(script);
    });
  }
  return xtermPromise;
}

const STATUS_TEXT = {
  checking: '检查服务端支持…',
  unsupported: '服务端未开启实时终端',
  connecting: '正在连接…',
  waiting: '正在通过 Agent 长连接建立隧道…',
  ready: '已连接',
  closed: '连接已断开',
  error: '连接失败',
};

const TAG_COLOR = { online: 'success', offline: 'error', unknown: 'default' };
const SERVER_STATUS = { online: '在线', offline: '离线', unknown: '未知' };

/** 终端标题的状态：优先 status，其次 last_status，最后按心跳时间推断 */
function serverStatusOf(srv) {
  if (!srv) return 'unknown';
  const direct = srv.status || srv.last_status;
  if (direct === 'online' || direct === 'offline') return direct;
  const seen = Number(srv.agent_last_seen) || 0;
  if (seen > 0) {
    return Date.now() - seen < 180000 ? 'online' : 'offline';
  }
  return 'unknown';
}

const THEME = {
  background: '#0b1220',
  foreground: '#dbe4f0',
  cursor: '#34d399',
  cursorAccent: '#0b1220',
  selectionBackground: 'rgba(99, 102, 241, 0.35)',
  black: '#1e293b',
  red: '#f87171',
  green: '#34d399',
  yellow: '#fbbf24',
  blue: '#60a5fa',
  magenta: '#c084fc',
  cyan: '#22d3ee',
  white: '#e2e8f0',
  brightBlack: '#475569',
  brightRed: '#fca5a5',
  brightGreen: '#6ee7b7',
  brightYellow: '#fde68a',
  brightBlue: '#93c5fd',
  brightMagenta: '#d8b4fe',
  brightCyan: '#67e8f9',
  brightWhite: '#f8fafc',
};

function stripAnsi(text) {
  return String(text || '')
    .replace(/\u001b\[[0-9;?]*[A-Za-z]/g, '')
    .replace(/\u001b\][^\u0007]*\u0007/g, '')
    .replace(/\r/g, '');
}

/* 兼容没有 TextDecoder 的极简环境（jsdom 等） */
function decodeUtf8(bytes) {
  try {
    if (typeof TextDecoder !== 'undefined') return new TextDecoder().decode(bytes);
  } catch (e) { /* fallthrough */ }
  let pct = '';
  for (let i = 0; i < bytes.length; i += 1) pct += '%' + ('0' + bytes[i].toString(16)).slice(-2);
  try {
    return decodeURIComponent(pct);
  } catch (e) {
    return '';
  }
}

export default function RemoteShell({ server, open, onClose, local = false }) {
  const [status, setStatus] = useState('checking');
  const [message, setMessage] = useState('');
  const [nonce, setNonce] = useState(0);
  // 长连接推送后若 2 秒仍未接入，再提示排查（正常情况下不会出现）
  const [waitSlow, setWaitSlow] = useState(false);
  useEffect(() => {
    if (status !== 'waiting') {
      setWaitSlow(false);
      return undefined;
    }
    const id = setTimeout(() => setWaitSlow(true), 2000);
    return () => clearTimeout(id);
  }, [status]);
  const termRef = useRef(null);
  const fitRef = useRef(null);
  const wsRef = useRef(null);
  const preRef = useRef(null);
  const aliveRef = useRef(true);

  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
    };
  }, []);

  /* 收尾：关 socket、销毁终端 */
  const teardown = () => {
    try {
      if (wsRef.current) {
        wsRef.current.onclose = null;
        wsRef.current.close();
      }
    } catch (e) { /* ignore */ }
    wsRef.current = null;
    try {
      if (termRef.current) termRef.current.dispose();
    } catch (e) { /* ignore */ }
    termRef.current = null;
    fitRef.current = null;
  };

  useEffect(() => {
    if (!open) {
      teardown();
      return undefined;
    }
    setStatus('checking');
    setMessage('');

    // ---- 终端写入批处理 ----
    // top/ls -R 这类高频输出如果逐条 write，每帧会触发多次重排；合并到一帧只写一次
    let pendingChunks = [];
    let flushRaf = 0;
    const flushPending = () => {
      flushRaf = 0;
      if (!pendingChunks.length) return;
      const chunks = pendingChunks;
      pendingChunks = [];
      const term = termRef.current;
      if (term) {
        if (chunks.length === 1) {
          term.write(chunks[0]);
        } else {
          // 合并成单个 Uint8Array 一次性写入
          let len = 0;
          for (const c of chunks) len += c.byteLength !== undefined ? c.byteLength : c.length;
          const buf = new Uint8Array(len);
          let off = 0;
          for (const c of chunks) {
            const arr = c.byteLength !== undefined ? c : new TextEncoder().encode(c);
            buf.set(arr, off);
            off += arr.length;
          }
          term.write(buf);
        }
      } else if (preRef.current) {
        preRef.current.textContent += chunks.map((c) => (typeof c === 'string' ? c : decodeUtf8(c))).join('');
        preRef.current.scrollTop = preRef.current.scrollHeight;
      }
    };
    const queueWrite = (chunk) => {
      pendingChunks.push(chunk);
      // 数据量过大时立即冲刷，避免内存膨胀
      if (pendingChunks.length > 512) { flushPending(); return; }
      if (!flushRaf) flushRaf = requestAnimationFrame(flushPending);
    };
    const cancelQueue = () => {
      if (flushRaf) { cancelAnimationFrame(flushRaf); flushRaf = 0; }
      pendingChunks = [];
    };

    const appendPlain = (text) => {
      if (!preRef.current) return;
      preRef.current.textContent += stripAnsi(text);
      preRef.current.scrollTop = preRef.current.scrollHeight;
    };

    const sendResize = (cols, rows) => {
      const ws = wsRef.current;
      if (ws && ws.readyState === 1) {
        try {
          ws.send(JSON.stringify({ type: 'resize', cols, rows }));
        } catch (e) { /* ignore */ }
      }
    };

    const connect = async () => {
      const token = getToken();
      if (!token) {
        setStatus('error');
        setMessage('登录已过期，请刷新页面重新登录');
        return;
      }
      // WebSocket 握手失败时浏览器拿不到响应体，这里先查一次原因（本机不需要）
      if (!local) {
        try {
          const pre = await api(`shell/precheck/${server.id}`);
          if (pre && pre.ok === false) {
            setStatus('error');
            setMessage(pre.error || '该服务器当前不可打开终端');
            return;
          }
        } catch (e) { /* 预检查失败不阻塞，走原有流程 */ }
      }
      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
      const path = local ? '/api/shell/local' : `/api/shell/browser/${server.id}`;
      const url = `${proto}://${window.location.host}${path}?token=${encodeURIComponent(token)}`;
      setStatus('connecting');
      let ws;
      try {
        ws = new WebSocket(url);
      } catch (e) {
        setStatus('error');
        setMessage('无法创建 WebSocket 连接：' + (e.message || e));
        return;
      }
      ws.binaryType = 'arraybuffer';
      wsRef.current = ws;

      ws.onopen = () => {
        if (!aliveRef.current) return;
        setStatus('waiting');
      };
      ws.onmessage = (ev) => {
        if (!aliveRef.current) return;
        const data = ev.data;
        if (typeof data === 'string') {
          let msg = null;
          try {
            msg = JSON.parse(data);
          } catch (e) {
            msg = null;
          }
          if (msg && msg.type === 'ready') {
            setStatus('ready');
            setMessage('');
            const fit = fitRef.current;
            if (fit && termRef.current) {
              try {
                fit.fit();
              } catch (e) { /* ignore */ }
            }
            if (termRef.current) termRef.current.focus();
            const t = termRef.current;
            if (t) sendResize(t.cols, t.rows);
          } else if (msg && msg.type === 'waiting') {
            setStatus('waiting');
          } else if (msg && msg.type === 'pushed') {
            setStatus('waiting');
          } else if (msg && msg.type === 'error') {
            setStatus('error');
            setMessage(msg.message || '连接失败');
          } else if (msg && msg.type === 'exit') {
            setStatus('closed');
            setMessage('远端会话已结束');
          }
          return;
        }
        const bytes =
          data && typeof data !== 'string' && typeof data.byteLength === 'number'
            ? new Uint8Array(data)
            : null;
        if (!bytes) return;
        queueWrite(bytes);
      };
      ws.onerror = () => {
        if (!aliveRef.current) return;
        setStatus('error');
        setMessage('终端连接失败：可能是未开启「允许执行命令」、Agent 离线或反向代理未放行 WebSocket');
      };
      ws.onclose = (ev) => {
        if (!aliveRef.current) return;
        setStatus((s) => (s === 'error' ? s : 'closed'));
        if (ev && ev.code === 4002) setMessage('Agent 未在 60 秒内接入');
      };
    };

    // 并行预取 xterm 分片（约 72KB gzip），与 shell/status 同时进行
    const xtermReady = loadXterm().catch(() => null);

    /* 先确认服务端支持，再初始化终端 */
    api('shell/status')
      .then((r) => {
        if (!aliveRef.current) return;
        if (!r || !r.durable) {
          setStatus('unsupported');
          setMessage(
            '当前节点未启用远程命令或 Agent 版本过低，实时终端不可用。'
          );
          return;
        }
        /* 终端初始化（有 canvas 才用 xterm，否则退化） */
        let canvasOk = false;
        try {
          canvasOk = !!document.createElement('canvas').getContext('2d');
        } catch (e) {
          canvasOk = false;
        }
        if (!canvasOk || !preRef.current) {
          connect();
        } else {
          xtermReady
            .then((mod) => {
              if (!mod) throw new Error('xterm 资源加载失败');
              const { Terminal, FitAddon, WebglAddon } = mod;
              if (!aliveRef.current || !preRef.current) return;
              try {
                const term = new Terminal({
                  cursorBlink: true,
                  cursorStyle: 'bar',
                  fontSize: 13,
                  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
                  lineHeight: 1.2,
                  scrollback: 1500,
                  theme: THEME,
                  convertEol: true,
                  smoothScrollDuration: 0,
                  fastScrollModifier: 'alt',
                  allowTransparency: false,
                });
                const fit = new FitAddon();
                term.loadAddon(fit);
                term.open(preRef.current);
                // GPU 渲染：DOM 渲染器在大量输出时是主要卡顿来源
                try {
                  const webgl = new WebglAddon();
                  webgl.onContextLoss(() => {
                    try { webgl.dispose(); } catch (e) { /* ignore */ }
                  });
                  term.loadAddon(webgl);
                } catch (e) {
                  /* 无 WebGL 时保持 DOM 渲染器 */
                }
                fit.fit();
                term.onData((d) => {
                  const sock = wsRef.current;
                  if (sock && sock.readyState === 1) {
                    try {
                      sock.send(new TextEncoder().encode(d));
                    } catch (e) { /* ignore */ }
                  }
                });
                term.onResize(({ cols, rows }) => sendResize(cols, rows));
                termRef.current = term;
                fitRef.current = fit;
              } catch (e) {
                termRef.current = null;
                fitRef.current = null;
              }
            })
            .catch(() => { /* 加载失败时保留纯文本输出 */ })
            .finally(() => {
              if (aliveRef.current) connect();
            });
        }
      })
      .catch((e) => {
        if (!aliveRef.current) return;
        setStatus('error');
        setMessage((e && e.message) || '无法获取终端状态');
      });

    const onResize = () => {
      const fit = fitRef.current;
      if (!fit) return;
      try {
        fit.fit();
      } catch (e) { /* ignore */ }
    };
    window.addEventListener('resize', onResize);

    return () => {
      window.removeEventListener('resize', onResize);
      cancelQueue();
      teardown();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, server && server.id, nonce]);

  const reconnect = () => {
    setMessage('');
    setStatus('checking');
    setNonce((n) => n + 1);
  };

  const clearScreen = () => {
    flushPending();
    if (termRef.current) termRef.current.clear();
    if (preRef.current) preRef.current.textContent = '';
  };

  return (
    <Modal
      open={!!open}
      onCancel={onClose}
      destroyOnHidden
      width={900}
      className="pp-shell-modal"
      title={
        <span className="pp-shell-title">
          <CodeOutlined /> 远程终端 · {(server && server.name) || ''}
          {server ? (
            <Tag color={TAG_COLOR[serverStatusOf(server)] || 'default'} style={{ marginLeft: 10 }}>
              {SERVER_STATUS[serverStatusOf(server)] || '未知'}
            </Tag>
          ) : null}
          {server && server.agent_version ? (
            <Tag color="blue" style={{ marginLeft: 6 }}>
              Agent {server.agent_version}
            </Tag>
          ) : null}
        </span>
      }
      footer={
        <Space>
          <Tooltip title="清空屏幕（不影响远端会话）">
            <Button icon={<ClearOutlined />} onClick={clearScreen}>
              清屏
            </Button>
          </Tooltip>
          <Tooltip title="断开并重新建立会话">
            <Button icon={<ReloadOutlined />} onClick={reconnect}>
              重新连接
            </Button>
          </Tooltip>
          <Button type="primary" onClick={onClose}>
            关闭
          </Button>
        </Space>
      }
    >
      <div className="pp-shell-status">
        <span className={`pp-shell-dot is-${status}`} />
        <span className="pp-shell-status-text">{STATUS_TEXT[status] || status}</span>
        {status === 'ready' ? <span className="pp-shell-hint">输入即发送，支持 vim / top 等交互程序</span> : null}
      </div>

      {status === 'unsupported' ? (
        <Alert type="warning" showIcon style={{ marginBottom: 10 }} message="实时终端不可用" description={message} />
      ) : null}
      {status === 'error' && message ? (
        <Alert type="error" showIcon style={{ marginBottom: 10 }} message={message} />
      ) : null}
      {status === 'waiting' ? (
        <Alert
          type={waitSlow ? 'warning' : 'info'}
          showIcon
          style={{ marginBottom: 10 }}
          message={waitSlow ? 'Agent 尚未响应' : '正在建立终端隧道'}
          description={
            waitSlow
              ? '已通过长连接推送会话，但 Agent 未在 2 秒内接入。请检查该节点是否在线、Agent 版本是否 ≥ 1.9.0。'
              : '正在通过 Agent 常驻长连接下发会话，通常 1 秒内完成。'
          }
        />
      ) : null}

      <div className="pp-shell-body" onClick={() => termRef.current && termRef.current.focus()}>
        <div className="pp-shell-term" ref={preRef} />
      </div>

      <div className="pp-shell-foot">
        终端经 Agent 常驻长连接隧道转发（内网优先），关闭窗口即结束远端 shell。
      </div>
    </Modal>
  );
}
