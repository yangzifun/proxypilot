/* =================================================================
 *  用户中心 · 顶部账号卡（左：登录邮箱；右：当前 UUID 的信息）
 *  合并了原来的「账号信息面板」与「信息与邮箱」页签
 * ================================================================= */
import React, { useCallback, useEffect, useState } from 'react';
import { App as AntdApp, Button, Input, Modal, Skeleton, Space, Tag } from 'antd';
import { EditOutlined, MailOutlined, KeyOutlined, SwapOutlined } from '@ant-design/icons';
import { portalReq } from './portalApi';
import { UuidRotateModal } from './PortalUuid';
import EmailChange from './EmailChange';
import PasswordChange from './PasswordChange';
import { CopyText } from '../components';
import { fmtAgo, fmtNum, fmtTime, shortUuid } from '../format';

const STATUS = {
  active: { color: 'success', text: '正常' },
  suspended: { color: 'warning', text: '暂停' },
  inactive: { color: 'default', text: '停用' },
};

export default function PortalAccount({ email, count, uuid, sub, onEmailChanged, only = 'all' }) {
  const { message } = AntdApp.useApp();
  const [mailOpen, setMailOpen] = useState(false);
  const [pwdOpen, setPwdOpen] = useState(false);
  const [rotateOpen, setRotateOpen] = useState(false);
  const [state, setState] = useState({ loading: !!uuid, data: null, error: null });
  const [editing, setEditing] = useState(null);

  const load = useCallback(async () => {
    if (!uuid) {
      setState({ loading: false, data: null, error: null });
      return;
    }
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const { data } = await portalReq(`/manage/uuid-info/${encodeURIComponent(uuid)}`);
      // 后端：{ success, exists, data: {...} }
      const payload = (data && data.data) || data || {};
      setState({ loading: false, data: payload, error: null });
    } catch (e) {
      setState({ loading: false, data: null, error: e });
    }
  }, [uuid]);

  useEffect(() => {
    load();
  }, [load]);

  const info = state.data || {};

  const save = async () => {
    if (!editing) return;
    setEditing((s) => ({ ...s, saving: true }));
    try {
      await portalReq('/manage/uuid-info', {
        method: 'POST',
        body: {
          uuid,
          email: info.email || '',
          contact_info: editing.contact_info,
          remark: editing.remark,
        },
      });
      message.success('已保存');
      setEditing(null);
      load();
    } catch (e) {
      setEditing((s) => ({ ...s, saving: false }));
      message.error(e.message || '保存失败');
    }
  };

  return (
    <section className="pp-account-card">
      {/* 左栏：我的订阅 + 用户信息（合并成一块） */}
      <div className="pp-account-col">
        {sub ? (
          <>
            <div className="pp-account-head">
              <span className="pp-account-label">我的订阅</span>
              <span className="pp-account-meta" style={{ marginTop: 0 }}>
                {fmtNum(sub.configs)} 个节点
              </span>
            </div>
            <div className="pp-account-subtitle">
              <b>{sub.remark || sub.owner || shortUuid(sub.uuid)}</b>
              <Tag color={(STATUS[String(sub.status)] || {}).color || 'default'} style={{ marginInlineEnd: 0 }}>
                {(STATUS[String(sub.status)] || {}).text || '未知'}
              </Tag>
            </div>
            <div className="pp-account-kv" style={{ marginTop: 6 }}>
              <div>
                <span>UUID</span>
                <CopyText value={sub.uuid} display={<span className="pp-mono">{shortUuid(sub.uuid)}</span>} />
              </div>
              <div>
                <span>创建</span>
                <b>{fmtTime(sub.created_at)}</b>
              </div>
              <div>
                <span>更新</span>
                <b>{fmtAgo(sub.updated_at)}</b>
              </div>
            </div>
            <div className="pp-account-actions" style={{ marginTop: 10 }}>
              <Button danger icon={<SwapOutlined />} onClick={() => setRotateOpen(true)}>
                更换 UUID
              </Button>
              <span className="pp-account-meta" style={{ marginLeft: 8 }}>
                旧订阅链接与旧配置会立即失效
              </span>
            </div>
          </>
        ) : null}

        {only === 'all' ? (
        <div className="pp-account-info">
          <div className="pp-account-head">
            <span className="pp-account-label">用户信息</span>
            {uuid && !state.loading ? (
              <Button
                type="link"
                size="small"
                icon={<EditOutlined />}
                style={{ paddingInline: 0 }}
                onClick={() =>
                  setEditing({
                    contact_info: info.contact_info || '',
                    remark: info.remark || '',
                  })
                }
              >
                编辑信息
              </Button>
            ) : null}
          </div>

          {!uuid ? (
            <div className="pp-account-meta">还没有绑定 UUID</div>
          ) : state.loading ? (
            <Skeleton active paragraph={{ rows: 2 }} title={false} />
          ) : state.error ? (
            <div className="pp-account-meta" style={{ color: '#be123c' }}>
              {state.error.message || '加载失败'}
            </div>
          ) : (
            <div className="pp-account-kv">
              <div>
                <span>联系方式</span>
                <b>{info.contact_info || '—'}</b>
              </div>
              <div>
                <span>备注</span>
                <b>{info.remark || '—'}</b>
              </div>
            </div>
          )}
        </div>
        ) : null}
      </div>

      {only === 'all' ? (
      <div className="pp-account-col pp-account-col-mail">
        <div className="pp-account-label">登录邮箱</div>
        <div className="pp-account-mail">{email || '—'}</div>
        <div className="pp-account-meta">
          已绑定 {count} 个 UUID · 换绑后请用新邮箱接收验证码
        </div>
        <div className="pp-account-actions">
          <Button icon={<MailOutlined />} onClick={() => setMailOpen(true)}>
            更换绑定邮箱
          </Button>
        </div>
      </div>
      ) : null}

      {only === 'all' ? (
        <div className="pp-account-col pp-account-col-pwd">
          <div className="pp-account-label">登录密码</div>
          <div className="pp-account-mail">••••••••</div>
          <div className="pp-account-meta">
            初始密码由管理员提供；建议改成只有你知道的密码
          </div>
          <div className="pp-account-actions">
            <Button icon={<KeyOutlined />} onClick={() => setPwdOpen(true)}>
              修改密码
            </Button>
          </div>
        </div>
      ) : null}

      <PasswordChange open={pwdOpen} onClose={() => setPwdOpen(false)} />

      <UuidRotateModal open={rotateOpen} uuid={uuid || (sub && sub.uuid) || ''} onClose={() => setRotateOpen(false)} />

      <EmailChange
        open={mailOpen}
        onClose={() => setMailOpen(false)}
        currentEmail={email}
        onDone={(mail) => {
          if (onEmailChanged) onEmailChanged(mail);
          load();
        }}
      />

      <Modal
        open={!!editing}
        title="编辑用户信息"
        onCancel={() => setEditing(null)}
        onOk={save}
        okText="保存"
        cancelText="取消"
        confirmLoading={!!(editing && editing.saving)}
        centered
        destroyOnHidden
        width={460}
      >
        {editing ? (
          <Space orientation="vertical" style={{ width: '100%' }} size={10}>
            <Input
              addonBefore="联系方式"
              value={editing.contact_info}
              onChange={(e) => setEditing((s) => ({ ...s, contact_info: e.target.value }))}
            />
            <Input
              addonBefore="备注"
              value={editing.remark}
              onChange={(e) => setEditing((s) => ({ ...s, remark: e.target.value }))}
            />
          </Space>
        ) : null}
      </Modal>
    </section>
  );
}
