/* =================================================================
 *  Cloudflare Worker: Configuration Management with MFA & Templates
 *  功能：
 *  1. 用户认证管理（JWT + MFA）
 *  2. 配置生成和管理
 *  3. 模板管理
 *  4. 访问统计分析
 *  5. 数据库操作
 * ================================================================= */

// =================================================================
//  CONSTANTS (常量定义)
// =================================================================
const DEFAULT_JWT_SECRET = 'your-default-jwt-secret-change-this';
const CACHE_CONTROL = 'no-cache, no-store, must-revalidate';

// =================================================================
//  UTILITY FUNCTIONS (工具函数)
// =================================================================

/**
 * 使用SHA-256算法哈希密码
 */
async function hashPassword(password) {
    const msgBuffer = new TextEncoder().encode(password);
    const hashBuffer = await crypto.subtle.digest('SHA-256', msgBuffer);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
}

/**
 * 签名JWT令牌
 */
async function signJwt(payload, secret = DEFAULT_JWT_SECRET) {
    const header = { alg: 'HS256', typ: 'JWT' };
    const encodedHeader = btoa(JSON.stringify(header)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
    const encodedPayload = btoa(JSON.stringify(payload)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
    const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
    const signature = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(`${encodedHeader}.${encodedPayload}`));
    const encodedSignature = btoa(String.fromCharCode(...new Uint8Array(signature))).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
    return `${encodedHeader}.${encodedPayload}.${encodedSignature}`;
}

/**
 * 验证并解析JWT令牌
 */
async function verifyJwt(token, secret = DEFAULT_JWT_SECRET) {
    try {
        const [h, p, s] = token.split('.');
        if (!h || !p || !s) return false;
        const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['verify']);
        const signature = Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
        const valid = await crypto.subtle.verify('HMAC', key, signature, new TextEncoder().encode(`${h}.${p}`));
        if (!valid) return false;
        const payload = JSON.parse(atob(p.replace(/-/g, '+').replace(/_/g, '/')));
        if (payload.exp < Date.now()) return false;
        return payload;
    } catch (e) { return false; }
}

/**
 * 生成JSON响应
 */
function jsonResponse(data, status = 200, cacheControl = CACHE_CONTROL) {
    return new Response(JSON.stringify(data, null, 2), {
        status,
        headers: {
            'Content-Type': 'application/json;charset=UTF-8',
            'Access-Control-Allow-Origin': '*',
            'Cache-Control': cacheControl,
            'X-Response-Time': Date.now().toString()
        }
    });
}

/**
 * 防抖函数
 */
function debounce(func, wait) {
    let timeout;
    return function executedFunction(...args) {
        const later = () => {
            clearTimeout(timeout);
            func(...args);
        };
        clearTimeout(timeout);
        timeout = setTimeout(later, wait);
    };
}

/**
 * 格式化时间戳为本地日期格式
 */
function formatDate(timestamp) {
    if (!timestamp || timestamp === 0) return '从未';
    const date = new Date(timestamp);
    return date.toLocaleString('zh-CN', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit'
    });
}

/**
 * 获取访问统计摘要
 */
async function getAccessStatsSummary(env, days = 30) {
    try {
        const db = env.DB;
        if (!db) throw new Error("D1数据库未绑定。");

        // 获取总访问统计
        const totalStats = await db.prepare(
            'SELECT COUNT(*) as total, COUNT(DISTINCT uuid) as unique_uuids, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription_total, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as apigen_total ' +
            'FROM config_access_logs'
        ).first();

        // 获取今日访问统计
        const today = new Date().toISOString().split('T')[0];
        const todayStats = await db.prepare(
            'SELECT COUNT(*) as today_total, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as today_subscription, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as today_apigen ' +
            'FROM config_access_logs WHERE DATE(created_at) = ?'
        ).bind(today).first();

        // 获取按日统计（近30天）
        const startDate = new Date();
        startDate.setDate(startDate.getDate() - days);
        const startDateStr = startDate.toISOString().split('T')[0];

        const dailyStats = await db.prepare(
            'SELECT DATE(created_at) as date, ' +
            'COUNT(*) as total, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as api_generation, ' +
            'COUNT(DISTINCT uuid) as unique_uuids ' +
            'FROM config_access_logs ' +
            'WHERE DATE(created_at) >= ? ' +
            'GROUP BY DATE(created_at) ' +
            'ORDER BY date DESC'
        ).bind(startDateStr).all();

        // 获取热门UUID排名
        const popularUUIDs = await db.prepare(
            'SELECT uuid, COUNT(*) as access_count, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription_count, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as apigen_count ' +
            'FROM config_access_logs ' +
            'GROUP BY uuid ' +
            'ORDER BY access_count DESC ' +
            'LIMIT 10'
        ).all();

        return {
            success: true,
            total_requests: totalStats?.total || 0,
            unique_uuids: totalStats?.unique_uuids || 0,
            subscription_requests: totalStats?.subscription_total || 0,
            api_generation_requests: totalStats?.apigen_total || 0,
            today_total: todayStats?.today_total || 0,
            today_subscription: todayStats?.today_subscription || 0,
            today_apigen: todayStats?.today_apigen || 0,
            daily_stats: dailyStats?.results || [],
            popular_uuids: popularUUIDs?.results || []
        };
    } catch (e) {
        console.error("获取访问统计失败:", e.message);
        return { success: false, error: e.message };
    }
}

/**
 * 获取UUID的访问详情
 */
async function getUUIDAccessDetails(env, uuid, startDate, endDate) {
    try {
        const db = env.DB;
        if (!db) throw new Error("D1数据库未绑定。");

        let query = 'SELECT uuid, query_type, client_ip, user_agent, created_at FROM config_access_logs WHERE uuid = ?';
        const params = [uuid];

        if (startDate) {
            query += ' AND DATE(created_at) >= ?';
            params.push(startDate);
        }

        if (endDate) {
            query += ' AND DATE(created_at) <= ?';
            params.push(endDate);
        }

        query += ' ORDER BY created_at DESC LIMIT 100';

        const { results } = await db.prepare(query).bind(...params).all();

        // 获取该UUID的基本信息
        const uuidStats = await db.prepare(
            'SELECT COUNT(*) as total_access, ' +
            'MIN(created_at) as first_access, ' +
            'MAX(created_at) as last_access ' +
            'FROM config_access_logs WHERE uuid = ?'
        ).bind(uuid).first();

        return {
            success: true,
            uuid: uuid,
            total_access: uuidStats?.total_access || 0,
            first_access: uuidStats?.first_access,
            last_access: uuidStats?.last_access,
            access_logs: results || []
        };
    } catch (e) {
        console.error(`获取UUID ${uuid} 访问详情失败:`, e.message);
        return { success: false, error: e.message };
    }
}

// =================================================================
//  TOTP AUTHENTICATION CLASS
// =================================================================

class TOTP {
    /**
     * 生成随机密钥
     */
    static generateSecret() {
        const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
        let secret = '';
        for (let i = 0; i < 20; i++) {
            secret += chars.charAt(Math.floor(Math.random() * chars.length));
        }
        return secret;
    }

    /**
     * 生成OTP验证码
     */
    static generateOTP(secret, window = 0) {
        const time = Math.floor(Date.now() / 30000) + window;
        return this.generateOTPWithTime(secret, time);
    }

    /**
     * 使用指定时间生成OTP
     */
    static generateOTPWithTime(secret, time) {
        const key = this.base32ToBytes(secret);
        const timeBuffer = new ArrayBuffer(8);
        const timeView = new DataView(timeBuffer);
        timeView.setUint32(0, Math.floor(time / 0x100000000));
        timeView.setUint32(4, time % 0x100000000);

        const keyObj = crypto.subtle.importKey('raw', new Uint8Array(key),
            { name: 'HMAC', hash: 'SHA-1' }, false, ['sign']);

        return keyObj.then(k => {
            return crypto.subtle.sign('HMAC', k, timeBuffer);
        }).then(signature => {
            const sigArray = new Uint8Array(signature);
            const offset = sigArray[sigArray.length - 1] & 0xf;

            const binary = ((sigArray[offset] & 0x7f) << 24) |
                ((sigArray[offset + 1] & 0xff) << 16) |
                ((sigArray[offset + 2] & 0xff) << 8) |
                (sigArray[offset + 3] & 0xff);

            const otp = binary % 1000000;
            return otp.toString().padStart(6, '0');
        });
    }

    /**
     * Base32转换
     */
    static base32ToBytes(base32) {
        const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
        const bytes = [];
        let bits = 0;
        let buffer = 0;

        for (let i = 0; i < base32.length; i++) {
            const char = base32.charAt(i);
            const value = chars.indexOf(char);
            if (value === -1) continue;

            buffer = (buffer << 5) | value;
            bits += 5;

            while (bits >= 8) {
                bits -= 8;
                bytes.push((buffer >> bits) & 0xff);
                buffer &= (1 << bits) - 1;
            }
        }

        return bytes;
    }

    /**
     * 验证OTP码
     */
    static async verify(secret, code, window = 1) {
        for (let i = -window; i <= window; i++) {
            const genCode = await this.generateOTP(secret, i);
            if (genCode === code) return true;
        }
        return false;
    }

    /**
     * 生成二维码URL
     */
    static generateOTPAuthURL(username, secret, issuer = 'YZFN Admin') {
        const encodedIssuer = encodeURIComponent(issuer);
        const encodedUsername = encodeURIComponent(username);
        return `otpauth://totp/${encodedIssuer}:${encodedUsername}?secret=${secret}&issuer=${encodedIssuer}&algorithm=SHA1&digits=6&period=30`;
    }
}

// =================================================================
//  IP & ADDRESS UTILITIES
// =================================================================

/**
 * 从HostMonit获取优化IPs
 */
async function fetchIpsFromHostMonit(type = 'v4') {
    try {
        const requestBody = { key: "iDetkOys" };
        if (type === 'v6') requestBody.type = 'v6';

        const res = await fetch('https://api.hostmonit.com/get_optimization_ip', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(requestBody)
        });

        if (!res.ok) return [];
        const data = await res.json();

        if (data.code === 200 && Array.isArray(data.info)) {
            return data.info.map(i => {
                let carrier = i.line || 'ALL';
                if (carrier === 'CMI') carrier = 'CM';
                if (carrier === 'CT') carrier = 'CT';
                if (carrier === 'CU') carrier = 'CU';
                return {
                    ip: i.ip,
                    ip_type: i.ip.includes(':') ? 'v6' : 'v4',
                    carrier: carrier,
                    source: `hostmonit_${type}`
                };
            });
        }
        return [];
    } catch (e) {
        console.error(`fetchIpsFromHostMonit error:`, e.message);
        return [];
    }
}

/**
 * 从VPS789获取Cloudflare IPs
 */
async function fetchIpsFromVps789() {
    try {
        const res = await fetch('https://vps789.com/openApi/cfIpApi', {
            headers: { 'User-Agent': 'CF-Worker/4.0', 'Accept': 'application/json' }
        });

        if (!res.ok) return [];
        const data = await res.json();
        const ips = [];

        if (data.code === 0 && data.data) {
            for (const k in data.data) {
                const arr = data.data[k];
                if (Array.isArray(arr)) {
                    let carrier = 'ALL';
                    if (k.includes('移动') || k.includes('CM') || k === 'CM') carrier = 'CM';
                    else if (k.includes('电信') || k.includes('CT') || k === 'CT') carrier = 'CT';
                    else if (k.includes('联通') || k.includes('CU') || k === 'CU') carrier = 'CU';

                    arr.forEach(i => {
                        if (i && i.ip) {
                            ips.push({
                                ip: i.ip,
                                ip_type: i.ip.includes(':') ? 'v6' : 'v4',
                                carrier: carrier,
                                source: 'vps789'
                            });
                        }
                    });
                }
            }
        }
        return ips;
    } catch (e) {
        console.error('fetchIpsFromVps789 error:', e.message);
        return [];
    }
}

/**
 * 检查并自动升级数据库表结构
 */
async function ensureSchema(env) {
    try {
        // 1. 确保表存在
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS cfips (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ip TEXT NOT NULL,
                ip_type TEXT,
                carrier TEXT,
                source TEXT,
                created_at INTEGER,
                updated_at INTEGER
            )
        `).run();

        // 2. 检查旧表是否缺少 updated_at 列 (Migration)
        const info = await env.DB.prepare('PRAGMA table_info(cfips)').all();
        const columns = (info.results || []).map(c => c.name);

        if (!columns.includes('updated_at')) {
            console.log('检测到旧表结构，正在添加 updated_at 列...');
            await env.DB.prepare('ALTER TABLE cfips ADD COLUMN updated_at INTEGER').run();
        }

        // 3. 确保设置表存在
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS auto_update_settings (
                source TEXT PRIMARY KEY,
                enabled INTEGER,
                updated_at INTEGER
            )
        `).run();

    } catch (e) {
        console.error('Schema check/update failed:', e);
    }
}

async function executeInChunks(stmts) {
    const CHUNK_SIZE = 10;
    for (let i = 0; i < stmts.length; i += CHUNK_SIZE) {
        const chunk = stmts.slice(i, i + CHUNK_SIZE);
        await Promise.all(chunk.map(stmt => stmt.run()));
    }
}

async function runIpUpdateTask(env, sources = null) {
    console.log('开始IP全量更新任务...');

    // --- 0. 确保数据库结构正确 ---
    await ensureSchema(env);

    // --- 1. 获取配置 ---
    if (sources === null) {
        try {
            const settingsRes = await env.DB.prepare(
                'SELECT source, enabled FROM auto_update_settings WHERE source IN (?, ?, ?, ?)'
            ).bind('hostmonit_v4', 'hostmonit_v6', 'vps789', 'global_enabled').all();

            if (settingsRes && settingsRes.results) {
                sources = {};
                settingsRes.results.forEach(setting => { sources[setting.source] = setting.enabled === 1; });
            } else {
                sources = { hostmonit_v4: true, hostmonit_v6: false, vps789: true, global_enabled: true };
            }
        } catch (e) {
            sources = { hostmonit_v4: true, hostmonit_v6: false, vps789: true, global_enabled: true };
        }
    }

    // --- 2. 获取数据 ---
    const tasks = [];
    if (sources.hostmonit_v4 !== false) tasks.push(fetchIpsFromHostMonit('v4'));
    if (sources.hostmonit_v6 !== false) tasks.push(fetchIpsFromHostMonit('v6'));
    if (sources.vps789 !== false) tasks.push(fetchIpsFromVps789());

    if (tasks.length === 0) return { success: false, message: "没有启用的API源" };

    const results = await Promise.allSettled(tasks);
    const allIps = [];
    const sourceStats = {};

    results.forEach((result, index) => {
        const sourceNames = ['hostmonit_v4', 'hostmonit_v6', 'vps789'];
        const sourceName = index < sourceNames.length ? sourceNames[index] : `Source_${index}`;

        if (result.status === 'fulfilled') {
            const ips = result.value;
            allIps.push(...ips);
            sourceStats[sourceName] = { count: ips.length, status: 'ok' };
        } else {
            sourceStats[sourceName] = { count: 0, status: 'error' };
        }
    });

    console.log(`API共获取到 ${allIps.length} 个原始IP`);

    // --- 3. 去重 ---
    const uniqueMap = new Map();
    allIps.forEach(i => {
        if (i && i.ip) {
            const normalizedIp = i.ip.trim();
            const carrier = i.carrier || 'ALL';
            const key = `${normalizedIp}_${carrier}`;

            if (!uniqueMap.has(key)) {
                uniqueMap.set(key, { ...i, ip: normalizedIp, carrier: carrier });
            } else {
                const existing = uniqueMap.get(key);
                if (existing.source && i.source && !existing.source.includes(i.source)) {
                    existing.source = `${existing.source}|${i.source}`;
                }
            }
        }
    });

    const newIpList = Array.from(uniqueMap.values());
    console.log(`去重后得到 ${newIpList.length} 个有效记录`);

    if (sources.global_enabled === 0) {
        return { success: true, count: newIpList.length, message: "自动更新已关闭" };
    }

    try {
        // --- 4. 清理原数据库中的所有IP ---
        console.log('清理原数据库中所有IP记录...');
        await env.DB.prepare('DELETE FROM cfips').run();

        // --- 5. 插入新IP数据 ---
        let insertedCount = 0;
        if (newIpList.length > 0) {
            console.log(`插入 ${newIpList.length} 个新IP记录...`);

            const insertStmts = newIpList.map(i =>
                env.DB.prepare('INSERT INTO cfips (ip, ip_type, carrier, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)')
                    .bind(i.ip, i.ip_type || 'v4', i.carrier, i.source, Date.now(), Date.now())
            );

            await executeInChunks(insertStmts);
            insertedCount = newIpList.length;
        }

        // --- 6. 更新最后执行时间 ---
        await env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
            .bind('last_executed', 1, Date.now()).run();

        return {
            success: true,
            message: `更新成功 (清理了所有旧IP，插入了${insertedCount}个新IP)`,
            sourceStats,
            stats: {
                cleaned: true,
                inserted: insertedCount
            }
        };

    } catch (e) {
        console.error('Database Sync Error:', e.message);
        return { success: false, message: "DB Error: " + e.message };
    }
}

async function initializeDatabaseSettings(env) {
    console.log('mg_worker.js: checking database tables...');
    try {
        // 创建 admin_users 表
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS admin_users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                password_hash TEXT NOT NULL,
                mfa_enabled INTEGER DEFAULT 0,
                mfa_secret TEXT,
                last_mfa_login INTEGER DEFAULT 0,
                last_backup_login INTEGER DEFAULT 0,
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            );
        `).run();

        // 创建 mfa_backup_codes 表
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS mfa_backup_codes (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT NOT NULL,
                code TEXT UNIQUE NOT NULL,
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
                used INTEGER DEFAULT 0,
                used_at INTEGER DEFAULT 0
            );
        `).run();

        // 创建 cf_domains 表 (Cloudflare域名)
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS cf_domains (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                domain TEXT UNIQUE NOT NULL,
                remark TEXT,
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
                updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            );
        `).run();

        // 创建 edgeone_domains 表 (腾讯云EdgeOne域名)
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS edgeone_domains (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                domain TEXT UNIQUE NOT NULL,
                remark TEXT,
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
                updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            );
        `).run();

        // 创建 configs 表
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS configs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                uuid TEXT NOT NULL,
                ip TEXT,
                domain TEXT,
                port INTEGER,
                meta TEXT,
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
                updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            );
        `).run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_uuid ON configs (uuid);').run();

        // 创建 cfips 表
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS cfips (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ip TEXT UNIQUE NOT NULL,
                ip_type TEXT,
                carrier TEXT,
                source TEXT,
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            );
        `).run();

        // 创建 auto_update_settings 表
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS auto_update_settings (
                source TEXT PRIMARY KEY,
                enabled INTEGER NOT NULL,
                updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            );
        `).run();

        // 创建 config_access_logs 表
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS config_access_logs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                uuid TEXT NOT NULL,
                query_type TEXT NOT NULL,
                client_ip TEXT,
                user_agent TEXT,
                created_at TEXT DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%SZ', 'now'))
            );
        `).run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_access_logs_uuid ON config_access_logs (uuid);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_access_logs_date ON config_access_logs (created_at);').run();

        // 创建 config_templates 表（新增）
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS config_templates (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT UNIQUE NOT NULL,
                url TEXT NOT NULL,
                description TEXT,
                is_default INTEGER DEFAULT 0,
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
                updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            )
        `).run();

        // 创建模板表索引
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_config_templates_name ON config_templates(name);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_config_templates_is_default ON config_templates(is_default);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_config_templates_created ON config_templates(created_at);').run();

        // 创建 uuid_info 表（新增）
        await env.DB.prepare(`
            CREATE TABLE IF NOT EXISTS uuid_info (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                uuid TEXT UNIQUE NOT NULL,            -- UUID (唯一)
                owner TEXT NOT NULL,                  -- 所属人姓名/标识
                email TEXT NOT NULL,                  -- 邮箱地址
                contact_info TEXT,                    -- 其他联系信息 (可选)
                status TEXT DEFAULT 'active',         -- 状态: active(活跃), inactive(停用), suspended(暂停)
                remark TEXT,                          -- 备注信息
                last_accessed INTEGER DEFAULT 0,      -- 最后访问时间戳
                access_count INTEGER DEFAULT 0,       -- 访问次数统计
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
                updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            )
        `).run();

        // UUID信息管理表索引
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_uuid_info_uuid ON uuid_info(uuid);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_uuid_info_owner ON uuid_info(owner);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_uuid_info_email ON uuid_info(email);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_uuid_info_status ON uuid_info(status);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_uuid_info_created ON uuid_info(created_at);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_uuid_info_last_accessed ON uuid_info(last_accessed);').run();

        console.log('mg_worker.js: D1 database tables checked/initialized.');

        // 初始化默认设置
        const defaultSettings = [
            { source: 'global_enabled', enabled: 1 },
            { source: 'hostmonit_v4', enabled: 1 },
            { source: 'hostmonit_v6', enabled: 0 },
            { source: 'vps789', enabled: 1 },
            { source: 'last_executed', enabled: 0 }
        ];

        for (const setting of defaultSettings) {
            await env.DB.prepare('INSERT OR IGNORE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind(setting.source, setting.enabled, Date.now())
                .run();
        }
    } catch (e) {
        console.error('mg_worker.js: Failed to check/initialize database tables:', e.message);
    }
}

// =================================================================
//  API HANDLERS (API处理函数)
// =================================================================

/**
 * 处理用户登录请求（支持MFA）
 */
async function handleLogin(req, env) {
    try {
        const { username, password, totp_code, step } = await req.json();

        const user = await env.DB.prepare(
            'SELECT * FROM admin_users WHERE username = ?'
        ).bind(username).first();

        if (!user || (await hashPassword(password)) !== user.password_hash) {
            return jsonResponse({ error: '用户名或密码错误' }, 401);
        }

        const requiresMFA = user.mfa_enabled === 1;

        if (!step) {
            if (!requiresMFA) {
                const token = await signJwt(
                    {
                        sub: user.username,
                        exp: Date.now() + 86400000,
                        requires_mfa: false
                    },
                    env.JWT_SECRET || DEFAULT_JWT_SECRET
                );
                return jsonResponse({ token, requires_mfa: false });
            } else {
                return jsonResponse({
                    requires_mfa: true,
                    username: user.username,
                    step: 'totp_required'
                });
            }
        }

        if (step === 'verify_totp' && requiresMFA) {
            if (!totp_code) {
                return jsonResponse({ error: '请输入验证码' }, 400);
            }

            if (!user.mfa_secret) {
                return jsonResponse({ error: 'MFA未设置' }, 400);
            }

            const isValid = await TOTP.verify(user.mfa_secret, totp_code, 1);
            if (!isValid) {
                return jsonResponse({ error: '验证码无效或已过期' }, 401);
            }

            await env.DB.prepare(
                'UPDATE admin_users SET last_mfa_login = ? WHERE username = ?'
            ).bind(Date.now(), user.username).run();

            const token = await signJwt(
                {
                    sub: user.username,
                    exp: Date.now() + 86400000,
                    requires_mfa: true,
                    mfa_verified: true
                },
                env.JWT_SECRET || DEFAULT_JWT_SECRET
            );

            return jsonResponse({
                token,
                requires_mfa: true,
                message: 'MFA验证成功'
            });
        }

        return jsonResponse({ error: '无效的登录步骤' }, 400);
    } catch (e) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 初始化MFA设置
 */
async function handleMfaInit(req, env) {
    try {
        const { username, password } = await req.json();

        const user = await env.DB.prepare(
            'SELECT * FROM admin_users WHERE username = ?'
        ).bind(username).first();

        if (!user || (await hashPassword(password)) !== user.password_hash) {
            return jsonResponse({ error: '用户名或密码错误' }, 401);
        }

        if (user.mfa_enabled === 1) {
            return jsonResponse({ error: 'MFA已启用' }, 400);
        }

        const newSecret = TOTP.generateSecret();
        const otpauth_url = TOTP.generateOTPAuthURL(username, newSecret);

        return jsonResponse({
            success: true,
            secret: newSecret,
            otpauth_url: otpauth_url,
            otpauth_url_encoded: encodeURIComponent(otpauth_url),
            qr_url: `https://qrcode.api.yangzifun.org/?data=${encodeURIComponent(otpauth_url)}&size=200`,
            message: '请使用身份验证器App手动输入密钥'
        });
    } catch (error) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 首次验证MFA代码
 */
async function handleMfaVerifyFirst(req, env) {
    try {
        const { username, totp_code, secret } = await req.json();

        if (!totp_code || !secret) {
            return jsonResponse({ error: '请提供验证码和密钥' }, 400);
        }

        const isValid = await TOTP.verify(secret, totp_code, 1);
        if (!isValid) {
            return jsonResponse({ error: '验证码无效或已过期' }, 400);
        }

        const backupCodes = Array.from({ length: 10 }, () =>
            Math.random().toString(36).substring(2, 10).toUpperCase()
        );

        await env.DB.prepare(
            'UPDATE admin_users SET mfa_enabled = 1, mfa_secret = ? WHERE username = ?'
        ).bind(secret, username).run();

        const stmts = backupCodes.map(code =>
            env.DB.prepare(
                'INSERT INTO mfa_backup_codes (username, code, created_at, used) VALUES (?, ?, ?, 0)'
            ).bind(username, code, Date.now())
        );

        if (stmts.length > 0) {
            await env.DB.batch(stmts);
        }

        return jsonResponse({
            success: true,
            message: 'MFA已成功启用',
            backup_codes: backupCodes,
            important: '请妥善保管备份码'
        });
    } catch (error) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 使用备份码进行MFA登录
 */
async function handleMfaLoginWithBackup(req, env) {
    try {
        const { username, password, backup_code } = await req.json();

        const user = await env.DB.prepare(
            'SELECT * FROM admin_users WHERE username = ? AND mfa_enabled = 1'
        ).bind(username).first();

        if (!user || (await hashPassword(password)) !== user.password_hash) {
            return jsonResponse({ error: '用户名或密码错误' }, 401);
        }

        const backupRecord = await env.DB.prepare(
            'SELECT * FROM mfa_backup_codes WHERE username = ? AND code = ? AND used = 0'
        ).bind(username, backup_code.trim().toUpperCase()).first();

        if (!backupRecord) {
            return jsonResponse({ error: '备份码无效或已使用' }, 401);
        }

        await env.DB.prepare(
            'UPDATE mfa_backup_codes SET used = 1, used_at = ? WHERE id = ?'
        ).bind(Date.now(), backupRecord.id).run();

        await env.DB.prepare(
            'UPDATE admin_users SET last_backup_login = ? WHERE username = ?'
        ).bind(Date.now(), username).run();

        const token = await signJwt(
            {
                sub: user.username,
                exp: Date.now() + 86400000,
                requires_mfa: true,
                mfa_verified: true,
                via_backup_code: true
            },
            env.JWT_SECRET || DEFAULT_JWT_SECRET
        );

        const remaining = await env.DB.prepare(
            'SELECT COUNT(*) as count FROM mfa_backup_codes WHERE username = ? AND used = 0'
        ).bind(username).first('count') || 0;

        return jsonResponse({
            token,
            message: '使用备份码登录成功',
            backup_codes_remaining: remaining
        });
    } catch (error) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 处理更改密码请求
 */
async function handleChangePassword(req, env, currentUser) {
    try {
        const { oldPassword, newPassword } = await req.json();

        const user = await env.DB.prepare(
            'SELECT * FROM admin_users WHERE username = ?'
        ).bind(currentUser).first();

        if (!user || (await hashPassword(oldPassword)) !== user.password_hash) {
            return jsonResponse({ error: '旧密码错误' }, 403);
        }

        await env.DB.prepare(
            'UPDATE admin_users SET password_hash = ? WHERE username = ?'
        ).bind(await hashPassword(newPassword), currentUser).run();

        return jsonResponse({
            success: true,
            message: '密码修改成功'
        });
    } catch (error) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

// 获取MFA状态
async function handleMfaStatus(req, env, currentUser) {
    try {
        const user = await env.DB.prepare(
            'SELECT mfa_enabled, last_mfa_login, last_backup_login FROM admin_users WHERE username = ?'
        ).bind(currentUser).first();

        const backupCodesCount = await env.DB.prepare(
            'SELECT COUNT(*) as count FROM mfa_backup_codes WHERE username = ? AND used = 0'
        ).bind(currentUser).first('count') || 0;

        return jsonResponse({
            mfa_enabled: user.mfa_enabled === 1,
            last_mfa_login: user.last_mfa_login,
            last_backup_login: user.last_backup_login,
            backup_codes_remaining: backupCodesCount
        });
    } catch (error) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 禁用MFA认证
 */
async function handleMfaDisable(req, env, currentUser) {
    try {
        const { password } = await req.json();

        const user = await env.DB.prepare(
            'SELECT * FROM admin_users WHERE username = ?'
        ).bind(currentUser).first();

        if ((await hashPassword(password)) !== user.password_hash) {
            return jsonResponse({ error: '密码错误' }, 401);
        }

        await env.DB.prepare(
            'UPDATE admin_users SET mfa_enabled = 0, mfa_secret = NULL WHERE username = ?'
        ).bind(currentUser).run();

        await env.DB.prepare(
            'DELETE FROM mfa_backup_codes WHERE username = ?'
        ).bind(currentUser).run();

        return jsonResponse({
            success: true,
            message: 'MFA已禁用'
        });
    } catch (error) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

// 重新生成备份码
async function handleMfaRegenerateBackupCodes(req, env, currentUser) {
    try {
        const { password } = await req.json();

        const user = await env.DB.prepare(
            'SELECT * FROM admin_users WHERE username = ?'
        ).bind(currentUser).first();

        if ((await hashPassword(password)) !== user.password_hash) {
            return jsonResponse({ error: '密码错误' }, 401);
        }

        if (user.mfa_enabled !== 1) {
            return jsonResponse({ error: 'MFA未启用' }, 400);
        }

        const newBackupCodes = Array.from({ length: 10 }, () =>
            Math.random().toString(36).substring(2, 10).toUpperCase()
        );

        await env.DB.prepare(
            'DELETE FROM mfa_backup_codes WHERE username = ?'
        ).bind(currentUser).run();

        const stmts = newBackupCodes.map(code =>
            env.DB.prepare(
                'INSERT INTO mfa_backup_codes (username, code, created_at, used) VALUES (?, ?, ?, 0)'
            ).bind(currentUser, code, Date.now())
        );

        if (stmts.length > 0) {
            await env.DB.batch(stmts);
        }

        return jsonResponse({
            success: true,
            backup_codes: newBackupCodes,
            message: '备份码已重新生成'
        });
    } catch (error) {
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 获取自动更新设置
 */
async function handleGetAutoUpdateSettings(req, env) {
    try {
        const settings = await env.DB.prepare(
            'SELECT source, enabled, updated_at FROM auto_update_settings WHERE source IN (?, ?, ?, ?, ?)'
        ).bind('global_enabled', 'hostmonit_v4', 'hostmonit_v6', 'vps789', 'last_executed').all();

        const result = {
            global_enabled: 0,
            hostmonit_v4: 1,
            hostmonit_v6: 0,
            vps789: 1,
            last_executed: 0
        };

        if (settings && settings.results) {
            settings.results.forEach(setting => {
                if (setting.source === 'last_executed') {
                    result.last_executed = setting.updated_at;
                } else {
                    result[setting.source] = setting.enabled;
                }
            });
        }
        return jsonResponse(result);
    } catch (error) {
        console.error('handleGetAutoUpdateSettings error:', error.message);
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 设置自动更新配置
 */
async function handleSetAutoUpdateSettings(req, env) {
    try {
        const { global_enabled, hostmonit_v4, hostmonit_v6, vps789 } = await req.json();

        const stmts = [
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('global_enabled', global_enabled ? 1 : 0, Date.now()),
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('hostmonit_v4', hostmonit_v4 ? 1 : 0, Date.now()),
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('hostmonit_v6', hostmonit_v6 ? 1 : 0, Date.now()),
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('vps789', vps789 ? 1 : 0, Date.now())
        ];

        await env.DB.batch(stmts);
        return jsonResponse({
            success: true,
            message: '自动更新设置已保存'
        });
    } catch (error) {
        console.error('handleSetAutoUpdateSettings error:', error.message);
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 直接执行IP更新
 */
async function handleDirectIpUpdate(req, env) {
    try {
        const { global_enabled, hostmonit_v4, hostmonit_v6, vps789 } = await req.json();

        // 立即保存这些设置到数据库
        const stmts = [
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('global_enabled', global_enabled ? 1 : 0, Date.now()),
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('hostmonit_v4', hostmonit_v4 ? 1 : 0, Date.now()),
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('hostmonit_v6', hostmonit_v6 ? 1 : 0, Date.now()),
            env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)')
                .bind('vps789', vps789 ? 1 : 0, Date.now())
        ];
        await env.DB.batch(stmts);

        // 构建sources对象用于runIpUpdateTask
        const sources = {
            global_enabled: global_enabled ? 1 : 0,
            hostmonit_v4: hostmonit_v4 ? 1 : 0,
            hostmonit_v6: hostmonit_v6 ? 1 : 0,
            vps789: vps789 ? 1 : 0
        };

        // 直接执行IP更新任务
        const result = await runIpUpdateTask(env, sources);

        return jsonResponse(result);
    } catch (error) {
        console.error('handleDirectIpUpdate error:', error.message);
        return jsonResponse({
            success: false,
            message: 'IP更新失败: ' + error.message
        }, 500);
    }
}

/**
 * 获取访问统计数据
 */
async function handleGetStats(req, env) {
    try {
        const url = new URL(req.url);
        const days = parseInt(url.searchParams.get('days')) || 30;

        // 获取基础统计
        const domains = await env.DB.prepare('SELECT COUNT(*) as c FROM cf_domains').first('c');
        const ips = await env.DB.prepare('SELECT COUNT(*) as c FROM cfips').first('c');
        const uuids = await env.DB.prepare('SELECT COUNT(DISTINCT uuid) as c FROM configs').first('c');
        const templates = await env.DB.prepare('SELECT COUNT(*) as c FROM config_templates').first('c');

        const enabledSetting = await env.DB.prepare(
            'SELECT enabled FROM auto_update_settings WHERE source = ?'
        ).bind('global_enabled').first();
        const enabled = enabledSetting?.enabled || 0;

        const lastExecSetting = await env.DB.prepare(
            'SELECT updated_at FROM auto_update_settings WHERE source = ?'
        ).bind('last_executed').first();
        const lastExec = lastExecSetting?.updated_at || 0;

        // 获取订阅访问统计
        let accessStats = null;
        try {
            accessStats = await getAccessStatsSummary(env, days);
        } catch (e) {
            console.error("获取访问统计失败:", e.message);
            accessStats = {
                success: false,
                daily_stats: [],
                popular_uuids: []
            };
        }

        return jsonResponse({
            domains: domains || 0,
            ips: ips || 0,
            uuids: uuids || 0,
            templates: templates || 0,
            autoUpdate: enabled,
            lastExecuted: lastExec,
            access_stats: accessStats
        });
    } catch (error) {
        console.error('handleGetStats error:', error.message);
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

// 获取单个UUID的访问详情
async function handleGetUUIDAccessDetails(req, env) {
    try {
        const url = new URL(req.url);
        const uuid = url.searchParams.get('uuid');
        const startDate = url.searchParams.get('start_date');
        const endDate = url.searchParams.get('end_date');

        if (!uuid) {
            return jsonResponse({ error: '需要提供UUID参数' }, 400);
        }

        const details = await getUUIDAccessDetails(env, uuid, startDate, endDate);
        return jsonResponse(details);
    } catch (error) {
        console.error('handleGetUUIDAccessDetails error:', error.message);
        return jsonResponse({ error: '服务器错误' }, 500);
    }
}

/**
 * 处理域名管理API
 */
async function handleDomains(req, env, method) {
    try {
        const url = new URL(req.url);

        if (method === 'GET') {
            const page = parseInt(url.searchParams.get('page')) || 1;
            const size = parseInt(url.searchParams.get('size')) || 10;
            const sortField = url.searchParams.get('sort') || 'created_at';
            const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';
            const allowedSorts = ['domain', 'remark', 'created_at', 'source'];
            const actualSort = allowedSorts.includes(sortField) ? sortField : 'created_at';
            const offset = (page - 1) * size;

            // 使用UNION查询两个表的数据，并排序分页
            const query = `
                SELECT * FROM (
                    SELECT id, domain, remark, created_at, 'Cloudflare' as source FROM cf_domains
                    UNION ALL
                    SELECT id, domain, remark, created_at, '腾讯云EdgeOne' as source FROM edgeone_domains
                ) ORDER BY ${actualSort} ${sortOrder}
                LIMIT ? OFFSET ?
            `;

            // 获取总域名数量
            const totalQuery = `
                SELECT COUNT(*) as total FROM (
                    SELECT id FROM cf_domains
                    UNION ALL
                    SELECT id FROM edgeone_domains
                )
            `;

            const totalResult = await env.DB.prepare(totalQuery).first();
            const total = totalResult ? totalResult.total : 0;

            const { results } = await env.DB.prepare(query).bind(size, offset).all();

            return jsonResponse({
                success: true,
                total: total,
                data: results || [],
                page,
                size
            });
        }

        if (method === 'POST') {
            const { domain, remark, source } = await req.json();
            if(!domain) return jsonResponse({success: false, error:'域名不能为空'}, 400);

            if (!source || (source !== 'Cloudflare' && source !== '腾讯云EdgeOne')) {
                return jsonResponse({success: false, error:'域名来源必须为 Cloudflare 或 腾讯云EdgeOne'}, 400);
            }

            if (source === 'Cloudflare') {
                await env.DB.prepare(
                    'INSERT INTO cf_domains (domain, remark, created_at, updated_at) VALUES (?, ?, ?, ?)'
                ).bind(domain, remark || '', Date.now(), Date.now()).run();
            } else {
                await env.DB.prepare(
                    'INSERT INTO edgeone_domains (domain, remark, created_at, updated_at) VALUES (?, ?, ?, ?)'
                ).bind(domain, remark || '', Date.now(), Date.now()).run();
            }

            return jsonResponse({
                success: true,
                message: '域名添加成功'
            });
        }

        if (method === 'PUT') {
            const { id, domain, remark, source } = await req.json();
            if(!id || !domain || !source) return jsonResponse({success: false, error:'ID、域名和来源不能为空'}, 400);

            if (source === 'Cloudflare') {
                await env.DB.prepare(
                    'UPDATE cf_domains SET domain = ?, remark = ?, updated_at = ? WHERE id = ?'
                ).bind(domain, remark || '', Date.now(), id).run();
            } else if (source === '腾讯云EdgeOne') {
                await env.DB.prepare(
                    'UPDATE edgeone_domains SET domain = ?, remark = ?, updated_at = ? WHERE id = ?'
                ).bind(domain, remark || '', Date.now(), id).run();
            } else {
                return jsonResponse({success: false, error:'域名来源必须为 Cloudflare 或 腾讯云EdgeOne'}, 400);
            }

            return jsonResponse({
                success: true,
                message: '域名更新成功'
            });
        }

        if (method === 'DELETE') {
            const { id, source } = await req.json();
            if (!id || !source) return jsonResponse({success: false, error:'ID和来源不能为空'}, 400);

            if (source === 'Cloudflare') {
                await env.DB.prepare('DELETE FROM cf_domains WHERE id = ?').bind(id).run();
            } else if (source === '腾讯云EdgeOne') {
                await env.DB.prepare('DELETE FROM edgeone_domains WHERE id = ?').bind(id).run();
            } else {
                return jsonResponse({success: false, error:'域名来源必须为 Cloudflare 或 腾讯云EdgeOne'}, 400);
            }

            return jsonResponse({
                success: true,
                message: '域名删除成功'
            });
        }

        return jsonResponse({success: false, error: '不支持的请求方法'}, 405);
    } catch (error) {
        console.error('handleDomains error:', error.message);
        return jsonResponse({success: false, error: '服务器错误'}, 500);
    }
}

/**
 * 处理IP管理API
 */
async function handleIps(req, env, method) {
    try {
        const url = new URL(req.url);

        if (method === 'GET') {
            const page = parseInt(url.searchParams.get('page')) || 1;
            const size = parseInt(url.searchParams.get('size')) || 20;
            const sortField = url.searchParams.get('sort') || 'created_at';
            const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';

            const allowedSorts = ['ip', 'ip_type', 'carrier', 'source', 'created_at'];
            const actualSort = allowedSorts.includes(sortField) ? sortField : 'created_at';
            const offset = (page - 1) * size;

            const totalResult = await env.DB.prepare('SELECT COUNT(*) as total FROM cfips').first();
            const total = totalResult ? totalResult.total : 0;
            
            const query = `SELECT * FROM cfips ORDER BY ${actualSort} ${sortOrder} LIMIT ? OFFSET ?`;
            const { results } = await env.DB.prepare(query).bind(size, offset).all();

            return jsonResponse({
                success: true,
                total: total,
                data: results || [],
                page,
                size
            });
        }

        if (method === 'DELETE') {
            const { ip } = await req.json();
            if (!ip) return jsonResponse({success: false, error:'IP不能为空'}, 400);

            await env.DB.prepare('DELETE FROM cfips WHERE ip = ?').bind(ip).run();
            return jsonResponse({
                success: true,
                message: 'IP删除成功'
            });
        }

        return jsonResponse({success: false, error: '不支持的请求方法'}, 405);
    } catch (error) {
        console.error('handleIps error:', error.message);
        return jsonResponse({success: false, error: '服务器错误'}, 500);
    }
}

/**
 * 处理UUID管理API
 */
async function handleUuids(req, env, method) {
    try {
        const url = new URL(req.url);

        if (method === 'GET') {
            const page = parseInt(url.searchParams.get('page')) || 1;
            const size = parseInt(url.searchParams.get('size')) || 10;
            const sortField = url.searchParams.get('sort') || 'updated_at';
            const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';

            // 允许的排序字段增加了owner, email, status等
            const allowedSorts = ['uuid', 'owner', 'email', 'status', 'count', 'updated_at', 'last_accessed', 'access_count'];
            const actualSort = allowedSorts.includes(sortField) ? sortField : 'updated_at';
            const offset = (page - 1) * size;

            const totalResult = await env.DB.prepare('SELECT COUNT(DISTINCT uuid) as total FROM configs').first();
            const total = totalResult ? totalResult.total : 0;
            
            // 修改查询：左连接uuid_info表获取所有者信息
            const query = `
                SELECT 
                    c.uuid, 
                    COUNT(*) as count, 
                    MAX(c.created_at) as updated_at,
                    ui.owner,
                    ui.email,
                    ui.status,
                    ui.remark,
                    ui.contact_info,
                    ui.last_accessed,
                    ui.access_count
                FROM configs c 
                LEFT JOIN uuid_info ui ON c.uuid = ui.uuid 
                GROUP BY c.uuid 
                ORDER BY ${actualSort} ${sortOrder} 
                LIMIT ? OFFSET ?`;
            
            const { results } = await env.DB.prepare(query).bind(size, offset).all();

            return jsonResponse({
                success: true,
                total: total,
                data: results || [],
                page,
                size
            });
        }

        if (method === 'DELETE') {
            const { uuid } = await req.json();
            if (!uuid) return jsonResponse({success: false, error:'UUID不能为空'}, 400);

            await env.DB.prepare('DELETE FROM configs WHERE uuid = ?').bind(uuid).run();
            return jsonResponse({
                success: true,
                message: 'UUID组删除成功'
            });
        }

        // 添加POST方法用于创建/更新UUID信息
        if (method === 'POST') {
            const { uuid, owner, email, contact_info, status, remark } = await req.json();
            
            if (!uuid) return jsonResponse({success: false, error:'UUID不能为空'}, 400);
            if (!owner) return jsonResponse({success: false, error:'所属人姓名/标识不能为空'}, 400);
            if (!email) return jsonResponse({success: false, error:'邮箱地址不能为空'}, 400);
            
            // 检查UUID是否存在
            const uuidExists = await env.DB.prepare(
                'SELECT COUNT(*) as count FROM configs WHERE uuid = ?'
            ).bind(uuid).first('count') || 0;
            
            if (uuidExists === 0) {
                return jsonResponse({success: false, error:'UUID不存在，请先创建配置'}, 400);
            }
            
            const now = Date.now();
            const statusValue = status || 'active';
            
            // 插入或更新uuid_info表
            await env.DB.prepare(`
                INSERT OR REPLACE INTO uuid_info (
                    uuid, owner, email, contact_info, status, remark, 
                    last_accessed, access_count, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            `).bind(
                uuid, owner, email, contact_info || '', statusValue, remark || '',
                0, 0, now, now
            ).run();
            
            return jsonResponse({
                success: true,
                message: 'UUID信息保存成功'
            });
        }

        // 添加PUT方法用于更新UUID信息
        if (method === 'PUT') {
            const { uuid, owner, email, contact_info, status, remark } = await req.json();
            
            if (!uuid) return jsonResponse({success: false, error:'UUID不能为空'}, 400);
            if (!owner) return jsonResponse({success: false, error:'所属人姓名/标识不能为空'}, 400);
            if (!email) return jsonResponse({success: false, error:'邮箱地址不能为空'}, 400);
            
            // 检查uuid_info记录是否存在
            const existingRecord = await env.DB.prepare(
                'SELECT id FROM uuid_info WHERE uuid = ?'
            ).bind(uuid).first();
            
            if (!existingRecord) {
                return jsonResponse({success: false, error:'UUID信息记录不存在'}, 404);
            }
            
            const now = Date.now();
            const statusValue = status || 'active';
            
            // 更新uuid_info表
            await env.DB.prepare(`
                UPDATE uuid_info SET 
                    owner = ?, 
                    email = ?, 
                    contact_info = ?, 
                    status = ?, 
                    remark = ?,
                    updated_at = ?
                WHERE uuid = ?
            `).bind(
                owner, email, contact_info || '', statusValue, remark || '', now, uuid
            ).run();
            
            return jsonResponse({
                success: true,
                message: 'UUID信息更新成功'
            });
        }

        return jsonResponse({success: false, error: '不支持的请求方法'}, 405);
    } catch (error) {
        console.error('handleUuids error:', error.message);
        return jsonResponse({success: false, error: '服务器错误'}, 500);
    }
}

// =================================================================
//  TEMPLATE MANAGEMENT API (模板管理)
// =================================================================

/**
 * 处理模板管理API
 */
async function handleTemplates(req, env, method) {
    try {
        const url = new URL(req.url);

        if (method === 'GET') {
            const page = parseInt(url.searchParams.get('page')) || 1;
            const size = parseInt(url.searchParams.get('size')) || 10;
            const sortField = url.searchParams.get('sort') || 'created_at';
            const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';
            
            const allowedSorts = ['name', 'url', 'description', 'is_default', 'created_at', 'updated_at'];
            const actualSort = allowedSorts.includes(sortField) ? sortField : 'created_at';
            const offset = (page - 1) * size;

            // 获取总模板数量
            const totalResult = await env.DB.prepare('SELECT COUNT(*) as total FROM config_templates').first();
            const total = totalResult ? totalResult.total : 0;
            
            // 获取模板数据
            const { results } = await env.DB.prepare(
                `SELECT * FROM config_templates ORDER BY ${actualSort} ${sortOrder} LIMIT ? OFFSET ?`
            ).bind(size, offset).all();

            return jsonResponse({
                success: true,
                total: total,
                data: results || [],
                page,
                size
            });
        }

        if (method === 'POST') {
            const { name, url, description, is_default } = await req.json();
            
            if (!name || !url) {
                return jsonResponse({success: false, error: '模板名称和URL不能为空'}, 400);
            }

            // 验证名称唯一性
            const existing = await env.DB.prepare(
                'SELECT id FROM config_templates WHERE name = ?'
            ).bind(name).first();
            
            if (existing) {
                return jsonResponse({success: false, error: '模板名称已存在'}, 400);
            }

            // URL验证
            if (!url.startsWith('http://') && !url.startsWith('https://')) {
                return jsonResponse({success: false, error: 'URL必须以 http:// 或 https:// 开头'}, 400);
            }

            // 如果设置为默认模板，先取消其他默认模板
            if (is_default === 1) {
                await env.DB.prepare(
                    'UPDATE config_templates SET is_default = 0 WHERE is_default = 1'
                ).run();
            }

            const now = Date.now();
            await env.DB.prepare(
                'INSERT INTO config_templates (name, url, description, is_default, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)'
            ).bind(name, url, description || '', is_default || 0, now, now).run();

            return jsonResponse({
                success: true,
                message: '模板添加成功'
            });
        }

        if (method === 'PUT') {
            const { id, name, url, description, is_default } = await req.json();
            
            if (!id || !name || !url) {
                return jsonResponse({success: false, error: 'ID、名称和URL不能为空'}, 400);
            }

            // 验证名称唯一性（排除当前记录）
            const existing = await env.DB.prepare(
                'SELECT id FROM config_templates WHERE name = ? AND id != ?'
            ).bind(name, id).first();
            
            if (existing) {
                return jsonResponse({success: false, error: '模板名称已存在'}, 400);
            }

            // URL验证
            if (!url.startsWith('http://') && !url.startsWith('https://')) {
                return jsonResponse({success: false, error: 'URL必须以 http:// 或 https:// 开头'}, 400);
            }

            // 如果设置为默认模板，先取消其他默认模板
            if (is_default === 1) {
                await env.DB.prepare(
                    'UPDATE config_templates SET is_default = 0 WHERE is_default = 1'
                ).run();
            }

            const now = Date.now();
            await env.DB.prepare(
                'UPDATE config_templates SET name = ?, url = ?, description = ?, is_default = ?, updated_at = ? WHERE id = ?'
            ).bind(name, url, description || '', is_default || 0, now, id).run();

            return jsonResponse({
                success: true,
                message: '模板更新成功'
            });
        }

        if (method === 'DELETE') {
            const { id } = await req.json();
            if (!id) return jsonResponse({success: false, error: 'ID不能为空'}, 400);

            // 检查是否为默认模板
            const template = await env.DB.prepare(
                'SELECT is_default FROM config_templates WHERE id = ?'
            ).bind(id).first();

            if (template && template.is_default === 1) {
                return jsonResponse({success: false, error: '不能删除默认模板，请先设置其他模板为默认'}, 400);
            }

            await env.DB.prepare('DELETE FROM config_templates WHERE id = ?').bind(id).run();

            return jsonResponse({
                success: true,
                message: '模板删除成功'
            });
        }

        return jsonResponse({success: false, error: '不支持的请求方法'}, 405);
    } catch (error) {
        console.error('handleTemplates error:', error.message);
        return jsonResponse({success: false, error: '服务器错误: ' + error.message}, 500);
    }
}

/**
 * 设置默认模板
 */
async function handleSetDefaultTemplate(req, env) {
    try {
        const { id } = await req.json();
        if (!id) return jsonResponse({success: false, error: 'ID不能为空'}, 400);

        // 开始事务：先取消所有默认，再设置新的默认
        await env.DB.batch([
            env.DB.prepare('UPDATE config_templates SET is_default = 0 WHERE is_default = 1'),
            env.DB.prepare('UPDATE config_templates SET is_default = 1, updated_at = ? WHERE id = ?').bind(Date.now(), id)
        ]);

        return jsonResponse({
            success: true,
            message: '默认模板设置成功'
        });
    } catch (error) {
        console.error('handleSetDefaultTemplate error:', error.message);
        return jsonResponse({success: false, error: '服务器错误'}, 500);
    }
}

// =================================================================
//  MAIN ROUTE HANDLERS (主路由处理)
// =================================================================

/**
 * 主API路由处理
 */
async function handleApi(req, env) {
    const url = new URL(req.url);
    const path = url.pathname;
    const method = req.method;

    try {
        // 无需认证的API
        if (path === '/api/login' && method === 'POST') {
            return await handleLogin(req, env);
        }

        if (path === '/api/mfa/init' && method === 'POST') {
            return await handleMfaInit(req, env);
        }

        if (path === '/api/mfa/verify-first' && method === 'POST') {
            return await handleMfaVerifyFirst(req, env);
        }

        if (path === '/api/mfa/login-with-backup' && method === 'POST') {
            return await handleMfaLoginWithBackup(req, env);
        }

        // 需要JWT认证的API
        const authHeader = req.headers.get('Authorization');
        if (!authHeader || !authHeader.startsWith('Bearer ')) {
            return jsonResponse({ error: '未授权，请重新登录' }, 401);
        }

        const token = authHeader.split(' ')[1];
        const payload = await verifyJwt(token, env.JWT_SECRET || DEFAULT_JWT_SECRET);

        if (!payload) {
            return jsonResponse({ error: 'Token 无效或已过期' }, 401);
        }

        const currentUser = payload.sub;

        // 认证后的API
        if (path === '/api/change-password' && method === 'POST') {
            return await handleChangePassword(req, env, currentUser);
        }

        if (path === '/api/mfa/status' && method === 'GET') {
            return await handleMfaStatus(req, env, currentUser);
        }

        if (path === '/api/mfa/disable' && method === 'POST') {
            return await handleMfaDisable(req, env, currentUser);
        }

        if (path === '/api/mfa/backup-codes/regenerate' && method === 'POST') {
            return await handleMfaRegenerateBackupCodes(req, env, currentUser);
        }

        if (path === '/api/settings/auto-update' && method === 'GET') {
            return await handleGetAutoUpdateSettings(req, env);
        }

        if (path === '/api/settings/auto-update' && method === 'POST') {
            return await handleSetAutoUpdateSettings(req, env);
        }

        if (path === '/api/stats' && method === 'GET') {
            return await handleGetStats(req, env);
        }

        // 获取UUID访问详情
        if (path === '/api/stats/uuid-details' && method === 'GET') {
            return await handleGetUUIDAccessDetails(req, env);
        }

        if (path === '/api/domains' && ['GET', 'POST', 'PUT', 'DELETE'].includes(method)) {
            return await handleDomains(req, env, method);
        }

        if (path === '/api/ips' && ['GET', 'DELETE'].includes(method)) {
            return await handleIps(req, env, method);
        }

        // 直接IP更新接口
        if (path === '/api/ips/update' && method === 'POST') {
            return await handleDirectIpUpdate(req, env);
        }

        if (path === '/api/uuids' && ['GET', 'DELETE', 'POST', 'PUT'].includes(method)) {
            return await handleUuids(req, env, method);
        }

        // 模板管理API（新增）
        if (path === '/api/templates' && ['GET', 'POST', 'PUT', 'DELETE'].includes(method)) {
            return await handleTemplates(req, env, method);
        }

        if (path === '/api/templates/set-default' && method === 'POST') {
            return await handleSetDefaultTemplate(req, env);
        }

        return jsonResponse({ error: 'API端点不存在' }, 404);

    } catch (error) {
        console.error('API处理错误:', error);
        return jsonResponse({ error: '服务器内部错误: ' + error.message }, 500);
    }
}

// =================================================================
//  HTML TEMPLATES (HTML模板)
// =================================================================

const globalCss = `
@import url('https://cdn.jsdelivr.net/npm/tailwindcss@3.4.1/tailwind.min.css');

* {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
}

html { 
    font-size: 14px; 
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
}

body, html { 
    margin: 0; 
    padding: 0; 
    min-height: 100vh; 
    background-color: #fff; 
    line-height: 1.5;
    color: #3d474d;
}

:root {
    --primary-color: #5a666d;
    --secondary-color: #89949B;
    --success-color: #10b981;
    --danger-color: #ef4444;
    --warning-color: #f59e0b;
    --info-color: #3b82f6;
    --light-color: #f8f9fa;
    --dark-color: #3d474d;
    --border-color: #E8EBED;
    --shadow-color: rgba(0,0,0,0.1);
}

/* ============ 容器和布局 ============ */
.container { 
    width: 100%; 
    min-height: 100vh; 
    display: flex; 
    flex-direction: column; 
    align-items: center; 
    padding: 20px; 
}

.content-group { 
    width: 100%; 
    max-width: 1200px; 
    text-align: center; 
    margin-top: 20px; 
}

/* ============ 顶部栏 ============ */
.top-bar { 
    width: 100%; 
    max-width: 1200px; 
    display: flex; 
    justify-content: space-between; 
    margin-bottom: 20px; 
    align-items: center; 
    padding: 0 5px; 
}

/* ============ 标题和描述 ============ */
.profile-name { 
    font-size: 2rem; 
    color: var(--dark-color); 
    margin-bottom: 5px; 
    font-weight: bold;
}

.profile-quote { 
    color: var(--secondary-color); 
    margin-bottom: 10px; 
    min-height: 1em; 
}

/* ============ 操作链接 ============ */
.action-link { 
    font-size: 0.9rem; 
    color: var(--secondary-color); 
    cursor: pointer; 
    text-decoration: none; 
    margin-right: 15px; 
    transition: color 0.2s; 
    display: inline-flex;
    align-items: center;
    gap: 4px;
}

.action-link:hover { 
    color: var(--primary-color); 
    text-decoration: underline; 
}

.action-link.logout { 
    color: var(--danger-color); 
}

.action-link.logout:hover { 
    color: #d32f2f; 
}

/* ============ 导航栏 ============ */
.nav-grid { 
    display: flex; 
    flex-wrap: wrap; 
    justify-content: center; 
    gap: 8px; 
    margin-bottom: 25px; 
    border-bottom: 1px solid var(--border-color); 
    padding-bottom: 20px; 
}

.nav-btn { 
    display: inline-flex; 
    align-items: center; 
    justify-content: center; 
    padding: 8px 16px; 
    background: var(--border-color); 
    border: 2px solid var(--secondary-color); 
    border-radius: 4px; 
    color: var(--primary-color); 
    font-weight: 500; 
    font-size: 0.95rem; 
    cursor: pointer; 
    transition: all 0.2s; 
    white-space: nowrap; 
    text-decoration: none; 
    outline: none;
}

.nav-btn:hover { 
    background: var(--secondary-color); 
    color: white; 
    border-color: var(--secondary-color);
}

.nav-btn.active { 
    background-color: var(--primary-color); 
    color: white; 
    border-color: var(--primary-color); 
}

.nav-btn.danger { 
    border-color: var(--danger-color); 
    color: var(--danger-color); 
    background: #fee2e2; 
}

.nav-btn.danger:hover { 
    background: var(--danger-color); 
    color: white; 
}

.nav-btn.small { 
    padding: 4px 10px; 
    font-size: 0.85rem; 
}

/* ============ 卡片 ============ */
.card { 
    background: var(--light-color); 
    border: 1px solid var(--border-color); 
    border-radius: 8px;
    padding: 24px;
    margin-bottom: 24px;
    text-align: left; 
    display: none; 
    animation: fadeIn 0.3s ease; 
    width: 100%;
}

.card.active { 
    display: block; 
}

.card h2 { 
    font-size: 1.4rem; 
    color: var(--dark-color); 
    margin-top: 0; 
    margin-bottom: 20px; 
    padding-bottom: 10px; 
    border-bottom: 2px solid var(--border-color); 
}

@keyframes fadeIn { 
    from { opacity: 0; transform: translateY(5px); } 
    to { opacity: 1; transform: translateY(0); } 
}

/* ============ 表单元素 ============ */
input[type="text"], 
input[type="password"], 
input[type="number"], 
input[type="email"], 
select, 
textarea { 
    width: 100%; 
    padding: 10px; 
    border: 2px solid var(--secondary-color); 
    border-radius: 4px; 
    background: #fff; 
    font-size: 0.9rem; 
    box-sizing: border-box; 
    margin-bottom: 10px; 
    font-family: inherit;
    transition: border-color 0.2s;
}

input:focus, 
select:focus, 
textarea:focus { 
    outline: none; 
    border-color: var(--primary-color); 
}

/* ============ 表格 ============ */
.table-container { 
    overflow-x: auto; 
    border: 2px solid var(--secondary-color); 
    border-radius: 4px; 
    background: #fff; 
    margin-bottom: 15px;
}

table { 
    width: 100%; 
    border-collapse: collapse; 
    font-size: 0.9rem; 
    min-width: 600px;
}

th, td { 
    padding: 10px 14px; 
    text-align: left; 
    border-bottom: 1px solid var(--border-color); 
}

th { 
    font-weight: 600; 
    color: var(--dark-color); 
    background-color: #f0f2f5; 
    cursor: pointer; 
    user-select: none; 
    position: relative;
}

th:hover { 
    background-color: #e2e6ea; 
}

th.sortable::after {
    content: '↕';
    margin-left: 5px;
    opacity: 0.5;
    font-size: 0.8em;
}

/* ============ 分页 ============ */
.pagination-bar { 
    display: flex; 
    justify-content: space-between; 
    align-items: center; 
    padding-top: 15px; 
    font-size: 0.9rem; 
    color: var(--primary-color);
    min-height: 40px;
}

.page-ctrl { 
    display: flex; 
    gap: 5px; 
    align-items: center; 
}

.page-info { 
    margin: 0 10px; 
    white-space: nowrap;
    min-width: 120px;
    text-align: center;
}

select.page-size { 
    padding: 4px 8px; 
    border: 1px solid var(--secondary-color); 
    border-radius: 4px; 
    background: #fff; 
    font-size: 0.85rem;
}

/* ============ 页脚 ============ */
.footer { 
    margin-top: auto; 
    padding-top: 40px; 
    color: var(--secondary-color); 
    font-size: 0.8rem; 
    text-align: center; 
    width: 100%; 
    border-top: 1px solid #f0f0f0; 
}

.footer a { 
    color: var(--primary-color); 
    text-decoration: none; 
}

.footer a:hover { 
    text-decoration: underline; 
}

/* ============ 开关控件 ============ */
.switch-group { 
    display: flex; 
    flex-wrap: wrap; 
    gap: 20px; 
    align-items: center; 
    margin-bottom: 15px; 
    background: var(--border-color); 
    padding: 15px; 
    border-radius: 4px; 
}

.switch-label { 
    font-weight: 500; 
    color: var(--primary-color); 
    display: flex; 
    align-items: center; 
    gap: 8px;
    cursor: pointer;
}

.switch { 
    position: relative; 
    display: inline-block; 
    width: 40px; 
    height: 22px; 
}

.switch input { 
    opacity: 0; 
    width: 0; 
    height: 0; 
}

.slider { 
    position: absolute; 
    cursor: pointer; 
    top: 0; 
    left: 0; 
    right: 0; 
    bottom: 0; 
    background-color: #ccc; 
    transition: .4s; 
    border-radius: 34px; 
}

.slider:before { 
    position: absolute; 
    content: ""; 
    height: 16px; 
    width: 16px; 
    left: 3px; 
    bottom: 3px; 
    background-color: white; 
    transition: .4s; 
    border-radius: 50%; 
}

input:checked + .slider { 
    background-color: var(--success-color); 
}

input:checked + .slider:before { 
    transform: translateX(18px); 
}

/* ============ 自动更新状态 ============ */
.auto-update-header { 
    display: flex; 
    justify-content: space-between; 
    align-items: center; 
    margin-bottom: 15px; 
}

.auto-update-status { 
    font-size: 0.9rem; 
    color: var(--dark-color); 
    padding: 5px 10px; 
    background: var(--border-color); 
    border-radius: 4px; 
    display: inline-flex; 
    align-items: center; 
    gap: 8px; 
}

.status-indicator { 
    width: 8px; 
    height: 8px; 
    border-radius: 50%; 
    display: inline-block; 
}

.status-on { 
    background-color: var(--success-color); 
}

.status-off { 
    background-color: var(--danger-color); 
}

.status-warning { 
    background-color: var(--warning-color); 
}

/* ============ MFA相关样式 ============ */
.mfa-status-badge { 
    display: inline-flex; 
    align-items: center; 
    padding: 4px 12px; 
    border-radius: 20px; 
    font-size: 0.85rem; 
    font-weight: 500; 
    gap: 6px; 
}

.mfa-active { 
    background-color: #d1fae5; 
    color: #065f46; 
}

.mfa-inactive { 
    background-color: #fee2e2; 
    color: #9f1239; 
}

/* ============ 二维码和备份码 ============ */
.qr-code-container { 
    text-align: center; 
    margin: 20px 0; 
    padding: 15px; 
    background: #f9fafb; 
    border-radius: 8px; 
    border: 1px solid #e5e7eb; 
}

.qr-instructions { 
    font-size: 0.9rem; 
    color: #6b7280; 
    margin-top: 15px; 
    line-height: 1.5; 
}

.backup-codes-box { 
    background: #f3f4f6; 
    border: 1px dashed #d1d5db; 
    padding: 15px; 
    border-radius: 6px; 
    margin: 15px 0; 
    font-family: monospace; 
    text-align: center; 
    line-height: 2; 
}

.backup-code { 
    display: inline-block; 
    margin: 5px; 
    padding: 5px 10px; 
    background: white; 
    border: 1px solid #e5e7eb; 
    border-radius: 4px; 
    font-weight: bold; 
}

/* ============ 安全步骤 ============ */
.security-step { 
    background: #f0f9ff; 
    border: 1px solid #bae6fd; 
    padding: 15px; 
    border-radius: 8px; 
    margin: 15px 0; 
}

.security-step h4 { 
    color: #0369a1; 
    margin-top: 0; 
}

.security-step ol, 
.security-step ul { 
    padding-left: 20px; 
}

/* ============ Toast消息 ============ */
#toast-container { 
    position: fixed; 
    top: 20px; 
    right: 20px; 
    z-index: 9999; 
    display: flex; 
    flex-direction: column; 
    gap: 10px; 
}

.toast { 
    padding: 12px 18px; 
    border-radius: 4px; 
    border: 2px solid var(--secondary-color); 
    background: #fff; 
    color: var(--dark-color); 
    font-weight: 500; 
    font-size: 0.9rem; 
    box-shadow: 0 4px 12px var(--shadow-color); 
    animation: slideIn 0.3s forwards, fadeOut 0.5s 3.5s forwards; 
    min-width: 250px;
    max-width: 350px;
}

@keyframes slideIn { 
    from { transform: translateX(100%); opacity: 0; } 
    to { transform: translateX(0); opacity: 1; } 
}

@keyframes fadeOut { 
    to { opacity: 0; transform: translateX(100%); } 
}

/* ============ 模态框 ============ */
.modal-overlay { 
    position: fixed; 
    top: 0; 
    left: 0; 
    width: 100%; 
    height: 100%; 
    background: rgba(0,0,0,0.5); 
    z-index: 1000; 
    display: none; 
    align-items: center; 
    justify-content: center; 
    padding: 20px;
    overflow-y: auto;
}

.modal { 
    background: #fff; 
    width: 90%; 
    max-width: 500px; 
    padding: 25px; 
    border-radius: 8px; 
    box-shadow: 0 10px 25px rgba(0,0,0,0.2); 
    max-height: 90vh; 
    overflow-y: auto; 
}

.modal h3 { 
    margin-top: 0; 
    color: var(--dark-color); 
    margin-bottom: 15px;
}

/* ============ 统计卡片 ============ */
.stat-grid { 
    display: grid; 
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); 
    gap: 15px; 
    margin-bottom: 20px; 
}

.stat-box { 
    background: var(--border-color); 
    padding: 20px; 
    border-radius: 8px; 
    text-align: center; 
    transition: transform 0.2s;
}

.stat-box:hover {
    transform: translateY(-2px);
    box-shadow: 0 4px 12px var(--shadow-color);
}

.stat-num { 
    font-size: 2rem; 
    color: var(--primary-color); 
    font-weight: bold; 
    display: block; 
    margin-bottom: 5px;
}

.stat-label { 
    font-size: 0.85rem; 
    color: var(--secondary-color); 
}

.last-update-info { 
    margin-top: 10px; 
    font-size: 0.85rem; 
    color: var(--dark-color); 
    background: var(--border-color);
    padding: 10px 15px;
    border-radius: 4px;
}

/* ============ 安全区域 ============ */
.security-section { 
    background: #f9fafb; 
    border-left: 4px solid #3b82f6; 
    padding: 15px; 
    margin: 20px 0; 
    border-radius: 0 8px 8px 0; 
}

.security-section h3 { 
    margin-top: 0; 
    color: #1e40af; 
}

.mfa-container { 
    max-width: 600px; 
    margin: 0 auto; 
}

.secret-display { 
    background: #1f2937; 
    color: #fbbf24; 
    padding: 15px; 
    border-radius: 6px; 
    font-family: monospace; 
    text-align: center; 
    margin: 15px 0; 
    font-size: 1.1rem; 
    letter-spacing: 1px; 
    word-break: break-all;
}

.copy-btn { 
    margin-left: 10px; 
    padding: 5px 10px; 
    background: #4b5563; 
    color: white; 
    border: none; 
    border-radius: 4px; 
    cursor: pointer; 
    font-size: 0.8rem; 
    transition: background 0.2s;
}

.copy-btn:hover { 
    background: #374151; 
}

/* ============ 登录页面 ============ */
.login-box { 
    width: 100%; 
    max-width: 400px; 
    padding: 40px 30px; 
    border: 2px solid var(--secondary-color); 
    border-radius: 8px; 
    background: #fff; 
    text-align: center; 
    margin: auto; 
    box-shadow: 0 4px 20px rgba(0, 0, 0, 0.08); 
}

.login-step { 
    display: block; 
}

.totp-input-container { 
    display: flex; 
    gap: 10px; 
    margin: 20px 0; 
}

.totp-input { 
    flex: 1; 
    text-align: center; 
    font-size: 1.5rem; 
    letter-spacing: 10px; 
    font-weight: bold; 
    padding: 10px;
}

.backup-link { 
    display: block; 
    margin-top: 15px; 
    font-size: 0.9rem; 
    color: #6b7280; 
    text-decoration: underline; 
    cursor: pointer; 
}

/* ============ 图表 ============ */
.chart-container { 
    position: relative; 
    width: 100%; 
    height: 400px; 
    margin: 20px 0; 
}

.chart-controls { 
    display: flex; 
    flex-wrap: wrap;
    gap: 10px; 
    margin-bottom: 15px; 
    align-items: center; 
}

.chart-controls select { 
    padding: 5px 10px; 
    border: 2px solid var(--secondary-color); 
    border-radius: 4px; 
    background: #fff; 
    min-width: 120px;
}

/* ============ 访问统计 ============ */
.access-stat-grid { 
    display: grid; 
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); 
    gap: 15px; 
    margin-bottom: 20px; 
}

.access-stat-box { 
    background: #f0f4f8; 
    padding: 15px; 
    border-radius: 8px; 
    border-left: 4px solid #3b82f6; 
    transition: transform 0.2s;
}

.access-stat-box:hover {
    transform: translateY(-2px);
    box-shadow: 0 4px 8px var(--shadow-color);
}

.access-stat-num { 
    font-size: 1.8rem; 
    color: #1e3a8a; 
    font-weight: bold; 
    display: block; 
}

.access-stat-label { 
    font-size: 0.85rem; 
    color: #4b5563; 
    margin-top: 5px; 
}

.access-stat-sub { 
    font-size: 0.75rem; 
    color: #6b7280; 
    margin-top: 3px; 
}

.access-detail-table { 
    width: 100%; 
    font-size: 0.85rem; 
    border-collapse: collapse;
}

.access-detail-table th { 
    background: #f8fafc; 
    font-weight: 600; 
    position: sticky;
    top: 0;
}

.access-detail-table td { 
    padding: 8px 10px; 
    border-bottom: 1px solid #e5e7eb;
}

.access-detail-table .type-badge { 
    display: inline-block; 
    padding: 2px 6px; 
    border-radius: 10px; 
    font-size: 0.75rem; 
    font-weight: 500; 
}

.type-subscription { 
    background: #dbeafe; 
    color: #1e40af; 
}

.type-apigen { 
    background: #d1fae5; 
    color: #065f46; 
}

/* ============ 热门UUID列表 ============ */
.popular-uuids-list { 
    max-height: 200px; 
    overflow-y: auto; 
    border: 1px solid #e5e7eb; 
    border-radius: 4px; 
    padding: 10px; 
}

.popular-uuids-item { 
    display: flex; 
    justify-content: space-between; 
    padding: 8px; 
    border-bottom: 1px solid #f3f4f6; 
    cursor: pointer;
    transition: background 0.2s;
}

.popular-uuids-item:hover { 
    background: #f8fafc; 
}

.popular-uuids-item:last-child { 
    border-bottom: none; 
}

.popular-uuids-uuid { 
    font-family: monospace; 
    font-size: 0.85rem; 
    word-break: break-all;
}

.popular-uuids-count { 
    font-weight: bold; 
    color: #1e40af; 
}

/* ============ 徽章样式 ============ */
.hosting-badge { 
    display: inline-block; 
    padding: 4px 10px; 
    border-radius: 12px; 
    font-size: 0.75rem; 
    font-weight: 500; 
    line-height: 1.4;
    white-space: nowrap;
}

.hosting-cloudflare { 
    background: #F6821F; 
    color: #fff; 
    border: 1px solid #F6821F; 
}

.hosting-tencent { 
    background: #4A86FF; 
    color: #fff; 
    border: 1px solid #4A86FF; 
}

/* 模板专用徽章 */
.template-badge { 
    display: inline-block; 
    padding: 4px 10px; 
    border-radius: 12px; 
    font-size: 0.75rem; 
    font-weight: 500; 
    line-height: 1.4;
}

.badge-default { 
    background-color: var(--success-color); 
    color: #fff; 
    border: 1px solid var(--success-color); 
}

.badge-normal { 
    background-color: #6b7280; 
    color: #fff; 
    border: 1px solid #6b7280; 
}

/* ============ 模板URL单元格 ============ */
.template-url-cell {
    max-width: 300px;
    word-break: break-all;
    white-space: normal;
}

/* ============ 加载器 ============ */
.loader {
    display: inline-block;
    width: 50px;
    height: 50px;
    border: 3px solid var(--border-color);
    border-radius: 50%;
    border-top-color: var(--primary-color);
    animation: spin 1s ease-in-out infinite;
}

@keyframes spin {
    to { transform: rotate(360deg); }
}

/* ============ 响应式设计 ============ */
@media (max-width: 768px) {
    html { font-size: 13px; }
    
    .container { padding: 15px; }
    
    .content-group { max-width: 100%; }
    
    .nav-grid { flex-direction: column; }
    .nav-btn { width: 100%; }
    
    .stat-grid { grid-template-columns: repeat(2, 1fr); }
    
    .access-stat-grid { grid-template-columns: repeat(2, 1fr); }
    
    .switch-group { flex-direction: column; align-items: flex-start; }
    
    .auto-update-header { flex-direction: column; align-items: flex-start; gap: 10px; }
    
    .pagination-bar { flex-direction: column; gap: 10px; }
    .page-ctrl { width: 100%; justify-content: center; }
    
    .modal { width: 95%; padding: 15px; }
}

@media (max-width: 480px) {
    html { font-size: 12px; }
    
    .profile-name { font-size: 1.5rem; }
    
    .stat-grid { grid-template-columns: 1fr; }
    
    .access-stat-grid { grid-template-columns: 1fr; }
    
    .card { padding: 15px; }
    
    .top-bar { flex-direction: column; gap: 10px; align-items: flex-start; }
    
    /* 分页按钮在移动端优化 */
    .page-ctrl button {
        padding: 8px 12px;
        font-size: 0.9rem;
    }
    
    .page-info {
        font-size: 0.8rem;
        min-width: 80px;
    }
}
`;

const loginHtml = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width,initial-scale=1.0">
    <link rel="icon" href="https://s3.yangzifun.org/logo.ico">
    <title>优选配置管理后台 - 登录</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/lucide@latest/dist/lucide.min.css">
    <script src="https://cdn.jsdelivr.net/npm/lucide@latest/dist/umd/lucide.min.js"></script>
    <style>
        ${globalCss}
        body { display: flex; flex-direction: column; justify-content: center; align-items: center; height: 100vh; min-height: 500px; background: #f9fafb; }
    </style>
</head>
<body>
    <div id="toast-container"></div>
    
    <div class="login-box" id="passwordStep">
        <h2 class="profile-name">优选配置管理后台</h2>
        <p class="profile-quote" id="loginStatus">请先验证身份</p>
        <div id="msg" style="color:#ef4444; font-size:0.9rem; margin-bottom:10px; display:none;"></div>
        
        <form id="loginForm" onsubmit="event.preventDefault(); doLogin();">
            <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 10px;">
                <svg class="lucide-user" width="20" height="20"></svg>
                <input type="text" id="username" placeholder="管理员账号" required autocomplete="username" style="flex: 1; margin: 0;">
            </div>
            <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 15px;">
                <svg class="lucide-lock" width="20" height="20"></svg>
                <input type="password" id="password" placeholder="访问密码" required autocomplete="current-password" style="flex: 1; margin: 0;">
            </div>
            <button type="submit" id="loginBtn" class="nav-btn active" style="width:100%; margin-top:15px;">
                <svg class="lucide-arrow-right" width="16" height="16"></svg>
                登 录
            </button>
        </form>
        
        <div style="margin-top: 15px; font-size: 0.8rem; color: #9ca3af;">
            首次登录请确保网络连接正常
        </div>
    </div>

    <div class="login-box" id="totpStep" style="display: none;">
        <h2 class="profile-name">双重验证</h2>
        <p class="profile-quote" id="mfaUsername"></p>
        
        <div style="text-align: center; margin: 20px 0;">
            <div style="font-size: 0.95rem; color: #4b5563; margin-bottom: 15px;">
                请输入您的6位验证码
            </div>
            
            <div class="totp-input-container">
                <input type="text" id="totpCode" maxlength="6" class="totp-input" 
                       pattern="[0-9]{6}" inputmode="numeric" 
                       placeholder="000000" required autocomplete="one-time-code">
            </div>
            
            <div style="color: #6b7280; font-size: 0.85rem; margin-bottom: 15px;">
                请从身份验证器App获取验证码
            </div>
            
            <div style="display: flex; gap: 10px;">
                <button type="button" class="nav-btn" onclick="goBackToPassword()" style="flex: 1;">
                    <svg class="lucide-arrow-left" width="16" height="16"></svg>
                    返回
                </button>
                <button type="button" class="nav-btn active" onclick="verifyTOTP()" id="verifyTOTPBtn" style="flex: 2;">
                    <svg class="lucide-check" width="16" height="16"></svg>
                    验证
                </button>
            </div>
        </div>
        
        <a class="backup-link" onclick="showBackupLogin()">使用备份码登录</a>
    </div>

    <div class="login-box" id="backupStep" style="display: none;">
        <h2 class="profile-name">备份码登录</h2>
        <p class="profile-quote">请输入8位备份码</p>
        
        <input type="text" id="backupUsername" style="display:none;">
        <input type="password" id="backupPassword" style="display:none;">
        
        <div style="text-align: left; margin: 15px 0;">
            <label style="display:block; margin-bottom:5px; font-size:0.9rem; color:#4b5563;">备份码:</label>
            <input type="text" id="backupCode" placeholder="例如：A1B2C3D4" maxlength="8" style="text-transform: uppercase;" required>
        </div>
        
        <div style="display: flex; gap: 10px; margin-top: 20px;">
            <button type="button" class="nav-btn" onclick="cancelBackupLogin()" style="flex: 1;">
                <svg class="lucide-x" width="16" height="16"></svg>
                取消
            </button>
            <button type="button" class="nav-btn active" onclick="verifyBackupCode()" id="verifyBackupBtn" style="flex: 2;">
                <svg class="lucide-check" width="16" height="16"></svg>
                验证备份码
            </button>
        </div>
        
        <div style="margin-top: 15px; font-size: 0.8rem; color: #6b7280; text-align: left;">
            <p>• 备份码为一次性使用，用后失效</p>
            <p>• 请确保备份码准确无误</p>
        </div>
    </div>

    <footer class="footer" style="border:none; width:auto; margin-top: 30px;">
        <p>Powered by <a href="https://www.yangzihome.space" target="_blank">YZFN</a></p>
    </footer>
    
    <script>
        function toast(message, type = 'info') {
            const container = document.getElementById('toast-container');
            const toast = document.createElement('div');
            toast.className = 'toast';
            toast.innerHTML = (type === 'error' ? '❌ ' : '✅ ') + message;
            container.appendChild(toast);
            setTimeout(() => toast.remove(), 4000);
        }
        
        async function doLogin() {
            const username = document.getElementById('username').value;
            const password = document.getElementById('password').value;
            const btn = document.getElementById('loginBtn');
            const msg = document.getElementById('msg');
            
            if (!username || !password) {
                msg.innerText = '请输入用户名和密码';
                msg.style.display = 'block';
                return;
            }
            
            btn.disabled = true;
            btn.innerText = '验证中...';
            msg.style.display = 'none';
            
            try {
                const response = await fetch('/api/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ 
                        username, 
                        password 
                    })
                });
                
                const data = await response.json();
                
                if (response.ok) {
                    if (data.requires_mfa) {
                        sessionStorage.setItem('mfa_username', username);
                        sessionStorage.setItem('mfa_password', password);
                        
                        document.getElementById('passwordStep').style.display = 'none';
                        document.getElementById('totpStep').style.display = 'block';
                        document.getElementById('mfaUsername').innerText = username;
                        document.getElementById('msg').style.display = 'none';
                        
                        setTimeout(() => document.getElementById('totpCode').focus(), 100);
                    } else {
                        localStorage.setItem('token', data.token);
                        localStorage.setItem('mfa_enabled', 'false');
                        window.location.href = '/';
                    }
                } else {
                    msg.innerText = data.error || '登录失败';
                    msg.style.display = 'block';
                }
            } catch (error) {
                msg.innerText = '连接服务器失败';
                msg.style.display = 'block';
            } finally {
                btn.disabled = false;
                btn.innerText = '登 录';
            }
        }
        
        function goBackToPassword() {
            document.getElementById('passwordStep').style.display = 'block';
            document.getElementById('totpStep').style.display = 'none';
            document.getElementById('backupStep').style.display = 'none';
            document.getElementById('msg').style.display = 'none';
            document.getElementById('password').value = '';
            document.getElementById('password').focus();
        }
        
        async function verifyTOTP() {
            const totpCode = document.getElementById('totpCode').value;
            const username = sessionStorage.getItem('mfa_username');
            const password = sessionStorage.getItem('mfa_password');
            const btn = document.getElementById('verifyTOTPBtn');
            
            if (!totpCode || totpCode.length !== 6) {
                toast('请输入6位验证码', 'error');
                return;
            }
            
            btn.disabled = true;
            btn.innerText = '验证中...';
            
            try {
                const response = await fetch('/api/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        username,
                        password,
                        totp_code: totpCode,
                        step: 'verify_totp'
                    })
                });
                
                const data = await response.json();
                
                if (response.ok) {
                    localStorage.setItem('token', data.token);
                    localStorage.setItem('mfa_enabled', 'true');
                    sessionStorage.removeItem('mfa_username');
                    sessionStorage.removeItem('mfa_password');
                    toast('双重验证成功');
                    setTimeout(() => window.location.href = '/', 1000);
                } else {
                    toast(data.error || '验证失败', 'error');
                    document.getElementById('totpCode').value = '';
                    document.getElementById('totpCode').focus();
                }
            } catch (error) {
                toast('验证失败，请重试', 'error');
            } finally {
                btn.disabled = false;
                btn.innerText = '验证';
            }
        }
        
        function showBackupLogin() {
            const username = sessionStorage.getItem('mfa_username');
            const password = sessionStorage.getItem('mfa_password');
            
            if (!username || !password) {
                toast('会话信息丢失，请重新登录', 'error');
                goBackToPassword();
                return;
            }
            
            document.getElementById('backupUsername').value = username;
            document.getElementById('backupPassword').value = password;
            
            document.getElementById('totpStep').style.display = 'none';
            document.getElementById('backupStep').style.display = 'block';
            document.getElementById('backupCode').focus();
        }
        
        function cancelBackupLogin() {
            document.getElementById('backupStep').style.display = 'none';
            document.getElementById('totpStep').style.display = 'block';
            document.getElementById('totpCode').focus();
        }
        
        async function verifyBackupCode() {
            const username = document.getElementById('backupUsername').value;
            const password = document.getElementById('backupPassword').value;
            const backupCode = document.getElementById('backupCode').value.toUpperCase();
            const btn = document.getElementById('verifyBackupBtn');
            
            if (!backupCode || backupCode.length < 6) {
                toast('请输入有效的备份码', 'error');
                return;
            }
            
            btn.disabled = true;
            btn.innerText = '验证中...';
            
            try {
                const response = await fetch('/api/mfa/login-with-backup', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        username,
                        password,
                        backup_code: backupCode
                    })
                });
                
                const data = await response.json();
                
                if (response.ok) {
                    localStorage.setItem('token', data.token);
                    localStorage.setItem('mfa_enabled', 'true');
                    sessionStorage.removeItem('mfa_username');
                    sessionStorage.removeItem('mfa_password');
                    toast('使用备份码登录成功');
                    setTimeout(() => window.location.href = '/', 1000);
                } else {
                    toast(data.error || '备份码无效', 'error');
                    document.getElementById('backupCode').value = '';
                    document.getElementById('backupCode').focus();
                }
            } catch (error) {
                toast('验证失败，请重试', 'error');
            } finally {
                btn.disabled = false;
                btn.innerText = '验证备份码';
            }
        }
        
        document.getElementById('totpCode')?.addEventListener('input', function(e) {
            this.value = this.value.replace(/[^0-9]/g, '');
            if (this.value.length === 6) {
                verifyTOTP();
            }
        });
        
        document.addEventListener('DOMContentLoaded', function() {
            if (localStorage.getItem('token')) {
                window.location.href = '/';
            }
            document.getElementById('username').focus();
            // 初始化 Lucide 图标
            if (typeof lucide !== 'undefined') {
                lucide.createIcons();
            }
        });
    </script>
</body>
</html>
`;

const adminHtml = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width,initial-scale=1.0">
    <link rel="icon" href="https://s3.yangzifun.org/logo.ico">
    <title>优选配置管理后台</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/lucide@latest/dist/lucide.min.css">
    <script src="https://cdn.jsdelivr.net/npm/lucide@latest/dist/umd/lucide.min.js"></script>
    <style>${globalCss}</style>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
</head>
<body>
    <div id="toast-container"></div>
    <div id="loader" style="position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);color:#89949B">系统加载中...</div>

    <div class="container" id="app" style="display:none">
        <div class="top-bar">
            <div>
                <span style="font-weight:bold; color:#5a666d;">CF 优选配置管理</span> 
                <span style="color:#89949B; font-size:0.9rem;">| Admin</span>
                <span id="mfaBadge" style="margin-left:10px; display:none;" class="mfa-status-badge"></span>
            </div>
            <div>
                <a class="action-link" onclick="openPwdModal()">
                    <svg class="lucide-key" width="16" height="16"></svg>
                    修改密码
                </a>
                <a class="action-link logout" onclick="logout()">
                    <svg class="lucide-log-out" width="16" height="16"></svg>
                    安全退出
                </a>
            </div>
        </div>

        <div class="content-group">
            <h1 class="profile-name">管理后台</h1>
            <div class="nav-grid">
                <button class="nav-btn active" onclick="switchTab('dash', this)">
                    <svg class="lucide-chart-line" width="16" height="16"></svg>
                    系统概览
                </button>
                <button class="nav-btn" onclick="switchTab('dom', this)">
                    <svg class="lucide-globe" width="16" height="16"></svg>
                    优选域名
                </button>
                <button class="nav-btn" onclick="switchTab('ips', this)">
                    <svg class="lucide-network" width="16" height="16"></svg>
                    IP 资源池
                </button>
                <button class="nav-btn" onclick="switchTab('uuids', this)">
                    <svg class="lucide-key" width="16" height="16"></svg>
                    配置分组
                </button>
                <button class="nav-btn" onclick="switchTab('templates', this)">
                    <svg class="lucide-layers" width="16" height="16"></svg>
                    配置模板
                </button>
                <button class="nav-btn" onclick="switchTab('security', this)">
                    <svg class="lucide-shield" width="16" height="16"></svg>
                    安全中心
                </button>
            </div>

            <div id="dash" class="card active">
                <h2>系统状态</h2>
                <div class="stat-grid">
                    <div class="stat-box"><span class="stat-num" id="s-dom">-</span><span class="stat-label">优选域名</span></div>
                    <div class="stat-box"><span class="stat-num" id="s-ip">-</span><span class="stat-label">活跃 IP</span></div>
                    <div class="stat-box"><span class="stat-num" id="s-uuid">-</span><span class="stat-label">配置分组</span></div>
                    <div class="stat-box"><span class="stat-num" id="s-template">-</span><span class="stat-label">配置模板</span></div>
                </div>
                
                <div class="access-stat-grid" id="accessStatsGrid" style="display:none;">
                    <div class="access-stat-box">
                        <span class="access-stat-num" id="as-total">0</span>
                        <span class="access-stat-label">总访问次数</span>
                        <span class="access-stat-sub" id="as-unique">0个独立UUID</span>
                    </div>
                    <div class="access-stat-box">
                        <span class="access-stat-num" id="as-today">0</span>
                        <span class="access-stat-label">今日访问</span>
                        <span class="access-stat-sub" id="as-today-split">订阅:0 | 网页:0</span>
                    </div>
                    <div class="access-stat-box">
                        <span class="access-stat-num" id="as-subscription">0</span>
                        <span class="access-stat-label">订阅访问</span>
                        <span class="access-stat-sub">通过订阅链接访问</span>
                    </div>
                    <div class="access-stat-box">
                        <span class="access-stat-num" id="as-apigen">0</span>
                        <span class="access-stat-label">网页生成</span>
                        <span class="access-stat-sub">通过网页工具生成</span>
                    </div>
                </div>
                
                <h3 style="margin-top: 30px; margin-bottom: 15px;">📊 订阅访问趋势分析 (多指标视图)</h3>
                
                <div class="chart-controls">
                    <select id="chartDays" onchange="loadAccessStats()">
                        <option value="7">最近7天</option>
                        <option value="14" selected>最近14天</option>
                        <option value="30">最近30天</option>
                        <option value="60">最近60天</option>
                    </select>
                    <button class="nav-btn" onclick="loadAccessStats()">
                        <svg class="lucide-refresh-cw" width="16" height="16"></svg>
                        刷新数据
                    </button>
                    <button class="nav-btn" onclick="switchChartType('total')" id="chartTotalBtn">总访问量</button>
                    <button class="nav-btn" onclick="switchChartType('split')" id="chartSplitBtn">订阅/网页</button>
                    <button class="nav-btn" onclick="switchChartType('uuids')" id="chartUuidsBtn">活跃UUID数</button>
                    <button class="nav-btn active" onclick="switchChartType('all')" id="chartAllBtn">全部指标</button>
                </div>
                
                <div class="chart-container">
                    <canvas id="accessChart"></canvas>
                </div>
                
                <div style="margin-top: 20px; display: flex; gap: 20px;">
                    <div style="flex: 1;">
                        <h3 style="margin-top: 0; font-size: 1.1rem;">热门 UUID 排行</h3>
                        <div class="popular-uuids-list" id="popularUUIDsList">
                            <div style="text-align: center; padding: 20px; color: #6b7280;">加载中...</div>
                        </div>
                    </div>
                    
                    <div style="flex: 2;">
                        <h3 style="margin-top: 0; font-size: 1.1rem;">访问趋势说明</h3>
                        <div style="background: #f8fafc; padding: 15px; border-radius: 4px; font-size: 0.9rem; color: #4b5563;">
                            <p><strong>订阅访问：</strong>用户通过订阅链接（/batch-configs/{uuid}）访问配置</p>
                            <p><strong>网页生成：</strong>用户通过网页工具（/generate）生成配置</p>
                            <p><strong>独立用户：</strong>按UUID统计的每日活跃用户数</p>
                            <p style="margin-top: 10px; color: #6b7280;">
                                <small>数据统计基于 config_access_logs 表，记录所有UUID配置生成请求</small>
                            </p>
                        </div>
                    </div>
                </div>
                
                <div class="last-update-info">
                    自动更新状态: <span id="autoUpdateStatus">加载中...</span>
                    <br>最后执行时间: <span id="lastExecuted">未知</span>
                </div>
                
                <div class="security-section" id="mfaStatusSection" style="display:none;">
                    <h3>⛑️ 账户安全状态</h3>
                    <div id="mfaStatusDetails">加载中...</div>
                </div>
            </div>

            <div id="dom" class="card">
                <h2>优选域名管理</h2>
                <div style="display:flex; gap:10px; margin-bottom:15px; flex-wrap: wrap;">
                    <input type="text" id="newD" placeholder="域名 (例如: cf.example.com)" style="flex:2; min-width: 200px; margin:0">
                    <input type="text" id="newR" placeholder="自定义备注" style="flex:1; min-width: 150px; margin:0">
                    <select id="newSource" style="flex:1; min-width: 150px; margin:0; padding: 10px; border: 2px solid #89949B; border-radius: 4px; background: #fff; font-size: 0.9rem; box-sizing: border-box; color: #3d474d; cursor: pointer; transition: border-color 0.2s; font-family: inherit;">
                        <option value="Cloudflare">Cloudflare</option>
                        <option value="腾讯云EdgeOne">腾讯云EdgeOne</option>
                    </select>
                    <button class="nav-btn active" onclick="addDomain()" style="min-width: 100px;">
                        <svg class="lucide-plus" width="16" height="16"></svg>
                        添加域名
                    </button>
                </div>
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th class="sortable" onclick="sortDom('domain')">域名 ↕</th>
                                <th class="sortable" onclick="sortDom('remark')">备注 ↕</th>
                                <th class="sortable" onclick="sortDom('source')">来源 ↕</th>
                                <th class="sortable" onclick="sortDom('created_at')">创建时间 ↕</th>
                                <th style="width: 150px;">操作</th>
                            </tr>
                        </thead>
                        <tbody id="domList">
                            <tr><td colspan="5" style="text-align:center; padding:20px;">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                <div class="pagination-bar" id="domPager" style="display: flex;">
                    <div class="page-ctrl">
                        <select class="page-size" id="domSize" onchange="changeDomSize()">
                            <option value="5">5条/页</option>
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                        </select>
                    </div>
                    <div class="page-ctrl">
                        <button class="nav-btn small" onclick="changeDomPage(-1)" id="domPrevBtn">上一页</button>
                        <span class="page-info" id="domPageInfo">1 / 1</span>
                        <button class="nav-btn small" onclick="changeDomPage(1)" id="domNextBtn">下一页</button>
                    </div>
                </div>
            </div>

            <div id="ips" class="card">
                <div class="auto-update-header">
                    <h2>IP 资源池管理</h2>
                    <div style="display: flex; align-items: center; gap: 15px;">
                        <div class="auto-update-status">
                            <span class="status-indicator" id="globalStatusIndicator"></span>
                            <span id="globalStatusText">自动更新: 关闭</span>
                        </div>
                    </div>
                </div>
                
                <div class="switch-group">
                    <label class="switch-label">主开关
                        <div class="switch">
                            <input type="checkbox" id="sw-global" checked>
                            <span class="slider"></span>
                        </div>
                    </label>
                    <label class="switch-label">HostMonit IPv4接口
                        <div class="switch">
                            <input type="checkbox" id="sw-hm-v4" checked>
                            <span class="slider"></span>
                        </div>
                    </label>
                    <label class="switch-label">HostMonit IPv6接口
                        <div class="switch">
                            <input type="checkbox" id="sw-hm-v6">
                            <span class="slider"></span>
                        </div>
                    </label>
                    <label class="switch-label">Vps789接口
                        <div class="switch">
                            <input type="checkbox" id="sw-v7" checked>
                            <span class="slider"></span>
                        </div>
                    </label>
                    <button class="nav-btn" onclick="saveAutoUpdateSettings()">
                        <svg class="lucide-save" width="16" height="16"></svg>
                        保存设置
                    </button>
                    <button class="nav-btn active" onclick="refreshIps()">
                        <svg class="lucide-refresh-cw" width="16" height="16"></svg>
                        立即更新
                    </button>
                </div>
                
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th class="sortable" onclick="sortIp('ip')">IP 地址 ↕</th>
                                <th class="sortable" onclick="sortIp('ip_type')">类型 ↕</th>
                                <th class="sortable" onclick="sortIp('carrier')">运营商 ↕</th>
                                <th class="sortable" onclick="sortIp('source')">来源 ↕</th>
                                <th class="sortable" onclick="sortIp('created_at')">入库时间 ↕</th>
                                <th style="width: 100px;">操作</th>
                            </tr>
                        </thead>
                        <tbody id="ipList">
                            <tr><td colspan="6" style="text-align:center; padding:20px;">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                <div class="pagination-bar" id="ipPager" style="display: flex;">
                    <div class="page-ctrl">
                        <select class="page-size" id="ipSize" onchange="changeIpSize()">
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                            <option value="100">100条/页</option>
                        </select>
                    </div>
                    <div class="page-ctrl">
                        <button class="nav-btn small" onclick="changeIpPage(-1)" id="ipPrevBtn">上一页</button>
                        <span class="page-info" id="ipPageInfo">1 / 1</span>
                        <button class="nav-btn small" onclick="changeIpPage(1)" id="ipNextBtn">下一页</button>
                    </div>
                </div>
            </div>

            <div id="uuids" class="card">
                <h2>UUID 配置分组</h2>
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th class="sortable" onclick="sortUuid('uuid')">UUID ↕</th>
                                <th class="sortable" onclick="sortUuid('owner')">所有者 ↕</th>
                                <th class="sortable" onclick="sortUuid('email')">邮箱 ↕</th>
                                <th class="sortable" onclick="sortUuid('status')">状态 ↕</th>
                                <th class="sortable" onclick="sortUuid('count')">节点数 ↕</th>
                                <th class="sortable" onclick="sortUuid('updated_at')">最近更新 ↕</th>
                                <th style="width: 200px;">操作</th>
                            </tr>
                        </thead>
                        <tbody id="uuidList">
                            <tr><td colspan="7" style="text-align:center; padding:20px;">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                <div class="pagination-bar" id="uuidPager" style="display: flex;">
                    <div class="page-ctrl">
                        <select class="page-size" id="uuidSize" onchange="changeUuidSize()">
                            <option value="5">5条/页</option>
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                        </select>
                    </div>
                    <div class="page-ctrl">
                        <button class="nav-btn small" onclick="changeUuidPage(-1)" id="uuidPrevBtn">上一页</button>
                        <span class="page-info" id="uuidPageInfo">1 / 1</span>
                        <button class="nav-btn small" onclick="changeUuidPage(1)" id="uuidNextBtn">下一页</button>
                    </div>
                </div>
            </div>
            
            <div id="templates" class="card">
                <h2>📋 配置模板管理</h2>
                <div style="margin-bottom: 20px; padding: 15px; background: #f8f9fa; border-radius: 6px;">
                    <p style="margin: 0; font-size: 0.9rem; color: #6b7280;">
                        配置模板用于存储常用的配置格式，方便快速生成和统一管理配置。
                    </p>
                </div>
                
                <div class="template-form" style="display: flex; gap: 10px; margin-bottom: 20px; flex-wrap: wrap;">
                    <input type="text" id="newTemplateName" placeholder="模板名称（必填）" 
                           style="flex: 1; min-width: 150px;" 
                           title="模板名称，必须唯一">
                    <input type="text" id="newTemplateUrl" placeholder="模板URL（必填）" 
                           style="flex: 2; min-width: 200px;"
                           title="配置模板的URL地址">
                    <input type="text" id="newTemplateDesc" placeholder="模板描述" 
                           style="flex: 1; min-width: 150px;"
                           title="模板描述信息，可选">
                    <div style="display: flex; align-items: center; gap: 8px; padding: 0 10px;">
                        <input type="checkbox" id="newTemplateDefault" style="width: 16px; height: 16px;">
                        <label for="newTemplateDefault" style="font-size: 0.9rem; color: #5a666d; cursor: pointer;">
                            设为默认
                        </label>
                    </div>
                    <button class="nav-btn active" onclick="addTemplate()" style="min-width: 100px;">
                        <svg class="lucide-plus" width="16" height="16"></svg>
                        添加模板
                    </button>
                </div>
                
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th class="sortable" onclick="sortTemplate('name')">模板名称 ↕</th>
                                <th class="sortable" onclick="sortTemplate('url')">模板URL ↕</th>
                                <th class="sortable" onclick="sortTemplate('description')">描述 ↕</th>
                                <th class="sortable" onclick="sortTemplate('is_default')">状态 ↕</th>
                                <th class="sortable" onclick="sortTemplate('created_at')">创建时间 ↕</th>
                                <th style="width: 200px;">操作</th>
                            </tr>
                        </thead>
                        <tbody id="templateList">
                            <tr><td colspan="6" style="text-align:center; padding:20px;">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                
                <div class="pagination-bar" id="templatePager" style="display: flex;">
                    <div class="page-ctrl">
                        <select class="page-size" id="templateSize" onchange="changeTemplateSize()">
                            <option value="5">5条/页</option>
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                        </select>
                    </div>
                    <div class="page-ctrl">
                        <button class="nav-btn small" onclick="changeTemplatePage(-1)" id="templatePrevBtn">上一页</button>
                        <span class="page-info" id="templatePageInfo">1 / 1</span>
                        <button class="nav-btn small" onclick="changeTemplatePage(1)" id="templateNextBtn">下一页</button>
                    </div>
                </div>
            </div>

            <div id="security" class="card">
                <div class="mfa-container">
                    <h2>🛡️ 账户安全</h2>
                    
                    <div class="security-section" id="mfaCurrentStatus">
                        <h3>双重验证状态</h3>
                        <div id="mfaStatusContent">加载中...</div>
                        <div id="mfaActions" style="margin-top: 15px;">
                            <button class="nav-btn active" onclick="startMfaSetup()" id="enableMfaBtn">
                                <svg class="lucide-shield-plus" width="16" height="16"></svg>
                                启用双重验证
                            </button>
                            <button class="nav-btn danger" onclick="disableMfaConfirm()" id="disableMfaBtn" style="display:none;">
                                <svg class="lucide-shield-off" width="16" height="16"></svg>
                                禁用双重验证
                            </button>
                        </div>
                    </div>
                    
                    <div id="mfaSetupSteps" style="display:none;"></div>
                </div>
            </div>

        </div>

        <footer class="footer">
            <p>Powered by <a href="https://www.yangzihome.space" target="_blank">YZFN</a></p>
        </footer>
    </div>

    <!-- 修改密码弹窗 -->
    <div class="modal-overlay" id="pwdModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 style="margin-top:0; color:#3d474d">修改管理员密码</h3>
            <div style="margin-bottom:15px;">
                <input type="password" id="oldP" placeholder="当前旧密码" autocomplete="current-password">
                <input type="password" id="newP" placeholder="新密码" autocomplete="new-password">
                <input type="password" id="confirmP" placeholder="确认新密码" autocomplete="new-password">
            </div>
            <div style="display:flex; justify-content:flex-end; gap:10px;">
                <button class="nav-btn" onclick="document.getElementById('pwdModal').style.display='none'">
                    <svg class="lucide-x" width="16" height="16"></svg>
                    取消
                </button>
                <button class="nav-btn active" onclick="changePwd()">
                    <svg class="lucide-check" width="16" height="16"></svg>
                    确认修改
                </button>
            </div>
        </div>
    </div>

    <!-- 域名编辑弹窗 -->
    <div class="modal-overlay" id="editDomModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 style="margin-top:0; color:#3d474d">编辑域名</h3>
            <input type="hidden" id="editId">
            <input type="hidden" id="editSource">
            <div style="margin-bottom:15px;">
                <label style="display:block;margin-bottom:5px;font-size:0.9rem;color:#5a666d;">域名:</label>
                <input type="text" id="editDomain">
                <label style="display:block;margin-bottom:5px;font-size:0.9rem;color:#5a666d;">备注:</label>
                <input type="text" id="editRemark">
            </div>
            <div style="display:flex; justify-content:flex-end; gap:10px;">
                <button class="nav-btn" onclick="document.getElementById('editDomModal').style.display='none'">
                    <svg class="lucide-x" width="16" height="16"></svg>
                    取消
                </button>
                <button class="nav-btn active" onclick="updateDomain()">
                    <svg class="lucide-check" width="16" height="16"></svg>
                    保存修改
                </button>
            </div>
        </div>
    </div>

    <!-- MFA 验证密码弹窗 -->
    <div class="modal-overlay" id="mfaAuthModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 style="margin-top:0; color:#3d474d">验证身份以启用双重验证</h3>
            <div style="margin-top:10px;">
                <input type="password" id="mfaAuthPassword" placeholder="请输入当前密码" autocomplete="current-password" onkeydown="if(event.key==='Enter') submitMfaAuthModal()">
                <div id="mfaAuthError" style="color:#ef4444; margin-top:8px; display:none;"></div>
            </div>
            <div style="display:flex; justify-content:flex-end; gap:10px; margin-top:20px;">
                <button class="nav-btn" onclick="document.getElementById('mfaAuthModal').style.display='none'; window._mfaAuthCallback=null;">
                    <svg class="lucide-x" width="16" height="16"></svg>
                    取消
                </button>
                <button class="nav-btn active" onclick="submitMfaAuthModal()">
                    <svg class="lucide-arrow-right" width="16" height="16"></svg>
                    继续
                </button>
            </div>
        </div>
    </div>

    <!-- 备份码显示弹窗 -->
    <div class="modal-overlay" id="backupCodesModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <div style="max-height: 80vh; overflow-y: auto;">
                <h3 style="margin-top:0; color:#3d474d; position: sticky; top: 0; background: white; padding-bottom: 10px; border-bottom: 1px solid #eee;">🔐 备份码</h3>
                
                <div style="color: #dc2626; background: #fee2e2; padding: 10px; border-radius: 6px; margin: 15px 0; font-size: 0.9rem;">
                    <strong>重要提示：</strong>
                    <p>• 请立即保存这些备份码！</p>
                    <p>• 每个备份码只能使用一次</p>
                    <p>• 保存后无法再次查看完整列表</p>
                </div>
                
                <div id="backupCodesList" class="backup-codes-box" style="font-size: 1rem; line-height: 2.5;"></div>
                
                <div style="margin-top: 20px;">
                    <button class="nav-btn active" onclick="printBackupCodes()" style="margin-right: 10px;">
                        <svg class="lucide-printer" width="16" height="16"></svg>
                        打印
                    </button>
                    <button class="nav-btn" onclick="copyBackupCodes()">
                        <svg class="lucide-clipboard-copy" width="16" height="16"></svg>
                        复制
                    </button>
                </div>
                
                <div style="margin-top: 20px; font-size: 0.9rem; color: #6b7280;">
                    <p><strong>如何使用备份码：</strong></p>
                    <p>• 登录时选择"使用备份码登录"</p>
                    <p>• 输入完整的8位备份码</p>
                    <p>• 使用后该备份码将立即失效</p>
                </div>
                
                <div style="display:flex; justify-content:flex-end; margin-top: 20px;">
                    <button class="nav-btn active" onclick="document.getElementById('backupCodesModal').style.display='none'">我已保存</button>
                </div>
            </div>
        </div>
    </div>

    <!-- UUID访问详情弹窗 -->
    <div class="modal-overlay" id="uuidDetailsModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal" style="max-width: 800px;">
            <div style="max-height: 80vh; overflow-y: auto;">
                <h3 style="margin-top:0; color:#3d474d; position: sticky; top: 0; background: white; padding-bottom: 10px; border-bottom: 1px solid #eee;">📋 UUID访问详情: <span id="modalUUID"></span></h3>
                
                <div id="uuidStats" style="margin: 15px 0; font-size: 0.9rem;">
                    <div style="display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; margin-bottom: 15px;">
                        <div style="background: #f0f4f8; padding: 10px; border-radius: 4px;">
                            <div style="font-size: 1.2rem; font-weight: bold; color: #1e40af;" id="modalTotalAccess">0</div>
                            <div style="font-size: 0.8rem; color: #6b7280;">总访问次数</div>
                        </div>
                        <div style="background: #f0f4f8; padding: 10px; border-radius: 4px;">
                            <div style="font-size: 1rem; font-weight: bold; color: #3d474d;" id="modalFirstAccess">-</div>
                            <div style="font-size: 0.8rem; color: #6b7280;">首次访问</div>
                        </div>
                        <div style="background: #f0f4f8; padding: 10px; border-radius: 4px;">
                            <div style="font-size: 1rem; font-weight: bold; color: #3d474d;" id="modalLastAccess">-</div>
                            <div style="font-size: 0.8rem; color: #6b7280;">最后访问</div>
                        </div>
                    </div>
                </div>
                
                <div style="margin: 15px 0;">
                    <h4 style="margin: 0 0 10px 0; font-size: 1rem;">最近访问记录</h4>
                    <div style="max-height: 300px; overflow-y: auto; border: 1px solid #e5e7eb; border-radius: 4px;">
                        <table class="access-detail-table">
                            <thead>
                                <tr>
                                    <th>时间</th>
                                    <th>访问类型</th>
                                    <th>客户端IP</th>
                                    <th>User Agent</th>
                                </tr>
                            </thead>
                            <tbody id="modalAccessLogs"></tbody>
                        </table>
                    </div>
                </div>
                
                <div style="display:flex; justify-content:flex-end; margin-top: 20px;">
                    <button class="nav-btn active" onclick="document.getElementById('uuidDetailsModal').style.display='none'">关闭</button>
                </div>
            </div>
        </div>
    </div>

    <!-- 模板编辑弹窗 -->
    <div class="modal-overlay" id="editTemplateModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3>✏️ 编辑模板</h3>
            <input type="hidden" id="editTemplateId">
            <div style="margin-bottom: 20px;">
                <div style="margin-bottom: 15px;">
                    <label style="display:block; margin-bottom:5px; font-size:0.9rem; color:#5a666d; font-weight: 500;">
                        模板名称 <span style="color:#ef4444;">*</span>
                    </label>
                    <input type="text" id="editTemplateName" placeholder="请输入模板名称" required>
                </div>
                
                <div style="margin-bottom: 15px;">
                    <label style="display:block; margin-bottom:5px; font-size:0.9rem; color:#5a666d; font-weight: 500;">
                        模板URL <span style="color:#ef4444;">*</span>
                    </label>
                    <input type="text" id="editTemplateUrl" placeholder="请输入模板URL" required>
                </div>
                
                <div style="margin-bottom: 15px;">
                    <label style="display:block; margin-bottom:5px; font-size:0.9rem; color:#5a666d; font-weight: 500;">
                        模板描述
                    </label>
                    <input type="text" id="editTemplateDesc" placeholder="请输入模板描述（可选）">
                </div>
                
                <div style="display: flex; align-items: center; gap: 8px; margin-top: 10px;">
                    <input type="checkbox" id="editTemplateDefault" style="width: 16px; height: 16px;">
                    <label for="editTemplateDefault" style="font-size: 0.9rem; color: #5a666d; cursor: pointer;">
                        设为默认模板（只能有一个默认模板）
                    </label>
                </div>
            </div>
            
            <div style="display:flex; justify-content:flex-end; gap:10px;">
                <button class="nav-btn" onclick="document.getElementById('editTemplateModal').style.display='none'">
                    <svg class="lucide-x" width="16" height="16"></svg>
                    取消
                </button>
                <button class="nav-btn active" onclick="updateTemplate()" id="updateTemplateBtn">
                    <svg class="lucide-check" width="16" height="16"></svg>
                    保存修改
                </button>
            </div>
        </div>
    </div>

    <!-- 编辑UUID信息弹窗 -->
    <div class="modal-overlay" id="editUuidModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 style="margin-top:0; color:#3d474d">编辑UUID信息</h3>
            <input type="hidden" id="editUuid">
            <div style="margin-bottom:15px;">
                <label style="display:block;margin-bottom:5px;font-size:0.9rem;color:#5a666d;">所属人姓名/标识:</label>
                <input type="text" id="editOwner" required>
                <label style="display:block;margin-bottom:5px;font-size:0.9rem;color:#5a666d;">邮箱地址:</label>
                <input type="email" id="editEmail" required>
                <label style="display:block;margin-bottom:5px;font-size:0.9rem;color:#5a666d;">其他联系信息:</label>
                <input type="text" id="editContactInfo">
                <label style="display:block;margin-bottom:5px;font-size:0.9rem;color:#5a666d;">状态:</label>
                <select id="editStatus">
                    <option value="active">活跃</option>
                    <option value="inactive">停用</option>
                    <option value="suspended">暂停</option>
                </select>
                <label style="display:block;margin-bottom:5px;font-size:0.9rem;color:#5a666d;">备注:</label>
                <textarea id="editUuidRemark" rows="3"></textarea>
            </div>
            <div style="display:flex; justify-content:flex-end; gap:10px;">
                <button class="nav-btn" onclick="document.getElementById('editUuidModal').style.display='none'">
                    <svg class="lucide-x" width="16" height="16"></svg>
                    取消
                </button>
                <button class="nav-btn active" onclick="saveUuidInfo()">
                    <svg class="lucide-check" width="16" height="16"></svg>
                    保存
                </button>
            </div>
        </div>
    </div>

    <script>
        const token = localStorage.getItem('token');
        if (!token) window.location.href = '/login';

        // 初始化后刷新 Lucide 图标
        function refreshLucideIcons() {
            if (typeof lucide !== 'undefined') {
                lucide.createIcons();
            }
        }

        // ============ 工具函数 ============
        function toast(message, type = 'info') { 
            const c = document.getElementById('toast-container'); 
            const d = document.createElement('div'); 
            d.className = 'toast'; 
            d.innerHTML = (type === 'error' ? '❌ ' : '✅ ') + message; 
            c.appendChild(d); 
            setTimeout(() => d.remove(), 5000); 
        }
        
        function fmtDate(timestamp) { 
            if (!timestamp || timestamp === 0) return '从未';
            const date = new Date(timestamp);
            return date.toLocaleString('zh-CN', {
                year: 'numeric',
                month: '2-digit',
                day: '2-digit',
                hour: '2-digit',
                minute: '2-digit'
            });
        }
        
        function fmtShortDate(dateStr) {
            if (!dateStr) return '';
            const date = new Date(dateStr);
            return date.toLocaleDateString('zh-CN', {
                month: '2-digit',
                day: '2-digit'
            });
        }
        
        async function api(path, method = 'GET', body) {
            try {
                const res = await fetch('/api/' + path, { 
                    method, 
                    headers: {
                        'Authorization': 'Bearer ' + token, 
                        'Content-Type': 'application/json'
                    }, 
                    body: body ? JSON.stringify(body) : null 
                });
                
                if (res.status === 401) {
                    toast('会话已过期，请重新登录', 'error');
                    setTimeout(() => logout(), 2000);
                    return null;
                }
                
                if (res.status === 403) {
                    const error = await res.json();
                    toast(error.error || '权限不足', 'error');
                    return null;
                }
                
                if (res.status === 404) {
                    toast('API端点不存在', 'error');
                    return null;
                }
                
                const data = await res.json();
                if (data.error) {
                    toast(data.error, 'error');
                    return null;
                }
                
                return data;
            } catch(e) { 
                toast('网络错误: ' + e.message, 'error'); 
                return null; 
            }
        }
        
        function logout() { 
            localStorage.clear();
            sessionStorage.clear();
            window.location.href = '/login'; 
        }

        // ============ 状态管理 ============
        let domState = { page: 1, size: 10, total: 0, sort: 'id', order: 'desc' };
        let ipState = { page: 1, size: 10, total: 0, sort: 'created_at', order: 'desc' };
        let uuidState = { page: 1, size: 10, total: 0, sort: 'updated_at', order: 'desc' };
        let templateState = { page: 1, size: 10, total: 0, sort: 'created_at', order: 'desc', loading: false };
        let autoUpdateSettings = { global_enabled: false, hostmonit_v4: true, hostmonit_v6: false, vps789: true };
        let mfaStatus = { enabled: false, last_login: 0, backup_codes: 0 };
        let currentMfaSecret = '';
        let accessChart = null;
        let currentChartType = 'all';

        // ============ 初始化 ============
        document.addEventListener('DOMContentLoaded', async function() {
            if (!token) {
                window.location.href = '/login';
                return;
            }
            
            try {
                await init(); 
                // 初始化 Lucide 图标
                if (typeof lucide !== 'undefined') {
                    lucide.createIcons();
                }
            } catch (error) {
                document.getElementById('loader').innerHTML = \`
                    <div style="text-align:center; color:#ef4444;">
                        <p>初始化失败</p>
                        <button onclick="location.reload()" class="nav-btn">重试</button>
                    </div>
                \`;
            }
        });

        async function init() {
            try {
                await loadStats();
                await loadAutoUpdateSettings();
                await checkMfaStatus();
                
                // 初始化时显示所有分页容器
                document.querySelectorAll('.pagination-bar').forEach(pager => {
                    pager.style.display = 'flex';
                });
                
                document.getElementById('loader').style.display = 'none';
                document.getElementById('app').style.display = 'flex';
                
                toast('系统加载完成');
            } catch (error) {
                console.error('初始化错误:', error);
                throw error;
            }
        }
        
        // ============ 标签页切换 ============
        function switchTab(id, btn) {
            if (!btn) {
                const activeBtn = document.querySelector('.nav-grid .nav-btn.active');
                if (activeBtn) {
                    activeBtn.classList.remove('active');
                }
                const newBtn = document.querySelector(\`.nav-btn[onclick*="switchTab('\${id}'"]\`);
                if (newBtn) {
                    newBtn.classList.add('active');
                }
            } else {
                document.querySelectorAll('.nav-grid .nav-btn').forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
            }
            
            document.querySelectorAll('.card').forEach(c => c.classList.remove('active'));
            document.getElementById(id).classList.add('active');
            
            if (id === 'dash') loadStats();
            if (id === 'dom') loadDom();
            if (id === 'ips') loadIp();
            if (id === 'uuids') loadUuid();
            if (id === 'templates') loadTemplates();
            if (id === 'security') updateSecurityTab();
            
            // 刷新 Lucide 图标
            setTimeout(refreshLucideIcons, 100);
        }

        // ============ 加载统计信息 ============
        async function loadStats() {
            const data = await api('stats');
            if (data) {
                // 基础统计
                document.getElementById('s-dom').innerText = data.domains;
                document.getElementById('s-ip').innerText = data.ips;
                document.getElementById('s-uuid').innerText = data.uuids;
                document.getElementById('s-template').innerText = data.templates;
                
                const statusText = data.autoUpdate === 1 ? 
                    '<span style="color:#10b981">已启用</span>' : 
                    '<span style="color:#ef4444">已关闭</span>';
                document.getElementById('autoUpdateStatus').innerHTML = statusText;
                
                if (data.lastExecuted > 0) {
                    document.getElementById('lastExecuted').innerText = fmtDate(data.lastExecuted);
                } else {
                    document.getElementById('lastExecuted').innerText = '从未执行';
                }
                
                // 访问统计
                if (data.access_stats && data.access_stats.success) {
                    const stats = data.access_stats;
                    document.getElementById('accessStatsGrid').style.display = 'grid';
                    
                    document.getElementById('as-total').innerText = stats.total_requests;
                    document.getElementById('as-unique').textContent = stats.unique_uuids + '个独立UUID';
                    document.getElementById('as-today').innerText = stats.today_total;
                    document.getElementById('as-today-split').innerHTML =  \`订阅:\${stats.today_subscription} | 网页:\${stats.today_apigen}\`;
                    document.getElementById('as-subscription').innerText = stats.subscription_requests;
                    document.getElementById('as-apigen').innerText = stats.api_generation_requests;
                }
                
                // 如果当前在系统概览页面，自动加载访问趋势图表
                const currentCard = document.querySelector('.card.active');
                if (currentCard && currentCard.id === 'dash') {
                    loadAccessStats();
                }
            }
        }

        // ============ 加载访问统计和图表 ============
        async function loadAccessStats() {
            const days = document.getElementById('chartDays').value;
            const data = await api(\`stats?days=\${days}\`);
            
            if (data && data.access_stats && data.access_stats.success) {
                const stats = data.access_stats;
                renderAccessChart(stats.daily_stats);
                renderPopularUUIDs(stats.popular_uuids);
                updateChartButtons();
            } else {
                toast('无法加载访问统计数据', 'error');
            }
        }
        
        function renderAccessChart(dailyStats) {
            const ctx = document.getElementById('accessChart').getContext('2d');
            
            // 处理数据
            const dates = dailyStats.map(item => fmtShortDate(item.date)).reverse();
            const totals = dailyStats.map(item => item.total).reverse();
            const subscriptions = dailyStats.map(item => item.subscription).reverse();
            const apigens = dailyStats.map(item => item.api_generation).reverse();
            const uniqueUUIDS = dailyStats.map(item => item.unique_uuids).reverse();
            
            // 销毁现有图表
            if (accessChart) {
                accessChart.destroy();
            }
            
            // 创建新图表
            let datasets = [];
            
            switch (currentChartType) {
                case 'total':
                    datasets = [{
                        label: '总访问量',
                        data: totals,
                        backgroundColor: '#3b82f6',
                        borderColor: '#2563eb',
                        borderWidth: 2,
                        tension: 0.3
                    }];
                    break;
                    
                case 'split':
                    datasets = [
                        {
                            label: '订阅访问',
                            data: subscriptions,
                            backgroundColor: '#10b981',
                            borderColor: '#059669',
                            borderWidth: 2,
                            tension: 0.3
                        },
                        {
                            label: '网页生成',
                            data: apigens,
                            backgroundColor: '#f59e0b',
                            borderColor: '#d97706',
                            borderWidth: 2,
                            tension: 0.3
                        }
                    ];
                    break;
                    
                case 'uuids':
                    datasets = [{
                        label: '活跃UUID数',
                        data: uniqueUUIDS,
                        backgroundColor: '#8b5cf6',
                        borderColor: '#7c3aed',
                        borderWidth: 2,
                        tension: 0.3
                    }];
                    break;
                    
                case 'all':
                    datasets = [
                        {
                            label: '总访问量',
                            data: totals,
                            backgroundColor: 'rgba(59, 130, 246, 0.8)',
                            borderColor: '#2563eb',
                            borderWidth: 2,
                            tension: 0.3,
                            yAxisID: 'y'
                        },
                        {
                            label: '订阅访问',
                            data: subscriptions,
                            backgroundColor: 'rgba(16, 185, 129, 0.8)',
                            borderColor: '#059669',
                            borderWidth: 2,
                            tension: 0.3,
                            yAxisID: 'y'
                        },
                        {
                            label: '网页生成',
                            data: apigens,
                            backgroundColor: 'rgba(245, 158, 11, 0.8)',
                            borderColor: '#d97706',
                            borderWidth: 2,
                            tension: 0.3,
                            yAxisID: 'y'
                        },
                        {
                            label: '活跃UUID数',
                            data: uniqueUUIDS,
                            backgroundColor: 'rgba(139, 92, 246, 0.8)',
                            borderColor: '#7c3aed',
                            borderWidth: 2,
                            tension: 0.3,
                            yAxisID: 'y2'
                        }
                    ];
                    break;
            }
            
            const isAllChart = currentChartType === 'all';
            
            accessChart = new Chart(ctx, {
                type: 'line',
                data: {
                    labels: dates,
                    datasets: datasets
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        legend: {
                            position: 'top',
                        },
                        title: {
                            display: true,
                            text: isAllChart ? '订阅访问趋势分析 - 全部指标' : '用户订阅访问趋势图'
                        },
                        tooltip: {
                            mode: 'index',
                            intersect: false,
                            callbacks: {
                                label: function(context) {
                                    let label = context.dataset.label || '';
                                    if (label) {
                                        label += ': ';
                                    }
                                    label += context.parsed.y;
                                    return label;
                                }
                            }
                        }
                    },
                    scales: {
                        x: {
                            grid: {
                                display: false
                            }
                        },
                        y: {
                            beginAtZero: true,
                            ticks: {
                                precision: 0
                            },
                            title: {
                                display: isAllChart,
                                text: isAllChart ? '访问次数' : ''
                            }
                        },
                        ...(isAllChart ? {
                            y2: {
                                beginAtZero: true,
                                position: 'right',
                                ticks: {
                                    precision: 0
                                },
                                title: {
                                    display: true,
                                    text: '活跃用户数'
                                },
                                grid: {
                                    drawOnChartArea: false
                                }
                            }
                        } : {})
                    },
                    interaction: {
                        intersect: false,
                        mode: 'index'
                    },
                    elements: {
                        point: {
                            radius: 4,
                            hoverRadius: 6
                        }
                    }
                }
            });
        }
        
        function renderPopularUUIDs(uuids) {
            const container = document.getElementById('popularUUIDsList');
            
            if (!uuids || uuids.length === 0) {
                container.innerHTML = '<div style="text-align: center; padding: 20px; color: #6b7280;">暂无访问数据</div>';
                return;
            }
            
            let html = '';
            uuids.forEach((item, index) => {
                const subscriptionPercent = item.access_count > 0 ? 
                    Math.round(item.subscription_count / item.access_count * 100) : 0;
                const apigenPercent = item.access_count > 0 ? 
                    Math.round(item.apigen_count / item.access_count * 100) : 0;
                
                html += \`
                    <div class="popular-uuids-item" onclick="showUUIDDetails('\${item.uuid}')" style="cursor: pointer;">
                        <div>
                            <span class="popular-uuids-uuid" title="\${item.uuid}">\${item.uuid}</span>
                            <div style="font-size: 0.75rem; color: #6b7280; margin-top: 2px;">
                                \${subscriptionPercent}%订阅 | \${apigenPercent}%网页
                            </div>
                        </div>
                        <div>
                            <span class="popular-uuids-count">\${item.access_count}</span>
                            <span style="font-size: 0.8rem; color: #9ca3af;">次</span>
                        </div>
                    </div>
                \`;
            });
            
            container.innerHTML = html;
            refreshLucideIcons();
        }
        
        function switchChartType(type) {
            currentChartType = type;
            updateChartButtons();
            loadAccessStats();
        }
        
        function updateChartButtons() {
            document.getElementById('chartTotalBtn').classList.toggle('active', currentChartType === 'total');
            document.getElementById('chartSplitBtn').classList.toggle('active', currentChartType === 'split');
            document.getElementById('chartUuidsBtn').classList.toggle('active', currentChartType === 'uuids');
            document.getElementById('chartAllBtn').classList.toggle('active', currentChartType === 'all');
        }
        
        async function showUUIDDetails(uuid) {
            const modal = document.getElementById('uuidDetailsModal');
            const modalUUID = document.getElementById('modalUUID');
            const modalTotalAccess = document.getElementById('modalTotalAccess');
            const modalFirstAccess = document.getElementById('modalFirstAccess');
            const modalLastAccess = document.getElementById('modalLastAccess');
            const modalAccessLogs = document.getElementById('modalAccessLogs');
            
            modalUUID.textContent = uuid;
            modalTotalAccess.textContent = '加载中...';
            modalFirstAccess.textContent = '-';
            modalLastAccess.textContent = '-';
            modalAccessLogs.innerHTML = '<tr><td colspan="4" style="text-align:center; padding:20px;">加载中...</td></tr>';
            
            modal.style.display = 'flex';
            
            const data = await api(\`stats/uuid-details?uuid=\${encodeURIComponent(uuid)}\`);
            
            if (data && data.success) {
                modalTotalAccess.textContent = data.total_access;
                modalFirstAccess.textContent = data.first_access ? fmtDate(data.first_access) : '-';
                modalLastAccess.textContent = data.last_access ? fmtDate(data.last_access) : '-';
                
                if (data.access_logs && data.access_logs.length > 0) {
                    let logsHTML = '';
                    data.access_logs.forEach(log => {
                        const typeClass = log.query_type === 'subscription' ? 'type-subscription' : 'type-apigen';
                        const typeText = log.query_type === 'subscription' ? '订阅' : '网页';
                        
                        logsHTML += \`
                            <tr>
                                <td>\${fmtDate(log.created_at)}</td>
                                <td><span class="type-badge \${typeClass}">\${typeText}</span></td>
                                <td>\${log.client_ip || '未知'}</td>
                                <td style="max-width: 200px; overflow: hidden; text-overflow: ellipsis;" title="\${log.user_agent}">\${log.user_agent || '未知'}</td>
                            </tr>
                        \`;
                    });
                    modalAccessLogs.innerHTML = logsHTML;
                } else {
                    modalAccessLogs.innerHTML = '<tr><td colspan="4" style="text-align:center; padding:20px;">暂无访问记录</td></tr>';
                }
            } else {
                toast('无法加载UUID详情', 'error');
                modalTotalAccess.textContent = '0';
                modalAccessLogs.innerHTML = '<tr><td colspan="4" style="text-align:center; padding:20px;">加载失败</td></tr>';
            }
        }

        // ============ 自动更新设置 ============
        async function loadAutoUpdateSettings() {
            const settings = await api('settings/auto-update');
            if (settings) {
                autoUpdateSettings = settings;
                
                document.getElementById('sw-global').checked = settings.global_enabled === 1;
                document.getElementById('sw-hm-v4').checked = settings.hostmonit_v4 === 1;
                document.getElementById('sw-hm-v6').checked = settings.hostmonit_v6 === 1;
                document.getElementById('sw-v7').checked = settings.vps789 === 1;
                
                const indicator = document.getElementById('globalStatusIndicator');
                const statusText = document.getElementById('globalStatusText');
                
                if (settings.global_enabled === 1) {
                    indicator.className = 'status-indicator status-on';
                    statusText.textContent = '自动更新: 已启用';
                } else {
                    indicator.className = 'status-indicator status-off';
                    statusText.textContent = '自动更新: 已关闭';
                }
            }
        }

        async function saveAutoUpdateSettings() {
            const globalEnabled = document.getElementById('sw-global').checked;
            const hostmonitV4Enabled = document.getElementById('sw-hm-v4').checked;
            const hostmonitV6Enabled = document.getElementById('sw-hm-v6').checked;
            const vps789Enabled = document.getElementById('sw-v7').checked;
            
            const res = await api('settings/auto-update', 'POST', {
                global_enabled: globalEnabled,
                hostmonit_v4: hostmonitV4Enabled,
                hostmonit_v6: hostmonitV6Enabled,
                vps789: vps789Enabled
            });
            
            if (res && res.success) {
                toast('自动更新设置已保存');
                await loadAutoUpdateSettings();
                await loadStats();
            }
        }

        // ============ MFA功能 ============
        async function checkMfaStatus() {
            const data = await api('mfa/status');
            if (data) {
                mfaStatus = {
                    enabled: data.mfa_enabled,
                    last_login: data.last_mfa_login,
                    backup_codes: data.backup_codes_remaining || 0
                };
                
                updateMfaUI();
            }
        }

        function updateMfaUI() {
            const mfaBadge = document.getElementById('mfaBadge');
            const mfaStatusSection = document.getElementById('mfaStatusSection');
            
            if (mfaStatus.enabled) {
                const lastLogin = mfaStatus.last_login ? 
                    '上次验证: ' + fmtDate(mfaStatus.last_login) : '从未验证';
                const backupCount = mfaStatus.backup_codes > 0 ? 
                    \`，剩余\${mfaStatus.backup_codes}个备份码\` : '，无备份码';
                
                mfaBadge.innerHTML = '🛡️ MFA已启用';
                mfaBadge.className = 'mfa-status-badge mfa-active';
                mfaBadge.style.display = 'inline-flex';
                
                mfaStatusSection.style.display = 'block';
                document.getElementById('mfaStatusDetails').innerHTML = \`
                    <div>双重验证: <span style="color:#10b981">已启用</span></div>
                    <div>\${lastLogin}</div>
                    <div>\${backupCount}</div>
                \`;
            } else {
                mfaBadge.innerHTML = '⚠️ MFA未启用';
                mfaBadge.className = 'mfa-status-badge mfa-inactive';
                mfaBadge.style.display = 'inline-flex';
                mfaStatusSection.style.display = 'none';
            }
            
            updateSecurityTab();
        }

        function updateSecurityTab() {
            const mfaStatusContent = document.getElementById('mfaStatusContent');
            const enableMfaBtn = document.getElementById('enableMfaBtn');
            const disableMfaBtn = document.getElementById('disableMfaBtn');
            const setupSteps = document.getElementById('mfaSetupSteps');

            if (mfaStatus.enabled) {
                const lastLogin = mfaStatus.last_login ? 
                    fmtDate(mfaStatus.last_login) : '从未';
                
                mfaStatusContent.innerHTML = \`
                    <div style="color:#10b981; font-weight:bold; margin-bottom:10px;">
                        ✅ 双重验证已启用
                    </div>
                    <div>上次双重验证登录: \${lastLogin}</div>
                    <div>剩余备份码: <span style="font-weight:bold;">\${mfaStatus.backup_codes}</span> 个</div>
                    <div style="margin-top:15px;">
                        <button class="nav-btn small active" onclick="showBackupCodes()" \${mfaStatus.backup_codes === 0 ? 'disabled' : ''}>
                            查看备份码
                        </button>
                        <button class="nav-btn small" onclick="regenerateBackupCodes()">
                            重新生成备份码
                        </button>
                    </div>
                \`;
                
                enableMfaBtn.style.display = 'none';
                disableMfaBtn.style.display = 'inline-block';
                setupSteps.style.display = 'none';
                setupSteps.innerHTML = '';
            } else {
                mfaStatusContent.innerHTML = \`
                    <div style="color:#ef4444; font-weight:bold; margin-bottom:10px;">
                        ⚠️ 双重验证未启用
                    </div>
                    <p style="font-size:0.9rem; color:#6b7280; margin:10px 0;">
                        双重验证（MFA）可为您的账户提供额外保护。
                        启用后，登录时需要密码和动态验证码。
                    </p>
                \`;
                
                enableMfaBtn.style.display = 'inline-block';
                disableMfaBtn.style.display = 'none';
            }
        }

        async function startMfaSetup() {
            const username = getCurrentUsername();
            openMfaAuthModal(async (password) => {
                if (!password) return;
                try {
                    const response = await fetch('/api/mfa/init', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({
                            username: username,
                            password: password
                        })
                    });
                    const data = await response.json();
                    if (response.ok) {
                        currentMfaSecret = data.secret;
                        showMfaSetupStep1(data);
                    } else {
                        toast(data.error || '初始化失败', 'error');
                    }
                } catch (error) {
                    toast('网络错误，请重试', 'error');
                }
            });
        }

        function showMfaSetupStep1(data) {
            const setupSteps = document.getElementById('mfaSetupSteps');
            setupSteps.style.display = 'block';
            setupSteps.innerHTML = \`
                <div class="security-step">
                    <h4>步骤 1: 输入密钥</h4>
                    <p style="font-size:0.9rem; color:#6b7280;">
                        使用身份验证器App（如Google Authenticator、Microsoft Authenticator、Authy等）：
                    </p>
                    
                    <div class="qr-code-container">
                        <div class="secret-display">
                            \${currentMfaSecret} 
                            <button class="copy-btn" onclick="copyToClipboard('\${currentMfaSecret}')">复制</button>
                        </div>
                        
                        <div class="qr-instructions">
                            <ol>
                                <li>打开身份验证器App</li>
                                <li>点击"添加账户"或"+"按钮</li>
                                <li>选择"手动输入密钥"</li>
                                <li>输入密钥：<strong>\${currentMfaSecret}</strong></li>
                                <li>账户名：<strong>\${getCurrentUsername()}</strong></li>
                                <li>类型：<strong>TOTP</strong></li>
                                <li>位数：<strong>6</strong></li>
                                <li>周期：<strong>30秒</strong></li>
                            </ol>
                        </div>
                    </div>
                    
                    <div style="text-align:center; margin:20px 0;">
                        <button class="nav-btn active" onclick="showMfaSetupStep2()">我已添加账户，继续</button>
                        <button class="nav-btn" onclick="cancelMfaSetup()">取消</button>
                    </div>
                </div>
            \`;
            
            switchTab('security');
        }

        function showMfaSetupStep2() {
            const setupSteps = document.getElementById('mfaSetupSteps');
            setupSteps.innerHTML = \`
                <div class="security-step">
                    <h4>步骤 2: 验证</h4>
                    <p style="font-size:0.9rem; color:#6b7280;">
                        请在输入框中输入身份验证器App生成的6位验证码：
                    </p>
                    
                    <div style="text-align: center; margin: 20px 0;">
                        <div style="display:flex; gap:10px; justify-content:center; margin:20px 0;">
                            <input type="text" id="verifyTOTPInput" maxlength="6" 
                                   pattern="[0-9]{6}" inputmode="numeric" 
                                   style="font-size:1.5rem; text-align:center; letter-spacing:10px; width:150px;"
                                   placeholder="000000">
                        </div>
                        
                        <div style="display:flex; gap:10px; justify-content:center;">
                            <button class="nav-btn" onclick="showMfaSetupStep1()">返回上一步</button>
                            <button class="nav-btn active" onclick="verifyMfaSetup()">验证并完成</button>
                        </div>
                    </div>
                \`;
            
            setTimeout(() => {
                document.getElementById('verifyTOTPInput').focus();
            }, 100);
        }

        async function verifyMfaSetup() {
            const totpCode = document.getElementById('verifyTOTPInput').value;
            const username = getCurrentUsername();
            
            if (!totpCode || totpCode.length !== 6) {
                toast('请输入6位验证码', 'error');
                return;
            }
            
            try {
                const response = await fetch('/api/mfa/verify-first', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        username: username,
                        totp_code: totpCode,
                        secret: currentMfaSecret
                    })
                });
                
                const data = await response.json();
                
                if (response.ok) {
                    showBackupCodes(data.backup_codes);
                    currentMfaSecret = '';
                    const setupSteps = document.getElementById('mfaSetupSteps');
                    if (setupSteps) {
                        setupSteps.style.display = 'none';
                        setupSteps.innerHTML = '';
                    }
                    await checkMfaStatus();
                    toast('双重验证已成功启用！');
                } else {
                    toast(data.error || '验证码无效，请重试', 'error');
                    document.getElementById('verifyTOTPInput').value = '';
                    document.getElementById('verifyTOTPInput').focus();
                }
            } catch (error) {
                toast('网络错误，请重试', 'error');
            }
        }

        function cancelMfaSetup() {
            const setupSteps = document.getElementById('mfaSetupSteps');
            setupSteps.style.display = 'none';
            setupSteps.innerHTML = '';
            currentMfaSecret = '';
            updateSecurityTab();
        }

        function showBackupCodes(backupCodes) {
            if (!backupCodes) {
                toast('没有备份码数据', 'error');
                return;
            }
            
            const modal = document.getElementById('backupCodesModal');
            const listContainer = document.getElementById('backupCodesList');
            
            let listHTML = '';
            backupCodes.forEach((code, index) => {
                listHTML += \`<div class="backup-code">\${code}</div>\`;
                if ((index + 1) % 5 === 0) {
                    listHTML += '<br>';
                }
            });
            
            listContainer.innerHTML = listHTML;
            modal.style.display = 'flex';
        }

        function disableMfaConfirm() {
            if (confirm('确定要禁用双重验证吗？禁用后您的账户将只有密码保护。')) {
                openMfaAuthModal(async (password) => {
                    if (!password) return;
                    disableMfa(password);
                });
            }
        }

        async function disableMfa(password) {
            const res = await api('mfa/disable', 'POST', { password });
            if (res && res.success) {
                await checkMfaStatus();
                toast('双重验证已禁用');
            }
        }

        async function regenerateBackupCodes() {
            openMfaAuthModal(async (password) => {
                if (!password) return;
                const res = await api('mfa/backup-codes/regenerate', 'POST', { password });
                if (res && res.success) {
                    showBackupCodes(res.backup_codes);
                    await checkMfaStatus();
                    toast('备份码已重新生成');
                }
            });
        }

        // ============ 通用工具函数 ============
        function getCurrentUsername() {
            try {
                const payload = JSON.parse(atob(token.split('.')[1]));
                return payload.sub || 'admin';
            } catch {
                return 'admin';
            }
        }

        function copyToClipboard(text) {
            navigator.clipboard.writeText(text).then(() => {
                toast('已复制到剪贴板');
            }).catch(err => {
                toast('复制失败', 'error');
            });
        }

        function printBackupCodes() {
            const printWindow = window.open('', '_blank');
            const codes = Array.from(document.querySelectorAll('.backup-code'))
                .map(el => el.textContent)
                .join('\\n');
            
            printWindow.document.write(\`
                <html>
                <head>
                    <title>MFA备份码 - \${getCurrentUsername()}</title>
                    <style>
                        body { font-family: Arial, sans-serif; padding: 20px; }
                        h1 { color: #333; }
                        .codes { font-family: monospace; font-size: 1.2em; line-height: 2; }
                        .code { display: inline-block; margin: 5px; padding: 5px 10px; border: 1px solid #ccc; }
                        .warning { color: #d00; background: #fee; padding: 10px; border-radius: 5px; margin: 20px 0; }
                    </style>
                </head>
                <body>
                    <h1>MFA备份码</h1>
                    <p>账户: \${getCurrentUsername()}</p>
                    <p>生成时间: \${new Date().toLocaleString()}</p>
                    
                    <div class="warning">
                        <strong>重要提示：</strong>
                        <p>• 请妥善保存此页</p>
                        <p>• 每个备份码只能使用一次</p>
                        <p>• 用完后请重新生成</p>
                    </div>
                    
                    <div class="codes">
                        \${codes.split('\\n').map(code => \`<div class="code">\${code}</div>\`).join('')}
                    </div>
                </body>
                </html>
            \`);
            printWindow.document.close();
            printWindow.print();
        }

        function copyBackupCodes() {
            const codes = Array.from(document.querySelectorAll('.backup-code'))
                .map(el => el.textContent)
                .join('\\n');
            
            copyToClipboard(codes);
        }

        // ============ Modal控制函数 ============
        function openPwdModal() {
            document.getElementById('pwdModal').style.display = 'flex';
            document.getElementById('oldP').value = '';
            document.getElementById('newP').value = '';
            document.getElementById('confirmP').value = '';
            setTimeout(()=>document.getElementById('oldP').focus(), 100);
        }

        function openMfaAuthModal(callback) {
            window._mfaAuthCallback = callback;
            const modal = document.getElementById('mfaAuthModal');
            modal.style.display = 'flex';
            document.getElementById('mfaAuthPassword').value = '';
            document.getElementById('mfaAuthError').style.display = 'none';
            setTimeout(()=>document.getElementById('mfaAuthPassword').focus(), 100);
        }

        function submitMfaAuthModal() {
            const pwd = document.getElementById('mfaAuthPassword').value;
            if (!pwd) {
                const err = document.getElementById('mfaAuthError');
                err.textContent = '请输入当前密码';
                err.style.display = '';
                return;
            }
            const cb = window._mfaAuthCallback;
            window._mfaAuthCallback = null;
            document.getElementById('mfaAuthModal').style.display = 'none';
            if (typeof cb === 'function') cb(pwd);
        }

        async function changePwd() {
            const oldP = document.getElementById('oldP').value;
            const newP = document.getElementById('newP').value;
            const confirmP = document.getElementById('confirmP').value;
            
            if (!oldP || !newP || !confirmP) {
                toast('请填写所有密码字段', 'error');
                return;
            }
            
            if (newP !== confirmP) {
                toast('新密码和确认密码不一致', 'error');
                return;
            }
            
            if (newP.length < 6) {
                toast('新密码至少需要6位', 'error');
                return;
            }
            
            const res = await api('change-password', 'POST', {
                oldPassword: oldP,
                newPassword: newP
            });
            
            if (res && res.success) {
                toast('密码修改成功');
                document.getElementById('pwdModal').style.display = 'none';
                document.getElementById('oldP').value = '';
                document.getElementById('newP').value = '';
                document.getElementById('confirmP').value = '';
            }
        }

        // ============ 域名管理 ============
        async function loadDom() {
            // 确保分页按钮可见
            const pager = document.getElementById('domPager');
            if (pager) pager.style.display = 'flex';
            
            const q = 'domains?page=' + domState.page + '&size=' + domState.size + '&sort=' + domState.sort + '&order=' + domState.order;
            const d = await api(q);
            if(d && d.success && d.data) {
                let h = '';
                d.data.forEach((i, index) => {
                    const domainSafe = (i.domain || '').replace(/"/g, '\\"');
                    const remarkSafe = (i.remark || '').replace(/"/g, '\\"');
                    const sourceSafe = (i.source || '').replace(/"/g, '\\"');
                    
                    h += '<tr>' +
                         '<td>' + i.domain + '</td>' +
                         '<td>' + (i.remark || '<span style="color:#ccc">无</span>') + '</td>' +
                         '<td><span class="hosting-badge ' + (i.source === 'Cloudflare' ? 'hosting-cloudflare' : i.source === '腾讯云EdgeOne' ? 'hosting-tencent' : '') + '">' + i.source + '</span></td>' +
                         '<td>' + fmtDate(i.created_at) + '</td>' +
                         '<td>' +
                         '<button class="nav-btn small" onclick="editD(' + index + ', \\'' + domainSafe + '\\', \\'' + remarkSafe + '\\', \\'' + sourceSafe + '\\')">编辑</button>' +
                         '<button class="nav-btn danger small" onclick="delD(' + index + ', \\'' + sourceSafe + '\\', \\'' + domainSafe + '\\')">删除</button>' +
                         '</td>' +
                         '</tr>';
                });
                document.getElementById('domList').innerHTML = h || '<tr><td colspan="5" style="text-align:center">无数据</td></tr>';
                domState.total = d.total || 0;
                updatePager('dom', domState);
                refreshLucideIcons();
            } else {
                // 即使加载失败，也更新分页信息
                updatePager('dom', domState);
            }
        }
        
        async function addDomain() {
            const d = document.getElementById('newD').value;
            const r = document.getElementById('newR').value;
            const source = document.getElementById('newSource').value;
            
            if(!d) return toast('域名不能为空', 'error');
            
            const res = await api('domains', 'POST', {
                domain: d, 
                remark: r,
                source: source
            });
            
            if(res && res.success) {
                toast('域名添加成功'); 
                document.getElementById('newD').value=''; 
                document.getElementById('newR').value=''; 
                domState.page = 1; 
                loadDom();
            }
        }
        
        async function delD(index, source, domain) { 
            if(confirm('确认删除域名 "' + domain + '"?')) { 
                const q = 'domains?page=' + domState.page + '&size=' + domState.size + '&sort=' + domState.sort + '&order=' + domState.order;
                const d = await api(q);
                if(d && d.data && d.data[index]) {
                    const domainData = d.data[index];
                    const res = await api('domains', 'DELETE', {
                        id: domainData.id,
                        source: domainData.source
                    });
                    if(res && res.success) {
                        toast('域名删除成功');
                        loadDom(); 
                    }
                } else {
                    toast('无法找到要删除的域名', 'error');
                }
            } 
        }
        
        function editD(index, domain, remark, source) { 
            document.getElementById('editId').value = index; 
            document.getElementById('editDomain').value = domain; 
            document.getElementById('editRemark').value = remark;
            document.getElementById('editSource').value = source;
            document.getElementById('editDomModal').style.display = 'flex'; 
            setTimeout(()=>document.getElementById('editDomain').focus(), 100);
        }
        
        async function updateDomain() {
            const index = document.getElementById('editId').value; 
            const domain = document.getElementById('editDomain').value; 
            const remark = document.getElementById('editRemark').value;
            const source = document.getElementById('editSource').value;
            
            if(!domain) return toast('域名不能为空', 'error');
            
            const res = await api('domains', 'PUT', { 
                id: index,
                domain, 
                remark,
                source
            });
            
            if(res && res.success) { 
                toast('域名修改成功'); 
                document.getElementById('editDomModal').style.display = 'none'; 
                loadDom(); 
            }
        }
        
        function changeDomPage(d) { changePage('dom', d, domState, loadDom); }
        function changeDomSize() { changeSize('dom', domState, loadDom); }
        function sortDom(f) { changeSort(f, domState, loadDom); }

        // ============ IP管理 ============
        async function loadIp() {
            // 确保分页按钮可见
            const pager = document.getElementById('ipPager');
            if (pager) pager.style.display = 'flex';
            
            const q = \`ips?page=\${ipState.page}&size=\${ipState.size}&sort=\${ipState.sort}&order=\${ipState.order}\`;
            const d = await api(q);
            if(d && d.success && d.data) {
                let h = '';
                d.data.forEach(i => {
                    let sourceDisplay = (i.source || 'unknown').split('|').map(s => {
                        if (s === 'hostmonit_v4') return 'HostMonit IPv4';
                        if (s === 'hostmonit_v6') return 'HostMonit IPv6';
                        if (s === 'vps789') return 'Vps789';
                        return s;
                    }).join(' | ');

                    h += \`<tr>
                        <td>\${i.ip}</td>
                        <td>\${i.ip_type}</td>
                        <td>\${i.carrier}</td>
                        <td>\${sourceDisplay}</td>
                        <td>\${fmtDate(i.created_at)}</td>
                        <td><button class="nav-btn danger small" onclick="delI('\${i.ip}')">删除</button></td>
                    </tr>\`;
                });
                document.getElementById('ipList').innerHTML = h || '<tr><td colspan="6" style="text-align:center">无数据</td></tr>';
                ipState.total = d.total || 0;
                updatePager('ip', ipState);
                refreshLucideIcons();
            } else {
                // 即使加载失败，也更新分页信息
                updatePager('ip', ipState);
            }
        }
        
        async function refreshIps() {
            const globalEnabled = document.getElementById('sw-global').checked;
            const hmV4 = document.getElementById('sw-hm-v4').checked; 
            const hmV6 = document.getElementById('sw-hm-v6').checked;
            const v7 = document.getElementById('sw-v7').checked;

            if (!globalEnabled) {
                toast('自动更新主开关已禁用，无法执行立即更新', 'warning');
                return;
            }
            if (!hmV4 && !hmV6 && !v7) {
                toast('请至少启用一个 IP 来源接口！', 'error');
                return;
            }

            toast('IP更新任务已开始...', 'info');
            
            try {
                const res = await api('ips/update', 'POST', {
                    global_enabled: globalEnabled,
                    hostmonit_v4: hmV4, 
                    hostmonit_v6: hmV6,
                    vps789: v7 
                });
                
                if (res && res.success) {
                    toast('更新完成: ' + res.message); 
                    ipState.page = 1; 
                    loadIp(); 
                    loadStats();
                } else if (res) {
                    toast('更新失败: ' + (res.message || '未知错误'), 'error'); 
                }
            } catch (error) {
                toast('网络错误: ' + error.message, 'error');
            }
        }
        
        async function delI(ip) { 
            if(confirm('确认删除此 IP?')) { 
                await api('ips', 'DELETE', {ip}); 
                loadIp(); 
            } 
        }
        
        function changeIpPage(d) { changePage('ip', d, ipState, loadIp); }
        function changeIpSize() { changeSize('ip', ipState, loadIp); }
        function sortIp(f) { changeSort(f, ipState, loadIp); }

        // ============ UUID管理 ============
        async function loadUuid() {
            // 确保分页按钮可见
            const pager = document.getElementById('uuidPager');
            if (pager) pager.style.display = 'flex';
            
            const q = \`uuids?page=\${uuidState.page}&size=\${uuidState.size}&sort=\${uuidState.sort}&order=\${uuidState.order}\`;
            const d = await api(q);
            if(d && d.success && d.data) {
                let h = '';
                d.data.forEach(i => {
                    // 处理所有者信息（可能为空）
                    const owner = i.owner || '<span style="color:#ccc">未设置</span>';
                    const email = i.email || '<span style="color:#ccc">未设置</span>';
                    const status = i.status || 'active';
                    
                    // 状态显示样式
                    let statusBadge = '';
                    if (status === 'active') {
                        statusBadge = '<span style="background:#10b981; color:white; padding:2px 8px; border-radius:12px; font-size:0.8rem;">活跃</span>';
                    } else if (status === 'inactive') {
                        statusBadge = '<span style="background:#6b7280; color:white; padding:2px 8px; border-radius:12px; font-size:0.8rem;">停用</span>';
                    } else if (status === 'suspended') {
                        statusBadge = '<span style="background:#ef4444; color:white; padding:2px 8px; border-radius:12px; font-size:0.8rem;">暂停</span>';
                    } else {
                        statusBadge = \`<span style="background:#ccc; color:white; padding:2px 8px; border-radius:12px; font-size:0.8rem;">\${status}</span>\`;
                    }
                    
                    const safeUUID = (i.uuid || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeOwner = (i.owner || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeEmail = (i.email || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeContactInfo = (i.contact_info || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeRemark = (i.remark || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeStatus = (i.status || 'active').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    
                    h += \`
                    <tr>
                        <td style="font-family:monospace; font-size:0.85rem;">\${i.uuid}</td>
                        <td>\${owner}</td>
                        <td>\${email}</td>
                        <td>\${statusBadge}</td>
                        <td>\${i.count} 个配置</td>
                        <td>\${fmtDate(i.updated_at)}</td>
                        <td>
                            <div style="display:flex; gap:5px;">
                                <button class="nav-btn small" onclick="editUuidInfo('\${safeUUID}', '\${safeOwner}', '\${safeEmail}', '\${safeContactInfo}', '\${safeStatus}', '\${safeRemark}')">编辑信息</button>
                                <button class="nav-btn danger small" onclick="delU('\${i.uuid}')">删除整组</button>
                            </div>
                        </td>
                    </tr>\`;
                });
                document.getElementById('uuidList').innerHTML = h || '<tr><td colspan="7" style="text-align:center">无数据</td></tr>';
                uuidState.total = d.total || 0;
                updatePager('uuid', uuidState);
                refreshLucideIcons();
            } else {
                // 即使加载失败，也更新分页信息
                document.getElementById('uuidList').innerHTML = '<tr><td colspan="7" style="text-align:center; padding:20px;">加载失败，请刷新重试</td></tr>';
                updatePager('uuid', uuidState);
            }
        }
        
        async function delU(uuid) { 
            if(confirm('确认删除此UUID的所有配置?')) { 
                const res = await api('uuids', 'DELETE', {uuid}); 
                if(res && res.success) {
                    toast('UUID组删除成功');
                    loadUuid();
                }
            } 
        }
        
        function editUuidInfo(uuid, owner, email, contactInfo, status, remark) {
            document.getElementById('editUuid').value = uuid;
            document.getElementById('editOwner').value = owner;
            document.getElementById('editEmail').value = email;
            document.getElementById('editContactInfo').value = contactInfo;
            document.getElementById('editStatus').value = status;
            document.getElementById('editUuidRemark').value = remark;
            document.getElementById('editUuidModal').style.display = 'flex';
        }
        
        async function saveUuidInfo() {
            const uuid = document.getElementById('editUuid').value;
            const owner = document.getElementById('editOwner').value.trim();
            const email = document.getElementById('editEmail').value.trim();
            const contactInfo = document.getElementById('editContactInfo').value.trim();
            const status = document.getElementById('editStatus').value;
            const remark = document.getElementById('editUuidRemark').value.trim();

            if (!owner) {
                toast('所属人姓名/标识不能为空', 'error');
                return;
            }
            if (!email) {
                toast('邮箱地址不能为空', 'error');
                return;
            }

            const res = await api('uuids', 'POST', {
                uuid,
                owner,
                email,
                contact_info: contactInfo,
                status,
                remark
            });

            if (res && res.success) {
                toast('UUID信息保存成功');
                document.getElementById('editUuidModal').style.display = 'none';
                loadUuid();
            }
        }
        
        function changeUuidPage(d) { changePage('uuid', d, uuidState, loadUuid); }
        function changeUuidSize() { changeSize('uuid', uuidState, loadUuid); }
        function sortUuid(f) { changeSort(f, uuidState, loadUuid); }

        // ============ 模板管理 ============
        async function loadTemplates() {
            if (templateState.loading) return;
            templateState.loading = true;
            
            const q = \`templates?page=\${templateState.page}&size=\${templateState.size}&sort=\${templateState.sort}&order=\${templateState.order}\`;
            const list = document.getElementById('templateList');
            
            try {
                const d = await api(q);
                if (d && d.success) {
                    if (d.data && d.data.length > 0) {
                        let html = '';
                        d.data.forEach(i => {
                            const createdAt = fmtDate(i.created_at);
                            const defaultBadge = i.is_default === 1 ? 
                                '<span class="template-badge badge-default">默认</span>' : 
                                '<span class="template-badge badge-normal">普通</span>';
                            
                            const description = i.description ? i.description : '<span style="color:#ccc">无描述</span>';
                            
                            const safeName = (i.name || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                            const safeUrl = (i.url || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                            const safeDesc = (i.description || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                            
                            html += \`<tr>
                                <td><strong>\${i.name}</strong></td>
                                <td class="template-url-cell" title="\${i.url}">\${i.url}</td>
                                <td>\${description}</td>
                                <td>\${defaultBadge}</td>
                                <td>\${createdAt}</td>
                                <td>
                                    <div style="display: flex; gap: 5px; flex-wrap: wrap;">
                                        <button class="nav-btn small" onclick="editTemplate(\${i.id}, '\${safeName}', '\${safeUrl}', '\${safeDesc}', \${i.is_default})">
                                            编辑
                                        </button>
                                        \${i.is_default === 0 ? 
                                            \`<button class="nav-btn small" onclick="setDefaultTemplate(\${i.id})" title="设为默认模板">
                                                设为默认
                                            </button>\` : 
                                            ''
                                        }
                                        <button class="nav-btn danger small" onclick="deleteTemplate(\${i.id})" title="删除模板">
                                            删除
                                        </button>
                                    </div>
                                </td>
                            </tr>\`;
                        });
                        list.innerHTML = html;
                        updatePager('template', templateState);
                        refreshLucideIcons();
                    } else {
                        list.innerHTML = '<tr><td colspan="6" style="text-align:center; padding:30px; color:#89949B;">暂无模板数据，请添加第一个模板</td></tr>';
                        updatePager('template', templateState);
                    }
                } else {
                    list.innerHTML = '<tr><td colspan="6" style="text-align:center; padding:20px; color:#ef4444;">加载失败，请刷新重试</td></tr>';
                    updatePager('template', templateState);
                }
            } catch (error) {
                console.error('加载模板失败:', error);
                list.innerHTML = '<tr><td colspan="6" style="text-align:center; padding:20px; color:#ef4444;">加载失败：' + error.message + '</td></tr>';
                updatePager('template', templateState);
            } finally {
                templateState.loading = false;
            }
        }

        async function addTemplate() {
            const name = document.getElementById('newTemplateName').value.trim();
            const url = document.getElementById('newTemplateUrl').value.trim();
            const desc = document.getElementById('newTemplateDesc').value.trim();
            const isDefault = document.getElementById('newTemplateDefault').checked ? 1 : 0;
            
            if (!name) {
                toast('请输入模板名称', 'error');
                document.getElementById('newTemplateName').focus();
                return;
            }
            
            if (!url) {
                toast('请输入模板URL', 'error');
                document.getElementById('newTemplateUrl').focus();
                return;
            }
            
            if (!url.startsWith('http://') && !url.startsWith('https://')) {
                toast('URL必须以 http:// 或 https:// 开头', 'error');
                document.getElementById('newTemplateUrl').focus();
                return;
            }
            
            try {
                const response = await api('templates', 'POST', {
                    name,
                    url,
                    description: desc,
                    is_default: isDefault
                });
                
                if (response && response.success) {
                    toast(response.message || '模板添加成功');
                    
                    document.getElementById('newTemplateName').value = '';
                    document.getElementById('newTemplateUrl').value = '';
                    document.getElementById('newTemplateDesc').value = '';
                    document.getElementById('newTemplateDefault').checked = false;
                    
                    templateState.page = 1;
                    await loadTemplates();
                }
            } catch (error) {
                console.error('添加模板失败:', error);
            }
        }

        function editTemplate(id, name, url, desc, isDefault) {
            document.getElementById('editTemplateId').value = id;
            document.getElementById('editTemplateName').value = name;
            document.getElementById('editTemplateUrl').value = url;
            document.getElementById('editTemplateDesc').value = desc;
            document.getElementById('editTemplateDefault').checked = isDefault === 1;
            
            const modal = document.getElementById('editTemplateModal');
            modal.style.display = 'flex';
            
            setTimeout(() => {
                document.getElementById('editTemplateName').focus();
            }, 100);
        }

        async function updateTemplate() {
            const id = document.getElementById('editTemplateId').value;
            const name = document.getElementById('editTemplateName').value.trim();
            const url = document.getElementById('editTemplateUrl').value.trim();
            const desc = document.getElementById('editTemplateDesc').value.trim();
            const isDefault = document.getElementById('editTemplateDefault').checked ? 1 : 0;
            
            if (!name) {
                toast('请输入模板名称', 'error');
                document.getElementById('editTemplateName').focus();
                return;
            }
            
            if (!url) {
                toast('请输入模板URL', 'error');
                document.getElementById('editTemplateUrl').focus();
                return;
            }
            
            if (!url.startsWith('http://') && !url.startsWith('https://')) {
                toast('URL必须以 http:// 或 https:// 开头', 'error');
                document.getElementById('editTemplateUrl').focus();
                return;
            }
            
            const btn = document.getElementById('updateTemplateBtn');
            const originalText = btn.innerHTML;
            
            try {
                btn.disabled = true;
                btn.innerHTML = '保存中...';
                
                const response = await api('templates', 'PUT', {
                    id,
                    name,
                    url,
                    description: desc,
                    is_default: isDefault
                });
                
                if (response && response.success) {
                    toast(response.message || '模板更新成功');
                    document.getElementById('editTemplateModal').style.display = 'none';
                    await loadTemplates();
                }
            } catch (error) {
                console.error('更新模板失败:', error);
            } finally {
                btn.disabled = false;
                btn.innerHTML = originalText;
            }
        }

        async function deleteTemplate(id) {
            if (!confirm('确定要删除这个模板吗？此操作不可撤销。')) {
                return;
            }
            
            try {
                const response = await api('templates', 'DELETE', { id });
                
                if (response && response.success) {
                    toast(response.message || '模板删除成功');
                    
                    if (templateState.total > 0) {
                        const maxPage = Math.ceil((templateState.total - 1) / templateState.size);
                        if (templateState.page > maxPage) {
                            templateState.page = maxPage > 0 ? maxPage : 1;
                        }
                    }
                    
                    await loadTemplates();
                }
            } catch (error) {
                console.error('删除模板失败:', error);
            }
        }

        async function setDefaultTemplate(id) {
            try {
                const response = await api('templates/set-default', 'POST', { id });
                
                if (response && response.success) {
                    toast(response.message || '默认模板设置成功');
                    await loadTemplates();
                }
            } catch (error) {
                console.error('设置默认模板失败:', error);
            }
        }

        function changeTemplatePage(d) { changePage('template', d, templateState, loadTemplates); }
        function changeTemplateSize() { changeSize('template', templateState, loadTemplates); }
        function sortTemplate(f) { changeSort(f, templateState, loadTemplates); }

        // ============ 通用分页和排序函数 ============
        function updatePager(prefix, state) {
            const pager = document.getElementById(\`\${prefix}Pager\`);
            if (!pager) return;
            
            const maxPage = Math.ceil(state.total / state.size) || 1;
            const currentPage = state.page;
            
            document.getElementById(\`\${prefix}PageInfo\`).innerText = \`\${currentPage} / \${maxPage} (共\${state.total}条)\`;
            
            const prevBtn = document.getElementById(\`\${prefix}PrevBtn\`);
            const nextBtn = document.getElementById(\`\${prefix}NextBtn\`);
            
            if (prevBtn) {
                prevBtn.disabled = currentPage <= 1;
                prevBtn.style.opacity = currentPage <= 1 ? '0.5' : '1';
                prevBtn.style.cursor = currentPage <= 1 ? 'not-allowed' : 'pointer';
            }
            
            if (nextBtn) {
                nextBtn.disabled = currentPage >= maxPage;
                nextBtn.style.opacity = currentPage >= maxPage ? '0.5' : '1';
                nextBtn.style.cursor = currentPage >= maxPage ? 'not-allowed' : 'pointer';
            }
            
            const sizeSelect = document.getElementById(\`\${prefix}Size\`);
            if (sizeSelect) sizeSelect.value = state.size;
            
            // 始终显示分页栏
            pager.style.display = 'flex';
            
            // 如果只有一页或没有数据，隐藏翻页按钮但保留分页信息
            if (maxPage <= 1) {
                if (prevBtn) prevBtn.style.display = 'none';
                if (nextBtn) nextBtn.style.display = 'none';
                document.getElementById(\`\${prefix}PageInfo\`).innerText = \`共\${state.total}条\`;
            } else {
                if (prevBtn) prevBtn.style.display = 'inline-flex';
                if (nextBtn) nextBtn.style.display = 'inline-flex';
            }
        }
        
        function changePage(prefix, delta, state, callback) {
            const maxPage = Math.ceil(state.total / state.size) || 1;
            const newPage = state.page + delta;
            
            if (newPage > 0 && newPage <= maxPage) {
                state.page = newPage;
                if (callback) callback();
                
                const card = document.getElementById(prefix === 'template' ? 'templates' : prefix);
                if (card) {
                    const tableContainer = card.querySelector('.table-container');
                    if (tableContainer) {
                        tableContainer.scrollIntoView({ behavior: 'smooth', block: 'start' });
                    }
                }
            }
        }
        
        function changeSize(prefix, state, callback) {
            const newSize = parseInt(document.getElementById(\`\${prefix}Size\`).value);
            if (newSize > 0 && newSize <= 100) {
                state.size = newSize;
                state.page = 1;
                if (callback) callback();
            }
        }
        
        function changeSort(field, state, callback) {
            if (state.sort === field) {
                state.order = state.order === 'asc' ? 'desc' : 'asc';
            } else {
                state.sort = field;
                state.order = 'desc';
            }
            state.page = 1;
            if (callback) callback();
        }

        // ============ 关闭Modal函数 ============
        function closeAllModals() {
            document.querySelectorAll('.modal-overlay').forEach(modal => {
                modal.style.display = 'none';
            });
        }

        document.querySelectorAll('.modal-overlay').forEach(modal => {
            modal.addEventListener('click', function(e) {
                if (e.target === this) {
                    this.style.display = 'none';
                }
            });
        });

        // ============ 键盘快捷键 ============
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape') {
                closeAllModals();
            }
            
            if (e.ctrlKey && e.key === 's') {
                e.preventDefault();
                const currentActiveCard = document.querySelector('.card.active');
                if (currentActiveCard && currentActiveCard.id === 'ips') {
                    saveAutoUpdateSettings();
                }
            }
        });
    </script>
</body>
</html>
`;

// =================================================================
//  WORKER ENTRY POINT (主入口)
// =================================================================

export default {
    async fetch(request, env, ctx) {
        const url = new URL(request.url);

        // 确保数据库设置已初始化
        ctx.waitUntil(initializeDatabaseSettings(env));

        // 处理API请求
        if (url.pathname.startsWith('/api')) {
            return await handleApi(request, env);
        }

        // 处理静态页面
        if (url.pathname === '/login') {
            return new Response(loginHtml, {
                headers: {
                    'Content-Type': 'text/html;charset=UTF-8',
                    'Cache-Control': CACHE_CONTROL
                }
            });
        }

        // 默认返回管理页面
        return new Response(adminHtml, {
            headers: {
                'Content-Type': 'text/html;charset=UTF-8',
                'Cache-Control': CACHE_CONTROL
            }
        });
    },

    // 添加定时任务，自动执行IP更新
    async scheduled(event, env, ctx) {
        ctx.waitUntil((async () => {
            try {
                console.log('开始定时IP更新任务...');
                await runIpUpdateTask(env);
                console.log('定时IP更新任务完成');
            } catch (error) {
                console.error('定时IP更新任务失败:', error.message);
            }
        })());
    }
};
