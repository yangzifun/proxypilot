import React from 'react';
import { Button, Form, Input, Radio, Switch } from 'antd';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';

/**
 * 多 CDN 编辑组件（NodeBindings / Nodes 共用）。
 *
 * 后端契约为节点上的对象数组：
 *   cdn_entries: [{ name, domain, primary, enabled, prefer_group }]
 * 兼容旧字段：sb_cdn_entries（NodeBindings 行）/ cdn_domains / sb_cdn_domains /
 * cdn_domain / sb_cdn（单字符串）。
 *
 * 规则：
 *   - enabled 缺省 true；停用的 CDN 不生成订阅链接（配置保留）；
 *   - 主 CDN 只能在「启用」条目中唯一；主 CDN 被停用时自动回退第一个启用条目；
 *   - 全部停用时给出明确提示。
 *
 * 这里提供的纯函数（normalizeCdnEntries / collectCdnEntries / cdnSummary）
 * 同时用于回填、提交与列表展示，便于单测。
 */

export const CDN_MAX = 8;
export const CDN_NAME_MAX = 24;

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

/** 只在启用条目中选出唯一主 CDN；没有标记时第一个启用条目为主。 */
function normalizePrimary(out) {
  let primaryIdx = -1;
  for (let i = 0; i < out.length; i++) {
    if (out[i].enabled && out[i].primary) {
      primaryIdx = i;
      break;
    }
  }
  if (primaryIdx < 0) primaryIdx = out.findIndex((e) => e.enabled);
  out.forEach((e, i) => {
    e.primary = e.enabled && i === primaryIdx;
  });
  return out;
}

/**
 * 把节点行 / cdn_entries 数组 / sb_cdn_entries / 旧域名数组 / 单字符串统一成表单行。
 * 规则：去空域名、域名去重、最多 8 条、enabled 缺省 true、主 CDN 只在启用条目中唯一。
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
      primary: !!obj.primary,
      enabled: normalizeEnabled(obj.enabled),
      prefer_group: String(obj.prefer_group == null ? '' : obj.prefer_group).trim(),
    });
    if (out.length >= CDN_MAX) break;
  }
  return normalizePrimary(out);
}

/**
 * 把表单行整理成提交给后端的 cdn_entries：
 * - 跳过空域名行；停用条目也提交（保留配置），但 primary=false；
 * - 域名重复 / 超过 8 条 / 名称过长抛错（由调用方提示）；
 * - 空名称自动补「CDN1、CDN2…」；主 CDN 只在启用条目中唯一，且必须启用。
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
      primary: !!item.primary,
      enabled: normalizeEnabled(item.enabled),
      prefer_group: String(item.prefer_group == null ? '' : item.prefer_group).trim(),
    });
    if (out.length > CDN_MAX) throw new Error(`最多只能配置 ${CDN_MAX} 个 CDN`);
  }
  normalizePrimary(out);
  return out;
}

/** 节点列表展示用摘要：启用主 CDN 名称 + 其余启用数量 + Tooltip（标注主/备、启用/停用）。 */
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
  const primary = enabled.find((e) => e.primary) || enabled[0];
  const extra = enabled.length - 1;
  return {
    label: primary.name || primary.domain,
    text: `CDN ${primary.name || primary.domain}${extra > 0 ? ` +${extra}` : ''}`,
    extra,
    enabledCount: enabled.length,
    total,
    tooltip: entries
      .map((e) => `${e.enabled ? (e.primary ? '主·启用' : '备·启用') : '已停用'} ${e.name || '默认'} ${e.domain}`)
      .join('\n'),
    entries,
  };
}

/**
 * 表单字段：多 CDN 动态列表（名称 + 启用开关 + 域名 + 主/备 + 删除）。
 * 用法：在 Form 内 <CdnDomainsField name="cdn_entries" />
 */
export default function CdnDomainsField({ name = 'cdn_entries', label = 'CDN 域名', disabled = false }) {
  const form = Form.useFormInstance();
  const list = Form.useWatch(name, form) || [];
  const enabledCount = list.filter((x) => x && x.enabled !== false).length;
  const primaryIndex = list.findIndex((x) => x && x.enabled !== false && x.primary);

  const applyList = (next) => {
    form.setFieldValue(name, next);
    form.validateFields([name]).catch(() => {});
  };

  const setPrimary = (index) => {
    const current = form.getFieldValue(name) || [];
    applyList(
      current.map((it, i) => {
        const row = { ...(it || {}), enabled: normalizeEnabled(it && it.enabled) };
        row.primary = i === index && row.enabled;
        return row;
      })
    );
  };

  const handleEnabledChange = (index, checked) => {
    const current = form.getFieldValue(name) || [];
    const next = current.map((it, i) => {
      const row = { ...(it || {}), enabled: i === index ? !!checked : normalizeEnabled(it && it.enabled) };
      if (i === index) row.enabled = !!checked;
      return row;
    });
    // 关闭当前主 CDN → 自动回退到第一个启用条目；若没有启用条目则主 CDN 置空
    let primaryIdx = next.findIndex((x) => x.primary && x.enabled);
    if (!checked && primaryIdx === index) {
      primaryIdx = next.findIndex((x, i) => i !== index && x.enabled);
    }
    if (primaryIdx < 0) primaryIdx = next.findIndex((x) => x.enabled);
    next.forEach((x, i) => {
      x.primary = x.enabled && i === primaryIdx;
    });
    applyList(next);
  };

  const handleRemove = (index) => {
    const current = form.getFieldValue(name) || [];
    const next = current.filter((_, i) => i !== index);
    let primaryIdx = next.findIndex((x) => x.primary && x.enabled);
    if (primaryIdx < 0) primaryIdx = next.findIndex((x) => x.enabled);
    next.forEach((x, i) => {
      x.primary = x.enabled && i === primaryIdx;
    });
    applyList(next);
  };

  return (
    <div style={{ width: '100%' }}>
      <div style={{ fontSize: 12, color: '#64748b', marginBottom: 6, display: 'flex', justifyContent: 'space-between', gap: 8 }}>
        <span>{label}（每个 CDN 一条订阅链接，主 CDN 优先；停用后不生成链接）</span>
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
                <span style={{ width: 110 }}>名称（如 CF/ESA/EO）</span>
                <span style={{ width: 56, textAlign: 'center' }}>启用</span>
                <span style={{ flex: 1 }}>域名</span>
                <span style={{ width: 118, textAlign: 'center' }}>主 / 备</span>
                <span style={{ width: 28 }} />
              </div>
            ) : null}
            {fields.map((field, index) => {
              const row = list[index] || {};
              const rowEnabled = row.enabled !== false;
              const isPrimary = primaryIndex === index;
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
                    <Input placeholder="CF / ESA / EO" maxLength={CDN_NAME_MAX} disabled={disabled} />
                  </Form.Item>
                  <Form.Item
                    name={[field.name, 'enabled']}
                    valuePropName="checked"
                    style={{ width: 56, marginBottom: 0, display: 'flex', justifyContent: 'center', paddingTop: 4 }}
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
                    <Input placeholder="cdn.example.com" disabled={disabled} />
                  </Form.Item>
                  <div style={{ width: 118, textAlign: 'center', paddingTop: 5, display: 'flex', gap: 4, justifyContent: 'center', alignItems: 'center' }}>
                    <Radio checked={isPrimary} disabled={disabled || !rowEnabled} onChange={() => setPrimary(index)}>
                      主
                    </Radio>
                    {!rowEnabled ? (
                      <span style={{ fontSize: 11, color: '#f59e0b', whiteSpace: 'nowrap' }}>已停用</span>
                    ) : isPrimary ? null : (
                      <span style={{ fontSize: 11, color: '#94a3b8' }}>备</span>
                    )}
                  </div>
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
              onClick={() => add({ name: '', domain: '', primary: fields.length === 0, enabled: true, prefer_group: '' })}
            >
              添加 CDN（{fields.length}/{CDN_MAX}）
            </Button>
            <Form.ErrorList errors={errors} />
          </>
        )}
      </Form.List>
      <div style={{ fontSize: 11.5, color: '#94a3b8', lineHeight: 1.7, marginTop: 4 }}>
        名称仅用于后台识别和节点命名（如 CF/ESA/EO）；域名需在对应 CDN/Tunnel 侧配置回源到本节点；最多 8 条；
        主 CDN 只能有一个且必须启用，停用的 CDN 不会生成订阅链接。
      </div>
    </div>
  );
}