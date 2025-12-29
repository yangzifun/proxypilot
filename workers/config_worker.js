/* =================================================================
 * Cloudflare Worker: Config Manager + External Link + Analytics + Domain Hosting
 * 功能：配置存储、编辑、订阅管理，包含跳转至配置生成的按钮和统计分析
 * 新增：KV 状态记录 (Session Persistence via IP)
 * ================================================================= */

// =================================================================
//  GLOBAL UTILITIES (后端工具函数)
// =================================================================

function utf8_to_b64(str) {
    return btoa(encodeURIComponent(str).replace(/%([0-9A-F]{2})/g,
        function toSolidBytes(match, p1) {
            return String.fromCharCode(parseInt(p1, 16));
        }));
}

function b64_to_utf8(str) {
    try {
        return decodeURIComponent(atob(str).split('').map(function(c) {
            return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
        }).join(''));
    } catch (e) {
        throw new Error("无效的 Base64 字符串");
    }
}

function jsonResponse(data, status = 200) {
    return new Response(JSON.stringify(data, null, 2), {
        status: status,
        headers: {
            'Content-Type': 'application/json;charset=UTF-8',
            'Access-Control-Allow-Origin': '*'
        },
    });
}

function getProtocol(configStr) {
    if (!configStr || typeof configStr !== 'string') return 'unknown';
    if (configStr.startsWith('vmess://')) return 'vmess';
    if (configStr.startsWith('vless://')) return 'vless';
    if (configStr.startsWith('trojan://')) return 'trojan';
    if (configStr.startsWith('hysteria2://')) return 'hysteria2';
    if (configStr.startsWith('tuic://')) return 'tuic';
    if (configStr.startsWith('anytls://')) return 'anytls';
    if (configStr.startsWith('socks5://')) return 'socks5';
    if (configStr.startsWith('any-reality://')) return 'any-reality';
    if (configStr.startsWith('ss://')) return 'ss';
    return 'unknown';
}

function extractRemarkFromConfig(configStr, protocol) {
    try {
        if (protocol === 'vmess') {
            const vmessObj = JSON.parse(b64_to_utf8(configStr.substring(8)));
            return vmessObj.ps || vmessObj.remark || null;
        } else if (protocol === 'vless' || protocol === 'trojan' ||
            protocol === 'hysteria2' || protocol === 'tuic' ||
            protocol === 'anytls' || protocol === 'socks5' ||
            protocol === 'any-reality' || protocol === 'ss') {
            const url = new URL(configStr);
            if (url.hash) return decodeURIComponent(url.hash.substring(1));
        }
    } catch (e) { }
    return null;
}

// =================================================================
//  DATABASE LOGIC (数据库操作)
// =================================================================

// 获取配置列表
async function fetchConfigsByUuidFromDB(uuid, env) {
    const db = env.DB;
    if (!db) return [];
    try {
        const stmt = db.prepare('SELECT id, config_data, protocol, remark, domain_hosting FROM configs WHERE uuid = ? ORDER BY id ASC');
        const { results } = await stmt.bind(uuid).all();
        return results;
    } catch (e) { return []; }
}

// 获取UUID访问统计
async function fetchUuidAccessStatsFromDB(uuid, env, days = 30) {
    const db = env.DB;
    if (!db) return { success: false, error: "数据库未连接" };

    try {
        const tableCheck = await db.prepare(
            "SELECT name FROM sqlite_master WHERE type='table' AND name='config_access_logs'"
        ).first();

        if (!tableCheck) {
            return {
                success: false,
                error: "访问日志表不存在，请确保已升级到v2.0+版本"
            };
        }

        const totalStats = await db.prepare(`
            SELECT
                COUNT(*) as total_access,
                SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END) as subscription_count,
                SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END) as apigen_count,
                MIN(created_at) as first_access,
                MAX(created_at) as last_access
            FROM config_access_logs
            WHERE uuid = ?
        `).bind(uuid).first();

        const today = new Date().toISOString().split('T')[0];
        const todayStats = await db.prepare(`
            SELECT
                COUNT(*) as today_total,
                SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END) as today_subscription,
                SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END) as today_apigen
            FROM config_access_logs
            WHERE uuid = ? AND DATE(created_at) = ?
        `).bind(uuid, today).first();

        const startDate = new Date();
        startDate.setDate(startDate.getDate() - days);
        const startDateStr = startDate.toISOString().split('T')[0];

        const dailyStats = await db.prepare(`
            SELECT
                DATE(created_at) as date,
                COUNT(*) as total,
                SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END) as subscription,
                SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END) as api_generation
            FROM config_access_logs
            WHERE uuid = ? AND DATE(created_at) >= ?
            GROUP BY DATE(created_at)
            ORDER BY date ASC
        `).bind(uuid, startDateStr).all();

        const recentLogs = await db.prepare(`
            SELECT
                uuid, query_type, client_ip, user_agent, created_at
            FROM config_access_logs
            WHERE uuid = ?
            ORDER BY created_at DESC
                LIMIT 50
        `).bind(uuid).all();

        return {
            success: true,
            uuid: uuid,
            total_access: totalStats?.total_access || 0,
            subscription_count: totalStats?.subscription_count || 0,
            apigen_count: totalStats?.apigen_count || 0,
            first_access: totalStats?.first_access,
            last_access: totalStats?.last_access,
            today_total: todayStats?.today_total || 0,
            today_subscription: todayStats?.today_subscription || 0,
            today_apigen: todayStats?.today_apigen || 0,
            daily_stats: dailyStats?.results || [],
            recent_logs: recentLogs?.results || []
        };

    } catch (e) {
        console.error("获取UUID访问统计失败:", e.message);
        return {
            success: false,
            error: "数据库查询失败: " + e.message
        };
    }
}

// 订阅输出 (Raw Base64)
async function handleRawSubscription(uuid, env) {
    if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);
    const configs = await fetchConfigsByUuidFromDB(uuid, env);
    if (!configs || configs.length === 0) return new Response("UUID Not Found or Empty", { status: 404 });

    const configList = configs.map(c => c.config_data);
    return new Response(btoa(configList.join('\n')), {
        status: 200,
        headers: {
            'Content-Type': 'text/plain;charset=UTF-8',
            'Subscription-User-Info': 'upload=0; download=0; total=10737418240000; expire=2524608000'
        },
    });
}

// =================================================================
//  API HANDLERS (接口逻辑)
// =================================================================

async function handleAddConfig(request, env) {
    let body;
    try { body = await request.json(); } catch (e) { return jsonResponse({ error: '无效 JSON' }, 400); }
    const { uuid, config_data, domain_hosting = 'Cloudflare' } = body;
    if (!uuid || !config_data) return jsonResponse({ error: '字段缺失' }, 400);

    const validDomainHostings = [
        'Cloudflare', '阿里ESA', '腾讯Edgeone', 'AWS Cloudfront',
        'Gcore', 'Fastly', 'CacheFly', 'LightCDN', 'Vercel', 'Netlify',
        '无', '其他'
    ];
    const hostingValue = validDomainHostings.includes(domain_hosting) ? domain_hosting : 'Cloudflare';

    const lines = config_data.split('\n').map(l => l.trim()).filter(Boolean);
    const stmts = [];
    for (const line of lines) {
        const p = getProtocol(line);
        if (p === 'unknown') continue;
        const remark = extractRemarkFromConfig(line, p);
        stmts.push(env.DB.prepare('INSERT INTO configs (uuid, config_data, protocol, remark, domain_hosting, created_at, updated_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(uuid,config_data) DO NOTHING').bind(uuid, line, p, remark, hostingValue, Date.now(), Date.now()));
    }
    if (stmts.length === 0) return jsonResponse({ error: '无有效配置' }, 400);
    await env.DB.batch(stmts);
    return jsonResponse({ success: true, uuid: uuid }); // 返回 UUID 供 Router 更新 KV
}

async function handleUpdateConfig(request, env) {
    let body;
    try { body = await request.json(); } catch (e) { return jsonResponse({ error: '无效JSON' }, 400); }
    const { id, config_data, domain_hosting = 'Cloudflare' } = body;

    const protocol = getProtocol(config_data);
    if(protocol === 'unknown') return jsonResponse({ error: '不支持的配置格式' }, 400);
    const remark = extractRemarkFromConfig(config_data, protocol);

    const validDomainHostings = [
        'Cloudflare', '阿里ESA', '腾讯Edgeone', 'AWS Cloudfront',
        'Gcore', 'Fastly', 'CacheFly', 'LightCDN', 'Vercel', 'Netlify',
        '无', '其他'
    ];
    const hostingValue = validDomainHostings.includes(domain_hosting) ? domain_hosting : 'Cloudflare';

    try {
        const res = await env.DB.prepare('UPDATE configs SET config_data = ?, protocol = ?, remark = ?, domain_hosting = ?, updated_at = ? WHERE id = ?')
            .bind(config_data, protocol, remark, hostingValue, Date.now(), id).run();
        return res.changes > 0 ? jsonResponse({ success: true, message: 'Updated' }) : jsonResponse({ error: '未变更' }, 404);
    } catch(e) { return jsonResponse({ error: e.message }, 500); }
}

async function handleGetConfigs(uuid, env) {
    const results = await fetchConfigsByUuidFromDB(uuid, env);
    return jsonResponse({ uuid, configs: results });
}

async function handleDelete(type, value, env) {
    const sql = type === 'uuid' ? 'DELETE FROM configs WHERE uuid = ?' : 'DELETE FROM configs WHERE id = ?';
    await env.DB.prepare(sql).bind(value).run();
    return jsonResponse({ success: true });
}

// =================================================================
//  MAIN ROUTER (已修改：集成 KV 状态管理)
// =================================================================

export default {
    async fetch(request, env, ctx) {
        const url = new URL(request.url);
        const path = url.pathname;
        const method = request.method;
        const DOMAIN_NAME = url.origin;

        // 1. 获取客户端 IP 用于区分用户
        const clientIP = request.headers.get('CF-Connecting-IP') || 'unknown-ip';

        const manageUUID = new URLPattern({ pathname: '/manage/configs/:uuid' });
        const manageID = new URLPattern({ pathname: '/manage/configs/id/:id' });
        const subUUID = new URLPattern({ pathname: '/sub/:uuid' });
        const uuidStats = new URLPattern({ pathname: '/manage/stats/:uuid' });

        try {
            if (method === 'GET') {
                if (path === '/') {
                    // 2. 访问首页：尝试从 KV 读取该 IP 上次使用的 UUID
                    let lastUsedUuid = '';
                    if (env.KV && clientIP !== 'unknown-ip') {
                        try {
                            lastUsedUuid = await env.KV.get(`user_last_uuid_${clientIP}`);
                        } catch (e) { console.error("KV Read Error:", e); }
                    }

                    // 3. 将 UUID 注入到 HTML 模板中，前端 JS 会读取这个变量
                    let html = managePageHtmlContent.replace(/YOUR_WORKER_DOMAIN_PATH/g, DOMAIN_NAME);
                    html = html.replace(/__SERVER_INJECTED_UUID__/g, lastUsedUuid || '');

                    return new Response(html, {
                        headers: { 'Content-Type': 'text/html;charset=UTF-8' }
                    });
                }

                const subMatch = subUUID.exec(url);
                if (subMatch) return await handleRawSubscription(subMatch.pathname.groups.uuid, env);

                const uuidMatch = manageUUID.exec(url);
                if (uuidMatch) {
                    const currentUuid = uuidMatch.pathname.groups.uuid;

                    // 4. 查询配置 API：异步将 UUID 写入 KV，记录该 IP 的状态 (保留30天)
                    if (env.KV && clientIP !== 'unknown-ip') {
                        ctx.waitUntil(env.KV.put(`user_last_uuid_${clientIP}`, currentUuid, { expirationTtl: 60 * 60 * 24 * 30 }));
                    }

                    return await handleGetConfigs(currentUuid, env);
                }

                const statsMatch = uuidStats.exec(url);
                if (statsMatch) {
                    const urlObj = new URL(request.url);
                    const days = urlObj.searchParams.get('days') || 30;
                    const currentUuid = statsMatch.pathname.groups.uuid;

                    // 统计查询时也顺便更新 KV
                    if (env.KV && clientIP !== 'unknown-ip') {
                        ctx.waitUntil(env.KV.put(`user_last_uuid_${clientIP}`, currentUuid, { expirationTtl: 60 * 60 * 24 * 30 }));
                    }

                    const stats = await fetchUuidAccessStatsFromDB(currentUuid, env, parseInt(days));
                    return jsonResponse(stats);
                }
            }

            if (method === 'POST' && path === '/manage/configs') {
                // 添加配置时，因为 Body 只能读一次，我们先克隆 request 获取 uuid
                // 但为了不影响 handleAddConfig 的逻辑，我们在 handleAddConfig 返回后，如果成功，
                // 其实 handleAddConfig 内部并没有返回 UUID 给我们，所以这里简单做法是 clone
                const cloneReq = request.clone();
                try {
                    const body = await cloneReq.json();
                    if (body.uuid && env.KV && clientIP !== 'unknown-ip') {
                        ctx.waitUntil(env.KV.put(`user_last_uuid_${clientIP}`, body.uuid, { expirationTtl: 60 * 60 * 24 * 30 }));
                    }
                } catch(e) {}

                return await handleAddConfig(request, env);
            }

            if (method === 'PUT' && path === '/manage/configs') return await handleUpdateConfig(request, env);

            if (method === 'DELETE') {
                const idMatch = manageID.exec(url);
                if (idMatch) return await handleDelete('id', idMatch.pathname.groups.id, env);
                const uuidMatch = manageUUID.exec(url);
                if (uuidMatch) return await handleDelete('uuid', uuidMatch.pathname.groups.uuid, env);
            }

            return new Response('404 Not Found', { status: 404 });
        } catch (err) {
            return new Response("Error: " + err.message, { status: 500 });
        }
    }
};

// =================================================================
//  FRONTEND CONTENT (Updated with KV State Injection)
// =================================================================

const newGlobalStyle = `
html { font-size: 87.5%; } body, html { margin: 0; padding: 0; min-height: 100%; background-color: #fff; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
.container { width: 100%; min-height: 100vh; display: flex; flex-direction: column; justify-content: center; align-items: center; padding: 40px 20px; box-sizing: border-box; }
.content-group { width: 100%; max-width: 1000px; text-align: center; z-index: 10; box-sizing: border-box; }
.profile-name { font-size: 2.2rem; color: #3d474d; margin-bottom: 10px; font-weight: bold;}
.profile-quote { color: #89949B; margin-bottom: 27px; min-height: 1.2em; }
.nav-grid { display: flex; flex-wrap: wrap; justify-content: center; gap: 8px; margin-bottom: 27px; }
.nav-btn { display: inline-flex; align-items: center; justify-content: center; padding: 8px 16px; text-align: center; background: #E8EBED; border: 2px solid #89949B; border-radius: 4px; color: #5a666d; text-decoration: none !important; font-weight: 500; font-size: 0.95rem; line-height: 1.2; transition: all 0.3s; white-space: nowrap; cursor: pointer; box-sizing: border-box; }
.nav-btn:hover:not(:disabled) { background: #89949B; color: white; }
.nav-btn:disabled { opacity: 0.6; cursor: not-allowed;}
.nav-btn.primary { background-color: #5a666d; color: white; border-color: #5a666d;}
.nav-btn.primary:hover:not(:disabled) { background-color: #3d474d; }
.card { background: #f8f9fa; border: 1px solid #E8EBED; border-radius: 8px; padding: 24px; margin-bottom: 24px; text-align: left; }
.card h2 { font-size: 1.5rem; color: #3d474d; margin-top: 0; margin-bottom: 20px; text-align: center;}
.form-group { margin-bottom: 16px; }
.form-group label { display: block; color: #5a666d; font-weight: 500; margin-bottom: 8px; font-size: 0.9rem;}
textarea, input[type="text"], input[type="number"], select { width: 100%; padding: 10px; border: 2px solid #89949B; border-radius: 4px; background: #fff; font-family: 'SF Mono', 'Courier New', monospace; font-size: 0.9rem; box-sizing: border-box; resize: vertical; margin-bottom: 5px;}
textarea:focus, input:focus, select:focus { outline: none; border-color: #3d474d; }
.info-box { background-color: #e8ebed; color: #5a666d; border-left: 4px solid #89949B; padding: 12px 16px; border-radius: 4px; font-size: 0.85rem; text-align: left; line-height: 1.5; margin: 16px 0; }
.footer { margin-top: 40px; text-align: center; color: #89949B; font-size: 0.8rem; }
.footer a { color: #5a666d; text-decoration: none; }
.hidden { display: none; }
#toast-container { position: fixed; top: 20px; right: 20px; z-index: 9999; display: flex; flex-direction: column; gap: 10px; }
.toast { display: flex; align-items: center; padding: 12px 18px; border-radius: 4px; box-shadow: 0 4px 12px rgba(0,0,0,0.1); font-weight: 500; font-size: 0.9rem; border: 2px solid #89949B; background: #fff; color: #3d474d; opacity: 0; transform: translateX(100%); animation: slideIn 0.5s forwards, fadeOut 0.5s 4.5s forwards; }
@keyframes slideIn { to { opacity: 1; transform: translateX(0); } }
@keyframes fadeOut { from { opacity: 1; } to { opacity: 0; transform: translateX(100%); } }

.table-container { overflow-x: auto; border: 2px solid #89949B; border-radius: 4px; background: #fff; margin-top:20px;}
table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
th, td { padding: 10px 14px; text-align: left; border-bottom: 2px solid #E8EBED; white-space: nowrap; }
th { font-weight: bold; color: #3d474d; background-color: #f0f2f5; }
.config-data-cell { white-space: normal; word-break: break-all; max-width: 200px; font-size: 0.8rem; color: #666; }
.actions-cell button { margin-right: 5px; padding: 4px 8px; font-size: 0.8rem; }
.domain-hosting-cell { font-size: 0.85rem; font-weight: 500; }

.hosting-badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 0.75rem; font-weight: 500; line-height: 1.4; }
.hosting-cloudflare { background: #F6821F; color: #fff; border: 1px solid #F6821F; }
.hosting-aliyun { background: #FE6A00; color: #fff; border: 1px solid #FE6A00; }
.hosting-tencent { background: #4A86FF; color: #fff; border: 1px solid #4A86FF; }
.hosting-aws { background: #232B37; color: #fff; border: 1px solid #232B37; }
.hosting-gcore { background: #FF4C00; color: #fff; border: 1px solid #FF4C00; }
.hosting-fastly { background: #F7272C; color: #fff; border: 1px solid #F7272C; }
.hosting-cachefly { background: #391376; color: #fff; border: 1px solid #391376; }
.hosting-lightcdn { background: #F78B2C; color: #fff; border: 1px solid #F78B2C; }
.hosting-vercel { background: #000; color: #fff; border: 1px solid #000; }
.hosting-netlify { background: #05B7B5; color: #fff; border: 1px solid #05B7B5; }
.hosting-none { background: #f8f9fa; color: #6c757d; border: 1px solid #6c757d; }
.hosting-other { background: #f0f0f0; color: #666; border: 1px solid #666; }

.modal-overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.5); z-index: 100; display: flex; align-items: center; justify-content: center; opacity: 0; pointer-events: none; transition: opacity 0.3s; }
.modal-overlay.open { opacity: 1; pointer-events: auto; }
.modal { background: #fff; width: 90%; max-width: 600px; max-height: 90vh; overflow-y: auto; border-radius: 8px; padding: 25px; box-shadow: 0 5px 15px rgba(0,0,0,0.3); transform: translateY(-20px); transition: transform 0.3s; }
.modal-overlay.open .modal { transform: translateY(0); }
.modal-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 15px; border-bottom: 2px solid #E8EBED; padding-bottom: 10px; }
.modal-title { font-size: 1.25rem; font-weight: bold; color: #3d474d; }
.modal-close { cursor: pointer; font-size: 1.5rem; color: #89949B; line-height: 1; }
.modal-body { text-align: left; }
.edit-field { margin-bottom: 12px; }
.edit-field label { font-size: 0.85rem; color: #89949B; margin-bottom: 4px; display: block; }
.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.grid-3 { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 10px; }

.stat-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; margin-bottom: 20px; }
.stat-box { background: #f0f4f8; padding: 15px; border-radius: 4px; border-left: 4px solid #3b82f6; text-align: center; }
.stat-num { font-size: 1.8rem; color: #1e40af; font-weight: bold; display: block; }
.stat-label { font-size: 0.85rem; color: #4b5563; margin-top: 5px; }
.stat-sub { font-size: 0.75rem; color: #6b7280; margin-top: 3px; }
.chart-container { position: relative; width: 100%; height: 300px; margin: 20px 0; }
.chart-controls { display: flex; gap: 10px; margin-bottom: 15px; align-items: center; }
.chart-controls select { padding: 6px 12px; border: 2px solid #89949B; border-radius: 4px; background: #fff; font-size: 0.9rem; }
.chart-controls button { padding: 6px 12px; background: #e8ebed; border: 2px solid #89949B; border-radius: 4px; color: #5a666d; cursor: pointer; transition: all 0.2s; font-size: 0.9rem; }
.chart-controls button:hover { background: #89949B; color: white; }
.chart-controls button.active { background: #5a666d; color: white; border-color: #5a666d; }
.access-detail-table { width: 100%; font-size: 0.85rem; border: 1px solid #e5e7eb; border-radius: 4px; overflow: hidden; }
.access-detail-table th { background: #f8fafc; font-weight: 600; padding: 8px 10px; }
.access-detail-table td { padding: 8px 10px; border-top: 1px solid #e5e7eb; }
.type-badge { display: inline-block; padding: 2px 6px; border-radius: 10px; font-size: 0.75rem; font-weight: 500; }
.type-subscription { background: #dbeafe; color: #1e40af; }
.type-apigen { background: #d1fae5; color: #065f46; }
.access-log-container { max-height: 200px; overflow-y: auto; border: 1px solid #e5e7eb; border-radius: 4px; padding: 10px; margin-top: 10px; }
.access-log-item { display: flex; justify-content: space-between; padding: 6px 0; border-bottom: 1px solid #f3f4f6; font-size: 0.85rem; }
.access-log-item:last-child { border-bottom: none; }
.timestamp { color: #6b7280; font-family: monospace; }
.log-type { font-weight: 500; }

.hosting-select { padding: 10px 12px; border: 2px solid #89949B; border-radius: 4px; background-color: #fff; font-size: 0.9rem; color: #5a666d; font-family: 'SF Mono', 'Courier New', monospace; cursor: pointer; transition: all 0.2s ease; appearance: none; background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12' fill='none'%3E%3Cpath d='M2.5 4.5L6 8L9.5 4.5' stroke='%235a666d' stroke-width='1.5' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E"); background-repeat: no-repeat; background-position: right 12px center; background-size: 12px; padding-right: 32px; }
.hosting-select:hover { border-color: #5a666d; background-color: #f8f9fa; }
.hosting-select:focus { outline: none; border-color: #3d474d; box-shadow: 0 0 0 3px rgba(93, 105, 109, 0.1); }
.hosting-select:disabled { opacity: 0.6; cursor: not-allowed; background-color: #f0f2f5; }
.hosting-option { padding: 10px 12px; font-size: 0.9rem; font-family: 'SF Mono', 'Courier New', monospace; }
.hosting-option:hover { background-color: #f0f2f5; }
`;

const managePageHtmlContent = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0">
  <link rel="icon" href="https://s3.yangzifun.org/logo.ico" type="image/x-icon">
  <title>代理配置管理器</title>
  <style>${newGlobalStyle}</style>
  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
</head>
<body>
  <div id="toast-container"></div>
  
  <div id="editModalOverlay" class="modal-overlay">
    <div class="modal">
      <div class="modal-header">
        <span class="modal-title">编辑配置 (Editor)</span>
        <span class="modal-close" onclick="closeEditModal()">&times;</span>
      </div>
      <div class="modal-body">
        <input type="hidden" id="edit-id"><input type="hidden" id="edit-protocol">
        <div class="grid-3">
            <div class="edit-field"><label>别名 (Remarks)</label><input type="text" id="edit-ps"></div>
            <div class="edit-field"><label>端口 (Port)</label><input type="number" id="edit-port"></div>
            <div class="edit-field"><label>域名托管 (Hosting)</label>
                <select id="edit-domain-hosting" class="hosting-select">
                    <option value="Cloudflare" class="hosting-option">Cloudflare</option>
                    <option value="阿里ESA" class="hosting-option">阿里ESA</option>
                    <option value="腾讯Edgeone" class="hosting-option">腾讯Edgeone</option>
                    <option value="AWS Cloudfront" class="hosting-option">AWS Cloudfront</option>
                    <option value="Gcore" class="hosting-option">Gcore</option>
                    <option value="Fastly" class="hosting-option">Fastly</option>
                    <option value="CacheFly" class="hosting-option">CacheFly</option>
                    <option value="LightCDN" class="hosting-option">LightCDN</option>
                    <option value="Vercel" class="hosting-option">Vercel</option>
                    <option value="Netlify" class="hosting-option">Netlify</option>
                    <option value="无" class="hosting-option">无</option>
                    <option value="其他" class="hosting-option">其他</option>
                </select>
            </div>
        </div>
        <div class="edit-field"><label>地址 (Address)</label><input type="text" id="edit-add"></div>
        <div class="edit-field"><label>UUID / Password</label><input type="text" id="edit-id-uuid"></div>
        <div class="grid-2">
            <div class="edit-field"><label>传输 (Net)</label><select id="edit-net"><option value="tcp">TCP</option><option value="ws">WebSocket</option><option value="grpc">gRPC</option></select></div>
            <div class="edit-field"><label>伪装 (Type)</label><input type="text" id="edit-type" placeholder="none"></div>
        </div>
        <div class="grid-2">
            <div class="edit-field"><label>伪装域名 (Host)</label><input type="text" id="edit-host"></div>
            <div class="edit-field"><label>路径 (Path)</label><input type="text" id="edit-path"></div>
        </div>
        <div class="grid-2">
             <div class="edit-field"><label>TLS</label><select id="edit-tls"><option value="">关闭</option><option value="tls">开启 TLS</option></select></div>
             <div class="edit-field"><label>SNI</label><input type="text" id="edit-sni"></div>
        </div>
        <div style="margin-top: 20px; text-align: right;">
            <button class="nav-btn" onclick="closeEditModal()">取消</button>
            <button class="nav-btn primary" onclick="saveEditedConfig()">保存修改</button>
        </div>
      </div>
    </div>
  </div>

  <div class="container">
    <div class="content-group">
      <h1 class="profile-name">配置管理器</h1>
      <p class="profile-quote">节点存储与订阅管理</p>
      
      <div class="nav-grid">
         <button class="nav-btn primary" disabled style="opacity:1;cursor:default;">管理面板</button>
         <a href="https://cfst.yangzifun.org" target="_blank" class="nav-btn primary">CF配置生成</a>
      </div>

      <div class="card">
        <h2>1. 检索订阅</h2>
        <div class="form-group" style="display:flex; gap:10px;">
           <input type="text" id="queryUuidInput" placeholder="请输入您的 UUID" style="margin-bottom:0;">
           <button id="queryBtn" class="nav-btn primary" onclick="manageQueryByUuid()">查询</button>
        </div>
        
        <div id="subLinkDisplay" class="form-group hidden" style="display:none; gap:10px; margin-top:10px;">
           <input type="text" id="subUrlLink" readonly style="margin-bottom:0; color:#5a666d; background-color: #f8f9fa;">
           <button class="nav-btn" onclick="copySubLink()" style="white-space: nowrap;">复制</button>
        </div>
      </div>

      <div id="resultCard" class="card hidden">
        <h2>2. 配置列表</h2>
        <div style="text-align:right; margin-bottom:10px;">
           <button class="nav-btn" style="background:#d44; color:#fff; border-color:#d44;" onclick="deleteAll()">删除本组所有</button>
        </div>
        <div id="queryResultsContainer"></div>
      </div>

      <div id="statsCard" class="card hidden">
        <h2>3. 访问统计</h2>
        <div class="chart-controls">
          <select id="statsDays" class="hosting-select" onchange="loadUuidStats()">
            <option value="7" class="hosting-option">最近7天</option>
            <option value="14" class="hosting-option">最近14天</option>
            <option value="30" selected class="hosting-option">最近30天</option>
            <option value="60" class="hosting-option">最近60天</option>
          </select>
          <button class="nav-btn active" onclick="loadUuidStats()">刷新</button>
          <button class="nav-btn" onclick="switchChartType('total')" id="chartTotalBtn">总访问</button>
          <button class="nav-btn" onclick="switchChartType('split')" id="chartSplitBtn">分类统计</button>
        </div>
        
        <div id="statsSummary" class="stat-grid"></div>
        
        <div class="chart-container">
          <canvas id="statsChart"></canvas>
        </div>
        
        <div style="margin-top: 20px;">
          <h3 style="font-size: 1.1rem; margin: 0 0 10px 0;">最近访问记录</h3>
          <div id="recentLogs" class="access-log-container">
            <div style="text-align: center; padding: 20px; color: #6b7280;">加载中...</div>
          </div>
        </div>
      </div>

      <div id="addCard" class="card hidden">
        <h2>4. 添加新节点</h2>
        <div class="form-group">
          <label>域名托管服务 (Domain Hosting)</label>
          <select id="domainHostingSelect" class="hosting-select">
            <option value="Cloudflare" selected>Cloudflare</option>
            <option value="阿里ESA">阿里ESA</option>
            <option value="腾讯Edgeone">腾讯Edgeone</option>
            <option value="AWS Cloudfront">AWS Cloudfront</option>
            <option value="Gcore">Gcore</option>
            <option value="Fastly">Fastly</option>
            <option value="CacheFly">CacheFly</option>
            <option value="LightCDN">LightCDN</option>
            <option value="Vercel">Vercel</option>
            <option value="Netlify">Netlify</option>
            <option value="无">无</option>
            <option value="其他">其他</option>
          </select>
          <div class="info-box">
            <strong>说明：</strong> 选择此配置使用的域名托管服务。
            <br>新增选项：Gcore, Fastly, CacheFly, LightCDN, Vercel, Netlify，"无"表示不使用域名托管。
          </div>
        </div>
        <div class="form-group">
          <label>配置数据 (可批量添加)</label>
          <textarea id="addConfigData" placeholder="支持批量添加：\nvmess://...\nvless://...\ntrojan://..." rows="4"></textarea>
        </div>
        <button onclick="manageAddConfig()" class="nav-btn primary" style="width:100%">添加到当前 UUID</button>
      </div>

      <footer class="footer"><p>Powered by <a href="https://www.yangzihome.space">YZFN</a> | <a href="https://www.yangzihome.space/security.html">安全声明</a></p></footer>
    </div>
  </div>

  <script>
    const WORKER_DOMAIN = "YOUR_WORKER_DOMAIN_PATH";
    // KV 注入点：后端会替换此字符串为真实 UUID
    const AUTO_FILL_UUID = "__SERVER_INJECTED_UUID__"; 

    const toastIcons = { success: '✅', error: '❌', info: 'ℹ️' };
    let currentUuid = '';
    let statsChart = null;
    let currentChartType = 'split';
    
    const hostingStyleMap = {
      'Cloudflare': 'hosting-cloudflare', '阿里ESA': 'hosting-aliyun', '腾讯Edgeone': 'hosting-tencent',
      'AWS Cloudfront': 'hosting-aws', 'Gcore': 'hosting-gcore', 'Fastly': 'hosting-fastly',
      'CacheFly': 'hosting-cachefly', 'LightCDN': 'hosting-lightcdn', 'Vercel': 'hosting-vercel',
      'Netlify': 'hosting-netlify', '无': 'hosting-none', '其他': 'hosting-other'
    };
    
    function showToast(m, t='info') {
       const c = document.getElementById('toast-container');
       const el = document.createElement('div');
       el.className = 'toast';
       el.innerHTML = '<span>' + (toastIcons[t] || '') + ' ' + m + '</span>';
       c.appendChild(el);
       setTimeout(() => { if(el.parentNode) el.parentNode.removeChild(el); }, 5000);
    }

    async function manageQueryByUuid() {
       const inp = document.getElementById('queryUuidInput');
       const val = inp.value.trim();
       if (!val) return showToast('请输入 UUID', 'error');
       
       currentUuid = val;
       document.getElementById('queryBtn').disabled = true;
       document.getElementById('queryBtn').innerText = '查询中...';

       try {
           const res = await fetch(WORKER_DOMAIN + '/manage/configs/' + val);
           if (!res.ok) throw new Error('查询失败 ' + res.status);
           const data = await res.json();
           renderConfigs(data.configs);
           showStatsCard(true);
           showAddCard(true);
           
           // 显示订阅链接
           const subLink = WORKER_DOMAIN + '/sub/' + val;
           const linkBox = document.getElementById('subLinkDisplay');
           const linkInp = document.getElementById('subUrlLink');
           linkInp.value = subLink;
           linkBox.classList.remove('hidden');
           linkBox.style.display = 'flex';
           
           // 加载统计
           loadUuidStats();
       } catch (e) {
           showToast(e.message, 'error');
           document.getElementById('resultCard').classList.add('hidden');
       } finally {
           document.getElementById('queryBtn').disabled = false;
           document.getElementById('queryBtn').innerText = '查询';
       }
    }

    function renderConfigs(list) {
       const c = document.getElementById('queryResultsContainer');
       const card = document.getElementById('resultCard');
       c.innerHTML = '';
       if (!list || list.length === 0) {
           c.innerHTML = '<p style="text-align:center;color:#89949B;">暂无配置数据</p>';
           card.classList.remove('hidden');
           return;
       }
       
       const tableDiv = document.createElement('div');
       tableDiv.className = 'table-container';
       let h = '<table><thead><tr><th>ID</th><th>托管</th><th>备注</th><th>协议</th><th>配置预览</th><th>操作</th></tr></thead><tbody>';
       
       list.forEach(item => {
           const badgeClass = hostingStyleMap[item.domain_hosting] || 'hosting-cloudflare';
           h += '<tr>';
           h += '<td>' + item.id + '</td>';
           h += '<td><span class="hosting-badge ' + badgeClass + '">' + (item.domain_hosting || 'Cloudflare') + '</span></td>';
           h += '<td>' + (item.remark || '-') + '</td>';
           h += '<td>' + item.protocol + '</td>';
           h += '<td class="config-data-cell">' + item.config_data.substring(0, 50) + '...</td>';
           h += '<td class="actions-cell">';
           h += '<button class="nav-btn" onclick="openEditModal(' + JSON.stringify(item).replace(/"/g, '&quot;') + ')">编辑</button>';
           h += '<button class="nav-btn" style="color:#d44;border-color:#d44;" onclick="deleteOne(' + item.id + ')">删除</button>';
           h += '</td></tr>';
       });
       h += '</tbody></table>';
       tableDiv.innerHTML = h;
       c.appendChild(tableDiv);
       card.classList.remove('hidden');
    }

    async function manageAddConfig() {
       const txt = document.getElementById('addConfigData').value.trim();
       const hosting = document.getElementById('domainHostingSelect').value;
       if (!currentUuid) return showToast('请先查询 UUID', 'error');
       if (!txt) return showToast('请输入配置数据', 'error');
       
       try {
           const res = await fetch(WORKER_DOMAIN + '/manage/configs', {
               method: 'POST',
               headers: { 'Content-Type': 'application/json' },
               body: JSON.stringify({ uuid: currentUuid, config_data: txt, domain_hosting: hosting })
           });
           const d = await res.json();
           if (d.error) throw new Error(d.error);
           showToast('添加成功', 'success');
           document.getElementById('addConfigData').value = '';
           manageQueryByUuid();
       } catch (e) { showToast(e.message, 'error'); }
    }

    async function saveEditedConfig() {
        const id = document.getElementById('edit-id').value;
        const proto = document.getElementById('edit-protocol').value;
        const ps = document.getElementById('edit-ps').value;
        const add = document.getElementById('edit-add').value;
        const port = document.getElementById('edit-port').value;
        const uuid = document.getElementById('edit-id-uuid').value;
        const net = document.getElementById('edit-net').value;
        const type = document.getElementById('edit-type').value;
        const host = document.getElementById('edit-host').value;
        const path = document.getElementById('edit-path').value;
        const tls = document.getElementById('edit-tls').value;
        const sni = document.getElementById('edit-sni').value;
        const hosting = document.getElementById('edit-domain-hosting').value;

        let newConfig = '';
        if (proto === 'vmess') {
            const vmessObj = {
                v: "2", ps: ps, add: add, port: port, id: uuid, aid: "0", scy: "auto",
                net: net, type: type || "none", host: host, path: path, tls: tls, sni: sni
            };
            newConfig = 'vmess://' + btoa(JSON.stringify(vmessObj));
        } else {
            showToast('仅演示 VMess 编辑拼装，其他协议建议删除后重新添加', 'info');
            closeEditModal();
            return; 
        }

        try {
           const res = await fetch(WORKER_DOMAIN + '/manage/configs', {
               method: 'PUT',
               headers: {'Content-Type': 'application/json'},
               body: JSON.stringify({ id: id, config_data: newConfig, domain_hosting: hosting })
           });
           const d = await res.json();
           if(d.error) throw new Error(d.error);
           showToast('修改成功', 'success');
           closeEditModal();
           manageQueryByUuid();
        } catch(e) { showToast(e.message, 'error'); }
    }

    async function deleteOne(id) {
       if(!confirm('确定删除此配置?')) return;
       try {
           const res = await fetch(WORKER_DOMAIN + '/manage/configs/id/' + id, { method: 'DELETE' });
           if(res.ok) { showToast('已删除', 'success'); manageQueryByUuid(); }
           else showToast('删除失败', 'error');
       } catch(e) { showToast(e.message, 'error'); }
    }

    async function deleteAll() {
       if(!currentUuid) return;
       if(!confirm('确定清空该 UUID 下所有配置? 此操作不可恢复!')) return;
       try {
           const res = await fetch(WORKER_DOMAIN + '/manage/configs/' + currentUuid, { method: 'DELETE' });
           if(res.ok) { showToast('已清空', 'success'); manageQueryByUuid(); }
           else showToast('清空失败', 'error');
       } catch(e) { showToast(e.message, 'error'); }
    }

    function copySubLink() {
       const el = document.getElementById('subUrlLink');
       el.select();
       document.execCommand('copy');
       showToast('订阅链接已复制', 'success');
    }

    function openEditModal(item) {
        document.getElementById('edit-id').value = item.id;
        document.getElementById('edit-protocol').value = item.protocol;
        document.getElementById('edit-domain-hosting').value = item.domain_hosting || 'Cloudflare';
        
        if (item.protocol === 'vmess') {
            try {
                const v = JSON.parse(atob(item.config_data.substring(8)));
                document.getElementById('edit-ps').value = v.ps || '';
                document.getElementById('edit-add').value = v.add || '';
                document.getElementById('edit-port').value = v.port || '';
                document.getElementById('edit-id-uuid').value = v.id || '';
                document.getElementById('edit-net').value = v.net || 'tcp';
                document.getElementById('edit-type').value = v.type || '';
                document.getElementById('edit-host').value = v.host || '';
                document.getElementById('edit-path').value = v.path || '';
                document.getElementById('edit-tls').value = v.tls || '';
                document.getElementById('edit-sni').value = v.sni || v.host || '';
            } catch(e) {}
        } else {
             document.getElementById('edit-ps').value = item.remark || '';
        }
        
        document.getElementById('editModalOverlay').classList.add('open');
    }

    function closeEditModal() {
        document.getElementById('editModalOverlay').classList.remove('open');
    }

    function showStatsCard(show) {
        const el = document.getElementById('statsCard');
        if(show) el.classList.remove('hidden');
        else el.classList.add('hidden');
    }

    function showAddCard(show) {
        const el = document.getElementById('addCard');
        if(show) el.classList.remove('hidden');
        else el.classList.add('hidden');
    }

    async function loadUuidStats() {
        if(!currentUuid) return;
        const days = document.getElementById('statsDays').value;
        try {
            const res = await fetch(WORKER_DOMAIN + '/manage/stats/' + currentUuid + '?days=' + days);
            const data = await res.json();
            if(data.success) {
                renderStats(data);
            }
        } catch(e) { console.error(e); }
    }
    
    function renderStats(data) {
        const summary = document.getElementById('statsSummary');
        summary.innerHTML = \`
            <div class="stat-box"><span class="stat-num">\${data.total_access}</span><span class="stat-label">总访问量</span></div>
            <div class="stat-box"><span class="stat-num">\${data.today_total}</span><span class="stat-label">今日访问</span></div>
            <div class="stat-box"><span class="stat-num">\${data.subscription_count}</span><span class="stat-label">订阅获取</span></div>
        \`;
        
        const logs = document.getElementById('recentLogs');
        if(data.recent_logs && data.recent_logs.length > 0) {
            logs.innerHTML = data.recent_logs.map(l => \`
                <div class="access-log-item">
                    <span class="timestamp">\${l.created_at.replace('T', ' ').split('.')[0]}</span>
                    <span class="type-badge \${l.query_type === 'subscription' ? 'type-subscription' : 'type-apigen'}">\${l.query_type}</span>
                    <span>\${l.client_ip}</span>
                </div>
            \`).join('');
        } else {
            logs.innerHTML = '<div style="text-align:center;padding:10px;">暂无记录</div>';
        }

        const ctx = document.getElementById('statsChart').getContext('2d');
        if(statsChart) statsChart.destroy();
        
        const labels = data.daily_stats.map(d => d.date);
        const totalData = data.daily_stats.map(d => d.total);
        const subData = data.daily_stats.map(d => d.subscription);
        const apiData = data.daily_stats.map(d => d.api_generation);

        let datasets = [];
        if (currentChartType === 'total') {
            datasets.push({ label: '总访问量', data: totalData, borderColor: '#3b82f6', tension: 0.1 });
        } else {
            datasets.push({ label: '订阅获取', data: subData, borderColor: '#1e40af', tension: 0.1 });
            datasets.push({ label: 'API管理', data: apiData, borderColor: '#065f46', tension: 0.1 });
        }
        
        statsChart = new Chart(ctx, {
            type: 'line',
            data: { labels: labels, datasets: datasets },
            options: { responsive: true, maintainAspectRatio: false }
        });
    }

    function switchChartType(type) {
        currentChartType = type;
        document.getElementById('chartTotalBtn').className = type === 'total' ? 'nav-btn active' : 'nav-btn';
        document.getElementById('chartSplitBtn').className = type === 'split' ? 'nav-btn active' : 'nav-btn';
        loadUuidStats();
    }

    // 自动填充与查询逻辑
    window.addEventListener('DOMContentLoaded', () => {
        if (AUTO_FILL_UUID && AUTO_FILL_UUID !== "null" && AUTO_FILL_UUID.length > 5 && AUTO_FILL_UUID !== "__" + "SERVER_INJECTED_UUID" + "__") {
            document.getElementById('queryUuidInput').value = AUTO_FILL_UUID;
            manageQueryByUuid();
            showToast('已自动恢复上次会话', 'success');
        }
    });

  </script>
</body>
</html>
`;