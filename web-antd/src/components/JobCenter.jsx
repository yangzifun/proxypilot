/* =================================================================
 *  后台任务中心
 *  · 顶栏入口 + 运行中徽标；点开抽屉看列表 / 逐项进度
 *  · 任何批量按钮拿到 { async:true, job_id } 后调用 openJobCenter 打开
 * ================================================================= */
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { App as AntdApp, Badge, Button, Drawer, Empty, Popconfirm, Progress, Space, Table, Tag, Tooltip } from 'antd';
import { ReloadOutlined, ThunderboltOutlined, CloseCircleOutlined, RedoOutlined } from '@ant-design/icons';
import { api, apiPost } from '../api';
import { fmtAgo } from '../format';

const bus = typeof window !== 'undefined' ? new EventTarget() : null;

export function openJobCenter(jobId) {
    if (!bus) return;
    bus.dispatchEvent(new CustomEvent('open', { detail: jobId || 0 }));
}

/** 批量接口返回 { async:true, job_id } 时自动打开任务中心，返回 true 表示已转后台 */
export function watchBatch(res) {
    if (res && res.async && res.job_id) {
        openJobCenter(res.job_id);
        return true;
    }
    return false;
}

const STATUS = {
    queued: ['default', '排队中'],
    running: ['processing', '执行中'],
    success: ['success', '成功'],
    partial: ['warning', '部分失败'],
    failed: ['error', '失败'],
    canceled: ['default', '已取消']
};

export default function JobCenter() {
    const { message } = AntdApp.useApp();
    const [open, setOpen] = useState(false);
    const [rows, setRows] = useState([]);
    const [active, setActive] = useState(0);
    const [loading, setLoading] = useState(false);
    const [detail, setDetail] = useState(null); // { job, items }
    const [detailLoading, setDetailLoading] = useState(false);
    const openRef = useRef(false);
    openRef.current = open;
    const detailRef = useRef(null);
    detailRef.current = detail;

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const res = await api('jobs?status=all&limit=20');
            setRows((res && res.data) || []);
            setActive(Number((res && res.active) || 0));
        } catch (e) {
            /* 静默：不打扰主流程 */
        } finally {
            setLoading(false);
        }
    }, []);

    const loadDetail = useCallback(async (jobId) => {
        if (!jobId) return;
        setDetailLoading(true);
        try {
            const res = await api('jobs/' + jobId + '?items=1&limit=200');
            setDetail({ job: (res && res.data) || null, items: (res && res.items) || [] });
        } catch (e) {
            message.error(e.message || '读取任务失败');
        } finally {
            setDetailLoading(false);
        }
    }, [message]);

    useEffect(() => {
        load();
        let tick = 0;
        const timer = setInterval(() => {
            tick++;
            if (openRef.current) {
                // 抽屉打开：列表 + 未结束任务的明细一起刷新（否则详情停在旧进度）
                load();
                const d = detailRef.current;
                if (d && d.job && ['queued', 'running'].includes(d.job.status)) loadDetail(d.job.id);
            } else if (tick % 5 === 0) {
                // 抽屉关闭：只低频刷新徽标
                load();
            }
        }, 3000);
        return () => clearInterval(timer);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [load, loadDetail]);

    useEffect(() => {
        if (!bus) return undefined;
        const onOpen = (e) => {
            setOpen(true);
            load();
            if (e.detail) loadDetail(e.detail);
        };
        bus.addEventListener('open', onOpen);
        return () => bus.removeEventListener('open', onOpen);
    }, [load, loadDetail]);

    const cancel = async (job) => {
        try {
            await apiPost('jobs/' + job.id + '/cancel', {});
            message.success('已取消，剩余项会标记为跳过');
            load();
            if (detail && detail.job && detail.job.id === job.id) loadDetail(job.id);
        } catch (e) {
            message.error(e.message || '取消失败');
        }
    };

    const retry = async (job) => {
        try {
            const r = await apiPost('jobs/' + job.id + '/retry', {});
            message.success('已重试 ' + (r.retry_items || 0) + ' 个失败项');
            load();
            loadDetail(job.id);
        } catch (e) {
            message.error(e.message || '重试失败');
        }
    };

    const columns = useMemo(() => [
        { title: 'ID', dataIndex: 'id', width: 56 },
        {
            title: '任务', dataIndex: 'title', ellipsis: true,
            render: (v, r) => (
                <Tooltip title={v}>
                    <span>{v || r.kind}</span>
                </Tooltip>
            )
        },
        {
            title: '状态', dataIndex: 'status', width: 92,
            render: (v) => {
                const [color, text] = STATUS[v] || ['default', v];
                return <Tag color={color} style={{ marginInlineEnd: 0 }}>{text}</Tag>;
            }
        },
        {
            title: '进度', width: 150,
            render: (_, r) => (
                <Progress
                    percent={Number(r.percent || 0)}
                    size="small"
                    status={r.status === 'failed' ? 'exception' : (r.status === 'running' ? 'active' : undefined)}
                    format={() => (r.done || 0) + '/' + (r.total || 0)}
                />
            )
        },
        { title: '失败', dataIndex: 'failed', width: 58, render: (v) => (Number(v) ? <b style={{ color: '#e11d48' }}>{v}</b> : 0) },
        { title: '时间', dataIndex: 'created_at', width: 92, render: (v) => <span className="pp-muted">{fmtAgo(v)}</span> },
        {
            title: '操作', width: 130,
            render: (_, r) => (
                <Space size={0}>
                    <Button type="link" size="small" onClick={() => loadDetail(r.id)}>详情</Button>
                    {['queued', 'running'].includes(r.status) ? (
                        <Popconfirm title="取消该任务？" description="当前执行中的项会跑完" okText="取消任务" onConfirm={() => cancel(r)}>
                            <Button type="text" size="small" danger icon={<CloseCircleOutlined />} aria-label="取消" />
                        </Popconfirm>
                    ) : null}
                    {Number(r.failed) > 0 ? (
                        <Tooltip title="只重试失败项">
                            <Button type="text" size="small" icon={<RedoOutlined />} onClick={() => retry(r)} aria-label="重试" />
                        </Tooltip>
                    ) : null}
                </Space>
            )
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    ], [detail]);

    const itemColumns = [
        { title: '#', dataIndex: 'seq', width: 52 },
        { title: '目标', dataIndex: 'label', ellipsis: true },
        {
            title: '状态', dataIndex: 'status', width: 88,
            render: (v) => {
                const map = { pending: ['default', '等待'], running: ['processing', '执行'], success: ['success', '成功'], failed: ['error', '失败'], skipped: ['default', '跳过'] };
                const [c, t] = map[v] || ['default', v];
                return <Tag color={c} style={{ marginInlineEnd: 0 }}>{t}</Tag>;
            }
        },
        { title: '结果', dataIndex: 'message', ellipsis: true, render: (v) => <span className="pp-muted" style={{ fontSize: 12 }}>{v || '—'}</span> }
    ];

    return (
        <>
            <Tooltip title="后台任务">
                <Badge count={active} size="small" offset={[-2, 4]}>
                    <Button type="text" icon={<ThunderboltOutlined />} onClick={() => { setOpen(true); load(); }}>
                        任务
                    </Button>
                </Badge>
            </Tooltip>

            <Drawer
                open={open}
                width={760}
                onClose={() => setOpen(false)}
                title={<span>后台任务 <span className="pp-muted" style={{ fontSize: 12, marginLeft: 8 }}>点批量操作后在这里看进度，关掉页面也会继续跑</span></span>}
                extra={<Button size="small" icon={<ReloadOutlined />} onClick={() => { load(); if (detail && detail.job) loadDetail(detail.job.id); }} loading={loading}>刷新</Button>}
                destroyOnClose
            >
                {detail && detail.job ? (
                    <div style={{ marginBottom: 18, padding: 12, border: '1px solid #eef2f7', borderRadius: 12, background: '#fbfcff' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                            <b>#{detail.job.id} · {detail.job.title}</b>
                            <Tag color={(STATUS[detail.job.status] || [])[0]}>{(STATUS[detail.job.status] || [])[1] || detail.job.status}</Tag>
                            <Button size="small" type="link" onClick={() => setDetail(null)}>收起</Button>
                        </div>
                        <Progress percent={Number(detail.job.percent || 0)} size="small" format={() => (detail.job.done || 0) + '/' + (detail.job.total || 0)} />
                        <Table
                            rowKey="id"
                            size="small"
                            loading={detailLoading}
                            dataSource={detail.items}
                            columns={itemColumns}
                            pagination={false}
                            scroll={{ y: 240 }}
                            style={{ marginTop: 8 }}
                        />
                    </div>
                ) : null}

                <Table
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={rows}
                    columns={columns}
                    pagination={false}
                    locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有批量任务" /> }}
                />
            </Drawer>
        </>
    );
}
