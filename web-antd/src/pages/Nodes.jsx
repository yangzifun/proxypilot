import React, { useMemo, useState } from 'react';
import {
  ApiOutlined,
  CloudDownloadOutlined,
  CloudUploadOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  FileAddOutlined,
  NodeIndexOutlined,
  PlusOutlined,
  ReadOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SyncOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Col,
  Collapse,
  Divider,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiPost } from '../api';
import TemplateEditor from './SingboxTemplateEditor';
import { useAsync, readForm } from '../hooks';
import { CopyText, EmptyBlock, ErrorBlock, IconAction, JsonView, PageCard, StatCard } from '../components';
import { watchBatch } from '../components/JobCenter';
import { fmtAgo, fmtNum, fmtTime, shortUuid } from '../format';
import { chartPalette } from '../theme';

const PROTO_LABEL = {
  ss: 'SS2022',
  anytls: 'AnyTLS',
  hy2: 'Hysteria2',
  reality: 'Reality',
  vlws: 'VLESS-WS',
  vmess: 'TUIC',
  masque: 'MASQUE',
};

/* 每个协议的端口 / 传输 / 用途说明：表格里 hover 标签可见，模板页顶部也有完整清单 */
const PROTO_DOCS = {
  ss: {
    label: 'SS2022', port: 13702, transport: 'TCP',
    desc: 'Shadowsocks 2022，使用 2022-blake3-aes-256-gcm，每用户密钥由服务器密钥与 UUID 派生。',
    tips: ['开销低、兼容性好，适合作为基础节点', '客户端需支持 Shadowsocks 2022；旧版客户端可能不兼容', 'TCP 13702，无需额外证书'],
  },
  anytls: {
    label: 'AnyTLS', port: 13703, transport: 'TLS',
    desc: '基于 TLS 的抗主动探测协议，复用站点证书，用 TLS 握手隐藏代理特征。',
    tips: ['适合作为 TLS 伪装入口', '需要可用的域名与证书；SNI 建议使用常见站点', '客户端需支持 AnyTLS（Mihomo / 新版 sing-box）'],
  },
  hy2: {
    label: 'Hysteria2', port: 13701, transport: 'QUIC / UDP',
    desc: '基于 QUIC 的 Hysteria2，自签 TLS + BBR 拥塞控制，弱网和高丢包环境表现突出。',
    tips: ['必须放行 UDP 13701', '对 UDP QoS / 运营商限速较敏感', '客户端需支持 Hysteria2，并允许自签证书'],
  },
  reality: {
    label: 'VLESS-REALITY', port: 13705, transport: 'TCP + REALITY',
    desc: 'VLESS + REALITY + xtls-rprx-vision，借用真实站点握手，无需自有证书。',
    tips: ['抗封锁能力强，适合直连入口', '需正确配置 serverName 与 REALITY public-key', '客户端需支持 REALITY / Vision'],
  },
  vlws: {
    label: 'VLESS-WS', port: 13706, transport: 'TLS + WebSocket',
    desc: 'VLESS over WebSocket + TLS，默认路径 /vlws，可套 CDN 或中转。',
    tips: ['适合被墙后换 IP、套 CDN 的场景', 'CDN 场景需保证 Host / SNI / 路径与源站一致', '客户端需支持 WebSocket + TLS'],
  },
  vmess: {
    label: 'TUIC v5', port: 13704, transport: 'QUIC / UDP',
    desc: 'TUIC v5，基于 QUIC，使用 BBR，关闭 0-RTT；字段名 vmess 为历史兼容。',
    tips: ['必须放行 UDP 13704', '对 UDP 质量敏感，弱网可能抖动', '客户端需支持 TUIC v5'],
  },
  masque: {
    label: 'MASQUE', port: 13708, transport: 'HTTP/3 · CONNECT-IP',
    desc: '由 sing-box masque-server endpoint 承载，基于 HTTP/3 CONNECT-IP。',
    tips: ['需要 sing-box ≥ 1.15.0', '默认模板不启用，需确认客户端支持 MASQUE', 'UDP 13708，注意防火墙放行'],
  },
};

const SWITCHES = [
  { name: 'enable_ss', label: 'SS2022' },
  { name: 'enable_anytls', label: 'AnyTLS' },
  { name: 'enable_hy2', label: 'Hysteria2' },
  { name: 'enable_reality', label: 'Reality' },
  { name: 'enable_vlws', label: 'VLESS-WS' },
  { name: 'enable_vmess', label: 'TUIC' },
  { name: 'enable_masque', label: 'MASQUE' },
  { name: 'enabled', label: '模板启用' },
];

/** 后端 singboxBool 的接受范围 */
const b = (v) => (v ? true : false);

export default function Nodes({ initialTab, templatesOnly }) {
  const { message, modal } = AntdApp.useApp();

  const templates = useAsync(() => api('singbox/templates'), []);
  const nodes = useAsync(() => api('singbox/nodes'), []);
  const uuids = useAsync(() => api('singbox/uuids'), []);
  const servers = useAsync(() => api('servers'), []);

  const tplRows = (templates.data && templates.data.data) || [];
  const nodeRows = (nodes.data && nodes.data.data) || [];
  const uuidRows = (uuids.data && uuids.data.data) || [];
  const serverRows = (servers.data && servers.data.data) || [];
  const [tplModal, setTplModal] = useState(null); // { mode, row }
  const [nodeModal, setNodeModal] = useState(null); // { mode, row }
  const [view, setView] = useState(null); // { title, value }
  const [protoDocOpen, setProtoDocOpen] = useState(false);
  const [busy, setBusy] = useState('');

  const reloadAll = () => {
    templates.reload().catch(() => {});
    nodes.reload().catch(() => {});
    uuids.reload().catch(() => {});
  };

  const granted = useMemo(() => {
    const set = new Set();
    nodeRows.forEach((n) => (n.uuid_list || []).forEach((u) => set.add(u)));
    return set.size;
  }, [nodeRows]);

  const runAll = (title, desc, path, okText) => {
    modal.confirm({
      title,
      content: desc,
      okText,
      onOk: async () => {
        setBusy(path);
        try {
          const res = await apiPost(path, { push: true });
          if (watchBatch(res)) { message.success('已创建后台任务 #' + res.job_id + '（' + (res.total || 0) + ' 项），进度见右上角「任务」'); reloadAll(); return; }
          const failed = (res.data || []).filter((x) => x.ok === false);
          if (failed.length) message.warning(`已下发 ${res.ok_count || 0}/${res.total || 0}，失败 ${failed.length} 个`);
          else message.success(`已下发 ${res.ok_count || 0}/${res.total || 0} 个节点`);
          reloadAll();
        } catch (e) {
          message.error(e.message || '下发失败');
        } finally {
          setBusy('');
        }
      },
    });
  };

  const reconcile = async () => {
    setBusy('monitor/uuid-check');
    try {
      const res = await apiPost('monitor/uuid-check', {});
      if (watchBatch(res)) { message.success('已创建后台巡检任务 #' + res.job_id + '，进度见右上角「任务」'); return; }
      message.success(
        `一致性巡检完成：检查 ${res.checked || res.total || 0} 项，修复 ${res.fixed || res.repaired || 0} 项`
      );
      reloadAll();
    } catch (e) {
      message.error(e.message || '巡检失败');
    } finally {
      setBusy('');
    }
  };

  const deleteTemplate = async (row) => {
    try {
      await api(`singbox/templates/${row.id}`, { method: 'DELETE' });
      message.success('模板已删除');
      templates.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const deleteNode = async (row) => {
    try {
      const res = await api(`singbox/nodes/${row.id}`, { method: 'DELETE' });
      message.success(`节点已删除${res.removed_node_configs ? '，同时清理 ' + res.removed_node_configs + ' 份下发配置' : ''}`);
      reloadAll();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const applyNode = async (row) => {
    setBusy('apply-' + row.id);
    try {
      const res = await apiPost(`singbox/nodes/${row.id}/apply`, { push: true });
      message.success(`「${row.name}」已下发${res.changed ? '，变更 ' + res.changed + ' 处' : ''}`);
      nodes.reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '下发失败');
    } finally {
      setBusy('');
    }
  };

  /* ---------------- 模板表 ---------------- */
  const tplColumns = [
    { title: 'ID', dataIndex: 'id', width: 62 },
    {
      title: '模板名称',
      dataIndex: 'name',
      width: 190,
      render: (v, row) => (
        <div>
          <b>{v}</b>
          <div className="pp-muted" style={{ fontSize: 12 }}>
            {row.remark || `#${row.id}`}
          </div>
        </div>
      ),
    },
    {
      title: '协议',
      width: 290,
      render: (_, row) => {
        const list = Array.isArray(row.protocols) ? row.protocols : [];
        const on = SWITCHES.filter((s) => s.name !== 'enabled' && Number(row[s.name.replace('enable_', 'enable_')]) === 1);
        const names = list.length ? list : on.map((s) => s.name.replace('enable_', ''));
        return (
          <Space size={4} wrap>
            {names.length ? (
              names.map((p) => {
                const doc = PROTO_DOCS[p];
                return (
                  <Tooltip key={p} title={doc ? `${doc.label} · 端口 ${doc.port} · ${doc.transport}\n${doc.desc}` : p}>
                    <Tag color="purple" style={{ marginInlineEnd: 0 }}>
                      {PROTO_LABEL[p] || p}
                    </Tag>
                  </Tooltip>
                );
              })
            ) : (
              <span className="pp-muted">—</span>
            )}
          </Space>
        );
      },
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 80,
      render: (v) => (Number(v) === 1 ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>),
    },
    { title: '更新时间', dataIndex: 'updated_at', width: 112, render: (v) => <span className="pp-muted">{fmtAgo(v)}</span> },
    {
      title: '操作',
      width: 118,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0}>
          <IconAction title="编辑" icon={<EditOutlined />} onClick={() => setTplModal({ mode: 'edit', row })} />
          <IconAction
            title="参数"
            icon={<EyeOutlined />}
            onClick={async () => {
              try {
                const res = await api(`singbox/templates/${row.id}`);
                setView({ title: `模板 #${row.id} · ${row.name}（原始参数）`, value: res.data });
              } catch (e) {
                message.error(e.message || '读取失败');
              }
            }}
          />
          <Popconfirm
            title="删除该模板？"
            description="若仍有节点绑定会被后端拒绝。"
            okText="删除"
            okButtonProps={{ danger: true }}
            onConfirm={() => deleteTemplate(row)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  /* ---------------- 节点表 ---------------- */
  const nodeColumns = [
    { title: 'ID', dataIndex: 'id', width: 62 },
    {
      title: '节点',
      dataIndex: 'name',
      width: 180,
      render: (v, row) => (
        <div>
          <b>{v}</b>
          <div className="pp-muted" style={{ fontSize: 12 }}>
            {row.remark || row.domain || `#${row.id}`}
          </div>
        </div>
      ),
    },
    {
      title: '服务器',
      width: 150,
      render: (_, row) => (
        <div>
          <div style={{ color: '#334155' }}>{row.server_name || '—'}</div>
          <div className="pp-mono pp-muted" style={{ fontSize: 12 }}>{row.server_host || ''}</div>
        </div>
      ),
    },
    {
      title: '模板',
      width: 150,
      render: (_, row) => (row.template_name ? <Tag color="purple">{row.template_name}</Tag> : <span className="pp-muted">内置默认</span>),
    },
    {
      title: '域名',
      dataIndex: 'domain',
      width: 170,
      ellipsis: true,
      render: (v, row) => (
        <div>
          {v ? <span className="pp-mono">{v}</span> : <span className="pp-muted">—</span>}
          {row.cdn_domain ? (
            <div className="pp-muted" style={{ fontSize: 11.5, marginTop: 2 }}>
              CDN {row.cdn_domain}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      title: '授权 UUID',
      dataIndex: 'uuid_list',
      width: 130,
      render: (v) => {
        const list = v || [];
        return (
          <Tooltip title={list.length ? list.map(shortUuid).join('\n') : '未授权任何 UUID（将下发模板默认用户）'}>
            <Tag color={list.length ? 'geekblue' : 'default'} style={{ marginInlineEnd: 0 }}>
              {list.length} 个
            </Tag>
          </Tooltip>
        );
      },
    },
    {
      title: '上次下发',
      dataIndex: 'last_apply_at',
      width: 118,
      render: (v) => (
        <span className="pp-muted" title={fmtTime(v)}>
          {v ? fmtAgo(v) : '从未'}
        </span>
      ),
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 78,
      render: (v) => (Number(v) === 1 ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>),
    },
    {
      title: '操作',
      width: 128,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0}>
          <Tooltip title="下发">
            <Button
              type="text"
              size="small"
              icon={<CloudUploadOutlined />}
              loading={busy === 'apply-' + row.id}
              onClick={() => applyNode(row)}
              aria-label="下发"
            />
          </Tooltip>
          <IconAction title="编辑" icon={<EditOutlined />} onClick={() => setNodeModal({ mode: 'edit', row })} />
          <Popconfirm title="删除该节点绑定？" okText="删除" okButtonProps={{ danger: true }} onConfirm={() => deleteNode(row)}>
            <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div>
        <Tabs
          defaultActiveKey="tpl"
          tabBarStyle={templatesOnly ? { display: 'none' } : undefined}
          items={([
            {
              key: 'tpl',
              label: `协议模板（${tplRows.length}）`,
              children: (
                <PageCard
                  title={<span className="pp-section-title">协议模板（sing-box / MASQUE）</span>}
                  extra={
                    <Space>
                      <Button icon={<ReadOutlined />} onClick={() => setProtoDocOpen(true)}>
                        协议说明
                      </Button>
                      <Button icon={<ReloadOutlined />} onClick={() => templates.reload().catch(() => {})} loading={templates.loading}>
                        刷新
                      </Button>
                      <Button type="primary" icon={<FileAddOutlined />} onClick={() => setTplModal({ mode: 'create', row: null })}>
                        新建模板
                      </Button>
                    </Space>
                  }
                  bodyStyle={{ padding: 0 }}
                >
                  {templates.error ? (
                    <div style={{ padding: 18 }}>
                      <ErrorBlock error={templates.error} onRetry={() => templates.reload().catch(() => {})} />
                    </div>
                  ) : null}
                  <Table
                    className="pp-table"
                    rowKey="id"
                    size="medium"
                    loading={templates.loading}
                    dataSource={tplRows}
                    columns={tplColumns}
                    scroll={{ x: 1180 }}
                    pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 个模板` }}
                  />
                </PageCard>
              ),
            },
            {
              key: 'node',
              label: `节点绑定（${nodeRows.length}）`,
              children: (
                <PageCard
                  title={<span className="pp-section-title">节点绑定</span>}
                  extra={
                    <Space wrap>
                      <Button icon={<ReloadOutlined />} onClick={() => nodes.reload().catch(() => {})} loading={nodes.loading}>
                        刷新
                      </Button>
                      <Tooltip title="检查「授权 UUID → 节点配置 → 订阅」是否一致并自动修复">
                        <Button icon={<SyncOutlined />} loading={busy === 'monitor/uuid-check'} onClick={reconcile}>
                          UUID 一致性巡检
                        </Button>
                      </Tooltip>
                      <Button
                        icon={<CloudDownloadOutlined />}
                        loading={busy === 'singbox/apply-all'}
                        onClick={() => runAll('一键下发全部节点？', '按最新模板重新渲染所有已绑定节点并推送，客户端会短暂重连。', 'singbox/apply-all', '立即下发')}
                      >
                        一键下发
                      </Button>
                      <Button type="primary" icon={<PlusOutlined />} onClick={() => setNodeModal({ mode: 'create', row: null })}>
                        绑定节点
                      </Button>
                    </Space>
                  }
                  bodyStyle={{ padding: 0 }}
                >
                  {nodes.error ? (
                    <div style={{ padding: 18 }}>
                      <ErrorBlock error={nodes.error} onRetry={() => nodes.reload().catch(() => {})} />
                    </div>
                  ) : null}
                  <Table
                    className="pp-table"
                    rowKey="id"
                    size="medium"
                    loading={nodes.loading}
                    dataSource={nodeRows}
                    columns={nodeColumns}
                    scroll={{ x: 1320 }}
                    pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 个节点` }}
                    locale={{ emptyText: <EmptyBlock text="还没有绑定节点：先在「服务器管理」接入服务器，再点「绑定节点」" /> }}
                  />
                </PageCard>
              ),
            },
          ]).filter((it) => !templatesOnly || it.key === 'tpl')}
        />
      </div>

      <Modal
        open={protoDocOpen}
        title={<span><ReadOutlined /> 协议说明</span>}
        onCancel={() => setProtoDocOpen(false)}
        footer={<Button onClick={() => setProtoDocOpen(false)}>关闭</Button>}
        width={980}
        centered
        destroyOnHidden
      >
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(430px, 1fr))', gap: 12 }}>
          {Object.entries(PROTO_DOCS).map(([key, d]) => (
            <div key={key} style={{ border: '1px solid #e8edf5', borderRadius: 12, padding: '14px 16px', background: '#fbfcff' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <b style={{ fontSize: 14 }}>{d.label}</b>
                <Tag color="purple" style={{ marginInlineEnd: 0 }}>{d.port}</Tag>
                <span className="pp-muted" style={{ fontSize: 12 }}>{d.transport}</span>
              </div>
              <div style={{ fontSize: 13, lineHeight: 1.8, color: '#475569', marginTop: 6 }}>{d.desc}</div>
              <ul style={{ margin: '8px 0 0', paddingLeft: 18, fontSize: 12.5, lineHeight: 1.8, color: '#64748b' }}>
                {d.tips.map((tip) => <li key={tip}>{tip}</li>)}
              </ul>
            </div>
          ))}
        </div>
      </Modal>

      <TemplateEditor
        state={tplModal}
        onClose={() => setTplModal(null)}
        onSaved={() => {
          setTplModal(null);
          templates.reload().catch(() => {});
        }}
        onView={(title, value) => setView({ title, value })}
      />

      <NodeEditor
        state={nodeModal}
        servers={serverRows}
        templates={tplRows}
        onClose={() => setNodeModal(null)}
        onSaved={() => {
          setNodeModal(null);
          reloadAll();
        }}
      />

      <Modal
        open={!!view}
        title={view ? view.title : ''}
        onCancel={() => setView(null)}
        footer={<Button onClick={() => setView(null)}>关闭</Button>}
        width={820}
      >
        {view ? <JsonView value={view.value} maxHeight={540} /> : null}
      </Modal>
    </div>
  );
}

/* =================================================================
 *  模板编辑器：从配置导入 / 手动配置 / 编辑 / 预览
 * ================================================================= */
const IMP_DEFAULT = { name: '', sb_json: '', masque_json: '', enable_masque: false, port_base: 0 };

const TPL_DEFAULTS = {
  remark: '',
  port_base: 13700,
  warp_base: 13800,
  ss_method: '2022-blake3-aes-256-gcm',
  reality_dest: 'www.microsoft.com:443',
  reality_sni: 'www.microsoft.com',
  ws_sni: 'www.bing.com',
  disguise_url: 'https://www.bing.com',
  cert_path: '/etc/sing-box/cert.crt',
  key_path: '/etc/sing-box/private.key',
  enable_ss: true,
  enable_anytls: true,
  enable_hy2: true,
  enable_reality: true,
  enable_vlws: true,
  enable_vmess: true,
  enable_masque: false,
  enabled: true,
  warp_enabled: false,
};

/* =================================================================
 *  节点绑定编辑器
 * ================================================================= */
function NodeEditor({ state, servers, templates, onClose, onSaved }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const open = !!state;
  const isEdit = !!state && state.mode === 'edit';

  React.useEffect(() => {
    if (!state) return;
    form.resetFields();
    if (state.mode === 'edit') {
      const r = state.row;
      form.setFieldsValue({
        name: r.name,
        server_id: r.server_id,
        template_id: r.template_id || 0,
        domain: r.domain || '',
        cdn_domain: r.cdn_domain || '',
        sb_path: r.sb_path || '/etc/sing-box/sb.json',
        caddy_path: r.caddy_path || '/etc/sing-box/Caddyfile',
        service_sb: r.service_sb || 'sing-box',
        service_caddy: r.service_caddy || 'caddy-naive',
        enabled: Number(r.enabled) === 1,
        remark: r.remark || '',
        auto_deploy: false,
      });
    } else {
      form.setFieldsValue({
        template_id: templates.length ? templates[0].id : 0,
        enabled: true,
        auto_deploy: true,
        sb_path: '/etc/sing-box/sb.json',
        caddy_path: '/etc/sing-box/Caddyfile',
        service_sb: 'sing-box',
        service_caddy: 'caddy-naive',
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const save = async () => {
    const v = await readForm(form);
    if (!v) return;
    setSaving(true);
    try {
      const body = {
        name: v.name,
        server_id: v.server_id,
        template_id: v.template_id || 0,
        domain: v.domain || '',
        cdn_domain: v.cdn_domain || '',
        port_base: v.port_base || 0,
        warp_base: v.warp_base || 0,
        warp_enabled: Number(v.warp_enabled),
        sb_path: v.sb_path,
        caddy_path: v.caddy_path,
        service_sb: v.service_sb,
        service_caddy: v.service_caddy,
        enabled: !!v.enabled,
        remark: v.remark || '',
        auto_deploy: !!v.auto_deploy,
      };
      const res = isEdit
        ? await api(`singbox/nodes/${state.row.id}`, { method: 'PUT', body })
        : await api('singbox/nodes', { method: 'POST', body });
      const dep = res && res.deployed;
      message.success(
        (isEdit ? '节点已更新' : '节点已绑定') + (dep && dep.ok === false ? '，但下发失败：' + (dep.error || '') : '')
      );
      onSaved();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const authorized = (state && state.row && state.row.uuid_list) || [];

  return (
    <Modal
      open={open}
      title={isEdit ? `编辑节点绑定 #${state.row.id}` : '绑定新节点'}
      onCancel={onClose}
      onOk={save}
      confirmLoading={saving}
      okText={isEdit ? '保存' : '绑定并下发'}
      width={760}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 12 }}>
        <Row gutter={12}>
          <Col span={10}>
            <Form.Item name="name" label="节点名称" rules={[{ required: true, message: '请填写名称' }]}>
              <Input placeholder="hk-node" maxLength={64} />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item name="server_id" label="目标服务器" rules={[{ required: true, message: '请选择服务器' }]}>
              <Select
                placeholder="选择服务器"
                options={servers.map((s) => ({
                  value: s.id,
                  label: `${s.name}${s.last_status === 'online' ? ' · 在线' : ''}`,
                }))}
                showSearch
                optionFilterProp="label"
              />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item name="template_id" label="协议模板">
              <Select
                options={[
                  { value: 0, label: '内置默认模板' },
                  ...templates.map((t) => ({ value: t.id, label: t.name })),
                ]}
              />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="domain" label="域名" extra="用于节点链接与证书；可留空">
              <Input placeholder="hk.example.com" />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item
              name="cdn_domain"
              label="CDN 域名（可选）"
              extra="填了会部署 13707（127.0.0.1 明文 WS，供 Cloudflare Tunnel 回源），并给用户生成一条 CDN 链接（客户端连 CDN 域名:443）"
            >
              <Input placeholder="cdn.example.com" />
            </Form.Item>
          </Col>
        </Row>

        <Collapse
          ghost
          items={[
            {
              key: 'adv',
              label: <span style={{ fontSize: 13, color: '#64748b' }}>高级：落盘路径与服务名</span>,
              children: (
                <>
                  <Row gutter={12}>
                    <Col span={8}>
                      <Form.Item name="sb_path" label="sb.json 路径">
                        <Input />
                      </Form.Item>
                    </Col>
                    <Col span={8}>
                      <Form.Item name="caddy_path" label="Caddyfile 路径">
                        <Input />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Row gutter={12}>
                    <Col span={8}>
                      <Form.Item name="service_sb" label="sing-box 服务名">
                        <Input />
                      </Form.Item>
                    </Col>
                    <Col span={8}>
                      <Form.Item name="service_caddy" label="caddy 服务名">
                        <Input />
                      </Form.Item>
                    </Col>
                  </Row>
                </>
              ),
            },
          ]}
        />

        <Row gutter={12} style={{ marginTop: 4 }}>
          <Col span={8}>
            <Form.Item name="enabled" label="启用" valuePropName="checked">
              <Switch size="small" />
            </Form.Item>
          </Col>
          <Col span={16}>
            <Form.Item name="auto_deploy" label="保存后立即下发" valuePropName="checked">
              <Switch size="small" />
            </Form.Item>
          </Col>
        </Row>
        <Form.Item name="remark" label="备注">
          <Input placeholder="可为空" maxLength={200} />
        </Form.Item>
      </Form>
    </Modal>
  );
}

/* 用浏览器自带 randomUUID，失败时退化到 v4 手工生成 */
function safeRandomUuid() {
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
    else if (i === 19) out += hex[(Math.random() * 4 | 0) + 8];
    else out += hex[(Math.random() * 16) | 0];
  }
  return out;
}
