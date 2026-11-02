/* =================================================================
 *  移动端「订阅生成」：Segmented 选格式 → Popup + Picker 选模板 → 生成
 *  生成结果显示链接、复制 / 下载 / 二维码（本机渲染，不依赖外网服务）、一键导入客户端
 * ================================================================= */
import React, { useEffect, useMemo, useState } from 'react';
import { Button, Card, Picker, Popup, Segmented, Toast } from 'antd-mobile';
import { QRCode } from 'antd';
import { CheckCircleFilled, QrcodeOutlined } from '@ant-design/icons';
import { portalReq } from './portalApi';

const FORMATS = [
  { key: 'raw', label: '原始', hint: 'v2rayN / Shadowrocket 等通用客户端' },
  { key: 'clash', label: 'Clash', hint: 'Clash / Mihomo 专用，可套用订阅模板' },
  { key: 'batch', label: '批量', hint: '一次性导出全部节点' },
];

/* 模板名是中文，订阅链接是 UTF-8：btoa 只吃 Latin-1，先转义再编码 */
function toBase64(str) {
  try {
    if (typeof btoa !== 'function') return '';
    return btoa(unescape(encodeURIComponent(String(str))));
  } catch (e) {
    return '';
  }
}

function importApps(url, kind) {
  const enc = encodeURIComponent(url);
  const b64 = kind === 'clash' ? '' : toBase64(url);
  return kind === 'clash'
    ? [
        { name: 'Clash Verge', href: `clash://install-config?url=${enc}` },
        { name: 'ClashX', href: `clash://install-config?url=${enc}` },
        { name: 'Stash', href: `clash://install-config?url=${enc}` },
      ]
    : [
        { name: 'Shadowrocket', href: `shadowrocket://add/sub://${b64}` },
        { name: 'sing-box', href: `sing-box://import-remote-profile?url=${enc}` },
        { name: 'v2rayNG', href: `v2rayng://install-sub?url=${b64}` },
      ];
}

export default function MobileSubscribe({ uuid }) {
  const [fmt, setFmt] = useState('raw');
  const [templates, setTemplates] = useState([]);
  const [template, setTemplate] = useState('');
  const [pickerOpen, setPickerOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);
  const [qr, setQr] = useState(null);

  useEffect(() => {
    let alive = true;
    portalReq('/api/templates')
      .then(({ data }) => {
        if (!alive) return;
        const list = Array.isArray(data) ? data : Array.isArray(data && data.data) ? data.data : [];
        setTemplates(list);
        const def = list.find((t) => Number(t.is_default) === 1) || list[0];
        if (def) setTemplate(def.name);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  const url = useMemo(() => {
    const base = typeof window !== 'undefined' ? window.location.origin : '';
    if (fmt === 'clash') {
      return `${base}/clash-config?uuid=${encodeURIComponent(uuid)}${template ? `&template=${encodeURIComponent(template)}` : ''}`;
    }
    if (fmt === 'batch') return `${base}/batch-configs/${encodeURIComponent(uuid)}`;
    return `${base}/sub/${encodeURIComponent(uuid)}`;
  }, [fmt, template, uuid]);

  const generate = async () => {
    setLoading(true);
    setResult(null);
    try {
      const { text, headers } = await portalReq(url);
      const raw = String((headers && headers.get && headers.get('X-External-Nodes')) || '');
      // 合并进来的外部节点数：只认纯数字，避免把普通响应头当成计数
      const extNodes = /^\d+$/.test(raw) ? raw : '';
      setResult({
        url,
        size: String(text || '').length,
        text: String(text || ''),
        kind: fmt,
        extNodes,
      });
      Toast.show({ content: extNodes ? `订阅已生成（合并外部节点 ${extNodes} 条）` : '订阅已生成' });
    } catch (e) {
      Toast.show({ content: e.message || '生成失败' });
    } finally {
      setLoading(false);
    }
  };

  const download = () => {
    if (!result) return;
    try {
      const blob = new Blob([result.text], { type: 'text/plain;charset=utf-8' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = `subscription-${String(uuid).slice(0, 8)}.${result.kind === 'clash' ? 'yaml' : 'txt'}`;
      document.body.appendChild(a);
      a.click();
      setTimeout(() => {
        URL.revokeObjectURL(a.href);
        a.remove();
      }, 1000);
    } catch (e) {
      Toast.show({ content: '浏览器不支持下载，请复制链接' });
    }
  };

  const copy = async () => {
    if (!result) return;
    try {
      await navigator.clipboard.writeText(result.url);
      Toast.show({ content: '已复制' });
    } catch (e) {
      Toast.show({ content: '复制失败，请长按选择' });
    }
  };

  const templateColumns = [templates.map((t) => ({
    label: t.name + (Number(t.is_default) === 1 ? '（默认）' : ''),
    value: t.name,
  }))];

  return (
    <div className="msub">
      <Segmented
        block
        value={fmt}
        onChange={setFmt}
        options={FORMATS.map((f) => ({ label: f.label, value: f.key }))}
      />
      <div className="msub-hint">{FORMATS.find((f) => f.key === fmt).hint}</div>

      {fmt === 'clash' ? (
        <Card
          className="msub-pick"
          onClick={() => {
            if (!templates.length) {
              Toast.show({ content: '暂无可用订阅模板，先用默认格式生成即可' });
              return;
            }
            setPickerOpen(true);
          }}
        >
          <div className="msub-pick-row">
            <span>订阅模板</span>
            <b>{template || '未选择'}</b>
          </div>
        </Card>
      ) : null}

      <Button block color="primary" size="large" loading={loading} onClick={generate} className="msub-go">
        生成订阅
      </Button>
      <div className="msub-note">订阅链接含你的 UUID，等同于账号密码，请勿公开分享。</div>

      {result ? (
        <div className="msub-out">
          <div className="msub-out-head">
            <CheckCircleFilled />
            <span>
              已生成（{result.size} 字节{result.extNodes ? `，合并外部节点 ${result.extNodes} 条` : ''}）
            </span>
          </div>
          <div className="msub-out-url">{result.url}</div>
          <div className="msub-out-actions">
            <Button size="small" onClick={copy} className="msub-act is-primary">
              复制链接
            </Button>
            <Button size="small" onClick={download} className="msub-act">
              下载配置
            </Button>
            <Button size="small" onClick={() => setQr({ url: result.url })} className="msub-act">
              <QrcodeOutlined /> 二维码
            </Button>
          </div>
          <div className="msub-apps">
            <span className="msub-apps-label">一键导入</span>
            {importApps(result.url, result.kind).map((a) => (
              <a key={a.name} className="msub-app" href={a.href}>
                {a.name}
              </a>
            ))}
          </div>
        </div>
      ) : (
        <div className="msub-steps">
          <div className="msub-steps-title">三步拿到订阅</div>
          <ol>
            <li>
              <b>1</b>
              <span>选格式：通用客户端选「原始」，Clash / Mihomo 选「Clash」</span>
            </li>
            <li>
              <b>2</b>
              <span>点「生成订阅」，得到专属链接</span>
            </li>
            <li>
              <b>3</b>
              <span>复制到客户端，或生成后点 App 名称直接唤起</span>
            </li>
          </ol>
        </div>
      )}

      <Popup
        visible={pickerOpen}
        onMaskClick={() => setPickerOpen(false)}
        bodyStyle={{ borderTopLeftRadius: 16, borderTopRightRadius: 16 }}
      >
        <div className="msub-popup-title">选择订阅模板</div>
        <Picker
          columns={templateColumns}
          visible={pickerOpen}
          onClose={() => setPickerOpen(false)}
          value={template ? [template] : []}
          onConfirm={(v) => {
            setTemplate(v[0] || '');
            setPickerOpen(false);
          }}
        />
      </Popup>

      <Popup visible={!!qr} onMaskClick={() => setQr(null)} bodyStyle={{ padding: 20, borderRadius: 16 }}>
        <div className="msub-popup-title">扫码导入</div>
        {qr ? (
          <div className="msub-qr">
            <QRCode value={qr.url} size={220} type="svg" errorLevel="M" bordered={false} />
            <div className="msub-qr-url">{qr.url}</div>
          </div>
        ) : null}
      </Popup>
    </div>
  );
}
