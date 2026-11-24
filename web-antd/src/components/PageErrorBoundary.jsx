/* =================================================================
 *  PageErrorBoundary · 页面级错误边界
 *  · 捕获路由内容渲染/生命周期异常，避免整页白屏
 *  · 展示错误摘要 + 重试（重置错误状态）/ 重新加载 / 返回概览
 * ================================================================= */
import React from 'react';
import { Button, Result, Space } from 'antd';

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
    this.state = { error: null, info: null };
    this.handleRetry = this.handleRetry.bind(this);
    this.handleReload = this.handleReload.bind(this);
    this.handleHome = this.handleHome.bind(this);
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  componentDidCatch(error, info) {
    this.setState({ error, info });
    // 只打印摘要，避免控制台被大段堆栈刷屏
    try {
      // eslint-disable-next-line no-console
      console.error('[PageErrorBoundary] 页面渲染异常：', errorSummary(error));
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
    this.setState({ error: null, info: null });
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
    try {
      window.location.hash = '#dash';
    } catch (e) {
      /* ignore */
    }
  }

  render() {
    const { error, info } = this.state;
    if (!error) return this.props.children;
    const detail = errorDetail(error, info);
    return (
      <div className="pp-error-boundary" data-error-boundary="1" data-page={this.props.pageKey || ''} style={{ padding: 24 }}>
        <Result
          status="500"
          title="页面出现异常"
          subTitle={<span style={{ fontSize: 13 }}>{errorSummary(error)}</span>}
          extra={
            <Space wrap>
              <Button type="primary" onClick={this.handleRetry}>
                重试
              </Button>
              <Button onClick={this.handleReload}>重新加载</Button>
              <Button onClick={this.handleHome}>返回系统概览</Button>
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