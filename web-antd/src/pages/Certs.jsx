import React, { useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Col,
  Row,
  Descriptions,
  Input,
  Popconfirm,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from 'antd';
import {
  DownloadOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  CloudUploadOutlined,
  KeyOutlined,
} from '@ant-design/icons';
import { useAsync } from '../hooks';
import { api, apiPost, getToken } from '../api';
import { CopyText, ErrorBlock, PageCard, StatCard } from '../components';

const { Text } = Typography;

const WILDCARD_DOMAINS = 'yzfn.top,*.yzfn.top';

function fmtTime(ms) {
  const n = Number(ms || 0);
  if (!n) return '—';
  return new Date(n).toLocaleString('zh-CN', { hour12: false });
}

function fmtDate(ms) {
  const n = Number(ms || 0);
  if (!n) return '—';
  return new Date(n).toLocaleDateString('zh-CN');
}

function daysTag(days, expired) {
  if (expired) return <Tag color="red">已过期</Tag>;
  const d = Number(days || 0);
  if (d <= 7) return <Tag color="red">剩 {d} 天</Tag>;
  if (d <= 30) return <Tag color="orange">剩 {d} 天</Tag>;
  return <Tag color="green">剩 {d} 天</Tag>;
}

/** 证书管理：本站内部 CA + 站点功能证书（签发 / 下发 / 下载） */
export default function Certs() {
  const ca = useAsync(() => api('vpn/ca/status'), []);
  const cert = useAsync(() => api('vpn/cert/status'), []);
  const certNodes = useAsync(() => api('vpn/cert/nodes'), []);
  const [zone, setZone] = useState('');
  const [busy, setBusy] = useState('');

  const caData = ca.data || {};
  const certData = cert.data || {};
  const siteCert = certData.cert;
  const nodeRows = (certNodes.data && certNodes.data.data) || [];
  const daysLeft = useMemo(() => {
    if (!siteCert || !siteCert.not_after) return 0;
    return Math.floor((Number(siteCert.not_after) - Date.now()) / 86400000);
  }, [siteCert]);

  const issue = async () => {
    setBusy('issue');
    try {
      const body = zone.trim() ? { zone: zone.trim() } : {};
      const res = await apiPost('vpn/cert/issue', body);
      message.success(`签发成功：${(res.domains || []).join('、') || '已更新'}`);
      cert.reload();
    } catch (e) {
      message.error('签发失败：' + (e.message || e));
    } finally {
      setBusy('');
    }
  };

  const distribute = async () => {
    setBusy('distribute');
    try {
      const res = await apiPost('vpn/cert/distribute', {});
      const ok = res.queued ?? res.installed ?? res.count ?? '';
      message.success(`已下发到组网中心与节点${ok !== '' ? `（${ok} 处）` : ''}`);
      certNodes.reload().catch(() => {});
    } catch (e) {
      message.error('下发失败：' + (e.message || e));
    } finally {
      setBusy('');
    }
  };

  // 下载走 fetch + Authorization 头，再转 blob 触发下载。
  //
  // 旧实现是 window.open('/api/vpn/cert/download?...&token=...')：window.open 不会带
  // Authorization 头，而后端 authed 只认 Header，query 里的 token 没有任何代码消费，
  // 所以下载恒 401（审计 P1-5）。改为与 Backup.jsx 一致的 fetch + Bearer 头方案，
  // 不额外开后端「query token」通道，避免 JWT 落到 URL / 访问日志 / Referer 里。
  const download = async (kind) => {
    setBusy('download-' + kind);
    try {
      const res = await fetch(`/api/vpn/cert/download?kind=${encodeURIComponent(kind)}`, {
        headers: { Authorization: 'Bearer ' + getToken() },
      });
      if (!res.ok) {
        let msg = `下载失败（HTTP ${res.status}）`;
        try {
          const j = await res.json();
          if (j && j.error) msg = j.error;
        } catch (e) {
          /* ignore */
        }
        throw new Error(msg);
      }
      const blob = await res.blob();
      const cd = res.headers.get('Content-Disposition') || '';
      const m = /filename="?([^";]+)"?/.exec(cd);
      const name = (m && m[1]) || `site-${kind}.pem`;
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
    } catch (e) {
      message.error(e.message || '下载失败');
    } finally {
      setBusy('');
    }
  };

  const caRows = [
    { k: '主体', v: caData.subject || '—' },
    { k: '序列号', v: caData.serial || '—' },
    { k: '签名算法', v: caData.algorithm || '—' },
    { k: '生效时间', v: fmtTime(caData.not_before) },
    { k: '到期时间', v: fmtTime(caData.not_after) },
    { k: 'SHA-256 指纹', v: caData.fingerprint ? <CopyText text={caData.fingerprint} /> : '—' },
    { k: '已签发节点证书', v: caData.issued_node_certs ?? '—' },
  ];

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={12} lg={6}>
          <StatCard
          icon={<SafetyCertificateOutlined />}
          label="本站 CA"
          value={caData.ok ? '可用' : '不可用'}
          accent="var(--pp-success)"
            hint={caData.common_name || '未初始化'}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
          icon={<SafetyCertificateOutlined />}
          label="CA 到期"
            value={fmtDate(caData.not_after)}
            accent="var(--pp-chart-7)"
            hint={caData.days_left != null ? `剩 ${caData.days_left} 天` : '—'}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
          icon={<KeyOutlined />}
          label="功能证书"
            value={siteCert ? (siteCert.domains || '').split(',').length + ' 个域名' : '未签发'}
            accent="var(--pp-chart-6)"
            hint={siteCert ? siteCert.source || 'acme' : '使用 Cloudflare DNS-01'}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
          icon={<KeyOutlined />}
          label="证书到期"
            value={siteCert ? fmtDate(siteCert.not_after) : '—'}
            accent={siteCert && daysLeft <= 30 ? 'var(--pp-warning)' : 'var(--pp-success)'}
            hint={siteCert ? `剩 ${daysLeft} 天` : '—'}
          />
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={12}>
      <PageCard
        title="本站 CA 证书"
        subtitle="面板内置 CA（ECDSA P-256，10 年），用于给节点 VPN 签发内部证书"
        extra={
          <Space>
            <Button icon={<DownloadOutlined />} href="/api/vpn/ca.crt" target="_blank">
              下载 CA 证书
            </Button>
            <Button icon={<ReloadOutlined />} onClick={() => ca.reload()}>
              刷新
            </Button>
          </Space>
        }
      >
        {ca.error ? (
          <ErrorBlock error={ca.error} />
        ) : (
          <>
            {!caData.ok && caData.error ? (
              <Alert type="warning" showIcon message={caData.error} style={{ marginBottom: 12 }} />
            ) : null}
            <Descriptions bordered size="small" column={1} items={caRows.map((r) => ({
              key: r.k,
              label: r.k,
              children: r.v,
            }))} />
            <Alert
              style={{ marginTop: 12 }}
              type="info"
              showIcon
              message="客户端首次连接会校验节点证书；使用内部 CA 时需信任此 CA 证书（客户端支持 -ca 指定）。"
            />
          </>
        )}
      </PageCard>

        </Col>
        <Col xs={24} xl={12}>
      <PageCard
        title="功能证书（站点 / 节点通配）"
        subtitle="通过 Cloudflare DNS-01 申请通配证书，同时下发到组网中心、VPN 与 sing-box"
        extra={
          <Space>
            <Button
              type="primary"
              icon={<SafetyCertificateOutlined />}
              loading={busy === 'issue'}
              onClick={() => issue()}
            >
              签发 / 续签
            </Button>
            <Popconfirm
              title="下发到所有节点？"
              description="会覆盖组网中心与各节点现有证书并重启对应服务"
              onConfirm={distribute}
              okText="下发"
              cancelText="取消"
            >
              <Button icon={<CloudUploadOutlined />} loading={busy === 'distribute'} disabled={!siteCert}>
                下发到全部节点
              </Button>
            </Popconfirm>
            <Button icon={<ReloadOutlined />} onClick={() => cert.reload()}>
              刷新
            </Button>
          </Space>
        }
      >
        <Descriptions
          bordered
          size="small"
          column={1}
          style={{ marginBottom: 12 }}
          items={[
            {
              key: 'wildcard',
              label: '目标通配域名',
              children: <span className="pp-mono">{WILDCARD_DOMAINS}</span>,
            },
            {
              key: 'paths',
              label: '下发路径',
              children: (
                <span>
                  <span className="pp-mono">/etc/proxypilot/tunnel/tls/</span>
                  {' · '}
                  <span className="pp-mono">/etc/sing-box/cert.crt + private.key</span>
                </span>
              ),
            },
          ]}
        />
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="证书会同时写入 tunnel 与 sing-box；sing-box 仅在服务 active 时自动重启。"
        />
        <Space wrap style={{ marginBottom: 12 }}>
          <Text type="secondary">签发 Zone：</Text>
          <Input
            placeholder="留空使用设置里的 vpn_cert_zone（默认 yzfn.top）"
            value={zone}
            onChange={(e) => setZone(e.target.value)}
            style={{ width: 320 }}
            allowClear
          />
          <Text type="secondary">
            Cloudflare Token：{certData.has_cloudflare_token ? '已配置' : '未配置'}
          </Text>
        </Space>

        {cert.error ? (
          <ErrorBlock error={cert.error} />
        ) : !siteCert ? (
          <Alert type="warning" showIcon message={<>尚未签发功能证书。通配证书目标为 <b>{WILDCARD_DOMAINS}</b>，请先点击右上角「签发 / 续签」。</>} />
        ) : (
          <>
            <Descriptions
              bordered
              size="small"
              column={2}
              items={[
                { key: 'd', label: '域名', children: siteCert.domains || '—' },
                { key: 's', label: '来源', children: <Tag color="blue">{siteCert.source || 'acme'}</Tag> },
                { key: 'nb', label: '生效时间', children: fmtTime(siteCert.not_before) },
                {
                  key: 'na',
                  label: '到期时间',
                  children: (
                    <Space>
                      {fmtTime(siteCert.not_after)}
                      {daysTag(daysLeft, daysLeft <= 0)}
                    </Space>
                  ),
                },
                {
                  key: 'fp',
                  label: 'SHA-256 指纹',
                  children: siteCert.fingerprint ? <CopyText text={siteCert.fingerprint} /> : '—',
                  span: 2,
                },
                { key: 'u', label: '更新时间', children: fmtTime(siteCert.updated_at), span: 2 },
              ]}
            />
            <Space style={{ marginTop: 12 }}>
              <Button icon={<DownloadOutlined />} loading={busy === 'download-cert'} onClick={() => download('cert')}>
                下载证书
              </Button>
              <Button icon={<DownloadOutlined />} loading={busy === 'download-chain'} onClick={() => download('chain')}>
                下载链
              </Button>
              <Button icon={<DownloadOutlined />} danger loading={busy === 'download-key'} onClick={() => download('key')}>
                下载私钥
              </Button>
            </Space>
          </>
        )}
      </PageCard>

        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col span={24}>
      <PageCard
        title="节点证书同步"
        subtitle="证书下发目标与 sing-box 服务状态；下发后刷新查看"
        extra={<Button icon={<ReloadOutlined />} onClick={() => certNodes.reload().catch(() => {})} loading={certNodes.loading}>刷新</Button>}
      >
        {certNodes.error ? (
          <ErrorBlock error={certNodes.error} />
        ) : (
          <Table
            className="pp-table"
            size="small"
            rowKey="id"
            loading={certNodes.loading}
            dataSource={nodeRows}
            pagination={false}
            locale={{ emptyText: '暂无节点（link_mode=local 的本机节点不在此表）' }}
            columns={[
              {
                title: '节点名称',
                dataIndex: 'name',
                render: (v, row) => <span>{v || `#${row.id}`}</span>,
              },
              {
                title: 'TLS 域名',
                dataIndex: 'domain',
                render: (v) => (v ? <span className="pp-mono">{v}</span> : <span className="pp-muted">—</span>),
              },
              {
                title: 'sing-box 服务',
                dataIndex: 'singbox_service',
                render: (v) => {
                  const state = String(v || 'unknown');
                  const color = state === 'active' ? 'green' : state === 'inactive' ? 'orange' : 'default';
                  return <Tag color={color}>{state}</Tag>;
                },
              },
              {
                title: '证书状态',
                dataIndex: 'certificate',
                render: (v, row) => (
                  <Tooltip title={row.fingerprint || undefined}>
                    <Tag color="blue">{String(v || 'unknown')}</Tag>
                  </Tooltip>
                ),
              },
              {
                title: '最近同步',
                dataIndex: 'last_synced_at',
                render: (v, row) => (v || row.updated_at ? fmtTime(v || row.updated_at) : <span className="pp-muted">—</span>),
              },
              {
                title: '错误',
                dataIndex: 'error',
                render: (v) => (v ? <span className="pp-danger">{v}</span> : <span className="pp-muted">—</span>),
              },
            ]}
          />
        )}
      </PageCard>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col span={24}>
      <PageCard title="使用说明" subtitle="证书用途与注意事项">
        <Table
          size="small"
          pagination={false}
          rowKey="k"
          columns={[
            { title: '项目', dataIndex: 'k', width: 180 },
            { title: '说明', dataIndex: 'v' },
          ]}
          dataSource={[
            { k: '本站 CA', v: '面板内置 CA，启动时自动创建并加密存库；用于给节点 VPN 签发内部证书（90 天）。' },
            { k: '功能证书', v: '站点/节点通配证书（yzfn.top,*.yzfn.top），走 Cloudflare DNS-01 校验；自动续签需自行配置定时任务。' },
            { k: '下发', v: '写入组网中心 /etc/proxypilot/mesh/tls、本机 VPN /etc/proxypilot/tunnel/tls 与 sing-box /etc/sing-box；随后通过 Agent 任务队列分发到各节点。' },
            { k: '客户端信任', v: '使用内部 CA 时，客户端可下载 CA 证书并以 -ca 参数指定，或改用 ACME 公共证书。' },
          ]}
        />
      </PageCard>
        </Col>
      </Row>
    </div>
  );
}