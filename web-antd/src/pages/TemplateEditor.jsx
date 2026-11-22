/* =================================================================
 *  订阅模板 · 解析编辑器（全屏 Drawer）
 *
 *  设计要点：
 *  · 前端不解析、不重写 YAML：所有结构化编辑都调用后端 patch-section 生成新 YAML；
 *  · 分区：基础设置 / DNS / TUN / 策略组 / 规则 / 规则集 / 原始 YAML；
 *  · 流程：加载内容（直接读取存储桶/模板 URL）→ 编辑 → 校验 → 保存到存储桶（立即生效）；发布版本快照仅保留兼容；
 *  · 版本历史可查看/回滚（回滚写入内容，保存到存储桶后生效）；
 *  · 原始 YAML 采用「带行号的等宽 TextArea」（未引入 CodeMirror，避免新增依赖与构建体积，见报告）。
 * ================================================================= */
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  App as AntdApp,
  Button,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Modal,
  Popconfirm,
  Select,
  Skeleton,
  Space,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import {
  CheckCircleOutlined,
  CloseOutlined,
  DownloadOutlined,
  EyeOutlined,
  HistoryOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  ReloadOutlined,
  SaveOutlined,
  UnorderedListOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import { api } from '../api';
import { useAsync } from '../hooks';
import { fmtBytes, fmtTime } from '../format';
import { CopyText } from '../components';
import { ObjectForm, ProxyGroupsTable, RuleProvidersTable, RulesTable } from '../components/TemplateEditor/sections';
import TemplateListEditor from '../components/TemplateEditor/TemplateListEditor';

const SECTIONS = [
  { key: 'base', label: '基础设置' },
  { key: 'dns', label: 'DNS' },
  { key: 'tun', label: 'TUN' },
  { key: 'proxy_groups', label: '策略组' },
  { key: 'rules', label: '规则' },
  { key: 'rule_providers', label: '规则集' },
  { key: 'raw', label: '原始 YAML' },
];

// 左侧导航按组展示
const SECTION_GROUPS = [
  { title: '基础', keys: ['base', 'dns', 'tun'] },
  { title: '策略与规则', keys: ['proxy_groups', 'rules', 'rule_providers'] },
  { title: '原始内容', keys: ['raw'] },
];

const SECTION_ALIASES = {
  base: 'base',
  dns: 'dns',
  tun: 'tun',
  'proxy-groups': 'proxy_groups',
  proxy_groups: 'proxy_groups',
  proxygroups: 'proxy_groups',
  groups: 'proxy_groups',
  rules: 'rules',
  'rule-providers': 'rule_providers',
  rule_providers: 'rule_providers',
  providers: 'rule_providers',
  raw: 'raw',
  yaml: 'raw',
};

function normalizeSectionKey(raw) {
  const k = String(raw || '').toLowerCase().replace(/\s+/g, '');
  return SECTION_ALIASES[k] || '';
}

/** 编辑器接口统一返回 { success, data }；这里取 data，兼容平铺写法。 */
function unwrap(res) {
  if (res && typeof res === 'object' && Object.prototype.hasOwnProperty.call(res, 'data')) {
    return res.data;
  }
  return res;
}

/**
 * 从后端 errors[] 提取可展示信息。
 * 后端结构校验错误是字符串（如 `proxy-groups[2] 缺少 name`、`rules[5] 为空`、`dns.listen 必须是字符串`），
 * 无独立 section/line 字段：这里按错误文案前缀推断分区、按 `line N` 推断行号（YAML 语法错误带行号时可用）。
 */
function errorItem(e) {
  if (typeof e === 'string') {
    const raw = e;
    const lineMatch = raw.match(/(?:line|行)\s*(\d+)/i);
    const head = raw.trim().split(/[（(\[.:：\s]/)[0].toLowerCase();
    const section = normalizeSectionKey(head);
    return {
      message: raw,
      section,
      line: lineMatch ? Number(lineMatch[1]) : 0,
    };
  }
  if (e && typeof e === 'object') {
    return {
      message: String(e.message || e.msg || e.error || JSON.stringify(e)),
      section: normalizeSectionKey(e.section || e.part || e.section_name || ''),
      line: Number(e.line || e.line_no || e.lineNumber || 0) || 0,
    };
  }
  return { message: String(e) };
}

/** 带行号的原始 YAML 编辑器（同步滚动；校验错误行号标红） */
function RawYamlEditor({ value, onChange, errorLines }) {
  const gutterRef = useRef(null);
  const taRef = useRef(null);
  const lineCount = useMemo(() => String(value || '').split('\n').length, [value]);
  const errSet = useMemo(() => new Set((errorLines || []).map(Number).filter((n) => n > 0)), [errorLines]);

  return (
    <div
      style={{
        display: 'flex',
        height: '100%',
        minHeight: 460,
        border: '1px solid var(--pp-border)',
        borderRadius: 10,
        overflow: 'hidden',
        background: 'var(--pp-surface)',
      }}
    >
      <div
        ref={gutterRef}
        style={{
          width: 58,
          flex: 'none',
          overflow: 'hidden',
          background: 'var(--pp-code-bg)',
          borderRight: '1px solid var(--pp-border)',
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
          fontSize: 12,
          lineHeight: '20px',
          padding: '10px 8px 10px 0',
          textAlign: 'right',
          color: 'var(--pp-code-text)',
          userSelect: 'none',
        }}
      >
        {Array.from({ length: lineCount }, (_, i) => (
          <div key={i} style={{ color: errSet.has(i + 1) ? 'var(--pp-danger)' : undefined, fontWeight: errSet.has(i + 1) ? 600 : 400 }}>
            {i + 1}
          </div>
        ))}
      </div>
      <textarea
        ref={taRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onScroll={() => {
          if (gutterRef.current && taRef.current) gutterRef.current.scrollTop = taRef.current.scrollTop;
        }}
        spellCheck={false}
        wrap="off"
        style={{
          flex: 1,
          minWidth: 0,
          border: 0,
          outline: 'none',
          resize: 'none',
          padding: '10px 12px',
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
          fontSize: 12.5,
          lineHeight: '20px',
          whiteSpace: 'pre',
          overflow: 'auto',
          tabSize: 2,
          background: 'var(--pp-code-bg)',
          color: 'var(--pp-code-text)',
        }}
      />
    </div>
  );
}

export default function TemplateEditor({ id, open, onClose, onChanged }) {
  const { message } = AntdApp.useApp();
  const base = `templates/manage/${id}`;

  const [meta, setMeta] = useState(null);
  const [content, setContent] = useState('');
  const [savedContent, setSavedContent] = useState('');
  const [parsed, setParsed] = useState(null);
  const [parsedContent, setParsedContent] = useState('');
  const [section, setSection] = useState('base');
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(null);
  const [parseLoading, setParseLoading] = useState(false);
  const [busy, setBusy] = useState('');
  const [validation, setValidation] = useState(null);
  const [patchWarnings, setPatchWarnings] = useState([]);
  const [preview, setPreview] = useState(null);
  const [versions, setVersions] = useState({ open: false, list: [], loading: false, view: null });
  const [saveError, setSaveError] = useState(false);
  const [listEditorOpen, setListEditorOpen] = useState(false);
  // CDN 公共地址：模板顶部展示完整 CDN URL（bucket_key 拼接或 meta.cdn_url）
  const fileStatus = useAsync(() => api('attachments/status'), []);
  const cdnBase = String((fileStatus.data && fileStatus.data.cdn_base_url) || '').replace(/\/+$/, '');
  const [navCollapsed, setNavCollapsed] = useState(false);
  const [vw, setVw] = useState(() => (typeof window !== 'undefined' ? window.innerWidth || 1440 : 1440));
  const narrow = vw < 1200;
  const mobile = vw < 768;

  useEffect(() => {
    const onResize = () => setVw(window.innerWidth || 1440);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  const dirty = content !== savedContent;
  const external = !!(
    meta
    && (meta.publishable === false
      || meta.managed === false
      || /external/i.test(String(meta.source || '')))
  );

  const draftState = useMemo(() => {
    if (dirty) return { color: 'orange', text: '已修改未保存' };
    if (saveError) return { color: 'red', text: '保存失败' };
    const ds = String((meta && (meta.draft_sha256 || meta.sha256)) || '');
    if (!ds) return { color: 'default', text: '未保存' };
    return { color: 'green', text: '已保存到存储桶' };
  }, [dirty, saveError, meta]);

  const summary = (parsed && parsed.summary) || {};
  const sections = (parsed && parsed.sections) || {};

  // 左侧导航计数：对象分区显示字段数、列表分区显示条数、原始 YAML 显示字数
  const sectionCount = useCallback((key) => {
    const sec = sections[key];
    if (key === 'raw') return content ? `${String(content).length} 字` : null;
    if (key === 'base' || key === 'dns' || key === 'tun') {
      return sec && typeof sec === 'object' ? Object.keys(sec).length : 0;
    }
    return Array.isArray(sec) ? sec.length : 0;
  }, [sections, content]);

  const parseContent = useCallback(async (c, silent) => {
    setParseLoading(true);
    try {
      const res = await api(`${base}/parse`, { method: 'POST', body: { content: c } });
      const d = unwrap(res) || {};
      setParsed({ summary: d.summary || {}, sections: d.sections || {} });
      setParsedContent(c);
      return d;
    } catch (e) {
      if (!silent) message.error(e.message || '解析失败');
      return null;
    } finally {
      setParseLoading(false);
    }
  }, [base, message]);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    setSaveError(false);
    try {
      const m = unwrap(await api(`${base}/content`)) || {};
      // 内容优先使用 content API 返回的 content（后端可能直接读存储桶或模板 URL），
      // 不再强制 import-url 到草稿；仅当接口没有返回任何内容时才兜底导入（兼容旧后端）。
      let c = String((m && (m.content || m.draft_content || m.published_content)) || '');
      if (!c) {
        try {
          const r = unwrap(await api(`${base}/import-url`, { method: 'POST', body: {} })) || {};
          const imported = String(r.content || r.draft_content || '');
          if (imported) {
            c = imported;
            message.info('已从模板 URL 读取内容；保存会写入存储桶并立即生效');
          }
        } catch (e) {
          if (e && e.status === 403) {
            message.warning('需要超级管理员权限才能读取模板 URL 内容；当前仅可查看/预览');
          } else {
            message.warning(e.message || '模板内容读取失败，可稍后重试');
          }
        }
      }
      // 兼容后端字段：draft_sha256 / sha256；bucket_key 用于展示存储桶文件
      const metaNext = {
        ...(m || {}),
        draft_sha256: String((m && (m.draft_sha256 || m.sha256)) || ''),
      };
      setMeta(metaNext);
      setContent(c);
      setSavedContent(c);
      setValidation(null);
      setPatchWarnings([]);
      if (c) await parseContent(c, true);
      else setParsed(null);
    } catch (e) {
      setLoadError(e);
    } finally {
      setLoading(false);
    }
  }, [base, message, parseContent]);

  useEffect(() => {
    if (open && id) {
      setSection('base');
      setVersions({ open: false, list: [], loading: false, view: null });
      load();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, id]);

  // 原始 YAML 被修改后切到结构化分区：先按当前内容重新解析，避免基于旧结构编辑
  useEffect(() => {
    if (!open || loading || section === 'raw') return;
    if (!content || content === parsedContent) return;
    parseContent(content, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [section, content, parsedContent, loading, open]);

  const validateContent = useCallback(async (c, silent) => {
    const res = await api(`${base}/validate`, { method: 'POST', body: { content: c } });
    const d = unwrap(res) || {};
    const payload = {
      ok: !!d.ok,
      errors: d.errors || [],
      warnings: d.warnings || [],
      summary: d.summary || {},
    };
    setValidation(payload);
    if (!silent) {
      if (payload.ok) {
        message.success(payload.warnings.length ? `校验通过（${payload.warnings.length} 条警告）` : '校验通过');
      } else {
        message.error(`校验未通过：${payload.errors.length} 个错误`);
      }
    }
    return payload;
  }, [base, message]);

  const doValidate = async () => {
    if (busy || !content) return;
    setBusy('validate');
    try {
      await validateContent(content);
    } catch (e) {
      message.error(e.message || '校验失败');
    } finally {
      setBusy('');
    }
  };

  const saveDraft = async () => {
    if (busy || !content) return false;
    setBusy('save');
    try {
      const res = await api(`${base}/content`, {
        method: 'PUT',
        body: { content, base_sha256: (meta && meta.draft_sha256) || undefined },
      });
      const d = unwrap(res) || {};
      const sha = String(d.sha256 || d.draft_sha256 || '');
      setSaveError(false);
      setSavedContent(content);
      setMeta((m) => ({ ...(m || {}), draft_content: content, draft_sha256: sha || (m && m.draft_sha256) || '', draft_updated_at: Date.now() }));
      let v = null;
      try {
        v = await validateContent(content, true);
      } catch (e) {
        /* 校验失败不阻塞保存提示 */
      }
      message.success(v && !v.ok ? '已保存到存储桶（校验存在错误，可稍后修正）' : '已保存到存储桶，立即生效');
      if (onChanged) onChanged();
      return true;
    } catch (e) {
      setSaveError(true);
      if (e && e.status === 409) {
        message.error('保存冲突：草稿已被他人修改，请关闭后重新打开再编辑');
      } else if (e && e.status === 403) {
        message.error('需要超级管理员权限才能保存到存储桶');
      } else {
        message.error(e.message || '保存失败');
      }
      return false;
    } finally {
      setBusy('');
    }
  };

  const applyPatch = async (sectionKey, data) => {
    if (busy || !content) return;
    setBusy('patch');
    try {
      const res = await api(`${base}/patch-section`, {
        method: 'POST',
        body: { content, section: sectionKey, data },
      });
      const d = unwrap(res) || {};
      const next = String(d.content || '');
      if (!next) throw new Error('后端未返回新的 YAML 内容');
      const warns = Array.isArray(d.warnings) ? d.warnings.map((w) => String(w)) : [];
      const verrs = Array.isArray(d.errors) ? d.errors.map((e) => String(e)) : [];
      setContent(next);
      setPatchWarnings(warns);
      // patch 响应已带 sections/summary，直接刷新，避免多一次 parse 请求
      if (d.sections) {
        setParsed({ summary: d.summary || {}, sections: d.sections });
        setParsedContent(next);
      } else {
        await parseContent(next, true);
      }
      if (verrs.length) {
        setValidation({ ok: false, errors: verrs, warnings: warns, summary: d.summary || {} });
        message.warning(`已更新区段，但校验存在 ${verrs.length} 个错误`);
      } else {
        setValidation(null);
        if (warns.length) message.warning(`已更新区段，但存在 ${warns.length} 条警告`);
        else message.success('已更新该区段（尚未保存为草稿）');
      }
    } catch (e) {
      message.error(e.message || '区段更新失败');
    } finally {
      setBusy('');
    }
  };

  /* ---------------- 预览 ---------------- */
  const openPreview = () => {
    setPreview({ open: true, uuid: '', yaml: '', proxies: 0, total: 0, skipped: 0, loading: false });
  };

  const runPreview = async () => {
    if (!preview || preview.loading) return;
    setPreview((p) => ({ ...p, loading: true }));
    try {
      const body = { content };
      const uuid = String(preview.uuid || '').trim();
      if (uuid) body.uuid = uuid;
      const res = await api(`${base}/preview`, { method: 'POST', body });
      const d = unwrap(res) || {};
      setPreview((p) => ({
        ...p,
        loading: false,
        yaml: String(d.yaml || ''),
        proxies: Number(d.proxies || 0),
        total: Number(d.total || 0),
        skipped: Number(d.skipped || 0),
      }));
    } catch (e) {
      setPreview((p) => ({ ...p, loading: false }));
      message.error(e.message || '预览失败');
    }
  };

  const downloadPreview = () => {
    if (!preview || !preview.yaml) return;
    try {
      const blob = new Blob([preview.yaml], { type: 'text/yaml;charset=utf-8' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = `template-${id}-preview.yaml`;
      document.body.appendChild(a);
      a.click();
      setTimeout(() => {
        URL.revokeObjectURL(a.href);
        a.remove();
      }, 800);
    } catch (e) {
      message.error('浏览器不支持下载，请复制内容');
    }
  };

  /* ---------------- 版本历史 ---------------- */
  const openVersions = async () => {
    setVersions({ open: true, list: [], loading: true, view: null });
    try {
      const res = await api(`${base}/versions`);
      const payload = unwrap(res);
      const list = Array.isArray(payload) ? payload : (payload && (payload.data || payload.versions)) || [];
      setVersions((s) => ({ ...s, list, loading: false }));
    } catch (e) {
      setVersions((s) => ({ ...s, loading: false }));
      message.error(e.message || '读取版本历史失败');
    }
  };

  const viewVersion = async (row) => {
    const vid = row.id !== undefined ? row.id : row.version;
    try {
      const res = await api(`${base}/versions/${encodeURIComponent(vid)}`);
      setVersions((s) => ({ ...s, view: unwrap(res) || {} }));
    } catch (e) {
      message.error(e.message || '读取版本内容失败');
    }
  };

  const restoreVersion = async (row) => {
    const vid = row.id !== undefined ? row.id : row.version;
    try {
      const res = await api(`${base}/versions/${encodeURIComponent(vid)}/restore`, { method: 'POST', body: {} });
      const d = unwrap(res) || {};
      const c = String(d.content || d.draft_content || '');
      if (c) {
        setContent(c);
        setSavedContent(c);
        await parseContent(c, true);
        setMeta((m) => ({ ...(m || {}), draft_sha256: String(d.sha256 || (m && m.draft_sha256) || '') }));
      } else {
        await load();
      }
      setVersions((s) => ({ ...s, open: false, view: null }));
      message.success('已回滚内容：保存到存储桶后生效');
    } catch (e) {
      if (e && e.status === 403) message.error('需要超级管理员权限才能回滚版本');
      else message.error(e.message || '回滚失败');
    }
  };

  /* ---------------- 关闭（未保存确认） ---------------- */
  const requestClose = () => {
    if (!dirty) {
      if (onClose) onClose();
      return;
    }
    Modal.confirm({
      title: '有未保存的修改',
      content: '关闭后本次未保存的修改会丢失。可以先保存到存储桶再关闭。',
      okText: '保存到存储桶并关闭',
      cancelText: '不保存，直接关闭',
      onOk: async () => {
        const ok = await saveDraft();
        if (ok && onClose) onClose();
      },
      onCancel: () => {
        if (onClose) onClose();
      },
    });
  };

  // Ctrl/Cmd + S 保存到存储桶
  const saveRef = useRef(saveDraft);
  saveRef.current = saveDraft;
  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => {
      if ((e.ctrlKey || e.metaKey) && String(e.key).toLowerCase() === 's') {
        e.preventDefault();
        if (dirty && !busy) saveRef.current();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, dirty, busy]);

  // 刷新/关页面前提示未保存
  useEffect(() => {
    if (!open || !dirty) return undefined;
    const handler = (e) => {
      e.preventDefault();
      e.returnValue = '';
      return '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [open, dirty]);

  /* ---------------- 渲染 ---------------- */
  const errorItems = useMemo(() => ((validation && validation.errors) || []).map(errorItem), [validation]);
  const errorLines = useMemo(() => errorItems.map((e) => e.line).filter((n) => n > 0), [errorItems]);

  const summaryTags = useMemo(() => {
    const tags = [];
    if (summary.proxy_groups !== undefined) tags.push(`策略组 ${summary.proxy_groups}`);
    if (summary.rules !== undefined) tags.push(`规则 ${summary.rules}`);
    if (summary.rule_providers !== undefined) tags.push(`规则集 ${summary.rule_providers}`);
    if (summary.top_keys !== undefined) tags.push(`顶层键 ${summary.top_keys}`);
    if (summary.proxies !== undefined) tags.push(`proxies ${summary.proxies}`);
    return tags;
  }, [summary]);

  const jumpToError = (e) => {
    const key = normalizeSectionKey(e.section);
    if (key) setSection(key);
  };

  const renderSectionBody = () => {
    if (loading) return <Skeleton active paragraph={{ rows: 8 }} />;
    if (loadError) {
      return (
        <Alert
          type="error"
          showIcon
          message="加载模板内容失败"
          description={loadError.message || String(loadError)}
          action={<Button size="small" onClick={load}>重试</Button>}
        />
      );
    }
    if (!content) {
      return (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="还没有草稿内容：可能是外部模板导入失败，请点「刷新」重试或先在列表页确认模板 URL 可访问"
        />
      );
    }
    if (section === 'raw') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, height: '100%' }}>
          <div className="pp-muted" style={{ fontSize: 12, lineHeight: 1.7 }}>
            原始 YAML 全文编辑：提交前请点「校验」或「保存到存储桶」由后端解析校验；结构化编辑请使用左侧其它分区。
            锚点/别名/注入位由后端保留，面板不会自动展开重写。
          </div>
          <RawYamlEditor value={content} onChange={setContent} errorLines={errorLines} />
        </div>
      );
    }
    if (section === 'base' || section === 'dns' || section === 'tun') {
      const label = section === 'base' ? '基础设置' : section === 'dns' ? 'DNS' : 'TUN';
      const hint =
        section === 'base'
          ? '端口、allow-lan、ipv6、日志、profile、sniffer 等顶层设置'
          : section === 'dns'
            ? 'DNS 监听、增强模式、fake-ip、nameserver / fallback 等'
            : 'TUN 启用、栈、自动路由、DNS 劫持等';
      return (
        <ObjectForm
          key={`${section}-${(sections[section] && JSON.stringify(sections[section]).length) || 0}`}
          title={label}
          hint={hint}
          value={sections[section]}
          disabled={!!busy}
          columns={vw >= 1000 ? 2 : 1}
          groups={
            section === 'dns'
              ? [{ title: 'DNS 设置', test: () => true }]
              : section === 'tun'
                ? [{ title: 'TUN 设置', test: () => true }]
                : undefined
          }
          onApply={(next) => applyPatch(section, next)}
        />
      );
    }
    if (section === 'proxy_groups') {
      return (
        <ProxyGroupsTable
          groups={sections.proxy_groups}
          disabled={!!busy}
          onChange={(next) => applyPatch('proxy_groups', next)}
        />
      );
    }
    if (section === 'rules') {
      return <RulesTable rules={sections.rules} disabled={!!busy} onChange={(next) => applyPatch('rules', next)} />;
    }
    if (section === 'rule_providers') {
      return (
        <RuleProvidersTable
          providers={sections.rule_providers}
          disabled={!!busy}
          onChange={(next) => applyPatch('rule_providers', next)}
        />
      );
    }
    return null;
  };

  return (
    <Drawer
      open={open}
      onClose={requestClose}
      placement="right"
      width="100%"
      closable={false}
      styles={{ body: { padding: 0 } }}
      title={null}
      destroyOnHidden={false}
    >
      <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden', paddingBottom: mobile ? 72 : 0 }}>
        {/* 顶部：信息行 + 操作行（sticky，不随内容滚动消失） */}
        <div style={{ position: 'sticky', top: 0, zIndex: 20, background: 'var(--pp-surface)', borderBottom: '1px solid var(--pp-border)' }}>
          {/* 信息行：名称 / 状态 / 存储桶 / 解析摘要 / 上次保存 / 关闭（独立最右） */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', padding: '10px 16px 6px' }}>
            <b style={{ fontSize: 15 }}>模板：{(meta && meta.name) || `#${id}`}</b>
            <Tag color={draftState.color} style={{ marginInlineEnd: 0 }}>{draftState.text}</Tag>
            {external ? <Tag color="default">外部模板</Tag> : null}
            {meta && meta.bucket_key ? (
              <span
                className="pp-mono pp-muted"
                style={{
                  fontSize: 12,
                  maxWidth: 360,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 6,
                }}
                title={meta.bucket_key}
              >
                存储桶：{meta.bucket_key}
                <CopyText value={meta.bucket_key} display={<span style={{ fontSize: 11 }}>复制</span>} tip="复制存储桶路径" />
              </span>
            ) : meta && meta.url ? (
              <a
                href={meta.url}
                target="_blank"
                rel="noreferrer"
                className="pp-mono pp-muted"
                style={{ fontSize: 12, maxWidth: 360, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                title={meta.url}
              >
                {meta.url}
              </a>
            ) : null}
            {(() => {
              const url = meta && (meta.cdn_url || meta.url)
                ? String(meta.cdn_url || meta.url)
                : (meta && meta.bucket_key && cdnBase ? `${cdnBase}/${String(meta.bucket_key).replace(/^\/+/, '')}` : '');
              if (!url) return null;
              return (
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, minWidth: 0 }}>
                  <Tooltip title={url}>
                    <a
                      href={url}
                      target="_blank"
                      rel="noreferrer"
                      className="pp-mono pp-muted"
                      style={{ maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                    >
                      CDN：{url}
                    </a>
                  </Tooltip>
                  <CopyText value={url} display={<span style={{ fontSize: 11 }}>复制</span>} tip="复制 CDN 地址" />
                </span>
              );
            })()}
            {summaryTags.length ? summaryTags.map((t) => <Tag key={t} style={{ marginInlineEnd: 0 }}>{t}</Tag>) : null}
            {meta && meta.content_version ? <Tag color="blue" style={{ marginInlineEnd: 0 }}>版本 {meta.content_version}</Tag> : null}
            {parseLoading ? <span className="pp-muted" style={{ fontSize: 12 }}>解析中…</span> : null}
            <span style={{ marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 8 }}>
              {meta && meta.draft_updated_at ? (
                <span className="pp-muted" style={{ fontSize: 12 }}>上次保存：{fmtTime(meta.draft_updated_at)}</span>
              ) : null}
              <Button size="small" icon={<CloseOutlined />} onClick={requestClose}>
                关闭
              </Button>
            </span>
          </div>
          {/* 操作行：次要操作 + 保存主按钮（primary 靠右，与关闭按钮分开） */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', padding: '0 16px 10px' }}>
            <span className="pp-muted" style={{ fontSize: 12 }}>
              Ctrl/Cmd+S 保存到存储桶
              {dirty ? <span style={{ color: 'var(--pp-warning)', marginLeft: 8 }}>有未保存修改</span> : null}
            </span>
            <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
              <Button size="small" icon={<CheckCircleOutlined />} loading={busy === 'validate'} onClick={doValidate} disabled={!content}>
                校验
              </Button>
              <Button size="small" icon={<EyeOutlined />} onClick={openPreview} disabled={!content}>
                预览
              </Button>
              <Button size="small" icon={<HistoryOutlined />} onClick={openVersions}>
                版本历史
              </Button>
              <Button size="small" icon={<UnorderedListOutlined />} onClick={() => setListEditorOpen(true)}>
                List 编辑
              </Button>
              <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={loading}>
                重新加载
              </Button>
              <Tooltip title="保存后直接写入存储桶并立即生效（无需发布）">
                <Button size="small" type="primary" icon={<SaveOutlined />} loading={busy === 'save'} onClick={saveDraft} disabled={!content || !dirty}>
                  保存到存储桶
                </Button>
              </Tooltip>
            </span>
          </div>
          {/* 窄屏：导航收起，用下拉选择分区 */}
          {mobile ? (
            <div style={{ padding: '0 16px 10px' }}>
              <Select
                size="small"
                value={section}
                onChange={setSection}
                style={{ width: '100%' }}
                options={SECTIONS.map((s) => ({ value: s.key, label: s.label }))}
              />
            </div>
          ) : null}
        </div>

        {/* 主体：左侧分区导航 + 右侧编辑区 */}
        <div style={{ flex: 1, display: 'flex', minHeight: 0, minWidth: 0, overflow: 'hidden' }}>
          {!mobile ? (
            <div
              style={{
                width: navCollapsed ? 56 : narrow ? 180 : 232,
                flex: 'none',
                borderRight: '1px solid var(--pp-border)',
                background: 'var(--pp-surface)',
                padding: '10px 8px',
                overflow: 'auto',
                transition: 'width .15s',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6, marginBottom: 6, padding: '0 4px' }}>
                {navCollapsed ? null : <span className="pp-muted" style={{ fontSize: 11.5 }}>分区导航</span>}
                <Button
                  size="small"
                  type="text"
                  aria-label="折叠导航"
                  title={navCollapsed ? '展开导航' : '折叠导航'}
                  icon={navCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
                  onClick={() => setNavCollapsed((v) => !v)}
                  style={{ marginLeft: navCollapsed ? 0 : 'auto' }}
                />
              </div>
              {SECTION_GROUPS.map((group) => (
                <div key={group.title} style={{ marginBottom: 10 }}>
                  {navCollapsed ? null : (
                    <div className="pp-muted" style={{ fontSize: 11, padding: '4px 8px', letterSpacing: 0.5 }}>
                      {group.title}
                    </div>
                  )}
                  {group.keys.map((key) => {
                    const s = SECTIONS.find((x) => x.key === key) || { key, label: key };
                    const active = section === key;
                    const count = sectionCount(key);
                    return (
                      <div
                        key={key}
                        role="button"
                        tabIndex={0}
                        title={s.label}
                        onClick={() => setSection(key)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') setSection(key);
                        }}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 6,
                          padding: navCollapsed ? '8px 0' : '8px 12px',
                          justifyContent: navCollapsed ? 'center' : 'flex-start',
                          marginBottom: 2,
                          borderRadius: 8,
                          cursor: 'pointer',
                          background: active ? 'var(--pp-info-soft)' : 'transparent',
                          color: active ? 'var(--pp-info)' : 'var(--pp-text)',
                          fontWeight: active ? 600 : 400,
                          fontSize: navCollapsed ? 12 : 13,
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {navCollapsed ? String(s.label).slice(0, 2) : s.label}
                        {!navCollapsed && count !== null ? (
                          <span className="pp-muted" style={{ marginLeft: 'auto', fontSize: 11 }}>{count}</span>
                        ) : null}
                      </div>
                    );
                  })}
                </div>
              ))}
              {navCollapsed ? null : (
                <div className="pp-muted" style={{ fontSize: 11.5, lineHeight: 1.7, padding: '12px 10px 0' }}>
                  结构化编辑由后端生成 YAML（patch-section）；「应用到编辑内容」只改本地内容，点「保存到存储桶」后生效。
                </div>
              )}
            </div>
          ) : null}

          <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
            {errorItems.length ? (
              <div style={{ maxHeight: 170, overflow: 'auto', borderBottom: '1px solid var(--pp-danger-soft)', background: 'var(--pp-danger-soft)', padding: '8px 16px' }}>
                <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--pp-danger)', marginBottom: 4 }}>
                  <WarningOutlined /> 校验错误（{errorItems.length}）
                </div>
                {errorItems.map((e, i) => (
                  <div
                    key={i}
                    onClick={() => jumpToError(e)}
                    style={{ fontSize: 12, color: 'var(--pp-danger)', padding: '2px 0', cursor: e.section ? 'pointer' : 'default' }}
                    title={e.section ? '点击跳到对应分区' : undefined}
                  >
                    {e.section ? <Tag color="red" style={{ marginInlineEnd: 6 }}>{e.section}</Tag> : null}
                    {e.line ? <span style={{ color: 'var(--pp-text)' }}>行 {e.line}：</span> : null}
                    {e.message}
                  </div>
                ))}
              </div>
            ) : null}
            {validation && validation.ok && (validation.warnings || []).length ? (
              <div style={{ padding: '6px 16px', borderBottom: '1px solid var(--pp-warning-soft)', background: 'var(--pp-warning-soft)', fontSize: 12, color: 'var(--pp-warning)' }}>
                <WarningOutlined /> 校验警告：{(validation.warnings || []).map((w) => (typeof w === 'string' ? w : w && w.message ? w.message : JSON.stringify(w))).join('；')}
              </div>
            ) : null}
            {patchWarnings.length ? (
              <div style={{ padding: '6px 16px', borderBottom: '1px solid var(--pp-warning-soft)', background: 'var(--pp-warning-soft)', fontSize: 12, color: 'var(--pp-warning)' }}>
                区段更新警告：{patchWarnings.join('；')}
              </div>
            ) : null}
            <div style={{ flex: 1, overflow: 'auto', padding: 16 }}>{renderSectionBody()}</div>
          </div>
        </div>

        {/* 移动端：保存按钮固定底部 */}
        {mobile ? (
          <div
            style={{
              position: 'fixed',
              left: 0,
              right: 0,
              bottom: 0,
              zIndex: 100,
              background: 'var(--pp-surface)',
              borderTop: '1px solid var(--pp-border)',
              padding: '10px 16px',
              boxShadow: 'var(--pp-shadow-md)',
            }}
          >
            <Button block type="primary" icon={<SaveOutlined />} loading={busy === 'save'} onClick={saveDraft} disabled={!content || !dirty}>
              保存到存储桶
            </Button>
          </div>
        ) : null}
      </div>

      {/* 预览弹窗 */}
      <TemplateListEditor open={listEditorOpen} onClose={() => setListEditorOpen(false)} />
      <Modal
        open={!!preview}
        title="预览：注入 proxies 后的订阅 YAML"
        width={900}
        centered
        destroyOnHidden
        onCancel={() => setPreview(null)}
        footer={(
          <Space>
            <Button onClick={runPreview} loading={preview && preview.loading} icon={<ReloadOutlined />}>
              重新生成
            </Button>
            <Button onClick={downloadPreview} disabled={!preview || !preview.yaml} icon={<DownloadOutlined />}>
              下载 YAML
            </Button>
            <Button type="primary" onClick={() => setPreview(null)}>关闭</Button>
          </Space>
        )}
      >
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
          <span className="pp-muted" style={{ fontSize: 12 }}>UUID（留空由后端使用示例节点）</span>
          <Input
            value={preview ? preview.uuid : ''}
            onChange={(e) => setPreview((p) => ({ ...p, uuid: e.target.value }))}
            placeholder="可选：输入 UUID 预览该用户的订阅"
            style={{ width: 320 }}
            className="pp-mono"
            allowClear
          />
          <Button size="small" onClick={runPreview} loading={preview && preview.loading}>
            生成预览
          </Button>
          {preview && (preview.total !== undefined) ? (
            <Space size={8}>
              <Tag>proxies {preview.proxies}</Tag>
              <Tag>节点 {preview.total}</Tag>
              <Tag>跳过 {preview.skipped}</Tag>
            </Space>
          ) : null}
        </div>
        <pre
          style={{
            margin: 0,
            maxHeight: '52vh',
            overflow: 'auto',
            background: 'var(--pp-code-bg)',
            color: 'var(--pp-code-text)',
            padding: 12,
            borderRadius: 8,
            fontSize: 12,
            lineHeight: 1.6,
          }}
        >
          {(preview && preview.yaml) || (preview && preview.loading ? '生成中…' : '点击「生成预览」查看注入 proxies 后的完整 YAML')}
        </pre>
        <div className="pp-muted" style={{ fontSize: 12, marginTop: 8 }}>
          仅预览，不写访问日志；不会修改模板内容。保存前建议用目标 UUID 预览确认注入结果。
        </div>
      </Modal>

      {/* 版本历史 */}
      <Modal
        open={versions.open}
        title="版本历史"
        width={960}
        centered
        destroyOnHidden
        onCancel={() => setVersions((s) => ({ ...s, open: false, view: null }))}
        footer={<Button onClick={() => setVersions((s) => ({ ...s, open: false, view: null }))}>关闭</Button>}
      >
        {versions.view ? (
          <div>
            <Space style={{ marginBottom: 8 }}>
              <Button size="small" onClick={() => setVersions((s) => ({ ...s, view: null }))}>返回列表</Button>
              <Tag>版本 {versions.view.version !== undefined ? versions.view.version : versions.view.id}</Tag>
              <Tag>{String(versions.view.action || '—')}</Tag>
              {versions.view.actor ? <Tag>{String(versions.view.actor)}</Tag> : null}
            </Space>
            <pre
              style={{
                margin: 0,
                maxHeight: '60vh',
                overflow: 'auto',
                background: 'var(--pp-code-bg)',
                color: 'var(--pp-code-text)',
                padding: 12,
                borderRadius: 8,
                fontSize: 12,
                lineHeight: 1.6,
              }}
            >
              {String(versions.view.content || '')}
            </pre>
          </div>
        ) : (
          <Table
            rowKey={(r, i) => String((r && (r.id !== undefined ? r.id : r.version)) || i)}
            size="small"
            dataSource={versions.list}
            loading={versions.loading}
            pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 个版本` }}
            columns={[
              { title: '版本', width: 90, render: (_, r) => (r.version !== undefined ? r.version : r.id) },
              { title: '操作', dataIndex: 'action', width: 110, render: (v) => <Tag>{String(v || '—')}</Tag> },
              { title: '操作人', dataIndex: 'actor', width: 170, render: (v) => v || '—' },
              { title: '时间', dataIndex: 'created_at', width: 170, render: (v) => fmtTime(v) },
              { title: 'SHA', dataIndex: 'sha256', width: 130, render: (v) => <span className="pp-mono">{String(v || '').slice(0, 10)}</span> },
              {
                title: '大小',
                dataIndex: 'size',
                width: 100,
                render: (v, r) => fmtBytes(Number(v) || (r && r.content ? String(r.content).length : 0)),
              },
              {
                title: '操作',
                width: 170,
                render: (_, row) => (
                  <Space size={0}>
                    <Button size="small" type="link" onClick={() => viewVersion(row)}>查看</Button>
                    <Popconfirm
                      title="回滚到该版本？"
                      description="回滚后请点击「保存到存储桶」，才会重新写入并影响订阅。"
                      okText="回滚"
                      onConfirm={() => restoreVersion(row)}
                    >
                      <Button size="small" type="link" danger>回滚</Button>
                    </Popconfirm>
                  </Space>
                ),
              },
            ]}
            locale={{ emptyText: '还没有版本记录' }}
          />
        )}
      </Modal>
    </Drawer>
  );
}