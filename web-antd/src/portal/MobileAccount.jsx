/* =================================================================
 *  移动端「账号与信息」：antd-mobile Form 编辑用户信息 + 换绑邮箱入口
 *  · 顶部账号卡：邮箱首字母头像 + 邮箱 + 订阅状态
 *  · UUID / 绑定邮箱只读展示（UUID 可复制）
 *  · 底部补一句支持的协议，免得用户以为少了一半节点
 * ================================================================= */
import React, { useEffect, useState } from 'react';
import { Button, Form, Input, List, Toast } from 'antd-mobile';
import { CopyOutlined, LogoutOutlined } from '@ant-design/icons';
import { useUuidInfo } from './hooks/usePortalData';
import EmailChange from './EmailChange';
import { shortUuid } from '../format';

const STATUS_TEXT = { active: '正常', suspended: '暂停', inactive: '停用' };
const STATUS_CLS = { active: 'is-on', suspended: 'is-warn', inactive: 'is-off' };

export default function MobileAccount({ email, uuid, onEmailChanged, onLogout }) {
  const { loading, data, save } = useUuidInfo(uuid);
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [mailOpen, setMailOpen] = useState(false);

  useEffect(() => {
    if (!data) return;
    form.setFieldsValue({
      contact_info: data.contact_info || '',
      remark: data.remark || '',
    });
  }, [data, form]);

  const onSubmit = async (values) => {
    if (!uuid) {
      Toast.show({ content: '还没有绑定订阅，暂时没有可编辑的信息' });
      return;
    }
    setSaving(true);
    try {
      await save(values);
      Toast.show({ content: '已保存' });
    } catch (e) {
      Toast.show({ content: e.message || '保存失败' });
    } finally {
      setSaving(false);
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(uuid);
      Toast.show({ content: '已复制 UUID' });
    } catch (e) {
      Toast.show({ content: '复制失败，请长按选择' });
    }
  };

  const shownEmail = (data && data.email) || email || '';
  const status = (data && data.status) || '';
  const statusText = STATUS_TEXT[status] || '未知';
  const statusCls = STATUS_CLS[status] || 'is-off';
  const initial = String(shownEmail || 'U').slice(0, 1).toUpperCase();

  return (
    <div className="macc">
      <div className="macc-hero">
        <div className="macc-avatar">{initial}</div>
        <div className="macc-hero-main">
          <div className="macc-hero-mail">{shownEmail || '未绑定邮箱'}</div>
          <div className="macc-hero-sub">
            <span className={`macc-status ${statusCls}`}>{statusText}</span>
            <span className="macc-hero-uuid">UUID {shortUuid(uuid)}</span>
          </div>
        </div>
      </div>

      <List header="账号" className="macc-list">
        <List.Item
          description="换绑后请用新邮箱接收验证码"
          extra={
            <Button size="mini" fill="none" className="macc-action" onClick={() => setMailOpen(true)}>
              更换
            </Button>
          }
        >
          <div className="macc-line">
            <span className="macc-label">登录邮箱</span>
            <span className="macc-value">{shownEmail || '—'}</span>
          </div>
        </List.Item>
        <List.Item
          extra={
            <Button size="mini" fill="none" className="macc-action" onClick={copy}>
              <CopyOutlined /> 复制
            </Button>
          }
        >
          <div className="macc-line">
            <span className="macc-label">UUID</span>
            <span className="macc-value macc-uuid">{uuid || '—'}</span>
          </div>
        </List.Item>
      </List>

      <List header="用户信息" className="macc-list">
        <Form
          form={form}
          layout="vertical"
          onFinish={onSubmit}
          footer={
            <Button
              block
              color="primary"
              size="large"
              type="submit"
              loading={saving || loading}
              disabled={!uuid}
              className="macc-save"
            >
              保存
            </Button>
          }
        >
          <Form.Item name="contact_info" label="联系方式">
            <Input placeholder="邮箱 / Telegram 等" clearable />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input placeholder="可选" clearable />
          </Form.Item>
        </Form>
      </List>

      {onLogout ? (
        <button type="button" className="macc-logout" onClick={onLogout}>
          <LogoutOutlined /> 退出登录
        </button>
      ) : null}

      <div className="macc-foot">
        支持的协议：VMess / VLESS / Trojan / Shadowsocks / Hysteria2 / TUIC / AnyTLS / Snell
      </div>

      <EmailChange
        open={mailOpen}
        onClose={() => setMailOpen(false)}
        currentEmail={shownEmail}
        onDone={(mail) => {
          setMailOpen(false);
          if (onEmailChanged) onEmailChanged(mail);
        }}
      />
    </div>
  );
}
