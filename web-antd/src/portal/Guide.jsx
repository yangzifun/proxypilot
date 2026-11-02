/* =================================================================
 *  用户中心 · 引导说明块
 *  统一「这个页面是干什么的 + 三步怎么做 + 一句注意事项」
 * ================================================================= */
import React from 'react';
import { InfoCircleOutlined } from '@ant-design/icons';

export default function Guide({ title, desc, steps = [], tip, icon, children }) {
  return (
    <section className="pp-guide">
      <div className="pp-guide-head">
        <span className="pp-guide-icon">{icon || <InfoCircleOutlined />}</span>
        <div style={{ minWidth: 0 }}>
          <div className="pp-guide-title">{title}</div>
          {desc ? <div className="pp-guide-desc">{desc}</div> : null}
        </div>
      </div>

      {steps.length ? (
        <ol className="pp-guide-steps">
          {steps.map((s, i) => (
            <li key={i}>
              <b>{i + 1}</b>
              <span>{s}</span>
            </li>
          ))}
        </ol>
      ) : null}

      {children ? <div className="pp-guide-more">{children}</div> : null}

      {tip ? <div className="pp-guide-tip">{tip}</div> : null}
    </section>
  );
}
