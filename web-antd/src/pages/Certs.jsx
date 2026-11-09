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
import { api, apiPost } from '../api';
import { CopyText, ErrorBlock, PageCard, StatCard } from '../components';

const { Text } = Typography;

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
  const [zone, setZone] = useState('');
  const [busy, setBusy] = useState('');

  const caData = ca.data || {};
  const certData = cert.data || {};
  const siteCert = certData.cert;
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
      const ok = res.installed ?? res.count ?? '';
      message.success(`已下发到组网中心与节点${ok !== '' ? `（${ok} 处）` : ''}`);
    } catch (e) {
      message.error('下发失败：' + (e.message || e));
    } finally {
      setBusy('');
    }
  };

  const download = (kind) => {
    const token = localStorage.getItem('token') || '';
    const url = `/api/vpn/cert/download?kind=${kind}&token=${encodeURIComponent(token)}`;
    window.open(url, '_blank');
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
          accent="#10b981"
            hint={caData.common_name || '未初始化'}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
          icon={<SafetyCertificateOutlined />}
          label="CA 到期"
            value={fmtDate(caData.not_after)}
            accent="#0ea5e9"
            hint={caData.days_left != null ? `剩 ${caData.days_left} 天` : '—'}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
          icon={<KeyOutlined />}
          label="功能证书"
            value={siteCert ? (siteCert.domains || '').split(',').length + ' 个域名' : '未签发'}
            accent="#8b5cf6"
            hint={siteCert ? siteCert.source || 'acme' : '使用 Cloudflare DNS-01'}
          />
        </Col>
        <Col xs={12} lg={6}>
          <StatCard
          icon={<KeyOutlined />}
          label="证书到期"
            value={siteCert ? fmtDate(siteCert.not_after) : '—'}
            accent={siteCert && daysLeft <= 30 ? '#f59e0b' : '#16a34a'}
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
        subtitle="通过 Cloudflare DNS-01 申请通配证书，可一键下发到组网中心与全部节点"
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
          <Alert type="warning" showIcon message="尚未签发功能证书，点击右上角「签发 / 续签」开始。" />
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
              <Button icon={<DownloadOutlined />} onClick={() => download('cert')}>
                下载证书
              </Button>
              <Button icon={<DownloadOutlined />} onClick={() => download('chain')}>
                下载链
              </Button>
              <Button icon={<DownloadOutlined />} danger onClick={() => download('key')}>
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
            { k: '功能证书', v: '站点通配证书（*.域名），走 Cloudflare DNS-01 校验，自动续签需自行配置定时任务。' },
            { k: '下发', v: '写入组网中心 /etc/proxypilot/mesh/tls 与本机 VPN，随后通过 Agent 任务队列分发到各节点。' },
            { k: '客户端信任', v: '使用内部 CA 时，客户端可下载 CA 证书并以 -ca 参数指定，或改用 ACME 公共证书。' },
          ]}
        />
      </PageCard>
        </Col>
      </Row>
    </div>
  );
}