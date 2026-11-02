import React, { useMemo, useState } from 'react';
import {
  CloudOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  FolderOpenOutlined,
  LinkOutlined,
  ReloadOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Breadcrumb,
  Button,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  Upload,
} from 'antd';
import { api, getToken } from '../api';
import { useAsync } from '../hooks';
import { ErrorBlock, PageCard, Toolbar } from '../components';
import { fmtBytes } from '../format';

function parentsOf(prefix) {
  const parts = String(prefix || '')
    .split('/')
    .filter(Boolean);
  const out = [{ title: <span><FolderOpenOutlined /> 根目录</span>, prefix: '' }];
  let cur = '';
  parts.forEach((p) => {
    cur += p + '/';
    out.push({ title: p, prefix: cur });
  });
  return out;
}

export default function Attachments() {
  const { message } = AntdApp.useApp();
  const [prefix, setPrefix] = useState('');
  const [uploading, setUploading] = useState(false);
  const [loadingKey, setLoadingKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState(null);
  const status = useAsync(() => api('attachments/status'), []);
  const list = useAsync(() => api('attachments', { query: { prefix } }), [prefix]);

  const configured = !!(status.data && status.data.configured);
  const cdnBase = (status.data && status.data.cdn_base_url) || '';
  const folders = (list.data && list.data.folders) || [];
  const files = (list.data && list.data.files) || [];
  const rows = useMemo(
    () => [
      ...folders.map((f) => ({ ...f, __type: 'folder', key: f.prefix })),
      ...files.map((f) => ({ ...f, __type: 'file' })),
    ],
    [folders, files],
  );

  const upload = async (file) => {
    if (!configured) {
      message.error('R2 未配置');
      return false;
    }
    setUploading(true);
    try {
      const fd = new FormData();
      fd.append('file', file);
      const res = await fetch('/api/attachments/upload?prefix=' + encodeURIComponent(prefix), {
        method: 'POST',
        headers: { Authorization: 'Bearer ' + getToken() },
        body: fd,
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(j.error || '上传失败（HTTP ' + res.status + '）');
      message.success('已上传 ' + (j.key || file.name));
      list.reload();
    } catch (e) {
      message.error(e.message || '上传失败');
    } finally {
      setUploading(false);
    }
    return false;
  };

  const fetchDownload = async (row) => {
    const res = await fetch('/api/attachments/download?key=' + encodeURIComponent(row.key), {
      headers: { Authorization: 'Bearer ' + getToken() },
    });
    if (!res.ok) {
      const j = await res.json().catch(() => ({}));
      throw new Error(j.error || '下载失败（HTTP ' + res.status + '）');
    }
    return res.blob();
  };

  const download = async (row) => {
    try {
      const blob = await fetchDownload(row);
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = row.name || 'download';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 10000);
    } catch (e) {
      message.error(e.message || '下载失败');
    }
  };

  const openFile = async (row) => {
    if (row.__type === 'folder') {
      setPrefix(row.prefix);
      return;
    }
    setLoadingKey(row.key);
    try {
      const j = await api('attachments/content', { query: { key: row.key } });
      if (j.editable) {
        setEditing({
          ...j,
          open: true,
          content: j.content || '',
        });
      } else if ((j.content_type || '').startsWith('image/')) {
        const blob = await fetchDownload(row);
        const u = URL.createObjectURL(blob);
        window.open(u, '_blank', 'noopener');
        setTimeout(() => URL.revokeObjectURL(u), 60000);
      } else {
        await download(row);
      }
    } catch (e) {
      message.error(e.message || '读取失败');
    } finally {
      setLoadingKey('');
    }
  };

  const save = async () => {
    if (!editing) return;
    setSaving(true);
    try {
      await api('attachments/content', {
        method: 'PUT',
        body: {
          key: editing.key,
          content: editing.content,
          content_type: editing.content_type,
        },
      });
      message.success('已保存 ' + editing.key);
      setEditing(null);
      list.reload();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const copyCDN = async (row) => {
    if (!row.cdn_url) return;
    try {
      await navigator.clipboard.writeText(row.cdn_url);
      message.success('已复制 CDN 链接');
    } catch (e) {
      message.info(row.cdn_url);
    }
  };

  const remove = async (row) => {
    try {
      await api('attachments', { method: 'DELETE', query: { key: row.key } });
      message.success('已删除 ' + row.key);
      list.reload();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (_, row) =>
        row.__type === 'folder' ? (
          <Button type="link" style={{ padding: 0 }} onClick={() => setPrefix(row.prefix)}>
            <FolderOpenOutlined /> {row.name}
          </Button>
        ) : (
          <Button type="link" style={{ padding: 0 }} loading={loadingKey === row.key} onClick={() => openFile(row)}>
            <EditOutlined /> {row.name}
          </Button>
        ),
    },
    {
      title: '大小',
      dataIndex: 'size',
      width: 120,
      render: (v, row) => (row.__type === 'file' ? fmtBytes(v) : '—'),
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 190,
      render: (v) => (v ? String(v).replace('T', ' ').replace('Z', ' UTC') : '—'),
    },
    {
      title: '操作',
      width: 260,
      render: (_, row) =>
        row.__type === 'folder' ? (
          <Button size="small" onClick={() => setPrefix(row.prefix)}>
            打开
          </Button>
        ) : (
          <Space>
            <Button size="small" icon={<EditOutlined />} onClick={() => openFile(row)}>
              打开/编辑
            </Button>
            <Button size="small" icon={<DownloadOutlined />} onClick={() => download(row)}>
              下载
            </Button>
            {row.cdn_url ? (
              <Button size="small" icon={<LinkOutlined />} onClick={() => copyCDN(row)}>
                CDN
              </Button>
            ) : null}
            <Popconfirm title={'删除 ' + row.key + ' ?'} onConfirm={() => remove(row)}>
              <Button size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          </Space>
        ),
    },
  ];

  if (status.error) {
    return <ErrorBlock error={status.error} onRetry={() => status.reload()} title="R2 状态读取失败" />;
  }

  return (
    <PageCard
      loading={status.loading && !status.data}
      title="附件管理"
      extra={
        <Space>
          <Tag icon={<CloudOutlined />} color={configured ? 'blue' : 'default'}>
            {configured ? 'R2: ' + (status.data.bucket || '') : 'R2 未配置'}
          </Tag>
          {cdnBase ? <Tag color="purple">CDN: {cdnBase}</Tag> : null}
          <Upload showUploadList={false} customRequest={({ file, onSuccess, onError }) => upload(file).then(onSuccess).catch(onError)}>
            <Button type="primary" icon={<UploadOutlined />} loading={uploading} disabled={!configured}>
              上传文件
            </Button>
          </Upload>
          <Button icon={<ReloadOutlined />} onClick={() => list.reload()}>
            刷新
          </Button>
        </Space>
      }
    >
      {!configured && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message="R2 未配置"
          description="请在「系统设置 → Cloudflare」填写 API Token、Account ID 与 Bucket。"
        />
      )}
      <Toolbar
        left={
          <Breadcrumb
            items={parentsOf(prefix).map((p) => ({
              title: p.prefix ? <a onClick={() => setPrefix(p.prefix)}>{p.title}</a> : p.title,
            }))}
          />
        }
        right={<Typography.Text type="secondary">文本文件可在线编辑；CDN 链接来自系统设置</Typography.Text>}
      />
      {list.error ? (
        <ErrorBlock error={list.error} onRetry={() => list.reload()} title="附件列表读取失败" />
      ) : (
        <Table
          rowKey={(row) => row.__type + ':' + row.key}
          size="middle"
          loading={list.loading}
          dataSource={rows}
          columns={columns}
          pagination={false}
          locale={{ emptyText: '该目录下没有文件' }}
        />
      )}

      <Modal
        open={!!(editing && editing.open)}
        title={<span><EditOutlined /> 编辑 {editing ? editing.key : ''}</span>}
        width={960}
        onCancel={() => setEditing(null)}
        onOk={save}
        okText="保存"
        confirmLoading={saving}
        destroyOnHidden
      >
        <Space direction="vertical" style={{ width: '100%' }} size={8}>
          <Typography.Text type="secondary">
            {editing ? editing.content_type : ''} · {fmtBytes(editing ? editing.size : 0)} · 最大 2 MiB
          </Typography.Text>
          {editing && editing.cdn_url ? (
            <Typography.Text copyable={{ text: editing.cdn_url }}>
              CDN: {editing.cdn_url}
            </Typography.Text>
          ) : null}
          <Input.TextArea
            value={editing ? editing.content : ''}
            onChange={(e) => setEditing((cur) => (cur ? { ...cur, content: e.target.value } : cur))}
            rows={22}
            style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 13 }}
            spellCheck={false}
          />
        </Space>
      </Modal>
    </PageCard>
  );
}
