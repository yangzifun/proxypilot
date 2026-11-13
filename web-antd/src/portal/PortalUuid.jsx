/* =================================================================
 *  用户中心 · 单个 UUID 的完整管理页
 *  配置管理 / 订阅生成 / 访问统计 / 外部订阅源 / 信息与换绑邮箱
 *  接口全部走用户令牌（role=user），后端无需改动
 * ================================================================= */
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert, App as AntdApp, Button, Col, Descriptions, Empty, Input, Modal, Popconfirm,
  QRCode, Row, Segmented, Select, Skeleton, Space, Table, Tabs, Tag, Tooltip,
} from 'antd';
import {
  ArrowLeftOutlined, CheckCircleOutlined, CloudDownloadOutlined, DeleteOutlined, EditOutlined, HistoryOutlined, LinkOutlined,
  MailOutlined, PlusOutlined, QrcodeOutlined, ReloadOutlined, SearchOutlined, SafetyOutlined, SwapOutlined, SyncOutlined,
} from '@ant-design/icons';
import { portalReq, portalApi } from './portalApi';
import { useTurnstile } from '../hooks';
import { useUuidConfigs, useUuidStats } from './hooks/usePortalData';
import { CopyText } from '../components';
import { AreaChart, ChartLegend, DonutChart } from '../charts';
import { chartPalette } from '../theme';
import Guide from './Guide';
import EmailChange from './EmailChange';
import MtprotoCard from './MtprotoCard';
import MasqueCard from './MasqueCard';
import PortalNav from './PortalNav';
import TrafficStrip from './TrafficStrip';
import useIsPc from './useIsPc';
import { fmtNum, fmtTime, shortUuid } from '../format';

const PROTOCOLS = [
  { value: 'all', label: '全部协议' },
  { value: 'vmess', label: 'VMess' },
  { value: 'vless', label: 'VLESS' },
  { value: 'trojan', label: 'Trojan' },
  // 后端 getProtocol() 把 ss:// 入库为 shadowsocks，这里的筛选值必须与库内一致
  { value: 'shadowsocks', label: 'Shadowsocks' },
  { value: 'hysteria2', label: 'Hysteria2' },
  { value: 'tuic', label: 'TUIC' },
  { value: 'anytls', label: 'AnyTLS' },
  { value: 'snell', label: 'Snell' },
];

const PROTO_COLOR = {
  vmess: 'geekblue', vless: 'purple', trojan: 'volcano', ss: 'cyan',
  hysteria2: 'magenta', tuic: 'green', snell: 'gold',
};

const shortData = (s, n = 64) => {
  const t = String(s || '');
  return t.length > n ? t.slice(0, n) + '…' : t;
};

/* 订阅链接可能是 UTF-8（模板名是中文），btoa 只吃 Latin-1：先转义再编码，失败就降级为空 */
function toBase64(str) {
  try {
    if (typeof btoa !== 'function') return '';
    return btoa(unescape(encodeURIComponent(String(str))));
  } catch (e) {
    return '';
  }
}

/* 各客户端的「一键导入」URL scheme（点开直接唤起 App 添加订阅） */
function importApps(url, kind) {
  const enc = encodeURIComponent(url);
  // 只有非 Clash 的两个 scheme 需要 base64，别为 Clash 也去编码（中文模板名会抛 InvalidCharacterError）
  const b64 = kind === 'clash' || kind === 'mihomo' ? '' : toBase64(url);
  const clashLike = [
    { name: 'Clash Verge', href: `clash://install-config?url=${enc}`, tip: '桌面 / 手机通用' },
    { name: 'ClashX', href: `clash://install-config?url=${enc}`, tip: 'macOS' },
    { name: 'Stash', href: `clash://install-config?url=${enc}`, tip: 'iOS' },
  ];
  const general = [
    { name: 'Shadowrocket', href: `shadowrocket://add/sub://${b64}`, tip: 'iOS（Base64 订阅）' },
    { name: 'sing-box', href: `sing-box://import-remote-profile?url=${enc}`, tip: 'SFA / SFI / SFM' },
    { name: 'v2rayNG', href: `v2rayng://install-sub?url=${b64}`, tip: 'Android' },
    { name: 'Clash Verge', href: `clash://install-config?url=${enc}`, tip: '桌面 / 手机通用' },
  ];
  return kind === 'clash' || kind === 'mihomo' ? clashLike : general;
}

export default function PortalUuid({ uuid, onBack, onUnauthorized, embedded = false, sections = null }) {
  const isPc = useIsPc();
  const { message } = AntdApp.useApp();
  const [tab, setTab] = useState('configs');
  const [rotateOpen, setRotateOpen] = useState(false); // 更换 UUID 弹窗

  /* ---------------- 配置列表（数据逻辑在 hooks 里） ---------------- */
  const configsHook = useUuidConfigs(uuid, onUnauthorized);
  const { filters, setFilters, page, setPage, pageSize, setPageSize } = configsHook;
  const cfg = {
    list: configsHook.list,
    total: configsHook.total,
    loading: configsHook.loading,
    error: configsHook.error,
  };
  const loadConfigs = configsHook.reload;

  /* ---------------- 订阅生成 ---------------- */
  const [sub, setSub] = useState({ template: '', loading: '', result: null });
  /* 订阅生成：一次只展示一种格式，避免三块几乎一样的内容并排堆叠 */
  const [fmt, setFmt] = useState('raw');
  const FORMATS = [
    { key: 'raw', title: '原始订阅', hint: '通用链接，v2rayN / Shadowrocket 等客户端直接导入', ext: '.txt' },
    { key: 'clash', title: 'Clash 订阅', hint: 'Clash / Mihomo 专用，可套用订阅模板', ext: '.yaml' },
    { key: 'batch', title: '批量配置', hint: '一次性导出全部节点，一行一个', ext: '.txt' },
  { key: 'mihomo', title: 'Mihomo 订阅', hint: 'mihomo 专用 YAML（按节点绑定生成）', ext: '.yaml' },
  ];
  const [templates, setTemplates] = useState([]);
  const [qr, setQr] = useState(null);
  /* 最近访问：表格收进弹窗，页面上只留一行摘要 */
  const [logOpen, setLogOpen] = useState(false);

  useEffect(() => {
    // /api/templates 返回的是 [{ name, url, description, is_default }] —— 模板用 name 标识，没有 id
    portalReq('/api/templates')
      .then(({ data }) => {
        const list = Array.isArray(data) ? data : Array.isArray(data && data.data) ? data.data : [];
        setTemplates(list);
        const def = list.find((t) => Number(t.is_default) === 1) || list[0];
        if (def) setSub((s) => ({ ...s, template: s.template || def.name }));
      })
      .catch(() => setTemplates([]));
  }, []);

  const baseUrl = typeof window !== 'undefined' ? window.location.origin : '';
  const subLinks = useMemo(() => {
    // 只有 Clash 订阅吃「订阅模板」；原始订阅 / 批量配置由后端固定格式输出
    return {
      raw: `${baseUrl}/sub/${encodeURIComponent(uuid)}`,
      clash: `${baseUrl}/clash-config?uuid=${encodeURIComponent(uuid)}${sub.template ? `&template=${sub.template}` : ''}`,
      batch: `${baseUrl}/batch-configs/${encodeURIComponent(uuid)}`,
      mihomo: `${baseUrl}/sub/mihomo/${encodeURIComponent(uuid)}`,
    };
  }, [baseUrl, uuid, sub.template]);

  const download = (text, filename) => {
    try {
      const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      setTimeout(() => {
        URL.revokeObjectURL(a.href);
        a.remove();
      }, 1000);
    } catch (e) {
      message.error('浏览器不支持直接下载，请复制链接');
    }
  };

  /* 生成：只取回内容并展示结果，下载/复制/二维码都由结果区的统一按钮触发 */
  const generate = async (kind) => {
    setSub((s) => ({ ...s, loading: kind, result: null }));
    try {
      const { text, headers } = await portalReq(subLinks[kind]);
      // 只认纯数字：某些环境（或代理）对未知响应头会回显别的内容，别当节点数展示
      const extRaw = String((headers && headers.get && headers.get('X-External-Nodes')) || '');
      const extNodes = /^\d+$/.test(extRaw) ? extRaw : '';
      message.success(kind === 'clash' ? 'Clash 订阅已生成' : `通用订阅已生成${extNodes ? `（合并外部节点 ${extNodes} 条）` : ''}`);
      setSub((s) => ({
        ...s,
        loading: '',
        result: { kind, url: subLinks[kind], size: String(text || '').length, text: String(text || ''), extNodes },
      }));
    } catch (e) {
      setSub((s) => ({ ...s, loading: '' }));
      message.error(e.message || '生成失败');
    }
  };

  const downloadResult = () => {
    const r = sub.result;
    if (!r) return;
    const stamp = new Date().toISOString().slice(0, 10);
    download(r.text || '', `subscription-${shortUuid(uuid)}-${stamp}.${r.kind === 'clash' ? 'yaml' : 'txt'}`);
  };

  /* ---------------- 访问统计（数据逻辑在 hooks 里，移动端复用同一份） ---------------- */
  const statsHook = useUuidStats(uuid);
  const stats = { loading: statsHook.loading, data: statsHook.data, error: statsHook.error };
  const loadStats = statsHook.reload;

  /* ---------------- 外部订阅源 ---------------- */
  const [src, setSrc] = useState({ list: [], loading: true, error: null });
  const [newSrc, setNewSrc] = useState({ url: '', remark: '', saving: false });

  const loadSrc = useCallback(async () => {
    setSrc((s) => ({ ...s, loading: true, error: null }));
    try {
      const { data } = await portalReq(`/manage/uuid-sub-sources?uuid=${encodeURIComponent(uuid)}`);
      setSrc({ list: (data && data.data) || [], loading: false, error: null });
    } catch (e) {
      setSrc({ list: [], loading: false, error: e });
    }
  }, [uuid]);

  const addSrc = async () => {
    if (!/^https?:\/\//i.test(newSrc.url.trim())) {
      message.error('请填写 http/https 开头的外部订阅地址');
      return;
    }
    setNewSrc((s) => ({ ...s, saving: true }));
    try {
      await portalReq('/manage/uuid-sub-sources', {
        method: 'POST',
        body: { uuid, url: newSrc.url.trim(), remark: newSrc.remark.trim() },
      });
      setNewSrc({ url: '', remark: '', saving: false });
      message.success('已添加');
      loadSrc();
    } catch (e) {
      setNewSrc((s) => ({ ...s, saving: false }));
      message.error(e.message || '添加失败');
    }
  };

  const toggleSrc = async (row) => {
    try {
      await portalReq('/manage/uuid-sub-sources', {
        method: 'PUT',
        body: { id: row.id, enabled: row.enabled ? 0 : 1 },
      });
      message.success(row.enabled ? '已禁用' : '已启用');
      loadSrc();
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const delSrc = async (row) => {
    try {
      await portalReq('/manage/uuid-sub-sources', { method: 'DELETE', body: { id: row.id } });
      message.success('已删除');
      loadSrc();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  /* ---------------- UUID 信息 / 换绑邮箱 ---------------- */
  const [info, setInfo] = useState({ loading: true, data: null, error: null });
  const [editInfo, setEditInfo] = useState(null);
  const [mailOpen, setMailOpen] = useState(false);

  const loadInfo = useCallback(async () => {
    setInfo({ loading: true, data: null, error: null });
    try {
      const { data } = await portalReq(`/manage/uuid-info/${encodeURIComponent(uuid)}`);
      // 后端返回 { success, exists, data: {...} }，这里兼容平铺与嵌套两种形态
      const payload = (data && data.data) || data || {};
      setInfo({ loading: false, data: payload, error: null });
    } catch (e) {
      setInfo({ loading: false, data: null, error: e });
    }
  }, [uuid]);

  useEffect(() => {
    if (embedded || tab === 'stats') loadStats();
    if (embedded || tab === 'sources') loadSrc();
    if (embedded || tab === 'info') loadInfo();
  }, [tab, embedded, loadStats, loadSrc, loadInfo]);


  const saveInfo = async () => {
    if (!editInfo) return;
    setEditInfo((s) => ({ ...s, saving: true }));
    try {
      const d = info.data || {};
      await portalReq('/manage/uuid-info', {
        method: 'POST',
        body: {
          uuid,
          email: d.email || '',
          contact_info: editInfo.contact_info,
          remark: editInfo.remark,
        },
      });
      message.success('已保存');
      setEditInfo(null);
      loadInfo();
    } catch (e) {
      setEditInfo((s) => ({ ...s, saving: false }));
      message.error(e.message || '保存失败');
    }
  };

  /* ---------------- 渲染 ---------------- */
  const columns = [
    {
      title: '协议',
      dataIndex: 'protocol',
      width: 104,
      render: (v) => <Tag color={PROTO_COLOR[String(v).toLowerCase()] || 'default'}>{String(v || '—').toUpperCase()}</Tag>,
    },
    {
      title: '备注',
      dataIndex: 'remark',
      width: 160,
      render: (v) => (v ? <span className="pp-cfg-remark" title={v}>{v}</span> : <span className="pp-muted">—</span>),
    },
    {
      title: '配置内容',
      dataIndex: 'config_data',
      // 不设宽度：占满剩余空间，内容完整换行展示（不再截断成 xxx…）
      render: (v) => <span className="pp-mono pp-config-full">{String(v || '') || '—'}</span>,
    },
  ];

  /* 「最近访问」弹窗里的列（页面上不再直接铺表格，太长） */
  const recentColumns = [
    { title: '时间', dataIndex: 'created_at', width: 180, render: (v) => fmtTime(v) },
    { title: '类型', dataIndex: 'query_type', width: 130, render: (v) => <Tag>{v || '—'}</Tag> },
    {
      title: 'IP',
      dataIndex: 'client_ip',
      width: 170,
      render: (v) => {
        const ip = String(v || '').trim();
        // 后端拿不到 cf-connecting-ip 时会写 'unknown'，别把英文原样抛给用户
        if (!ip || /^(unknown|undefined|null|-)$/i.test(ip)) {
          return (
            <Tooltip title="该次拉取没有经过 Cloudflare 边缘（或为历史数据），未记录 IP">
              <span className="pp-muted">—</span>
            </Tooltip>
          );
        }
        return <span className="pp-mono">{ip}</span>;
      },
    },
    {
      title: '客户端',
      dataIndex: 'user_agent',
      render: (v) => <span style={{ fontSize: 12, color: '#94a3b8' }}>{shortData(v, 72) || '—'}</span>,
    },
  ];

  /* 区块定义（函数形式：数组里引用了下面才计算的统计派生值，必须延迟到 render 时求值） */
  const renderTabs = () => [
              {
                key: 'configs',
                label: '配置管理',
                children: (
                  <div style={{ padding: 16 }}>
                    <Guide
                      title="查看这个 UUID 的节点（只读）"
                      desc="每条配置就是一条节点链接；客户端拉取订阅时，会按这里的顺序输出。节点由管理员统一维护，这里只做查看。"
                      steps={[
                        '用搜索框或协议筛选快速定位节点',
                        '复制「配置内容」可直接导入客户端',
                        '需要增删改节点，请联系管理员处理',
                      ]}
                      tip="节点变动后，客户端「更新订阅」即可拿到最新列表。"
                    />
                    <Space wrap style={{ marginBottom: 12 }}>
                      <Input
                        allowClear
                        prefix={<SearchOutlined style={{ color: '#94a3b8' }} />}
                        placeholder="搜索备注或配置内容"
                        value={filters.q}
                        onChange={(e) => setFilters((s) => ({ ...s, q: e.target.value }))}
                        onPressEnter={() => { setPage(1); loadConfigs(); }}
                        style={{ width: 240 }}
                      />
                      <Select
                        value={filters.protocol}
                        onChange={(v) => { setFilters((s) => ({ ...s, protocol: v })); setPage(1); }}
                        options={PROTOCOLS}
                        style={{ width: 140 }}
                      />
                      <Button icon={<ReloadOutlined />} onClick={() => { setPage(1); loadConfigs(); }}>
                        刷新
                      </Button>
                    </Space>

                    {cfg.error ? (
                      <Alert type="error" showIcon message={cfg.error.message || '加载失败'} style={{ marginBottom: 12 }} />
                    ) : null}

                    <Table
                      rowKey="id"
                      size="small"
                      className="pp-table pp-cfg-table"
                      tableLayout="fixed"
                      loading={cfg.loading}
                      dataSource={cfg.list}
                      columns={columns}
                      pagination={{
                        current: page,
                        pageSize,
                        total: cfg.total,
                        showSizeChanger: true,
                        pageSizeOptions: [10, 20, 50],
                        showTotal: (t) => `共 ${fmtNum(t)} 条`,
                        onChange: (p, ps) => { setPage(p); setPageSize(ps); },
                      }}
                      locale={{ emptyText: '这个 UUID 还没有节点，请联系管理员添加' }}
                    />
                  </div>
                ),
              },
              {
                key: 'sub',
                label: '订阅生成',
                children: (
                  <div style={{ padding: 16 }}>
                    <Guide
                      title="订阅怎么用：选格式 → 导入客户端"
                      desc="同一个 UUID 提供三种订阅格式，内容一致、面向不同客户端；生成后可以用链接、二维码或文件导入。"
                      steps={[
                        '按客户端挑一种格式（下方三张卡）',
                        '点「生成并下载」保存文件，或复制链接 / 扫码',
                        '在客户端里「从剪贴板导入」或「添加订阅」粘上链接',
                      ]}
                      tip="订阅链接含你的 UUID，等同于账号密码，请勿公开分享；节点变动后客户端「更新订阅」即可同步。"
                    >
                      <div>
                        <b>原始订阅</b>
                        Base64 编码的节点列表，通用性最好：v2rayN、Shadowrocket、Stash、Sing-box 都能直接吃，也是各客户端「添加订阅」时最稳的一种。
                      </div>
                      <div>
                        <b>Clash 订阅</b>
                        YAML 配置，给 Clash Verge / ClashX / Mihomo 用；支持策略组与规则，<b style={{ marginLeft: 0 }}>可以套用模板</b>改变分组和分流策略。
                      </div>
                      <div>
                        <b>批量配置</b>
                        全部节点明文导出（一行一个），适合一次性导入、脚本处理，或粘到自己的工具里做二次加工。
                      </div>
                      <div style={{ marginTop: 4, color: '#94a3b8' }}>
                        三种导入方式：<code>复制链接</code> 粘到客户端订阅栏 · <code>二维码</code> 手机扫码直接导入 · <code>生成并下载</code> 得到
                        <code>.txt</code> / <code>.yaml</code> 文件再导入。
                      </div>
                    </Guide>

                    <Space wrap style={{ marginBottom: 12 }}>
                      <Button danger icon={<SwapOutlined />} onClick={() => setRotateOpen(true)}>
                        更换 UUID
                      </Button>
                      <span className="pp-muted" style={{ fontSize: 12 }}>
                        旧订阅链接与旧配置会立即失效，客户端需要重新导入
                      </span>
                    </Space>

                    <div className="pp-fmt">
                      <Segmented
                        block
                        value={fmt}
                        onChange={setFmt}
                        options={FORMATS.map((f) => ({ value: f.key, label: f.title }))}
                      />
                      <div className="pp-fmt-hint">{FORMATS.find((f) => f.key === fmt).hint}</div>

                      {fmt === 'clash' ? (
                        <div className="pp-fmt-template">
                          <span>订阅模板</span>
                          <Select
                            size="small"
                            value={sub.template || undefined}
                            placeholder="选择模板"
                            onChange={(v) => setSub((s) => ({ ...s, template: v }))}
                            style={{ flex: '1 1 auto', minWidth: 0 }}
                            options={
                              templates.length
                                ? templates.map((t) => ({
                                    value: t.name,
                                    label: t.name + (Number(t.is_default) === 1 ? '（默认）' : ''),
                                  }))
                                : [{ value: '', label: '暂无可用模板', disabled: true }]
                            }
                          />
                        </div>
                      ) : null}

                      <div className="pp-fmt-actions">
                        <Button
                          type="primary"
                          icon={<CloudDownloadOutlined />}
                          loading={sub.loading === fmt}
                          onClick={() => generate(fmt)}
                        >
                          生成订阅
                        </Button>
                        <span className="pp-muted" style={{ fontSize: 12 }}>
                          生成后会在这里给出链接，可复制或扫码
                        </span>
                      </div>

                      {/* 统一的结果位：只有生成后才出现链接 / 复制 / 二维码 */}
                      {sub.result ? (
                        <div className="pp-fmt-out">
                          <div className="pp-fmt-out-head">
                            <CheckCircleOutlined />
                            <span>
                              {sub.result.kind === 'clash' ? 'Clash' : '通用'}订阅已生成（{fmtNum(sub.result.size)} 字节）
                            </span>
                          </div>
                          <code className="pp-mono" title={sub.result.url}>{sub.result.url}</code>
                          <div className="pp-fmt-out-actions">
                            <CopyText value={sub.result.url} display={<Button size="small">复制链接</Button>} />
                            <Button size="small" icon={<CloudDownloadOutlined />} onClick={downloadResult}>
                              下载配置
                            </Button>
                            <Button
                              size="small"
                              icon={<QrcodeOutlined />}
                              onClick={() => setQr({ url: sub.result.url, title: sub.result.kind === 'clash' ? 'Clash 订阅' : '通用订阅' })}
                            >
                              查看二维码
                            </Button>
                          </div>

                          {/* 一键唤起客户端导入（手机点开直接跳 App；桌面无对应客户端时点了没反应） */}
                          <div className="pp-fmt-apps">
                            <span className="pp-fmt-apps-label">导入到客户端</span>
                            <div className="pp-fmt-apps-list">
                              {importApps(sub.result.url, sub.result.kind).map((app) => (
                                <a key={app.name} className="pp-app-chip" href={app.href} title={app.tip || app.name}>
                                  {app.name}
                                </a>
                              ))}
                            </div>
                          </div>
                        </div>
                      ) : (
                        <div className="pp-fmt-empty">还没有生成，点上方「生成订阅」按钮</div>
                      )}
                    </div>
                  </div>
                ),
              },
              {
                key: 'tg',
                label: '代理服务',
                children: (
                  <div>
                    <MtprotoCard />
                    <MasqueCard />
                  </div>
                ),
              },
              {
                key: 'stats',
                label: '访问统计',
                children: (
                  <div style={{ padding: 16 }}>
                    <Guide
                      title="这个 UUID 的访问统计"
                      desc="客户端每次拉取订阅都会记一条，可以据此判断订阅是否在被使用、有没有人盗用。"
                      steps={[
                        '看「总访问 / 今日访问」判断活跃度',
                        '柱状图看近 30 天的拉取趋势',
                        '最近访问里的 IP 用于排查异常',
                      ]}
                      tip="统计只记录拉取时间、类型与 IP，不涉及节点内容。"
                    />
                    {stats.loading ? <Skeleton active paragraph={{ rows: 4 }} /> : null}
                    {stats.error ? <Alert type="error" showIcon message={stats.error.message || '统计加载失败'} /> : null}
                    {!stats.loading && !stats.error ? (
                      <>
                        <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
                          {[
                            { label: '总访问', value: statData.total_access },
                            { label: '今日访问', value: statData.today_total },
                            { label: '订阅拉取', value: statData.subscription_count },
                            { label: '接口生成', value: statData.apigen_count },
                          ].map((it) => (
                            <Col key={it.label} xs={12} md={6}>
                              <div className="pp-sub-link" style={{ flexDirection: 'column', alignItems: 'flex-start', gap: 2 }}>
                                <div className="pp-sub-link-hint">{it.label}</div>
                                <div style={{ fontSize: 22, fontWeight: 700, color: '#0f172a' }}>{fmtNum(it.value || 0)}</div>
                              </div>
                            </Col>
                          ))}
                        </Row>

                        <Row gutter={[14, 14]}>
                          <Col xs={24} lg={16}>
                            <div className="pp-chart-box">
                              <div className="pp-chart-title">近 30 天拉取趋势</div>
                              {dailyRows.length ? (
                                <>
                                  <AreaChart data={dailyRows} series={areaSeries} height={220} unit=" 次" />
                                  <ChartLegend items={areaSeries.map((s) => ({ name: s.name, color: s.color }))} />
                                </>
                              ) : (
                                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="近 30 天没有访问记录" />
                              )}
                            </div>
                          </Col>
                          <Col xs={24} lg={8}>
                            <div className="pp-chart-box">
                              <div className="pp-chart-title">访问构成</div>
                              <DonutChart
                                data={donutData}
                                size={168}
                                centerLabel="总访问"
                                centerValue={Number(statData.total_access || 0)}
                              />
                            </div>
                          </Col>
                        </Row>

                        <div className="pp-recent">
                          <div className="pp-recent-main">
                            <div className="pp-recent-title">最近访问</div>
                            <div className="pp-recent-sub">
                              {recent.length
                                ? `最近一条：${fmtTime(recent[0].created_at)} · 共 ${recent.length} 条`
                                : '暂无访问记录'}
                            </div>
                          </div>
                          <Button
                            size="small"
                            icon={<HistoryOutlined />}
                            disabled={!recent.length}
                            onClick={() => setLogOpen(true)}
                          >
                            查看最近访问
                          </Button>
                        </div>
                      </>
                    ) : null}
                  </div>
                ),
              },
              {
                key: 'sources',
                label: '外部订阅源',
                children: (
                  <div style={{ padding: 16 }}>
                    <Guide
                      title="把别处的订阅合并进来"
                      desc="添加其它订阅地址后，生成订阅时会把它们的节点一起合并输出，统一从一个链接拉取。"
                      steps={[
                        '粘贴 http/https 订阅地址（备注可留空）',
                        '启用后立即参与下次合并',
                        '不再需要就停用或删除，不影响已有节点',
                      ]}
                      tip="某个源拉取失败不会拖累整体，本站节点照常输出。"
                    />
                    <Space.Compact style={{ width: '100%', marginBottom: 12 }}>
                      <Input
                        placeholder="https://example.com/sub/xxx"
                        value={newSrc.url}
                        onChange={(e) => setNewSrc((s) => ({ ...s, url: e.target.value }))}
                      />
                      <Input
                        placeholder="备注（可选）"
                        value={newSrc.remark}
                        onChange={(e) => setNewSrc((s) => ({ ...s, remark: e.target.value }))}
                        style={{ width: 180 }}
                      />
                      <Button type="primary" icon={<PlusOutlined />} onClick={addSrc} loading={newSrc.saving}>
                        添加
                      </Button>
                    </Space.Compact>

                    {src.error ? <Alert type="error" showIcon message={src.error.message || '加载失败'} style={{ marginBottom: 12 }} /> : null}

                    <Table
                      rowKey="id"
                      size="small"
                      className="pp-table"
                      loading={src.loading}
                      dataSource={src.list}
                      pagination={false}
                      columns={[
                        { title: '地址', dataIndex: 'url', render: (v) => <span className="pp-mono" style={{ fontSize: 12, wordBreak: 'break-all' }}>{v}</span> },
                        { title: '备注', dataIndex: 'remark', width: 140, render: (v) => v || '—' },
                        {
                          title: '状态',
                          dataIndex: 'enabled',
                          width: 90,
                          render: (v) => (v ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>),
                        },
                        {
                          title: '操作',
                          width: 150,
                          render: (_, row) => (
                            <Space size={2}>
                              <Button type="link" size="small" icon={<SyncOutlined />} onClick={() => toggleSrc(row)}>
                                {row.enabled ? '禁用' : '启用'}
                              </Button>
                              <Popconfirm title="删除该订阅源？" okText="删除" cancelText="取消" onConfirm={() => delSrc(row)}>
                                <Button type="link" size="small" danger>删除</Button>
                              </Popconfirm>
                            </Space>
                          ),
                        },
                      ]}
                      locale={{ emptyText: '还没有外部订阅源' }}
                    />
                  </div>
                ),
              },
              {
                key: 'info',
                label: '信息与邮箱',
                children: (
                  <div style={{ padding: 16 }}>
                    <Guide
                      title="账号与绑定信息"
                      desc="这里展示这个 UUID 的绑定邮箱与联系方式；换绑邮箱需要接收一次验证码。"
                      steps={[
                        '点「编辑信息」改联系方式 / 备注',
                        '换绑邮箱：填新邮箱 → 收验证码 → 确认',
                        '换绑成功后，用新邮箱接收登录验证码',
                      ]}
                      tip="换绑会即时生效并自动换发登录令牌，当前页面无需重新登录。"
                    />
                    {info.loading ? <Skeleton active paragraph={{ rows: 3 }} /> : null}
                    {info.error ? <Alert type="error" showIcon message={info.error.message || '加载失败'} /> : null}
                    {!info.loading && !info.error ? (
                      <>
                        <Descriptions column={1} size="small" bordered style={{ marginBottom: 16 }}>
                          <Descriptions.Item label="UUID"><span className="pp-mono">{uuid}</span></Descriptions.Item>
                          <Descriptions.Item label="绑定邮箱">{info.data.email || '—'}</Descriptions.Item>
                          <Descriptions.Item label="联系方式">{info.data.contact_info || '—'}</Descriptions.Item>
                          <Descriptions.Item label="备注">{info.data.remark || '—'}</Descriptions.Item>
                        </Descriptions>
                        <Space wrap>
                          <Button
                            icon={<EditOutlined />}
                            onClick={() => setEditInfo({
                              contact_info: info.data.contact_info || '',
                              remark: info.data.remark || '',
                            })}
                          >
                            编辑信息
                          </Button>
                          <Button icon={<MailOutlined />} onClick={() => setMailOpen(true)}>
                            更换绑定邮箱
                          </Button>
                          <Button danger icon={<SwapOutlined />} onClick={() => setRotateOpen(true)}>
                            更换 UUID
                          </Button>
                        </Space>
                        <div style={{ marginTop: 10, fontSize: 12, color: '#94a3b8', lineHeight: 1.7 }}>
                          更换 UUID 会立即作废旧订阅链接与已下发的节点配置，客户端需要重新导入新链接。
                        </div>
                      </>
                    ) : null}
                  </div>
                ),
              },
            ];

  const statData = stats.data || {};
  const daily = Array.isArray(statData.daily_stats) ? statData.daily_stats : [];
  const recent = Array.isArray(statData.recent_logs) ? statData.recent_logs : [];

  const dailyRows = daily.slice(-30).map((d) => ({
    label: String(d.date || '').slice(5),
    fullLabel: String(d.date || ''),
    subscription: Number(d.subscription || 0),
    api: Number(d.api_generation || 0),
    total: Number(d.total || 0),
  }));
  const areaSeries = [
    { key: 'subscription', name: '订阅拉取', color: chartPalette[3] },
    { key: 'api', name: '接口生成', color: chartPalette[2] },
  ];
  const donutData = [
    { name: '订阅拉取', value: Number(statData.subscription_count || 0), color: chartPalette[3] },
    { name: '接口生成', value: Number(statData.apigen_count || 0), color: chartPalette[2] },
  ];

  const tabDefs = renderTabs();
  /* 指定 sections 时只渲染这几块（移动端把区块拆到不同页面用） */
  const onlySections =
    Array.isArray(sections) && sections.length ? tabDefs.filter((t) => sections.includes(t.key)) : null;
  /* PC 单页：顶部账号卡已合并「信息与邮箱」，这里不再重复渲染该区块 */
  const pcDefs = embedded ? tabDefs.filter((t) => t.key !== 'info') : tabDefs;
  /* PC 单页布局：上排「访问统计 | 订阅生成」，下排整行「配置管理 | 外部订阅源」 */
  const EMBED_TOP = ['stats', 'sub'];
  const EMBED_BOTTOM = ['configs', 'sources', 'tg'];
  const topTabs = pcDefs.filter((t) => EMBED_TOP.includes(t.key));
  const bottomTabs = pcDefs.filter((t) => EMBED_BOTTOM.includes(t.key));

  return (
    <div className={`${embedded ? 'pp-portal-embed' : 'pp-portal'}${!embedded && !isPc && !onlySections ? ' pp-has-nav' : ''}`}>
      <div className={embedded ? 'pp-portal-embed-inner' : 'pp-portal-wide'}>
        {/* PC 单页里上方订阅卡已经显示 UUID 与复制入口，这里不再重复 */}
        {embedded || onlySections ? null : (
          <header className="pp-portal-top">
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
              <div style={{ minWidth: 0 }}>
                <div className="pp-portal-brand-title">UUID {shortUuid(uuid)}</div>
                <div className="pp-portal-brand-sub pp-mono">{uuid}</div>
              </div>
            </div>
            <Space size={8} wrap style={{ justifyContent: 'flex-end' }}>
              <CopyText value={uuid} display={<Button size="small" icon={<LinkOutlined />}>复制 UUID</Button>} />
              <Button size="small" danger icon={<SwapOutlined />} onClick={() => setRotateOpen(true)}>
                更换 UUID
              </Button>
            </Space>
          </header>
        )}


        <TrafficStrip uuid={uuid} />

        <div className="pp-sub-card" style={{ padding: 0 }}>
          {onlySections ? (
            <div className="pp-embedded-big pp-embedded-plain">
              {onlySections.map((t) => (
                <section key={t.key} className="pp-embedded-sec">
                  <div className="pp-embedded-head">{t.label}</div>
                  {t.children}
                </section>
              ))}
            </div>
          ) : embedded ? (
            <div className="pp-embedded-big">
              {/* 上排：访问统计（左） + 订阅生成（右） */}
              <div className="pp-embedded-top">
                {topTabs.map((t) => (
                  <section key={t.key} className="pp-embedded-sec">
                    <div className="pp-embedded-head">{t.label}</div>
                    {t.children}
                  </section>
                ))}
              </div>
              {/* 下排：配置管理 + 外部订阅源，合并成底部整行 */}
              <div className="pp-embedded-bottom">
                {bottomTabs.map((t) => (
                  <section key={t.key} className="pp-embedded-sec">
                    <div className="pp-embedded-head">{t.label}</div>
                    {t.children}
                  </section>
                ))}
              </div>
            </div>
          ) : (
            <Tabs
              activeKey={tab}
              onChange={setTab}
              className="pp-portal-tabs"
              tabBarStyle={{ padding: '0 16px', marginBottom: 0 }}
              items={tabDefs}
            />
          )}
        </div>

        <div className="pp-portal-foot" style={{ marginTop: 14 }}>
          支持的协议：VMess / VLESS / Trojan / Shadowsocks / Hysteria2 / TUIC / Snell
        </div>
      </div>

      {/* 二维码弹窗 */}
      <Modal
        open={!!qr}
        title={qr ? qr.title : ''}
        onCancel={() => setQr(null)}
        footer={<Button onClick={() => setQr(null)}>关闭</Button>}
        width={420}
        centered
        destroyOnHidden
      >
        {qr ? (
          <div style={{ textAlign: 'center' }}>
            <QRCode
              value={qr.url}
              size={200}
              type="svg"
              errorLevel="M"
              bordered={false}
              style={{ padding: 18, background: '#fff', borderRadius: 12, display: 'inline-block' }}
            />
            <div style={{ marginTop: 12, fontSize: 12, color: '#94a3b8', wordBreak: 'break-all' }}>{qr.url}</div>
          </div>
        ) : null}
      </Modal>

      {/* 最近访问弹窗：页面上只留摘要，点按钮看完整列表 */}
      <Modal
        open={logOpen}
        title="最近访问"
        onCancel={() => setLogOpen(false)}
        footer={<Button onClick={() => setLogOpen(false)}>关闭</Button>}
        width={880}
        centered
        destroyOnHidden
      >
        <div className="pp-recent-tip">客户端每次拉取订阅或生成配置都会记一条；IP 可用于排查异常。</div>
        <Table
          rowKey={(r, i) => String(r.id || r.created_at || i)}
          size="small"
          className="pp-table"
          dataSource={recent}
          columns={recentColumns}
          scroll={{ x: 720 }}
          pagination={
            recent.length > 10
              ? { pageSize: 10, showSizeChanger: false, showTotal: (t) => `共 ${fmtNum(t)} 条` }
              : false
          }
          locale={{ emptyText: '暂无访问记录' }}
        />
      </Modal>

      {/* 编辑信息弹窗 */}
      <Modal
        open={!!editInfo}
        title="编辑用户信息"
        onCancel={() => setEditInfo(null)}
        onOk={saveInfo}
        okText="保存"
        cancelText="取消"
        confirmLoading={!!(editInfo && editInfo.saving)}
        centered
        destroyOnHidden
      >
        {editInfo ? (
          <Space orientation="vertical" style={{ width: '100%' }} size={10}>
            <Input addonBefore="联系方式" value={editInfo.contact_info} onChange={(e) => setEditInfo((s) => ({ ...s, contact_info: e.target.value }))} />
            <Input addonBefore="备注" value={editInfo.remark} onChange={(e) => setEditInfo((s) => ({ ...s, remark: e.target.value }))} />
          </Space>
        ) : null}
      </Modal>

      <EmailChange
        open={mailOpen}
        onClose={() => setMailOpen(false)}
        currentEmail={info.data ? info.data.email : ''}
        onDone={() => loadInfo()}
      />

      <UuidRotateModal open={rotateOpen} uuid={uuid} onClose={() => setRotateOpen(false)} />

      {/* 只有完整的「页签视图」自己带底部导航；分区嵌入模式由外层页面提供 */}
      {!embedded && !isPc && !onlySections ? (
        <PortalNav
          active="manage"
          onHome={() => {
            if (onBack) onBack();
          }}
          onManage={() => {
            if (window.scrollY) window.scrollTo(0, 0);
          }}
          onStats={() => {
            window.location.hash = '#/stats';
          }}
        />
      ) : null}

    </div>
  );
}

/* =================================================================
 *  更换 UUID 弹窗（用户端）
 *  · 二次认证：账号有密码 → 输入当前密码；没有密码 → 绑定邮箱 6 位验证码
 *  · 发送验证码前：站点配置了 Turnstile 时先过人机验证（复用 useTurnstile）
 *  · 成功后展示新 UUID 与三种新订阅链接；旧订阅链接与旧配置立即失效
 *  · 页面其它数据都按旧 UUID 加载，成功后需要刷新页面重新拉 /api/portal/uuids
 * ================================================================= */
const PORTAL_BOOT = (typeof window !== 'undefined' && window.__PP_BOOT__) || {};
const PORTAL_TURNSTILE_KEY = String(PORTAL_BOOT.turnstile || '');

/** 按后端规则生成三种订阅链接；响应里带 sub 时以响应为准。 */
export function uuidSubLinks(newUuid, sub) {
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  const u = encodeURIComponent(String(newUuid || ''));
  const s = sub || {};
  return [
    { key: 'raw', label: '原始订阅', url: s.raw || `${origin}/sub/${u}` },
    { key: 'batch', label: '批量配置', url: s.batch || `${origin}/batch-configs/${u}` },
    { key: 'clash', label: 'Clash 订阅', url: s.clash || `${origin}/clash-config?uuid=${u}` },
  ];
}

/** 换 UUID 成功后刷新页面数据：清掉移动端深链 hash，保留路径与查询串。 */
export function reloadPortalAfterUuidRotate() {
  try {
    const u = new URL(window.location.href);
    u.hash = '';
    window.location.replace(u.toString());
  } catch (e) {
    window.location.reload();
  }
}

export function UuidRotateModal({ open, uuid, onClose }) {
  const { message } = AntdApp.useApp();
  const turnstile = useTurnstile(PORTAL_TURNSTILE_KEY);
  const [pwd, setPwd] = useState({ loading: true, hasPassword: false, error: null });
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [countdown, setCountdown] = useState(0);
  const [sending, setSending] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [result, setResult] = useState(null);
  const [tsOpen, setTsOpen] = useState(false);

  /* 打开时先确认账号有没有设置密码，决定二次认证方式 */
  const loadPwdStatus = useCallback(() => {
    setPwd({ loading: true, hasPassword: false, error: null });
    portalApi('portal/password')
      .then((r) => setPwd({ loading: false, hasPassword: !!(r && r.has_password), error: null }))
      .catch((e) => setPwd({ loading: false, hasPassword: false, error: e }));
  }, []);

  useEffect(() => {
    if (!open) return;
    setPassword('');
    setCode('');
    setCountdown(0);
    setSending(false);
    setBusy(false);
    setErr('');
    setResult(null);
    setTsOpen(false);
    loadPwdStatus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (countdown <= 0) return undefined;
    const t = setTimeout(() => setCountdown((n) => n - 1), 1000);
    return () => clearTimeout(t);
  }, [countdown]);

  const sendCodeNow = async (tsToken) => {
    setSending(true);
    setErr('');
    try {
      const body = {};
      if (tsToken) body.cf_turnstile_response = tsToken;
      const res = await portalApi('portal/uuid/rotate/send-code', { method: 'POST', body });
      setCountdown(60);
      message.success(`验证码已发送至 ${(res && res.email) || '绑定邮箱'}，10 分钟内有效`);
    } catch (e) {
      setErr(e.message || '验证码发送失败');
      // 账号其实设置了密码（本地状态过期）：切回密码确认方式，避免用户卡在验证码流程
      if (e && String(e.message || '').includes('已设置密码')) loadPwdStatus();
      turnstile.reset();
    } finally {
      setSending(false);
    }
  };

  /* 配置了 Turnstile：先弹人机验证，通过后自动发验证码 */
  const sendCode = () => {
    if (PORTAL_TURNSTILE_KEY && !turnstile.getToken()) {
      setErr('');
      setTsOpen(true);
      return;
    }
    sendCodeNow(turnstile.getToken());
  };

  useEffect(() => {
    if (!tsOpen || !turnstile.token) return;
    setTsOpen(false);
    sendCodeNow(turnstile.getToken());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tsOpen, turnstile.token]);

  const submit = async () => {
    if (pwd.loading) {
      setErr('正在读取账号状态，请稍候再试');
      return;
    }
    if (pwd.hasPassword) {
      if (!password.trim()) {
        setErr('请输入当前登录密码');
        return;
      }
    } else if (!/^\d{6}$/.test(code.trim())) {
      setErr('请输入绑定邮箱收到的 6 位验证码');
      return;
    }
    setBusy(true);
    setErr('');
    try {
      const body = pwd.hasPassword ? { password } : { code: code.trim() };
      const res = await portalApi('portal/uuid/rotate', { method: 'POST', body });
      setPassword('');
      setCode('');
      setResult(res || { success: true });
      turnstile.reset();
      /* 重新拉一次订阅列表：确认后端已经切换到新 UUID（失败不阻塞结果展示） */
      portalApi('portal/uuids').catch(() => {});
      message.success((res && res.message) || 'UUID 已更换，旧链接已失效');
    } catch (e) {
      // 后端契约：本接口只有 portal token 失效才返回 401（密码/验证码错误是 400），
      // 因此 401 说明登录过期，交给 portalApi 清理登录态即可。
      setErr(e.message || '更换失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const done = !!(result && (result.success || result.new_uuid));
  const newUuid = done ? String((result && result.new_uuid) || '') : '';
  const newLinks = done ? uuidSubLinks(newUuid, result && result.sub) : [];

  return (
    <>
      <Modal
        open={open}
        title={done ? 'UUID 已更换' : '更换 UUID'}
        onCancel={() => {
          if (done) return;
          if (onClose) onClose();
        }}
        closable={!done}
        mask={{ closable: !done }}
        keyboard={!done}
        footer={
          done ? (
            <Space wrap>
              <Button type="primary" onClick={reloadPortalAfterUuidRotate}>
                我已保存新链接，刷新页面
              </Button>
            </Space>
          ) : undefined
        }
        onOk={done ? undefined : submit}
        okText="确认更换"
        cancelText="取消"
        okButtonProps={done ? undefined : { danger: true }}
        confirmLoading={busy}
        width={560}
        centered
        destroyOnHidden
      >
        {done ? (
          <div style={{ paddingTop: 4 }}>
            <Alert
              type="success"
              showIcon
              style={{ borderRadius: 10, marginBottom: 14 }}
              message="更换成功，旧 UUID 已作废"
              description="旧订阅链接与已下发的旧配置已全部失效，请在客户端里重新导入下面的新链接；旧链接不会再恢复。"
            />
            <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>新 UUID</div>
            <CopyText
              value={newUuid}
              display={<span className="pp-mono" style={{ fontSize: 13, wordBreak: 'break-all' }}>{newUuid}</span>}
              tip="点击复制新 UUID"
            />
            <div style={{ height: 16 }} />
            <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>新的订阅链接</div>
            <Space orientation="vertical" size={10} style={{ width: '100%' }}>
              {newLinks.map((it) => (
                <div key={it.key} className="pp-sub-link">
                  <div className="pp-sub-link-main">
                    <div className="pp-sub-link-label">{it.label}</div>
                    <div
                      className="pp-mono"
                      title={it.url}
                      style={{ fontSize: 11.5, color: '#64748b', marginTop: 3, wordBreak: 'break-all' }}
                    >
                      {it.url}
                    </div>
                  </div>
                  <div className="pp-sub-link-actions">
                    <CopyText value={it.url} display={<Button size="small" icon={<CloudDownloadOutlined />}>复制</Button>} />
                  </div>
                </div>
              ))}
            </Space>
            {result && result.warning ? (
              <Alert type="warning" showIcon style={{ marginTop: 14 }} message={String(result.warning)} />
            ) : null}
            <div style={{ marginTop: 14, fontSize: 12, color: '#94a3b8', lineHeight: 1.8 }}>
              页面数据仍按旧 UUID 加载：请先复制上面的新链接，再点下方按钮刷新页面；
              刷新后会重新拉取 /api/portal/uuids 并显示新 UUID。
            </div>
          </div>
        ) : (
          <div style={{ paddingTop: 4 }}>
            <Alert
              type="warning"
              showIcon
              style={{ borderRadius: 10, marginBottom: 14 }}
              message="旧订阅链接与旧配置将立即失效"
              description="更换后，原 UUID 的订阅链接、已下发的节点配置全部作废，所有客户端必须重新导入新链接；此操作不可撤销。"
            />
            <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>当前 UUID</div>
            <CopyText
              value={uuid || ''}
              display={<span className="pp-mono" style={{ fontSize: 13, wordBreak: 'break-all' }}>{uuid || '—'}</span>}
              tip="点击复制当前 UUID"
            />
            <div style={{ height: 16 }} />
            {pwd.loading ? <Skeleton active paragraph={{ rows: 2 }} title={false} /> : null}
            {!pwd.loading && pwd.error ? (
              <Alert
                type="error"
                showIcon
                message={`读取账号密码状态失败：${pwd.error.message || '网络异常'}`}
                action={<Button size="small" onClick={loadPwdStatus}>重试</Button>}
              />
            ) : null}
            {!pwd.loading && !pwd.error && pwd.hasPassword ? (
              <>
                <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>
                  二次认证：输入当前登录密码
                </div>
                <Input.Password
                  value={password}
                  onChange={(e) => { setPassword(e.target.value); setErr(''); }}
                  onPressEnter={submit}
                  placeholder="当前登录密码"
                  prefix={<SafetyOutlined style={{ color: '#94a3b8' }} />}
                  autoComplete="current-password"
                />
                <div style={{ marginTop: 6, fontSize: 12, color: '#94a3b8' }}>
                  密码只用于本次校验，不会保存或写入日志。
                </div>
              </>
            ) : null}
            {!pwd.loading && !pwd.error && !pwd.hasPassword ? (
              <>
                <div style={{ fontSize: 13, color: '#64748b', marginBottom: 6 }}>
                  二次认证：账号还没有设置密码，改为向绑定邮箱发送 6 位验证码
                </div>
                <Space.Compact style={{ width: '100%' }}>
                  <Input
                    value={code}
                    onChange={(e) => { setCode(e.target.value.replace(/\D/g, '').slice(0, 6)); setErr(''); }}
                    onPressEnter={submit}
                    placeholder="6 位验证码"
                    inputMode="numeric"
                    maxLength={6}
                  />
                  <Button onClick={sendCode} loading={sending} disabled={countdown > 0}>
                    {countdown > 0 ? `${countdown}s 后重发` : '发送验证码'}
                  </Button>
                </Space.Compact>
                <div style={{ marginTop: 6, fontSize: 12, color: '#94a3b8' }}>
                  验证码发送到绑定邮箱，10 分钟内有效，且只能使用一次。
                </div>
              </>
            ) : null}
            {err ? <Alert type="error" showIcon style={{ marginTop: 12 }} message={err} /> : null}
          </div>
        )}
      </Modal>

      {/* 人机验证：通过后自动发送验证码（与登录页同一套流程） */}
      <Modal
        open={tsOpen}
        onCancel={() => {
          setTsOpen(false);
          turnstile.reset();
        }}
        footer={null}
        title="人机验证"
        width={360}
        centered
        destroyOnHidden
      >
        <div style={{ display: 'flex', justifyContent: 'center', padding: '8px 0 4px' }}>
          <div className="pp-turnstile" ref={turnstile.slotRef} />
        </div>
        {turnstile.status === 'error' ? (
          <div style={{ fontSize: 12, color: '#f43f5e', textAlign: 'center', marginTop: 8 }}>
            验证组件加载失败，<a onClick={() => turnstile.reload()}>点此重试</a>
          </div>
        ) : null}
        <div style={{ fontSize: 12, color: '#94a3b8', textAlign: 'center', marginTop: 10 }}>
          完成验证后将自动发送验证码
        </div>
      </Modal>
    </>
  );
}
