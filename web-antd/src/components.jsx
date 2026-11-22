/* =================================================================
 *  共享展示组件（卡片 / 指标 / 状态 / 复制 / 错误态 / JSON 预览）
 * ================================================================= */
import React, { useMemo, useState } from 'react';
import { Alert, App as AntdApp, Button, Card, Empty, Skeleton, Spin, Tooltip } from 'antd';
import { CheckOutlined, CopyOutlined, RedoOutlined } from '@ant-design/icons';
import { Sparkline } from './charts';

/* ---------------- 卡片 ---------------- */

/**
 * 表格「操作」列的图标按钮：只显示图标，文字放进 Tooltip / aria-label
 */
export function IconAction({ title, icon, onClick, danger, disabled, href }) {
  return (
    <Tooltip title={title}>
      <Button
        type="text"
        size="small"
        danger={danger}
        icon={icon}
        onClick={onClick}
        disabled={disabled}
        href={href}
        aria-label={title}
      />
    </Tooltip>
  );
}

export function PageCard({ title, extra, children, className = '', bodyStyle, size, loading }) {
  return (
    <Card
      className={`pp-card ${className}`}
      title={title}
      extra={extra}
      bodyStyle={bodyStyle}
      size={size}
      style={{
        background: 'var(--pp-surface)',
        borderColor: 'var(--pp-border)',
        boxShadow: 'var(--pp-shadow-md)',
        borderRadius: 'var(--pp-radius-lg)',
        color: 'var(--pp-text)',
      }}
    >
      {loading ? (
        <div className="pp-loading-wrap">
          <Spin size="small" className="pp-loading-spin" />
          <Skeleton active paragraph={{ rows: 5 }} title={false} />
        </div>
      ) : children}
    </Card>
  );
}

/* ---------------- 指标卡 ---------------- */

export function StatCard({ icon, label, value, unit, hint, accent, bg, spark, onClick }) {
  const accentColor = accent || 'var(--pp-primary)';
  const iconBg = bg || (accent ? hexAlpha(accent, 0.12) : 'var(--pp-primary-soft)');
  return (
    <Card
      className="pp-card"
      hoverable={!!onClick}
      onClick={onClick}
      style={{
        cursor: onClick ? 'pointer' : undefined,
        background: 'var(--pp-surface)',
        borderColor: 'var(--pp-border)',
        boxShadow: 'var(--pp-shadow-md)',
        borderRadius: 'var(--pp-radius-lg)',
        color: 'var(--pp-text)',
      }}
    >
      <div className="pp-stat">
        <div className="pp-stat-icon" style={{ background: iconBg, color: accentColor }}>
          {icon}
        </div>
        <div className="pp-stat-main">
          <div className="pp-stat-label" style={{ color: 'var(--pp-text-secondary)' }}>{label}</div>
          <div className="pp-stat-value" style={{ color: 'var(--pp-text)' }}>
            {value}
            {unit ? <small>{unit}</small> : null}
          </div>
          {hint ? <div className="pp-stat-hint">{hint}</div> : null}
        </div>
      </div>
      {spark && spark.length > 1 ? (
        <div className="pp-stat-spark">
          <Sparkline values={spark} color={accentColor} height={30} />
        </div>
      ) : null}
    </Card>
  );
}

export function MiniStat({ label, value, unit, accent }) {
  return (
    <div className="pp-kv">
      <span className="pp-kv-k">{label}</span>
      <span className="pp-kv-v" style={accent ? { color: accent } : undefined}>
        {value}
        {unit ? <span style={{ fontSize: 12, color: 'var(--pp-text-tertiary)', marginLeft: 2 }}>{unit}</span> : null}
      </span>
    </div>
  );
}

function hexAlpha(hex, alpha) {
  const raw = String(hex || '').trim();
  if (raw.startsWith('var(')) {
    return `color-mix(in srgb, ${raw} ${Math.round(alpha * 100)}%, transparent)`;
  }
  const h = raw.replace('#', '');
  if (h.length !== 6) {
    return `color-mix(in srgb, var(--pp-primary) ${Math.round(alpha * 100)}%, transparent)`;
  }
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `color-mix(in srgb, #${h} ${Math.round(alpha * 100)}%, transparent)`;
}

/* ---------------- 状态 ---------------- */

const STATUS_MAP = {
  online: ['online', '在线'],
  offline: ['offline', '离线'],
  unknown: ['unknown', '未知'],
  active: ['online', '正常'],
  suspended: ['warn', '暂停'],
  inactive: ['unknown', '停用'],
  success: ['online', '成功'],
  failed: ['offline', '失败'],
  error: ['offline', '异常'],
  pending: ['warn', '等待中'],
  ok: ['online', '正常'],
  notinstalled: ['unknown', '未安装'],
  'not-installed': ['unknown', '未安装'],
  active_state: ['online', '运行'],
};

export function StatusPill({ status, children, dotOnly }) {
  const key = String(status || '').trim().toLowerCase();
  const [cls, text] = STATUS_MAP[key] || ['unknown', children || status || '—'];
  if (dotOnly) return <i className={`pp-dot ${cls}`} />;
  return (
    <span className="pp-nowrap">
      <i className={`pp-dot ${cls}`} />
      {children || text}
    </span>
  );
}

/* ---------------- 复制 ---------------- */

export function CopyText({ value, display, tip = '点击复制' }) {
  const { message } = AntdApp.useApp();
  const [done, setDone] = useState(false);
  const text = String(value == null ? '' : value);
  const copy = async (e) => {
    e.stopPropagation();
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
      } else {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      }
      setDone(true);
      message.success('已复制到剪贴板');
      setTimeout(() => setDone(false), 1400);
    } catch (err) {
      message.error('复制失败，请手动选择复制');
    }
  };
  return (
    <Tooltip title={tip}>
      <span
        className="pp-mono"
        onClick={copy}
        style={{ cursor: 'pointer', display: 'inline-flex', alignItems: 'center', gap: 6 }}
      >
        {display !== undefined ? display : text}
        <span style={{ color: done ? 'var(--pp-success)' : 'var(--pp-text-tertiary)', fontSize: 12 }}>
          {done ? <CheckOutlined /> : <CopyOutlined />}
        </span>
      </span>
    </Tooltip>
  );
}

/* ---------------- 空 / 错 / 载入 ---------------- */

export function EmptyBlock({ text = '暂无数据', children }) {
  return (
    <div className="pp-empty">
      <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span style={{ color: 'var(--pp-text-tertiary)' }}>{text}</span>}>
        {children}
      </Empty>
    </div>
  );
}

export function ErrorBlock({ error, onRetry, title = '加载失败' }) {
  const msg = (error && (error.message || String(error))) || '未知错误';
  return (
    <Alert
      type="error"
      showIcon
      message={title}
      description={msg}
      action={
        onRetry ? (
          <Button size="small" icon={<RedoOutlined />} onClick={onRetry}>
            重试
          </Button>
        ) : null
      }
      style={{ borderRadius: 12 }}
    />
  );
}

/* ---------------- JSON 预览 ---------------- */

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function highlightJson(text) {
  const esc = escapeHtml(text);
  return esc.replace(
    /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
    (m, str, colon, bool, num) => {
      if (str) {
        return colon ? `<span class="k">${str}</span>${colon}` : `<span class="s">${str}</span>`;
      }
      if (bool) return `<span class="b">${bool}</span>`;
      if (num) return `<span class="n">${num}</span>`;
      return m;
    }
  );
}

export function JsonView({ value, maxHeight = 420 }) {
  const text = useMemo(() => {
    if (typeof value === 'string') return value;
    try {
      return JSON.stringify(value, null, 2);
    } catch (e) {
      return String(value);
    }
  }, [value]);
  const html = useMemo(() => highlightJson(text), [text]);
  return (
    <pre className="pp-json" style={{ maxHeight }} dangerouslySetInnerHTML={{ __html: html }} />
  );
}

/* ---------------- 工具栏 ---------------- */

export function Toolbar({ left, right }) {
  return (
    <div className="pp-toolbar" style={{ color: 'var(--pp-text-secondary)' }}>
      {left}
      <div className="pp-toolbar-right">{right}</div>
    </div>
  );
}

/* ---------------- 通用卡片（T1 主题基础设施，后续页面复用到） ---------------- */

/** 数据卡片：表格/列表容器（与 PageCard 同构，语义化命名） */
export function DataCard({ title, extra, children, className = '', bodyStyle, loading, size }) {
  return (
    <PageCard
      title={title}
      extra={extra}
      className={`pp-data-card ${className}`}
      bodyStyle={bodyStyle}
      loading={loading}
      size={size}
    >
      {children}
    </PageCard>
  );
}

/** 表单卡片：标题/描述 + 内容 + 可选底部操作区 */
export function FormCard({ title, description, extra, children, footer, className = '' }) {
  return (
    <section
      className={`pp-form-card ${className}`}
      style={{
        background: 'var(--pp-surface)',
        border: '1px solid var(--pp-border)',
        borderRadius: 'var(--pp-radius-md)',
        boxShadow: 'var(--pp-shadow-sm)',
        color: 'var(--pp-text)',
        padding: 16,
        marginBottom: 16,
      }}
    >
      {title || description || extra ? (
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8, marginBottom: 12 }}>
          <div style={{ minWidth: 0, flex: 1 }}>
            {title ? <div className="pp-section-title">{title}</div> : null}
            {description ? <div className="pp-muted" style={{ fontSize: 12, marginTop: 2 }}>{description}</div> : null}
          </div>
          {extra ? <div>{extra}</div> : null}
        </div>
      ) : null}
      {children}
      {footer ? (
        <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--pp-border-soft)' }}>{footer}</div>
      ) : null}
    </section>
  );
}

/** 区块卡片：最小分区块（标题/描述/右侧操作） */
export function SectionCard({ title, description, extra, children, className = '' }) {
  return (
    <section
      className={`pp-section-card ${className}`}
      style={{
        background: 'var(--pp-surface)',
        border: '1px solid var(--pp-border)',
        borderRadius: 'var(--pp-radius-md)',
        boxShadow: 'var(--pp-shadow-sm)',
        color: 'var(--pp-text)',
        padding: 16,
        marginBottom: 16,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: children ? 12 : 0 }}>
        <div style={{ minWidth: 0, flex: 1 }}>
          {title ? <span className="pp-section-title">{title}</span> : null}
          {description ? <div className="pp-muted" style={{ fontSize: 12, marginTop: 2 }}>{description}</div> : null}
        </div>
        {extra ? <div>{extra}</div> : null}
      </div>
      {children}
    </section>
  );
}

