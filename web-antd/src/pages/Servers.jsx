import React, { useMemo, useState } from 'react';
import {
  CheckCircleOutlined,
  CloudDownloadOutlined,
  CodeOutlined,
  CloudServerOutlined,
  DeleteOutlined,
  DisconnectOutlined,
  EditOutlined,
  KeyOutlined,
  PlusOutlined,
  PoweroffOutlined,
  QuestionCircleOutlined,
  ReloadOutlined,
  SearchOutlined,
  SyncOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { api, apiPost } from '../api';
import { useAsync, readForm } from '../hooks';
import { CopyText, ErrorBlock, IconAction, JsonView, PageCard, StatCard, StatusPill, Toolbar } from '../components';
import { watchBatch } from '../components/JobCenter';
import RemoteShell from '../components/RemoteShell';
import { fmtAgo, fmtNum, fmtTime, fmtUptime, splitIps } from '../format';

/* SSH 状态展示：区分「已启用 / 已启用但没跑 / 手动运行但没自启 / 已停用」 */
function sshUi(s) {
  if (!s) return null;
  if (s.enabled === 'not-installed') {
    return { text: '未安装', color: 'default', enabled: false, active: false, toggleToEnable: true };
  }
  const enabled = s.enabled === 'enabled';
  const active = s.active === 'active';
  if (enabled && active) return { text: '已启用', color: 'green', enabled, active, toggleToEnable: false };
  if (enabled) return { text: '已启用 · 未运行', color: 'orange', enabled, active, toggleToEnable: false };
  if (active) return { text: '运行中 · 未自启', color: 'gold', enabled, active, toggleToEnable: false };
  return { text: '已停用', color: 'orange', enabled, active, toggleToEnable: true };
}
import { chartPalette } from '../theme';

export default function Servers() {
  const { message } = AntdApp.useApp();
  const [q, setQ] = useState('');
  const [status, setStatus] = useState('');
  const [compose, setCompose] = useState(false);
  const [saving, setSaving] = useState(false);
  const [result, setResult] = useState(null); // { title, command, token, note, error, serverId }
  const [disableSsh, setDisableSsh] = useState(false);
  const [tasksFor, setTasksFor] = useState(null);
  const [editFor, setEditFor] = useState(null);
  const [checking, setChecking] = useState(false);
  const [busy, setBusy] = useState('');
  const [selected, setSelected] = useState([]);
  const [pushing, setPushing] = useState(false);
  const [latestAgent, setLatestAgent] = useState('');
  const [execFor, setExecFor] = useState(null);
  const [form] = Form.useForm();

  const { data, loading, error, reload } = useAsync(
    () => api('servers', { query: { q, status } }),
    [q, status]
  );

  // 监控快照里的 SSH 状态（只读）：用于列表展示与启停按钮文案
  const monitor = useAsync(() => api('servers/monitor'), []);
  const sshMap = useMemo(() => {
    const map = new Map();
    ((monitor.data && monitor.data.servers) || []).forEach((s) => map.set(s.id, s.ssh || null));
    return map;
  }, [monitor.data]);

  /* 最新 Agent 版本（桶里的 manifest.json），用于按钮提示 */
  React.useEffect(() => {
    let alive = true;
    api('agent/latest')
      .then((r) => {
        if (alive) setLatestAgent(String((r && r.latest) || ''));
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  const rows = (data && data.data) || [];
  const counts = useMemo(() => {
    const c = { online: 0, offline: 0, unknown: 0 };
    rows.forEach((r) => {
      const s = String(r.last_status || 'unknown');
      if (s === 'online') c.online += 1;
      else if (s === 'offline') c.offline += 1;
      else c.unknown += 1;
    });
    return c;
  }, [rows]);

  const create = async () => {
    const values = await readForm(form);
    if (!values) return;
    setSaving(true);
    try {
      const res = await api('servers', { method: 'POST', body: values });
      message.success('服务器已创建，请复制安装命令到目标机器执行');
      setCompose(false);
      form.resetFields();
      setResult({
        title: `服务器「${values.name}」的接入命令`,
        command: res.install_command,
        token: res.agent_token,
        note: res.message,
      });
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  const showInstall = async (row) => {
    try {
      const res = await api(`servers/${row.id}/agent-token` + (disableSsh ? '?disable_ssh=1' : ''), {
        method: 'POST',
        body: {},
      });
      setResult({
        title: `服务器「${row.name}」的新接入密钥`,
        command: res.install_command,
        token: res.agent_token,
        note: res.message,
        serverId: row.id,
        disable_ssh: !!disableSsh,
      });
      reload().catch(() => {});
    } catch (e) {
      setResult({
        title: `服务器「${row.name}」`,
        error: e.message,
        note: e.payload && e.payload.token_hint ? `当前密钥指纹：${e.payload.token_hint}` : '',
        serverId: row.id,
      });
    }
  };

  const toggleDisableSsh = async (next) => {
    setDisableSsh(next);
    const sid = result && result.serverId;
    if (!sid) return;
    try {
      const res = await api(`servers/${sid}/agent-key` + (next ? '?disable_ssh=1' : ''));
      setResult((r) => Object.assign({}, r, { command: res.install_command, disable_ssh: next }));
    } catch (e) {
      message.error(e.message || '刷新安装命令失败');
    }
  };

  /* 单台更新 agent（老版只在有更新时显示这个按钮） */
  const pushOne = async (row) => {
    setBusy('push-' + row.id);
    try {
      const r = await api('agent/push-update', { method: 'POST', body: { server_ids: [row.id] } });
      if (watchBatch(r)) { message.success('已创建后台更新任务 #' + r.job_id + '，进度见右上角「任务」'); return; }
      const x = ((r && r.results) || [])[0] || {};
      if (!x.ok) message.error(`${row.name}：${x.error || '推送失败'}`);
      else if (x.skipped) message.info(`${row.name} 已是最新（${(r && r.latest) || ''}）`);
      else message.success(`${row.name} 已排队更新到 ${(r && r.latest) || ''}`);
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '推送失败');
    } finally {
      setBusy('');
    }
  };

  /* 更新 cloudflared（后台任务，任务中心可见进度） */
  const updateCloudflared = async (row) => {
    try {
      const r = await apiPost(`servers/${row.id}/update-cloudflared`, {});
      if (r && r.job_id) {
        message.success(`已创建后台任务 #${r.job_id}（更新 cloudflared），进度见右上角「任务」`);
      } else {
        message.success((r && r.message) || 'cloudflared 更新任务已下发');
      }
    } catch (e) {
      message.error(e.message || '更新 cloudflared 失败');
    }
  };
  /* 远程终端（「命令」按钮）—— 交互式输入输出，见 RemoteShell */
  const openExec = (row) => setExecFor(row);

  const remove = async (row) => {
    try {
      await api(`servers/${row.id}`, { method: 'DELETE' });
      message.success('服务器已删除');
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  /* SSH 启停：统一走后台任务（单台也建一个 1 项任务，进度在「任务」里看） */
  const sshToggle = async (ids, enable) => {
    const list = (ids || []).filter(Boolean);
    if (!list.length) return message.warning('请先勾选服务器');
    try {
      const res = await apiPost('jobs', { kind: 'ssh.toggle', params: { server_ids: list, enable: !!enable } });
      if (watchBatch(res)) message.success('已创建后台任务 #' + res.job_id + '（' + list.length + ' 台）');
      setSelected([]);
      setTimeout(() => monitor.reload().catch(() => {}), 1500);
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  /* 批量升级 Agent：有勾选就升级勾选的，没有就升级全部（后端 all: true） */
  const pushUpdate = async () => {
    setPushing(true);
    try {
      const body = selected.length ? { server_ids: selected } : { all: true };
      const r = await api('agent/push-update', { method: 'POST', body });
      if (watchBatch(r)) {
        message.success('已创建后台更新任务 #' + r.job_id + '（' + (r.total || 0) + ' 台），进度见右上角「任务」');
        setSelected([]);
        return;
      }
      const list = (r && r.results) || [];
      const ok = list.filter((x) => x.ok && !x.skipped);
      const skipped = list.filter((x) => x.skipped);
      const bad = list.filter((x) => !x.ok);
      let msg = `已排队 ${ok.length} 台（最新 ${(r && r.latest) || '-'}）`;
      if (skipped.length) msg += `，${skipped.length} 台已是最新`;
      if (bad.length) msg += `，失败：${bad.map((x) => `${x.name}（${x.error || '未知错误'}）`).join('，')}`;
      if (bad.length) message.error(msg);
      else message.success(msg);
      setSelected([]);
      reload().catch(() => {});
    } catch (e) {
      message.error(e.message || '推送失败');
    } finally {
      setPushing(false);
    }
  };

  const reboot = async (row) => {
    try {
      const res = await apiPost(`servers/${row.id}/reboot`, {});
      message.success(res.message || '重启指令已下发');
    } catch (e) {
      message.error(e.message || '重启失败');
    }
  };

  const testSelected = async () => {
    if (!selected.length) {
      message.warning('请先勾选要探测的服务器');
      return;
    }
    setChecking(true);
    let ok = 0;
    for (const id of selected) {
      try {
        await apiPost(`servers/${id}/test`, {});
        ok += 1;
      } catch (e) {
        /* 单台失败不影响其它 */
      }
    }
    setChecking(false);
    message.success(`已发起 ${ok}/${selected.length} 台探测`);
    reload().catch(() => {});
  };

  const columns = [
    {
      title: '服务器',
      dataIndex: 'name',
      width: 210,
      render: (v, row) => (
        <div>
          <div style={{ fontWeight: 600, color: '#0f172a' }}>
            {v}
            {row.link_mode === 'local' ? <Tag color="purple" style={{ marginLeft: 6 }}>本地</Tag> : null}
          </div>
          <div className="pp-muted" style={{ fontSize: 12 }}>
            {row.tags ? row.tags : row.remark || `#${row.id}`}
          </div>
        </div>
      ),
    },
    {
      title: '状态',
      dataIndex: 'last_status',
      width: 100,
      render: (v, row) => (
        <Tooltip title={row.last_error || undefined}>
          <span>
            <StatusPill status={v} />
          </span>
        </Tooltip>
      ),
    },
    {
      title: 'SSH',
      width: 92,
      render: (_, row) => {
        const st = sshUi(sshMap.get(row.id));
        if (!st) return <span className="pp-muted">—</span>;
        return <Tag color={st.color} style={{ marginInlineEnd: 0 }}>{st.text}</Tag>;
      },
    },
    {
      title: '地址',
      width: 190,
      render: (_, row) => {
        const { v4, v6 } = splitIps(row);
        return (
          <div>
            {v4 ? (
              <div className="pp-mono" style={{ color: '#334155', fontSize: 12 }}>
                {v4}
              </div>
            ) : null}
            {v6 ? (
              <div className="pp-mono" style={{ color: '#64748b', fontSize: 11.5 }}>
                {v6}
              </div>
            ) : null}
            {!v4 && !v6 ? (
              <div className="pp-mono" style={{ color: '#334155' }}>
                {row.host || '—'}
              </div>
            ) : null}
            {row.ip_country ? (
              <div className="pp-muted" style={{ fontSize: 11.5 }}>
                {row.ip_country} {row.ip_region}
              </div>
            ) : null}
          </div>
        );
      },
    },
    {
      title: 'Agent 版本',
      dataIndex: 'agent_version',
      width: 130,
      render: (v, row) => row.link_mode === 'local' ? <Tag color="purple">本地直控</Tag> : (v ? <Tag color="blue">{v}</Tag> : <span className="pp-muted">未上报</span>),
    },
    {
      title: '最后心跳',
      dataIndex: 'agent_last_seen',
      width: 132,
      render: (v) => (
        <Tooltip title={v ? fmtTime(v) : 'Agent 尚未上报心跳'}>
          <span className="pp-muted pp-nowrap">{v ? fmtAgo(v) : '—'}</span>
        </Tooltip>
      ),
    },
    {
      title: '轮询',
      dataIndex: 'agent_next_poll_sec',
      width: 104,
      align: 'center',
      render: (v) => <span className="pp-muted pp-nowrap">{v ? `${v} 秒` : '—'}</span>,
    },
    {
      title: '密钥',
      width: 120,
      render: (_, row) => (
        <span className="pp-mono pp-muted">{row.agent_token_hint || (row.has_agent_token ? '已设置' : '无')}</span>
      ),
    },
    {
      title: '操作',
      width: 230,
      fixed: 'right',
      render: (_, row) => (
        <Space size={0}>
          {row.link_mode !== 'local' ? (
            <Popconfirm
              title="安装 Agent 并轮换接入密钥？"
              description="旧密钥会立即失效，已安装的 Agent 需要重新执行安装命令。"
              okText="安装并轮换"
              onConfirm={() => showInstall(row)}
            >
              <Tooltip title="安装 Agent（同时轮换密钥）">
                <Button type="text" size="small" icon={<KeyOutlined />} aria-label="安装 Agent" />
              </Tooltip>
            </Popconfirm>
          ) : null}
          {sshMap.get(row.id) ? (() => {
            const st = sshUi(sshMap.get(row.id));
            return (
              <IconAction
                title={st.toggleToEnable ? '启用 SSH' : '停用 SSH'}
                icon={<DisconnectOutlined />}
                onClick={() => sshToggle([row.id], st.toggleToEnable)}
              />
            );
          })() : null}
          <Popconfirm
            title={`重启服务器「${row.name}」？`}
            description={
              row.link_mode === 'local'
                ? '本机重启会中断控制面板，需通过云厂商控制台确认恢复。'
                : '重启期间服务器会离线，Agent 与代理连接会短暂中断。'
            }
            okText="重启"
            okButtonProps={{ danger: true }}
            onConfirm={() => reboot(row)}
          >
            <Tooltip title="重启服务器">
              <Button type="text" size="small" danger icon={<PoweroffOutlined />} aria-label="重启服务器" />
            </Tooltip>
          </Popconfirm>
          {row.link_mode !== 'local' && row.agent_update ? (
            <IconAction
              title={`更新 agent${row.agent_latest ? ' → ' + row.agent_latest : ''}`}
              icon={<CloudDownloadOutlined />}
              onClick={() => pushOne(row)}
            />
          ) : null}
          <Popconfirm
            title="更新 cloudflared？"
            description="下载最新版本，替换 systemd 实际使用的二进制并自动重启；会创建后台任务。"
            okText="更新"
            onConfirm={() => updateCloudflared(row)}
          >
            <Tooltip title="更新 cloudflared">
              <Button type="text" size="small" icon={<CloudDownloadOutlined />} aria-label="更新 cloudflared" />
            </Tooltip>
          </Popconfirm>
          <IconAction
            title={row.allow_exec ? '命令' : '命令（该服务器未开启远程命令）'}
            icon={<CodeOutlined />}
            onClick={() => (row.allow_exec ? openExec(row) : message.warning('该服务器未开启「允许远程命令」'))}
          />
          <IconAction title="编辑" icon={<EditOutlined />} onClick={() => setEditFor(row)} />
          <IconAction title="任务队列" icon={<UnorderedListOutlined />} onClick={() => setTasksFor(row)} />
          <Popconfirm title="删除该服务器？" okText="删除" okButtonProps={{ danger: true }} onConfirm={() => remove(row)}>
            <Tooltip title="删除服务器">
              <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label="删除" />
            </Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={12} md={6}>
          <StatCard icon={<CloudServerOutlined />} label="服务器总数" value={fmtNum(rows.length)} accent={chartPalette[0]} />
        </Col>
        <Col xs={12} md={6}>
          <StatCard
            icon={<CheckCircleOutlined />}
            label="在线"
            value={fmtNum(counts.online)}
            accent={chartPalette[3]}
            hint={rows.length ? `在线率 ${((counts.online / rows.length) * 100).toFixed(0)}%` : '—'}
          />
        </Col>
        <Col xs={12} md={6}>
          <StatCard icon={<DisconnectOutlined />} label="离线" value={fmtNum(counts.offline)} accent={chartPalette[4]} />
        </Col>
        <Col xs={12} md={6}>
          <StatCard
            icon={<QuestionCircleOutlined />}
            label="未接入"
            value={fmtNum(counts.unknown)}
            accent={chartPalette[5]}
            hint="Agent 尚未上报心跳"
          />
        </Col>
      </Row>

      <div style={{ marginTop: 16 }}>
        <PageCard
          title={<span className="pp-section-title">服务器列表</span>}
          extra={
            <Space>
              <Button icon={<ReloadOutlined />} onClick={() => reload().catch(() => {})} loading={loading}>
                刷新
              </Button>
              <Button icon={<SyncOutlined />} loading={checking} onClick={testSelected} disabled={!selected.length}>
                探测选中{selected.length ? `（${selected.length}）` : ''}
              </Button>
              <Tooltip
                title={
                  latestAgent
                    ? `最新 Agent ${latestAgent}；只升级版本较旧的机器（勾选则只升级勾选的）`
                    : '勾选服务器后点此升级，未勾选则升级全部'
                }
              >
                <Popconfirm
                  title={selected.length ? `向选中的 ${selected.length} 台推送 Agent 更新？` : '向全部服务器推送 Agent 更新？'}
                  okText="推送"
                  cancelText="取消"
                  onConfirm={pushUpdate}
                >
                  <Button icon={<CloudDownloadOutlined />} loading={pushing} disabled={!rows.length}>
                    更新 Agent{selected.length ? `（${selected.length}）` : latestAgent ? ` · ${latestAgent}` : ''}
                  </Button>
                </Popconfirm>
              </Tooltip>
              <Popconfirm
                title={'启用选中 ' + selected.length + ' 台的 SSH？'}
                onConfirm={() => sshToggle(selected, true)}
                disabled={!selected.length}
              >
                <Button disabled={!selected.length}>启用 SSH</Button>
              </Popconfirm>
              <Popconfirm
                title={'停用选中 ' + selected.length + ' 台的 SSH？'}
                description="确认 Agent 在线；停用后只能从面板再启用"
                okText="停用"
                okButtonProps={{ danger: true }}
                onConfirm={() => sshToggle(selected, false)}
                disabled={!selected.length}
              >
                <Button danger disabled={!selected.length}>停用 SSH</Button>
              </Popconfirm>
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setCompose(true)}>
                接入服务器
              </Button>
            </Space>
          }
          bodyStyle={{ padding: 0 }}
        >
          <div style={{ padding: '16px 18px 4px' }}>
            <Toolbar
              left={
                <Space wrap>
                  <Input
                    allowClear
                    prefix={<SearchOutlined style={{ color: '#94a3b8' }} />}
                    placeholder="搜索名称 / IP / 标签"
                    value={q}
                    onChange={(e) => setQ(e.target.value)}
                    style={{ width: 260 }}
                  />
                  <Space.Compact>
                    {[
                      { k: '', t: '全部' },
                      { k: 'online', t: '在线' },
                      { k: 'offline', t: '离线' },
                      { k: 'unknown', t: '未知' },
                    ].map((o) => (
                      <Button
                        key={o.k}
                        type={status === o.k ? 'primary' : 'default'}
                        onClick={() => setStatus(o.k)}
                        size="medium"
                      >
                        {o.t}
                      </Button>
                    ))}
                  </Space.Compact>
                </Space>
              }
            />
          </div>

          {error ? (
            <div style={{ padding: 18 }}>
              <ErrorBlock error={error} onRetry={() => reload().catch(() => {})} />
            </div>
          ) : null}

          <Table
            className="pp-table"
            rowKey="id"
            size="medium"
            columns={columns}
            dataSource={rows}
            loading={loading}
            rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys) }}
            scroll={{ x: 1360 }}
            pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 台` }}
          />
        </PageCard>
      </div>

      {/* 新增服务器 */}
      <Modal
        open={compose}
        title="接入新服务器"
        onCancel={() => setCompose(false)}
        onOk={create}
        confirmLoading={saving}
        okText="创建并生成安装命令"
        width={560}
        destroyOnHidden
      >
        <Alert
          type="info"
          showIcon
          style={{ margin: '10px 0 16px', borderRadius: 10 }}
          message="Agent 只做出站连接"
          description="服务器无需开放任何管理端口。创建后把安装命令贴到目标机器执行即可。"
        />
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ enabled: true }}>
          <Form.Item name="name" label="服务器名称" rules={[{ required: true, message: '请填写名称' }]}>
            <Input placeholder="例如：hk-01" />
          </Form.Item>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name="tags" label="标签">
                <Input placeholder="香港,核心" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="allow_exec" label="允许远程命令" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="remark" label="备注">
            <Input.TextArea rows={2} placeholder="用途说明" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 安装命令 / 密钥 */}
      <Modal
        open={!!result}
        title={result && result.title}
        onCancel={() => setResult(null)}
        footer={
          <Button type="primary" onClick={() => setResult(null)}>
            知道了
          </Button>
        }
        width={720}
      >
        {result && result.error ? (
          <Alert type="warning" showIcon message={result.error} description={result.note || undefined} />
        ) : (
          <>
            {result && result.token ? (
              <div style={{ marginBottom: 14 }}>
                <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
                  接入密钥（仅本次显示，请立即保存）
                </div>
                <CopyText value={result.token} display={<span className="pp-mono">{result.token}</span>} />
              </div>
            ) : null}
            {result && result.serverId ? (
              <div style={{ marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <Switch checked={disableSsh} onChange={toggleDisableSsh} size="small" />
                <span style={{ fontSize: 13 }}>安装成功后停用 SSH（sshd/ssh）</span>
                <span className="pp-muted" style={{ fontSize: 12 }}>
                  装完先确认 Agent 在线再执行 <code>systemctl disable --now sshd</code>；需要恢复时在服务器详情里点「启用 SSH」，或从 WebSSH 执行。
                </span>
              </div>
            ) : null}
            <div className="pp-muted" style={{ fontSize: 12, marginBottom: 4 }}>
              在目标服务器上执行
            </div>
            <JsonView value={String((result && result.command) || '')} maxHeight={260} />
            {result && result.note ? (
              <div style={{ marginTop: 10, fontSize: 12, color: '#94a3b8', lineHeight: 1.7 }}>{result.note}</div>
            ) : null}
          </>
        )}
      </Modal>

      <TasksModal row={tasksFor} onClose={() => setTasksFor(null)} />

      <ServerEditModal
        row={editFor}
        onClose={() => setEditFor(null)}
        onSaved={() => {
          setEditFor(null);
          reload().catch(() => {});
        }}
      />

      {/* 远程终端（「命令」按钮）：可连续输入命令、实时回显 */}
      <RemoteShell server={execFor} open={!!execFor} onClose={() => setExecFor(null)} />
    </div>
  );
}

/* ---------------- 编辑服务器 ---------------- */
function ServerEditModal({ row, onClose, onSaved }) {
  const { message } = AntdApp.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);

  React.useEffect(() => {
    if (!row) return;
    form.setFieldsValue({
      name: row.name,
      host: row.host || '',
      tags: row.tags || '',
      remark: row.remark || '',
      enabled: !!row.enabled,
      allow_exec: !!row.allow_exec,
      metrics_every: row.metrics_every || 1,
      agent_next_poll_sec: row.agent_next_poll_sec || 10,
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row]);

  const save = async () => {
    const v = await readForm(form);
    if (!v) return;
    setSaving(true);
    try {
      await api(`servers/${row.id}`, { method: 'PUT', body: v });
      message.success('服务器已更新');
      onSaved();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={!!row}
      title={row ? `编辑服务器 · ${row.name}` : ''}
      onCancel={onClose}
      onOk={save}
      confirmLoading={saving}
      okText="保存"
      width={600}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" requiredMark={false} style={{ marginTop: 12 }}>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="name" label="名称" rules={[{ required: true, message: '请填写名称' }]}>
              <Input maxLength={64} />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item name="host" label="机器地址" extra="可留空（通常由 Agent 上报）">
              <Input placeholder="1.2.3.4 或 host.example.com" />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="tags" label="标签">
              <Input placeholder="香港,核心" maxLength={128} />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item name="agent_next_poll_sec" label="轮询间隔（秒）">
              <InputNumber min={2} max={900} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item name="metrics_every" label="每 N 次采指标">
              <InputNumber min={1} max={10} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={12}>
          <Col span={8}>
            <Form.Item name="enabled" label="启用" valuePropName="checked">
              <Switch size="small" />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item name="allow_exec" label="允许远程命令" valuePropName="checked">
              <Switch size="small" />
            </Form.Item>
          </Col>
        </Row>
        <Form.Item name="remark" label="备注">
          <Input.TextArea rows={2} maxLength={500} />
        </Form.Item>
      </Form>
    </Modal>
  );
}

/* ---------------- 任务队列 ---------------- */
function TasksModal({ row, onClose }) {
  const { message } = AntdApp.useApp();
  const [state, setState] = useState({ data: [], loading: false, error: null });

  const cancel = async (task) => {
    try {
      await api(`agent/tasks/${task.id}`, { method: 'DELETE' });
      message.success('任务已取消');
      load();
    } catch (e) {
      message.error(e.message || '取消失败');
    }
  };

  const load = React.useCallback(async () => {
    if (!row) return;
    setState({ data: [], loading: true, error: null });
    try {
      const res = await api(`servers/${row.id}/tasks`, { query: { limit: 30 } });
      setState({ data: (res && res.data) || [], loading: false, error: null });
    } catch (e) {
      setState({ data: [], loading: false, error: e });
    }
  }, [row]);

  React.useEffect(() => {
    if (row) load();
  }, [row, load]);

  return (
    <Modal
      open={!!row}
      title={row ? `任务队列 · ${row.name}` : '任务队列'}
      onCancel={onClose}
      footer={<Button onClick={onClose}>关闭</Button>}
      width={760}
    >
      {state.error ? <ErrorBlock error={state.error} onRetry={load} /> : null}
      <Table
        rowKey={(r) => r.id || r.task_id || Math.random()}
        size="small"
        loading={state.loading}
        dataSource={state.data}
        pagination={{ pageSize: 10, size: 'small' }}
        locale={{ emptyText: '暂无任务' }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 64 },
          {
            title: '类型',
            width: 110,
            render: (_, r) => <Tag color="geekblue">{r.type || r.action || r.task_type || '—'}</Tag>,
          },
          {
            title: '状态',
            width: 96,
            render: (_, r) => <StatusPill status={r.status} />,
          },
          {
            title: '更新时间',
            width: 150,
            render: (_, r) => <span className="pp-muted">{fmtTime(r.updated_at || r.created_at)}</span>,
          },
          {
            title: '结果',
            ellipsis: true,
            render: (_, r) => (
              <span className="pp-mono" style={{ color: '#64748b', fontSize: 12 }}>
                {String(r.result || r.message || r.error || r.output || '—').slice(0, 120)}
              </span>
            ),
          },
          {
            title: '',
            width: 72,
            render: (_, r) =>
              String(r.status) === 'pending' ? (
                <Popconfirm title="取消该任务？" okText="取消任务" onConfirm={() => cancel(r)}>
                  <Button type="link" size="small" danger>
                    取消
                  </Button>
                </Popconfirm>
              ) : null,
          },
        ]}
      />
    </Modal>
  );
}
