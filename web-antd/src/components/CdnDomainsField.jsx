import React from 'react';
import { Button, Form, Input, Radio } from 'antd';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';

/**
 * 多 CDN 编辑组件（NodeBindings / Nodes 共用）。
 *
 * 后端契约为节点上的对象数组：
 *   cdn_entries: [{ name, domain, primary, prefer_group }]
 * 兼容旧字段：cdn_domains（字符串数组）/ cdn_domain（单字符串）。
 *
 * 这里同时提供纯函数（normalizeCdnEntries / collectCdnEntries / cdnSummary）
 * 便于回填、提交与列表展示，也便于单测。
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

/**
 * 把节点行 / cdn_entries 数组 / cdn_domains 数组 / 单字符串统一成表单行。
 * 规则：去空域名、域名去重、最多 8 条、primary 唯一（没有标记时第一行为主）。
 */
export function normalizeCdnEntries(src) {
  if (!src) return [];
  let raw = [];
  if (Array.isArray(src)) {
    raw = src;
  } else if (typeof src === 'string') {
    raw = [src];
  } else if (src && typeof src === 'object') {
    if (Array.isArray(src.cdn_entries) && src.cdn_entries.length) raw = src.cdn_entries;
    else if (Array.isArray(src.cdn_domains) && src.cdn_domains.length) raw = src.cdn_domains;
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
      prefer_group: String(obj.prefer_group == null ? '' : obj.prefer_group).trim(),
    });
    if (out.length >= CDN_MAX) break;
  }
  let hasPrimary = false;
  for (const e of out) {
    if (e.primary && !hasPrimary) hasPrimary = true;
    else e.primary = false;
  }
  if (out.length && !hasPrimary) out[0].primary = true;
  return out;
}

/**
 * 把表单行整理成提交给后端的 cdn_entries：
 * - 跳过空域名行；
 * - 域名重复 / 超过 8 条 / 名称过长抛错（由调用方提示）；
 * - 空名称自动补「CDN1、CDN2…」，primary 唯一（无标记时第一行为主）。
 */
export function collectCdnEntries(list) {
  const arr = Array.isArray(list) ? list : [];
  const seen = new Set();
  const out = [];
  let primarySet = false;
  for (const item of arr) {
    if (!item || typeof item !== 'object') continue;
    const domain = normalizeCdnDomain(item.domain);
    if (!domain) continue;
    if (seen.has(domain)) throw new Error(`CDN 域名重复：${domain}`);
    seen.add(domain);
    const rawName = String(item.name == null ? '' : item.name).trim();
    if (rawName.length > CDN_NAME_MAX) throw new Error(`CDN 名称过长（最多 ${CDN_NAME_MAX} 字符）：${rawName}`);
    const name = rawName || `CDN${out.length + 1}`;
    const primary = !primarySet && !!item.primary;
    if (primary) primarySet = true;
    out.push({
      name,
      domain,
      primary,
      prefer_group: String(item.prefer_group == null ? '' : item.prefer_group).trim(),
    });
    if (out.length > CDN_MAX) throw new Error(`最多只能配置 ${CDN_MAX} 个 CDN`);
  }
  if (out.length && !primarySet) out[0].primary = true;
  return out;
}

/** 节点列表展示用摘要：主 CDN 名称/域名 + 数量 + Tooltip 文本。 */
export function cdnSummary(src) {
  const entries = normalizeCdnEntries(src);
  if (!entries.length) return null;
  const primary = entries.find((e) => e.primary) || entries[0];
  return {
    label: primary.name || primary.domain,
    extra: Math.max(0, entries.length - 1),
    text: `CDN ${primary.name || primary.domain}${entries.length > 1 ? ` +${entries.length - 1}` : ''}`,
    tooltip: entries
      .map((e) => `${e.primary ? '★ ' : ''}${e.name || '默认'} ${e.domain}`)
      .join('\n'),
    entries,
  };
}

/**
 * 表单字段：多 CDN 动态列表（名称 + 域名 + 主 CDN 单选 + 删除）。
 * 用法：在 Form 内 <CdnDomainsField name="cdn_entries" />
 */
export default function CdnDomainsField({ name = 'cdn_entries', label = 'CDN 域名', disabled = false }) {
  const form = Form.useFormInstance();
  const list = Form.useWatch(name, form) || [];
  const primaryIndex = list.findIndex((x) => x && x.primary);

  const setPrimary = (index) => {
    const current = form.getFieldValue(name) || [];
    form.setFieldValue(
      name,
      current.map((it, i) => ({ ...(it || {}), primary: i === index }))
    );
    // 主 CDN 变更后重新校验域名（避免旧重复错误残留）
    form.validateFields([name]).catch(() => {});
  };

  const handleRemove = (index) => {
    const current = form.getFieldValue(name) || [];
    const next = current.filter((_, i) => i !== index);
    if (next.length && !next.some((x) => x && x.primary)) {
      next[0] = { ...(next[0] || {}), primary: true };
    }
    form.setFieldValue(name, next);
    form.validateFields([name]).catch(() => {});
  };

  return (
    <div style={{ width: '100%' }}>
      <div style={{ fontSize: 12, color: '#64748b', marginBottom: 6, display: 'flex', justifyContent: 'space-between', gap: 8 }}>
        <span>{label}（每个 CDN 一条订阅链接，主 CDN 优先）</span>
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
                <span style={{ width: 130 }}>名称（如 CF/ESA/EO）</span>
                <span style={{ flex: 1 }}>域名</span>
                <span style={{ width: 88, textAlign: 'center' }}>主 CDN</span>
                <span style={{ width: 28 }} />
              </div>
            ) : null}
            {fields.map((field, index) => (
              <div key={field.key} style={{ display: 'flex', gap: 8, alignItems: 'flex-start', marginBottom: 4 }}>
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
                  style={{ width: 130, marginBottom: 0 }}
                >
                  <Input placeholder="CF / ESA / EO" maxLength={CDN_NAME_MAX} disabled={disabled} />
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
                <div style={{ width: 88, textAlign: 'center', paddingTop: 6 }}>
                  <Radio checked={primaryIndex === index} disabled={disabled} onChange={() => setPrimary(index)}>
                    主
                  </Radio>
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
            ))}
            <Button
              type="dashed"
              block
              icon={<PlusOutlined />}
              disabled={disabled || fields.length >= CDN_MAX}
              onClick={() => add({ name: '', domain: '', primary: fields.length === 0, prefer_group: '' })}
            >
              添加 CDN（{fields.length}/{CDN_MAX}）
            </Button>
            <Form.ErrorList errors={errors} />
          </>
        )}
      </Form.List>
      <div style={{ fontSize: 11.5, color: '#94a3b8', lineHeight: 1.7, marginTop: 4 }}>
        名称仅用于后台识别和节点命名（如 CF/ESA/EO）；域名需在对应 CDN/Tunnel 侧配置回源到本节点；最多 8 条，主 CDN 只允许一个。
      </div>
    </div>
  );
}