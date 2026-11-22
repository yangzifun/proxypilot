import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, App as AntdApp, Button, Drawer, Empty, Select, Skeleton, Tag, Tooltip } from 'antd';
import { FileTextOutlined, ReloadOutlined, SaveOutlined } from '@ant-design/icons';
import { api } from '../../api';
import { fmtBytes } from '../../format';

/**
 * 模板编辑页 · List（rule/*.list）域名/规则列表编辑
 * - 独立 Drawer，不挤占模板编辑区；
 * - 加载 rule/ 下 .list 文件，带行号等宽编辑器（保留注释/空行/顺序）；
 * - 保存走 PUT /api/attachments/content（key/content/content_type=text/plain）；
 * - 简单非法行提示（不阻止保存，避免误伤规则）。
 */

const RULE_TYPES = new Set([
  'DOMAIN', 'DOMAIN-SUFFIX', 'DOMAIN-KEYWORD', 'DOMAIN-REGEX', 'GEOSITE', 'GEOIP',
  'IP-CIDR', 'IP-CIDR6', 'SRC-IP-CIDR', 'SRC-PORT', 'DST-PORT', 'PROCESS-NAME',
  'PROCESS-PATH', 'RULE-SET', 'MATCH', 'AND', 'OR', 'NOT',
]);

/** 简单非法行检查：返回 [{line, message}]（空行/注释跳过，不阻止保存） */
export function analyzeList(text) {
  const issues = [];
  String(text || '').split('\n').forEach((raw, idx) => {
    const line = raw.trim();
    if (!line || line.startsWith('#') || line.startsWith('//')) return;
    if (line.includes(',')) {
      const type = line.split(',')[0].trim().toUpperCase();
      if (!RULE_TYPES.has(type) && !/^[A-Z][A-Z0-9-]{1,32}$/.test(type)) {
        issues.push({ line: idx + 1, message: `未识别的规则类型「${type}」` });
      }
      return;
    }
    if (/^https?:\/\//i.test(line) || line.includes('://') || /\s/.test(line)) {
      issues.push({ line: idx + 1, message: '裸行只允许域名/关键词（不能含协议或空格）' });
    }
  });
  return issues;
}

export default function TemplateListEditor({ open, onClose, initialKey = '', onSaved }) {
  const { message } = AntdApp.useApp();
  const gutterRef = useRef(null);
  const taRef = useRef(null);

  const [files, setFiles] = useState([]);
  const [listLoading, setListLoading] = useState(false);
  const [listError, setListError] = useState(null);
  const [selected, setSelected] = useState('');
  const [content, setContent] = useState('');
  const [savedContent, setSavedContent] = useState('');
  const [state, setState] = useState({ loading: false, saving: false, error: null, meta: null });

  const loadFiles = useCallback(async () => {
    setListLoading(true);
    setListError(null);
    try {
      const res = await api('attachments', { query: { prefix: 'rule/' } });
      const list = ((res && res.files) || [])
        .filter((f) => /\.list$/i.test(String((f && f.key) || '')))
        .map((f) => ({
          key: f.key,
          name: f.name || String(f.key).split('/').pop(),
          size: f.size,
          cdn_url: f.cdn_url,
        }));
      setFiles(list);
      const want = String(initialKey || '').trim();
      setSelected((prev) => {
        // 从列表页点击「编辑」时优先选中传入的 key；不存在则按需求选第一个。
        if (want) return list.some((x) => x.key === want) ? want : (list[0] ? list[0].key : '');
        return prev && list.some((x) => x.key === prev) ? prev : (list[0] ? list[0].key : '');
      });
    } catch (e) {
      setListError(e);
    } finally {
      setListLoading(false);
    }
  }, [initialKey]);

  const loadFile = useCallback(async (key) => {
    if (!key) {
      setContent('');
      setSavedContent('');
      setState({ loading: false, saving: false, error: null, meta: null });
      return;
    }
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const res = await api('attachments/content', { query: { key } });
      const text = String((res && res.content) || '');
      setContent(text);
      setSavedContent(text);
      setState({ loading: false, saving: false, error: null, meta: res || null });
    } catch (e) {
      setContent('');
      setSavedContent('');
      setState({ loading: false, saving: false, error: e, meta: null });
    }
  }, []);

  useEffect(() => {
    if (open) loadFiles();
  }, [open, loadFiles]);

  useEffect(() => {
    if (open && selected) loadFile(selected);
  }, [open, selected, loadFile]);

  const dirty = content !== savedContent;
  const issues = useMemo(() => analyzeList(content), [content]);
  const lines = useMemo(() => String(content || '').split('\n'), [content]);
  const readonly = !!(state.meta && state.meta.editable === false);

  const save = async () => {
    if (!selected || readonly) return;
    setState((s) => ({ ...s, saving: true, error: null }));
    try {
      const res = await api('attachments/content', {
        method: 'PUT',
        body: { key: selected, content, content_type: 'text/plain' },
      });
      setSavedContent(content);
      setState((s) => ({ ...s, saving: false, meta: { ...(s.meta || {}), ...(res || {}) } }));
      if (onSaved) onSaved();
      message.success('已保存到存储桶');
    } catch (e) {
      setState((s) => ({ ...s, saving: false, error: e }));
      message.error(e.message || '保存失败');
    }
  };

  return (
    <Drawer
      open={open}
      onClose={onClose}
      width={880}
      title={(
        <span>
          <FileTextOutlined /> List 编辑（rule/*.list）
        </span>
      )}
      destroyOnHidden
    >
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 12 }}>
        <Select
          style={{ flex: 1, minWidth: 260 }}
          value={selected || undefined}
          onChange={setSelected}
          loading={listLoading}
          showSearch
          optionFilterProp="label"
          placeholder="选择 rule/ 下的 .list 文件"
          options={files.map((f) => ({ value: f.key, label: f.key }))}
          notFoundContent={listLoading ? '加载中…' : '未找到 .list 文件'}
          allowClear={false}
        />
        <Button
          icon={<ReloadOutlined />}
          onClick={() => (selected ? loadFile(selected) : loadFiles())}
          loading={listLoading || state.loading}
        >
          重新加载
        </Button>
        <Button
          type="primary"
          icon={<SaveOutlined />}
          loading={state.saving}
          disabled={!selected || !dirty || readonly}
          onClick={save}
        >
          保存到存储桶
        </Button>
      </div>

      {listError ? (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message={listError.message || '读取 rule/ 列表失败（请检查对象存储 R2 配置）'}
        />
      ) : null}
      {state.loading ? <Skeleton active paragraph={{ rows: 4 }} /> : null}
      {!state.loading && state.error ? (
        <Alert type="error" showIcon message={state.error.message || '读取文件内容失败'} />
      ) : null}
      {!state.loading && !state.error && !selected ? <Empty description="没有可编辑的 .list 文件" /> : null}

      {!state.loading && !state.error && selected ? (
        <>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 8, fontSize: 12 }} className="pp-muted">
            <span className="pp-mono">{selected}</span>
            {state.meta && state.meta.size !== undefined ? <span>{fmtBytes(Number(state.meta.size))}</span> : null}
            {state.meta && state.meta.cdn_url ? (
              <Tooltip title={state.meta.cdn_url}>
                <span
                  style={{ maxWidth: 380, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'inline-block' }}
                >
                  {state.meta.cdn_url}
                </span>
              </Tooltip>
            ) : null}
            {readonly ? <Tag color="orange">只读</Tag> : <Tag color="green">可编辑</Tag>}
          </div>
          {issues.length ? (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 8 }}
              message={`检测到 ${issues.length} 行可能不规范（不阻止保存）`}
              description={(
                <div style={{ maxHeight: 90, overflow: 'auto', fontSize: 12 }}>
                  {issues.slice(0, 8).map((it) => (
                    <div key={it.line}>第 {it.line} 行：{it.message}</div>
                  ))}
                  {issues.length > 8 ? <div>…</div> : null}
                </div>
              )}
            />
          ) : null}
          <div
            style={{
              display: 'flex',
              height: 'calc(100vh - 260px)',
              minHeight: 360,
              border: '1px solid #e2e8f0',
              borderRadius: 10,
              overflow: 'hidden',
              background: '#fff',
            }}
          >
            <div
              ref={gutterRef}
              style={{
                width: 54,
                flex: 'none',
                overflow: 'hidden',
                background: '#f8fafc',
                borderRight: '1px solid #eef2f7',
                fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
                fontSize: 12,
                lineHeight: '20px',
                padding: '10px 8px 10px 0',
                textAlign: 'right',
                color: '#94a3b8',
                userSelect: 'none',
              }}
            >
              {lines.map((_, i) => (
                <div key={i}>{i + 1}</div>
              ))}
            </div>
            <textarea
              ref={taRef}
              value={content}
              onChange={(e) => setContent(e.target.value)}
              onScroll={() => {
                if (gutterRef.current && taRef.current) gutterRef.current.scrollTop = taRef.current.scrollTop;
              }}
              spellCheck={false}
              wrap="off"
              readOnly={readonly}
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
                background: readonly ? '#f8fafc' : '#fff',
              }}
            />
          </div>
          <div className="pp-muted" style={{ fontSize: 12, marginTop: 8, lineHeight: 1.7 }}>
            保留注释 / 空行 / 顺序；保存会直接写入存储桶（{selected}），订阅端拉取后立即生效。
            非法行仅提示、不阻止保存（避免误伤 IP/CIDR 等规则）。
          </div>
        </>
      ) : null}
    </Drawer>
  );
}