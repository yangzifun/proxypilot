import React, { useState } from 'react';
import {
  CloudDownloadOutlined,
  CloudUploadOutlined,
  DatabaseOutlined,
  InboxOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App as AntdApp,
  Button,
  Card,
  Col,
  Popconfirm,
  Row,
  Space,
  Switch,
  Tag,
  Typography,
  Upload,
} from 'antd';
import { getToken } from '../api';
import { PageCard } from '../components';
import { fmtBytes } from '../format';

export default function Backup() {
  const { message } = AntdApp.useApp();
  const [includeBucket, setIncludeBucket] = useState(true);
  const [includeAgent, setIncludeAgent] = useState(false);
  const [includeLogs, setIncludeLogs] = useState(true);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [file, setFile] = useState(null);
  const [result, setResult] = useState(null);

  const exportBackup = async () => {
    setExporting(true);
    try {
      const qs = `bucket=${includeBucket ? '1' : '0'}&agent=${includeAgent ? '1' : '0'}&logs=${includeLogs ? '1' : '0'}`;
      const res = await fetch(`/api/backup/export?${qs}`, {
        headers: { Authorization: 'Bearer ' + getToken() },
      });
      if (!res.ok) {
        let msg = '导出失败（HTTP ' + res.status + '）';
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
      const name = (m && m[1]) || 'proxypilot-backup.zip';
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
      message.success('备份已生成并开始下载：' + name);
    } catch (e) {
      message.error(e.message || '导出失败');
    } finally {
      setExporting(false);
    }
  };

  const importBackup = async () => {
    if (!file) {
      message.error('请先选择 ZIP 备份文件');
      return;
    }
    setImporting(true);
    setResult(null);
    try {
      const fd = new FormData();
      fd.append('file', file);
      const res = await fetch('/api/backup/import', {
        method: 'POST',
        headers: { Authorization: 'Bearer ' + getToken() },
        body: fd,
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(j.error || '导入失败（HTTP ' + res.status + '）');
      setResult(j);
      message.success(`导入完成：${j.tables} 张表 / ${j.rows} 行 / ${j.files} 个文件`);
      setFile(null);
    } catch (e) {
      message.error(e.message || '导入失败');
    } finally {
      setImporting(false);
    }
  };

  return (
    <PageCard
      title={<span><DatabaseOutlined /> 备份管理</span>}
      extra={<Tag color="blue">仅超级管理员</Tag>}
    >
      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
        message="导入会覆盖当前数据库与桶文件"
        description="导入前请确认已备份当前系统；恢复 Secret 需要相同的 PP_JWT_SECRET 与 PP_AGENT_KEY_SECRET。"
      />

      <Row gutter={[16, 16]}>
        <Col xs={24} lg={12}>
          <Card title={<span><CloudDownloadOutlined /> 导出备份</span>} bordered>
            <Space direction="vertical" size={14} style={{ width: '100%' }}>
              <Space wrap>
                <span>包含对象桶文件</span>
                <Switch checked={includeBucket} onChange={setIncludeBucket} />
                <span>包含 Agent 二进制</span>
                <Switch checked={includeAgent} onChange={setIncludeAgent} />
                <span>包含访问日志</span>
                <Switch checked={includeLogs} onChange={setIncludeLogs} />
              </Space>
              <Typography.Text type="secondary">
                导出 `schema.sql`、全表 JSON、桶内文件与 manifest；Secret 为加密值。
              </Typography.Text>
              <Button type="primary" icon={<CloudDownloadOutlined />} loading={exporting} onClick={exportBackup}>
                生成并下载 ZIP
              </Button>
            </Space>
          </Card>
        </Col>

        <Col xs={24} lg={12}>
          <Card title={<span><CloudUploadOutlined /> 导入备份</span>} bordered>
            <Space direction="vertical" size={14} style={{ width: '100%' }}>
              <Upload.Dragger
                accept=".zip,application/zip"
                maxCount={1}
                beforeUpload={(f) => {
                  setFile(f);
                  return false;
                }}
                onRemove={() => setFile(null)}
                fileList={file ? [{ uid: '-1', name: file.name, size: file.size, status: 'done' }] : []}
              >
                <p className="ant-upload-drag-icon"><InboxOutlined /></p>
                <p className="ant-upload-text">点击或拖拽 ProxyPilot 备份 ZIP 到这里</p>
                <p className="ant-upload-hint">仅支持本系统导出的格式；最大 2 GiB</p>
              </Upload.Dragger>
              {file ? (
                <Typography.Text type="secondary">
                  已选择：{file.name}（{fmtBytes(file.size)}）
                </Typography.Text>
              ) : null}
              <Popconfirm
                title="确认导入备份？"
                description="数据库表将被备份内容整表替换，桶文件会被覆盖。"
                okText="确认导入"
                okButtonProps={{ danger: true }}
                onConfirm={importBackup}
              >
                <Button danger icon={<CloudUploadOutlined />} loading={importing} disabled={!file}>
                  开始导入
                </Button>
              </Popconfirm>
              {result ? (
                <Alert
                  type="success"
                  showIcon
                  message="导入完成"
                  description={`版本 ${result.version || 'unknown'} · ${result.tables} 张表 · ${result.rows} 行 · ${result.files} 个文件`}
                />
              ) : null}
            </Space>
          </Card>
        </Col>
      </Row>
    </PageCard>
  );
}
