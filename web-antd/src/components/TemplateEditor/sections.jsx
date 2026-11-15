/* =================================================================
 *  订阅模板编辑器 · 分区结构化编辑组件
 *  · 基础 / DNS / TUN：通用对象表单（保留未知字段，JSON 子编辑器兜底）
 *  · 策略组 / 规则 / 规则集：表格化增删改排序
 *  · 所有改动都交由主组件调用后端 patch-section 生成新 YAML（前端不解析/重写 YAML）
 * ================================================================= */
import React, { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Empty,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
} from '@ant-design/icons';

const PROXY_GROUP_TYPES = [
  'select',
  'url-test',
  'fallback',
  'load-balance',
  'relay',
  'smart',
];

/** 通用 JSON 文本校验：返回 [ok, value|错误信息] */
function parseJSONText(text, fallback) {
  const t = String(text || '').trim();
  if (!t) return [true, fallback];
  try {
    return [true, JSON.parse(t)];
  } catch (e) {
    return [false, e.message];
  }
}

/** 判断值是否为简单类型（字符串/数字/布尔） */
function isScalar(v) {
  return v === null || ['string', 'number', 'boolean'].includes(typeof v);
}

/** 值 → JSON 文本框内容 */
function toJSONText(v) {
  try {
    return JSON.stringify(v, null, 2);
  } catch (e) {
    return '';
  }
}

/** 对象字段值 → 展示文本 */
function scalarText(v) {
  if (v === null || v === undefined) return '';
  if (typeof v === 'boolean') return String(v);
  if (typeof v === 'object') return toJSONText(v);
  return String(v);
}

/* -----------------------------------------------------------------
 * 通用对象表单：基础设置 / DNS / TUN
 * ----------------------------------------------------------------- */
export function ObjectForm({ title, hint, value, onApply, disabled, extraActions }) {
  const objectValue = value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  const [draft, setDraft] = useState(objectValue);
  const [jsonTexts, setJsonTexts] = useState({});
  const [jsonErrs, setJsonErrs] = useState({});
  const [addKey, setAddKey] = useState('');
  const [addValue, setAddValue] = useState('');

  // 外部（parse/patch 返回）内容变化时重置表单
  useEffect(() => {
    setDraft(objectValue);
    setJsonTexts({});
    setJsonErrs({});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  const dirty = useMemo(() => {
    try {
      const merged = { ...draft };
      Object.entries(jsonTexts).forEach(([k, text]) => {
        const [ok, v] = parseJSONText(text, merged[k]);
        if (ok) merged[k] = v;
      });
      return JSON.stringify(merged) !== JSON.stringify(objectValue);
    } catch (e) {
      return true;
    }
  }, [draft, jsonTexts, objectValue]);

  const setScalar = (key, v) => setDraft((d) => ({ ...d, [key]: v }));

  const setJSONText = (key, text) => {
    setJsonTexts((s) => ({ ...s, [key]: text }));
    const [ok, v] = parseJSONText(text, draft[key]);
    setJsonErrs((s) => {
      const next = { ...s };
      if (ok) delete next[key];
      else next[key] = v;
      return next;
    });
    if (ok) setDraft((d) => ({ ...d, [key]: v }));
  };

  const removeKey = (key) => {
    setDraft((d) => {
      const next = { ...d };
      delete next[key];
      return next;
    });
    setJsonTexts((s) => {
      const next = { ...s };
      delete next[key];
      return next;
    });
  };

  const addField = () => {
    const key = String(addKey || '').trim();
    if (!key) {
      return;
    }
    let parsed = addValue;
    const t = addValue.trim();
    if (t === 'true' || t === 'false') parsed = t === 'true';
    else if (t !== '' && !Number.isNaN(Number(t))) parsed = Number(t);
    else {
      const [ok, v] = parseJSONText(t, t);
      parsed = ok ? v : t;
    }
    setDraft((d) => ({ ...d, [key]: parsed }));
    setAddKey('');
    setAddValue('');
  };

  const apply = () => {
    if (Object.keys(jsonErrs).length) return;
    const merged = { ...draft };
    Object.entries(jsonTexts).forEach(([k, text]) => {
      const [ok, v] = parseJSONText(text, merged[k]);
      if (ok) merged[k] = v;
    });
    onApply(merged);
  };

  const entries = Object.entries(draft);

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10, flexWrap: 'wrap' }}>
        <b style={{ fontSize: 14 }}>{title}</b>
        {hint ? <span className="pp-muted" style={{ fontSize: 12 }}>{hint}</span> : null}
        <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 8 }}>
          {extraActions}
          <Button size="small" onClick={apply} disabled={disabled || !dirty || Object.keys(jsonErrs).length > 0}>
            应用修改
          </Button>
        </span>
      </div>
      {dirty ? (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 10, borderRadius: 8 }}
          message="有未应用的修改：点「应用修改」后会调用后端生成新的 YAML，不会直接发布。"
        />
      ) : null}
      {Object.keys(jsonErrs).length ? (
        <Alert type="error" showIcon style={{ marginBottom: 10, borderRadius: 8 }} message={Object.values(jsonErrs)[0]} />
      ) : null}

      {entries.length ? (
        <div style={{ display: 'grid', gap: 8 }}>
          {entries.map(([key, v]) => (
            <div
              key={key}
              style={{
                display: 'grid',
                gridTemplateColumns: 'minmax(120px, 200px) 1fr auto',
                gap: 10,
                alignItems: 'start',
                padding: '8px 10px',
                border: '1px solid #eef2f7',
                borderRadius: 8,
                background: '#fbfcfe',
              }}
            >
              <span className="pp-mono" style={{ fontSize: 12.5, color: '#334155', paddingTop: 5, wordBreak: 'break-all' }}>
                {key}
              </span>
              {typeof v === 'boolean' ? (
                <Switch checked={v} onChange={(checked) => setScalar(key, checked)} disabled={disabled} />
              ) : typeof v === 'number' ? (
                <InputNumber value={v} onChange={(n) => setScalar(key, n)} disabled={disabled} style={{ width: '100%' }} />
              ) : v === null || typeof v === 'string' ? (
                <Input
                  value={v === null ? '' : v}
                  onChange={(e) => setScalar(key, e.target.value)}
                  disabled={disabled}
                  className={v && typeof v === 'string' && v.length > 60 ? 'pp-mono' : undefined}
                />
              ) : (
                <Input.TextArea
                  className="pp-mono"
                  autoSize={{ minRows: 2, maxRows: 8 }}
                  value={jsonTexts[key] !== undefined ? jsonTexts[key] : toJSONText(v)}
                  onChange={(e) => setJSONText(key, e.target.value)}
                  spellCheck={false}
                  disabled={disabled}
                  style={{ fontSize: 12, lineHeight: 1.6 }}
                />
              )}
              <Popconfirm title={`删除字段 ${key}？`} okText="删除" onConfirm={() => removeKey(key)} disabled={disabled}>
                <Button size="small" type="text" danger icon={<DeleteOutlined />} disabled={disabled} aria-label={`删除 ${key}`} />
              </Popconfirm>
            </div>
          ))}
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该区段没有可编辑字段" />
      )}

      <div style={{ display: 'flex', gap: 8, marginTop: 12, flexWrap: 'wrap' }}>
        <Input
          placeholder="新字段名，如 port"
          value={addKey}
          onChange={(e) => setAddKey(e.target.value)}
          style={{ width: 180 }}
          disabled={disabled}
          className="pp-mono"
        />
        <Input
          placeholder="值（字符串 / 数字 / true / JSON）"
          value={addValue}
          onChange={(e) => setAddValue(e.target.value)}
          onPressEnter={addField}
          style={{ width: 260 }}
          disabled={disabled}
          className="pp-mono"
        />
        <Button icon={<PlusOutlined />} onClick={addField} disabled={disabled || !addKey.trim()}>
          添加字段
        </Button>
      </div>
      <div className="pp-muted" style={{ fontSize: 12, marginTop: 8, lineHeight: 1.7 }}>
        数组/对象字段用 JSON 编辑；未改动的字段（含未知字段）会原样保留。
      </div>
    </div>
  );
}

/* -----------------------------------------------------------------
 * 策略组表格
 * ----------------------------------------------------------------- */
const EMPTY_GROUP = { name: '', type: 'select', proxies: [] };

export function ProxyGroupsTable({ groups, onChange, disabled }) {
  const list = Array.isArray(groups) ? groups : [];
  const [editing, setEditing] = useState(null); // { index, data }
  const [err, setErr] = useState('');

  const openEdit = (index) => {
    setErr('');
    setEditing({ index, data: { ...(index >= 0 ? list[index] : EMPTY_GROUP) } });
  };

  const saveEdit = () => {
    if (!editing) return;
    const name = String(editing.data.name || '').trim();
    if (!name) {
      setErr('策略组名称不能为空（emoji 名称请原样保留）');
      return;
    }
    const duplicate = list.some((g, i) => i !== editing.index && String(g && g.name) === name);
    if (duplicate) {
      setErr(`策略组名称重复：${name}`);
      return;
    }
    if (editing.data.proxies !== undefined && !Array.isArray(editing.data.proxies)) {
      setErr('proxies 必须是数组');
      return;
    }
    const next = [...list];
    if (editing.index >= 0) next[editing.index] = editing.data;
    else next.push(editing.data);
    onChange(next);
    setEditing(null);
  };

  const move = (index, delta) => {
    const target = index + delta;
    if (target < 0 || target >= list.length) return;
    const next = [...list];
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  const remove = (index) => onChange(list.filter((_, i) => i !== index));

  const columns = [
    { title: '#', width: 48, render: (_, __, i) => <span className="pp-muted">{i + 1}</span> },
    {
      title: '名称',
      dataIndex: 'name',
      width: 220,
      render: (v) => <span style={{ wordBreak: 'break-all' }}>{String(v || '') || <span className="pp-muted">—</span>}</span>,
    },
    {
      title: '类型',
      dataIndex: 'type',
      width: 130,
      render: (v) => <Tag color="geekblue" style={{ marginInlineEnd: 0 }}>{String(v || 'select')}</Tag>,
    },
    {
      title: 'proxies',
      dataIndex: 'proxies',
      render: (v) => {
        const arr = Array.isArray(v) ? v : [];
        return arr.length ? (
          <Space size={4} wrap>
            {arr.slice(0, 4).map((p) => (
              <Tag key={String(p)} style={{ marginInlineEnd: 0 }}>{String(p)}</Tag>
            ))}
            {arr.length > 4 ? <span className="pp-muted">+{arr.length - 4}</span> : null}
          </Space>
        ) : (
          <span className="pp-muted">—</span>
        );
      },
    },
    {
      title: 'include-all',
      dataIndex: 'include_all',
      width: 100,
      render: (v) => (v ? <Tag color="green">是</Tag> : <span className="pp-muted">否</span>),
    },
    {
      title: 'url / interval',
      width: 200,
      render: (_, row) => (
        <span className="pp-muted" style={{ fontSize: 12, wordBreak: 'break-all' }}>
          {row.url ? String(row.url) : '—'}
          {row.interval ? ` · ${row.interval}s` : ''}
        </span>
      ),
    },
    {
      title: '操作',
      width: 178,
      render: (_, __, index) => (
        <Space size={0}>
          <Button size="small" type="text" disabled={disabled || index === 0} onClick={() => move(index, -1)} icon={<ArrowUpOutlined />} aria-label="上移" />
          <Button size="small" type="text" disabled={disabled || index === list.length - 1} onClick={() => move(index, 1)} icon={<ArrowDownOutlined />} aria-label="下移" />
          <Button size="small" type="link" icon={<EditOutlined />} disabled={disabled} onClick={() => openEdit(index)}>编辑</Button>
          <Popconfirm title={`删除策略组「${String(list[index] && list[index].name) || ''}」？`} okText="删除" onConfirm={() => remove(index)} disabled={disabled}>
            <Button size="small" type="link" danger icon={<DeleteOutlined />} disabled={disabled}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
        <b style={{ fontSize: 14 }}>策略组 proxy-groups</b>
        <span className="pp-muted" style={{ fontSize: 12 }}>共 {list.length} 组；emoji 名称按原样保存，规则引用关系由后端校验</span>
        <Button size="small" type="primary" icon={<PlusOutlined />} style={{ marginLeft: 'auto' }} disabled={disabled} onClick={() => openEdit(-1)}>
          新增策略组
        </Button>
      </div>
      <Table
        className="pp-table"
        rowKey={(r, i) => `${i}-${String(r && r.name)}`}
        size="small"
        columns={columns}
        dataSource={list}
        pagination={false}
        loading={disabled && false}
        scroll={{ x: 1000 }}
        locale={{ emptyText: '还没有策略组' }}
      />

      <Modal
        open={!!editing}
        title={editing && editing.index >= 0 ? '编辑策略组' : '新增策略组'}
        onCancel={() => setEditing(null)}
        onOk={saveEdit}
        okText="应用（生成 YAML）"
        width={620}
        destroyOnHidden
      >
        {editing ? (
          <div style={{ display: 'grid', gap: 10, marginTop: 10 }}>
            {err ? <Alert type="error" showIcon message={err} /> : null}
            <div style={{ display: 'grid', gridTemplateColumns: '90px 1fr', gap: 10, alignItems: 'center' }}>
              <span className="pp-muted" style={{ fontSize: 12 }}>名称</span>
              <Input
                value={editing.data.name || ''}
                onChange={(e) => setEditing((s) => ({ ...s, data: { ...s.data, name: e.target.value } }))}
                placeholder="原样保留 emoji，例如 🇭🇰 香港"
              />
              <span className="pp-muted" style={{ fontSize: 12 }}>类型</span>
              <Select
                value={editing.data.type || 'select'}
                onChange={(v) => setEditing((s) => ({ ...s, data: { ...s.data, type: v } }))}
                options={PROXY_GROUP_TYPES.map((t) => ({ value: t, label: t }))}
              />
              <span className="pp-muted" style={{ fontSize: 12 }}>proxies</span>
              <Select
                mode="tags"
                value={Array.isArray(editing.data.proxies) ? editing.data.proxies.map(String) : []}
                onChange={(v) => setEditing((s) => ({ ...s, data: { ...s.data, proxies: v } }))}
                placeholder="输入代理/策略组名称后回车"
                tokenSeparators={[',']}
              />
              <span className="pp-muted" style={{ fontSize: 12 }}>include-all</span>
              <Switch
                checked={!!editing.data.include_all}
                onChange={(v) => setEditing((s) => ({ ...s, data: { ...s.data, include_all: v } }))}
              />
              <span className="pp-muted" style={{ fontSize: 12 }}>url</span>
              <Input
                value={editing.data.url || ''}
                onChange={(e) => setEditing((s) => ({ ...s, data: { ...s.data, url: e.target.value } }))}
                placeholder="url-test / fallback 的测速地址（可空）"
              />
              <span className="pp-muted" style={{ fontSize: 12 }}>interval</span>
              <InputNumber
                value={editing.data.interval}
                onChange={(v) => setEditing((s) => ({ ...s, data: { ...s.data, interval: v } }))}
                style={{ width: '100%' }}
                placeholder="测速间隔秒"
              />
              <span className="pp-muted" style={{ fontSize: 12 }}>filter</span>
              <Input
                value={editing.data.filter || ''}
                onChange={(e) => setEditing((s) => ({ ...s, data: { ...s.data, filter: e.target.value } }))}
                placeholder="正则过滤（可空）"
              />
            </div>
            <div className="pp-muted" style={{ fontSize: 12 }}>
              未展示的字段（tolerance / lazy 等）会原样保留；高级字段可用「原始 YAML」编辑。
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  );
}

/* -----------------------------------------------------------------
 * 规则表格
 * ----------------------------------------------------------------- */
export function RulesTable({ rules, onChange, disabled }) {
  const list = Array.isArray(rules) ? rules.map(String) : [];
  const [q, setQ] = useState('');
  const [editing, setEditing] = useState(null); // { index, text }

  const filtered = useMemo(() => {
    const kw = String(q || '').trim().toLowerCase();
    if (!kw) return list.map((text, index) => ({ text, index }));
    return list
      .map((text, index) => ({ text, index }))
      .filter((x) => x.text.toLowerCase().includes(kw));
  }, [list, q]);

  const move = (index, delta) => {
    const target = index + delta;
    if (target < 0 || target >= list.length) return;
    const next = [...list];
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  const remove = (index) => onChange(list.filter((_, i) => i !== index));

  const saveEdit = () => {
    if (!editing) return;
    const text = String(editing.text || '').trim();
    if (!text) return;
    const next = [...list];
    if (editing.index >= 0) next[editing.index] = text;
    else next.push(text);
    onChange(next);
    setEditing(null);
  };

  const columns = [
    { title: '#', width: 56, render: (_, __, i) => <span className="pp-muted">{i + 1}</span> },
    {
      title: '规则',
      dataIndex: 'text',
      render: (v, row) => <span className="pp-mono" style={{ fontSize: 12, wordBreak: 'break-all' }}>{v}</span>,
    },
    {
      title: '操作',
      width: 200,
      render: (_, row) => (
        <Space size={0}>
          <Button size="small" type="text" disabled={disabled || row.index === 0} onClick={() => move(row.index, -1)} icon={<ArrowUpOutlined />} aria-label="上移" />
          <Button size="small" type="text" disabled={disabled || row.index === list.length - 1} onClick={() => move(row.index, 1)} icon={<ArrowDownOutlined />} aria-label="下移" />
          <Button size="small" type="link" icon={<EditOutlined />} disabled={disabled} onClick={() => setEditing({ index: row.index, text: row.text })}>编辑</Button>
          <Popconfirm title="删除该规则？" okText="删除" onConfirm={() => remove(row.index)} disabled={disabled}>
            <Button size="small" type="link" danger icon={<DeleteOutlined />} disabled={disabled}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10, flexWrap: 'wrap' }}>
        <b style={{ fontSize: 14 }}>规则 rules</b>
        <span className="pp-muted" style={{ fontSize: 12 }}>共 {list.length} 条；规则引用不存在的策略组/规则集时由后端给出警告</span>
        <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 8 }}>
          <Input
            size="small"
            allowClear
            placeholder="搜索规则"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            style={{ width: 200 }}
          />
          <Button size="small" type="primary" icon={<PlusOutlined />} disabled={disabled} onClick={() => setEditing({ index: -1, text: '' })}>
            新增规则
          </Button>
        </span>
      </div>
      <Table
        className="pp-table"
        rowKey={(r) => `${r.index}-${r.text}`}
        size="small"
        columns={columns}
        dataSource={filtered}
        pagination={filtered.length > 100 ? { pageSize: 100, showSizeChanger: false } : false}
        scroll={{ x: 800 }}
        locale={{ emptyText: q ? '没有匹配的规则' : '还没有规则' }}
      />

      <Modal
        open={!!editing}
        title={editing && editing.index >= 0 ? `编辑规则 #${editing.index + 1}` : '新增规则'}
        onCancel={() => setEditing(null)}
        onOk={saveEdit}
        okText="应用（生成 YAML）"
        width={720}
        destroyOnHidden
      >
        {editing ? (
          <div style={{ marginTop: 10 }}>
            <div className="pp-muted" style={{ fontSize: 12, marginBottom: 8 }}>
              完整规则字符串，例如 <code>RULE-SET,proxy,🚀 节点选择</code> 或 <code>DOMAIN-SUFFIX,google.com,🚀 节点选择</code>。
              规则按顺序匹配，上移/下移可调整优先级。
            </div>
            <Input.TextArea
              className="pp-mono"
              autoSize={{ minRows: 3, maxRows: 8 }}
              value={editing.text}
              onChange={(e) => setEditing((s) => ({ ...s, text: e.target.value }))}
              spellCheck={false}
            />
          </div>
        ) : null}
      </Modal>
    </div>
  );
}

/* -----------------------------------------------------------------
 * 规则集表格（对象 map）
 * ----------------------------------------------------------------- */
const PROVIDER_KEYS = ['type', 'behavior', 'format', 'url', 'interval', 'path'];

export function RuleProvidersTable({ providers, onChange, disabled }) {
  const objectValue = providers && typeof providers === 'object' && !Array.isArray(providers) ? providers : {};
  const list = useMemo(
    () => Object.entries(objectValue).map(([name, fields]) => ({ name, fields: fields && typeof fields === 'object' ? fields : {} })),
    [objectValue]
  );
  const [editing, setEditing] = useState(null); // { name, fields }

  const commit = (rows) => {
    const next = {};
    rows.forEach((r) => {
      next[r.name] = r.fields;
    });
    onChange(next);
  };

  const move = (index, delta) => {
    const target = index + delta;
    if (target < 0 || target >= list.length) return;
    const next = [...list];
    [next[index], next[target]] = [next[target], next[index]];
    commit(next);
  };

  const remove = (index) => commit(list.filter((_, i) => i !== index));

  const saveEdit = () => {
    if (!editing) return;
    const name = String(editing.name || '').trim();
    if (!name) return;
    const rows = list.filter((r) => r.name !== name);
    rows.push({ name, fields: editing.fields });
    commit(rows);
    setEditing(null);
  };

  const columns = [
    { title: '#', width: 48, render: (_, __, i) => <span className="pp-muted">{i + 1}</span> },
    { title: '名称', dataIndex: 'name', width: 180, render: (v) => <span className="pp-mono">{v}</span> },
    { title: 'type', width: 90, render: (_, r) => <span className="pp-muted">{String(r.fields.type || '—')}</span> },
    { title: 'behavior', width: 110, render: (_, r) => <span className="pp-muted">{String(r.fields.behavior || '—')}</span> },
    { title: 'format', width: 90, render: (_, r) => <span className="pp-muted">{String(r.fields.format || '—')}</span> },
    {
      title: 'url',
      ellipsis: true,
      render: (_, r) => <span className="pp-muted" style={{ fontSize: 12 }}>{String(r.fields.url || '—')}</span>,
    },
    {
      title: 'interval',
      width: 90,
      render: (_, r) => <span className="pp-muted">{r.fields.interval !== undefined ? String(r.fields.interval) : '—'}</span>,
    },
    {
      title: '操作',
      width: 200,
      render: (_, r) => (
        <Space size={0}>
          <Button size="small" type="text" disabled={disabled || list.indexOf(r) === 0} onClick={() => move(list.indexOf(r), -1)} icon={<ArrowUpOutlined />} aria-label="上移" />
          <Button size="small" type="text" disabled={disabled || list.indexOf(r) === list.length - 1} onClick={() => move(list.indexOf(r), 1)} icon={<ArrowDownOutlined />} aria-label="下移" />
          <Button size="small" type="link" icon={<EditOutlined />} disabled={disabled} onClick={() => setEditing({ name: r.name, fields: { ...r.fields } })}>编辑</Button>
          <Popconfirm title={`删除规则集「${r.name}」？`} okText="删除" onConfirm={() => remove(list.indexOf(r))} disabled={disabled}>
            <Button size="small" type="link" danger icon={<DeleteOutlined />} disabled={disabled}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10, flexWrap: 'wrap' }}>
        <b style={{ fontSize: 14 }}>规则集 rule-providers</b>
        <span className="pp-muted" style={{ fontSize: 12 }}>
          共 {list.length} 条；使用 <code>&lt;&lt;: *anchor</code> 合并的条目由后端保留锚点结构，前端不重写 YAML
        </span>
        <Button size="small" type="primary" icon={<PlusOutlined />} style={{ marginLeft: 'auto' }} disabled={disabled} onClick={() => setEditing({ name: '', fields: { type: 'http', behavior: 'domain', format: 'mrs', url: '', interval: 86400 } })}>
          新增规则集
        </Button>
      </div>
      <Table
        className="pp-table"
        rowKey="name"
        size="small"
        columns={columns}
        dataSource={list}
        pagination={false}
        scroll={{ x: 1100 }}
        locale={{ emptyText: '还没有规则集' }}
      />

      <Modal
        open={!!editing}
        title={editing && list.some((r) => r.name === editing.name) ? `编辑规则集「${editing.name}」` : '新增规则集'}
        onCancel={() => setEditing(null)}
        onOk={saveEdit}
        okText="应用（生成 YAML）"
        width={680}
        destroyOnHidden
      >
        {editing ? (
          <div style={{ display: 'grid', gap: 10, marginTop: 10 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '90px 1fr', gap: 10, alignItems: 'center' }}>
              <span className="pp-muted" style={{ fontSize: 12 }}>名称</span>
              <Input
                value={editing.name}
                onChange={(e) => setEditing((s) => ({ ...s, name: e.target.value }))}
                placeholder="如 proxy / direct / reject / apple"
              />
              {PROVIDER_KEYS.map((key) => (
                <React.Fragment key={key}>
                  <span className="pp-muted" style={{ fontSize: 12 }}>{key}</span>
                  {key === 'interval' ? (
                    <InputNumber
                      value={editing.fields[key]}
                      onChange={(v) => setEditing((s) => ({ ...s, fields: { ...s.fields, [key]: v } }))}
                      style={{ width: '100%' }}
                    />
                  ) : (
                    <Input
                      value={editing.fields[key] === undefined ? '' : String(editing.fields[key])}
                      onChange={(e) => setEditing((s) => ({ ...s, fields: { ...s.fields, [key]: e.target.value } }))}
                      className="pp-mono"
                    />
                  )}
                </React.Fragment>
              ))}
            </div>
            <div className="pp-muted" style={{ fontSize: 12 }}>
              其他未展示字段会原样保留；锚点/别名合并结构由后端 patch 时保留，请勿在 UI 中展开。
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  );
}