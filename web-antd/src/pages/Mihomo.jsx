import React, { useEffect, useMemo, useState } from 'react';
import {
  CloudDownloadOutlined,
  CopyOutlined,
  DownloadOutlined,
  SafetyCertificateOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { ReadOutlined, FileAddOutlined } from '@ant-design/icons';
import {
  App as AntdApp,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Col,
  Row,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiDelete, apiGet, apiPost, apiPut, getToken } from '../api';
import { PageCard, StatCard } from '../components';
import { fmtAgo, fmtTime } from '../format';

const { TextArea } = Input;

/** 默认 mihomo 模板：proxies 下方保留一个空行，生成时会替换为实际节点 */
export const DEFAULT_MIHOMO_TEMPLATE = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: info
ipv6: false
unified-delay: true
tcp-concurrent: true
external-controller: 127.0.0.1:9090

dns:
  enable: true
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  nameserver:
    - https://223.5.5.5/dns-query
    - https://1.1.1.1/dns-query
  fallback:
    - https://8.8.8.8/dns-query

proxies:

proxy-groups:
  - name: Proxy
    type: select
    proxies:
      - Auto
      - DIRECT
  - name: Auto
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    tolerance: 50
    proxies:
      - {{PROXY_NAMES}}

rules:
  - GEOIP,LAN,DIRECT,no-resolve
  - GEOIP,CN,DIRECT
  - MATCH,Proxy
`;
const yamlScalar = (text, key) => {
  const re = new RegExp('^' + key + ':\\s*(.*)$', 'm');
  const m = String(text || '').match(re);
  return m ? m[1].trim().replace(/^["']|["']$/g, '') : '';
};
const yamlSet = (text, key, value) => {
  const src = String(text || '');
  const re = new RegExp('^' + key + ':\\s*.*$', 'm');
  const line = key + ': ' + value;
  if (re.test(src)) return src.replace(re, line);
  return line + '\n' + src;
};
const parseQuick = (text) => ({
  port: Number(yamlScalar(text, 'mixed-port')) || 7890,
  mode: yamlScalar(text, 'mode') || 'rule',
  log: yamlScalar(text, 'log-level') || 'info',
  ctrl: yamlScalar(text, 'external-controller') || '127.0.0.1:9090',
  lan: yamlScalar(text, 'allow-lan') === 'true',
  ipv6: yamlScalar(text, 'ipv6') === 'true',
});

const downloadText = (name, text) => {
  const blob = new Blob([text], { type: 'text/yaml;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name || 'mihomo.yaml';
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
};

const SERVER_PROTOCOLS = [
  { value: 'vless_ws', label: 'VLESS-WS' },
  { value: 'vless_reality', label: 'VLESS-REALITY' },
  { value: 'vmess', label: 'VMess' },
  { value: 'trojan', label: 'Trojan' },
  { value: 'anytls', label: 'AnyTLS' },
  { value: 'hy2', label: 'Hysteria2' },
  { value: 'tuic', label: 'TUIC' },
  { value: 'socks', label: 'SOCKS' },
  { value: 'http', label: 'HTTP' },
  { value: 'ss', label: 'Shadowsocks' },
];

const DEFAULT_SERVER_CFG = {
  protocols: ['vless_ws', 'vless_reality', 'vmess', 'trojan', 'anytls', 'hy2', 'tuic'],
  port_base: 13800,
  ws_path: '/vlws',
  ss_method: '2022-blake3-aes-256-gcm',
  cert_path: '/etc/mihomo/cert.crt',
  key_path: '/etc/mihomo/private.key',
  reality_dest: 'www.microsoft.com:443',
  reality_private_key: '',
  reality_short_id: '',
  reality_server_names: ['www.microsoft.com'],
  secret: '',
};

const PROTOCOLS = [
  { value: 'shadowsocks', label: 'SS2022' },
  { value: 'anytls', label: 'AnyTLS' },
  { value: 'hysteria2', label: 'Hysteria2' },
  { value: 'vless-reality', label: 'VLESS-REALITY' },
  { value: 'vless-ws', label: 'VLESS-WS' },
  { value: 'vless-ws-cdn', label: 'VLESS-WS-CDN' },
  { value: 'tuic', label: 'TUIC' },
];

const MIHOMO_TEMPLATE_EXAMPLE = JSON.stringify({
  listeners: [
    { name: 'hy2', type: 'hysteria2', port: 13801, listen: '::', up: '100 Mbps', down: '500 Mbps', certificate: '/etc/mihomo/cert.crt', 'private-key': '/etc/mihomo/private.key' },
    { name: 'ss', type: 'shadowsocks', port: 13802, listen: '::', cipher: '2022-blake3-aes-256-gcm', password: 'CHANGE_ME_BASE64_KEY=' },
    { name: 'anytls', type: 'anytls', port: 13803, listen: '::', certificate: '/etc/mihomo/cert.crt', 'private-key': '/etc/mihomo/private.key' },
    { name: 'tuic', type: 'tuic', port: 13804, listen: '::', 'congestion-controller': 'bbr', certificate: '/etc/mihomo/cert.crt', 'private-key': '/etc/mihomo/private.key' },
    { name: 'vless-reality', type: 'vless', port: 13805, listen: '::', 'reality-config': { dest: 'www.microsoft.com:443', 'private-key': 'CHANGE_ME', 'short-id': ['0123456789abcdef'], 'server-names': ['www.microsoft.com'] } },
    { name: 'vless-ws', type: 'vless', port: 13806, listen: '::', 'ws-path': '/vlws', certificate: '/etc/mihomo/cert.crt', 'private-key': '/etc/mihomo/private.key' },
    { name: 'vmess', type: 'vmess', port: 13807, listen: '::' },
    { name: 'trojan', type: 'trojan', port: 13808, listen: '::', certificate: '/etc/mihomo/cert.crt', 'private-key': '/etc/mihomo/private.key' },
  ],
}, null, 2);

export default function Mihomo({ templatesOnly }) {
  const { message } = AntdApp.useApp();
  const [tab, setTab] = useState('templates');
  const [loading, setLoading] = useState(false);
  const [templates, setTemplates] = useState([]);
  const [bindings, setBindings] = useState([]);
  const [servers, setServers] = useState([]);
  const [uuids, setUuids] = useState([]);

  const [tplOpen, setTplOpen] = useState(false);
  const [tplEditing, setTplEditing] = useState(null);
  const [tplForm] = Form.useForm();
  const [quick, setQuick] = useState(parseQuick(DEFAULT_MIHOMO_TEMPLATE));
  const applyQuick = (patch) => {
    const next = { ...quick, ...patch };
    setQuick(next);
    let c = tplForm.getFieldValue('content') || '';
    if ('port' in patch) c = yamlSet(c, 'mixed-port', next.port);
    if ('mode' in patch) c = yamlSet(c, 'mode', next.mode);
    if ('log' in patch) c = yamlSet(c, 'log-level', next.log);
    if ('ctrl' in patch) c = yamlSet(c, 'external-controller', next.ctrl || '127.0.0.1:9090');
    if ('lan' in patch) c = yamlSet(c, 'allow-lan', next.lan ? 'true' : 'false');
    if ('ipv6' in patch) c = yamlSet(c, 'ipv6', next.ipv6 ? 'true' : 'false');
    tplForm.setFieldsValue({ content: c });
  };

  const [bindOpen, setBindOpen] = useState(false);
  const [bindEditing, setBindEditing] = useState(null);
  const [bindForm] = Form.useForm();
  const bindTplId = Form.useWatch('template_id', bindForm);
  const bindTplMode = (templates.find((x) => x.id === bindTplId) || {}).mode || 'subscription';

  const [versions, setVersions] = useState({ open: false, template: null, list: [], loading: false, content: '' });
  const [selectedIds, setSelectedIds] = useState([]);
  const [checkState, setCheckState] = useState({ open: false, loading: false, data: null });
  const [helpOpen, setHelpOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [tplMode, setTplMode] = useState('subscription');
  const [serverCfg, setServerCfg] = useState(DEFAULT_SERVER_CFG);
  const [tplPreview, setTplPreview] = useState({ yaml: '', loading: false });
  const [tplJson, setTplJson] = useState('');
  const [tplParsed, setTplParsed] = useState(null);
  const [tplVersions, setTplVersions] = useState([]);
  const [preview, setPreview] = useState(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  const loadTemplates = async () => {
    const res = await apiGet('/api/mihomo/templates');
    setTemplates(res.data || []);
  };
  const loadBindings = async () => {
    const res = await apiGet('/api/mihomo/nodes');
    setBindings(res.data || []);
  };
  const loadAll = async () => {
    setLoading(true);
    try {
      await Promise.all([loadTemplates(), loadBindings()]);
    } catch (e) {
      message.error(e.message || '加载失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadAll();
    apiGet('/api/servers')
      .then((res) => setServers(res.data || []))
      .catch(() => setServers([]));
    apiGet('/api/singbox/uuids')
      .then((res) => setUuids(res.data || []))
      .catch(() => setUuids([]));
  }, []);

  const serverOptions = useMemo(
    () => servers.map((s) => ({ value: s.id, label: `${s.name || `#${s.id}`}${s.host ? ` · ${s.host}` : ''}` })),
    [servers]
  );
  const templateOptions = useMemo(
    () => templates.map((t) => ({ value: t.id, label: t.name })),
    [templates]
  );
  const uuidOptions = useMemo(
    () =>
      uuids.map((u) => ({
        value: u.uuid,
        label: `${u.remark || u.owner || (u.email || '').split('@')[0] || u.uuid.slice(0, 8)} · ${String(u.uuid).slice(0, 8)}…`,
      })),
    [uuids]
  );

  const loadVersions = async (row) => {
    setVersions({ open: true, template: row, list: [], loading: true, content: '' });
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}/versions`);
      setVersions((v) => ({ ...v, list: res.data || [], loading: false }));
    } catch (e) {
      message.error(e.message || '读取版本失败');
      setVersions((v) => ({ ...v, loading: false }));
    }
  };
  const viewVersion = async (vid) => {
    const tpl = versions.template;
    if (!tpl) return;
    try {
      const res = await apiGet(`/api/mihomo/templates/${tpl.id}/versions/${vid}`);
      setPreview({ title: `${tpl.name} · 版本 #${vid}`, yaml: res.data.content, templateOnly: true });
    } catch (e) {
      message.error(e.message || '读取版本失败');
    }
  };
  const restoreVersion = async (vid) => {
    const tpl = versions.template;
    if (!tpl) return;
    try {
      await apiPost(`/api/mihomo/templates/${tpl.id}/versions/${vid}/restore`, {});
      message.success('已回滚到该版本');
      loadTemplates();
      loadVersions(tpl);
    } catch (e) {
      message.error(e.message || '回滚失败');
    }
  };

  const openCreateTpl = () => {
    setTplJson(MIHOMO_TEMPLATE_EXAMPLE);
    setTplParsed(null);
    setTplVersions([]);
    setTplEditing(null);
    tplForm.setFieldsValue({ name: '', remark: '', content: DEFAULT_MIHOMO_TEMPLATE, enabled: true, protocols: [] });
    setQuick(parseQuick(DEFAULT_MIHOMO_TEMPLATE));
    setTplMode('subscription');
    setServerCfg(DEFAULT_SERVER_CFG);
    setTplOpen(true);
  };
  const openEditTpl = async (row) => {
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}`);
      const data = res.data || row;
      setTplEditing(data);
      tplForm.setFieldsValue({
        name: data.name,
        remark: data.remark,
        content: data.content,
        enabled: !!data.enabled,
      });
      let cfg = {};
      try {
        cfg = JSON.parse(data.server_json || data.server_cfg ? JSON.stringify(data.server_cfg || {}) : '{}');
      } catch (e) {
        cfg = {};
      }
      if (!cfg || !Object.keys(cfg).length) {
        try {
          cfg = JSON.parse(data.server_json || '{}');
        } catch (e) {
          cfg = {};
        }
      }
      const payload = cfg.listeners && cfg.listeners.length ? { listeners: cfg.listeners } : cfg;
      setTplJson(JSON.stringify(payload, null, 2));
      setTplParsed(null);
      setTplPreview({ yaml: '', loading: false });
      try {
        const vres = await apiGet(`/api/mihomo/templates/${row.id}/versions`);
        setTplVersions(vres.data || []);
      } catch (e) {
        setTplVersions([]);
      }
      setTplOpen(true);
    } catch (e) {
      message.error(e.message || '读取模板失败');
    }
  };
  const updateListener = (index, field, value) => {
    try {
      const parsed = JSON.parse(tplJson || '{}');
      const list = parsed.listeners || [];
      if (!list[index]) return;
      list[index][field] = value;
      parsed.listeners = list;
      setTplJson(JSON.stringify(parsed, null, 2));
      setTplParsed(list.map((l, i) => ({ key: i, name: l.name, type: l.type, port: l.port, listen: l.listen, note: '' })));
    } catch (e) {
      /* ignore */
    }
  };
  const saveTpl = async () => {
    const v = await tplForm.validateFields();
    let parsedJson = null;
    try {
      parsedJson = JSON.parse(tplJson || '{}');
    } catch (e) {
      message.error('模板 JSON 格式错误：' + e.message);
      return;
    }
    const body = { name: v.name, remark: v.remark, enabled: v.enabled, mode: 'server', server_json: JSON.stringify(parsedJson) };
    try {
      if (tplEditing) await apiPut(`/api/mihomo/templates/${tplEditing.id}`, body);
      else await apiPost('/api/mihomo/templates', body);
      message.success(tplEditing ? '模板已保存' : '模板已创建');
      setTplOpen(false);
      loadTemplates();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };
  const copyTemplate = async (row) => {
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}`);
      setTplEditing(null);
      tplForm.setFieldsValue({
        name: res.data.name + ' 副本',
        remark: res.data.remark || '',
        content: res.data.content,
        enabled: !!res.data.enabled,
        protocols: res.data.protocols || [],
      });
      setQuick(parseQuick(res.data.content));
      setTplMode(res.data.mode || 'subscription');
      try {
        setServerCfg({ ...DEFAULT_SERVER_CFG, ...(JSON.parse(res.data.server_json || '{}') || {}) });
      } catch (e) {
        setServerCfg(DEFAULT_SERVER_CFG);
      }
      setTplOpen(true);
    } catch (e) {
      message.error(e.message || '复制失败');
    }
  };
  const toggleTemplate = async (row, enabled) => {
    try {
      await apiPost(`/api/mihomo/templates/${row.id}/enabled`, { enabled });
      message.success(enabled ? '模板已启用' : '模板已停用');
      loadTemplates();
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };
  const previewServerCfg = async () => {
    setTplPreview((p) => ({ ...p, loading: true }));
    try {
      const res = await apiPost('/api/mihomo/templates/preview-server', {
        server_json: JSON.stringify(serverCfg || {}),
      });
      setTplPreview({ yaml: (res.data && res.data.yaml) || '', loading: false });
    } catch (e) {
      setTplPreview({ yaml: '', loading: false });
      message.error(e.message || '生成预览失败');
    }
  };
  const applyParseResult = (d) => {
    const f = d.fields || {};
    setQuick({
      port: Number(f['mixed-port']) || quick.port,
      mode: f.mode || quick.mode,
      log: f['log-level'] || quick.log,
      ctrl: f['external-controller'] || quick.ctrl,
      lan: !!f['allow-lan'],
      ipv6: !!f.ipv6,
    });
    if (d.mode === 'server' && d.server_cfg) {
      setTplMode('server');
      setServerCfg({ ...DEFAULT_SERVER_CFG, ...d.server_cfg });
    }
  };
  const parseContent = async () => {
    const content = tplForm.getFieldValue('content') || '';
    try {
      const res = await apiPost('/api/mihomo/templates/parse', { content });
      const d = res.data || {};
      applyParseResult(d);
      const msg = `节点 ${d.proxy_count || 0} · 分组 ${d.group_count || 0} · 规则 ${d.rule_count || 0}` + (d.listeners ? ` · listeners ${d.listeners}` : '');
      if ((d.warnings || []).length) message.warning(msg + '；' + d.warnings.join('；'));
      else message.success(msg);
    } catch (e) {
      message.error(e.message || '解析失败');
    }
  };
  const importYamlFile = (file) => {
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      const text = String(reader.result || '');
      tplForm.setFieldsValue({ content: text });
      setQuick(parseQuick(text));
      message.success('已导入 ' + file.name);
    };
    reader.readAsText(file, 'utf-8');
  };
  const removeTpl = async (row) => {
    try {
      await apiDelete(`/api/mihomo/templates/${row.id}`);
      message.success('已删除');
      loadTemplates();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const bulkToggle = async (enabled) => {
    if (!selectedIds.length) return;
    try {
      await apiPost('/api/mihomo/nodes/bulk', { ids: selectedIds, enabled });
      message.success(enabled ? '已批量启用' : '已批量停用');
      setSelectedIds([]);
      loadBindings();
      loadStats();
    } catch (e) {
      message.error(e.message || '批量操作失败');
    }
  };
  const exportBindings = async (ids) => {
    if (!ids.length) return;
    try {
      const res = await fetch('/api/mihomo/export?ids=' + ids.join(','), {
        headers: { Authorization: 'Bearer ' + getToken() },
      });
      if (!res.ok) throw new Error('导出失败');
      const text = await res.text();
      downloadText('mihomo-bindings.yaml', text);
      message.success('已导出 ' + ids.length + ' 个绑定');
    } catch (e) {
      message.error(e.message || '导出失败');
    }
  };
  const autoFix = async () => {
    try {
      const res = await apiPost('/api/mihomo/check/fix', {});
      const fixed = (res.data && res.data.fixed) || 0;
      message.success(fixed ? `已修复 ${fixed} 个绑定` : '没有需要修复的项');
      runCheck();
      loadBindings();
    } catch (e) {
      message.error(e.message || '自动修复失败');
    }
  };
  const runCheck = async () => {
    setCheckState({ open: true, loading: true, data: null });
    try {
      const res = await apiGet('/api/mihomo/check');
      setCheckState({ open: true, loading: false, data: res.data });
    } catch (e) {
      setCheckState({ open: true, loading: false, data: { issues: [], issue_count: 0, checked: 0 } });
      message.error(e.message || '检查失败');
    }
  };
  const exportTemplate = async (row) => {
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}`);
      downloadText((res.data.name || 'mihomo') + '.yaml', res.data.content || '');
    } catch (e) {
      message.error(e.message || '导出失败');
    }
  };

  const renderServerConfig = async (row) => {
    try {
      const res = await apiPost(`/api/mihomo/nodes/${row.id}/render`, {});
      const d = res.data || {};
      const portLines = Object.entries(d.ports || {})
        .map(([k, v]) => `${k}=${v}`)
        .join('  ');
      setPreview({
        title: `服务端配置 · ${row.name}`,
        yaml: d.yaml,
        filename: d.filename,
        server: `端口：${portLines}`,
        template: `${d.config_path} · 服务 ${d.service}`,
      });
    } catch (e) {
      message.error(e.message || '生成失败');
    }
  };
  const applyServerBinding = async (row) => {
    try {
      const res = await apiPost(`/api/mihomo/nodes/${row.id}/apply`, {});
      message.success(res.message || '已排入下发任务');
      loadBindings();
    } catch (e) {
      message.error(e.message || '下发失败');
    }
  };
  const openCreateBind = (prefill) => {
    setBindEditing(null);
    bindForm.setFieldsValue({
      name: prefill?.name || '',
      server_id: undefined,
      template_id: prefill?.template_id,
      uuid_list: prefill?.uuid_list || [],
      protocols: prefill?.protocols || [],
      advanced: { group_type: 'url-test', test_url: 'http://www.gstatic.com/generate_204', extra_rules: '' },
      port_base: 0,
      domain: '',
      config_path: '/etc/mihomo/config.yaml',
      service: 'mihomo',
      enabled: true,
      remark: prefill?.remark || '',
    });
    setBindOpen(true);
  };
  const openCreateBindBlank = () => {
    setBindEditing(null);
    bindForm.setFieldsValue({
      name: '', server_id: undefined, template_id: undefined, uuid_list: [], protocols: [],
      advanced: { group_type: 'url-test', test_url: 'http://www.gstatic.com/generate_204', extra_rules: '' },
      port_base: 0, domain: '', config_path: '/etc/mihomo/config.yaml', service: 'mihomo',
      enabled: true, remark: '',
    });
    setBindOpen(true);
  };
  const openEditBind = (row) => {
    setBindEditing(row);
    bindForm.setFieldsValue({
      name: row.name,
      server_id: row.server_id,
      template_id: row.template_id,
      uuid_list: row.uuid_list || [],
      protocols: row.protocols || [],
      advanced: {
        group_type: (row.advanced && row.advanced.group_type) || 'url-test',
        test_url: (row.advanced && row.advanced.test_url) || 'http://www.gstatic.com/generate_204',
        extra_rules: (row.advanced && row.advanced.extra_rules) || '',
      },
      port_base: row.port_base || 0,
      domain: row.domain || '',
      config_path: row.config_path || '/etc/mihomo/config.yaml',
      service: row.service || 'mihomo',
      enabled: !!row.enabled,
      remark: row.remark || '',
    });
    setBindOpen(true);
  };
  const saveBind = async () => {
    const v = await bindForm.validateFields();
    const body = {
      ...v,
      uuid_list: v.uuid_list || [],
      protocols: v.protocols || [],
      advanced: v.advanced || {},
    };
    try {
      if (bindEditing) await apiPut(`/api/mihomo/nodes/${bindEditing.id}`, body);
      else await apiPost('/api/mihomo/nodes', body);
      message.success(bindEditing ? '绑定已保存' : '绑定已创建');
      setBindOpen(false);
      loadBindings();
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };
  const removeBind = async (row) => {
    try {
      await apiDelete(`/api/mihomo/nodes/${row.id}`);
      message.success('已删除');
      loadBindings();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const openPreview = async (row) => {
    setPreview({ title: row.name, loading: true });
    setPreviewLoading(true);
    try {
      const res = await apiGet(`/api/mihomo/nodes/${row.id}/preview`);
      setPreview({ title: row.name, ...res.data });
    } catch (e) {
      message.error(e.message || '生成配置失败');
      setPreview({ title: row.name, yaml: `# 生成失败：${e.message || '未知错误'}` });
    } finally {
      setPreviewLoading(false);
    }
  };

  const tplColumns = [
    { title: 'ID', dataIndex: 'id', width: 70 },
    {
      title: '模板名称',
      dataIndex: 'name',
      width: 220,
      render: (v, row) => (
        <div>
          <div>{v}</div>
          {row.remark ? <div className="pp-muted" style={{ fontSize: 12 }}>{row.remark}</div> : null}
        </div>
      ),
    },
    {
      title: '协议',
      width: 300,
      render: (_, row) => {
        const asArr = (v) => {
          if (Array.isArray(v)) return v;
          if (typeof v === 'string') {
            try {
              const parsed = JSON.parse(v);
              return Array.isArray(parsed) ? parsed : [];
            } catch (e) {
              return [];
            }
          }
          return [];
        };
        const cfg = row.server_cfg && typeof row.server_cfg === 'object' ? row.server_cfg : {};
        const list = asArr(cfg.protocols).length ? asArr(cfg.protocols) : asArr(cfg.listeners).map((l) => l.type || 'listener');
        if (!list.length) return <span className="pp-muted">—</span>;
        return (
          <span>
            {list.map((x) => (
              <Tag key={x} style={{ marginBottom: 4 }}>{x}</Tag>
            ))}
            {cfg.port_base ? <span className="pp-muted" style={{ fontSize: 11.5, marginLeft: 4 }}>:{cfg.port_base}</span> : null}
          </span>
        );
      },
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 80,
      render: (v) => (Number(v) ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>),
    },
    { title: '更新时间', dataIndex: 'updated_at', width: 112, render: (v) => <span className="pp-muted">{fmtAgo(v)}</span> },
    {
      title: '操作',
      width: 118,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0}>
          <Button type="text" size="small" title="编辑" icon={<EditOutlined />} onClick={() => openEditTpl(row)} />
          <Button type="text" size="small" title="预览" icon={<EyeOutlined />} onClick={() => openTplPreview(row)} />
          <Popconfirm title="删除该模板？" onConfirm={() => removeTpl(row)}>
            <Button type="text" size="small" danger title="删除" icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const openTplPreview = async (row) => {
    try {
      const res = await apiGet(`/api/mihomo/templates/${row.id}`);
      setPreview({ title: row.name, yaml: res.data.content, templateOnly: true });
    } catch (e) {
      message.error(e.message || '读取模板失败');
    }
  };

  const bindColumns = [
    { title: '绑定名称', dataIndex: 'name', ellipsis: true },
    { title: '服务器', dataIndex: 'server_name', ellipsis: true, render: (v, r) => v || `#${r.server_id}` },
    { title: '模板', dataIndex: 'template_name', ellipsis: true, render: (v) => v || '—' },
    {
      title: '授权 UUID',
      dataIndex: 'uuid_count',
      width: 110,
      render: (v, r) => (
        <Tooltip title={(r.uuid_list || []).length ? r.uuid_list.map((u) => String(u).slice(0, 8)).join('\n') : '未指定（使用模板全部用户）'}>
          <Tag color={v ? 'geekblue' : 'default'}>{v || 0}</Tag>
        </Tooltip>
      ),
    },
    { title: '最近生成', dataIndex: 'last_preview_at', width: 130, render: (v) => (v ? fmtTime(v) : '未生成') },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 90,
      render: (v) => (v ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>),
    },
    { title: '更新时间', dataIndex: 'updated_at', width: 170, render: (v) => fmtTime(v) },
    {
      title: '操作',
      width: 300,
      fixed: 'right',
      render: (_, row) => (
        <Space size={4}>
          <Button size="small" type="link" icon={<EyeOutlined />} onClick={() => openPreview(row)}>
            预览配置
          </Button>
          <Button size="small" type="link" onClick={() => renderServerConfig(row)}>
            生成配置
          </Button>
          <Button size="small" type="link" onClick={() => applyServerBinding(row)}>
            下发
          </Button>
          <Button size="small" type="link" icon={<EditOutlined />} onClick={() => openEditBind(row)}>
            编辑
          </Button>
          <Button
            size="small"
            type="link"
            onClick={() =>
              openCreateBind({
                name: (row.name || '绑定') + ' 副本',
                template_id: row.template_id,
                uuid_list: row.uuid_list || [],
                protocols: row.protocols || [],
                remark: row.remark || '',
              })
            }
          >
            复制
          </Button>
          <Popconfirm title="删除该绑定？" onConfirm={() => removeBind(row)}>
            <Button size="small" type="link" danger icon={<DeleteOutlined />}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
    <div>
      <Tabs
        activeKey={tab}
          tabBarStyle={templatesOnly ? { display: 'none' } : undefined}
        onChange={setTab}
        items={([
          {
            key: 'templates',
            label: `协议模板（${templates.length}）`,
            children: (
              <PageCard
                title={<span className="pp-section-title">协议模板（mihomo）</span>}
                extra={
                  <Space>
                    <Button icon={<ReadOutlined />} onClick={() => setHelpOpen(true)}>
                      协议说明
                    </Button>
                    <Button icon={<ReloadOutlined />} onClick={loadAll} loading={loading}>
                      刷新
                    </Button>
                    <Button type="primary" icon={<FileAddOutlined />} onClick={openCreateTpl}>
                      新建模板
                    </Button>
                  </Space>
                }
                bodyStyle={{ padding: 0 }}
              >
                <Table
                  className="pp-table"
                  rowKey="id"
                  size="medium"
                  loading={loading}
                  dataSource={templates}
                  columns={tplColumns}
                  scroll={{ x: 1180 }}
                  pagination={{ pageSize: 10, showTotal: (n) => `共 ${n} 个模板` }}
                />
              </PageCard>
            ),
          },
          {
            key: 'bindings',
            label: `节点绑定（${bindings.length}）`,
            children: (
              <>
                <Space wrap style={{ marginBottom: 12 }}>
                  <Button size="small" icon={<SafetyCertificateOutlined />} onClick={runCheck} loading={checkState.loading}>
                    一致性检查
                  </Button>
                  <Button size="small" disabled={!selectedIds.length} onClick={() => bulkToggle(true)}>
                    批量启用
                  </Button>
                  <Button size="small" disabled={!selectedIds.length} onClick={() => bulkToggle(false)}>
                    批量停用
                  </Button>
                  <Button
                    size="small"
                    icon={<DownloadOutlined />}
                    disabled={!selectedIds.length}
                    onClick={() => exportBindings(selectedIds)}
                  >
                    批量导出
                  </Button>
                  <span className="pp-muted" style={{ fontSize: 12 }}>
                    已选 {selectedIds.length} 项
                  </span>
                </Space>
                <Table
                  rowKey="id"
                  size="middle"
                  loading={loading}
                  dataSource={bindings}
                  columns={bindColumns}
                  rowSelection={{ selectedRowKeys: selectedIds, onChange: setSelectedIds }}
                  scroll={{ x: 1250 }}
                  pagination={{ pageSize: 10, hideOnSinglePage: true }}
                />
              </>
            ),
          },
        ]).filter((it) => !templatesOnly || it.key === 'templates')}
      />

      <Modal
        title={tplEditing ? `编辑模板 · ${tplEditing.name}` : '新建 mihomo 模板'}
        open={tplOpen}
        onCancel={() => setTplOpen(false)}
        onOk={saveTpl}
        okText="保存"
        width={860}
        destroyOnClose
      >
        <Form form={tplForm} layout="vertical">
          <Space size={16} style={{ width: '100%' }} align="start">
            <Form.Item name="name" label="模板名称" rules={[{ required: true, message: '请填写名称' }]} style={{ width: 320 }}>
              <Input placeholder="例如：内置默认" maxLength={100} />
            </Form.Item>
            <Form.Item name="remark" label="备注" style={{ flex: 1, minWidth: 240 }}>
              <Input placeholder="默认配置" maxLength={200} />
            </Form.Item>
          </Space>
          <Space size={24} align="center" style={{ marginBottom: 10 }}>
            <span>
              <span className="pp-muted" style={{ fontSize: 12, marginRight: 8 }}>启用</span>
              <Form.Item name="enabled" valuePropName="checked" noStyle>
                <Switch size="small" defaultChecked />
              </Form.Item>
            </span>
          </Space>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
            <span className="pp-muted" style={{ fontSize: 12 }}>listeners JSON（端口写在模板里，保存后按模板下发；用户由节点授权注入）</span>
            <Space size={8}>
              <Button size="small" onClick={() => setTplJson(MIHOMO_TEMPLATE_EXAMPLE)}>填入示例</Button>
              <Button
                size="small"
                loading={tplPreview.loading}
                onClick={async () => {
                  setTplPreview((p) => ({ ...p, loading: true }));
                  try {
                    const res = await apiPost('/api/mihomo/templates/preview-server', { server_json: tplJson });
                    setTplPreview({ yaml: (res.data && res.data.yaml) || '', loading: false });
                  } catch (e) {
                    setTplPreview({ yaml: '', loading: false });
                    message.error(e.message || '生成失败');
                  }
                }}
              >
                生成当前配置
              </Button>
            </Space>
          </div>
          <Input.TextArea
            value={tplJson}
            onChange={(e) => setTplJson(e.target.value)}
            autoSize={{ minRows: 14, maxRows: 26 }}
            style={{ fontFamily: 'ui-monospace, Menlo, monospace', fontSize: 12 }}
            placeholder='{"listeners":[{"name":"hy2","type":"hysteria2","port":13801}]}'
          />
          <Space size={8} wrap style={{ marginTop: 8, marginBottom: 10 }}>
            <Select
              size="small"
              style={{ minWidth: 200 }}
              placeholder={tplVersions.length ? '选择历史版本' : '暂无历史版本'}
              disabled={!tplVersions.length}
              onChange={async (vid) => {
                try {
                  const res = await apiGet(`/api/mihomo/templates/${tplEditing.id}/versions/${vid}`);
                  const row = res.data || {};
                  const raw = row.content || row.server_json || '{}';
                  setTplJson(typeof raw === 'string' ? raw : JSON.stringify(raw, null, 2));
                  message.success('已载入版本，保存后生效');
                } catch (e) {
                  message.error(e.message || '载入版本失败');
                }
              }}
              options={tplVersions.map((v) => ({ value: v.id, label: '#' + v.id + ' · ' + (v.created_at ? new Date(v.created_at).toLocaleString() : '') }))}
            />
            <Button
              size="small"
              onClick={() => {
                setImportText('');
                setImportOpen(true);
              }}
            >
              从配置导入
            </Button>
            <Button
              size="small"
              onClick={() => {
                try {
                  const parsed = JSON.parse(tplJson || '{}');
                  const list = parsed.listeners || [];
                  if (!Array.isArray(list) || !list.length) throw new Error('缺少 listeners 数组');
                  setTplParsed(list.map((l, i) => ({ key: i,
                    name: l.name || '-',
                    type: l.type || '-',
                    port: l.port || 0,
                    listen: l.listen || '',
                    note: [
                      l.certificate ? 'TLS' : '',
                      l['reality-config'] ? 'REALITY' : '',
                      l['ws-path'] ? 'WS ' + l['ws-path'] : '',
                      l.password && l.type === 'shadowsocks' ? 'SS' : '',
                    ].filter(Boolean).join(' · ') || '—',
                  })));
                  message.success(`解析成功：${list.length} 个 listener`);
                } catch (e) {
                  setTplParsed(null);
                  message.error('解析失败：' + e.message);
                }
              }}
            >
              解析配置
            </Button>
          </Space>
          {tplParsed ? (
            <Table
              size="small"
              rowKey="key"
              pagination={false}
              style={{ marginBottom: 10 }}
              dataSource={tplParsed}
              columns={[
                { title: '名称', width: 160, render: (_, row) => <Input size="small" value={row.name} onChange={(e) => updateListener(row.key, 'name', e.target.value)} /> },
                { title: '类型', width: 140, render: (_, row) => (
                  <Select
                    size="small"
                    value={row.type}
                    style={{ width: '100%' }}
                    options={['vless','vmess','trojan','anytls','hysteria2','tuic','shadowsocks','socks','http'].map((v) => ({ value: v, label: v }))}
                    onChange={(v) => updateListener(row.key, 'type', v)}
                  />
                ) },
                { title: '端口', width: 100, render: (_, row) => <InputNumber size="small" min={1} max={65535} value={row.port} style={{ width: '100%' }} onChange={(v) => updateListener(row.key, 'port', Number(v) || 0)} /> },
                { title: '监听', width: 110, render: (_, row) => <Input size="small" value={row.listen} onChange={(e) => updateListener(row.key, 'listen', e.target.value)} /> },
                { title: '说明', dataIndex: 'note' },
                {
                  title: '操作',
                  width: 80,
                  render: (_, row) => (
                    <Button
                      size="small"
                      type="link"
                      danger
                      onClick={() => {
                        try {
                          const parsed = JSON.parse(tplJson || '{}');
                          parsed.listeners = (parsed.listeners || []).filter((_, i) => i !== row.key);
                          setTplJson(JSON.stringify(parsed, null, 2));
                          setTplParsed(parsed.listeners.map((l, i) => ({ key: i, name: l.name, type: l.type, port: l.port, listen: l.listen, note: '' })));
                        } catch (e) {
                          message.error('删除失败');
                        }
                      }}
                    >
                      删除
                    </Button>
                  ),
                },
              ]}
            />
          ) : null}
          {tplPreview.yaml ? (
            <pre
              style={{
                margin: '0 0 12px',
                maxHeight: 220,
                overflow: 'auto',
                background: '#0f172a',
                color: '#e2e8f0',
                borderRadius: 10,
                padding: 12,
                fontSize: 12,
                lineHeight: 1.5,
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
              }}
            >
              {tplPreview.yaml}
            </pre>
          ) : null}
        </Form>
      </Modal>

      <Modal
        title={bindEditing ? `编辑绑定 · ${bindEditing.name}` : '新建 mihomo 节点绑定'}
        open={bindOpen}
        onCancel={() => setBindOpen(false)}
        onOk={saveBind}
        okText="保存"
        width={680}
        destroyOnClose
      >
        <Form form={bindForm} layout="vertical">
          <Form.Item name="name" label="绑定名称" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如：HK-01 mihomo" maxLength={100} />
          </Form.Item>
          <Form.Item name="server_id" label="服务器（绑定该服务器上的 sing-box 节点）" rules={[{ required: true, message: '请选择服务器' }]}>
            <Select showSearch optionFilterProp="label" options={serverOptions} placeholder="选择服务器" />
          </Form.Item>
          <Form.Item name="template_id" label="mihomo 模板" rules={[{ required: true, message: '请选择模板' }]}>
            <Select showSearch optionFilterProp="label" options={templateOptions} placeholder="选择模板" />
          </Form.Item>
          <Space size={12} style={{ width: '100%' }} align="start">
            <Form.Item name={['advanced', 'group_type']} label="自动分组类型" style={{ width: 200 }}>
              <Select
                options={[
                  { value: 'url-test', label: 'url-test（测速选优）' },
                  { value: 'fallback', label: 'fallback（故障切换）' },
                  { value: 'load-balance', label: 'load-balance（负载均衡）' },
                  { value: 'select', label: 'select（手动）' },
                ]}
              />
            </Form.Item>
            <Form.Item name={['advanced', 'test_url']} label="测速地址" style={{ flex: 1 }}>
              <Input placeholder="http://www.gstatic.com/generate_204" />
            </Form.Item>
          </Space>
          <Form.Item name={['advanced', 'extra_rules']} label="额外规则（每行一条，插入到 MATCH 之前）">
            <TextArea rows={3} placeholder="DOMAIN-SUFFIX,example.com,Proxy" />
          </Form.Item>
          {bindTplMode === 'server' ? (
            <div className="pp-muted" style={{ fontSize: 12, margin: '4px 0 12px' }}>
              用户授权请在「订阅用户 → 详情 → 服务」中勾选该节点；此处只绑定模板与节点信息。
            </div>
          ) : null}
          {bindTplMode !== 'server' ? (
          <>
          <Form.Item name="port_base" label="端口基址（0 = 使用模板默认，服务端模板生效）">
            <InputNumber min={0} max={65000} style={{ width: 200 }} />
          </Form.Item>
          <Form.Item name="domain" label="域名（可选，写入备注与后续下发）">
            <Input placeholder="hk.example.com" />
          </Form.Item>
          <Form.Item name="config_path" label="配置文件路径">
            <Input placeholder="/etc/mihomo/config.yaml" />
          </Form.Item>
          <Form.Item name="service" label="systemd 服务名">
            <Input placeholder="mihomo" />
          </Form.Item>
          <Form.Item name="uuid_list" label="UUID（不选则使用模板中的全部用户）">
            <Select
              mode="multiple"
              allowClear
              showSearch
              optionFilterProp="label"
              options={uuidOptions}
              placeholder="选择 UUID"
              maxTagCount="responsive"
            />
          </Form.Item>
          </>
          ) : null}
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input placeholder="可选" maxLength={200} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`版本历史 · ${versions.template?.name || ''}`}
        open={versions.open}
        onCancel={() => setVersions({ open: false, template: null, list: [], loading: false, content: '' })}
        footer={null}
        width={760}
      >
        <Table
          rowKey="id"
          size="small"
          loading={versions.loading}
          dataSource={versions.list}
          pagination={{ pageSize: 8, hideOnSinglePage: true }}
          columns={[
            { title: '版本', dataIndex: 'id', width: 90, render: (v) => '#' + v },
            { title: '名称', dataIndex: 'name', ellipsis: true },
            { title: '内容长度', dataIndex: 'content_len', width: 110, render: (v) => (v || 0) + ' 字符' },
            { title: '操作人', dataIndex: 'actor', width: 150, ellipsis: true, render: (v) => v || '—' },
            { title: '时间', dataIndex: 'created_at', width: 170, render: (v) => fmtTime(v) },
            {
              title: '操作',
              width: 160,
              render: (_, row) => (
                <Space size={4}>
                  <Button size="small" type="link" onClick={() => viewVersion(row.id)}>
                    查看
                  </Button>
                  <Popconfirm title="回滚到该版本？" onConfirm={() => restoreVersion(row.id)}>
                    <Button size="small" type="link">
                      回滚
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Modal>

      <Modal
        title={`配置预览 · ${preview?.title || ''}`}
        open={!!preview}
        onCancel={() => setPreview(null)}
        width={900}
        footer={
          <Space>
            <Button
              icon={<CopyOutlined />}
              disabled={!preview?.yaml}
              onClick={() => {
                navigator.clipboard?.writeText(preview.yaml);
                message.success('已复制');
              }}
            >
              复制
            </Button>
            <Button
              type="primary"
              icon={<CloudDownloadOutlined />}
              disabled={!preview?.yaml}
              onClick={() => downloadText(preview.filename || `${preview.title || 'mihomo'}.yaml`, preview.yaml)}
            >
              下载
            </Button>
            <Button onClick={() => setPreview(null)}>关闭</Button>
          </Space>
        }
      >
        {preview?.templateOnly ? null : preview && !preview.templateOnly ? (
          <div style={{ marginBottom: 10, color: '#64748b', fontSize: 12.5 }}>
            服务器 {preview.server || '—'} · 模板 {preview.template || '—'} · UUID {preview.uuid_count || 0} · 节点 {preview.proxy_count || 0}
            {preview.skipped ? ` · 跳过 ${preview.skipped}` : ''}
          </div>
        ) : null}
        <pre
          style={{
            margin: 0,
            maxHeight: '60vh',
            overflow: 'auto',
            background: '#0f172a',
            color: '#e2e8f0',
            borderRadius: 10,
            padding: 14,
            fontSize: 12,
            lineHeight: 1.55,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
          }}
        >
          {previewLoading ? '生成中…' : preview?.yaml || ''}
        </pre>
      </Modal>
    </div>
      <Modal
        title="从配置导入"
        open={importOpen}
        onCancel={() => setImportOpen(false)}
        width={760}
        onOk={async () => {
          if (!importText.trim()) return;
          try {
            const res = await apiPost('/api/mihomo/templates/parse', { content: importText });
            const d = res.data || {};
            tplForm.setFieldsValue({ content: importText });
            applyParseResult(d);
            const msg = '节点 ' + (d.proxy_count || 0) + ' · 分组 ' + (d.group_count || 0) + ' · 规则 ' + (d.rule_count || 0) + (d.listeners ? ' · listeners ' + d.listeners : '');
            if ((d.warnings || []).length) message.warning(msg + '；' + d.warnings.join('；'));
            else message.success(msg);
            setImportOpen(false);
          } catch (e) {
            message.error(e.message || '解析失败');
          }
        }}
        okText="解析并填入"
      >
        <TextArea
          rows={14}
          value={importText}
          onChange={(e) => setImportText(e.target.value)}
          placeholder="粘贴 mihomo / Clash YAML 配置"
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12.5 }}
        />
      </Modal>

      <Modal
        title="mihomo 配置说明"
        open={helpOpen}
        onCancel={() => setHelpOpen(false)}
        footer={<Button onClick={() => setHelpOpen(false)}>关闭</Button>}
        width={720}
      >
        <div style={{ fontSize: 13, lineHeight: 1.9, color: '#334155' }}>
          <p><b>常用字段</b></p>
          <ul style={{ paddingLeft: 18 }}>
            <li><code>mixed-port</code>：HTTP/SOCKS 混合端口，默认 7890</li>
            <li><code>mode</code>：rule（规则）/ global（全局）/ direct（直连）</li>
            <li><code>log-level</code>：silent / error / warning / info / debug</li>
            <li><code>external-controller</code>：RESTful 控制端口，供面板/客户端使用</li>
            <li><code>dns.enhanced-mode</code>：fake-ip（推荐）或 redir-host</li>
          </ul>
          <p><b>节点注入</b></p>
          <ul style={{ paddingLeft: 18 }}>
            <li>模板中的 <code>proxies:</code> 下留一个空行，生成时会替换为实际节点</li>
            <li>分组可用 <code>{'{{PROXY_NAMES}}'}</code> 占位，自动替换为节点名列表</li>
          </ul>
          <p><b>绑定高级项</b></p>
          <ul style={{ paddingLeft: 18 }}>
            <li>自动分组类型：改写 Auto 组的 type（url-test / fallback / load-balance / select）</li>
            <li>测速地址：替换 url-test 的检测 URL</li>
            <li>额外规则：按行插入到 MATCH 之前，例如 <code>DOMAIN-SUFFIX,openai.com,Proxy</code></li>
          </ul>
          <p><b>协议支持</b></p>
          <ul style={{ paddingLeft: 18 }}>
            <li>SS2022 / AnyTLS / Hysteria2 / VLESS-REALITY / VLESS-WS / VLESS-WS-CDN / TUIC</li>
            <li>订阅地址：<code>/sub/mihomo/&lt;uuid&gt;</code>，可直接导入 Clash Verge、Mihomo Party、Stash</li>
          </ul>
        </div>
      </Modal>

      <Modal
        title="一致性检查"
        open={checkState.open}
        onCancel={() => setCheckState({ open: false, loading: false, data: null })}
        footer={
          <Space>
            <Button type="primary" onClick={autoFix}>
              自动修复越权 UUID
            </Button>
            <Button onClick={() => setCheckState({ open: false, loading: false, data: null })}>关闭</Button>
          </Space>
        }
        width={760}
      >
        {checkState.data ? (
          <>
            <div style={{ marginBottom: 10, color: '#64748b', fontSize: 12.5 }}>
              已检查 {checkState.data.checked || 0} 个绑定，发现 {checkState.data.issue_count || 0} 个问题
            </div>
            <Table
              rowKey={(r, i) => i}
              size="small"
              dataSource={checkState.data.issues || []}
              pagination={false}
              locale={{ emptyText: '未发现问题' }}
              columns={[
                { title: '级别', dataIndex: 'level', width: 90, render: (v) => <Tag color={v === 'error' ? 'red' : 'orange'}>{v === 'error' ? '错误' : '警告'}</Tag> },
                { title: '绑定', dataIndex: 'name', width: 180, ellipsis: true },
                { title: '问题', dataIndex: 'message' },
              ]}
            />
          </>
        ) : (
          <div style={{ padding: '20px 0', color: '#94a3b8' }}>检查中…</div>
        )}
      </Modal>
    </div>
  );
}
