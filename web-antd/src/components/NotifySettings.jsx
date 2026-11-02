/* =================================================================
 *  服务器上下线通知设置（服务器管理页使用）
 *  · 读取 / 保存 /api/monitor/notify；支持发送测试邮件
 * ================================================================= */
import React, { useEffect } from 'react';
import { Alert, App as AntdApp, Button, Form, Input, InputNumber, Modal, Space, Switch } from 'antd';
import { api } from '../api';
import { readForm } from '../hooks';

export default function NotifySettings({ open, onClose }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [emailReady, setEmailReady] = React.useState(true);

  useEffect(() => {
    if (!open) return;
    setLoading(true);
    api('monitor/notify')
      .then((res) => {
        setEmailReady(!!res.email_ready);
        form.setFieldsValue({
          enabled: !!res.enabled,
          emails: res.emails || '',
          cooldown_min: Number(res.cooldown_min) || 10,
        });
      })
      .catch((e) => message.error(e.message || '读取通知设置失败'))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const save = async () => {
    const v = await readForm(form);
    if (!v) return;
    setSaving(true);
    try {
      await api('monitor/notify', {
        method: 'PUT',
        body: { enabled: !!v.enabled, emails: v.emails || '', cooldown_min: Number(v.cooldown_min) || 10 },
      });
      message.success('通知设置已保存');
      onClose();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    setTesting(true);
    try {
      const res = await api('monitor/notify/test', { method: 'POST', body: {} });
      message.success(`测试邮件已发送至 ${res.to || '收件人'}`);
    } catch (e) {
      message.error(e.message || '发送失败');
    } finally {
      setTesting(false);
    }
  };

  return (
    <Modal
      open={open}
      title="服务器上下线通知"
      onCancel={onClose}
      width={560}
      destroyOnHidden
      footer={
        <Space>
          <Button onClick={test} loading={testing}>
            发送测试邮件
          </Button>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" onClick={save} loading={saving}>
            保存
          </Button>
        </Space>
      }
    >
      {!emailReady ? (
        <Alert
          type="warning"
          showIcon
          style={{ borderRadius: 10, margin: '8px 0 14px' }}
          message="Worker 还没有配置邮件服务"
          description="需要配置 EMAIL（邮件发送 binding）与 SENDER_EMAIL（发件人）后才能发送通知。"
        />
      ) : null}
      <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 10 }} disabled={loading}>
        <Form.Item name="enabled" label="启用上下线通知" valuePropName="checked">
          <Switch size="small" />
        </Form.Item>
        <Form.Item name="emails" label="收件邮箱" extra="多个邮箱用逗号分隔" rules={[{ required: false }]}>
          <Input.TextArea rows={2} placeholder="ops@example.com, admin@example.com" />
        </Form.Item>
        <Form.Item name="cooldown_min" label="同类告警冷却（分钟）">
          <InputNumber min={1} max={1440} style={{ width: '100%' }} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
