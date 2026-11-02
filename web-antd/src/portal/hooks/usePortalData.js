/* =================================================================
 *  用户中心数据 hooks（PC 单页与移动端共用）
 *  · useUuidStats   访问统计
 *  · useUuidInfo    UUID 信息（所有者 / 联系方式 / 备注）
 *  · useUuidSources 外部订阅源
 * ================================================================= */
import { useCallback, useEffect, useState } from 'react';
import { portalReq } from '../portalApi';

/** 访问统计：{ loading, data, error, reload } */
export function useUuidStats(uuid, days = 30) {
  const [state, setState] = useState({ loading: true, data: null, error: null });

  const reload = useCallback(async () => {
    if (!uuid) {
      setState({ loading: false, data: null, error: null });
      return;
    }
    setState({ loading: true, data: null, error: null });
    try {
      const { data } = await portalReq(`/manage/stats/${encodeURIComponent(uuid)}?days=${days}`);
      setState({ loading: false, data: data || {}, error: null });
    } catch (e) {
      setState({ loading: false, data: null, error: e });
    }
  }, [uuid, days]);

  useEffect(() => {
    reload();
  }, [reload]);

  return { ...state, reload };
}

/** UUID 信息（含编辑保存） */
export function useUuidInfo(uuid) {
  const [state, setState] = useState({ loading: !!uuid, data: null, error: null });

  const reload = useCallback(async () => {
    if (!uuid) {
      setState({ loading: false, data: null, error: null });
      return;
    }
    setState({ loading: true, data: null, error: null });
    try {
      const { data } = await portalReq(`/manage/uuid-info/${encodeURIComponent(uuid)}`);
      // 后端：{ success, exists, data: {...} }，兼容平铺与嵌套
      const payload = (data && data.data) || data || {};
      setState({ loading: false, data: payload, error: null });
    } catch (e) {
      setState({ loading: false, data: null, error: e });
    }
  }, [uuid]);

  useEffect(() => {
    reload();
  }, [reload]);

  const save = useCallback(
    async (patch) => {
      const current = state.data || {};
      await portalReq('/manage/uuid-info', {
        method: 'POST',
        body: {
          uuid,
          owner: current.owner || '',
          email: current.email || '',
          contact_info: current.contact_info || '',
          remark: current.remark || '',
          ...patch,
        },
      });
      await reload();
    },
    [uuid, state.data, reload]
  );

  return { ...state, reload, save };
}

/** 配置列表（分页 + 搜索 + 协议筛选） */
export function useUuidConfigs(uuid, onUnauthorized) {
  const [filters, setFilters] = useState({ q: '', protocol: 'all' });
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [cfg, setCfg] = useState({ list: [], total: 0, loading: true, error: null });

  const reload = useCallback(async () => {
    if (!uuid) {
      setCfg({ list: [], total: 0, loading: false, error: null });
      return;
    }
    setCfg((s) => ({ ...s, loading: true, error: null }));
    const qs = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
    if (filters.protocol !== 'all') qs.set('protocol', filters.protocol);
    if (filters.q.trim()) qs.set('q', filters.q.trim());
    try {
      const { data } = await portalReq(`/manage/configs/${encodeURIComponent(uuid)}?${qs.toString()}`);
      setCfg({
        list: (data && data.configs) || [],
        total: Number((data && data.pagination && data.pagination.total) || 0),
        loading: false,
        error: null,
      });
    } catch (e) {
      if (e.status === 401) {
        if (onUnauthorized) onUnauthorized();
        return;
      }
      if (e.status === 404) {
        setCfg({ list: [], total: 0, loading: false, error: null });
        return;
      }
      setCfg((s) => ({ ...s, loading: false, error: e }));
    }
  }, [uuid, page, pageSize, filters, onUnauthorized]);

  useEffect(() => {
    reload();
  }, [reload]);

  return {
    filters,
    setFilters,
    page,
    setPage,
    pageSize,
    setPageSize,
    list: cfg.list,
    total: cfg.total,
    loading: cfg.loading,
    error: cfg.error,
    reload,
  };
}

/** 外部订阅源列表（含新增 / 启停 / 删除） */
export function useUuidSources(uuid) {
  const [state, setState] = useState({ list: [], loading: true, error: null });

  const reload = useCallback(async () => {
    if (!uuid) {
      setState({ list: [], loading: false, error: null });
      return;
    }
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const { data } = await portalReq(`/manage/uuid-sub-sources?uuid=${encodeURIComponent(uuid)}`);
      setState({ list: (data && data.data) || [], loading: false, error: null });
    } catch (e) {
      setState({ list: [], loading: false, error: e });
    }
  }, [uuid]);

  useEffect(() => {
    reload();
  }, [reload]);

  /** 新增：{ url, remark } */
  const add = useCallback(
    async ({ url, remark = '' }) => {
      await portalReq('/manage/uuid-sub-sources', {
        method: 'POST',
        body: { uuid, url: String(url || '').trim(), remark: String(remark || '').trim() },
      });
      await reload();
    },
    [uuid, reload]
  );

  /** 启停：传当前行，内部取反 */
  const toggle = useCallback(
    async (row) => {
      await portalReq('/manage/uuid-sub-sources', {
        method: 'PUT',
        body: { id: row.id, enabled: Number(row.enabled) ? 0 : 1 },
      });
      await reload();
    },
    [reload]
  );

  const remove = useCallback(
    async (row) => {
      await portalReq('/manage/uuid-sub-sources', { method: 'DELETE', body: { id: row.id } });
      await reload();
    },
    [reload]
  );

  return { ...state, reload, add, toggle, remove };
}
