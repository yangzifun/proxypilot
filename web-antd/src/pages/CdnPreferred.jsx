import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  App as AntdApp,
  AutoComplete,
  Button,
  Input,
  Modal,
  Popconfirm,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import {
  CheckCircleOutlined,
  CloudDownloadOutlined,
  DeleteOutlined,
  EditOutlined,
  ExperimentOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { apiDelete, apiGet, apiPost, apiPut } from '../api';
import { PageCard } from '../components';

/** 默认分组：Cloudflare 优选地址。旧 default 由后端兼容映射到本分组。 */
const DEFAULT_GROUP = 'cloudflare';
/** 自定义分组名：字母、数字、- 和 _。 */
const GROUP_NAME_RE = /^[A-Za-z0-9_-]+$/;

/** 分组名归一化：空串 / default 统一成 cloudflare，其余去首尾空格保留。 */
function normalizeGroupName(raw) {
  const v = String(raw === null || raw === undefined ? '' : raw).trim();
  if (!v || v.toLowerCase() === 'default') return DEFAULT_GROUP;
  return v;
}

/** 测试结果展示：行内「测试」与「测试地址」共用；绿=通过，红=失败。 */
function TestResultView({ result }) {
  const d = (result && result.data) || {};
  const ok = !!d.ok;
  const resolvedRaw = d.resolved;
  const resolved = Array.isArray(resolvedRaw)
    ? resolvedRaw.map((x) => String(x)).filter(Boolean)
    : String(resolvedRaw || '').split(',').map((x) => x.trim()).filter(Boolean);
  const latency = Number(d.latency_ms) || 0;
  const httpStatus = Number(d.http_status) || 0;
  const errText = String(d.error || '');
  // 后端分阶段 error 前缀可区分「该阶段未进行」与「该阶段失败」：未配置 SNI/路径时不报错、直接跳过。
  const tlsFailed = !d.tls_ok && /^TLS/i.test(errText);
  const httpFailed = !httpStatus && /^HTTP/i.test(errText);
  const rows = [
    { label: '地址', value: d.address ? `${d.address}${d.port ? ':' + d.port : ''}` : (result && result.target) || '—', tone: 'normal' },
    { label: '解析 IP', value: resolved.length ? resolved.join(', ') : '—', tone: 'normal', mono: true },
    { label: 'TCP', value: d.tcp_ok ? '通' : '不通', tone: d.tcp_ok ? 'ok' : 'fail' },
    { label: 'TLS', value: d.tls_ok ? '通' : tlsFailed ? '握手失败' : '未进行', tone: d.tls_ok ? 'ok' : tlsFailed ? 'fail' : 'muted' },
    {
      label: 'HTTP',
      value: httpStatus ? `HTTP ${httpStatus}` : httpFailed ? '请求失败' : '未发起',
      tone: httpStatus ? (httpStatus < 400 ? 'ok' : 'fail') : httpFailed ? 'fail' : 'muted',
    },
    { label: '总延迟', value: latency > 0 ? `${latency} ms` : '—', tone: 'normal' },
  ];
  const toneColor = (tone) => (tone === 'ok' ? '#16a34a' : tone === 'fail' ? '#dc2626' : tone === 'muted' ? '#64748b' : '#334155');
  return (
    <div>
      <Alert
        type={ok ? 'success' : 'error'}
        showIcon
        style={{ borderRadius: 10, marginBottom: 12 }}
        message={ok ? '测试通过：面板服务器可连通该地址' : '测试未通过'}
        description={d.error ? String(d.error) : undefined}
      />
      <div style={{ display: 'grid', gap: 8 }}>
        {rows.map((it) => (
          <div
            key={it.label}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              padding: '7px 10px',
              border: '1px solid #eef2f7',
              borderRadius: 8,
              background: '#fbfcfe',
            }}
          >
            <span className="pp-muted" style={{ width: 64, flex: 'none', fontSize: 12 }}>{it.label}</span>
            <span
              className={it.mono ? 'pp-mono' : undefined}
              style={{ fontSize: 12.5, wordBreak: 'break-all', color: toneColor(it.tone) }}
            >
              {it.value}
            </span>
          </div>
        ))}
      </div>
      <div className="pp-muted" style={{ fontSize: 12, marginTop: 12, lineHeight: 1.7 }}>
        测试由<b>面板服务器</b>直接发起（TCP / TLS / HTTPS），<b>不经过任何节点服务器</b>；
        结果只代表面板所在网络的连通性，用于判断该优选地址是否可用。
      </div>
    </div>
  );
}

export default function CdnPreferred() {
  const { message } = AntdApp.useApp();
  const [rows, setRows] = useState([]);
  const [groups, setGroups] = useState([]);
  const [group, setGroup] = useState(DEFAULT_GROUP);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState('');
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [importGroup, setImportGroup] = useState(DEFAULT_GROUP);
  const [enabled, setEnabled] = useState(false);
  const [editRow, setEditRow] = useState(null);
  const [editForm, setEditForm] = useState({ address: '', port: 443, remark: '' });
  const [cfgOpen, setCfgOpen] = useState(false);
  const [cfg, setCfg] = useState({ sni: '', path: '' });
  // 新建分组
  const [newGroupOpen, setNewGroupOpen] = useState(false);
  const [newGroupName, setNewGroupName] = useState('');
  // 单地址测试：行内测试 / 测试地址弹窗 / 结果弹窗
  const [testRowId, setTestRowId] = useState(0);
  const [testLoading, setTestLoading] = useState(false);
  const [testResult, setTestResult] = useState(null); // { payload, target, data }
  const [addrOpen, setAddrOpen] = useState(false);
  const [addrBusy, setAddrBusy] = useState(false);
  const [addrForm, setAddrForm] = useState({ address: '', port: 443, sni: '', path: '' });

  const activeGroup = normalizeGroupName(group);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiGet('/api/cdn-preferred', { group: normalizeGroupName(group) });
      setRows(res.data || []);
      const g = await apiGet('/api/cdn-preferred/groups');
      setGroups(g.data || []);
      const en = await apiGet('/api/cdn-preferred/enabled');
      setEnabled(!!en.enabled);
      const cs = await apiGet('/api/cdn-preferred/settings');
      setCfg({ sni: cs.sni || '', path: cs.path || '' });
    } catch (e) {
      message.error(e.message || '加载失败');
    } finally {
      setLoading(false);
    }
  }, [group, message]);

  useEffect(() => {
    load();
  }, [load]);

  const toggle = async (row, enabled) => {
    try {
      await apiPut(`/api/cdn-preferred/${row.id}`, { enabled });
      setRows((rs) => rs.map((r) => (r.id === row.id ? { ...r, enabled: enabled ? 1 : 0 } : r)));
      message.success(enabled ? '已启用' : '已停用');
    } catch (e) {
      message.error(e.message || '操作失败');
    }
  };

  const move = async (row, delta) => {
    const idx = rows.findIndex((r) => r.id === row.id);
    const target = idx + delta;
    if (idx < 0 || target < 0 || target >= rows.length) return;
    const next = [...rows];
    [next[idx], next[target]] = [next[target], next[idx]];
    setRows(next);
    try {
      await apiPut(`/api/cdn-preferred/${row.id}`, { sort: target });
      await apiPut(`/api/cdn-preferred/${next[idx].id}`, { sort: idx });
      load();
    } catch (e) {
      message.error(e.message || '排序失败');
    }
  };

  const remove = async (row) => {
    try {
      await apiDelete(`/api/cdn-preferred/${row.id}`);
      message.success('已删除');
      load();
    } catch (e) {
      message.error(e.message || '删除失败');
    }
  };

  const openEdit = (row) => {
    setEditRow(row);
    setEditForm({ address: row.address, port: row.port || 443, remark: row.remark || '' });
  };

  const saveEdit = async () => {
    if (!editRow) return;
    const addr = String(editForm.address || '').trim();
    if (!addr) {
      message.warning('地址不能为空');
      return;
    }
    const port = Number(editForm.port) || 443;
    setBusy('edit');
    try {
      await apiPut(`/api/cdn-preferred/${editRow.id}`, { address: addr, port, remark: String(editForm.remark || '').trim() });
      message.success('已保存');
      setEditRow(null);
      load();
    } catch (e) {
      message.error(e.message || '保存失败');
    } finally {
      setBusy('');
    }
  };

  const doImport = async () => {
    if (!importText.trim()) {
      message.warning('请粘贴优选地址');
      return;
    }
    const targetGroup = normalizeGroupName(importGroup);
    setBusy('import');
    try {
      const res = await apiPost('/api/cdn-preferred/import', { group_name: targetGroup, text: importText });
      message.success(`导入完成：新增 ${res.added || 0} 条，跳过 ${res.skipped || 0} 条（分组 ${targetGroup}）`);
      setImportOpen(false);
      setImportText('');
      setGroup(targetGroup);
      setImportGroup(targetGroup);
      load();
    } catch (e) {
      message.error(e.message || '导入失败');
    } finally {
      setBusy('');
    }
  };

  const toggleGlobal = async (next) => {
    setEnabled(next);
    try {
      await apiPut('/api/cdn-preferred/enabled', { enabled: next });
      message.success(next ? '已启用：CDN 节点订阅将使用优选地址' : '已关闭优选替换');
    } catch (e) {
      setEnabled(!next);
      message.error(e.message || '保存失败');
    }
  };

  const saveCfg = async () => {
    try {
      await apiPut('/api/cdn-preferred/settings', { sni: cfg.sni.trim(), path: cfg.path.trim() });
      message.success('检测设置已保存');
      setCfgOpen(false);
    } catch (e) {
      message.error(e.message || '保存失败');
    }
  };

  const checkAll = async () => {
    setBusy('check');
    try {
      const res = await apiPost('/api/cdn-preferred/check', {});
      const list = res.data || [];
      const ok = list.filter((x) => x.ok).length;
      const off = list.filter((x) => x.auto_disabled).length;
      message.success(`检测完成：可用 ${ok} / ${list.length}` + (off ? `，自动停用 ${off} 条` : ''));
      load();
    } catch (e) {
      message.error(e.message || '检测失败');
    } finally {
      setBusy('');
    }
  };

  const addOne = async () => {
    let value = '';
    Modal.confirm({
      title: '新增优选地址',
      content: (
        <div>
          <div className="pp-muted" style={{ fontSize: 12, marginBottom: 6 }}>
            格式：host:port#备注（端口可省略，默认 443）；将添加到当前分组 {activeGroup}
          </div>
          <Input placeholder="162.159.198.1:443#甬哥优选" onChange={(e) => { value = e.target.value; }} />
        </div>
      ),
      onOk: async () => {
        if (!value.trim()) return;
        await apiPost('/api/cdn-preferred', {
          group_name: activeGroup,
          address: value.split('#')[0].trim().split(':')[0],
          port: 443,
          remark: value.split('#')[1] || '',
        });
        load();
      },
    });
  };

  /* ---------------- 单地址测试（面板服务器发起，不经过节点） ---------------- */

  const runTest = useCallback(async (payload, target) => {
    const res = await apiPost('/api/cdn-preferred/test', payload);
    setTestResult({ payload, target: target || '', data: (res && res.data) || {} });
  }, []);

  const testOneRow = async (row) => {
    if (testRowId) return;
    setTestRowId(Number(row.id) || 0);
    try {
      await runTest({ id: row.id }, `${row.address}${row.port ? ':' + row.port : ''}`);
    } catch (e) {
      message.error(e.message || '测试失败');
    } finally {
      setTestRowId(0);
    }
  };

  const openAddrTest = () => {
    setAddrForm({
      address: '',
      port: 443,
      sni: String(cfg.sni || ''),
      path: String(cfg.path || ''),
    });
    setAddrOpen(true);
  };

  const submitAddrTest = async () => {
    const address = String(addrForm.address || '').trim();
    if (!address) {
      message.warning('请填写要测试的地址（IP 或域名）');
      return;
    }
    const port = Number(addrForm.port) || 443;
    if (!(port > 0 && port < 65536)) {
      message.warning('端口需在 1-65535 之间');
      return;
    }
    setAddrBusy(true);
    try {
      const payload = {
        address,
        port,
        sni: String(addrForm.sni || '').trim(),
        path: String(addrForm.path || '').trim(),
      };
      await runTest(payload, `${address}:${port}`);
      setAddrOpen(false);
    } catch (e) {
      message.error(e.message || '测试失败');
    } finally {
      setAddrBusy(false);
    }
  };

  const retryTest = async () => {
    if (!testResult) return;
    setTestLoading(true);
    try {
      await runTest(testResult.payload, testResult.target);
    } catch (e) {
      message.error(e.message || '测试失败');
    } finally {
      setTestLoading(false);
    }
  };

  /* ---------------- 分组：新建 / 切换 ---------------- */

  const openNewGroup = () => {
    setNewGroupName('');
    setNewGroupOpen(true);
  };

  const submitNewGroup = () => {
    const v = String(newGroupName || '').trim();
    if (!v) {
      message.warning('请输入分组名');
      return;
    }
    if (v.toLowerCase() === 'default') {
      message.warning('default 已改名为 cloudflare，请使用其他分组名');
      return;
    }
    if (!GROUP_NAME_RE.test(v)) {
      message.warning('分组名只能包含字母、数字、- 和 _');
      return;
    }
    setNewGroupOpen(false);
    setGroup(v);
    setImportGroup(v);
    setImportOpen(true);
    message.info(`已切换到分组 ${v}：导入地址后该分组会自动创建`);
  };

  const columns = [
    {
      title: '排序', width: 90, render: (_, row, idx) => (
        <Space size={0}>
          <Button size="small" type="text" disabled={idx === 0} onClick={() => move(row, -1)}>↑</Button>
          <Button size="small" type="text" disabled={idx === rows.length - 1} onClick={() => move(row, 1)}>↓</Button>
        </Space>
      ),
    },
    { title: '地址', dataIndex: 'address', render: (v) => <span className="pp-mono">{v}</span> },
    { title: '端口', dataIndex: 'port', width: 80 },
    { title: '备注', dataIndex: 'remark', ellipsis: true, render: (v) => v || '—' },
    {
      title: '状态', dataIndex: 'enabled', width: 100,
      render: (v, row) => (
        <Tag color={v ? 'success' : 'default'} style={{ cursor: 'pointer' }} onClick={() => toggle(row, !v)}>
          {v ? '已启用' : '已停用'}
        </Tag>
      ),
    },
    {
      title: '连续失败', dataIndex: 'fail_count', width: 100,
      render: (v) => (Number(v) > 0 ? <Tag color="error">{v} 次</Tag> : <span className="pp-muted">0</span>),
    },
    {
      title: '最近检测', width: 160,
      render: (_, row) => (row.last_check_at
        ? <span className="pp-muted">{row.last_check_ok ? `可用 · ${row.last_latency_ms}ms` : '不可用'}</span>
        : <span className="pp-muted">未检测</span>),
    },
    {
      title: '操作', width: 224,
      render: (_, row) => {
        const testing = testRowId === Number(row.id);
        const anyTesting = testRowId > 0;
        return (
          <Space size={0}>
            <Tooltip title="从面板服务器测试该地址（不经过节点服务器）">
              <Button
                size="small"
                type="link"
                icon={<ExperimentOutlined />}
                loading={testing}
                disabled={anyTesting && !testing}
                onClick={() => testOneRow(row)}
              >
                测试
              </Button>
            </Tooltip>
            <Button size="small" type="link" icon={<EditOutlined />} disabled={anyTesting} onClick={() => openEdit(row)}>编辑</Button>
            <Popconfirm title="删除该地址？" onConfirm={() => remove(row)} disabled={anyTesting}>
              <Button size="small" type="link" danger icon={<DeleteOutlined />} disabled={anyTesting}>删除</Button>
            </Popconfirm>
          </Space>
        );
      },
    },
  ];

  const groupOptions = useMemo(() => {
    const names = (groups || [])
      .map((g) => String(g.group_name || '').trim())
      .filter(Boolean);
    const merged = [DEFAULT_GROUP, ...names.filter((n) => n.toLowerCase() !== DEFAULT_GROUP)];
    return merged.map((n) => ({ value: n, label: n === DEFAULT_GROUP ? `${n}（默认）` : n }));
  }, [groups]);

  const enabledCount = rows.filter((r) => r.enabled).length;

  return (
    <PageCard
      title={<span><CloudDownloadOutlined /> 域名优先</span>}
      extra={
        <Space wrap>
          <Switch checked={enabled} onChange={toggleGlobal} checkedChildren="替换已启用" unCheckedChildren="替换已关闭" />
          <AutoComplete
            options={groupOptions}
            value={group}
            onChange={setGroup}
            style={{ width: 170 }}
            placeholder={`分组（默认 ${DEFAULT_GROUP}）`}
          />
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
          <Tooltip title="从面板服务器逐个测试当前分组的地址（不经过节点服务器）">
            <Button icon={<CheckCircleOutlined />} loading={busy === 'check'} onClick={checkAll}>一键检测</Button>
          </Tooltip>
          <Tooltip title="从面板服务器测试任意地址（不经过节点服务器）">
            <Button icon={<ExperimentOutlined />} onClick={openAddrTest}>测试地址</Button>
          </Tooltip>
          <Button onClick={() => setCfgOpen(true)}>检测设置</Button>
          <Button
            icon={<PlusOutlined />}
            onClick={() => {
              setImportGroup(activeGroup);
              setImportOpen(true);
            }}
          >
            批量导入
          </Button>
          <Button icon={<PlusOutlined />} onClick={openNewGroup}>新建分组</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={addOne}>新增</Button>
        </Space>
      }
    >
      <div style={{ padding: '0 16px 8px' }} className="pp-muted">
        启用后，CDN 版本节点在用户订阅中会按列表展开为多个优选地址，SNI 与 Host 仍保持节点 CDN 域名。
        当前分组：<b>{activeGroup}</b>
        {activeGroup === DEFAULT_GROUP ? '（Cloudflare 优选）' : ''}，共 {rows.length} 条，启用 {enabledCount} 条。
        其他类型的 CDN 优选可创建自定义分组（如 <code>aliyun</code>、<code>tencent</code>、<code>overseas</code>），
        导入地址后分组会自动创建；用左上角分组框切换查看/管理。
      </div>
      <Table className="pp-table" rowKey="id" size="medium" loading={loading} dataSource={rows} columns={columns} pagination={false} />

      {/* 单地址测试结果：行内测试与「测试地址」共用 */}
      <Modal
        title={testResult ? `测试结果 · ${testResult.data && testResult.data.ok ? '可用' : '不可用'}` : '测试结果'}
        open={!!testResult}
        onCancel={() => setTestResult(null)}
        footer={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={retryTest} loading={testLoading}>再测一次</Button>
            <Button type="primary" onClick={() => setTestResult(null)}>关闭</Button>
          </Space>
        }
        width={560}
        centered
        destroyOnHidden
      >
        {testResult ? <TestResultView result={testResult} /> : null}
      </Modal>

      {/* 测试任意地址：默认带全局检测设置的 SNI / 路径，可连续测试 */}
      <Modal
        title="测试地址（从面板服务器发起）"
        open={addrOpen}
        onCancel={() => setAddrOpen(false)}
        onOk={submitAddrTest}
        confirmLoading={addrBusy}
        okText="开始测试"
        width={560}
        centered
        destroyOnHidden
      >
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 10, lineHeight: 1.7 }}>
          测试由<b>面板服务器</b>直接发起 TCP / TLS / HTTPS 连接，<b>不经过节点服务器</b>。
          地址支持 IP 或域名；SNI / 路径留空时使用「检测设置」里的全局值。
        </div>
        <div style={{ display: 'grid', gap: 10 }}>
          <Input
            addonBefore="地址"
            value={addrForm.address}
            onChange={(e) => setAddrForm((f) => ({ ...f, address: e.target.value }))}
            placeholder="162.159.198.1 或 cdn.example.com"
          />
          <Input
            addonBefore="端口"
            type="number"
            value={addrForm.port}
            onChange={(e) => setAddrForm((f) => ({ ...f, port: e.target.value }))}
            placeholder="443"
          />
          <Input
            addonBefore="SNI"
            value={addrForm.sni}
            onChange={(e) => setAddrForm((f) => ({ ...f, sni: e.target.value }))}
            placeholder="留空使用检测设置"
          />
          <Input
            addonBefore="路径"
            value={addrForm.path}
            onChange={(e) => setAddrForm((f) => ({ ...f, path: e.target.value }))}
            placeholder="留空使用检测设置"
          />
        </div>
        <div className="pp-muted" style={{ fontSize: 12, marginTop: 10 }}>
          测试结果不影响表格数据；需要连续测试不同地址时，关闭结果弹窗后再次打开本弹窗即可。
        </div>
      </Modal>

      <Modal
        title="检测设置"
        open={cfgOpen}
        onCancel={() => setCfgOpen(false)}
        onOk={saveCfg}
        okText="保存"
        destroyOnHidden
      >
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 10, lineHeight: 1.7 }}>
          留空只做 TCP 连通性检测；填写 SNI 后升级为 TLS 握手校验，再填路径则额外发一次 HTTPS 请求。
          连续失败 3 次的地址会被自动停用。
          <br />
          检测与<b>单地址测试</b>都由<b>面板服务器</b>发起，<b>不经过节点服务器</b>。
        </div>
        <div style={{ display: 'grid', gap: 10 }}>
          <Input addonBefore="检测 SNI" value={cfg.sni} onChange={(e) => setCfg((c) => ({ ...c, sni: e.target.value }))} placeholder="例如 vmpt-lax.yzfn.top" />
          <Input addonBefore="检测路径" value={cfg.path} onChange={(e) => setCfg((c) => ({ ...c, path: e.target.value }))} placeholder="/vlws（可留空）" />
        </div>
      </Modal>

      <Modal
        title="编辑优选地址"
        open={!!editRow}
        onCancel={() => setEditRow(null)}
        onOk={saveEdit}
        confirmLoading={busy === 'edit'}
        okText="保存"
        destroyOnHidden
      >
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 8 }}>修改后立即生效，已刷新订阅的客户端下次更新即可看到。</div>
        <div style={{ display: 'grid', gap: 10 }}>
          <Input addonBefore="地址" value={editForm.address} onChange={(e) => setEditForm((f) => ({ ...f, address: e.target.value }))} placeholder="162.159.198.1 或 yg1.ygkkk.dpdns.org" />
          <Input addonBefore="端口" type="number" value={editForm.port} onChange={(e) => setEditForm((f) => ({ ...f, port: e.target.value }))} />
          <Input addonBefore="备注" value={editForm.remark} onChange={(e) => setEditForm((f) => ({ ...f, remark: e.target.value }))} placeholder="甬哥优选 | 备注" />
        </div>
      </Modal>

      {/* 新建分组：确定后切组并打开导入（导入时自动创建分组） */}
      <Modal
        title="新建分组"
        open={newGroupOpen}
        onCancel={() => setNewGroupOpen(false)}
        onOk={submitNewGroup}
        okText="创建并导入"
        width={480}
        centered
        destroyOnHidden
      >
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 10, lineHeight: 1.7 }}>
          分组名只能包含字母、数字、<code>-</code> 和 <code>_</code>，不能使用 <code>default</code>
          （已改名为 <code>{DEFAULT_GROUP}</code>）。确定后会把当前分组切到该名称并打开批量导入，
          导入地址后分组自动创建。
        </div>
        <Input
          value={newGroupName}
          onChange={(e) => setNewGroupName(e.target.value)}
          onPressEnter={submitNewGroup}
          placeholder="例如 aliyun / tencent / overseas"
          maxLength={32}
          allowClear
        />
      </Modal>

      <Modal title="批量导入优选地址" open={importOpen} onCancel={() => setImportOpen(false)} onOk={doImport} confirmLoading={busy === 'import'} width={640} destroyOnHidden>
        <div className="pp-muted" style={{ fontSize: 12, marginBottom: 8, lineHeight: 1.7 }}>
          每行一条，支持 <code>host:port#备注</code>，端口省略默认 443；重复地址自动跳过。
          分组留空或填 <code>default</code> 时写入 <code>{DEFAULT_GROUP}</code>（Cloudflare 优选）；
          填其他名称（如 <code>aliyun</code>、<code>tencent</code>、<code>overseas</code>）会自动创建对应分组。
        </div>
        <Input
          placeholder={`分组（默认 ${DEFAULT_GROUP}）`}
          value={importGroup}
          onChange={(e) => setImportGroup(e.target.value)}
          style={{ marginBottom: 8 }}
        />
        <Input.TextArea rows={12} value={importText} onChange={(e) => setImportText(e.target.value)}
          placeholder={'162.159.198.1:443#甬哥优选\nyg1.ygkkk.dpdns.org:443#甬哥优选'} />
      </Modal>
    </PageCard>
  );
}