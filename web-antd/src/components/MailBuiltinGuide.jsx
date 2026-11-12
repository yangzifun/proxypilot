import React, { useState } from 'react';
import {
  CopyOutlined,
  ReloadOutlined,
  SendOutlined,
  CheckCircleFilled,
  CloseCircleFilled,
  ExclamationCircleFilled,
  QuestionCircleFilled,
  CloudUploadOutlined,
  EditOutlined,
} from '@ant-design/icons';
import { Alert, App as AntdApp, Button, Input, Space, Table, Tag, Typography } from 'antd';
import { apiPost } from '../api';

// 自建直投向导：填域名 + 发件人 → 生成 DNS 记录 → 一键写入 / 手动填写 → 校验 → 发测试。
// 不依赖任何第三方发信服务，面板直接连收件方 MX 投递。

const TYPE_COLOR = { TXT: 'blue', PTR: 'purple' };

function StatusTag({ status }) {
  if (status === 'ok') return <Tag color="success" icon={<CheckCircleFilled />}>已生效</Tag>;
  if (status === 'provider_default') return <Tag color="warning" icon={<ExclamationCircleFilled />}>服务商默认名</Tag>;
  if (status === 'forward_mismatch') return <Tag color="warning" icon={<ExclamationCircleFilled />}>反查不一致</Tag>;
  if (status === 'missing') return <Tag color="error" icon={<CloseCircleFilled />}>未配置</Tag>;
  return <Tag icon={<QuestionCircleFilled />}>未知</Tag>;
}

export default function MailBuiltinGuide({ guide, reload }) {
  const { message, modal } = AntdApp.useApp();
  const [testTo, setTestTo] = useState('');
  const [busy, setBusy] = useState(false);
  const [applying, setApplying] = useState(false);
  // 手动填写/覆盖的记录值：key -> value
  const [edits, setEdits] = useState({});
  const [editing, setEditing] = useState(null);

  const valueOf = (r) => (edits[r.key] != null ? edits[r.key] : r.value);
  const records = guide?.records || [];

  // 生成 BIND zone 片段：Cloudflare 的 DNS → Import and Export 支持整段粘贴。
  // BIND 的 TXT 单个字符串上限 255 字节，超长必须拆成多个引号段用空格连接。
  const toBindZone = () => {
    const chunk = (s) => {
      const out = [];
      for (let i = 0; i < s.length; i += 255) out.push(`"${s.slice(i, i + 255)}"`);
      return out.join(' ');
    };
    const lines = [];
    for (const r of records) {
      if (r.type !== 'TXT') continue;
      const host = r.host.endsWith('.') ? r.host : r.host + '.';
      lines.push(`${host} 300 IN TXT ${chunk(valueOf(r))}`);
    }
    return lines.join('\n');
  };
  const domain = guide?.domain || '';
  const missing = records.filter((r) => r.status !== 'ok').length;

  const copy = async (text) => {
    try {
      await navigator.clipboard.writeText(text);
      message.success('已复制');
    } catch {
      message.warning('复制失败，请手动选择');
    }
  };

  const applyDNS = async () => {
    setApplying(true);
    try {
      const r = await apiPost('admin/mail/dns/apply', {});
      const detail = (r?.results || [])
        .map((it) => `${it.key}: ${it.error ? '失败 - ' + it.error : it.action}`)
        .join('\n');
      if (r?.success) {
        message.success(r.message || 'DNS 记录已写入');
        modal.success({ title: '写入结果', content: <pre style={{ fontSize: 12, margin: 0 }}>{detail}</pre> });
      } else {
        message.warning(r?.message || '部分记录写入失败');
        modal.info({
          title: '部分失败',
          content: (
            <div>
              <pre style={{ fontSize: 12, margin: 0 }}>{detail}</pre>
              <div style={{ marginTop: 8, fontSize: 12, color: '#64748b' }}>
                需要 Cloudflare Token 具备 <b>Zone → DNS → Edit</b> 权限；没有权限就按表格手动添加。
              </div>
            </div>
          ),
        });
      }
      reload();
    } catch (e) {
      message.error(e.message || '写入失败，请手动添加记录');
    } finally {
      setApplying(false);
    }
  };

  const regen = async (bits) => {
    setBusy(true);
    try {
      const r = await apiPost('admin/mail/dkim', bits ? { bits } : {});
      message.success(r?.message || '已生成新的 DKIM 密钥');
      setEdits({});
      reload();
    } catch (e) {
      message.error(e.message || '生成失败');
    } finally {
      setBusy(false);
    }
  };

  const sendTest = async () => {
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(testTo.trim())) {
      message.warning('请输入合法的收件邮箱');
      return;
    }
    setBusy(true);
    try {
      const r = await apiPost('admin/mail/test', { to: testTo.trim() });
      message.success(r?.message || '已提交发送');
    } catch (e) {
      message.error(e.message || '发送失败');
    } finally {
      setBusy(false);
    }
  };

  const columns = [
    {
      title: '类型',
      dataIndex: 'type',
      width: 66,
      render: (v) => <Tag color={TYPE_COLOR[v] || 'default'}>{v}</Tag>,
    },
    {
      title: '主机记录',
      dataIndex: 'host',
      width: 250,
      render: (v, r) => (
        <Space size={4} wrap>
          <Typography.Text code copyable={{ text: v }} style={{ fontSize: 11.5 }}>
            {v}
          </Typography.Text>
          {r.key === 'dkim' ? <Tag color="gold">DKIM</Tag> : null}
        </Space>
      ),
    },
    {
      title: '记录值（可直接编辑）',
      dataIndex: 'value',
      render: (_, r) => {
        const val = valueOf(r);
        const isEditing = editing === r.key;
        if (r.type === 'PTR') {
          return <Typography.Text style={{ fontSize: 12 }}>{val}</Typography.Text>;
        }
        return (
          <Space direction="vertical" size={4} style={{ width: '100%' }}>
            {isEditing ? (
              <Input.TextArea
                autoSize={{ minRows: 2, maxRows: 6 }}
                value={val}
                onChange={(e) => setEdits((s) => ({ ...s, [r.key]: e.target.value }))}
                style={{ fontSize: 11.5, fontFamily: 'monospace' }}
              />
            ) : (
              <Typography.Paragraph
                style={{ margin: 0, fontSize: 11.5, wordBreak: 'break-all', fontFamily: 'monospace' }}
                ellipsis={{ rows: 3, expandable: true, symbol: '展开' }}
              >
                {val}
              </Typography.Paragraph>
            )}
            <Space size={10}>
              <Button size="small" type="link" icon={<CopyOutlined />} onClick={() => copy(val)} style={{ padding: 0 }}>
                复制
              </Button>
              <Button
                size="small"
                type="link"
                icon={<EditOutlined />}
                style={{ padding: 0 }}
                onClick={() => {
                  if (isEditing) {
                    setEditing(null);
                    message.success('已保存本地填写值（不会改动服务器配置，仅便于复制）');
                  } else {
                    setEditing(r.key);
                  }
                }}
              >
                {isEditing ? '完成' : '编辑'}
              </Button>
              {edits[r.key] != null ? (
                <Button
                  size="small"
                  type="link"
                  style={{ padding: 0 }}
                  onClick={() => setEdits((s) => {
                    const n = { ...s };
                    delete n[r.key];
                    return n;
                  })}
                >
                  还原
                </Button>
              ) : null}
            </Space>
          </Space>
        );
      },
    },
    { title: '状态', dataIndex: 'status', width: 96, render: (v) => <StatusTag status={v} /> },
  ];

  return (
    <Space direction="vertical" size={12} style={{ width: '100%' }}>
      <Alert
        type="info"
        showIcon
        message="自建直投：面板直接连收件方 MX 投递，不经任何第三方"
        description="只需要两个信息：发信域名 与 发件人邮箱。把下面三条 TXT 记录加到 DNS 即可；没有 SPF / DKIM / DMARC 时邮件几乎必定进垃圾箱。"
      />

      {!domain ? (
        <Alert
          type="warning"
          showIcon
          message="还没填写发信域名与发件人"
          description="先在上面填写「发信域名」与「发件人邮箱」，保存后回到这里配置 DNS。"
        />
      ) : (
        <>
          <Space size={8} wrap>
            <Tag color="blue">域名 {domain}</Tag>
            <Tag color="cyan">发件人 {guide.from || '—'}</Tag>
            <Tag color="geekblue">selector {guide.selector}</Tag>
            <Tag color={guide.egress_ip ? 'green' : 'orange'}>出口 IP {guide.egress_ip || '未探测到'}</Tag>
            {guide.has_dkim ? (
              <Tag color="success">DKIM {guide.dkim_bits || 2048} 位 · {guide.dkim_record_len} 字符</Tag>
            ) : (
              <Tag color="error">DKIM 未生成</Tag>
            )}
          </Space>

          {missing > 0 ? (
            <Alert
              type="warning"
              showIcon
              message={`还有 ${missing} 条记录未生效`}
              description="DNS 生效通常 1-10 分钟（Cloudflare 一般 1 分钟内）。改完点「重新校验」。"
            />
          ) : (
            <Alert type="success" showIcon message="DNS 记录已全部生效，可以发送测试邮件了" />
          )}

          <Table
            rowKey="key"
            size="small"
            pagination={false}
            columns={columns}
            dataSource={records}
            expandable={{
              expandedRowRender: (r) => (
                <div style={{ fontSize: 12, color: '#64748b', lineHeight: 1.7 }}>
                  {r.note}
                  {r.actual ? (
                    <div style={{ marginTop: 4, fontFamily: 'monospace', wordBreak: 'break-all' }}>
                      当前解析结果：{r.actual}
                    </div>
                  ) : null}
                </div>
              ),
              defaultExpandAllRows: false,
            }}
          />

          {guide.dkim_record_len && !guide.dkim_single_ok ? (
            <Alert
              type="warning"
              showIcon
              message={`DKIM 记录长 ${guide.dkim_record_len} 字符，超过 DNS 单段 255 字节上限`}
              description={
                <span>
                  记录会被拆成多段。绝大多数邮件服务能正确拼接，但<b>部分网关只读第一段</b>，
                  表现为发信被拒：「DKIM signature error」。
                  遇到这种情况，点下面的按钮换成 1024 位密钥（整条约 233 字符，单段即可），
                  生成后重新把新的 TXT 值填进 DNS。
                </span>
              }
              action={
                <Button size="small" danger loading={busy} onClick={() => regen(1024)}>
                  换成 1024 位
                </Button>
              }
            />
          ) : null}

          <Space size={8} wrap>
            <Button
              type="primary"
              icon={<CloudUploadOutlined />}
              onClick={applyDNS}
              loading={applying}
              disabled={!guide.has_dkim}
            >
              一键写入 Cloudflare DNS
            </Button>
            <Button icon={<CopyOutlined />} onClick={() => copy(toBindZone())}>
              复制为 BIND 片段
            </Button>
            <Button icon={<ReloadOutlined />} onClick={reload}>
              重新校验
            </Button>
            <Button onClick={() => regen()} loading={busy} danger={guide.has_dkim}>
              {guide.has_dkim ? `轮换 DKIM 密钥（${guide.dkim_bits || 2048} 位）` : '生成 DKIM 密钥'}
            </Button>
          </Space>

          <Alert
            type="warning"
            showIcon
            message="手动添加时的注意事项"
            description={
              <span>
                粘贴到 Cloudflare 表单时，<b>记录值前后都不要加引号</b>（表格里的「复制」按钮已经是可直接粘贴的形式）。
                Cloudflare 会自己处理 255 字节的拆分；带引号会报「记录内容的引用方式不正确」。
                只有 Import and Export 的 BIND 片段才需要引号，那里用「复制为 BIND 片段」。
              </span>
            }
          />

          <Alert
            type="info"
            showIcon
            message="不想申请 Token？用 BIND 片段手动导入"
            description={
              <span>
                点「复制为 BIND 片段」，然后到 Cloudflare → 选中 <b>{domain.split('.').slice(-2).join('.')}</b> →
                DNS → Records → <b>Import and Export</b> → 粘贴导入。三条记录一次到位，不需要任何 API Token。
              </span>
            }
          />

          <Alert
            type="info"
            showIcon
            message="还需要在 VPS 服务商控制台设置 PTR"
            description={
              <span>
                把本机公网 IP 的反向解析指向 <b>{domain}</b>。没有 PTR 的 IP 会被 Gmail / QQ 直接判为垃圾。
                多数服务商（RackNerd / Vultr / DigitalOcean）在控制台的 Networking 或 rDNS 里改。
              </span>
            }
          />

          <Space.Compact style={{ width: '100%', maxWidth: 480 }}>
            <Input
              placeholder="收件邮箱，用于发测试邮件"
              value={testTo}
              onChange={(e) => setTestTo(e.target.value)}
              onPressEnter={sendTest}
              allowClear
            />
            <Button type="primary" icon={<SendOutlined />} onClick={sendTest} loading={busy}>
              发测试邮件
            </Button>
          </Space.Compact>
        </>
      )}
    </Space>
  );
}