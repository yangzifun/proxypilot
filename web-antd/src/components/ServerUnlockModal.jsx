import React, { useCallback, useEffect, useRef, useState } from 'react';
import { ReloadOutlined, UnlockOutlined } from '@ant-design/icons';
import { Alert, App as AntdApp, Button, Col, Empty, Modal, Row, Space, Spin, Tag, Typography } from 'antd';
import { api } from '../api';
import { fmtTime } from '../format';

const STATUS = {
  unlocked: { color: 'success', text: '已解锁' },
  partial: { color: 'gold', text: '部分可用' },
  available: { color: 'processing', text: '可用' },
  uncertain: { color: 'warning', text: '待确认' },
  blocked: { color: 'error', text: '受限' },
  warn: { color: 'warning', text: '异常' },
  unknown: { color: 'default', text: '未知' },
};

const GROUP_TITLE = {
  ai: { title: 'AI 服务', desc: 'ChatGPT / Claude / Gemini / Copilot' },
  stream: { title: '影视与流媒体', desc: 'Netflix / YouTube / Disney+ / Prime Video / Spotify / TikTok' },
};

export default function ServerUnlockModal({ server, onClose }) {
  const { message } = AntdApp.useApp();
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState('');
  const alive = useRef(true);
  const timer = useRef(null);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);

  const load = useCallback(async () => {
    if (!server) return;
    setLoading(true);
    setError('');
    try {
      const res = await api(`servers/${server.id}/unlock`);
      if (alive.current) setData(res.data || null);
    } catch (e) {
      if (alive.current) setError(e.message || '读取解锁结果失败');
    } finally {
      if (alive.current) setLoading(false);
    }
  }, [server]);

  useEffect(() => {
    setData(null);
    setError('');
    if (server) load();
  }, [server, load]);

  const poll = useCallback(
    async (taskId) => {
      if (!alive.current) return;
      try {
        const res = await api(`servers/${server.id}/unlock`, { query: { task_id: taskId } });
        if (!alive.current) return;
        if (res.status === 'done') {
          setData(res.data || null);
          setChecking(false);
          message.success('解锁检测完成');
          return;
        }
        if (res.status === 'failed') {
          setError(res.error || '检测失败');
          setChecking(false);
          return;
        }
        timer.current = setTimeout(() => poll(taskId), 2000);
      } catch (e) {
        if (alive.current) {
          setError(e.message || '检测失败');
          setChecking(false);
        }
      }
    },
    [server, message]
  );

  const start = async () => {
    if (!server) return;
    setChecking(true);
    setError('');
    try {
      const res = await api(`servers/${server.id}/unlock`, { method: 'POST', body: {} });
      if (res.task_id) poll(res.task_id);
      else {
        setChecking(false);
        message.info(res.message || '检测已下发');
      }
    } catch (e) {
      setChecking(false);
      setError(e.message || '发起检测失败');
    }
  };

  const groups = ['ai', 'stream'];

  return (
    <Modal
      open={!!server}
      title={
        <span>
          <UnlockOutlined style={{ color: '#6366f1', marginRight: 6 }} />
          解锁详情 · {server ? server.name : ''}
        </span>
      }
      onCancel={onClose}
      footer={
        <Space>
          <Button onClick={onClose}>关闭</Button>
          <Button type="primary" icon={<ReloadOutlined />} loading={checking} onClick={start}>
            {data ? '重新检测' : '开始检测'}
          </Button>
        </Space>
      }
      width={760}
      centered
      destroyOnHidden
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Alert
          type="info"
          showIcon
          message="影视解锁由 CommonMediaTests 在目标服务器实测，AI 服务为出口可达性检测"
          description="Netflix / Disney+ 依据实测播放权限判断；YouTube、Prime Video 等仅能判断出口访问情况。AI 服务即使 HTTP 200 仍需登录后确认账号可用，403 通常代表地区或风控限制。"
        />
        {data && data.checked_at ? (
          <Typography.Text type="secondary">上次检测：{fmtTime(data.checked_at)}</Typography.Text>
        ) : null}
        {checking ? (
          <div style={{ padding: '28px 0', textAlign: 'center' }}>
            <Spin />
            <div className="pp-muted" style={{ marginTop: 10 }}>正在从目标服务器发起检测，通常需要 10–60 秒…</div>
          </div>
        ) : null}
        {error ? <Alert type="error" showIcon message={error} /> : null}
        {!checking && !data && !error ? (
          <Empty description="尚未检测，点击「开始检测」获取解锁情况" />
        ) : null}
        {!checking && data && data.services && data.services.length
          ? groups.map((group) => {
              const items = data.services.filter((s) => s.group === group);
              if (!items.length) return null;
              return (
                <div key={group}>
                  <div style={{ fontWeight: 650, marginBottom: 8 }}>
                    {GROUP_TITLE[group].title}
                    <span className="pp-muted" style={{ fontWeight: 400, fontSize: 12, marginLeft: 8 }}>
                      {GROUP_TITLE[group].desc}
                    </span>
                  </div>
                  <Row gutter={[10, 10]}>
                    {items.map((s) => {
                      const st = STATUS[s.status] || STATUS.unknown;
                      return (
                        <Col xs={24} sm={12} key={s.key}>
                          <div style={{ border: '1px solid #eef2f7', borderRadius: 10, padding: '10px 12px', background: '#fbfcff' }}>
                            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                              <b style={{ fontSize: 13 }}>{s.name}</b>
                              <Tag color={st.color} style={{ marginLeft: 'auto', marginInlineEnd: 0 }}>{st.text}</Tag>
                            </div>
                            <div className="pp-muted" style={{ fontSize: 11, marginTop: 4, lineHeight: 1.6 }}>
                              {s.code ? `HTTP ${s.code} · ` : ''}{s.detail || '—'}
                            </div>
                          </div>
                        </Col>
                      );
                    })}
                  </Row>
                </div>
              );
            })
          : null}
      </Space>
    </Modal>
  );
}
