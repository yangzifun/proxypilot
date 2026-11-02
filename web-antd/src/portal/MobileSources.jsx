/* =================================================================
 *  移动端「外部订阅源」：从配置管理页打开（Popup）
 *  与 PC 端能力对齐：新增 / 启停 / 删除；某个源拉取失败只影响该条
 * ================================================================= */
import React, { useState } from 'react';
import { Button, Dialog, Empty, Input, List, Popup, SpinLoading, Toast } from 'antd-mobile';
import { useUuidSources } from './hooks/usePortalData';

export default function MobileSourcesPopup({ uuid, visible, onClose }) {
  const { list, loading, error, reload, add, toggle, remove } = useUuidSources(uuid);
  const [form, setForm] = useState({ url: '', remark: '', saving: false });

  const submit = async () => {
    const url = form.url.trim();
    if (!/^https?:\/\//i.test(url)) {
      Toast.show({ content: '请填写 http/https 开头的订阅地址' });
      return;
    }
    setForm((s) => ({ ...s, saving: true }));
    try {
      await add({ url, remark: form.remark.trim() });
      setForm({ url: '', remark: '', saving: false });
      Toast.show({ content: '已添加' });
    } catch (e) {
      setForm((s) => ({ ...s, saving: false }));
      Toast.show({ content: e.message || '添加失败' });
    }
  };

  const onToggle = async (row) => {
    try {
      await toggle(row);
      Toast.show({ content: Number(row.enabled) ? '已停用' : '已启用' });
    } catch (e) {
      Toast.show({ content: e.message || '操作失败' });
    }
  };

  const onDelete = async (row) => {
    const ok = await Dialog.confirm({
      content: `删除订阅源「${row.remark || row.url}」？`,
      confirmText: '删除',
      cancelText: '取消',
    }).catch(() => false);
    if (!ok) return;
    try {
      await remove(row);
      Toast.show({ content: '已删除' });
    } catch (e) {
      Toast.show({ content: e.message || '删除失败' });
    }
  };

  return (
    <Popup
      visible={visible}
      onMaskClick={onClose}
      destroyOnClose
      bodyStyle={{ height: '82vh', overflow: 'auto', borderTopLeftRadius: 16, borderTopRightRadius: 16 }}
    >
      <div className="ppm-popup-head">
        <b>外部订阅源</b>
        <div>
          <Button size="mini" fill="none" onClick={reload}>
            刷新
          </Button>
          <Button size="mini" fill="none" onClick={onClose}>
            关闭
          </Button>
        </div>
      </div>

      <div className="msrc-body">
        <div className="msrc-add">
          <Input
            placeholder="https://example.com/sub/xxx"
            value={form.url}
            onChange={(v) => setForm((s) => ({ ...s, url: v }))}
            clearable
          />
          <Input
            placeholder="备注（可选）"
            value={form.remark}
            onChange={(v) => setForm((s) => ({ ...s, remark: v }))}
            clearable
          />
          <Button block color="primary" loading={form.saving} onClick={submit}>
            添加订阅源
          </Button>
          <div className="msrc-tip">启用后会参与下次订阅生成，把对方节点合并进你的订阅。</div>
        </div>

        {loading && !list.length ? (
          <div className="mstat-loading">
            <SpinLoading color="primary" />
          </div>
        ) : error ? (
          <div className="ppm-tip">
            {error.message || '加载失败'}
            <div style={{ marginTop: 10 }}>
              <Button size="small" onClick={reload}>
                重试
              </Button>
            </div>
          </div>
        ) : list.length ? (
          <List header={`已添加 ${list.length} 个`} className="msrc-list">
            {list.map((s) => (
              <List.Item
                key={s.id}
                description={<span className="msrc-url">{s.url}</span>}
                extra={
                  <span className="msrc-actions">
                    <Button
                      size="mini"
                      fill="none"
                      color={Number(s.enabled) ? 'default' : 'primary'}
                      onClick={() => onToggle(s)}
                    >
                      {Number(s.enabled) ? '停用' : '启用'}
                    </Button>
                    <Button size="mini" fill="none" color="danger" onClick={() => onDelete(s)}>
                      删除
                    </Button>
                  </span>
                }
              >
                <span className="msrc-remark">
                  <i className={`msrc-dot${Number(s.enabled) ? ' is-on' : ''}`} />
                  {s.remark || '未命名订阅源'}
                </span>
              </List.Item>
            ))}
          </List>
        ) : (
          <Empty description="还没有添加外部订阅源" className="msrc-empty" />
        )}
      </div>
    </Popup>
  );
}
