/* =================================================================
 *  MTProto（Telegram 专用代理）节点管理
 *  · 协议模板：监听端口 13709 / 运行参数；伪装域名在节点绑定/编辑时单独填写
 *  · 节点绑定：服务器 + 模板 + 可选覆盖（域名 / 端口）
 *  · 授权：按 UUID 生成独立 secret；撤销 / 轮换只影响该用户
 *  · 与订阅完全隔离：链接只在后台和用户中心出现，不进 /sub
 * ================================================================= */
import React, { useMemo, useState } from 'react';
import {
  ApiOutlined,
  CloudUploadOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  MailOutlined,
  PlusOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SwapOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Col,
  Divider,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  QRCode,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiPost } from '../api';
import { useAsync, readForm } from '../hooks';
import { CopyText, EmptyBlock, ErrorBlock, IconAction, PageCard, StatCard } from '../components';
import { watchBatch } from '../components/JobCenter';
import { fmtAgo, shortUuid, fmtNum } from '../format';
import { chartPalette } from '../theme';

const MODE_LABEL = { multi: '每用户独立', single: '整节点共享' };
const PREFER_OPTIONS = [
  { value: 'prefer-ipv4', label: '优先 IPv4' },
  { value: 'prefer-ipv6', label: '优先 IPv6' },
  { value: 'only-ipv4', label: '仅 IPv4' },
  { value: 'only-ipv6', label: '仅 IPv6' },
];

function serviceTag(state) {
  const s = String(state || '');
  if (s === 'active') return <Tag color="success">运行中</Tag>;
  if (s === 'failed') return <Tag color="error">异常</Tag>;
  if (s === 'inactive') return <Tag>未启动</Tag>;
  if (s === 'not-installed') return <Tag>未安装</Tag>;
  return <span className="pp-muted">—</span>;
}

function inheritTag(on) {
  return on ? <Tag style={{ marginInlineStart: 6 }}>继承</Tag> : null;
}

export default function MtprotoPanel({ templatesOnly }) {
  const { message, modal } = AntdApp.useApp();
  const nodes = useAsync(() => api('mtproto/nodes'), []);
  const templates = useAsync(() => api('mtproto/templates'), []);
  const servers = useAsync(() => api('servers'), []);
  const uuids = useAsync(() => api('mtproto/uuids'), []);

  const nodeRows = (nodes.data && nodes.data.data) || [];
  const tplRows = (templates.data && templates.data.data) || [];
  const serverRows = (servers.data && servers.data.data) || [];
  const uuidRows = (uuids.data && uuids.data.data) || [];

  const [nodeForm] = Form.useForm();
  const [tplForm] = Form.useForm();
  const [nodeModal, setNodeModal] = useState(null); // { mode, row }
  const [tplModal, setTplModal] = useState(null); // { mode, row }
  const [busy, setBusy] = useState('');
  const [preview, setPreview] = useState(null); // { title, content }
  const [linkModal, setLinkModal] = useState(null); // { title, tg, tme }

  const [drawerNode, setDrawerNode] = useState(null);
  const [clientRows, setClientRows] = useState([]);
  const [clientLoading, setClientLoading] = useState(false);
  const [clientError, setClientError] = useState(null);
  const [revealed, setRevealed] = useState(false);
  const [grant, setGrant] = useState({ uuid: '', label: '', send_mail: false });
  const [granting, setGranting] = useState(false);

  const reloadAll = () => {
    nodes.reload().catch(() => {});
    templates.reload().catch(() => {});
  };

  const loadClients = async (node, reveal) => {
    if (!node) return;
    setClientLoading(true);
    setClientError(null);
    try {
      const res = await api('mtproto/nodes/' + node.id + '/clients' + (reveal ? '?reveal=1' : ''));
      setClientRows((res && res.data) || []);
      setRevealed(!!reveal);
    } catch (e) {
      setClientRows([]);
      setClientError(e);
    } finally {
      setClientLoading(false);
    }
  };

  const openClients = (row) => {
    setDrawerNode(row);
    setGrant({ uuid: '', label: '', send_mail: false });
    loadClients(row, false);
  };

  /* ---------------- 模板 ---------------- */
  const saveTemplate = async () => {
    const values = await readForm(tplForm);
    if (!values) return;
    const body = Object.assign({}, values, { blocklist: values.blocklist ? 1 : 0, enabled: values.enabled ? 1 : 0, push: true });
    setBusy('tpl-save');
    try {
      const res = tplModal.mode === 'edit'
        ? await api('mtproto/templates/' + tplModal.row.id, { method: 'PUT', body })
        : await apiPost('mtproto/templates', body);
      if (res.async) {
        message.success('模板已保存，正在后台重渲染节点（任务 #' + res.job_id + '）');
      } else if (tplModal.mode === 'edit') {
        const ok = Number(res.applied_ok || 0);
        const total = (res.applied || []).length;
        message.success(`模板已保存，已重渲染 ${ok}/${total} 个绑定节点`);
      } else {
        message.success('模板已创建');
      }
      setTplModal(null);
      reloadAll();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setBusy('');
    }
  };

  const applyTemplate = async (row) => {
    setBusy('tpl-apply-' + row.id);
    try {
      const res = await apiPost('mtproto/templates/' + row.id + '/apply', { push: true });
      if (watchBatch(res)) { message.success('已创建后台任务 #' + res.job_id + '，进度见右上角「任务」'); reloadAll(); return; }
      const failed = (res.data || []).filter((x) => x.ok === false);
      if (failed.length) message.warning(`已下发 ${res.ok_count || 0}/${res.total || 0}，失败 ${failed.length} 个`);
      else message.success(`已按模板「${row.name}」下发 ${res.ok_count || 0}/${res.total || 0} 个节点`);
      reloadAll();
    } catch (e) {
      message.error(e.message || '下发失败');
    } finally {
      setBusy('');
    }
  };

  const deleteTemplate = async (row) => {
    try {
      await api('mtproto/templates/' + row.id, { method: 'DELETE' });
      message.success('模板已删除');
      reloadAll();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  /* ---------------- 节点 ---------------- */
  const saveNode = async () => {
    const values = await readForm(nodeForm);
    if (!values) return;
    const body = Object.assign({}, values, { enabled: values.enabled ? 1 : 0, auto_deploy: true });
    setBusy('node-save');
    try {
      const res = nodeModal.mode === 'edit'
        ? await api('mtproto/nodes/' + nodeModal.row.id, { method: 'PUT', body })
        : await apiPost('mtproto/nodes', body);
      if (res.deployed && res.deployed.ok === false) message.warning('已保存，但下发失败：' + res.deployed.error);
      else message.success(nodeModal.mode === 'edit' ? '节点已保存' : '节点已创建，正在排队下发');
      setNodeModal(null);
      reloadAll();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setBusy('');
    }
  };

  const applyNode = async (row) => {
    setBusy('apply-' + row.id);
    try {
      const res = await apiPost('mtproto/nodes/' + row.id + '/apply', { push: true });
      message.success(`「${row.name}」已重新渲染${res.changed ? '（有变更）' : '（无变更）'}${res.queued && res.queued.ok ? '，已排队下发' : ''}`);
      reloadAll();
      if (drawerNode && drawerNode.id === row.id) loadClients(row, revealed);
    } catch (e) {
      message.error(e.message || '下发失败');
    } finally {
      setBusy('');
    }
  };

  const applyAll = () => {
    modal.confirm({
      title: '下发全部 MTProto 节点？',
      content: '按当前模板与授权重新渲染所有节点，并排队下发；客户端会短暂重连。',
      okText: '立即下发',
      onOk: async () => {
        setBusy('apply-all');
        try {
          const res = await apiPost('mtproto/apply-all', {});
          if (watchBatch(res)) { message.success('已创建后台任务 #' + res.job_id + '（' + (res.total || 0) + ' 项）'); reloadAll(); return; }
          message.success(`已下发 ${res.ok_count || 0}/${res.total || 0} 个节点`);
          reloadAll();
        } catch (e) {
          message.error(e.message || '下发失败');
        } finally {
          setBusy('');
        }
      },
    });
  };

  const deleteNode = async (row) => {
    try {
      await api('mtproto/nodes/' + row.id, { method: 'DELETE' });
      message.success('节点已删除');
      reloadAll();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const showPreview = async (row) => {
    try {
      const res = await apiPost('mtproto/nodes/' + row.id + '/preview', {});
      setPreview({ title: `mtg 配置预览 · ${row.name}`, content: res.content || '' });
    } catch (e) {
      message.error(e.message || '预览失败');
    }
  };

  const addClient = async () => {
    if (!drawerNode) return;
    if (!grant.uuid) return message.warning('请选择要授权的 UUID');
    setGranting(true);
    try {
      const res = await apiPost('mtproto/nodes/' + drawerNode.id + '/clients', {
        uuid: grant.uuid,
        label: grant.label,
        send_mail: !!grant.send_mail,
      });
      if (res.mailed && res.mailed.ok === false) message.warning('已授权，但邮件发送失败：' + res.mailed.error);
      else if (grant.send_mail) message.success('已授权，链接已发送到用户邮箱');
      else message.success('已授权，链接已写入节点配置');
      setGrant({ uuid: '', label: '', send_mail: false });
      loadClients(drawerNode, false);
      reloadAll();
    } catch (e) {
      message.error(e.message || '授权失败');
    } finally {
      setGranting(false);
    }
  };

  const clientAction = async (row, action) => {
    if (!drawerNode) return;
    setBusy(action + '-' + row.id);
    try {
      if (action === 'link') {
        const res = await apiPost('mtproto/clients/' + row.id + '/link', {});
        setLinkModal({ title: `Telegram 链接 · ${row.email || shortUuid(row.uuid)}`, tg: res.tg, tme: res.tme });
      } else if (action === 'mail') {
        await apiPost('mtproto/clients/' + row.id + '/send-mail', {});
        message.success('已发送到 ' + (row.email || '绑定邮箱'));
        loadClients(drawerNode, revealed);
      } else if (action === 'rotate') {
        await apiPost('mtproto/clients/' + row.id + '/rotate', {});
        message.success('已轮换 secret，旧链接立即失效');
        loadClients(drawerNode, revealed);
      } else if (action === 'revoke') {
        await apiPost('mtproto/clients/' + row.id + '/revoke', {});
        message.success('已撤销，该用户链接立即失效');
        loadClients(drawerNode, revealed);
        reloadAll();
      } else if (action === 'restore') {
        await apiPost('mtproto/clients/' + row.id + '/restore', {});
        message.success('已重新授权（生成新 secret）');
        loadClients(drawerNode, revealed);
        reloadAll();
      }
    } catch (e) {
      message.error(e.message || '操作失败');
    } finally {
      setBusy('');
    }
  };

  /* ---------------- 表格列 ---------------- */
  const tplColumns = [
    {
      title: '模板',
      dataIndex: 'name',
      width: 200,
      render: (v, row) => (
        <div>
          <b>{v}</b>
          <div className="pp-muted" style={{ fontSize: 12 }}>{row.remark || `#${row.id}`}</div>
        </div>
      ),
    },
    {
      title: '监听端口',
      dataIndex: 'port',
      width: 110,
      render: (v, row) => (
        <span className="pp-mono">
          {v}
          <span className="pp-muted" style={{ fontSize: 12 }}> / 统计 {row.api_port || 13719}</span>
        </span>
      ),
    },
    { title: '模式', dataIndex: 'secret_mode', width: 110, render: (v) => <Tag color={v === 'single' ? 'orange' : 'purple'}>{MODE_LABEL[v] || v}</Tag> },
    { title: '绑定节点', dataIndex: 'node_count', width: 92, render: (v) => <Tag color={Number(v) ? 'geekblue' : 'default'}>{Number(v)} 个</Tag> },
    { title: '更新时间', dataIndex: 'updated_at', width: 108, render: (v) => <span className="pp-muted">{fmtAgo(v)}</span> },
    {
      title: '操作',
      width: 150,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0}>
          <IconAction title="编辑模板（保存后自动重渲染绑定节点）" icon={<EditOutlined />} onClick={() => setTplModal({ mode: 'edit', row })} />
          <Tooltip title="按该模板重新下发全部绑定节点">
            <Button
              type="text"
              size="small"
              icon={<CloudUploadOutlined />}
              disabled={!Number(row.node_count)}
              loading={busy === 'tpl-apply-' + row.id}
              onClick={() => applyTemplate(row)}
              aria-label="下发"
            />
          </Tooltip>
          <Popconfirm
            title="删除该模板？"
            description="仍有节点绑定时会被后端拒绝。"
            okText="删除"
            okButtonProps={{ danger: true }}
            onConfirm={() => deleteTemplate(row)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const nodeColumns = [
    {
      title: '节点',
      dataIndex: 'name',
      width: 170,
      render: (v, row) => (
        <div>
          <b>{v}</b>
          <div className="pp-muted" style={{ fontSize: 12 }}>{row.remark || `#${row.id}`}</div>
        </div>
      ),
    },
    {
      title: '服务器',
      width: 140,
      render: (_, row) => (
        <div>
          <div style={{ color: '#334155' }}>{row.server_name || '—'}</div>
          <div className="pp-mono pp-muted" style={{ fontSize: 12 }}>{row.server_ip4 || row.server_host || ''}</div>
        </div>
      ),
    },
    {
      title: '连接地址',
      width: 160,
      render: (_, row) => (row.host ? <span className="pp-mono">{row.host}</span> : <span className="pp-muted">自动（服务器 IP）</span>),
    },
    {
      title: '模板',
      dataIndex: 'template_name',
      width: 140,
      render: (v) => (v ? <Tag color="purple">{v}</Tag> : <span className="pp-muted">—</span>),
    },
    {
      title: '伪装域名',
      dataIndex: 'domain',
      width: 220,
      ellipsis: true,
      render: (v, row) => (v ? <span className="pp-mono">{v}{inheritTag(row.overrides && row.overrides.domain)}</span> : <span className="pp-muted">—</span>),
    },
    {
      title: '端口',
      width: 130,
      render: (_, row) => (
        <span className="pp-mono">
          {row.port}
          {inheritTag(row.overrides && row.overrides.port)}
          <span className="pp-muted" style={{ fontSize: 12 }}> / 统计 {row.api_port}</span>
        </span>
      ),
    },
    { title: '授权', dataIndex: 'client_count', width: 80, render: (v) => <Tag color={Number(v) ? 'geekblue' : 'default'}>{Number(v)} 人</Tag> },
    { title: '服务', dataIndex: 'service_state', width: 92, render: (v) => serviceTag(v) },
    { title: '最近下发', dataIndex: 'last_apply_at', width: 104, render: (v) => <span className="pp-muted">{v ? fmtAgo(v) : '未下发'}</span> },
    {
      title: '操作',
      width: 190,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0}>
          <IconAction title="授权用户 / 管理链接" icon={<TeamOutlined />} onClick={() => openClients(row)} />
          <IconAction title="查看渲染后的 mtg 配置" icon={<EyeOutlined />} onClick={() => showPreview(row)} />
          <IconAction title="立即下发" icon={<CloudUploadOutlined />} onClick={() => applyNode(row)} disabled={busy === 'apply-' + row.id} />
          <IconAction title="编辑" icon={<EditOutlined />} onClick={() => setNodeModal({ mode: 'edit', row })} />
          <Popconfirm
            title="删除该 MTProto 节点？"
            description="会同时删除已下发的配置与该节点上的全部授权。"
            okText="删除"
            okButtonProps={{ danger: true }}
            onConfirm={() => deleteNode(row)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const clientColumns = [
    {
      title: '用户',
      width: 240,
      render: (_, row) => (
        <div>
          <div className="pp-mono" style={{ fontSize: 12 }}>{row.email || '—'}</div>
          <Tooltip title={row.uuid}>
            <span className="pp-muted" style={{ fontSize: 12 }}>{shortUuid(row.uuid)}{row.label ? ' · ' + row.label : ''}</span>
          </Tooltip>
        </div>
      ),
    },
    {
      title: 'secret',
      dataIndex: 'secret',
      width: 190,
      render: (v, row) => (
        <span className="pp-mono" style={{ fontSize: 12 }}>
          {v || '—'}
          {row.secret_domain ? <span className="pp-muted"> · {row.secret_domain}</span> : null}
        </span>
      ),
    },
    { title: '状态', dataIndex: 'status', width: 84, render: (v) => (v === 'active' ? <Tag color="success">生效</Tag> : <Tag>已撤销</Tag>) },
    { title: '创建', dataIndex: 'created_at', width: 104, render: (v) => <span className="pp-muted">{fmtAgo(v)}</span> },
    {
      title: '操作',
      width: 190,
      render: (_, row) => (
        <Space size={0}>
          <Tooltip title="查看链接 / 二维码">
            <Button type="text" size="small" icon={<QrcodeOutlined />} disabled={row.status !== 'active'} onClick={() => clientAction(row, 'link')} aria-label="链接" />
          </Tooltip>
          <Tooltip title="发送到绑定邮箱">
            <Button type="text" size="small" icon={<MailOutlined />} disabled={row.status !== 'active' || busy === 'mail-' + row.id} onClick={() => clientAction(row, 'mail')} aria-label="发邮件" />
          </Tooltip>
          <Tooltip title="轮换 secret（旧链接立即失效）">
            <Button type="text" size="small" icon={<SwapOutlined />} disabled={row.status !== 'active'} onClick={() => clientAction(row, 'rotate')} aria-label="轮换" />
          </Tooltip>
          {row.status === 'active' ? (
            <Popconfirm title="撤销该用户的授权？" description="只断该用户，其他用户不受影响。" okText="撤销" okButtonProps={{ danger: true }} onConfirm={() => clientAction(row, 'revoke')}>
              <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="撤销" />
            </Popconfirm>
          ) : (
            <Tooltip title="重新授权（生成新 secret）">
              <Button type="text" size="small" icon={<PlusOutlined />} onClick={() => clientAction(row, 'restore')} aria-label="重新授权" />
            </Tooltip>
          )}
        </Space>
      ),
    },
  ];

  const nodePane = (
    <>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="节点默认继承模板的端口与模式；伪装域名请在节点上单独填写。授权链接不会写进订阅。"
      />
      <Table
        className="pp-table"
        rowKey="id"
        size="medium"
        loading={nodes.loading}
        dataSource={nodeRows}
        columns={nodeColumns}
        scroll={{ x: 1280 }}
        pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 个节点` }}
        locale={{ emptyText: <EmptyBlock text="还没有 MTProto 节点：先在「协议模板」里确认参数，再点右上角「新建节点」" /> }}
      />
    </>
  );

  const tplPane = (
    <>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="改模板会立即按新参数重渲染全部绑定节点（并排队下发）；节点上的显式覆盖优先于模板。"
      />
      <Table
        className="pp-table"
        rowKey="id"
        size="medium"
        loading={templates.loading}
        dataSource={tplRows}
        columns={tplColumns}
        scroll={{ x: 1040 }}
        pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 个模板` }}
        locale={{ emptyText: <EmptyBlock text="还没有协议模板：点右上角「新建模板」" /> }}
      />
    </>
  );

  return (
    <>

      <Tabs
        defaultActiveKey="templates"
          tabBarStyle={templatesOnly ? { display: 'none' } : undefined}
        items={([
          {
            key: 'templates',
            label: `协议模板（${tplRows.length}）`,
            children: (
              <PageCard
                title={<span className="pp-section-title">MTProto 协议模板</span>}
                extra={
                  <Space wrap>
                    <Button icon={<ReloadOutlined />} onClick={() => templates.reload().catch(() => {})} loading={templates.loading}>
                      刷新
                    </Button>
                    <Button type="primary" icon={<PlusOutlined />} onClick={() => setTplModal({ mode: 'create', row: null })}>
                      新建模板
                    </Button>
                  </Space>
                }
              >
                {nodes.error || templates.error ? (
                  <div style={{ marginBottom: 12 }}>
                    <ErrorBlock error={nodes.error || templates.error} onRetry={reloadAll} />
                  </div>
                ) : null}
                {tplPane}
              </PageCard>
            ),
          },
          {
            key: 'nodes',
            label: `节点绑定（${nodeRows.length}）`,
            children: (
              <PageCard
                title={<span className="pp-section-title">MTProto 节点绑定</span>}
                extra={
                  <Space wrap>
                    <Button icon={<ReloadOutlined />} onClick={() => nodes.reload().catch(() => {})} loading={nodes.loading}>
                      刷新
                    </Button>
                    <Button icon={<CloudUploadOutlined />} loading={busy === 'apply-all'} onClick={applyAll}>
                      全部下发
                    </Button>
                    <Button type="primary" icon={<PlusOutlined />} onClick={() => setNodeModal({ mode: 'create', row: null })}>
                      新建节点
                    </Button>
                  </Space>
                }
              >
                {nodes.error || templates.error ? (
                  <div style={{ marginBottom: 12 }}>
                    <ErrorBlock error={nodes.error || templates.error} onRetry={reloadAll} />
                  </div>
                ) : null}
                {nodePane}
              </PageCard>
            ),
          },
        ]).filter((it) => !templatesOnly || it.key === 'templates')}
      />

      {/* 节点表单 */}
      <Modal
        open={!!nodeModal}
        title={nodeModal && nodeModal.mode === 'edit' ? `编辑 MTProto 节点 #${nodeModal.row.id}` : '新建 MTProto 节点'}
        onCancel={() => setNodeModal(null)}
        onOk={saveNode}
        okText={nodeModal && nodeModal.mode === 'edit' ? '保存并下发' : '创建并下发'}
        confirmLoading={busy === 'node-save'}
        width={720}
        destroyOnClose
      >
        {nodeModal ? <NodeForm form={nodeForm} mode={nodeModal.mode} row={nodeModal.row} servers={serverRows} templates={tplRows} /> : null}
      </Modal>

      {/* 模板表单 */}
      <Modal
        open={!!tplModal}
        title={tplModal && tplModal.mode === 'edit' ? `编辑协议模板 #${tplModal.row.id}` : '新建协议模板'}
        onCancel={() => setTplModal(null)}
        onOk={saveTemplate}
        okText={tplModal && tplModal.mode === 'edit' ? '保存并重渲染绑定节点' : '创建'}
        confirmLoading={busy === 'tpl-save'}
        width={720}
        destroyOnClose
      >
        {tplModal ? <TemplateForm form={tplForm} mode={tplModal.mode} row={tplModal.row} /> : null}
      </Modal>

      {/* 授权抽屉 */}
      <Drawer open={!!drawerNode} onClose={() => setDrawerNode(null)} width={860} destroyOnClose title={drawerNode ? `授权用户 · ${drawerNode.name}` : ''}>
        {drawerNode ? (
          <>
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 14 }}
              message={drawerNode.secret_mode === 'single' ? '整节点共享模式：所有用户拿到同一条链接，轮换 / 撤销会影响全部用户。' : '每个用户一条独立 secret：撤销或轮换只影响该用户，其他人不掉线。'}
              description={
                <span className="pp-muted">
                  端口 {drawerNode.port}（{drawerNode.overrides && drawerNode.overrides.port ? '继承模板' : '节点覆盖'}）· 伪装域名 {drawerNode.domain || '未设置'}。链接即凭证，不进订阅。
                </span>
              }
            />

            <Space wrap style={{ marginBottom: 14 }}>
              <Select
                style={{ width: 280 }}
                showSearch
                optionFilterProp="label"
                placeholder="选择要授权的 UUID"
                value={grant.uuid || undefined}
                onChange={(v) => setGrant((s) => Object.assign({}, s, { uuid: v }))}
                options={uuidRows.map((u) => ({ value: u.uuid, label: `${u.email || u.owner || '未绑定邮箱'} · ${shortUuid(u.uuid)}` }))}
              />
              <Input style={{ width: 160 }} placeholder="备注（可选）" value={grant.label} onChange={(e) => setGrant((s) => Object.assign({}, s, { label: e.target.value }))} />
              <Space size={6}>
                <Switch checked={grant.send_mail} onChange={(v) => setGrant((s) => Object.assign({}, s, { send_mail: v }))} />
                <span className="pp-muted">同时发邮件</span>
              </Space>
              <Button type="primary" icon={<PlusOutlined />} loading={granting} onClick={addClient}>
                授权
              </Button>
              <Button icon={<ReloadOutlined />} onClick={() => loadClients(drawerNode, false)}>
                刷新
              </Button>
              {revealed ? (
                <Button icon={<EyeOutlined />} onClick={() => loadClients(drawerNode, false)}>
                  隐藏 secret
                </Button>
              ) : (
                <Button
                  icon={<EyeOutlined />}
                  onClick={() => {
                    modal.confirm({
                      title: '显示 secret 明文？',
                      content: '本次查看会写入下发日志（操作人 + 节点 + 用户）。',
                      okText: '显示',
                      onOk: () => loadClients(drawerNode, true),
                    });
                  }}
                >
                  显示 secret
                </Button>
              )}
            </Space>

            {clientError ? <ErrorBlock error={clientError} onRetry={() => loadClients(drawerNode, revealed)} /> : null}
            <Table
              className="pp-table"
              rowKey="id"
              size="small"
              loading={clientLoading}
              dataSource={clientRows}
              columns={clientColumns}
              pagination={{ pageSize: 8, showTotal: (t) => `共 ${t} 条授权` }}
              locale={{ emptyText: <EmptyBlock text="还没有授权用户：从上方选一个 UUID，点「授权」" /> }}
            />
          </>
        ) : null}
      </Drawer>

      {/* 链接 / 二维码 */}
      <Modal open={!!linkModal} title={linkModal ? linkModal.title : ''} footer={null} onCancel={() => setLinkModal(null)} width={520} destroyOnClose>
        {linkModal ? (
          <div style={{ textAlign: 'center' }}>
            <div style={{ display: 'inline-block', padding: 10, background: '#fff', borderRadius: 12 }}>
              <QRCode value={linkModal.tg} size={220} type="svg" errorLevel="M" bordered={false} />
            </div>
            <p className="pp-muted" style={{ marginTop: 10 }}>
              Telegram → 设置 → 数据和存储 → 代理 → 添加代理，或直接用手机扫上面的码。
            </p>
            <Divider style={{ margin: '12px 0' }} />
            <div style={{ textAlign: 'left', wordBreak: 'break-all' }}>
              <div style={{ marginBottom: 8 }}>
                <div className="pp-muted" style={{ fontSize: 12 }}>tg:// 链接</div>
                <CopyText value={linkModal.tg} />
              </div>
              <div>
                <div className="pp-muted" style={{ fontSize: 12 }}>t.me 链接（转发给自己再点开）</div>
                <CopyText value={linkModal.tme} />
              </div>
            </div>
          </div>
        ) : null}
      </Modal>

      {/* 配置预览 */}
      <Modal open={!!preview} title={preview ? preview.title : ''} footer={null} onCancel={() => setPreview(null)} width={820} destroyOnClose>
        <pre className="pp-json" style={{ maxHeight: 520, whiteSpace: 'pre-wrap' }}>{preview ? preview.content : ''}</pre>
      </Modal>
    </>
  );
}

/* ---------------- 节点表单（只填覆盖项，其余继承模板）---------------- */
function NodeForm({ form, mode, row, servers, templates }) {
  const initial = useMemo(() => {
    if (mode === 'edit' && row) {
      return {
        name: row.name,
        server_id: row.server_id,
        template_id: row.template_id || undefined,
        host: row.host || '',
        domain: row.overrides && row.overrides.domain ? '' : row.domain,
        port: row.overrides && row.overrides.port ? undefined : row.port,
        api_port: row.overrides && row.overrides.api_port ? undefined : row.api_port,
        remark: row.remark || '',
        enabled: !!row.enabled,
      };
    }
    return { enabled: true };
  }, [mode, row]);

  return (
    <Form form={form} layout="vertical" initialValues={initial} preserve={false}>
      <Row gutter={12}>
        <Col span={10}>
          <Form.Item name="name" label="节点名称" rules={[{ required: true, message: '给节点起个名字' }]}>
            <Input placeholder="例如 hk-tg" maxLength={64} />
          </Form.Item>
        </Col>
        <Col span={14}>
          <Form.Item name="server_id" label="服务器" rules={[{ required: true, message: '选择服务器' }]}>
            <Select showSearch optionFilterProp="label" placeholder="选择一台已接入 Agent 的服务器" options={servers.map((s) => ({ value: s.id, label: `${s.name} · ${s.host || s.last_ip4 || ''}` }))} />
          </Form.Item>
        </Col>
      </Row>
      <Form.Item name="template_id" label="协议模板" rules={[{ required: true, message: '选择协议模板' }]} extra="监听端口（默认 13709）、伪装域名、模式等默认从模板继承">
        <Select
          placeholder="选择协议模板"
          options={templates.map((t) => ({ value: t.id, label: t.name + ' · ' + t.port }))}
        />
      </Form.Item>
      <Row gutter={12}>
        <Col span={16}>
          <Form.Item
            name="host"
            label="连接地址（域名或 IP，可选）"
            extra="用于生成 tg:// 链接与 mtg 的 public-ipv4；留空则使用服务器上报的公网 IP"
          >
            <Input placeholder="tg.example.com 或 203.0.113.10" />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={12}>
        <Col span={10}>
          <Form.Item
            name="domain"
            label="伪装域名（FakeTLS 的 SNI）"
            rules={[{ required: true, message: '请填写伪装域名' }]}
            extra="每个节点单独填写；建议与服务器 IP 归属地相符，只能填域名，不能填 IP"
          >
            <Input placeholder="storage.googleapis.com" />
          </Form.Item>
        </Col>
        <Col span={7}>
          <Form.Item name="port" label="端口（可选，留空继承）">
            <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="继承模板" />
          </Form.Item>
        </Col>
        <Col span={7}>
          <Form.Item name="api_port" label="统计端口（可选）">
            <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="继承模板 / 默认 13719" />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={12}>
        <Col span={16}>
          <Form.Item name="remark" label="备注">
            <Input maxLength={200} />
          </Form.Item>
        </Col>
        <Col span={8}>
          <Form.Item name="enabled" label="启用该节点" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Col>
      </Row>
    </Form>
  );
}

/* ---------------- 模板表单 ---------------- */
function TemplateForm({ form, mode, row }) {
  const initial = useMemo(() => {
    if (mode === 'edit' && row) {
      return {
        name: row.name,
        remark: row.remark || '',
        domain: row.domain || '',
        port: row.port,
        api_port: row.api_port || undefined,
        secret_mode: row.secret_mode,
        concurrency: row.concurrency,
        prefer_ip: row.prefer_ip,
        public_ipv4: row.public_ipv4 || '',
        blocklist: !!row.blocklist,
        throttle_max: row.throttle_max,
        enabled: !!row.enabled,
      };
    }
    return { port: 13709, secret_mode: 'multi', concurrency: 8192, prefer_ip: 'prefer-ipv4', blocklist: false, throttle_max: 0, enabled: true };
  }, [mode, row]);

  return (
    <Form form={form} layout="vertical" initialValues={initial} preserve={false}>
      <Row gutter={12}>
        <Col span={10}>
          <Form.Item name="name" label="模板名称" rules={[{ required: true, message: '给模板起个名字' }]}>
            <Input placeholder="例如 Telegram 默认" maxLength={64} />
          </Form.Item>
        </Col>
        <Col span={7}>
          <Form.Item name="port" label="监听端口（TCP）" rules={[{ required: true }]} extra="默认 13709">
            <InputNumber min={1} max={65535} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
        <Col span={7}>
          <Form.Item name="api_port" label="统计端口（可选）" extra="留空 = 13719">
            <InputNumber min={1} max={65535} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={12}>
        <Col span={8}>
          <Form.Item name="secret_mode" label="授权模式">
            <Select options={[{ value: 'multi', label: '每用户独立' }, { value: 'single', label: '整节点共享' }]} />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={12}>
        <Col span={8}>
          <Form.Item name="concurrency" label="最大并发连接">
            <InputNumber min={1} max={500000} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
        <Col span={8}>
          <Form.Item name="prefer_ip" label="出站 IP 偏好">
            <Select options={PREFER_OPTIONS} />
          </Form.Item>
        </Col>
        <Col span={8}>
          <Form.Item name="throttle_max" label="每用户连接上限（0=不限）">
            <InputNumber min={0} max={500000} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={12}>
        <Col span={12}>
          <Form.Item name="blocklist" label="启用 IP 黑名单" valuePropName="checked" extra="内网 / 家宽会误杀，默认关">
            <Switch />
          </Form.Item>
        </Col>
        <Col span={12}>
          <Form.Item name="enabled" label="启用该模板" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Col>
      </Row>
      <Form.Item name="remark" label="备注">
        <Input maxLength={200} />
      </Form.Item>
    </Form>
  );
}
