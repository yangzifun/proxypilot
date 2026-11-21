import React from 'react';
import { Button, Form, Input, Select, Switch } from 'antd';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';

/**
 * 多 CDN 编辑组件（NodeBindings / Nodes 共用）。
 *
 * 后端契约为节点上的对象数组：
 *   cdn_entries: [{ name, domain, enabled, prefer_group }]
 * 兼容旧字段：sb_cdn_entries（NodeBindings 行）/ cdn_domains / sb_cdn_domains /
 * cdn_domain / sb_cdn（单字符串）。
 *
 * 规则（V1.5.1：不再区分主/备）：
 *   - 每条 CDN 只有「是否启用」；enabled 缺省 true（旧数据无 enabled 视为启用）；
 *   - 多个 CDN 可同时启用、同时生成订阅链接；停用的 CDN 不生成链接（配置保留）；
 *   - 全部停用时给出明确提示；旧数据里的 primary 字段直接忽略（不报错）。
 *
 * 这里提供的纯函数（normalizeCdnEntries / collectCdnEntries / cdnSummary）
 * 同时用于回填、提交与列表展示，便于单测。
 */

export const CDN_MAX = 8;
export const CDN_NAME_MAX = 24;

/** 名称下拉预设（严格下拉不可手填；旧数据如 CF 通过临时选项保留，用户可改选新预设） */
export const CDN_NAME_PRESETS = [
  { value: 'Cloudflare', label: 'Cloudflare' },
  { value: 'EO', label: 'EO（EdgeOne）' },
  { value: 'ESA', label: 'ESA' },
  { value: 'CloudFront', label: 'CloudFront' },
  { value: 'Fastly', label: 'Fastly' },
  // 旧名称临时选项：避免旧数据（CF）编辑时值丢失，后续可统一改为 Cloudflare
  { value: 'CF', label: 'CF（旧名称）' },
];

// 只允许域名（不含协议/路径/端口/IP）；TLD 必须为字母，避免 1.2.3.4 被当成域名。
const DOMAIN_RE = /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;
const IPV4_RE = /^(?:\d{1,3}\.){3}\d{1,3}$/;
// eslint-disable-next-line no-control-regex
const CONTROL_RE = /[\u0000-\u001f\u007f]/;

/** 归一化域名输入：去首尾空格、转小写；回填旧数据时容忍误带协议/路径。 */
export function normalizeCdnDomain(raw) {
  let s = String(raw == null ? '' : raw).trim().toLowerCase();
  s = s.replace(/^https?:\/\//, '');
  s = s.replace(/\/.*$/, '');
  return s;
}

/** 校验域名；返回空字符串表示合法，否则返回中文错误文案。 */
export function validateCdnDomain(value) {
  const raw = String(value == null ? '' : value).trim();
  if (!raw) return '请填写 CDN 域名';
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(raw)) return '只填写域名，不要带协议（如 https://）';
  if (raw.includes('/')) return '只填写域名，不要带路径';
  if (raw.includes(':') || /\s/.test(raw)) return '只填写域名，不要带端口或空格';
  const v = raw.toLowerCase();
  if (v.length > 253) return '域名过长（最多 253 字符）';
  if (CONTROL_RE.test(v)) return '只填写域名';
  if (IPV4_RE.test(v)) return '不能使用 IP 地址，请填写域名';
  if (!DOMAIN_RE.test(v)) return '域名格式不正确，例如 cdn.example.com';
  return '';
}

/** 校验名称；空名允许（提交时自动补默认名）。 */
export function validateCdnName(value) {
  const v = String(value == null ? '' : value).trim();
  if (!v) return '';
  if (v.length > CDN_NAME_MAX) return `名称最多 ${CDN_NAME_MAX} 个字符`;
  if (CONTROL_RE.test(v)) return '名称不能包含换行或控制字符';
  return '';
}

function normalizeEnabled(v) {
  return v == null ? true : !!v;
}

/**
 * 把节点行 / cdn_entries 数组 / sb_cdn_entries / 旧域名数组 / 单字符串统一成表单行。
 * 规则：去空域名、域名去重、最多 8 条、enabled 缺省 true；不再处理 primary。
 */
export function normalizeCdnEntries(src) {
  if (!src) return [];
  let raw = [];
  if (Array.isArray(src)) {
    raw = src;
  } else if (typeof src === 'string') {
    raw = [src];
  } else if (src && typeof src === 'object') {
    // NodeBindings 行优先 sb_cdn_entries，Nodes 行用 cdn_entries；再回退旧字段
    if (Array.isArray(src.cdn_entries) && src.cdn_entries.length) raw = src.cdn_entries;
    else if (Array.isArray(src.sb_cdn_entries) && src.sb_cdn_entries.length) raw = src.sb_cdn_entries;
    else if (Array.isArray(src.cdn_domains) && src.cdn_domains.length) raw = src.cdn_domains;
    else if (Array.isArray(src.sb_cdn_domains) && src.sb_cdn_domains.length) raw = src.sb_cdn_domains;
    else if (src.cdn_domain) raw = [src.cdn_domain];
    else if (src.sb_cdn) raw = [src.sb_cdn];
  }
  const seen = new Set();
  const out = [];
  for (const item of raw) {
    const obj = item && typeof item === 'object' ? item : { domain: item };
    const domain = normalizeCdnDomain(obj.domain);
    if (!domain || seen.has(domain)) continue;
    seen.add(domain);
    out.push({
      name: String(obj.name == null ? '' : obj.name).trim(),
      domain,
      enabled: normalizeEnabled(obj.enabled),
      prefer_group: String(obj.prefer_group == null ? '' : obj.prefer_group).trim(),
    });
    if (out.length >= CDN_MAX) break;
  }
  return out;
}

/**
 * 把表单行整理成提交给后端的 cdn_entries：
 * - 跳过空域名行；停用条目也提交（enabled=false，保留配置）；
 * - 域名重复 / 超过 8 条 / 名称过长抛错（由调用方提示）；
 * - 空名称自动补「CDN1、CDN2…」；不再写入/校验 primary（所有启用条目均生成链接）。
 */
export function collectCdnEntries(list) {
  const arr = Array.isArray(list) ? list : [];
  const seen = new Set();
  const out = [];
  for (const item of arr) {
    if (!item || typeof item !== 'object') continue;
    const domain = normalizeCdnDomain(item.domain);
    if (!domain) continue;
    if (seen.has(domain)) throw new Error(`CDN 域名重复：${domain}`);
    seen.add(domain);
    const rawName = String(item.name == null ? '' : item.name).trim();
    if (rawName.length > CDN_NAME_MAX) throw new Error(`CDN 名称过长（最多 ${CDN_NAME_MAX} 字符）：${rawName}`);
    const name = rawName || `CDN${out.length + 1}`;
    out.push({
      name,
      domain,
      enabled: normalizeEnabled(item.enabled),
      prefer_group: String(item.prefer_group == null ? '' : item.prefer_group).trim(),
    });
    if (out.length > CDN_MAX) throw new Error(`最多只能配置 ${CDN_MAX} 个 CDN`);
  }
  return out;
}

/**
 * 节点列表展示用摘要：按启用数量显示（取第一个启用条目的名称），
 * 如 2 个启用、1 个停用 → `CDN CF +1`；全部停用 → `CDN 已停用`。
 */
export function cdnSummary(src) {
  const entries = normalizeCdnEntries(src);
  if (!entries.length) return null;
  const enabled = entries.filter((e) => e.enabled);
  const total = entries.length;
  if (!enabled.length) {
    return {
      label: '已停用',
      text: 'CDN 已停用',
      extra: 0,
      enabledCount: 0,
      total,
      tooltip: entries.map((e) => `已停用 ${e.name || '默认'} ${e.domain}`).join('\n'),
      entries,
    };
  }
  const first = enabled[0];
  const extra = enabled.length - 1;
  return {
    label: first.name || first.domain,
    text: `CDN ${first.name || first.domain}${extra > 0 ? ` +${extra}` : ''}`,
    extra,
    enabledCount: enabled.length,
    total,
    tooltip: entries
      .map((e) => `${e.enabled ? '启用' : '已停用'} ${e.name || '默认'} ${e.domain}`)
      .join('\n'),
    entries,
  };
}

// CDN 品牌色（用于列表标签）；未知名称使用中性色。
export const CDN_BRAND_COLORS = {
  Cloudflare: '#F38020',
  CF: '#F38020',
  ESA: '#FF6A00',
  EO: '#0052D9',
  EdgeOne: '#0052D9',
  CloudFront: '#FF9900',
  Fastly: '#FF282D',
};

/** 旧名称/别名统一成展示名（不修改存储值）。 */
export function cdnDisplayName(name, domain) {
  const n = String(name || '').trim();
  const key = n.toUpperCase();
  if (key === 'CF') return 'Cloudflare';
  if (key === 'EO' || key === 'EDGEONE') return 'EO';
  if (key === 'ESA') return 'ESA';
  if (n) return n;
  return domain || 'CDN';
}

/** 节点列表用标签：每个已配置 CDN 一个标签，品牌色；停用标签灰色。 */
export function cdnTagItems(src) {
  return normalizeCdnEntries(src).map((e) => {
    const label = cdnDisplayName(e.name, e.domain);
    const color = CDN_BRAND_COLORS[e.name] || CDN_BRAND_COLORS[label] || '#64748b';
    return {
      key: e.domain,
      label,
      domain: e.domain,
      name: e.name,
      enabled: e.enabled,
      color: e.enabled ? color : '#94a3b8',
      tooltip: `${label} · ${e.domain} · ${e.enabled ? '启用' : '已停用'}`,
    };
  });
}

/**
 * 表单字段：多 CDN 动态列表（名称 + 启用开关 + 域名 + 删除）。
 * 名称严格下拉（Select，不可手填；旧值自动作为临时选项保留）；域名手动填写（Input，不下拉）。
 * 用法：在 Form 内 <CdnDomainsField name="cdn_entries" />
 */
export default function CdnDomainsField({
  name = 'cdn_entries',
  label = 'CDN 域名',
  disabled = false,
  nameOptions,
}) {
  const form = Form.useFormInstance();
  const list = Form.useWatch(name, form) || [];
  const enabledCount = list.filter((x) => x && x.enabled !== false).length;
  // 名称预设（严格下拉）；域名由用户手动填写
  const nameOptionList = (Array.isArray(nameOptions) && nameOptions.length ? nameOptions : CDN_NAME_PRESETS).map((o) =>
    typeof o === 'string' ? { value: o, label: o } : o
  );

  const applyList = (next) => {
    form.setFieldValue(name, next);
    form.validateFields([name]).catch(() => {});
  };

  const handleEnabledChange = (index, checked) => {
    const current = form.getFieldValue(name) || [];
    const next = current.map((it, i) => ({
      ...(it || {}),
      enabled: i === index ? !!checked : normalizeEnabled(it && it.enabled),
    }));
    applyList(next);
  };

  const handleRemove = (index) => {
    const current = form.getFieldValue(name) || [];
    applyList(current.filter((_, i) => i !== index));
  };

  return (
    <div style={{ width: '100%' }}>
      <div style={{ fontSize: 12, color: '#64748b', marginBottom: 6, display: 'flex', justifyContent: 'space-between', gap: 8 }}>
        <span>{label}（每个启用的 CDN 生成一条订阅链接，可同时启用多个；停用后不生成链接）</span>
        <span className="pp-muted">{list.length}/{CDN_MAX}</span>
      </div>
      <Form.List
        name={name}
        rules={[
          {
            validator: async (_, value) => {
              if ((value || []).length > CDN_MAX) throw new Error(`最多只能配置 ${CDN_MAX} 个 CDN`);
            },
          },
        ]}
      >
        {(fields, { add, remove }, { errors }) => (
          <>
            {fields.length ? (
              <div style={{ display: 'flex', gap: 8, fontSize: 11.5, color: '#94a3b8', padding: '0 2px 4px' }}>
                <span style={{ width: 110 }}>名称（Cloudflare/ESA/EO）</span>
                <span style={{ width: 64, textAlign: 'center' }}>启用</span>
                <span style={{ flex: 1 }}>域名</span>
                <span style={{ width: 28 }} />
              </div>
            ) : null}
            {fields.map((field, index) => {
              const row = list[index] || {};
              const rowEnabled = row.enabled !== false;
              // 旧值不在预设中（如历史手填名称）→ 作为临时选项加入，避免编辑时值丢失
              const rowName = String(row.name || '').trim();
              const nameOptionsForRow = rowName && !nameOptionList.some((o) => o && o.value === rowName)
                ? [...nameOptionList, { value: rowName, label: `${rowName}（旧名称）` }]
                : nameOptionList;
              return (
                <div
                  key={field.key}
                  style={{ display: 'flex', gap: 8, alignItems: 'flex-start', marginBottom: 4, opacity: rowEnabled ? 1 : 0.62 }}
                >
                  <Form.Item
                    name={[field.name, 'name']}
                    rules={[
                      {
                        validator: (_, v) => {
                          const err = validateCdnName(v);
                          return err ? Promise.reject(new Error(err)) : Promise.resolve();
                        },
                      },
                    ]}
                    style={{ width: 110, marginBottom: 0 }}
                  >
                    <Select
                      options={nameOptionsForRow}
                      placeholder="Cloudflare / ESA / EO"
                      disabled={disabled}
                      showSearch
                      optionFilterProp="label"
                    />
                  </Form.Item>
                  <Form.Item
                    name={[field.name, 'enabled']}
                    valuePropName="checked"
                    style={{ width: 64, marginBottom: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 2, paddingTop: 4 }}
                  >
                    <Switch size="small" disabled={disabled} onChange={(checked) => handleEnabledChange(index, checked)} />
                  </Form.Item>
                  <Form.Item
                    name={[field.name, 'domain']}
                    rules={[
                      {
                        validator: (_, v) => {
                          const err = validateCdnDomain(v);
                          return err ? Promise.reject(new Error(err)) : Promise.resolve();
                        },
                      },
                      {
                        validator: (_, v) => {
                          const d = normalizeCdnDomain(v);
                          if (!d) return Promise.resolve();
                          const all = form.getFieldValue(name) || [];
                          const dup = all.some(
                            (it, i) => i !== index && normalizeCdnDomain(it && it.domain) === d
                          );
                          return dup ? Promise.reject(new Error('CDN 域名重复')) : Promise.resolve();
                        },
                      },
                    ]}
                    style={{ flex: 1, marginBottom: 0 }}
                  >
                    <Input
                      placeholder="cdn.example.com"
                      disabled={disabled}
                    />
                  </Form.Item>
                  <div style={{ width: 28, paddingTop: 2 }}>
                    <Button
                      type="text"
                      danger
                      size="small"
                      icon={<DeleteOutlined />}
                      disabled={disabled}
                      aria-label="删除 CDN"
                      onClick={() => handleRemove(index)}
                    />
                  </div>
                </div>
              );
            })}
            {fields.length > 0 && enabledCount === 0 ? (
              <div style={{ fontSize: 12, color: '#f59e0b', margin: '2px 0 6px' }}>
                所有 CDN 已停用，不会生成 CDN 链接
              </div>
            ) : null}
            <Button
              type="dashed"
              block
              icon={<PlusOutlined />}
              disabled={disabled || fields.length >= CDN_MAX}
              onClick={() => add({ name: '', domain: '', enabled: true, prefer_group: '' })}
            >
              添加 CDN（{fields.length}/{CDN_MAX}）
            </Button>
            <Form.ErrorList errors={errors} />
          </>
        )}
      </Form.List>
      <div style={{ fontSize: 11.5, color: '#94a3b8', lineHeight: 1.7, marginTop: 4 }}>
        名称从下拉选择（Cloudflare/EO/ESA/CloudFront 等，不可手填）用于后台识别和节点命名；域名需手动填写，并在对应 CDN/Tunnel 侧配置回源到本节点；最多 8 条；
        停用的 CDN 不生成订阅链接，可同时启用多个。
      </div>
    </div>
  );
}