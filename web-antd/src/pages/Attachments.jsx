import React, { useEffect, useMemo, useState } from 'react';
import {
  ArrowUpOutlined,
  CloudOutlined,
  CopyOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  EyeOutlined,
  FolderAddOutlined,
  FolderOpenOutlined,
  LinkOutlined,
  MoreOutlined,
  ReloadOutlined,
  SettingOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Breadcrumb,
  Button,
  Dropdown,
  Form,
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
  const parts = String(prefix || '').split('/').filter(Boolean);
  const out = [{ title: <span><FolderOpenOutlined /> 根目录</span>, prefix: '' }];
  let cur = '';
  parts.forEach((p) => {
    cur += p + '/';
    out.push({ title: p, prefix: cur });
  });
  return out;
}

function parentPrefix(prefix) {
  const parts = String(prefix || '').split('/').filter(Boolean);
  parts.pop();
  return parts.length ? parts.join('/') + '/' : '';
}

function extOf(name) {
  const i = String(name || '').lastIndexOf('.');
  return i > 0 ? name.slice(i + 1).toLowerCase() : '';
}

// 对象存储设置：Bucket / Endpoint / CDN 地址。
// 统一 Cloudflare Token 与 Account ID 在「系统设置 → Cloudflare」，这里只放桶相关参数。
function StorageSettings({ status, onSaved }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [open, setOpen] = useState(false);

  const load = async () => {
    try {
      const s = await api('settings');
      const vals = s?.values || {};
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
  }, [open]);

  const save = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      await api('settings', { method: 'POST', body: v });
      message.success('存储设置已保存');
      onSaved?.();
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

export default function Attachments() {
  const { message, modal } = AntdApp.useApp();
  const [prefix, setPrefix] = useState('');
  const [uploading, setUploading] = useState(false);
  const [loadingKey, setLoadingKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(null);
  const [preview, setPreview] = useState(null);
  const [mkdirOpen, setMkdirOpen] = useState(false);
  const [mkdirName, setMkdirName] = useState('');
  const [rename, setRename] = useState(null); // { row, mode: 'rename'|'copy', value }
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

  const fetchBlob = async (row) => {
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
      const blob = await fetchBlob(row);
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
        setEditing({ ...j, open: true, content: j.content || '' });
      } else if ((j.content_type || '').startsWith('image/')) {
        const blob = await fetchBlob(row);
        setPreview({ name: row.name, url: URL.createObjectURL(blob) });
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
        body: { key: editing.key, content: editing.content, content_type: editing.content_type },
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
      if (row.__type === 'folder') {
        const res = await api('attachments', { method: 'DELETE', query: { prefix: row.prefix } });
        message.success(`已删除目录 ${row.name}（${res.deleted || 0} 个对象）`);
      } else {
        await api('attachments', { method: 'DELETE', query: { key: row.key } });
        message.success('已删除 ' + row.key);
      }
      list.reload();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const doMkdir = async () => {
    const name = mkdirName.trim();
    if (!name) {
      message.warning('请输入目录名');
      return;
    }
    setBusy(true);
    try {
      await api('attachments/folder', { method: 'POST', body: { prefix, name } });
      message.success('已创建目录 ' + name);
      setMkdirOpen(false);
      setMkdirName('');
      list.reload();
    } catch (e) {
      message.error(e.message || '创建失败');
    } finally {
      setBusy(false);
    }
  };

  const doRelocate = async () => {
    if (!rename) return;
    const value = String(rename.value || '').trim().replace(/^\/+/, '');
    if (!value) {
      message.warning('请输入新名称');
      return;
    }
    setBusy(true);
    const row = rename.row;
    try {
      const isFolder = row.__type === 'folder';
      const from = isFolder ? row.prefix : row.key;
      const to = isFolder ? prefix + value.replace(/\/+$/, '') + '/' : prefix + value;
      const res = await api(rename.mode === 'copy' ? 'attachments/copy' : 'attachments/move', {
        method: 'POST',
        body: { from, to, folder: isFolder },
      });
      message.success(
        `${rename.mode === 'copy' ? '已复制' : '已重命名为'} ${to}${isFolder ? `（${res.objects || 0} 个对象）` : ''}`
      );
      setRename(null);
      list.reload();
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy(false);
    }
  };

  const moreMenu = (row) => ({
    items: [
      {
        key: 'edit',
        icon: row.__type === 'folder' ? <FolderOpenOutlined /> : <EditOutlined />,
        label: row.__type === 'folder' ? '打开目录' : '打开 / 编辑',
      },
      ...(row.__type === 'file'
        ? [
            { key: 'preview', icon: <EyeOutlined />, label: '预览' },
            { key: 'download', icon: <DownloadOutlined />, label: '下载' },
          ]
        : []),
      { key: 'rename', icon: <EditOutlined />, label: '重命名' },
      ...(row.__type === 'file'
        ? [
            { key: 'copy', icon: <CopyOutlined />, label: '复制副本' },
            { key: 'cdn', icon: <LinkOutlined />, label: '复制 CDN 链接', disabled: !row.cdn_url },
          ]
        : []),
      { type: 'divider' },
      { key: 'delete', icon: <DeleteOutlined />, label: '删除', danger: true },
    ],
    onClick: ({ key, domEvent }) => {
      domEvent.stopPropagation();
      if (key === 'edit') openFile(row);
      if (key === 'preview') openFile(row);
      if (key === 'download') download(row);
      if (key === 'cdn') copyCDN(row);
      if (key === 'rename') setRename({ row, mode: 'rename', value: row.name });
      if (key === 'copy') setRename({ row, mode: 'copy', value: 'copy-' + row.name });
      if (key === 'delete') {
        if (row.__type === 'folder') {
          modal.confirm({
            title: `删除目录 ${row.name}？`,
            content: '将递归删除该目录下的全部文件，无法恢复。',
            okText: '删除',
            okButtonProps: { danger: true },
            onOk: () => remove(row),
          });
        } else {
          modal.confirm({
            title: `删除文件 ${row.name}？`,
            content: row.key,
            okText: '删除',
            okButtonProps: { danger: true },
            onOk: () => remove(row),
          });
        }
      }
    },
  });

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
          <Space size={6}>
            <Button type="link" style={{ padding: 0 }} loading={loadingKey === row.key} onClick={() => openFile(row)}>
              <EditOutlined /> {row.name}
            </Button>
            {extOf(row.name) ? <Tag style={{ marginInlineEnd: 0 }}>{extOf(row.name)}</Tag> : null}
          </Space>
        ),
    },
    { title: '大小', dataIndex: 'size', width: 110, render: (v, row) => (row.__type === 'file' ? fmtBytes(v) : '—') },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 190,
      render: (v) => (v ? String(v).replace('T', ' ').replace('Z', ' UTC') : '—'),
    },
    {
      title: '操作',
      width: 210,
      render: (_, row) => (
        <Space size={4}>
          <Button size="small" onClick={() => openFile(row)}>
            {row.__type === 'folder' ? '打开' : '编辑'}
          </Button>
          {row.__type === 'file' ? (
            <Button size="small" icon={<DownloadOutlined />} onClick={() => download(row)} />
          ) : null}
          <Dropdown menu={moreMenu(row)} trigger={['click']}>
            <Button size="small" icon={<MoreOutlined />} />
          </Dropdown>
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
          <Button icon={<FolderAddOutlined />} disabled={!configured} onClick={() => setMkdirOpen(true)}>
            新建文件夹
          </Button>
          <Upload showUploadList={false} customRequest={({ file, onSuccess, onError }) => upload(file).then(onSuccess).catch(onError)}>
            <Button type="primary" icon={<UploadOutlined />} loading={uploading} disabled={!configured}>
              上传文件
            </Button>
          </Upload>
          <StorageSettings status={status} onSaved={() => { status.reload(); list.reload(); }} />
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
          description="点右上角「对象存储设置」填写 Bucket；API Token 与 Account ID 在「系统设置 → Cloudflare」。"
        />
      )}
      <Toolbar
        left={
          <Space>
            <Button
              size="small"
              icon={<ArrowUpOutlined />}
              disabled={!prefix}
              onClick={() => setPrefix(parentPrefix(prefix))}
            >
              上一级
            </Button>
            <Breadcrumb
              items={parentsOf(prefix).map((p) => ({
                title: p.prefix ? <a onClick={() => setPrefix(p.prefix)}>{p.title}</a> : p.title,
              }))}
            />
          </Space>
        }
        right={
          <Typography.Text type="secondary">
            文本 / 配置 / 代码类文件均可在线编辑（≤2 MiB）；图片可预览；目录支持重命名与递归删除
          </Typography.Text>
        }
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
        width={1100}
        onCancel={() => setEditing(null)}
        onOk={save}
        okText="保存"
        confirmLoading={saving}
        destroyOnHidden
      >
        <Space direction="vertical" style={{ width: '100%' }} size={8}>
          <Space size={16}>
            <Typography.Text type="secondary">
              {editing ? editing.content_type : ''} · {fmtBytes(editing ? editing.size : 0)} · 最大 2 MiB
            </Typography.Text>
            {editing && editing.cdn_url ? (
              <Typography.Text copyable={{ text: editing.cdn_url }}>CDN: {editing.cdn_url}</Typography.Text>
            ) : null}
          </Space>
          <Input.TextArea
            value={editing ? editing.content : ''}
            onChange={(e) => setEditing((cur) => (cur ? { ...cur, content: e.target.value } : cur))}
            autoSize={{ minRows: 22, maxRows: 34 }}
            style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 13 }}
            spellCheck={false}
          />
        </Space>
      </Modal>

      <Modal
        open={!!preview}
        title={preview ? preview.name : ''}
        footer={null}
        width={900}
        onCancel={() => {
          if (preview && preview.url) URL.revokeObjectURL(preview.url);
          setPreview(null);
        }}
        destroyOnHidden
      >
        {preview ? (
          <div style={{ textAlign: 'center' }}>
            <img src={preview.url} alt={preview.name} style={{ maxWidth: '100%', maxHeight: '70vh' }} />
          </div>
        ) : null}
      </Modal>

      <Modal
        open={mkdirOpen}
        title="新建文件夹"
        onCancel={() => setMkdirOpen(false)}
        onOk={doMkdir}
        okText="创建"
        confirmLoading={busy}
        destroyOnHidden
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Typography.Text type="secondary">位置：{prefix || '根目录'}</Typography.Text>
          <Input
            placeholder="目录名，例如 assets"
            value={mkdirName}
            onChange={(e) => setMkdirName(e.target.value)}
            onPressEnter={doMkdir}
          />
        </Space>
      </Modal>

      <Modal
        open={!!rename}
        title={rename ? (rename.mode === 'copy' ? '复制副本' : '重命名') : ''}
        onCancel={() => setRename(null)}
        onOk={doRelocate}
        okText={rename && rename.mode === 'copy' ? '复制' : '重命名'}
        confirmLoading={busy}
        destroyOnHidden
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Typography.Text type="secondary">
            位置：{prefix || '根目录'} · {rename ? rename.row.name : ''}
          </Typography.Text>
          <Input
            value={rename ? rename.value : ''}
            onChange={(e) => setRename((cur) => (cur ? { ...cur, value: e.target.value } : cur))}
            onPressEnter={doRelocate}
          />
        </Space>
      </Modal>
    </PageCard>
  );
}
