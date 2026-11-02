/* =================================================================
 *  用户中心 · Telegram（MTProto）代理卡片
 *  · 独立于订阅：链接不进 /sub，只在这里看二维码 / 发邮件
 *  · 二维码在浏览器本地渲染（antd QRCode），不经过任何第三方
 * ================================================================= */
import React, { useCallback, useEffect, useState } from 'react';
import { Alert, App as AntdApp, Button, Empty, Modal, QRCode, Skeleton, Space, Tag } from 'antd';
import { MailOutlined, QrcodeOutlined, ReloadOutlined } from '@ant-design/icons';
import { portalApi } from './portalApi';
import { CopyText } from '../components';

export default function MtprotoCard({ mobile = false }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ loading: true, error: null, rows: [] });
  const [qr, setQr] = useState(null);
  const [busy, setBusy] = useState('');

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const data = await portalApi('portal/mtproto');
      setState({ loading: false, error: null, rows: (data && data.data) || [] });
    } catch (e) {
      setState({ loading: false, error: e, rows: [] });
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const openQr = async (row) => {
    setQr({ url: row.tg, tme: row.tme, title: row.node_name || 'Telegram 代理' });
    try {
      await portalApi('portal/mtproto/' + row.id + '/deliver', { method: 'POST', body: {} });
    } catch (e) {
      /* 记录交付失败不影响展示 */
    }
  };

  const sendMail = async (row) => {
    setBusy('mail-' + row.id);
    try {
      const res = await portalApi('portal/mtproto/' + row.id + '/send-mail', { method: 'POST', body: {} });
      message.success('已发送到 ' + (res && res.to ? res.to : '你的绑定邮箱'));
    } catch (e) {
      message.error(e.message || '发送失败，请稍后再试');
    } finally {
      setBusy('');
    }
  };

  const rows = state.rows;

  return (
    <div style={{ padding: mobile ? 0 : 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginBottom: 10 }}>
        <div>
          <b>Telegram 代理</b>
          <span className="pp-muted" style={{ marginLeft: 8, fontSize: 12 }}>MTProto · 与订阅分开，仅 TG 使用</span>
        </div>
        <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={state.loading}>
          刷新
        </Button>
      </div>

      {state.error ? (
        <Alert
          type="error"
          showIcon
          message={state.error.message || '加载失败'}
          action={<Button size="small" onClick={load}>重试</Button>}
        />
      ) : null}

      {state.loading && !rows.length ? <Skeleton active paragraph={{ rows: 2 }} title={false} /> : null}

      {!state.loading && !state.error && !rows.length ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={<span style={{ color: '#94a3b8' }}>管理员还没有为你开通 Telegram 代理</span>}
        />
      ) : null}

      {rows.map((row) => (
        <div
          key={row.id}
          style={{
            padding: '12px 14px',
            marginBottom: 12,
            border: '1px solid #eef2f7',
            borderRadius: 14,
            background: '#fbfcff',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
            <b>{row.node_name || 'Telegram 代理'}</b>
            <Tag color="blue" style={{ marginInlineEnd: 0 }}>MTProto</Tag>
            <span className="pp-muted" style={{ fontSize: 12 }}>{row.host}:{row.port}</span>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 10, marginBottom: 8 }}>
            <div>
              <div className="pp-muted" style={{ fontSize: 12 }}>服务器（域名 / IP）</div>
              <CopyText value={row.host || ''} />
            </div>
            <div>
              <div className="pp-muted" style={{ fontSize: 12 }}>端口</div>
              <CopyText value={String(row.port || '')} />
            </div>
            <div>
              <div className="pp-muted" style={{ fontSize: 12 }}>密钥（Secret）</div>
              <CopyText value={row.secret || ''} display={row.secret_hint || '点击复制'} />
            </div>
          </div>

          <div className="pp-muted" style={{ fontSize: 12, lineHeight: 1.9, marginBottom: 10 }}>
            配置方法：Telegram → 设置 → 数据和存储 → 代理 → 添加代理 → 选择 <b>MTProto</b>，把上面的服务器、端口、密钥填进去；
            也可以直接点「一键导入」或扫码。链接即凭证，请勿转发，泄露后请联系管理员重置。
          </div>

          <Space wrap>
            <Button size="small" type="primary" href={row.tg}>一键导入</Button>
            <Button size="small" icon={<QrcodeOutlined />} onClick={() => openQr(row)}>二维码</Button>
            <Button size="small" icon={<MailOutlined />} loading={busy === 'mail-' + row.id} onClick={() => sendMail(row)}>发到邮箱</Button>
          </Space>
        </div>
      ))}

      <Modal
        open={!!qr}
        title={qr ? qr.title : ''}
        onCancel={() => setQr(null)}
        footer={<Button onClick={() => setQr(null)}>关闭</Button>}
        width={420}
        centered
        destroyOnHidden
      >
        {qr ? (
          <div style={{ textAlign: 'center' }}>
            <QRCode
              value={qr.url}
              size={220}
              type="svg"
              errorLevel="M"
              bordered={false}
              style={{ padding: 18, background: '#fff', borderRadius: 12, display: 'inline-block' }}
            />
            <p className="pp-muted" style={{ margin: '12px 0 8px', fontSize: 13 }}>
              手机扫码，或在 Telegram 里点「添加代理」；也可以点下面的链接直接唤起。
            </p>
            <div style={{ textAlign: 'left', wordBreak: 'break-all', fontSize: 12 }}>
              <div className="pp-muted" style={{ marginBottom: 4 }}>tg:// 链接</div>
              <CopyText value={qr.url} />
              <div className="pp-muted" style={{ margin: '8px 0 4px' }}>t.me 链接（转发给自己再点开）</div>
              <CopyText value={qr.tme || qr.url} />
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  );
}
