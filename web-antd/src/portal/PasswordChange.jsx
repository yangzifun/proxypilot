import React, { useEffect, useState } from 'react';
import { App as AntdApp, Button, Form, Input, Modal, Space, Tag, Typography } from 'antd';
import { KeyOutlined, SafetyOutlined } from '@ant-design/icons';
import { portalApi } from './portalApi';

// 用户中心 · 修改密码。
// 初始密码由管理员创建用户时生成，登录后在这里改成自己的。
export default function PasswordChange({ open, onClose, mustChange = false }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState({ loading: true, has_password: true });

  useEffect(() => {
    if (!open) return;
    let alive = true;
    (async () => {
      try {
        const r = await portalApi('portal/password');
        if (alive) setStatus({ loading: false, has_password: !!r.has_password });
      } catch {
        if (alive) setStatus({ loading: false, has_password: true });
      }
    })();
    return () => { alive = false; };
  }, [open]);

  const submit = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      await portalApi('portal/password', {
        method: 'POST',
        body: { old_password: v.old_password || '', new_password: v.new_password },
      });
      message.success('密码已更新，下次请用新密码登录');
      form.resetFields();
      if (onClose) onClose(true);
    } catch (e) {
      message.error(e.message || '修改失败');
    } finally {
      setBusy(false);
    }
  };

  const needOld = status.has_password;

  return (
    <Modal
      title="修改登录密码"
      open={open}
      onCancel={() => onClose && onClose(false)}
      onOk={submit}
      okText="保存"
      confirmLoading={busy}
      destroyOnHidden
      width={440}
    >
      <Space direction="vertical" size={10} style={{ width: '100%' }}>
        {mustChange ? (
          <Tag color="orange" icon={<SafetyOutlined />}>
            你正在使用初始密码，建议立即修改
          </Tag>
        ) : null}
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          密码至少 8 位；修改后请用新密码登录，验证码登录仍然可用。
        </Typography.Text>

        <Form form={form} layout="vertical" style={{ marginTop: 4 }}>
          {needOld ? (
            <Form.Item
              name="old_password"
              label="原密码"
              rules={[{ required: true, message: '请输入原密码' }]}
            >
              <Input.Password prefix={<KeyOutlined />} placeholder="当前密码" autoComplete="current-password" />
            </Form.Item>
          ) : null}
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '至少 8 位' },
            ]}
          >
            <Input.Password prefix={<KeyOutlined />} placeholder="至少 8 位" autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirm"
            label="确认新密码"
            dependencies={['new_password']}
            rules={[
              { required: true, message: '请再次输入新密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('new_password') === value) return Promise.resolve();
                  return Promise.reject(new Error('两次输入不一致'));
                },
              }),
            ]}
          >
            <Input.Password prefix={<KeyOutlined />} placeholder="再次输入" autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Space>
    </Modal>
  );
}