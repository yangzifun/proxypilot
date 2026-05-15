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

function b64ToUtf8(str) {
    try {
        return decodeURIComponent(atob(str).split('').map(c =>
            '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2)
        ).join(''));
    } catch (e) {
        throw new Error('无效的 Base64 字符串，无法解码 VMess 配置。');
    }
}

function getProtocol(configStr) {
    if (!configStr || typeof configStr !== 'string') return 'unknown';

    const protocolPatterns = [
        { prefix: 'vmess://', type: 'vmess' },
        { prefix: 'vless://', type: 'vless' },
        { prefix: 'trojan://', type: 'trojan' },
        { prefix: 'ss://', type: 'shadowsocks' },
        { prefix: 'hysteria2://', type: 'hysteria2' },
        { prefix: 'tuic://', type: 'tuic' },
        { prefix: 'anytls://', type: 'anytls' },
        { prefix: 'any-reality://', type: 'any-reality' },
        { prefix: 'anyreality://', type: 'anyreality' },
        { prefix: 'reality://', type: 'reality' },
        { prefix: 'socks5://', type: 'socks5' },
        { prefix: 'socks://', type: 'socks5' }
    ];

    const normalized = configStr.toLowerCase();
    const matched = protocolPatterns.find(pattern => normalized.startsWith(pattern.prefix));
    return matched ? matched.type : 'unknown';
}

function extractRemarkFromConfig(configStr, protocol) {
    try {
        if (protocol === 'vmess') {
            const vmessObj = JSON.parse(b64ToUtf8(configStr.substring(8)));
            return vmessObj.ps || vmessObj.remark || null;
        }

        if (['vless', 'trojan', 'hysteria2', 'tuic', 'anytls', 'socks5',
            'any-reality', 'anyreality', 'reality', 'shadowsocks'].includes(protocol)) {
            const url = new URL(configStr);
            if (url.hash) return decodeURIComponent(url.hash.substring(1));
        }
    } catch (e) { }
    return null;
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
                config_data TEXT,
                protocol TEXT,
                remark TEXT,
                domain_hosting TEXT DEFAULT 'Cloudflare',
                created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
                updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
            );
        `).run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_uuid ON configs (uuid);').run();

        const configInfo = await env.DB.prepare('PRAGMA table_info(configs)').all();
        const configColumns = (configInfo.results || []).map(c => c.name);
        const configMigrations = [
            ['config_data', 'ALTER TABLE configs ADD COLUMN config_data TEXT'],
            ['protocol', 'ALTER TABLE configs ADD COLUMN protocol TEXT'],
            ['remark', 'ALTER TABLE configs ADD COLUMN remark TEXT'],
            ['domain_hosting', "ALTER TABLE configs ADD COLUMN domain_hosting TEXT DEFAULT 'Cloudflare'"]
        ];
        for (const [column, sql] of configMigrations) {
            if (!configColumns.includes(column)) {
                await env.DB.prepare(sql).run();
            }
        }
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_protocol ON configs (protocol);').run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_domain_hosting ON configs (domain_hosting);').run();

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

async function fetchConfigsByUuidFromDB(uuid, env, options = {}) {
    const db = env.DB;
    if (!db) return { results: [], total: 0, page: 1, pageSize: 10, totalPages: 0 };

    try {
        let conditions = ['uuid = ?', "config_data IS NOT NULL", "config_data != ''"];
        const params = [uuid];

        if (options.protocol && options.protocol !== 'all') {
            conditions.push('protocol = ?');
            params.push(options.protocol);
        }

        if (options.domain_hosting && options.domain_hosting !== 'all') {
            conditions.push('domain_hosting = ?');
            params.push(options.domain_hosting);
        }

        const whereClause = 'WHERE ' + conditions.join(' AND ');
        const totalResult = await db.prepare(`SELECT COUNT(*) as total FROM configs ${whereClause}`).bind(...params).first();
        const total = totalResult?.total || 0;
        const page = Math.max(parseInt(options.page) || 1, 1);
        const pageSize = Math.min(Math.max(parseInt(options.pageSize) || 10, 1), 100);
        const offset = (page - 1) * pageSize;

        const { results } = await db.prepare(`
            SELECT id, uuid, config_data, protocol, remark, domain_hosting, created_at, updated_at
            FROM configs
            ${whereClause}
            ORDER BY id ASC
            LIMIT ? OFFSET ?
        `).bind(...params, pageSize, offset).all();

        return {
            results: results || [],
            total,
            page,
            pageSize,
            totalPages: Math.ceil(total / pageSize) || 1
        };
    } catch (e) {
        console.error(`获取UUID ${uuid} 配置失败:`, e.message);
        return { results: [], total: 0, page: 1, pageSize: 10, totalPages: 0 };
    }
}

async function ensureConfigManagerSchema(env) {
    await env.DB.prepare(`
        CREATE TABLE IF NOT EXISTS configs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            uuid TEXT NOT NULL,
            ip TEXT,
            domain TEXT,
            port INTEGER,
            meta TEXT,
            config_data TEXT,
            protocol TEXT,
            remark TEXT,
            domain_hosting TEXT DEFAULT 'Cloudflare',
            created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000),
            updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000)
        )
    `).run();

    const info = await env.DB.prepare('PRAGMA table_info(configs)').all();
    const columns = (info.results || []).map(c => c.name);
    const migrations = [
        ['config_data', 'ALTER TABLE configs ADD COLUMN config_data TEXT'],
        ['protocol', 'ALTER TABLE configs ADD COLUMN protocol TEXT'],
        ['remark', 'ALTER TABLE configs ADD COLUMN remark TEXT'],
        ['domain_hosting', "ALTER TABLE configs ADD COLUMN domain_hosting TEXT DEFAULT 'Cloudflare'"]
    ];

    for (const [column, sql] of migrations) {
        if (!columns.includes(column)) await env.DB.prepare(sql).run();
    }

    await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_uuid ON configs (uuid);').run();
    await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_protocol ON configs (protocol);').run();
    await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_domain_hosting ON configs (domain_hosting);').run();
}

async function fetchUuidStatsForManager(uuid, env, days = 30) {
    try {
        const startDate = new Date();
        startDate.setDate(startDate.getDate() - days);
        const startDateStr = startDate.toISOString().split('T')[0];
        const today = new Date().toISOString().split('T')[0];

        const totalStats = await env.DB.prepare(`
            SELECT COUNT(*) as total,
                   SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription_total,
                   SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as apigen_total,
                   MIN(created_at) as first_access,
                   MAX(created_at) as last_access
            FROM config_access_logs
            WHERE uuid = ?
        `).bind(uuid).first();

        const todayStats = await env.DB.prepare(`
            SELECT COUNT(*) as total
            FROM config_access_logs
            WHERE uuid = ? AND DATE(created_at) = ?
        `).bind(uuid, today).first();

        const dailyStats = await env.DB.prepare(`
            SELECT DATE(created_at) as date,
                   COUNT(*) as total,
                   SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription,
                   SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as api_generation
            FROM config_access_logs
            WHERE uuid = ? AND DATE(created_at) >= ?
            GROUP BY DATE(created_at)
            ORDER BY date DESC
        `).bind(uuid, startDateStr).all();

        const recentLogs = await env.DB.prepare(`
            SELECT query_type, client_ip, user_agent, created_at
            FROM config_access_logs
            WHERE uuid = ?
            ORDER BY created_at DESC
            LIMIT 30
        `).bind(uuid).all();

        return {
            success: true,
            uuid,
            total_access: totalStats?.total || 0,
            today_access: todayStats?.total || 0,
            subscription_total: totalStats?.subscription_total || 0,
            api_generation_total: totalStats?.apigen_total || 0,
            first_access: totalStats?.first_access,
            last_access: totalStats?.last_access,
            daily_stats: dailyStats?.results || [],
            recent_logs: recentLogs?.results || []
        };
    } catch (e) {
        console.error(`获取UUID ${uuid} 管理统计失败:`, e.message);
        return { success: false, error: e.message };
    }
}

function normalizeDomainHosting(domainHosting) {
    const validDomainHostings = [
        'Cloudflare', '阿里ESA', '腾讯Edgeone', '腾讯云EdgeOne', 'AWS Cloudfront',
        'Gcore', 'Fastly', 'CacheFly', 'LightCDN', 'Vercel', 'Netlify',
        '无', '其他'
    ];
    return validDomainHostings.includes(domainHosting) ? domainHosting : 'Cloudflare';
}

async function handleUuidConfigs(req, env, method) {
    try {
        await ensureConfigManagerSchema(env);
        const url = new URL(req.url);

        if (method === 'GET') {
            const uuid = url.searchParams.get('uuid');
            if (!uuid) return jsonResponse({ success: false, error: 'UUID不能为空' }, 400);

            const result = await fetchConfigsByUuidFromDB(uuid, env, {
                page: url.searchParams.get('page') || 1,
                pageSize: url.searchParams.get('size') || 10,
                protocol: url.searchParams.get('protocol') || 'all',
                domain_hosting: url.searchParams.get('domain_hosting') || 'all'
            });

            return jsonResponse({
                success: true,
                uuid,
                data: result.results,
                total: result.total,
                page: result.page,
                size: result.pageSize,
                totalPages: result.totalPages
            });
        }

        if (method === 'POST') {
            const { uuid, config_data, domain_hosting = 'Cloudflare' } = await req.json();
            if (!uuid || !config_data) return jsonResponse({ success: false, error: 'UUID和配置内容不能为空' }, 400);

            const lines = config_data.split('\n').map(line => line.trim()).filter(Boolean);
            const stmts = [];
            const now = Date.now();
            const hostingValue = normalizeDomainHosting(domain_hosting);
            let skipped = 0;

            for (const line of lines) {
                const protocol = getProtocol(line);
                if (protocol === 'unknown') {
                    skipped++;
                    continue;
                }

                const existing = await env.DB.prepare(
                    'SELECT id FROM configs WHERE uuid = ? AND config_data = ?'
                ).bind(uuid, line).first();
                if (existing) {
                    skipped++;
                    continue;
                }

                const remark = extractRemarkFromConfig(line, protocol);
                stmts.push(env.DB.prepare(`
                    INSERT INTO configs (uuid, config_data, protocol, remark, domain_hosting, created_at, updated_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?)
                `).bind(uuid, line, protocol, remark, hostingValue, now, now));
            }

            if (stmts.length === 0) {
                return jsonResponse({ success: false, error: skipped > 0 ? '没有可新增的有效配置' : '无有效配置' }, 400);
            }

            await executeInChunks(stmts);
            return jsonResponse({ success: true, inserted: stmts.length, skipped });
        }

        if (method === 'PUT') {
            const { id, config_data, domain_hosting = 'Cloudflare' } = await req.json();
            if (!id || !config_data) return jsonResponse({ success: false, error: 'ID和配置内容不能为空' }, 400);

            const protocol = getProtocol(config_data.trim());
            if (protocol === 'unknown') return jsonResponse({ success: false, error: '不支持的配置格式' }, 400);

            const remark = extractRemarkFromConfig(config_data.trim(), protocol);
            await env.DB.prepare(`
                UPDATE configs
                SET config_data = ?, protocol = ?, remark = ?, domain_hosting = ?, updated_at = ?
                WHERE id = ?
            `).bind(config_data.trim(), protocol, remark, normalizeDomainHosting(domain_hosting), Date.now(), parseInt(id)).run();

            return jsonResponse({ success: true, message: '配置更新成功' });
        }

        if (method === 'DELETE') {
            const { id, uuid } = await req.json();
            if (id) {
                await env.DB.prepare('DELETE FROM configs WHERE id = ?').bind(parseInt(id)).run();
                return jsonResponse({ success: true, message: '配置删除成功' });
            }

            if (uuid) {
                await env.DB.prepare('DELETE FROM configs WHERE uuid = ?').bind(uuid).run();
                return jsonResponse({ success: true, message: '分组配置已清空' });
            }

            return jsonResponse({ success: false, error: 'ID或UUID不能为空' }, 400);
        }

        return jsonResponse({ success: false, error: '不支持的请求方法' }, 405);
    } catch (error) {
        console.error('handleUuidConfigs error:', error.message);
        return jsonResponse({ success: false, error: '服务器错误: ' + error.message }, 500);
    }
}

async function handleUuidConfigStats(req, env) {
    try {
        const url = new URL(req.url);
        const uuid = url.searchParams.get('uuid');
        const days = parseInt(url.searchParams.get('days')) || 30;
        if (!uuid) return jsonResponse({ success: false, error: 'UUID不能为空' }, 400);

        return jsonResponse(await fetchUuidStatsForManager(uuid, env, days));
    } catch (error) {
        console.error('handleUuidConfigStats error:', error.message);
        return jsonResponse({ success: false, error: '服务器错误' }, 500);
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

        if (path === '/api/uuid-configs' && ['GET', 'POST', 'PUT', 'DELETE'].includes(method)) {
            return await handleUuidConfigs(req, env, method);
        }

        if (path === '/api/uuid-configs/stats' && method === 'GET') {
            return await handleUuidConfigStats(req, env);
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

const loginHtml = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width,initial-scale=1.0">
    <link rel="icon" href="https://s3.yangzifun.org/logo.ico">
    <title>优选配置管理后台 - 登录</title>
    <script src="https://unpkg.com/lucide@latest"></script>
    <script src="https://cdn.tailwindcss.com"></script>
    <script>
        tailwind.config = {
            theme: {
                extend: {
                    colors: {
                        primary: '#5a666d',
                        secondary: '#89949B',
                        success: '#10b981',
                        danger: '#ef4444',
                        warning: '#f59e0b',
                        info: '#3b82f6',
                        light: '#f8f9fa',
                        dark: '#3d474d',
                        border: '#E8EBED',
                    }
                }
            }
        }
    </script>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        
        @keyframes fadeIn {
            from { opacity: 0; transform: translateY(5px); }
            to { opacity: 1; transform: translateY(0); }
        }
        
        @keyframes slideIn {
            from { transform: translateX(100%); opacity: 0; }
            to { transform: translateX(0); opacity: 1; }
        }
        
        @keyframes fadeOut {
            to { opacity: 0; transform: translateX(100%); }
        }
        
        .totp-input {
            text-align: center;
            letter-spacing: 10px;
            font-weight: bold;
        }
        
        .backup-code {
            display: inline-block;
            margin: 0.25rem;
            padding: 0.5rem 1rem;
            background: white;
            border: 1px solid #e5e7eb;
            border-radius: 0.25rem;
            font-weight: bold;
            font-family: monospace;
        }
        
        .type-badge {
            display: inline-block;
            padding: 0.125rem 0.5rem;
            border-radius: 9999px;
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
        
        .hosting-badge {
            display: inline-block;
            padding: 0.25rem 0.75rem;
            border-radius: 9999px;
            font-size: 0.75rem;
            font-weight: 500;
            white-space: nowrap;
        }
        
        .hosting-cloudflare {
            background: #F6821F;
            color: white;
        }
        
        .hosting-tencent {
            background: #4A86FF;
            color: white;
        }
        
        .template-badge {
            display: inline-block;
            padding: 0.25rem 0.75rem;
            border-radius: 9999px;
            font-size: 0.75rem;
            font-weight: 500;
        }
        
        .badge-default {
            background-color: #10b981;
            color: white;
        }
        
        .badge-normal {
            background-color: #6b7280;
            color: white;
        }
        
        .mfa-status-badge {
            display: inline-flex;
            align-items: center;
            padding: 0.25rem 0.75rem;
            border-radius: 9999px;
            font-size: 0.75rem;
            font-weight: 500;
            gap: 0.375rem;
        }
        
        .mfa-active {
            background-color: #d1fae5;
            color: #065f46;
        }
        
        .mfa-inactive {
            background-color: #fee2e2;
            color: #9f1239;
        }
        
        .status-indicator {
            width: 0.5rem;
            height: 0.5rem;
            border-radius: 9999px;
            display: inline-block;
        }
        
        .status-on {
            background-color: #10b981;
        }
        
        .status-off {
            background-color: #ef4444;
        }
        
        .status-warning {
            background-color: #f59e0b;
        }
        
        .secret-display {
            background: #1f2937;
            color: #fbbf24;
            padding: 1rem;
            border-radius: 0.375rem;
            font-family: monospace;
            text-align: center;
            margin: 1rem 0;
            font-size: 1.1rem;
            letter-spacing: 1px;
            word-break: break-all;
        }
        
        .backup-codes-box {
            background: #f3f4f6;
            border: 1px dashed #d1d5db;
            padding: 1rem;
            border-radius: 0.375rem;
            margin: 1rem 0;
            font-family: monospace;
            text-align: center;
            line-height: 2;
        }
        
        .security-step {
            background: #f0f9ff;
            border: 1px solid #bae6fd;
            padding: 1rem;
            border-radius: 0.5rem;
            margin: 1rem 0;
        }
        
        .qr-code-container {
            text-align: center;
            margin: 1.25rem 0;
            padding: 1rem;
            background: #f9fafb;
            border-radius: 0.5rem;
            border: 1px solid #e5e7eb;
        }
        
        .toast {
            padding: 0.75rem 1.125rem;
            border-radius: 0.25rem;
            border: 2px solid #89949B;
            background: #fff;
            color: #3d474d;
            font-weight: 500;
            font-size: 0.9rem;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            animation: slideIn 0.3s forwards, fadeOut 0.5s 3.5s forwards;
            min-width: 250px;
            max-width: 350px;
        }
    </style>
</head>
<body class="bg-gray-50 flex flex-col justify-center items-center min-h-screen p-4">
    <div id="toast-container" class="fixed top-5 right-5 z-50 flex flex-col gap-2.5"></div>
    
    <div id="passwordStep" class="w-full max-w-md bg-white p-8 rounded-lg border-2 border-secondary shadow-lg">
        <h2 class="text-2xl font-bold text-dark mb-2">优选配置管理后台</h2>
        <p class="text-secondary mb-4 min-h-[1em]" id="loginStatus">请先验证身份</p>
        <div id="msg" class="text-danger text-sm mb-4 hidden"></div>
        
        <form id="loginForm" onsubmit="event.preventDefault(); doLogin();">
            <div class="flex items-center gap-2 mb-4">
                <i data-lucide="user" class="w-5 h-5 text-secondary"></i>
                <input type="text" id="username" placeholder="管理员账号" required autocomplete="username"
                       class="flex-1 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
            </div>
            <div class="flex items-center gap-2 mb-6">
                <i data-lucide="lock" class="w-5 h-5 text-secondary"></i>
                <input type="password" id="password" placeholder="访问密码" required autocomplete="current-password"
                       class="flex-1 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
            </div>
            <button type="submit" id="loginBtn"
                    class="w-full bg-primary text-white py-2.5 px-4 rounded border-2 border-primary font-medium hover:bg-opacity-90 transition flex items-center justify-center gap-2">
                <i data-lucide="arrow-right" class="w-4 h-4"></i>
                登 录
            </button>
        </form>
        
        <div class="mt-4 text-sm text-gray-400">
            首次登录请确保网络连接正常
        </div>
    </div>

    <div id="totpStep" class="w-full max-w-md bg-white p-8 rounded-lg border-2 border-secondary shadow-lg hidden">
        <h2 class="text-2xl font-bold text-dark mb-2">双重验证</h2>
        <p class="text-secondary mb-6" id="mfaUsername"></p>
        
        <div class="text-center mb-6">
            <div class="text-gray-600 mb-4">
                请输入您的6位验证码
            </div>
            
            <div class="flex gap-2.5 mb-4">
                <input type="text" id="totpCode" maxlength="6" class="totp-input flex-1 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary"
                       pattern="[0-9]{6}" inputmode="numeric" placeholder="000000" required autocomplete="one-time-code">
            </div>
            
            <div class="text-gray-400 text-sm mb-6">
                请从身份验证器App获取验证码
            </div>
            
            <div class="flex gap-2.5">
                <button type="button" onclick="goBackToPassword()"
                        class="flex-1 bg-gray-100 text-primary py-2.5 px-4 rounded border-2 border-secondary font-medium hover:bg-secondary hover:text-white transition flex items-center justify-center gap-2">
                    <i data-lucide="arrow-left" class="w-4 h-4"></i>
                    返回
                </button>
                <button type="button" onclick="verifyTOTP()" id="verifyTOTPBtn"
                        class="flex-2 bg-primary text-white py-2.5 px-4 rounded border-2 border-primary font-medium hover:bg-opacity-90 transition flex items-center justify-center gap-2">
                    <i data-lucide="check" class="w-4 h-4"></i>
                    验证
                </button>
            </div>
        </div>
        
        <a class="block text-center text-gray-500 text-sm underline cursor-pointer hover:text-gray-700" onclick="showBackupLogin()">使用备份码登录</a>
    </div>

    <div id="backupStep" class="w-full max-w-md bg-white p-8 rounded-lg border-2 border-secondary shadow-lg hidden">
        <h2 class="text-2xl font-bold text-dark mb-2">备份码登录</h2>
        <p class="text-secondary mb-6">请输入8位备份码</p>
        
        <input type="text" id="backupUsername" class="hidden">
        <input type="password" id="backupPassword" class="hidden">
        
        <div class="mb-6">
            <label class="block text-gray-600 text-sm mb-2">备份码:</label>
            <input type="text" id="backupCode" placeholder="例如：A1B2C3D4" maxlength="8" style="text-transform: uppercase;" required
                   class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
        </div>
        
        <div class="flex gap-2.5 mb-6">
            <button type="button" onclick="cancelBackupLogin()"
                    class="flex-1 bg-gray-100 text-primary py-2.5 px-4 rounded border-2 border-secondary font-medium hover:bg-secondary hover:text-white transition flex items-center justify-center gap-2">
                <i data-lucide="x" class="w-4 h-4"></i>
                取消
            </button>
            <button type="button" onclick="verifyBackupCode()" id="verifyBackupBtn"
                    class="flex-2 bg-primary text-white py-2.5 px-4 rounded border-2 border-primary font-medium hover:bg-opacity-90 transition flex items-center justify-center gap-2">
                <i data-lucide="check" class="w-4 h-4"></i>
                验证备份码
            </button>
        </div>
        
        <div class="text-gray-400 text-sm">
            <p class="mb-1">• 备份码为一次性使用，用后失效</p>
            <p>• 请确保备份码准确无误</p>
        </div>
    </div>

    <footer class="mt-8 text-secondary text-sm">
        <p>Powered by <a href="https://www.yangzihome.space" target="_blank" class="text-primary hover:underline">YZFN</a></p>
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
                msg.classList.remove('hidden');
                return;
            }
            
            btn.disabled = true;
            btn.innerHTML = '<i data-lucide="loader" class="w-4 h-4 animate-spin"></i>验证中...';
            msg.classList.add('hidden');
            
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
                        
                        document.getElementById('passwordStep').classList.add('hidden');
                        document.getElementById('totpStep').classList.remove('hidden');
                        document.getElementById('mfaUsername').innerText = username;
                        msg.classList.add('hidden');
                        
                        setTimeout(() => document.getElementById('totpCode').focus(), 100);
                    } else {
                        localStorage.setItem('token', data.token);
                        localStorage.setItem('mfa_enabled', 'false');
                        window.location.href = '/';
                    }
                } else {
                    msg.innerText = data.error || '登录失败';
                    msg.classList.remove('hidden');
                }
            } catch (error) {
                msg.innerText = '连接服务器失败';
                msg.classList.remove('hidden');
            } finally {
                btn.disabled = false;
                btn.innerHTML = '<i data-lucide="arrow-right" class="w-4 h-4"></i>登 录';
            }
        }
        
        function goBackToPassword() {
            document.getElementById('passwordStep').classList.remove('hidden');
            document.getElementById('totpStep').classList.add('hidden');
            document.getElementById('backupStep').classList.add('hidden');
            document.getElementById('msg').classList.add('hidden');
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
            btn.innerHTML = '<i data-lucide="loader" class="w-4 h-4 animate-spin"></i>验证中...';
            
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
                btn.innerHTML = '<i data-lucide="check" class="w-4 h-4"></i>验证';
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
            
            document.getElementById('totpStep').classList.add('hidden');
            document.getElementById('backupStep').classList.remove('hidden');
            document.getElementById('backupCode').focus();
        }
        
        function cancelBackupLogin() {
            document.getElementById('backupStep').classList.add('hidden');
            document.getElementById('totpStep').classList.remove('hidden');
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
            btn.innerHTML = '<i data-lucide="loader" class="w-4 h-4 animate-spin"></i>验证中...';
            
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
                btn.innerHTML = '<i data-lucide="check" class="w-4 h-4"></i>验证备份码';
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
            lucide.createIcons();
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
    <script src="https://unpkg.com/lucide@latest"></script>
    <script src="https://cdn.tailwindcss.com"></script>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <script>
        tailwind.config = {
            theme: {
                extend: {
                    colors: {
                        primary: '#5a666d',
                        secondary: '#89949B',
                        success: '#10b981',
                        danger: '#ef4444',
                        warning: '#f59e0b',
                        info: '#3b82f6',
                        light: '#f8f9fa',
                        dark: '#3d474d',
                        border: '#E8EBED',
                    }
                }
            }
        }
    </script>
    <style>
        @keyframes fadeIn {
            from { opacity: 0; transform: translateY(5px); }
            to { opacity: 1; transform: translateY(0); }
        }
        
        @keyframes slideIn {
            from { transform: translateX(100%); opacity: 0; }
            to { transform: translateX(0); opacity: 1; }
        }
        
        @keyframes fadeOut {
            to { opacity: 0; transform: translateX(100%); }
        }
        
        @keyframes spin {
            to { transform: rotate(360deg); }
        }
        
        .nav-btn {
            display: inline-flex;
            align-items: center;
            justify-content: center;
            padding: 0.5rem 1rem;
            background: #E8EBED;
            border: 2px solid #89949B;
            border-radius: 0.25rem;
            color: #5a666d;
            font-weight: 500;
            font-size: 0.95rem;
            cursor: pointer;
            transition: all 0.2s;
            white-space: nowrap;
            text-decoration: none;
            outline: none;
        }
        
        .nav-btn:hover {
            background: #89949B;
            color: white;
            border-color: #89949B;
        }
        
        .nav-btn.active {
            background-color: #5a666d;
            color: white;
            border-color: #5a666d;
        }
        
        .nav-btn.danger {
            border-color: #ef4444;
            color: #ef4444;
            background: #fee2e2;
        }
        
        .nav-btn.danger:hover {
            background: #ef4444;
            color: white;
        }
        
        .nav-btn.small {
            padding: 0.25rem 0.625rem;
            font-size: 0.85rem;
        }
        
        .card {
            background: #f8f9fa;
            border: 1px solid #E8EBED;
            border-radius: 0.5rem;
            padding: 1.5rem;
            margin-bottom: 1.5rem;
            text-align: left;
            display: none;
            animation: fadeIn 0.3s ease;
            width: 100%;
        }
        
        .card.active {
            display: block;
        }
        
        .table-container {
            overflow-x: auto;
            border: 2px solid #89949B;
            border-radius: 0.25rem;
            background: #fff;
            margin-bottom: 1rem;
        }
        
        .toast {
            padding: 0.75rem 1.125rem;
            border-radius: 0.25rem;
            border: 2px solid #89949B;
            background: #fff;
            color: #3d474d;
            font-weight: 500;
            font-size: 0.9rem;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            animation: slideIn 0.3s forwards, fadeOut 0.5s 3.5s forwards;
            min-width: 250px;
            max-width: 350px;
        }
        
        .modal-overlay {
            position: fixed;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
            background: rgba(0,0,0,0.5);
            z-index: 50;
            display: none;
            align-items: center;
            justify-content: center;
            padding: 1.25rem;
            overflow-y: auto;
        }
        
        .modal {
            background: #fff;
            width: 90%;
            max-width: 500px;
            padding: 1.5rem;
            border-radius: 0.5rem;
            box-shadow: 0 10px 25px rgba(0,0,0,0.2);
            max-height: 90vh;
            overflow-y: auto;
        }
        
        .switch {
            position: relative;
            display: inline-block;
            width: 2.5rem;
            height: 1.375rem;
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
            border-radius: 2.125rem;
        }
        
        .slider:before {
            position: absolute;
            content: "";
            height: 1rem;
            width: 1rem;
            left: 0.1875rem;
            bottom: 0.1875rem;
            background-color: white;
            transition: .4s;
            border-radius: 50%;
        }
        
        input:checked + .slider {
            background-color: #10b981;
        }
        
        input:checked + .slider:before {
            transform: translateX(1.125rem);
        }
        
        .mfa-status-badge {
            display: inline-flex;
            align-items: center;
            padding: 0.25rem 0.75rem;
            border-radius: 9999px;
            font-size: 0.75rem;
            font-weight: 500;
            gap: 0.375rem;
        }
        
        .mfa-active {
            background-color: #d1fae5;
            color: #065f46;
        }
        
        .mfa-inactive {
            background-color: #fee2e2;
            color: #9f1239;
        }
        
        .status-indicator {
            width: 0.5rem;
            height: 0.5rem;
            border-radius: 9999px;
            display: inline-block;
        }
        
        .status-on {
            background-color: #10b981;
        }
        
        .status-off {
            background-color: #ef4444;
        }
        
        .secret-display {
            background: #1f2937;
            color: #fbbf24;
            padding: 1rem;
            border-radius: 0.375rem;
            font-family: monospace;
            text-align: center;
            margin: 1rem 0;
            font-size: 1.1rem;
            letter-spacing: 1px;
            word-break: break-all;
        }
        
        .backup-codes-box {
            background: #f3f4f6;
            border: 1px dashed #d1d5db;
            padding: 1rem;
            border-radius: 0.375rem;
            margin: 1rem 0;
            font-family: monospace;
            text-align: center;
            line-height: 2;
        }
        
        .backup-code {
            display: inline-block;
            margin: 0.3125rem;
            padding: 0.3125rem 0.625rem;
            background: white;
            border: 1px solid #e5e7eb;
            border-radius: 0.25rem;
            font-weight: bold;
        }
        
        .security-step {
            background: #f0f9ff;
            border: 1px solid #bae6fd;
            padding: 1rem;
            border-radius: 0.5rem;
            margin: 1rem 0;
        }
        
        .qr-code-container {
            text-align: center;
            margin: 1.25rem 0;
            padding: 1rem;
            background: #f9fafb;
            border-radius: 0.5rem;
            border: 1px solid #e5e7eb;
        }
        
        .type-badge {
            display: inline-block;
            padding: 0.125rem 0.5rem;
            border-radius: 9999px;
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
        
        .hosting-badge {
            display: inline-block;
            padding: 0.25rem 0.75rem;
            border-radius: 9999px;
            font-size: 0.75rem;
            font-weight: 500;
            white-space: nowrap;
        }
        
        .hosting-cloudflare {
            background: #F6821F;
            color: white;
        }
        
        .hosting-tencent {
            background: #4A86FF;
            color: white;
        }
        
        .template-badge {
            display: inline-block;
            padding: 0.25rem 0.75rem;
            border-radius: 9999px;
            font-size: 0.75rem;
            font-weight: 500;
        }
        
        .badge-default {
            background-color: #10b981;
            color: white;
        }
        
        .badge-normal {
            background-color: #6b7280;
            color: white;
        }
        
        .template-url-cell {
            max-width: 300px;
            word-break: break-all;
            white-space: normal;
        }

        .config-data-cell {
            max-width: 360px;
            word-break: break-all;
            white-space: normal;
        }
        
        .loader {
            display: inline-block;
            width: 3.125rem;
            height: 3.125rem;
            border: 3px solid #E8EBED;
            border-radius: 50%;
            border-top-color: #5a666d;
            animation: spin 1s ease-in-out infinite;
        }
        
        .chart-container {
            position: relative;
            width: 100%;
            height: 400px;
            margin: 1.25rem 0;
        }
        
        .access-stat-box {
            background: #f0f4f8;
            padding: 1rem;
            border-radius: 0.5rem;
            border-left: 4px solid #3b82f6;
            transition: transform 0.2s;
        }
        
        .access-stat-box:hover {
            transform: translateY(-2px);
            box-shadow: 0 4px 8px rgba(0,0,0,0.1);
        }
        
        .popular-uuids-item {
            display: flex;
            justify-content: space-between;
            padding: 0.5rem;
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
            padding: 0.5rem 0.625rem;
            border-bottom: 1px solid #e5e7eb;
        }
        
        .sortable::after {
            content: '↕';
            margin-left: 0.3125rem;
            opacity: 0.5;
            font-size: 0.8em;
        }
        
        /* 页码按钮样式 */
        #domPageNumbers .nav-btn.small,
        #ipPageNumbers .nav-btn.small, 
        #uuidPageNumbers .nav-btn.small,
        #templatePageNumbers .nav-btn.small {
            padding: 0.25rem 0.5rem;
            min-width: 32px;
            height: 32px;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 0.875rem;
            font-weight: 500;
            border-radius: 4px;
            transition: all 0.2s ease;
        }
        
        #domPageNumbers .nav-btn.small.active,
        #ipPageNumbers .nav-btn.small.active,
        #uuidPageNumbers .nav-btn.small.active,
        #templatePageNumbers .nav-btn.small.active {
            background-color: #5a666d;
            color: white;
            border-color: #5a666d;
            cursor: default;
        }
        
        #domPageNumbers .nav-btn.small:hover:not(.active),
        #ipPageNumbers .nav-btn.small:hover:not(.active),
        #uuidPageNumbers .nav-btn.small:hover:not(.active),
        #templatePageNumbers .nav-btn.small:hover:not(.active) {
            background-color: #89949B;
            color: white;
            border-color: #89949B;
        }
        
        /* 分页控件容器 */
        #domPaginationControls,
        #ipPaginationControls,
        #uuidPaginationControls,
        #templatePaginationControls {
            display: flex;
            align-items: center;
            gap: 4px;
        }
        
        /* 页码省略号样式 */
        #domPageNumbers .text-secondary,
        #ipPageNumbers .text-secondary,
        #uuidPageNumbers .text-secondary,
        #templatePageNumbers .text-secondary {
            display: flex;
            align-items: center;
            justify-content: center;
            min-width: 24px;
            color: #6b7280;
            user-select: none;
        }
    </style>
</head>
<body class="bg-white min-h-screen">
    <div id="toast-container" class="fixed top-5 right-5 z-50 flex flex-col gap-2.5"></div>
    <div id="loader" class="fixed top-1/2 left-1/2 transform -translate-x-1/2 -translate-y-1/2 text-secondary">系统加载中...</div>

    <div class="container mx-auto p-5 min-h-screen flex flex-col items-center" id="app" style="display:none">
        <div class="w-full max-w-6xl flex justify-between items-center mb-5 px-1">
            <div>
                <span class="font-bold text-dark">CF 优选配置管理</span> 
                <span class="text-secondary text-sm">| Admin</span>
                <span id="mfaBadge" class="mfa-status-badge ml-2.5 hidden"></span>
            </div>
            <div class="flex items-center gap-2">
                <a class="text-secondary text-sm cursor-pointer hover:text-primary transition flex items-center gap-1" onclick="openPwdModal()">
                    <i data-lucide="key" class="w-4 h-4"></i>
                    修改密码
                </a>
                <a class="text-danger text-sm cursor-pointer hover:text-red-700 transition flex items-center gap-1 ml-3.5" onclick="logout()">
                    <i data-lucide="log-out" class="w-4 h-4"></i>
                    安全退出
                </a>
            </div>
        </div>

        <div class="w-full max-w-6xl text-center mt-5">
            <h1 class="text-3xl font-bold text-dark mb-1">管理后台</h1>
            <div class="flex flex-wrap justify-center gap-2 mb-6 pb-5 border-b border-border">
                <button class="nav-btn active" onclick="switchTab('dash', this)">
                    <i data-lucide="chart-line" class="w-4 h-4 mr-1"></i>
                    系统概览
                </button>
                <button class="nav-btn" onclick="switchTab('dom', this)">
                    <i data-lucide="globe" class="w-4 h-4 mr-1"></i>
                    优选域名
                </button>
                <button class="nav-btn" onclick="switchTab('ips', this)">
                    <i data-lucide="network" class="w-4 h-4 mr-1"></i>
                    IP 资源池
                </button>
                <button class="nav-btn" onclick="switchTab('uuids', this)">
                    <i data-lucide="key" class="w-4 h-4 mr-1"></i>
                    配置分组
                </button>
                <button class="nav-btn" onclick="switchTab('templates', this)">
                    <i data-lucide="layers" class="w-4 h-4 mr-1"></i>
                    配置模板
                </button>
                <button class="nav-btn" onclick="switchTab('security', this)">
                    <i data-lucide="shield" class="w-4 h-4 mr-1"></i>
                    安全中心
                </button>
            </div>

            <div id="dash" class="card active">
                <h2 class="text-xl font-semibold text-dark mb-5 pb-2.5 border-b-2 border-border">系统状态</h2>
                <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
                    <div class="bg-border p-5 rounded-lg text-center transition-transform hover:-translate-y-1">
                        <span class="text-3xl font-bold text-primary block mb-1" id="s-dom">-</span>
                        <span class="text-sm text-secondary">优选域名</span>
                    </div>
                    <div class="bg-border p-5 rounded-lg text-center transition-transform hover:-translate-y-1">
                        <span class="text-3xl font-bold text-primary block mb-1" id="s-ip">-</span>
                        <span class="text-sm text-secondary">活跃 IP</span>
                    </div>
                    <div class="bg-border p-5 rounded-lg text-center transition-transform hover:-translate-y-1">
                        <span class="text-3xl font-bold text-primary block mb-1" id="s-uuid">-</span>
                        <span class="text-sm text-secondary">配置分组</span>
                    </div>
                    <div class="bg-border p-5 rounded-lg text-center transition-transform hover:-translate-y-1">
                        <span class="text-3xl font-bold text-primary block mb-1" id="s-template">-</span>
                        <span class="text-sm text-secondary">配置模板</span>
                    </div>
                </div>
                
                <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6" id="accessStatsGrid" style="display:none;">
                    <div class="access-stat-box">
                        <span class="text-2xl font-bold text-blue-800 block" id="as-total">0</span>
                        <span class="text-sm text-gray-600">总访问次数</span>
                        <span class="text-xs text-gray-500 block mt-1" id="as-unique">0个独立UUID</span>
                    </div>
                    <div class="access-stat-box">
                        <span class="text-2xl font-bold text-blue-800 block" id="as-today">0</span>
                        <span class="text-sm text-gray-600">今日访问</span>
                        <span class="text-xs text-gray-500 block mt-1" id="as-today-split">订阅:0 | 网页:0</span>
                    </div>
                    <div class="access-stat-box">
                        <span class="text-2xl font-bold text-blue-800 block" id="as-subscription">0</span>
                        <span class="text-sm text-gray-600">订阅访问</span>
                        <span class="text-xs text-gray-500 block mt-1">通过订阅链接访问</span>
                    </div>
                    <div class="access-stat-box">
                        <span class="text-2xl font-bold text-blue-800 block" id="as-apigen">0</span>
                        <span class="text-sm text-gray-600">网页生成</span>
                        <span class="text-xs text-gray-500 block mt-1">通过网页工具生成</span>
                    </div>
                </div>
                
                <h3 class="text-lg font-semibold text-dark mt-8 mb-4">📊 订阅访问趋势分析 (多指标视图)</h3>
                
                <div class="flex flex-wrap gap-2.5 items-center mb-4">
                    <select id="chartDays" onchange="loadAccessStats()"
                            class="px-3 py-1.5 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                        <option value="7">最近7天</option>
                        <option value="14" selected>最近14天</option>
                        <option value="30">最近30天</option>
                        <option value="60">最近60天</option>
                    </select>
                    <button class="nav-btn" onclick="loadAccessStats()">
                        <i data-lucide="refresh-cw" class="w-4 h-4 mr-1"></i>
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
                
                <div class="flex flex-col lg:flex-row gap-5 mt-5">
                    <div class="lg:w-1/3">
                        <h3 class="text-base font-semibold text-dark mb-3">热门 UUID 排行</h3>
                        <div class="popular-uuids-list border border-gray-200 rounded max-h-48 overflow-y-auto p-2.5" id="popularUUIDsList">
                            <div class="text-center py-5 text-gray-500">加载中...</div>
                        </div>
                    </div>
                    
                    <div class="lg:w-2/3">
                        <h3 class="text-base font-semibold text-dark mb-3">访问趋势说明</h3>
                        <div class="bg-gray-50 p-4 rounded text-sm text-gray-600">
                            <p class="mb-2"><strong>订阅访问：</strong>用户通过订阅链接（/batch-configs/{uuid}）访问配置</p>
                            <p class="mb-2"><strong>网页生成：</strong>用户通过网页工具（/generate）生成配置</p>
                            <p class="mb-2"><strong>独立用户：</strong>按UUID统计的每日活跃用户数</p>
                            <p class="mt-3 text-gray-500 text-xs">
                                <i>数据统计基于 config_access_logs 表，记录所有UUID配置生成请求</i>
                            </p>
                        </div>
                    </div>
                </div>
                
                <div class="mt-6 p-3 bg-border rounded text-sm text-dark">
                    自动更新状态: <span id="autoUpdateStatus">加载中...</span>
                    <br>最后执行时间: <span id="lastExecuted">未知</span>
                </div>
                
                <div class="mt-6 p-4 bg-blue-50 border-l-4 border-blue-500 rounded-r" id="mfaStatusSection" style="display:none;">
                    <h3 class="text-base font-semibold text-blue-800 mb-3">⛑️ 账户安全状态</h3>
                    <div id="mfaStatusDetails">加载中...</div>
                </div>
            </div>

            <div id="dom" class="card">
                <h2 class="text-xl font-semibold text-dark mb-5 pb-2.5 border-b-2 border-border">优选域名管理</h2>
                <div class="flex flex-wrap gap-2.5 mb-4">
                    <input type="text" id="newD" placeholder="域名 (例如: cf.example.com)" 
                           class="flex-1 min-w-[200px] px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                    <input type="text" id="newR" placeholder="自定义备注" 
                           class="flex-1 min-w-[150px] px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                    <select id="newSource"
                           class="flex-1 min-w-[150px] px-3 py-2 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary text-dark cursor-pointer">
                        <option value="Cloudflare">Cloudflare</option>
                        <option value="腾讯云EdgeOne">腾讯云EdgeOne</option>
                    </select>
                    <button class="nav-btn active min-w-[100px]" onclick="addDomain()">
                        <i data-lucide="plus" class="w-4 h-4 mr-1"></i>
                        添加域名
                    </button>
                </div>
                <div class="table-container">
                    <table class="w-full border-collapse text-sm">
                        <thead>
                            <tr>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortDom('domain')">域名 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortDom('remark')">备注 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortDom('source')">来源 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortDom('created_at')">创建时间 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold w-36">操作</th>
                            </tr>
                        </thead>
                        <tbody id="domList">
                            <tr><td colspan="5" class="p-5 text-center">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                <div class="flex flex-col sm:flex-row justify-between items-center pt-4 text-sm text-primary min-h-10" id="domPager">
                    <div class="flex items-center gap-2 mb-3 sm:mb-0">
                        <select class="page-size px-2 py-1 border border-secondary rounded bg-white" id="domSize" onchange="changeDomSize()">
                            <option value="5">5条/页</option>
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                        </select>
                        <span class="text-xs text-gray-500 ml-2" id="domTotalInfo">共 0 条</span>
                    </div>
                    <div class="flex items-center gap-1" id="domPaginationControls">
                        <button class="nav-btn small px-2" onclick="changeDomPage(-1)" id="domPrevBtn" title="上一页">
                            <i data-lucide="chevron-left" class="w-4 h-4"></i>
                        </button>
                        <div id="domPageNumbers" class="flex gap-1"></div> <!-- 数字页码容器 -->
                        <button class="nav-btn small px-2" onclick="changeDomPage(1)" id="domNextBtn" title="下一页">
                            <i data-lucide="chevron-right" class="w-4 h-4"></i>
                        </button>
                    </div>
                </div>
            </div>

            <div id="ips" class="card">
                <div class="flex flex-col sm:flex-row justify-between items-start sm:items-center mb-4">
                    <h2 class="text-xl font-semibold text-dark mb-3 sm:mb-0">IP 资源池管理</h2>
                    <div class="flex items-center gap-3.5">
                        <div class="px-2.5 py-1.5 bg-border rounded text-sm text-dark flex items-center gap-2">
                            <span class="status-indicator" id="globalStatusIndicator"></span>
                            <span id="globalStatusText">自动更新: 关闭</span>
                        </div>
                    </div>
                </div>
                
                <div class="flex flex-wrap gap-5 items-center mb-6 p-4 bg-border rounded">
                    <label class="flex items-center gap-2 cursor-pointer">
                        <span class="font-medium text-primary">主开关</span>
                        <div class="switch">
                            <input type="checkbox" id="sw-global" checked>
                            <span class="slider"></span>
                        </div>
                    </label>
                    <label class="flex items-center gap-2 cursor-pointer">
                        <span class="font-medium text-primary">HostMonit IPv4接口</span>
                        <div class="switch">
                            <input type="checkbox" id="sw-hm-v4" checked>
                            <span class="slider"></span>
                        </div>
                    </label>
                    <label class="flex items-center gap-2 cursor-pointer">
                        <span class="font-medium text-primary">HostMonit IPv6接口</span>
                        <div class="switch">
                            <input type="checkbox" id="sw-hm-v6">
                            <span class="slider"></span>
                        </div>
                    </label>
                    <label class="flex items-center gap-2 cursor-pointer">
                        <span class="font-medium text-primary">Vps789接口</span>
                        <div class="switch">
                            <input type="checkbox" id="sw-v7" checked>
                            <span class="slider"></span>
                        </div>
                    </label>
                    <button class="nav-btn" onclick="saveAutoUpdateSettings()">
                        <i data-lucide="save" class="w-4 h-4 mr-1"></i>
                        保存设置
                    </button>
                    <button class="nav-btn active" onclick="refreshIps()">
                        <i data-lucide="refresh-cw" class="w-4 h-4 mr-1"></i>
                        立即更新
                    </button>
                </div>
                
                <div class="table-container">
                    <table class="w-full border-collapse text-sm">
                        <thead>
                            <tr>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortIp('ip')">IP 地址 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortIp('ip_type')">类型 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortIp('carrier')">运营商 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortIp('source')">来源 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortIp('created_at')">入库时间 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold w-24">操作</th>
                            </tr>
                        </thead>
                        <tbody id="ipList">
                            <tr><td colspan="6" class="p-5 text-center">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                <div class="flex flex-col sm:flex-row justify-between items-center pt-4 text-sm text-primary min-h-10" id="ipPager">
                    <div class="flex items-center gap-2 mb-3 sm:mb-0">
                        <select class="page-size px-2 py-1 border border-secondary rounded bg-white" id="ipSize" onchange="changeIpSize()">
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                            <option value="100">100条/页</option>
                        </select>
                        <span class="text-xs text-gray-500 ml-2" id="ipTotalInfo">共 0 条</span>
                    </div>
                    <div class="flex items-center gap-1" id="ipPaginationControls">
                        <button class="nav-btn small px-2" onclick="changeIpPage(-1)" id="ipPrevBtn" title="上一页">
                            <i data-lucide="chevron-left" class="w-4 h-4"></i>
                        </button>
                        <div id="ipPageNumbers" class="flex gap-1"></div> <!-- 数字页码容器 -->
                        <button class="nav-btn small px-2" onclick="changeIpPage(1)" id="ipNextBtn" title="下一页">
                            <i data-lucide="chevron-right" class="w-4 h-4"></i>
                        </button>
                    </div>
                </div>
            </div>

            <div id="uuids" class="card">
                <h2 class="text-xl font-semibold text-dark mb-5 pb-2.5 border-b-2 border-border">UUID 配置分组</h2>
                <div class="table-container">
                    <table class="w-full border-collapse text-sm">
                        <thead>
                            <tr>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortUuid('uuid')">UUID ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortUuid('owner')">所有者 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortUuid('email')">邮箱 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortUuid('status')">状态 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortUuid('count')">节点数 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortUuid('updated_at')">最近更新 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold w-48">操作</th>
                            </tr>
                        </thead>
                        <tbody id="uuidList">
                            <tr><td colspan="7" class="p-5 text-center">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                <div class="flex flex-col sm:flex-row justify-between items-center pt-4 text-sm text-primary min-h-10" id="uuidPager">
                    <div class="flex items-center gap-2 mb-3 sm:mb-0">
                        <select class="page-size px-2 py-1 border border-secondary rounded bg-white" id="uuidSize" onchange="changeUuidSize()">
                            <option value="5">5条/页</option>
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                        </select>
                        <span class="text-xs text-gray-500 ml-2" id="uuidTotalInfo">共 0 条</span>
                    </div>
                    <div class="flex items-center gap-1" id="uuidPaginationControls">
                        <button class="nav-btn small px-2" onclick="changeUuidPage(-1)" id="uuidPrevBtn" title="上一页">
                            <i data-lucide="chevron-left" class="w-4 h-4"></i>
                        </button>
                        <div id="uuidPageNumbers" class="flex gap-1"></div> <!-- 数字页码容器 -->
                        <button class="nav-btn small px-2" onclick="changeUuidPage(1)" id="uuidNextBtn" title="下一页">
                            <i data-lucide="chevron-right" class="w-4 h-4"></i>
                        </button>
                    </div>
                </div>
            </div>
            
            <div id="templates" class="card">
                <h2 class="text-xl font-semibold text-dark mb-5 pb-2.5 border-b-2 border-border">📋 配置模板管理</h2>
                <div class="mb-5 p-4 bg-gray-50 rounded">
                    <p class="text-sm text-gray-600 m-0">
                        配置模板用于存储常用的配置格式，方便快速生成和统一管理配置。
                    </p>
                </div>
                
                <div class="flex flex-wrap gap-2.5 mb-5">
                    <input type="text" id="newTemplateName" placeholder="模板名称（必填）" 
                           class="flex-1 min-w-[150px] px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary"
                           title="模板名称，必须唯一">
                    <input type="text" id="newTemplateUrl" placeholder="模板URL（必填）" 
                           class="flex-2 min-w-[200px] px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary"
                           title="配置模板的URL地址">
                    <input type="text" id="newTemplateDesc" placeholder="模板描述" 
                           class="flex-1 min-w-[150px] px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary"
                           title="模板描述信息，可选">
                    <div class="flex items-center gap-2 px-2.5">
                        <input type="checkbox" id="newTemplateDefault" class="w-4 h-4">
                        <label for="newTemplateDefault" class="text-sm text-primary cursor-pointer">
                            设为默认
                        </label>
                    </div>
                    <button class="nav-btn active min-w-[100px]" onclick="addTemplate()">
                        <i data-lucide="plus" class="w-4 h-4 mr-1"></i>
                        添加模板
                    </button>
                </div>
                
                <div class="table-container">
                    <table class="w-full border-collapse text-sm">
                        <thead>
                            <tr>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortTemplate('name')">模板名称 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortTemplate('url')">模板URL ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortTemplate('description')">描述 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortTemplate('is_default')">状态 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold cursor-pointer sortable" onclick="sortTemplate('created_at')">创建时间 ↕</th>
                                <th class="p-3 text-left bg-gray-50 text-dark font-semibold w-48">操作</th>
                            </tr>
                        </thead>
                        <tbody id="templateList">
                            <tr><td colspan="6" class="p-5 text-center">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                
                <div class="flex flex-col sm:flex-row justify-between items-center pt-4 text-sm text-primary min-h-10" id="templatePager">
                    <div class="flex items-center gap-2 mb-3 sm:mb-0">
                        <select class="page-size px-2 py-1 border border-secondary rounded bg-white" id="templateSize" onchange="changeTemplateSize()">
                            <option value="5">5条/页</option>
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                        </select>
                        <span class="text-xs text-gray-500 ml-2" id="templateTotalInfo">共 0 条</span>
                    </div>
                    <div class="flex items-center gap-1" id="templatePaginationControls">
                        <button class="nav-btn small px-2" onclick="changeTemplatePage(-1)" id="templatePrevBtn" title="上一页">
                            <i data-lucide="chevron-left" class="w-4 h-4"></i>
                        </button>
                        <div id="templatePageNumbers" class="flex gap-1"></div> <!-- 数字页码容器 -->
                        <button class="nav-btn small px-2" onclick="changeTemplatePage(1)" id="templateNextBtn" title="下一页">
                            <i data-lucide="chevron-right" class="w-4 h-4"></i>
                        </button>
                    </div>
                </div>
            </div>

            <div id="security" class="card">
                <div class="max-w-2xl mx-auto">
                    <h2 class="text-xl font-semibold text-dark mb-5 pb-2.5 border-b-2 border-border">🛡️ 账户安全</h2>
                    
                    <div class="security-section" id="mfaCurrentStatus">
                        <h3 class="text-lg font-semibold text-blue-800 mb-3">双重验证状态</h3>
                        <div id="mfaStatusContent">加载中...</div>
                        <div id="mfaActions" class="mt-4">
                            <button class="nav-btn active" onclick="startMfaSetup()" id="enableMfaBtn">
                                <i data-lucide="shield-plus" class="w-4 h-4 mr-1"></i>
                                启用双重验证
                            </button>
                            <button class="nav-btn danger" onclick="disableMfaConfirm()" id="disableMfaBtn" style="display:none;">
                                <i data-lucide="shield-off" class="w-4 h-4 mr-1"></i>
                                禁用双重验证
                            </button>
                        </div>
                    </div>
                    
                    <div id="mfaSetupSteps" style="display:none;"></div>
                </div>
            </div>

        </div>

        <footer class="mt-auto pt-10 pb-5 text-secondary text-sm text-center w-full border-t border-gray-100">
            <p>Powered by <a href="https://www.yangzihome.space" target="_blank" class="text-primary hover:underline">YZFN</a></p>
        </footer>
    </div>

    <!-- 修改密码弹窗 -->
    <div class="modal-overlay" id="pwdModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 class="text-lg font-semibold text-dark mb-4">修改管理员密码</h3>
            <div class="mb-4">
                <input type="password" id="oldP" placeholder="当前旧密码" autocomplete="current-password"
                       class="w-full mb-3 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                <input type="password" id="newP" placeholder="新密码" autocomplete="new-password"
                       class="w-full mb-3 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                <input type="password" id="confirmP" placeholder="确认新密码" autocomplete="new-password"
                       class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
            </div>
            <div class="flex justify-end gap-2.5">
                <button class="nav-btn" onclick="document.getElementById('pwdModal').style.display='none'">
                    <i data-lucide="x" class="w-4 h-4 mr-1"></i>
                    取消
                </button>
                <button class="nav-btn active" onclick="changePwd()">
                    <i data-lucide="check" class="w-4 h-4 mr-1"></i>
                    确认修改
                </button>
            </div>
        </div>
    </div>

    <!-- 域名编辑弹窗 -->
    <div class="modal-overlay" id="editDomModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 class="text-lg font-semibold text-dark mb-4">编辑域名</h3>
            <input type="hidden" id="editId">
            <input type="hidden" id="editSource">
            <div class="mb-4">
                <label class="block text-sm text-primary mb-1.5">域名:</label>
                <input type="text" id="editDomain" class="w-full mb-3 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                <label class="block text-sm text-primary mb-1.5">备注:</label>
                <input type="text" id="editRemark" class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
            </div>
            <div class="flex justify-end gap-2.5">
                <button class="nav-btn" onclick="document.getElementById('editDomModal').style.display='none'">
                    <i data-lucide="x" class="w-4 h-4 mr-1"></i>
                    取消
                </button>
                <button class="nav-btn active" onclick="updateDomain()">
                    <i data-lucide="check" class="w-4 h-4 mr-1"></i>
                    保存修改
                </button>
            </div>
        </div>
    </div>

    <!-- MFA 验证密码弹窗 -->
    <div class="modal-overlay" id="mfaAuthModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 class="text-lg font-semibold text-dark mb-4">验证身份以启用双重验证</h3>
            <div class="mt-2.5">
                <input type="password" id="mfaAuthPassword" placeholder="请输入当前密码" autocomplete="current-password" onkeydown="if(event.key==='Enter') submitMfaAuthModal()"
                       class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                <div id="mfaAuthError" class="text-danger mt-2 hidden"></div>
            </div>
            <div class="flex justify-end gap-2.5 mt-5">
                <button class="nav-btn" onclick="document.getElementById('mfaAuthModal').style.display='none'; window._mfaAuthCallback=null;">
                    <i data-lucide="x" class="w-4 h-4 mr-1"></i>
                    取消
                </button>
                <button class="nav-btn active" onclick="submitMfaAuthModal()">
                    <i data-lucide="arrow-right" class="w-4 h-4 mr-1"></i>
                    继续
                </button>
            </div>
        </div>
    </div>

    <!-- 备份码显示弹窗 -->
    <div class="modal-overlay" id="backupCodesModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <div class="max-h-[80vh] overflow-y-auto">
                <h3 class="text-lg font-semibold text-dark sticky top-0 bg-white pb-2.5 border-b mb-4">🔐 备份码</h3>
                
                <div class="text-red-600 bg-red-50 p-3 rounded mb-4 text-sm">
                    <strong>重要提示：</strong>
                    <p class="mt-1">• 请立即保存这些备份码！</p>
                    <p>• 每个备份码只能使用一次</p>
                    <p>• 保存后无法再次查看完整列表</p>
                </div>
                
                <div id="backupCodesList" class="backup-codes-box text-base"></div>
                
                <div class="mt-5">
                    <button class="nav-btn active mr-2.5" onclick="printBackupCodes()">
                        <i data-lucide="printer" class="w-4 h-4 mr-1"></i>
                        打印
                    </button>
                    <button class="nav-btn" onclick="copyBackupCodes()">
                        <i data-lucide="clipboard-copy" class="w-4 h-4 mr-1"></i>
                        复制
                    </button>
                </div>
                
                <div class="mt-5 text-sm text-gray-500">
                    <p class="font-medium mb-1">如何使用备份码：</p>
                    <p>• 登录时选择"使用备份码登录"</p>
                    <p>• 输入完整的8位备份码</p>
                    <p>• 使用后该备份码将立即失效</p>
                </div>
                
                <div class="flex justify-end mt-5">
                    <button class="nav-btn active" onclick="document.getElementById('backupCodesModal').style.display='none'">我已保存</button>
                </div>
            </div>
        </div>
    </div>

    <!-- UUID访问详情弹窗 -->
    <div class="modal-overlay" id="uuidDetailsModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal max-w-4xl">
            <div class="max-h-[80vh] overflow-y-auto">
                <h3 class="text-lg font-semibold text-dark sticky top-0 bg-white pb-2.5 border-b mb-4">📋 UUID访问详情: <span id="modalUUID"></span></h3>
                
                <div id="uuidStats" class="my-4 text-sm">
                    <div class="grid grid-cols-3 gap-2.5 mb-4">
                        <div class="bg-blue-50 p-3 rounded">
                            <div class="text-xl font-bold text-blue-800" id="modalTotalAccess">0</div>
                            <div class="text-xs text-gray-500">总访问次数</div>
                        </div>
                        <div class="bg-blue-50 p-3 rounded">
                            <div class="text-base font-bold text-dark" id="modalFirstAccess">-</div>
                            <div class="text-xs text-gray-500">首次访问</div>
                        </div>
                        <div class="bg-blue-50 p-3 rounded">
                            <div class="text-base font-bold text-dark" id="modalLastAccess">-</div>
                            <div class="text-xs text-gray-500">最后访问</div>
                        </div>
                    </div>
                </div>
                
                <div class="my-4">
                    <h4 class="text-base font-semibold text-dark mb-2.5">最近访问记录</h4>
                    <div class="max-h-72 overflow-y-auto border border-gray-200 rounded">
                        <table class="access-detail-table w-full">
                            <thead>
                                <tr>
                                    <th class="p-2 bg-gray-50">时间</th>
                                    <th class="p-2 bg-gray-50">访问类型</th>
                                    <th class="p-2 bg-gray-50">客户端IP</th>
                                    <th class="p-2 bg-gray-50">User Agent</th>
                                </tr>
                            </thead>
                            <tbody id="modalAccessLogs"></tbody>
                        </table>
                    </div>
                </div>
                
                <div class="flex justify-end mt-5">
                    <button class="nav-btn active" onclick="document.getElementById('uuidDetailsModal').style.display='none'">关闭</button>
                </div>
            </div>
        </div>
    </div>

    <!-- UUID配置管理弹窗 -->
    <div class="modal-overlay" id="uuidConfigModal" onclick="event.target===this && closeUuidConfigManager()">
        <div class="modal" style="max-width: 1100px;">
            <div class="max-h-[85vh] overflow-y-auto">
                <div class="sticky top-0 bg-white pb-3 border-b mb-4 z-10">
                    <div class="flex flex-col md:flex-row md:items-center md:justify-between gap-3">
                        <div>
                            <h3 class="text-lg font-semibold text-dark">分组配置管理</h3>
                            <div class="font-mono text-xs text-gray-500 break-all" id="managedUuidText"></div>
                        </div>
                        <div class="flex gap-2 flex-wrap">
                            <button class="nav-btn small active" id="uuidConfigListTabBtn" onclick="switchUuidConfigTab('list')">配置列表</button>
                            <button class="nav-btn small" id="uuidConfigStatsTabBtn" onclick="switchUuidConfigTab('stats')">访问统计</button>
                            <button class="nav-btn small" id="uuidConfigAddTabBtn" onclick="switchUuidConfigTab('add')">添加新节点</button>
                            <button class="nav-btn small" onclick="closeUuidConfigManager()">关闭</button>
                        </div>
                    </div>
                </div>

                <div id="uuidConfigListPanel">
                    <div class="flex flex-wrap gap-2.5 items-center mb-4">
                        <select id="uuidConfigProtocolFilter" onchange="applyUuidConfigFilters()"
                                class="px-3 py-1.5 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                            <option value="all">全部协议</option>
                            <option value="vmess">vmess</option>
                            <option value="vless">vless</option>
                            <option value="trojan">trojan</option>
                            <option value="shadowsocks">shadowsocks</option>
                            <option value="hysteria2">hysteria2</option>
                            <option value="tuic">tuic</option>
                            <option value="anytls">anytls</option>
                            <option value="socks5">socks5</option>
                        </select>
                        <select id="uuidConfigHostingFilter" onchange="applyUuidConfigFilters()"
                                class="px-3 py-1.5 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                            <option value="all">全部托管</option>
                            <option value="Cloudflare">Cloudflare</option>
                            <option value="腾讯云EdgeOne">腾讯云EdgeOne</option>
                            <option value="腾讯Edgeone">腾讯Edgeone</option>
                            <option value="阿里ESA">阿里ESA</option>
                            <option value="AWS Cloudfront">AWS Cloudfront</option>
                            <option value="Gcore">Gcore</option>
                            <option value="Fastly">Fastly</option>
                            <option value="无">无</option>
                            <option value="其他">其他</option>
                        </select>
                        <select id="uuidConfigSize" onchange="changeUuidConfigSize()"
                                class="px-3 py-1.5 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                            <option value="5">5条/页</option>
                            <option value="10" selected>10条/页</option>
                            <option value="20">20条/页</option>
                            <option value="50">50条/页</option>
                        </select>
                        <button class="nav-btn small" onclick="loadUuidConfigs()">
                            <i data-lucide="refresh-cw" class="w-4 h-4 mr-1"></i>
                            刷新
                        </button>
                        <button class="nav-btn danger small" onclick="clearManagedUuidConfigs()">
                            <i data-lucide="trash-2" class="w-4 h-4 mr-1"></i>
                            清空分组配置
                        </button>
                    </div>

                    <div class="table-container">
                        <table class="w-full border-collapse text-sm">
                            <thead>
                                <tr>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">备注</th>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">协议</th>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">域名托管</th>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">配置</th>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold w-36">操作</th>
                                </tr>
                            </thead>
                            <tbody id="uuidConfigList">
                                <tr><td colspan="5" class="p-5 text-center">请选择分组</td></tr>
                            </tbody>
                        </table>
                    </div>

                    <div class="flex justify-between items-center pt-4 text-sm text-primary">
                        <span id="uuidConfigTotalInfo">共 0 条</span>
                        <div class="flex items-center gap-2">
                            <button class="nav-btn small" onclick="changeUuidConfigPage(-1)" id="uuidConfigPrevBtn">
                                <i data-lucide="chevron-left" class="w-4 h-4"></i>
                            </button>
                            <span id="uuidConfigPageInfo">第 1 / 1 页</span>
                            <button class="nav-btn small" onclick="changeUuidConfigPage(1)" id="uuidConfigNextBtn">
                                <i data-lucide="chevron-right" class="w-4 h-4"></i>
                            </button>
                        </div>
                    </div>
                </div>

                <div id="uuidConfigStatsPanel" style="display:none;">
                    <div class="flex flex-wrap gap-2.5 items-center mb-4">
                        <select id="uuidConfigStatsDays" onchange="loadManagedUuidStats()"
                                class="px-3 py-1.5 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                            <option value="7">最近7天</option>
                            <option value="14">最近14天</option>
                            <option value="30" selected>最近30天</option>
                            <option value="60">最近60天</option>
                        </select>
                        <button class="nav-btn small" onclick="loadManagedUuidStats()">刷新统计</button>
                    </div>
                    <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-4">
                        <div class="access-stat-box"><span class="text-2xl font-bold text-blue-800 block" id="managedStatsTotal">0</span><span class="text-sm text-gray-600">总访问</span></div>
                        <div class="access-stat-box"><span class="text-2xl font-bold text-blue-800 block" id="managedStatsToday">0</span><span class="text-sm text-gray-600">今日访问</span></div>
                        <div class="access-stat-box"><span class="text-2xl font-bold text-blue-800 block" id="managedStatsSub">0</span><span class="text-sm text-gray-600">订阅访问</span></div>
                        <div class="access-stat-box"><span class="text-2xl font-bold text-blue-800 block" id="managedStatsApi">0</span><span class="text-sm text-gray-600">网页生成</span></div>
                    </div>
                    <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4 text-sm">
                        <div class="bg-gray-50 p-3 rounded">首次访问：<span id="managedStatsFirst">-</span></div>
                        <div class="bg-gray-50 p-3 rounded">最后访问：<span id="managedStatsLast">-</span></div>
                    </div>
                    <div class="table-container">
                        <table class="w-full border-collapse text-sm">
                            <thead>
                                <tr>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">时间</th>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">类型</th>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">客户端IP</th>
                                    <th class="p-3 text-left bg-gray-50 text-dark font-semibold">User Agent</th>
                                </tr>
                            </thead>
                            <tbody id="managedStatsLogs">
                                <tr><td colspan="4" class="p-5 text-center">暂无数据</td></tr>
                            </tbody>
                        </table>
                    </div>
                </div>

                <div id="uuidConfigAddPanel" style="display:none;">
                    <div class="mb-4">
                        <label class="block text-sm font-medium text-primary mb-1.5">域名托管</label>
                        <select id="addUuidConfigHosting"
                                class="w-full px-3 py-2 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                            <option value="Cloudflare">Cloudflare</option>
                            <option value="腾讯云EdgeOne">腾讯云EdgeOne</option>
                            <option value="腾讯Edgeone">腾讯Edgeone</option>
                            <option value="阿里ESA">阿里ESA</option>
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
                    </div>
                    <div class="mb-4">
                        <label class="block text-sm font-medium text-primary mb-1.5">节点配置（可一次粘贴多行）</label>
                        <textarea id="addUuidConfigData" rows="8" placeholder="vmess://...&#10;vless://..."
                                  class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary font-mono text-sm"></textarea>
                    </div>
                    <button class="nav-btn active" onclick="addManagedUuidConfig()">
                        <i data-lucide="plus" class="w-4 h-4 mr-1"></i>
                        添加新节点
                    </button>
                </div>
            </div>
        </div>
    </div>

    <!-- 单条配置编辑弹窗 -->
    <div class="modal-overlay" id="editConfigModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal" style="max-width: 760px;">
            <h3 class="text-lg font-semibold text-dark mb-4">编辑节点配置</h3>
            <input type="hidden" id="editConfigId">
            <div class="mb-4">
                <label class="block text-sm font-medium text-primary mb-1.5">域名托管</label>
                <select id="editConfigHosting" class="w-full px-3 py-2 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                    <option value="Cloudflare">Cloudflare</option>
                    <option value="腾讯云EdgeOne">腾讯云EdgeOne</option>
                    <option value="腾讯Edgeone">腾讯Edgeone</option>
                    <option value="阿里ESA">阿里ESA</option>
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
            </div>
            <textarea id="editConfigData" rows="7" class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary font-mono text-sm mb-4"></textarea>
            <div class="flex justify-end gap-2.5">
                <button class="nav-btn" onclick="document.getElementById('editConfigModal').style.display='none'">取消</button>
                <button class="nav-btn active" onclick="saveManagedUuidConfig()">保存修改</button>
            </div>
        </div>
    </div>

    <!-- 模板编辑弹窗 -->
    <div class="modal-overlay" id="editTemplateModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 class="text-lg font-semibold text-dark mb-4">✏️ 编辑模板</h3>
            <input type="hidden" id="editTemplateId">
            <div class="mb-5">
                <div class="mb-4">
                    <label class="block text-sm font-medium text-primary mb-1.5">
                        模板名称 <span class="text-danger">*</span>
                    </label>
                    <input type="text" id="editTemplateName" placeholder="请输入模板名称" required
                           class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                </div>
                
                <div class="mb-4">
                    <label class="block text-sm font-medium text-primary mb-1.5">
                        模板URL <span class="text-danger">*</span>
                    </label>
                    <input type="text" id="editTemplateUrl" placeholder="请输入模板URL" required
                           class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                </div>
                
                <div class="mb-4">
                    <label class="block text-sm font-medium text-primary mb-1.5">
                        模板描述
                    </label>
                    <input type="text" id="editTemplateDesc" placeholder="请输入模板描述（可选）"
                           class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                </div>
                
                <div class="flex items-center gap-2 mt-2.5">
                    <input type="checkbox" id="editTemplateDefault" class="w-4 h-4">
                    <label for="editTemplateDefault" class="text-sm text-primary cursor-pointer">
                        设为默认模板（只能有一个默认模板）
                    </label>
                </div>
            </div>
            
            <div class="flex justify-end gap-2.5">
                <button class="nav-btn" onclick="document.getElementById('editTemplateModal').style.display='none'">
                    <i data-lucide="x" class="w-4 h-4 mr-1"></i>
                    取消
                </button>
                <button class="nav-btn active" onclick="updateTemplate()" id="updateTemplateBtn">
                    <i data-lucide="check" class="w-4 h-4 mr-1"></i>
                    保存修改
                </button>
            </div>
        </div>
    </div>

    <!-- 编辑UUID信息弹窗 -->
    <div class="modal-overlay" id="editUuidModal" onclick="event.target===this && (this.style.display='none')">
        <div class="modal">
            <h3 class="text-lg font-semibold text-dark mb-4">编辑UUID信息</h3>
            <input type="hidden" id="editUuid">
            <div class="mb-4">
                <label class="block text-sm text-primary mb-1.5">所属人姓名/标识:</label>
                <input type="text" id="editOwner" required
                       class="w-full mb-3 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                <label class="block text-sm text-primary mb-1.5">邮箱地址:</label>
                <input type="email" id="editEmail" required
                       class="w-full mb-3 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                <label class="block text-sm text-primary mb-1.5">其他联系信息:</label>
                <input type="text" id="editContactInfo"
                       class="w-full mb-3 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary">
                <label class="block text-sm text-primary mb-1.5">状态:</label>
                <select id="editStatus"
                       class="w-full mb-3 px-3 py-2 border-2 border-secondary rounded bg-white focus:outline-none focus:border-primary">
                    <option value="active">活跃</option>
                    <option value="inactive">停用</option>
                    <option value="suspended">暂停</option>
                </select>
                <label class="block text-sm text-primary mb-1.5">备注:</label>
                <textarea id="editUuidRemark" rows="3"
                         class="w-full px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary"></textarea>
            </div>
            <div class="flex justify-end gap-2.5">
                <button class="nav-btn" onclick="document.getElementById('editUuidModal').style.display='none'">
                    <i data-lucide="x" class="w-4 h-4 mr-1"></i>
                    取消
                </button>
                <button class="nav-btn active" onclick="saveUuidInfo()">
                    <i data-lucide="check" class="w-4 h-4 mr-1"></i>
                    保存
                </button>
            </div>
        </div>
    </div>

    <script>
        const token = localStorage.getItem('token');
        if (!token) window.location.href = '/login';

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

        // 初始化后刷新 Lucide 图标
        function refreshLucideIcons() {
            if (typeof lucide !== 'undefined') {
                lucide.createIcons();
            }
        }

        // ============ 状态管理 ============
        let domState = { page: 1, size: 10, total: 0, sort: 'id', order: 'desc' };
        let ipState = { page: 1, size: 10, total: 0, sort: 'created_at', order: 'desc' };
        let uuidState = { page: 1, size: 10, total: 0, sort: 'updated_at', order: 'desc' };
        let uuidConfigState = { uuid: '', page: 1, size: 10, total: 0, totalPages: 1, protocol: 'all', domain_hosting: 'all' };
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
                    <div class="text-center text-danger">
                        <p class="mb-2">初始化失败</p>
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
                document.querySelectorAll('#domPager, #ipPager, #uuidPager, #templatePager').forEach(pager => {
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
                const activeBtn = document.querySelector('.nav-btn.active');
                if (activeBtn) {
                    activeBtn.classList.remove('active');
                }
                const newBtn = document.querySelector(\`.nav-btn[onclick*="switchTab('\${id}'"]\`);
                if (newBtn) {
                    newBtn.classList.add('active');
                }
            } else {
                document.querySelectorAll('.nav-btn').forEach(b => b.classList.remove('active'));
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
                    '<span class="text-success">已启用</span>' : 
                    '<span class="text-danger">已关闭</span>';
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
                container.innerHTML = '<div class="text-center py-5 text-gray-500">暂无访问数据</div>';
                return;
            }
            
            let html = '';
            uuids.forEach((item, index) => {
                const subscriptionPercent = item.access_count > 0 ? 
                    Math.round(item.subscription_count / item.access_count * 100) : 0;
                const apigenPercent = item.access_count > 0 ? 
                    Math.round(item.apigen_count / item.access_count * 100) : 0;
                
                html += \`
                    <div class="popular-uuids-item" onclick="showUUIDDetails('\${item.uuid}')">
                        <div>
                            <span class="popular-uuids-uuid" title="\${item.uuid}">\${item.uuid}</span>
                            <div class="text-xs text-gray-500 mt-0.5">
                                \${subscriptionPercent}%订阅 | \${apigenPercent}%网页
                            </div>
                        </div>
                        <div>
                            <span class="popular-uuids-count">\${item.access_count}</span>
                            <span class="text-sm text-gray-400">次</span>
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
            modalAccessLogs.innerHTML = '<tr><td colspan="4" class="p-5 text-center">加载中...</td></tr>';
            
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
                                <td class="p-2">\${fmtDate(log.created_at)}</td>
                                <td class="p-2"><span class="type-badge \${typeClass}">\${typeText}</span></td>
                                <td class="p-2">\${log.client_ip || '未知'}</td>
                                <td class="p-2 max-w-[200px] truncate" title="\${log.user_agent}">\${log.user_agent || '未知'}</td>
                            </tr>
                        \`;
                    });
                    modalAccessLogs.innerHTML = logsHTML;
                } else {
                    modalAccessLogs.innerHTML = '<tr><td colspan="4" class="p-5 text-center">暂无访问记录</td></tr>';
                }
            } else {
                toast('无法加载UUID详情', 'error');
                modalTotalAccess.textContent = '0';
                modalAccessLogs.innerHTML = '<tr><td colspan="4" class="p-5 text-center">加载失败</td></tr>';
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
                    <div>双重验证: <span class="text-success">已启用</span></div>
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
                    <div class="text-success font-bold mb-2.5">
                        ✅ 双重验证已启用
                    </div>
                    <div class="mb-1">上次双重验证登录: \${lastLogin}</div>
                    <div class="mb-3">剩余备份码: <span class="font-bold">\${mfaStatus.backup_codes}</span> 个</div>
                    <div class="mt-4">
                        <button class="nav-btn small active mr-2" onclick="showBackupCodes()" \${mfaStatus.backup_codes === 0 ? 'disabled' : ''}>
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
                    <div class="text-danger font-bold mb-2.5">
                        ⚠️ 双重验证未启用
                    </div>
                    <p class="text-sm text-gray-500 my-2.5">
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
                    <h4 class="font-semibold text-blue-800 mb-2">步骤 1: 输入密钥</h4>
                    <p class="text-sm text-gray-500 mb-3">
                        使用身份验证器App（如Google Authenticator、Microsoft Authenticator、Authy等）：
                    </p>
                    
                    <div class="qr-code-container">
                        <div class="secret-display">
                            \${currentMfaSecret} 
                            <button class="ml-2.5 px-2 py-1 bg-gray-700 text-white border-none rounded text-xs cursor-pointer hover:bg-gray-600 transition" onclick="copyToClipboard('\${currentMfaSecret}')">复制</button>
                        </div>
                        
                        <div class="qr-instructions text-sm text-gray-600 mt-3">
                            <ol class="list-decimal pl-5 space-y-1">
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
                    
                    <div class="text-center my-5">
                        <button class="nav-btn active mr-2" onclick="showMfaSetupStep2()">我已添加账户，继续</button>
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
                    <h4 class="font-semibold text-blue-800 mb-2">步骤 2: 验证</h4>
                    <p class="text-sm text-gray-500 mb-3">
                        请在输入框中输入身份验证器App生成的6位验证码：
                    </p>
                    
                    <div class="text-center my-5">
                        <div class="flex gap-2.5 justify-center my-5">
                            <input type="text" id="verifyTOTPInput" maxlength="6" 
                                   pattern="[0-9]{6}" inputmode="numeric" 
                                   class="totp-input w-36 px-3 py-2 border-2 border-secondary rounded focus:outline-none focus:border-primary text-xl"
                                   placeholder="000000">
                        </div>
                        
                        <div class="flex gap-2.5 justify-center">
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
                        body { font-family: Arial, sans-serif; padding: 1.25rem; }
                        h1 { color: #333; }
                        .codes { font-family: monospace; font-size: 1.2em; line-height: 2; }
                        .code { display: inline-block; margin: 0.3125rem; padding: 0.3125rem 0.625rem; border: 1px solid #ccc; }
                        .warning { color: #d00; background: #fee; padding: 0.625rem; border-radius: 0.3125rem; margin: 1.25rem 0; }
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
                err.style.display = 'block';
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
                         '<td class="p-3">' + i.domain + '</td>' +
                         '<td class="p-3">' + (i.remark || '<span class="text-gray-300">无</span>') + '</td>' +
                         '<td class="p-3"><span class="hosting-badge ' + (i.source === 'Cloudflare' ? 'hosting-cloudflare' : i.source === '腾讯云EdgeOne' ? 'hosting-tencent' : '') + '">' + i.source + '</span></td>' +
                         '<td class="p-3">' + fmtDate(i.created_at) + '</td>' +
                         '<td class="p-3">' +
                         '<button class="nav-btn small mr-1" onclick="editD(' + index + ', \\'' + domainSafe + '\\', \\'' + remarkSafe + '\\', \\'' + sourceSafe + '\\')">编辑</button>' +
                         '<button class="nav-btn danger small" onclick="delD(' + index + ', \\'' + sourceSafe + '\\', \\'' + domainSafe + '\\')">删除</button>' +
                         '</td>' +
                         '</tr>';
                });
                document.getElementById('domList').innerHTML = h || '<tr><td colspan="5" class="p-3 text-center">无数据</td></tr>';
                domState.total = d.total || 0;
                updatePager('dom', domState, loadDom);
                refreshLucideIcons();
            } else {
                // 即使加载失败，也更新分页信息
                updatePager('dom', domState, loadDom);
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

        // ============ 完整的分页器更新函数 ============
        function updatePager(prefix, state, callback) {
            const pager = document.getElementById(\`\${prefix}Pager\`);
            if (!pager) return;
            
            const maxPage = Math.ceil(state.total / state.size) || 1;
            const currentPage = state.page;
            
            // 更新总数显示
            const totalInfo = document.getElementById(\`\${prefix}TotalInfo\`);
            if (totalInfo) totalInfo.innerText = \`共 \${state.total} 条\`;

            // 控制翻页按钮状态
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

            // 生成数字页码
            const numbersContainer = document.getElementById(\`\${prefix}PageNumbers\`);
            if (numbersContainer) {
                numbersContainer.innerHTML = '';
                
                // 简易页码逻辑：显示当前页附近的页码
                let startPage = Math.max(1, currentPage - 2);
                let endPage = Math.min(maxPage, startPage + 4);
                
                if (endPage - startPage < 4) {
                    startPage = Math.max(1, endPage - 4);
                }

                // 添加第一页按钮（如果不在范围内）
                if (startPage > 1) {
                    const firstBtn = document.createElement('button');
                    firstBtn.className = 'nav-btn small min-w-[32px]';
                    firstBtn.innerText = '1';
                    firstBtn.onclick = () => {
                        if (1 !== state.page) {
                            state.page = 1;
                            callback();
                        }
                    };
                    numbersContainer.appendChild(firstBtn);
                    
                    if (startPage > 2) {
                        const dots = document.createElement('span');
                        dots.className = 'text-secondary mx-1';
                        dots.innerText = '...';
                        numbersContainer.appendChild(dots);
                    }
                }

                // 添加页码按钮
                for (let i = startPage; i <= endPage; i++) {
                    const btn = document.createElement('button');
                    btn.className = \`nav-btn small min-w-[32px] \${i === currentPage ? 'active' : ''}\`;
                    btn.innerText = i;
                    btn.onclick = () => {
                        if (i !== state.page) {
                            state.page = i;
                            callback();
                        }
                    };
                    numbersContainer.appendChild(btn);
                }

                // 添加最后一页按钮（如果不在范围内）
                if (endPage < maxPage) {
                    if (endPage < maxPage - 1) {
                        const dots = document.createElement('span');
                        dots.className = 'text-secondary mx-1';
                        dots.innerText = '...';
                        numbersContainer.appendChild(dots);
                    }
                    
                    const lastBtn = document.createElement('button');
                    lastBtn.className = 'nav-btn small min-w-[32px]';
                    lastBtn.innerText = maxPage;
                    lastBtn.onclick = () => {
                        if (maxPage !== state.page) {
                            state.page = maxPage;
                            callback();
                        }
                    };
                    numbersContainer.appendChild(lastBtn);
                }
            }
            
            // 更新页码选择器
            const sizeSelect = document.getElementById(\`\${prefix}Size\`);
            if (sizeSelect) sizeSelect.value = state.size;
            
            // 始终显示分页栏
            pager.style.display = 'flex';
            
            // 如果只有一页或没有数据，隐藏翻页按钮但保留分页信息
            if (maxPage <= 1) {
                if (prevBtn) prevBtn.style.display = 'none';
                if (nextBtn) nextBtn.style.display = 'none';
                if (numbersContainer) numbersContainer.style.display = 'none';
                if (totalInfo) totalInfo.innerText = \`共 \${state.total} 条\`;
            } else {
                if (prevBtn) prevBtn.style.display = 'inline-flex';
                if (nextBtn) nextBtn.style.display = 'inline-flex';
                if (numbersContainer) numbersContainer.style.display = 'flex';
            }
            
            // 刷新图标（因为按钮里有图标）
            refreshLucideIcons();
        }
        
        function changePage(prefix, delta, state, callback) {
            const maxPage = Math.ceil(state.total / state.size) || 1;
            const newPage = state.page + delta;
            
            if (newPage > 0 && newPage <= maxPage) {
                state.page = newPage;
                if (callback) callback();
                
                const card = document.getElementById(prefix === 'template' ? 'templates' : 
                                                    prefix === 'dom' ? 'dom' : 
                                                    prefix === 'ip' ? 'ips' : 
                                                    prefix === 'uuid' ? 'uuids' : prefix);
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
                        <td class="p-3">\${i.ip}</td>
                        <td class="p-3">\${i.ip_type}</td>
                        <td class="p-3">\${i.carrier}</td>
                        <td class="p-3">\${sourceDisplay}</td>
                        <td class="p-3">\${fmtDate(i.created_at)}</td>
                        <td class="p-3"><button class="nav-btn danger small" onclick="delI('\${i.ip}')">删除</button></td>
                    </tr>\`;
                });
                document.getElementById('ipList').innerHTML = h || '<tr><td colspan="6" class="p-3 text-center">无数据</td></tr>';
                ipState.total = d.total || 0;
                updatePager('ip', ipState, loadIp);
                refreshLucideIcons();
            } else {
                // 即使加载失败，也更新分页信息
                updatePager('ip', ipState, loadIp);
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
                    const owner = i.owner || '<span class="text-gray-300">未设置</span>';
                    const email = i.email || '<span class="text-gray-300">未设置</span>';
                    const status = i.status || 'active';
                    
                    // 状态显示样式
                    let statusBadge = '';
                    if (status === 'active') {
                        statusBadge = '<span class="bg-success text-white px-2 py-0.5 rounded-full text-xs">活跃</span>';
                    } else if (status === 'inactive') {
                        statusBadge = '<span class="bg-gray-500 text-white px-2 py-0.5 rounded-full text-xs">停用</span>';
                    } else if (status === 'suspended') {
                        statusBadge = '<span class="bg-danger text-white px-2 py-0.5 rounded-full text-xs">暂停</span>';
                    } else {
                        statusBadge = \`<span class="bg-gray-300 text-white px-2 py-0.5 rounded-full text-xs">\${status}</span>\`;
                    }
                    
                    const safeUUID = (i.uuid || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeOwner = (i.owner || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeEmail = (i.email || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeContactInfo = (i.contact_info || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeRemark = (i.remark || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    const safeStatus = (i.status || 'active').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                    
                    h += \`
                    <tr>
                        <td class="p-3 font-mono text-sm">\${i.uuid}</td>
                        <td class="p-3">\${owner}</td>
                        <td class="p-3">\${email}</td>
                        <td class="p-3">\${statusBadge}</td>
                        <td class="p-3">\${i.count} 个配置</td>
                        <td class="p-3">\${fmtDate(i.updated_at)}</td>
                        <td class="p-3">
                            <div class="flex gap-1 flex-wrap">
                                <button class="nav-btn small active" onclick="openUuidConfigManager('\${safeUUID}')">配置管理</button>
                                <button class="nav-btn small" onclick="editUuidInfo('\${safeUUID}', '\${safeOwner}', '\${safeEmail}', '\${safeContactInfo}', '\${safeStatus}', '\${safeRemark}')">编辑信息</button>
                                <button class="nav-btn danger small" onclick="delU('\${i.uuid}')">删除整组</button>
                            </div>
                        </td>
                    </tr>\`;
                });
                document.getElementById('uuidList').innerHTML = h || '<tr><td colspan="7" class="p-3 text-center">无数据</td></tr>';
                uuidState.total = d.total || 0;
                updatePager('uuid', uuidState, loadUuid);
                refreshLucideIcons();
            } else {
                // 即使加载失败，也更新分页信息
                document.getElementById('uuidList').innerHTML = '<tr><td colspan="7" class="p-5 text-center">加载失败，请刷新重试</td></tr>';
                updatePager('uuid', uuidState, loadUuid);
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

        function escapeHtml(value) {
            return String(value ?? '')
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;')
                .replace(/'/g, '&#39;');
        }

        function escapeAttr(value) {
            return escapeHtml(value).replace(/\`/g, '&#96;');
        }

        function openUuidConfigManager(uuid) {
            uuidConfigState = { uuid, page: 1, size: 10, total: 0, totalPages: 1, protocol: 'all', domain_hosting: 'all' };
            document.getElementById('managedUuidText').textContent = uuid;
            document.getElementById('uuidConfigProtocolFilter').value = 'all';
            document.getElementById('uuidConfigHostingFilter').value = 'all';
            document.getElementById('uuidConfigSize').value = '10';
            document.getElementById('addUuidConfigData').value = '';
            document.getElementById('uuidConfigModal').style.display = 'flex';
            switchUuidConfigTab('list');
            loadUuidConfigs();
            loadManagedUuidStats();
        }

        function closeUuidConfigManager() {
            document.getElementById('uuidConfigModal').style.display = 'none';
            uuidConfigState.uuid = '';
        }

        function switchUuidConfigTab(tab) {
            const panels = {
                list: 'uuidConfigListPanel',
                stats: 'uuidConfigStatsPanel',
                add: 'uuidConfigAddPanel'
            };
            const buttons = {
                list: 'uuidConfigListTabBtn',
                stats: 'uuidConfigStatsTabBtn',
                add: 'uuidConfigAddTabBtn'
            };

            Object.keys(panels).forEach(key => {
                document.getElementById(panels[key]).style.display = key === tab ? 'block' : 'none';
                document.getElementById(buttons[key]).classList.toggle('active', key === tab);
            });

            if (tab === 'list') loadUuidConfigs();
            if (tab === 'stats') loadManagedUuidStats();
            setTimeout(refreshLucideIcons, 50);
        }

        async function loadUuidConfigs() {
            if (!uuidConfigState.uuid) return;

            uuidConfigState.protocol = document.getElementById('uuidConfigProtocolFilter').value;
            uuidConfigState.domain_hosting = document.getElementById('uuidConfigHostingFilter').value;
            uuidConfigState.size = parseInt(document.getElementById('uuidConfigSize').value) || 10;

            const query = 'uuid-configs?uuid=' + encodeURIComponent(uuidConfigState.uuid) +
                '&page=' + uuidConfigState.page +
                '&size=' + uuidConfigState.size +
                '&protocol=' + encodeURIComponent(uuidConfigState.protocol) +
                '&domain_hosting=' + encodeURIComponent(uuidConfigState.domain_hosting);

            const list = document.getElementById('uuidConfigList');
            list.innerHTML = '<tr><td colspan="5" class="p-5 text-center">加载中...</td></tr>';

            const data = await api(query);
            if (!data || !data.success) {
                list.innerHTML = '<tr><td colspan="5" class="p-5 text-center text-danger">加载失败</td></tr>';
                return;
            }

            uuidConfigState.total = data.total || 0;
            uuidConfigState.totalPages = data.totalPages || 1;
            uuidConfigState.page = data.page || 1;

            if (!data.data || data.data.length === 0) {
                list.innerHTML = '<tr><td colspan="5" class="p-5 text-center text-gray-500">暂无配置，请添加新节点</td></tr>';
            } else {
                let html = '';
                data.data.forEach(row => {
                    const configJson = escapeAttr(JSON.stringify(row));
                    const hosting = row.domain_hosting || 'Cloudflare';
                    html += '<tr>' +
                        '<td class="p-3">' + (row.remark ? escapeHtml(row.remark) : '<span class="text-gray-300">无</span>') + '</td>' +
                        '<td class="p-3 font-mono text-sm">' + escapeHtml(row.protocol || 'unknown') + '</td>' +
                        '<td class="p-3"><span class="hosting-badge ' + (hosting === 'Cloudflare' ? 'hosting-cloudflare' : hosting.includes('腾讯') ? 'hosting-tencent' : 'badge-normal') + '">' + escapeHtml(hosting) + '</span></td>' +
                        '<td class="p-3 config-data-cell font-mono text-xs" title="' + escapeAttr(row.config_data || '') + '">' + escapeHtml((row.config_data || '').slice(0, 96)) + ((row.config_data || '').length > 96 ? '...' : '') + '</td>' +
                        '<td class="p-3"><div class="flex gap-1 flex-wrap">' +
                        '<button class="nav-btn small" data-config="' + configJson + '" onclick="openManagedConfigEdit(JSON.parse(this.dataset.config))">编辑</button>' +
                        '<button class="nav-btn danger small" onclick="deleteManagedUuidConfig(' + row.id + ')">删除</button>' +
                        '</div></td>' +
                        '</tr>';
                });
                list.innerHTML = html;
            }

            updateUuidConfigPager();
            refreshLucideIcons();
        }

        function updateUuidConfigPager() {
            const totalPages = uuidConfigState.totalPages || 1;
            document.getElementById('uuidConfigTotalInfo').textContent = '共 ' + uuidConfigState.total + ' 条';
            document.getElementById('uuidConfigPageInfo').textContent = '第 ' + uuidConfigState.page + ' / ' + totalPages + ' 页';
            const prevBtn = document.getElementById('uuidConfigPrevBtn');
            const nextBtn = document.getElementById('uuidConfigNextBtn');
            prevBtn.disabled = uuidConfigState.page <= 1;
            nextBtn.disabled = uuidConfigState.page >= totalPages;
            prevBtn.style.opacity = prevBtn.disabled ? '0.5' : '1';
            nextBtn.style.opacity = nextBtn.disabled ? '0.5' : '1';
        }

        function applyUuidConfigFilters() {
            uuidConfigState.page = 1;
            loadUuidConfigs();
        }

        function changeUuidConfigSize() {
            uuidConfigState.page = 1;
            loadUuidConfigs();
        }

        function changeUuidConfigPage(delta) {
            const nextPage = uuidConfigState.page + delta;
            if (nextPage < 1 || nextPage > uuidConfigState.totalPages) return;
            uuidConfigState.page = nextPage;
            loadUuidConfigs();
        }

        async function addManagedUuidConfig() {
            const configData = document.getElementById('addUuidConfigData').value.trim();
            const hosting = document.getElementById('addUuidConfigHosting').value;
            if (!uuidConfigState.uuid || !configData) {
                toast('UUID或配置内容不能为空', 'error');
                return;
            }

            const res = await api('uuid-configs', 'POST', {
                uuid: uuidConfigState.uuid,
                config_data: configData,
                domain_hosting: hosting
            });

            if (res && res.success) {
                toast('已添加 ' + (res.inserted || 0) + ' 个节点');
                document.getElementById('addUuidConfigData').value = '';
                uuidConfigState.page = 1;
                switchUuidConfigTab('list');
                loadUuid();
                loadStats();
            }
        }

        function openManagedConfigEdit(config) {
            document.getElementById('editConfigId').value = config.id;
            document.getElementById('editConfigHosting').value = config.domain_hosting || 'Cloudflare';
            document.getElementById('editConfigData').value = config.config_data || '';
            document.getElementById('editConfigModal').style.display = 'flex';
            setTimeout(() => document.getElementById('editConfigData').focus(), 100);
        }

        async function saveManagedUuidConfig() {
            const id = document.getElementById('editConfigId').value;
            const configData = document.getElementById('editConfigData').value.trim();
            const hosting = document.getElementById('editConfigHosting').value;
            if (!id || !configData) {
                toast('配置内容不能为空', 'error');
                return;
            }

            const res = await api('uuid-configs', 'PUT', {
                id,
                config_data: configData,
                domain_hosting: hosting
            });

            if (res && res.success) {
                toast('配置已更新');
                document.getElementById('editConfigModal').style.display = 'none';
                loadUuidConfigs();
                loadUuid();
            }
        }

        async function deleteManagedUuidConfig(id) {
            if (!confirm('确认删除此节点配置?')) return;
            const res = await api('uuid-configs', 'DELETE', { id });
            if (res && res.success) {
                toast('配置已删除');
                loadUuidConfigs();
                loadUuid();
                loadStats();
            }
        }

        async function clearManagedUuidConfigs() {
            if (!uuidConfigState.uuid) return;
            if (!confirm('确认清空此分组的所有配置?')) return;
            const res = await api('uuid-configs', 'DELETE', { uuid: uuidConfigState.uuid });
            if (res && res.success) {
                toast('分组配置已清空');
                uuidConfigState.page = 1;
                loadUuidConfigs();
                loadUuid();
                loadStats();
            }
        }

        async function loadManagedUuidStats() {
            if (!uuidConfigState.uuid) return;
            const days = document.getElementById('uuidConfigStatsDays').value || 30;
            const data = await api('uuid-configs/stats?uuid=' + encodeURIComponent(uuidConfigState.uuid) + '&days=' + days);
            if (!data || !data.success) return;

            document.getElementById('managedStatsTotal').textContent = data.total_access || 0;
            document.getElementById('managedStatsToday').textContent = data.today_access || 0;
            document.getElementById('managedStatsSub').textContent = data.subscription_total || 0;
            document.getElementById('managedStatsApi').textContent = data.api_generation_total || 0;
            document.getElementById('managedStatsFirst').textContent = data.first_access ? fmtDate(data.first_access) : '-';
            document.getElementById('managedStatsLast').textContent = data.last_access ? fmtDate(data.last_access) : '-';

            const logs = document.getElementById('managedStatsLogs');
            if (!data.recent_logs || data.recent_logs.length === 0) {
                logs.innerHTML = '<tr><td colspan="4" class="p-5 text-center text-gray-500">暂无访问记录</td></tr>';
                return;
            }

            let html = '';
            data.recent_logs.forEach(log => {
                const isSub = log.query_type === 'subscription';
                html += '<tr>' +
                    '<td class="p-3">' + fmtDate(log.created_at) + '</td>' +
                    '<td class="p-3"><span class="type-badge ' + (isSub ? 'type-subscription' : 'type-apigen') + '">' + (isSub ? '订阅' : '网页') + '</span></td>' +
                    '<td class="p-3">' + escapeHtml(log.client_ip || '未知') + '</td>' +
                    '<td class="p-3 max-w-[260px] truncate" title="' + escapeAttr(log.user_agent || '') + '">' + escapeHtml(log.user_agent || '未知') + '</td>' +
                    '</tr>';
            });
            logs.innerHTML = html;
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
                    templateState.total = d.total || 0;
                    if (d.data && d.data.length > 0) {
                        let html = '';
                        d.data.forEach(i => {
                            const createdAt = fmtDate(i.created_at);
                            const defaultBadge = i.is_default === 1 ? 
                                '<span class="template-badge badge-default">默认</span>' : 
                                '<span class="template-badge badge-normal">普通</span>';
                            
                            const description = i.description ? i.description : '<span class="text-gray-300">无描述</span>';
                            
                            const safeName = (i.name || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                            const safeUrl = (i.url || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                            const safeDesc = (i.description || '').replace(/'/g, "&#39;").replace(/"/g, "&quot;");
                            
                            html += \`<tr>
                                <td class="p-3"><strong>\${i.name}</strong></td>
                                <td class="p-3 template-url-cell" title="\${i.url}">\${i.url}</td>
                                <td class="p-3">\${description}</td>
                                <td class="p-3">\${defaultBadge}</td>
                                <td class="p-3">\${createdAt}</td>
                                <td class="p-3">
                                    <div class="flex gap-1 flex-wrap">
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
                        updatePager('template', templateState, loadTemplates);
                        refreshLucideIcons();
                    } else {
                        list.innerHTML = '<tr><td colspan="6" class="p-5 text-center text-secondary">暂无模板数据，请添加第一个模板</td></tr>';
                        updatePager('template', templateState, loadTemplates);
                    }
                } else {
                    list.innerHTML = '<tr><td colspan="6" class="p-5 text-center text-danger">加载失败，请刷新重试</td></tr>';
                    updatePager('template', templateState, loadTemplates);
                }
            } catch (error) {
                console.error('加载模板失败:', error);
                list.innerHTML = '<tr><td colspan="6" class="p-5 text-center text-danger">加载失败：' + error.message + '</td></tr>';
                updatePager('template', templateState, loadTemplates);
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
                btn.innerHTML = '<i data-lucide="loader" class="w-4 h-4 animate-spin mr-1"></i>保存中...';
                
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
