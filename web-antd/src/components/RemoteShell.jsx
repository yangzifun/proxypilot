/* =================================================================
 *  服务器实时终端（WebSocket + Durable Object + Agent PTY）
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
  waiting: '等待 Agent 接入（下一次轮询，≤30 秒）…',
  ready: '已连接',
  closed: '连接已断开',
  error: '连接失败',
};

const TAG_COLOR = { online: 'success', offline: 'error', unknown: 'default' };
const SERVER_STATUS = { online: '在线', offline: '离线', unknown: '未知' };

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

export default function RemoteShell({ server, open, onClose }) {
  const [status, setStatus] = useState('checking');
  const [message, setMessage] = useState('');
  const [nonce, setNonce] = useState(0);
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

    const connect = () => {
      const token = getToken();
      if (!token) {
        setStatus('error');
        setMessage('登录已过期，请刷新页面重新登录');
        return;
      }
      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
      const url =
        `${proto}://${window.location.host}/api/shell/browser/${server.id}` +
        `?token=${encodeURIComponent(token)}`;
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
        if (termRef.current) {
          termRef.current.write(bytes);
        } else {
          appendPlain(decodeUtf8(bytes));
        }
      };
      ws.onerror = () => {
        if (!aliveRef.current) return;
        setStatus('error');
        setMessage('WebSocket 连接失败：请检查服务端是否配置了 Durable Object 绑定 SHELL');
      };
      ws.onclose = (ev) => {
        if (!aliveRef.current) return;
        setStatus((s) => (s === 'error' ? s : 'closed'));
        if (ev && ev.code === 4002) setMessage('Agent 未在 60 秒内接入');
      };
    };

    /* 先确认服务端支持，再初始化终端 */
    api('shell/status')
      .then((r) => {
        if (!aliveRef.current) return;
        if (!r || !r.durable) {
          setStatus('unsupported');
          setMessage(
            '服务端没有配置 Durable Object 绑定 SHELL，实时终端不可用。\n在 wrangler.toml 增加 [[durable_objects.bindings]] 与 migrations 后重新部署即可（见 README「实时终端」）。'
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
          loadXterm()
            .then(({ Terminal, FitAddon }) => {
              if (!aliveRef.current || !preRef.current) return;
              try {
                const term = new Terminal({
                  cursorBlink: true,
                  fontSize: 13,
                  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
                  lineHeight: 1.2,
                  scrollback: 3000,
                  theme: THEME,
                  convertEol: true,
                });
                const fit = new FitAddon();
                term.loadAddon(fit);
                term.open(preRef.current);
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
            <Tag color={TAG_COLOR[server.status] || 'default'} style={{ marginLeft: 10 }}>
              {SERVER_STATUS[server.status] || '未知'}
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
          type="info"
          showIcon
          style={{ marginBottom: 10 }}
          message="等待 Agent 接入"
          description="当前会话已登记，Agent 会在下一次轮询（≤30 秒）时拨号接入；若长时间无响应，请确认 Agent 版本 ≥ 1.3.0。"
        />
      ) : null}

      <div className="pp-shell-body" onClick={() => termRef.current && termRef.current.focus()}>
        <div className="pp-shell-term" ref={preRef} />
      </div>

      <div className="pp-shell-foot">
        会话由 Durable Object 中转，Agent 主动出站连接；关闭窗口即结束远端 shell。
      </div>
    </Modal>
  );
}
