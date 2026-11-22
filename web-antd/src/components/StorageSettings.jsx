import React, { useEffect, useState } from 'react';
import { Alert, App as AntdApp, Button, Form, Input, Modal } from 'antd';
import { SettingOutlined } from '@ant-design/icons';
import { api } from '../api';

/**
 * 对象存储设置（原「附件管理」页内组件，迁移到公共组件）
 * Bucket / Endpoint / CDN 公共地址 / Account ID / Access Key 配置；
 * 保存走 POST /api/settings（与系统设置一致）。
 */
export default function StorageSettings({ onSaved }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [open, setOpen] = useState(false);

  const load = async () => {
    try {
      const s = await api('settings');
      const vals = (s && s.values) || {};
      form.setFieldsValue({
        r2_bucket: vals.r2_bucket || '',
        r2_endpoint: vals.r2_endpoint || '',
        cdn_base_url: vals.cdn_base_url || '',
        r2_account_id: vals.r2_account_id || '',
        r2_access_key_id: vals.r2_access_key_id || '',
      });
    } catch (e) {
      message.error('读取存储设置失败：' + (e.message || e));
    }
  };

  useEffect(() => {
    if (open) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const save = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      await api('settings', { method: 'POST', body: v });
      message.success('存储设置已保存');
      if (onSaved) onSaved();
    } catch (e) {
      message.error('保存失败：' + (e.message || e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Button icon={<SettingOutlined />} onClick={() => setOpen(true)}>
        对象存储设置
      </Button>
      <Modal
        title="对象存储设置"
        open={open}
        onCancel={() => setOpen(false)}
        onOk={save}
        okText="保存"
        confirmLoading={saving}
        destroyOnHidden
        width={560}
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 14 }}
          message="Cloudflare Token 与 Account ID 在「系统设置 → Cloudflare」统一配置"
          description="这里只填写桶相关参数。Token 需要 Account → R2 → Object Read & Write 权限；系统会用 Token 自动换取 S3 凭证，无需再填 Access Key。"
        />
        <Form form={form} layout="vertical">
          <Form.Item
            name="r2_bucket"
            label="Bucket 名称"
            rules={[{ required: true, message: '请填写 Bucket' }]}
            extra="R2 控制台里创建的存储桶名，例如 proxypilot"
          >
            <Input placeholder="proxypilot" allowClear />
          </Form.Item>
          <Form.Item name="r2_endpoint" label="Endpoint" extra="留空按 Account ID 自动拼成 https://<account>.r2.cloudflarestorage.com">
            <Input placeholder="https://<account>.r2.cloudflarestorage.com" allowClear />
          </Form.Item>
          <Form.Item name="cdn_base_url" label="CDN 公共地址" extra="R2 绑定的自定义域名，例如 https://cdn.example.com；留空则只用鉴权下载">
            <Input placeholder="https://cdn.example.com" allowClear />
          </Form.Item>
          <Form.Item name="r2_account_id" label="Account ID（可选覆盖）" extra="一般留空，沿用系统设置里的 Account ID">
            <Input placeholder="留空沿用统一配置" allowClear />
          </Form.Item>
          <Form.Item name="r2_access_key_id" label="Access Key ID（可选）" extra="仅当不用统一 Token 换 S3 凭证时才需要填">
            <Input placeholder="留空由 Token 自动换取" allowClear />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}