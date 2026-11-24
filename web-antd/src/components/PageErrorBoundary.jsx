/* =================================================================
 *  PageErrorBoundary · 页面级错误边界
 *  · 捕获路由内容渲染/生命周期异常，避免整页白屏
 *  · 展示错误摘要 + 重试（重置错误状态）/ 重新加载 / 返回概览
 * ================================================================= */
import React from 'react';
import { Button, Result, Space } from 'antd';
import { reportClientError } from '../clientError';
import { isChunkLoadError, recoverChunkLoad } from '../lazyRoute';

function errorSummary(error) {
  if (!error) return '未知错误';
  const msg = error.message || error.msg || error;
  return String(msg || '未知错误');
}

function errorDetail(error, info) {
  const lines = [];
  if (error && error.stack) lines.push(...String(error.stack).split('\n').slice(0, 12));
  if (info && info.componentStack) {
    lines.push('组件栈：');
    lines.push(...String(info.componentStack).trim().split('\n').slice(0, 12));
  }
  return lines.join('\n');
}

export default class PageErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null, info: null, chunkError: false, autoReloading: false };
    this.handleRetry = this.handleRetry.bind(this);
    this.handleReload = this.handleReload.bind(this);
    this.handleHome = this.handleHome.bind(this);
  }

  static getDerivedStateFromError(error) {
    return { error, chunkError: isChunkLoadError(error) };
  }

  componentDidCatch(error, info) {
    const chunkError = isChunkLoadError(error);
    // 旧 chunk 404：第一次自动刷新一次；10 秒内已刷新过则不再刷新，进入友好提示。
    const autoReloading = chunkError ? recoverChunkLoad() : false;
    this.setState({ error, info, chunkError, autoReloading });
    // 只打印摘要，避免控制台被大段堆栈刷屏
    try {
      // eslint-disable-next-line no-console
      console.error('[PageErrorBoundary] 页面渲染异常：', errorSummary(error));
    } catch (e) {
      /* ignore */
    }
    // 上报到后端（静默、去重、限流；mode 由 boot/路径推断）
    try {
      reportClientError({
        message: errorSummary(error),
        stack: (error && error.stack) || (info && info.componentStack) || '',
        source: 'boundary',
      });
    } catch (e) {
      /* ignore */
    }
    if (typeof this.props.onError === 'function') {
      try {
        this.props.onError(error, info);
      } catch (e) {
        /* ignore */
      }
    }
  }

  handleRetry() {
    this.setState({ error: null, info: null, chunkError: false, autoReloading: false });
    if (typeof this.props.onReset === 'function') {
      try {
        this.props.onReset();
      } catch (e) {
        /* ignore */
      }
    }
  }

  handleReload() {
    try {
      window.location.reload();
    } catch (e) {
      /* ignore */
    }
  }

  handleHome() {
    if (typeof this.props.onHome === 'function') {
      try {
        this.props.onHome();
        return;
      } catch (e) {
        /* ignore */
      }
    }
    const { homePath = '', homeHash = '#dash' } = this.props;
    if (homePath) {
      try {
        window.location.assign(homePath);
        return;
      } catch (e) {
        /* jsdom/受限环境 fallback 到 hash */
      }
    }
    try {
      window.location.hash = homeHash || '#dash';
    } catch (e) {
      /* ignore */
    }
  }

  render() {
    const { error, info, chunkError, autoReloading } = this.state;
    if (!error) return this.props.children;
    const { title = '页面出现异常', homeLabel = '返回系统概览' } = this.props;
    if (chunkError) {
      return (
        <div className="pp-error-boundary" data-error-boundary="1" data-chunk-error="1" data-page={this.props.pageKey || ''} style={{ padding: 24 }}>
          <Result
            status="warning"
            title="页面资源已更新，请重新加载"
            subTitle={autoReloading
              ? '检测到旧版本页面资源，正在自动刷新页面……若未自动刷新，请点击下方“重新加载”。'
              : '检测到旧版本页面资源（通常发生在面板更新后），请点击“重新加载”获取最新版本。'}
            extra={
              <Space wrap>
                <Button type="primary" onClick={this.handleReload}>重新加载</Button>
                <Button onClick={this.handleHome}>{homeLabel}</Button>
              </Space>
            }
          />
        </div>
      );
    }
    const detail = errorDetail(error, info);
    return (
      <div className="pp-error-boundary" data-error-boundary="1" data-page={this.props.pageKey || ''} style={{ padding: 24 }}>
        <Result
          status="500"
          title={title}
          subTitle={<span style={{ fontSize: 13 }}>{errorSummary(error)}</span>}
          extra={
            <Space wrap>
              <Button type="primary" onClick={this.handleRetry}>
                重试
              </Button>
              <Button onClick={this.handleReload}>重新加载</Button>
              <Button onClick={this.handleHome}>{homeLabel}</Button>
            </Space>
          }
        />
        {detail ? (
          <pre
            className="pp-error-boundary-detail pp-mono"
            style={{
              margin: '0 auto',
              maxWidth: 860,
              maxHeight: 320,
              overflow: 'auto',
              padding: '12px 14px',
              borderRadius: 'var(--pp-radius-md)',
              border: '1px solid var(--pp-border)',
              background: 'var(--pp-code-bg)',
              color: 'var(--pp-code-text)',
              fontSize: 12,
              lineHeight: 1.6,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
            }}
          >
            {detail}
          </pre>
        ) : null}
      </div>
    );
  }
}