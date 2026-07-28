/* =================================================================
 *  Cloudflare Worker: Configuration Management with MFA & Templates
 *  前端重构版本：React 18 + Ant Design 5.x + Ant Design Icons 6.x + Ant Design Charts
 * ================================================================= */

// =================================================================
//  CONSTANTS (常量定义)
// =================================================================
const DEFAULT_JWT_SECRET = 'your-default-jwt-secret-change-this';
const CACHE_CONTROL = 'no-cache, no-store, must-revalidate';

// =================================================================
//  UTILITY FUNCTIONS & DB INITIALIZATION (保持原有后端逻辑不变)
// =================================================================

async function hashPassword(password) {
    const msgBuffer = new TextEncoder().encode(password);
    const hashBuffer = await crypto.subtle.digest('SHA-256', msgBuffer);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
}

async function signJwt(payload, secret = DEFAULT_JWT_SECRET) {
    const header = { alg: 'HS256', typ: 'JWT' };
    const encodedHeader = btoa(JSON.stringify(header)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
    const encodedPayload = btoa(JSON.stringify(payload)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
    const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
    const signature = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(`${encodedHeader}.${encodedPayload}`));
    const encodedSignature = btoa(String.fromCharCode(...new Uint8Array(signature))).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
    return `${encodedHeader}.${encodedPayload}.${encodedSignature}`;
}

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

function formatDate(timestamp) {
    if (!timestamp || timestamp === 0) return '从未';
    const date = new Date(timestamp);
    return date.toLocaleString('zh-CN', {
        year: 'numeric', month: '2-digit', day: '2-digit',
        hour: '2-digit', minute: '2-digit'
    });
}

function b64ToUtf8(str) {
    try {
        return decodeURIComponent(atob(str).split('').map(c => '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2)).join(''));
    } catch (e) { throw new Error('无效的 Base64 字符串，无法解码 VMess 配置。'); }
}

function getProtocol(configStr) {
    if (!configStr || typeof configStr !== 'string') return 'unknown';
    const protocolPatterns = [
        { prefix: 'vmess://', type: 'vmess' }, { prefix: 'vless://', type: 'vless' },
        { prefix: 'trojan://', type: 'trojan' }, { prefix: 'ss://', type: 'shadowsocks' },
        { prefix: 'hysteria2://', type: 'hysteria2' }, { prefix: 'tuic://', type: 'tuic' },
        { prefix: 'anytls://', type: 'anytls' }, { prefix: 'any-reality://', type: 'any-reality' },
        { prefix: 'anyreality://', type: 'anyreality' }, { prefix: 'reality://', type: 'reality' },
        { prefix: 'socks5://', type: 'socks5' }, { prefix: 'socks://', type: 'socks5' }
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
        if (['vless', 'trojan', 'hysteria2', 'tuic', 'anytls', 'socks5', 'any-reality', 'anyreality', 'reality', 'shadowsocks'].includes(protocol)) {
            const url = new URL(configStr);
            if (url.hash) return decodeURIComponent(url.hash.substring(1));
        }
    } catch (e) { }
    return null;
}

async function getAccessStatsSummary(env, days = 30) {
    try {
        const db = env.DB;
        if (!db) throw new Error("D1数据库未绑定。");

        const totalStats = await db.prepare(
            'SELECT COUNT(*) as total, COUNT(DISTINCT uuid) as unique_uuids, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription_total, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as apigen_total ' +
            'FROM config_access_logs'
        ).first();

        const today = new Date().toISOString().split('T')[0];
        const todayStats = await db.prepare(
            'SELECT COUNT(*) as today_total, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as today_subscription, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as today_apigen ' +
            'FROM config_access_logs WHERE DATE(created_at) = ?'
        ).bind(today).first();

        const startDate = new Date();
        startDate.setDate(startDate.getDate() - days);
        const startDateStr = startDate.toISOString().split('T')[0];

        const dailyStats = await db.prepare(
            'SELECT DATE(created_at) as date, COUNT(*) as total, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as api_generation, ' +
            'COUNT(DISTINCT uuid) as unique_uuids FROM config_access_logs ' +
            'WHERE DATE(created_at) >= ? GROUP BY DATE(created_at) ORDER BY date DESC'
        ).bind(startDateStr).all();

        const popularUUIDs = await db.prepare(
            'SELECT uuid, COUNT(*) as access_count, ' +
            'SUM(CASE WHEN query_type = "subscription" THEN 1 ELSE 0 END) as subscription_count, ' +
            'SUM(CASE WHEN query_type = "api-generation" THEN 1 ELSE 0 END) as apigen_count ' +
            'FROM config_access_logs GROUP BY uuid ORDER BY access_count DESC LIMIT 10'
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
        return { success: false, error: e.message };
    }
}

async function getUUIDAccessDetails(env, uuid, startDate, endDate) {
    try {
        const db = env.DB;
        let query = 'SELECT uuid, query_type, client_ip, user_agent, created_at FROM config_access_logs WHERE uuid = ?';
        const params = [uuid];
        if (startDate) { query += ' AND DATE(created_at) >= ?'; params.push(startDate); }
        if (endDate) { query += ' AND DATE(created_at) <= ?'; params.push(endDate); }
        query += ' ORDER BY created_at DESC LIMIT 100';

        const { results } = await db.prepare(query).bind(...params).all();
        const uuidStats = await db.prepare(
            'SELECT COUNT(*) as total_access, MIN(created_at) as first_access, MAX(created_at) as last_access ' +
            'FROM config_access_logs WHERE uuid = ?'
        ).bind(uuid).first();

        return {
            success: true, uuid: uuid,
            total_access: uuidStats?.total_access || 0,
            first_access: uuidStats?.first_access,
            last_access: uuidStats?.last_access,
            access_logs: results || []
        };
    } catch (e) { return { success: false, error: e.message }; }
}

class TOTP {
    static generateSecret() {
        const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
        let secret = '';
        for (let i = 0; i < 20; i++) secret += chars.charAt(Math.floor(Math.random() * chars.length));
        return secret;
    }
    static generateOTP(secret, window = 0) {
        const time = Math.floor(Date.now() / 30000) + window;
        return this.generateOTPWithTime(secret, time);
    }
    static generateOTPWithTime(secret, time) {
        const key = this.base32ToBytes(secret);
        const timeBuffer = new ArrayBuffer(8);
        const timeView = new DataView(timeBuffer);
        timeView.setUint32(0, Math.floor(time / 0x100000000));
        timeView.setUint32(4, time % 0x100000000);
        const keyObj = crypto.subtle.importKey('raw', new Uint8Array(key), { name: 'HMAC', hash: 'SHA-1' }, false, ['sign']);
        return keyObj.then(k => crypto.subtle.sign('HMAC', k, timeBuffer)).then(signature => {
            const sigArray = new Uint8Array(signature);
            const offset = sigArray[sigArray.length - 1] & 0xf;
            const binary = ((sigArray[offset] & 0x7f) << 24) | ((sigArray[offset + 1] & 0xff) << 16) | ((sigArray[offset + 2] & 0xff) << 8) | (sigArray[offset + 3] & 0xff);
            return (binary % 1000000).toString().padStart(6, '0');
        });
    }
    static base32ToBytes(base32) {
        const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
        const bytes = [];
        let bits = 0, buffer = 0;
        for (let i = 0; i < base32.length; i++) {
            const value = chars.indexOf(base32.charAt(i));
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
    static async verify(secret, code, window = 1) {
        for (let i = -window; i <= window; i++) {
            if (await this.generateOTP(secret, i) === code) return true;
        }
        return false;
    }
    static generateOTPAuthURL(username, secret, issuer = 'YZFN Admin') {
        return `otpauth://totp/${encodeURIComponent(issuer)}:${encodeURIComponent(username)}?secret=${secret}&issuer=${encodeURIComponent(issuer)}&algorithm=SHA1&digits=6&period=30`;
    }
}

async function fetchIpsFromHostMonit(type = 'v4') {
    try {
        const res = await fetch('https://api.hostmonit.com/get_optimization_ip', {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ key: "iDetkOys", ...(type === 'v6' ? { type: 'v6' } : {}) })
        });
        const data = await res.json();
        if (data.code === 200 && Array.isArray(data.info)) {
            return data.info.map(i => {
                let carrier = i.line || 'ALL';
                if (carrier === 'CMI') carrier = 'CM';
                if (carrier === 'CT') carrier = 'CT';
                if (carrier === 'CU') carrier = 'CU';
                return { ip: i.ip, ip_type: i.ip.includes(':') ? 'v6' : 'v4', carrier, source: `hostmonit_${type}` };
            });
        }
        return [];
    } catch (e) { return []; }
}

async function fetchIpsFromVps789() {
    try {
        const res = await fetch('https://vps789.com/openApi/cfIpApi', { headers: { 'User-Agent': 'CF-Worker/4.0', 'Accept': 'application/json' } });
        const data = await res.json();
        const ips = [];
        if (data.code === 0 && data.data) {
            for (const k in data.data) {
                if (Array.isArray(data.data[k])) {
                    let carrier = 'ALL';
                    if (k.includes('移动') || k.includes('CM') || k === 'CM') carrier = 'CM';
                    else if (k.includes('电信') || k.includes('CT') || k === 'CT') carrier = 'CT';
                    else if (k.includes('联通') || k.includes('CU') || k === 'CU') carrier = 'CU';
                    data.data[k].forEach(i => {
                        if (i && i.ip) ips.push({ ip: i.ip, ip_type: i.ip.includes(':') ? 'v6' : 'v4', carrier, source: 'vps789' });
                    });
                }
            }
        }
        return ips;
    } catch (e) { return []; }
}

async function ensureSchema(env) {
    try {
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS cfips (id INTEGER PRIMARY KEY AUTOINCREMENT, ip TEXT NOT NULL, ip_type TEXT, carrier TEXT, source TEXT, created_at INTEGER, updated_at INTEGER)`).run();
        const info = await env.DB.prepare('PRAGMA table_info(cfips)').all();
        if (!(info.results || []).map(c => c.name).includes('updated_at')) {
            await env.DB.prepare('ALTER TABLE cfips ADD COLUMN updated_at INTEGER').run();
        }
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS auto_update_settings (source TEXT PRIMARY KEY, enabled INTEGER, updated_at INTEGER)`).run();
    } catch (e) { }
}

async function ensureDomainTables(env) {
    await env.DB.prepare(`CREATE TABLE IF NOT EXISTS cf_domains (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT UNIQUE NOT NULL, remark TEXT, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000), updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000));`).run();
    await env.DB.prepare(`CREATE TABLE IF NOT EXISTS edgeone_domains (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT UNIQUE NOT NULL, remark TEXT, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000), updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000));`).run();
    await env.DB.prepare(`CREATE TABLE IF NOT EXISTS aliyun_esa_domains (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT UNIQUE NOT NULL, remark TEXT, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000), updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000));`).run();
}

async function executeInChunks(stmts) {
    for (let i = 0; i < stmts.length; i += 10) await Promise.all(stmts.slice(i, i + 10).map(stmt => stmt.run()));
}

async function runIpUpdateTask(env, sources = null) {
    await ensureSchema(env);
    if (sources === null) {
        try {
            const settingsRes = await env.DB.prepare('SELECT source, enabled FROM auto_update_settings WHERE source IN (?, ?, ?, ?)').bind('hostmonit_v4', 'hostmonit_v6', 'vps789', 'global_enabled').all();
            sources = {};
            if (settingsRes && settingsRes.results) settingsRes.results.forEach(s => { sources[s.source] = s.enabled === 1; });
            else sources = { hostmonit_v4: true, hostmonit_v6: false, vps789: true, global_enabled: true };
        } catch (e) { sources = { hostmonit_v4: true, hostmonit_v6: false, vps789: true, global_enabled: true }; }
    }
    const tasks = [];
    if (sources.hostmonit_v4 !== false) tasks.push(fetchIpsFromHostMonit('v4'));
    if (sources.hostmonit_v6 !== false) tasks.push(fetchIpsFromHostMonit('v6'));
    if (sources.vps789 !== false) tasks.push(fetchIpsFromVps789());
    if (tasks.length === 0) return { success: false, message: "没有启用的API源" };

    const results = await Promise.allSettled(tasks);
    const allIps = [];
    const sourceStats = {};
    results.forEach((result, index) => {
        const name = ['hostmonit_v4', 'hostmonit_v6', 'vps789'][index];
        if (result.status === 'fulfilled') { allIps.push(...result.value); sourceStats[name] = { count: result.value.length, status: 'ok' }; }
        else sourceStats[name] = { count: 0, status: 'error' };
    });

    const uniqueMap = new Map();
    allIps.forEach(i => {
        if (i && i.ip) {
            const key = `${i.ip.trim()}_${i.carrier || 'ALL'}`;
            if (!uniqueMap.has(key)) uniqueMap.set(key, { ...i, ip: i.ip.trim(), carrier: i.carrier || 'ALL' });
            else {
                const existing = uniqueMap.get(key);
                if (existing.source && i.source && !existing.source.includes(i.source)) existing.source += `|${i.source}`;
            }
        }
    });

    const newIpList = Array.from(uniqueMap.values()).slice(0, 100);
    if (sources.global_enabled === 0) return { success: true, count: newIpList.length, message: "自动更新已关闭" };

    try {
        await env.DB.prepare('DELETE FROM cfips').run();
        let insertedCount = 0;
        if (newIpList.length > 0) {
            const insertStmts = newIpList.map(i => env.DB.prepare('INSERT INTO cfips (ip, ip_type, carrier, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)').bind(i.ip, i.ip_type || 'v4', i.carrier, i.source, Date.now(), Date.now()));
            for (let i = 0; i < insertStmts.length; i += 100) await env.DB.batch(insertStmts.slice(i, i + 100));
            insertedCount = newIpList.length;
        }
        await env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('last_executed', 1, Date.now()).run();
        return { success: true, message: `更新成功 (插入了${insertedCount}个新IP)`, sourceStats, stats: { cleaned: true, inserted: insertedCount } };
    } catch (e) { return { success: false, message: "DB Error: " + e.message }; }
}

async function initializeDatabaseSettings(env) {
    try {
        await ensureDomainTables(env);
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS admin_users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, mfa_enabled INTEGER DEFAULT 0, mfa_secret TEXT, last_mfa_login INTEGER DEFAULT 0, last_backup_login INTEGER DEFAULT 0, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000));`).run();
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS mfa_backup_codes (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL, code TEXT UNIQUE NOT NULL, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000), used INTEGER DEFAULT 0, used_at INTEGER DEFAULT 0);`).run();
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS configs (id INTEGER PRIMARY KEY AUTOINCREMENT, uuid TEXT NOT NULL, ip TEXT, domain TEXT, port INTEGER, meta TEXT, config_data TEXT, protocol TEXT, remark TEXT, domain_hosting TEXT DEFAULT 'Cloudflare', created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000), updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000));`).run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_configs_uuid ON configs (uuid);').run();
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS cfips (id INTEGER PRIMARY KEY AUTOINCREMENT, ip TEXT UNIQUE NOT NULL, ip_type TEXT, carrier TEXT, source TEXT, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000));`).run();
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS auto_update_settings (source TEXT PRIMARY KEY, enabled INTEGER NOT NULL, updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000));`).run();
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS config_access_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, uuid TEXT NOT NULL, query_type TEXT NOT NULL, client_ip TEXT, user_agent TEXT, created_at TEXT DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%SZ', 'now')));`).run();
        await env.DB.prepare('CREATE INDEX IF NOT EXISTS idx_access_logs_uuid ON config_access_logs (uuid);').run();
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS config_templates (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE NOT NULL, url TEXT NOT NULL, description TEXT, is_default INTEGER DEFAULT 0, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000), updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000))`).run();
        await env.DB.prepare(`CREATE TABLE IF NOT EXISTS uuid_info (id INTEGER PRIMARY KEY AUTOINCREMENT, uuid TEXT UNIQUE NOT NULL, owner TEXT NOT NULL, email TEXT NOT NULL, contact_info TEXT, status TEXT DEFAULT 'active', remark TEXT, last_accessed INTEGER DEFAULT 0, access_count INTEGER DEFAULT 0, created_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000), updated_at INTEGER DEFAULT (CAST(STRFTIME('%s', 'now') AS INT) * 1000))`).run();
        
        const configInfo = await env.DB.prepare('PRAGMA table_info(configs)').all();
        const configColumns = (configInfo.results || []).map(c => c.name);
        if (!configColumns.includes('config_data')) await env.DB.prepare('ALTER TABLE configs ADD COLUMN config_data TEXT').run();
        if (!configColumns.includes('protocol')) await env.DB.prepare('ALTER TABLE configs ADD COLUMN protocol TEXT').run();
        if (!configColumns.includes('remark')) await env.DB.prepare('ALTER TABLE configs ADD COLUMN remark TEXT').run();
        if (!configColumns.includes('domain_hosting')) await env.DB.prepare("ALTER TABLE configs ADD COLUMN domain_hosting TEXT DEFAULT 'Cloudflare'").run();

        const defaultSettings = [{ source: 'global_enabled', enabled: 1 }, { source: 'hostmonit_v4', enabled: 1 }, { source: 'hostmonit_v6', enabled: 0 }, { source: 'vps789', enabled: 1 }, { source: 'last_executed', enabled: 0 }];
        for (const setting of defaultSettings) {
            await env.DB.prepare('INSERT OR IGNORE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind(setting.source, setting.enabled, Date.now()).run();
        }
    } catch (e) { console.error('Database init failed:', e.message); }
}

// =================================================================
//  API HANDLERS
// =================================================================

async function handleLogin(req, env) {
    try {
        const { username, password, totp_code, step, cf_turnstile_response } = await req.json();
        if (!step || step !== 'verify_totp') {
            if (!cf_turnstile_response) return jsonResponse({ error: '请完成人机验证' }, 400);
            const turnstileSecret = env.TURNSTILE_SECRET_KEY || '';
            if (turnstileSecret) {
                const verifyRes = await fetch('https://challenges.cloudflare.com/turnstile/v0/siteverify', {
                    method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: `secret=${encodeURIComponent(turnstileSecret)}&response=${encodeURIComponent(cf_turnstile_response)}`
                });
                const verifyData = await verifyRes.json();
                if (!verifyData.success) return jsonResponse({ error: '人机验证失败' }, 403);
            }
        }

        const user = await env.DB.prepare('SELECT * FROM admin_users WHERE username = ?').bind(username).first();
        if (!user || (await hashPassword(password)) !== user.password_hash) return jsonResponse({ error: '用户名或密码错误' }, 401);

        const requiresMFA = user.mfa_enabled === 1;
        if (!step) {
            if (!requiresMFA) {
                const token = await signJwt({ sub: user.username, exp: Date.now() + 86400000, requires_mfa: false }, env.JWT_SECRET || DEFAULT_JWT_SECRET);
                return jsonResponse({ token, requires_mfa: false });
            } else {
                return jsonResponse({ requires_mfa: true, username: user.username, step: 'totp_required' });
            }
        }
        if (step === 'verify_totp' && requiresMFA) {
            if (!totp_code) return jsonResponse({ error: '请输入验证码' }, 400);
            if (!user.mfa_secret) return jsonResponse({ error: 'MFA未设置' }, 400);
            if (!(await TOTP.verify(user.mfa_secret, totp_code, 1))) return jsonResponse({ error: '验证码无效或已过期' }, 401);
            await env.DB.prepare('UPDATE admin_users SET last_mfa_login = ? WHERE username = ?').bind(Date.now(), user.username).run();
            const token = await signJwt({ sub: user.username, exp: Date.now() + 86400000, requires_mfa: true, mfa_verified: true }, env.JWT_SECRET || DEFAULT_JWT_SECRET);
            return jsonResponse({ token, requires_mfa: true, message: 'MFA验证成功' });
        }
        return jsonResponse({ error: '无效的登录步骤' }, 400);
    } catch (e) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleMfaInit(req, env) {
    try {
        const { username, password } = await req.json();
        const user = await env.DB.prepare('SELECT * FROM admin_users WHERE username = ?').bind(username).first();
        if (!user || (await hashPassword(password)) !== user.password_hash) return jsonResponse({ error: '密码错误' }, 401);
        if (user.mfa_enabled === 1) return jsonResponse({ error: 'MFA已启用' }, 400);
        const newSecret = TOTP.generateSecret();
        const otpauth_url = TOTP.generateOTPAuthURL(username, newSecret);
        return jsonResponse({ success: true, secret: newSecret, otpauth_url: otpauth_url, qr_url: `https://qrcode.api.yangzifun.org/?data=${encodeURIComponent(otpauth_url)}&size=200` });
    } catch (error) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleMfaVerifyFirst(req, env) {
    try {
        const { username, totp_code, secret } = await req.json();
        if (!(await TOTP.verify(secret, totp_code, 1))) return jsonResponse({ error: '验证码无效' }, 400);
        const backupCodes = Array.from({ length: 10 }, () => Math.random().toString(36).substring(2, 10).toUpperCase());
        await env.DB.prepare('UPDATE admin_users SET mfa_enabled = 1, mfa_secret = ? WHERE username = ?').bind(secret, username).run();
        const stmts = backupCodes.map(code => env.DB.prepare('INSERT INTO mfa_backup_codes (username, code, created_at, used) VALUES (?, ?, ?, 0)').bind(username, code, Date.now()));
        await env.DB.batch(stmts);
        return jsonResponse({ success: true, backup_codes: backupCodes });
    } catch (error) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleMfaLoginWithBackup(req, env) {
    try {
        const { username, password, backup_code } = await req.json();
        const user = await env.DB.prepare('SELECT * FROM admin_users WHERE username = ? AND mfa_enabled = 1').bind(username).first();
        if (!user || (await hashPassword(password)) !== user.password_hash) return jsonResponse({ error: '密码错误' }, 401);
        const backupRecord = await env.DB.prepare('SELECT * FROM mfa_backup_codes WHERE username = ? AND code = ? AND used = 0').bind(username, backup_code.trim().toUpperCase()).first();
        if (!backupRecord) return jsonResponse({ error: '备份码无效' }, 401);
        await env.DB.prepare('UPDATE mfa_backup_codes SET used = 1, used_at = ? WHERE id = ?').bind(Date.now(), backupRecord.id).run();
        const token = await signJwt({ sub: user.username, exp: Date.now() + 86400000, requires_mfa: true, mfa_verified: true, via_backup_code: true }, env.JWT_SECRET || DEFAULT_JWT_SECRET);
        return jsonResponse({ token, message: '登录成功' });
    } catch (error) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleChangePassword(req, env, currentUser) {
    try {
        const { oldPassword, newPassword } = await req.json();
        const user = await env.DB.prepare('SELECT * FROM admin_users WHERE username = ?').bind(currentUser).first();
        if (!user || (await hashPassword(oldPassword)) !== user.password_hash) return jsonResponse({ error: '旧密码错误' }, 403);
        await env.DB.prepare('UPDATE admin_users SET password_hash = ? WHERE username = ?').bind(await hashPassword(newPassword), currentUser).run();
        return jsonResponse({ success: true });
    } catch (error) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleMfaStatus(req, env, currentUser) {
    try {
        const user = await env.DB.prepare('SELECT mfa_enabled, last_mfa_login, last_backup_login FROM admin_users WHERE username = ?').bind(currentUser).first();
        const count = await env.DB.prepare('SELECT COUNT(*) as count FROM mfa_backup_codes WHERE username = ? AND used = 0').bind(currentUser).first('count');
        return jsonResponse({ mfa_enabled: user.mfa_enabled === 1, last_mfa_login: user.last_mfa_login, backup_codes_remaining: count || 0 });
    } catch (error) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleMfaDisable(req, env, currentUser) {
    try {
        const { password } = await req.json();
        const user = await env.DB.prepare('SELECT * FROM admin_users WHERE username = ?').bind(currentUser).first();
        if ((await hashPassword(password)) !== user.password_hash) return jsonResponse({ error: '密码错误' }, 401);
        await env.DB.prepare('UPDATE admin_users SET mfa_enabled = 0, mfa_secret = NULL WHERE username = ?').bind(currentUser).run();
        await env.DB.prepare('DELETE FROM mfa_backup_codes WHERE username = ?').bind(currentUser).run();
        return jsonResponse({ success: true });
    } catch (error) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleMfaRegenerateBackupCodes(req, env, currentUser) {
    try {
        const { password } = await req.json();
        const user = await env.DB.prepare('SELECT * FROM admin_users WHERE username = ?').bind(currentUser).first();
        if ((await hashPassword(password)) !== user.password_hash) return jsonResponse({ error: '密码错误' }, 401);
        const codes = Array.from({ length: 10 }, () => Math.random().toString(36).substring(2, 10).toUpperCase());
        await env.DB.prepare('DELETE FROM mfa_backup_codes WHERE username = ?').bind(currentUser).run();
        const stmts = codes.map(code => env.DB.prepare('INSERT INTO mfa_backup_codes (username, code, created_at, used) VALUES (?, ?, ?, 0)').bind(currentUser, code, Date.now()));
        await env.DB.batch(stmts);
        return jsonResponse({ success: true, backup_codes: codes });
    } catch (error) { return jsonResponse({ error: '服务器错误' }, 500); }
}

async function handleGetAutoUpdateSettings(req, env) {
    const settings = await env.DB.prepare('SELECT source, enabled, updated_at FROM auto_update_settings').all();
    const result = { global_enabled: 0, hostmonit_v4: 1, hostmonit_v6: 0, vps789: 1, last_executed: 0 };
    if (settings && settings.results) {
        settings.results.forEach(s => { if (s.source === 'last_executed') result.last_executed = s.updated_at; else result[s.source] = s.enabled; });
    }
    return jsonResponse(result);
}

async function handleSetAutoUpdateSettings(req, env) {
    const { global_enabled, hostmonit_v4, hostmonit_v6, vps789 } = await req.json();
    const stmts = [
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('global_enabled', global_enabled ? 1 : 0, Date.now()),
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('hostmonit_v4', hostmonit_v4 ? 1 : 0, Date.now()),
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('hostmonit_v6', hostmonit_v6 ? 1 : 0, Date.now()),
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('vps789', vps789 ? 1 : 0, Date.now())
    ];
    await env.DB.batch(stmts);
    return jsonResponse({ success: true });
}

async function handleDirectIpUpdate(req, env) {
    const { global_enabled, hostmonit_v4, hostmonit_v6, vps789 } = await req.json();
    await env.DB.batch([
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('global_enabled', global_enabled ? 1 : 0, Date.now()),
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('hostmonit_v4', hostmonit_v4 ? 1 : 0, Date.now()),
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('hostmonit_v6', hostmonit_v6 ? 1 : 0, Date.now()),
        env.DB.prepare('INSERT OR REPLACE INTO auto_update_settings (source, enabled, updated_at) VALUES (?, ?, ?)').bind('vps789', vps789 ? 1 : 0, Date.now())
    ]);
    return jsonResponse(await runIpUpdateTask(env, { global_enabled: global_enabled ? 1 : 0, hostmonit_v4: hostmonit_v4 ? 1 : 0, hostmonit_v6: hostmonit_v6 ? 1 : 0, vps789: vps789 ? 1 : 0 }));
}

async function handleGetStats(req, env) {
    const url = new URL(req.url);
    const days = parseInt(url.searchParams.get('days')) || 30;
    const domainsResult = await env.DB.prepare(`SELECT (SELECT COUNT(*) FROM cf_domains) + (SELECT COUNT(*) FROM edgeone_domains) + (SELECT COUNT(*) FROM aliyun_esa_domains) as c`).first();
    const domains = domainsResult?.c || 0;
    const ips = await env.DB.prepare('SELECT COUNT(*) as c FROM cfips').first('c');
    const uuids = await env.DB.prepare('SELECT COUNT(DISTINCT uuid) as c FROM configs').first('c');
    const templates = await env.DB.prepare('SELECT COUNT(*) as c FROM config_templates').first('c');
    const enabled = await env.DB.prepare('SELECT enabled FROM auto_update_settings WHERE source = ?').bind('global_enabled').first()?.enabled || 0;
    const lastExec = await env.DB.prepare('SELECT updated_at FROM auto_update_settings WHERE source = ?').bind('last_executed').first()?.updated_at || 0;
    let accessStats = await getAccessStatsSummary(env, days);
    return jsonResponse({ domains, ips, uuids, templates, autoUpdate: enabled, lastExecuted: lastExec, access_stats: accessStats });
}

async function handleDomains(req, env, method) {
    const url = new URL(req.url);
    if (method === 'GET') {
        const page = parseInt(url.searchParams.get('page')) || 1;
        const size = parseInt(url.searchParams.get('size')) || 10;
        const sortField = ['domain', 'remark', 'created_at', 'source'].includes(url.searchParams.get('sort')) ? url.searchParams.get('sort') : 'created_at';
        const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';
        const offset = (page - 1) * size;
        const query = `SELECT * FROM (SELECT id, domain, remark, created_at, 'Cloudflare' as source FROM cf_domains UNION ALL SELECT id, domain, remark, created_at, '腾讯云EdgeOne' as source FROM edgeone_domains UNION ALL SELECT id, domain, remark, created_at, '阿里云ESA' as source FROM aliyun_esa_domains) ORDER BY ${sortField} ${sortOrder} LIMIT ? OFFSET ?`;
        const totalResult = await env.DB.prepare(`SELECT COUNT(*) as total FROM (SELECT id FROM cf_domains UNION ALL SELECT id FROM edgeone_domains UNION ALL SELECT id FROM aliyun_esa_domains)`).first();
        const { results } = await env.DB.prepare(query).bind(size, offset).all();
        return jsonResponse({ success: true, total: totalResult ? totalResult.total : 0, data: results || [] });
    }
    if (method === 'POST' || method === 'PUT') {
        const { id, domain, remark, source } = await req.json();
        if (!domain || !source) return jsonResponse({ error: '参数不全' }, 400);
        let table = source === 'Cloudflare' ? 'cf_domains' : source === '腾讯云EdgeOne' ? 'edgeone_domains' : 'aliyun_esa_domains';
        if (method === 'POST') await env.DB.prepare(`INSERT INTO ${table} (domain, remark, created_at, updated_at) VALUES (?, ?, ?, ?)`).bind(domain, remark || '', Date.now(), Date.now()).run();
        else await env.DB.prepare(`UPDATE ${table} SET domain = ?, remark = ?, updated_at = ? WHERE id = ?`).bind(domain, remark || '', Date.now(), id).run();
        return jsonResponse({ success: true });
    }
    if (method === 'DELETE') {
        const { id, source } = await req.json();
        let table = source === 'Cloudflare' ? 'cf_domains' : source === '腾讯云EdgeOne' ? 'edgeone_domains' : 'aliyun_esa_domains';
        await env.DB.prepare(`DELETE FROM ${table} WHERE id = ?`).bind(id).run();
        return jsonResponse({ success: true });
    }
}

async function handleIps(req, env, method) {
    const url = new URL(req.url);
    if (method === 'GET') {
        const page = parseInt(url.searchParams.get('page')) || 1;
        const size = parseInt(url.searchParams.get('size')) || 20;
        const sortField = ['ip', 'ip_type', 'carrier', 'source', 'created_at'].includes(url.searchParams.get('sort')) ? url.searchParams.get('sort') : 'created_at';
        const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';
        const { results } = await env.DB.prepare(`SELECT * FROM cfips ORDER BY ${sortField} ${sortOrder} LIMIT ? OFFSET ?`).bind(size, (page - 1) * size).all();
        const total = await env.DB.prepare('SELECT COUNT(*) as total FROM cfips').first('total');
        return jsonResponse({ success: true, total, data: results || [] });
    }
    if (method === 'DELETE') {
        const { ip } = await req.json();
        await env.DB.prepare('DELETE FROM cfips WHERE ip = ?').bind(ip).run();
        return jsonResponse({ success: true });
    }
}

async function handleUuids(req, env, method) {
    const url = new URL(req.url);
    if (method === 'GET') {
        const page = parseInt(url.searchParams.get('page')) || 1;
        const size = parseInt(url.searchParams.get('size')) || 10;
        const sortField = ['uuid', 'owner', 'email', 'status', 'count', 'updated_at'].includes(url.searchParams.get('sort')) ? url.searchParams.get('sort') : 'updated_at';
        const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';
        const query = `SELECT c.uuid, COUNT(*) as count, MAX(c.created_at) as updated_at, ui.owner, ui.email, ui.status, ui.remark, ui.contact_info FROM configs c LEFT JOIN uuid_info ui ON c.uuid = ui.uuid GROUP BY c.uuid ORDER BY ${sortField} ${sortOrder} LIMIT ? OFFSET ?`;
        const { results } = await env.DB.prepare(query).bind(size, (page - 1) * size).all();
        const total = await env.DB.prepare('SELECT COUNT(DISTINCT uuid) as total FROM configs').first('total');
        return jsonResponse({ success: true, total, data: results || [] });
    }
    if (method === 'POST' || method === 'PUT') {
        const { uuid, owner, email, contact_info, status, remark } = await req.json();
        await env.DB.prepare(`INSERT OR REPLACE INTO uuid_info (uuid, owner, email, contact_info, status, remark, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`).bind(uuid, owner, email, contact_info || '', status || 'active', remark || '', Date.now()).run();
        return jsonResponse({ success: true });
    }
    if (method === 'DELETE') {
        const { uuid } = await req.json();
        await env.DB.prepare('DELETE FROM configs WHERE uuid = ?').bind(uuid).run();
        return jsonResponse({ success: true });
    }
}

async function handleUuidConfigs(req, env, method) {
    const url = new URL(req.url);
    if (method === 'GET') {
        const uuid = url.searchParams.get('uuid');
        const page = parseInt(url.searchParams.get('page')) || 1;
        const size = parseInt(url.searchParams.get('size')) || 10;
        let conditions = ['uuid = ?', "config_data IS NOT NULL", "config_data != ''"];
        const params = [uuid];
        if (url.searchParams.get('protocol') && url.searchParams.get('protocol') !== 'all') { conditions.push('protocol = ?'); params.push(url.searchParams.get('protocol')); }
        if (url.searchParams.get('domain_hosting') && url.searchParams.get('domain_hosting') !== 'all') { conditions.push('domain_hosting = ?'); params.push(url.searchParams.get('domain_hosting')); }
        const whereClause = 'WHERE ' + conditions.join(' AND ');
        const total = await env.DB.prepare(`SELECT COUNT(*) as t FROM configs ${whereClause}`).bind(...params).first('t');
        const { results } = await env.DB.prepare(`SELECT * FROM configs ${whereClause} ORDER BY id ASC LIMIT ? OFFSET ?`).bind(...params, size, (page - 1) * size).all();
        return jsonResponse({ success: true, data: results || [], total });
    }
    if (method === 'POST') {
        const { uuid, config_data, domain_hosting = 'Cloudflare' } = await req.json();
        const lines = config_data.split('\n').map(l => l.trim()).filter(Boolean);
        const stmts = [];
        for (const line of lines) {
            const protocol = getProtocol(line);
            if (protocol !== 'unknown' && !(await env.DB.prepare('SELECT id FROM configs WHERE uuid=? AND config_data=?').bind(uuid, line).first())) {
                stmts.push(env.DB.prepare(`INSERT INTO configs (uuid, config_data, protocol, remark, domain_hosting, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`).bind(uuid, line, protocol, extractRemarkFromConfig(line, protocol), domain_hosting, Date.now(), Date.now()));
            }
        }
        await executeInChunks(stmts);
        return jsonResponse({ success: true });
    }
    if (method === 'PUT') {
        const { id, config_data, domain_hosting } = await req.json();
        const protocol = getProtocol(config_data.trim());
        await env.DB.prepare(`UPDATE configs SET config_data=?, protocol=?, remark=?, domain_hosting=?, updated_at=? WHERE id=?`).bind(config_data.trim(), protocol, extractRemarkFromConfig(config_data.trim(), protocol), domain_hosting, Date.now(), parseInt(id)).run();
        return jsonResponse({ success: true });
    }
    if (method === 'DELETE') {
        const { id, uuid } = await req.json();
        if (id) await env.DB.prepare('DELETE FROM configs WHERE id = ?').bind(parseInt(id)).run();
        else if (uuid) await env.DB.prepare('DELETE FROM configs WHERE uuid = ?').bind(uuid).run();
        return jsonResponse({ success: true });
    }
}

async function handleTemplates(req, env, method) {
    const url = new URL(req.url);
    if (method === 'GET') {
        const page = parseInt(url.searchParams.get('page')) || 1;
        const size = parseInt(url.searchParams.get('size')) || 10;
        const sortField = ['name', 'url', 'is_default', 'created_at'].includes(url.searchParams.get('sort')) ? url.searchParams.get('sort') : 'created_at';
        const sortOrder = (url.searchParams.get('order') || 'desc').toUpperCase() === 'ASC' ? 'ASC' : 'DESC';
        const { results } = await env.DB.prepare(`SELECT * FROM config_templates ORDER BY ${sortField} ${sortOrder} LIMIT ? OFFSET ?`).bind(size, (page - 1) * size).all();
        const total = await env.DB.prepare('SELECT COUNT(*) as t FROM config_templates').first('t');
        return jsonResponse({ success: true, total, data: results || [] });
    }
    if (method === 'POST' || method === 'PUT') {
        const { id, name, url, description, is_default } = await req.json();
        if (is_default === 1) await env.DB.prepare('UPDATE config_templates SET is_default = 0 WHERE is_default = 1').run();
        if (method === 'POST') await env.DB.prepare('INSERT INTO config_templates (name, url, description, is_default, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)').bind(name, url, description || '', is_default || 0, Date.now(), Date.now()).run();
        else await env.DB.prepare('UPDATE config_templates SET name=?, url=?, description=?, is_default=?, updated_at=? WHERE id=?').bind(name, url, description || '', is_default || 0, Date.now(), id).run();
        return jsonResponse({ success: true });
    }
    if (method === 'DELETE') {
        const { id } = await req.json();
        const t = await env.DB.prepare('SELECT is_default FROM config_templates WHERE id=?').bind(id).first();
        if (t && t.is_default === 1) return jsonResponse({ error: '不能删除默认模板' }, 400);
        await env.DB.prepare('DELETE FROM config_templates WHERE id=?').bind(id).run();
        return jsonResponse({ success: true });
    }
}

async function handleApi(req, env) {
    const url = new URL(req.url);
    const path = url.pathname, method = req.method;
    try {
        if (path === '/api/login' && method === 'POST') return await handleLogin(req, env);
        if (path === '/api/mfa/init' && method === 'POST') return await handleMfaInit(req, env);
        if (path === '/api/mfa/verify-first' && method === 'POST') return await handleMfaVerifyFirst(req, env);
        if (path === '/api/mfa/login-with-backup' && method === 'POST') return await handleMfaLoginWithBackup(req, env);

        const authHeader = req.headers.get('Authorization');
        if (!authHeader || !authHeader.startsWith('Bearer ')) return jsonResponse({ error: '未授权' }, 401);
        const payload = await verifyJwt(authHeader.split(' ')[1], env.JWT_SECRET || DEFAULT_JWT_SECRET);
        if (!payload) return jsonResponse({ error: 'Token过期' }, 401);
        const user = payload.sub;

        if (path === '/api/change-password') return await handleChangePassword(req, env, user);
        if (path === '/api/mfa/status') return await handleMfaStatus(req, env, user);
        if (path === '/api/mfa/disable') return await handleMfaDisable(req, env, user);
        if (path === '/api/mfa/backup-codes/regenerate') return await handleMfaRegenerateBackupCodes(req, env, user);
        if (path === '/api/settings/auto-update' && method === 'GET') return await handleGetAutoUpdateSettings(req, env);
        if (path === '/api/settings/auto-update' && method === 'POST') return await handleSetAutoUpdateSettings(req, env);
        if (path === '/api/stats') return await handleGetStats(req, env);
        if (path === '/api/stats/uuid-details') return await handleGetUUIDAccessDetails(req, env, url.searchParams.get('uuid'));
        if (path === '/api/domains') return await handleDomains(req, env, method);
        if (path === '/api/ips') return await handleIps(req, env, method);
        if (path === '/api/ips/update') return await handleDirectIpUpdate(req, env);
        if (path === '/api/uuids') return await handleUuids(req, env, method);
        if (path === '/api/uuid-configs') return await handleUuidConfigs(req, env, method);
        if (path === '/api/uuid-configs/stats') return jsonResponse(await fetchUuidStatsForManager(url.searchParams.get('uuid'), env, url.searchParams.get('days')));
        if (path === '/api/templates') return await handleTemplates(req, env, method);
        if (path === '/api/templates/set-default') { await env.DB.batch([env.DB.prepare('UPDATE config_templates SET is_default=0'), env.DB.prepare('UPDATE config_templates SET is_default=1 WHERE id=?').bind((await req.json()).id)]); return jsonResponse({ success: true }); }
        
        return jsonResponse({ error: 'Not found' }, 404);
    } catch (e) { return jsonResponse({ error: e.message }, 500); }
}

async function fetchUuidStatsForManager(uuid, env, days) {
    return getUUIDAccessDetails(env, uuid); // Simplified mapping for stats
}

// =================================================================
//  HTML TEMPLATES (重构为 React + Antd 驱动)
// =================================================================

const commonHead = `
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width,initial-scale=1.0">
    <link rel="icon" href="https://s3.yangzifun.org/logo.ico">
    <!-- React & ReactDOM -->
    <script src="https://unpkg.com/react@18/umd/react.production.min.js"></script>
    <script src="https://unpkg.com/react-dom@18/umd/react-dom.production.min.js"></script>
    <!-- Babel (for JSX in browser) -->
    <script src="https://unpkg.com/@babel/standalone/babel.min.js"></script>
    <!-- Day.js (Antd required) -->
    <script src="https://unpkg.com/dayjs@1/dayjs.min.js"></script>
    <!-- Ant Design -->
    <script src="https://unpkg.com/antd@5.15.0/dist/antd.min.js"></script>
    <!-- Ant Design Icons -->
    <script src="https://unpkg.com/@ant-design/icons@5.3.1/dist/index.umd.js"></script>
    <!-- G2Plot (Ant Design Charts 核心) -->
    <script src="https://unpkg.com/@antv/g2plot@2.4.31/dist/g2plot.min.js"></script>
    <style>
        body { margin: 0; padding: 0; background-color: #f0f2f5; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; }
        #root { display: flex; flex-direction: column; min-height: 100vh; }
        .loading-screen { display: flex; justify-content: center; align-items: center; height: 100vh; font-size: 18px; color: #888; }
    </style>
`;

const loginHtml = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <title>优选配置管理后台 - 登录</title>
    <script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>
    ${commonHead}
</head>
<body>
    <div id="root"><div class="loading-screen">加载系统组件中...</div></div>
    <script type="text/babel">
        const { useState, useEffect, useRef } = React;
        const { Card, Form, Input, Button, message, Space, Typography } = antd;
        const { UserOutlined, LockOutlined, KeyOutlined } = icons;
        const { Title, Text, Link } = Typography;

        const App = () => {
            const [step, setStep] = useState('password'); // password, totp, backup
            const [loading, setLoading] = useState(false);
            const [loginData, setLoginData] = useState({});
            const turnstileRef = useRef(null);
            
            useEffect(() => {
                if (localStorage.getItem('token')) window.location.href = '/';
            }, []);

            const onFinish = async (values) => {
                setLoading(true);
                try {
                    let body = { ...values };
                    
                    if (step === 'password') {
                        const token = document.querySelector('[name="cf-turnstile-response"]')?.value;
                        if (!token) throw new Error('请完成人机验证');
                        body.cf_turnstile_response = token;
                        setLoginData(values);
                    } else if (step === 'totp') {
                        body = { ...loginData, totp_code: values.totp_code, step: 'verify_totp' };
                    }

                    const res = await fetch(step === 'backup' ? '/api/mfa/login-with-backup' : '/api/login', {
                        method: 'POST', headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(step === 'backup' ? { ...loginData, backup_code: values.backup_code } : body)
                    });
                    const data = await res.json();

                    if (res.ok) {
                        if (data.requires_mfa && step === 'password') {
                            setStep('totp');
                        } else {
                            localStorage.setItem('token', data.token);
                            message.success('登录成功');
                            setTimeout(() => window.location.href = '/', 1000);
                        }
                    } else {
                        message.error(data.error || '登录失败');
                    }
                } catch (e) {
                    message.error(e.message || '网络错误');
                }
                setLoading(false);
            };

            return (
                <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', flex: 1, padding: 20 }}>
                    <Card style={{ width: 400, boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
                        <div style={{ textAlign: 'center', marginBottom: 24 }}>
                            <Title level={3} style={{ margin: 0, color: '#5a666d' }}>优选配置管理</Title>
                            <Text type="secondary">
                                {step === 'password' ? '请先验证身份' : step === 'totp' ? '双重验证' : '备份码登录'}
                            </Text>
                        </div>

                        {step === 'password' && (
                            <Form onFinish={onFinish} size="large">
                                <Form.Item name="username" rules={[{ required: true, message: '请输入管理员账号' }]}>
                                    <Input prefix={<UserOutlined />} placeholder="管理员账号" />
                                </Form.Item>
                                <Form.Item name="password" rules={[{ required: true, message: '请输入访问密码' }]}>
                                    <Input.Password prefix={<LockOutlined />} placeholder="访问密码" />
                                </Form.Item>
                                <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 24 }}>
                                    <div className="cf-turnstile" data-sitekey="__TURNSTILE_SITE_KEY__"></div>
                                </div>
                                <Button type="primary" htmlType="submit" block loading={loading} style={{ backgroundColor: '#5a666d' }}>
                                    登录
                                </Button>
                            </Form>
                        )}

                        {step === 'totp' && (
                            <Form onFinish={onFinish} size="large">
                                <Form.Item name="totp_code" rules={[{ required: true, len: 6, message: '请输入6位验证码' }]}>
                                    <Input placeholder="000000" style={{ textAlign: 'center', letterSpacing: 8 }} maxLength={6} />
                                </Form.Item>
                                <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                                    <Button onClick={() => setStep('password')}>返回</Button>
                                    <Button type="primary" htmlType="submit" loading={loading} style={{ backgroundColor: '#5a666d' }}>验证</Button>
                                </Space>
                                <div style={{ textAlign: 'center', marginTop: 16 }}>
                                    <Link onClick={() => setStep('backup')}>使用备份码登录</Link>
                                </div>
                            </Form>
                        )}

                        {step === 'backup' && (
                            <Form onFinish={onFinish} size="large" layout="vertical">
                                <Form.Item name="backup_code" label="备份码" rules={[{ required: true, message: '请输入备份码' }]}>
                                    <Input placeholder="例如：A1B2C3D4" style={{ textTransform: 'uppercase' }} />
                                </Form.Item>
                                <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                                    <Button onClick={() => setStep('totp')}>返回</Button>
                                    <Button type="primary" htmlType="submit" loading={loading} style={{ backgroundColor: '#5a666d' }}>验证备份码</Button>
                                </Space>
                            </Form>
                        )}
                    </Card>
                </div>
            );
        };
        const root = ReactDOM.createRoot(document.getElementById('root'));
        root.render(<App />);
    </script>
</body>
</html>
`;

const adminHtml = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <title>优选配置管理后台</title>
    ${commonHead}
</head>
<body>
    <div id="root"><div class="loading-screen">加载系统资源中...</div></div>
    <script type="text/babel">
        const { useState, useEffect, useRef } = React;
        const { Layout, Menu, Card, Row, Col, Statistic, Table, Button, Modal, Form, Input, Select, Switch, Space, Tag, message, Tabs, Drawer, Popconfirm } = antd;
        const { DashboardOutlined, GlobalOutlined, CloudServerOutlined, KeyOutlined, AppstoreAddOutlined, SafetyCertificateOutlined, LogoutOutlined, SyncOutlined, PlusOutlined, EditOutlined, DeleteOutlined, CopyOutlined } = icons;
        const { Header, Content, Footer } = Layout;
        const { Line } = G2Plot; // 从 G2Plot 全局变量获取

        const token = localStorage.getItem('token');
        if (!token) window.location.href = '/login';

        // 统一 API 请求包装
        const request = async (path, options = {}) => {
            const res = await fetch('/api/' + path, {
                ...options,
                headers: { 'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json', ...options.headers }
            });
            if (res.status === 401) {
                message.error('会话已过期');
                localStorage.clear();
                window.location.href = '/login';
                throw new Error('Unauthorized');
            }
            const data = await res.json();
            if (data.error) throw new Error(data.error);
            return data;
        };

        const App = () => {
            const [activeTab, setActiveTab] = useState('dash');
            const [pwdModalVisible, setPwdModalVisible] = useState(false);
            
            // 数据状态
            const [stats, setStats] = useState({});
            const [autoUpdate, setAutoUpdate] = useState({});
            const [mfaStatus, setMfaStatus] = useState({});

            useEffect(() => {
                loadBaseData();
            }, []);

            const loadBaseData = async () => {
                try {
                    const [s, a, m] = await Promise.all([
                        request('stats'),
                        request('settings/auto-update'),
                        request('mfa/status')
                    ]);
                    setStats(s); setAutoUpdate(a); setMfaStatus(m);
                } catch (e) {
                    message.error('加载基础数据失败');
                }
            };

            const logout = () => {
                localStorage.clear();
                window.location.href = '/login';
            };

            const handlePwdChange = async (values) => {
                if(values.new !== values.confirm) return message.error('两次密码不一致');
                try {
                    await request('change-password', { method: 'POST', body: JSON.stringify({ oldPassword: values.old, newPassword: values.new }) });
                    message.success('修改成功，请重新登录');
                    logout();
                } catch(e) { message.error(e.message); }
            };

            return (
                <Layout style={{ minHeight: '100vh' }}>
                    <Header style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', background: '#fff', borderBottom: '1px solid #f0f0f0', padding: '0 24px' }}>
                        <div style={{ fontSize: 18, fontWeight: 'bold', color: '#5a666d' }}>
                            CF 优选管理 <Tag color={mfaStatus.mfa_enabled ? 'success' : 'warning'} style={{ marginLeft: 8 }}>MFA {mfaStatus.mfa_enabled ? 'ON' : 'OFF'}</Tag>
                        </div>
                        <Space>
                            <Button type="text" icon={<KeyOutlined />} onClick={() => setPwdModalVisible(true)}>修改密码</Button>
                            <Button type="text" danger icon={<LogoutOutlined />} onClick={logout}>退出</Button>
                        </Space>
                    </Header>
                    <Content style={{ padding: '24px', maxWidth: 1200, margin: '0 auto', width: '100%' }}>
                        <Tabs 
                            activeKey={activeTab} 
                            onChange={setActiveTab} 
                            type="card"
                            items={[
                                { key: 'dash', label: <span><DashboardOutlined />概览</span>, children: <DashTab stats={stats} /> },
                                { key: 'dom', label: <span><GlobalOutlined />域名</span>, children: <DomTab /> },
                                { key: 'ips', label: <span><CloudServerOutlined />IP资源池</span>, children: <IpTab autoUpdate={autoUpdate} refreshData={loadBaseData} /> },
                                { key: 'uuids', label: <span><KeyOutlined />配置分组</span>, children: <UuidTab /> },
                                { key: 'tpl', label: <span><AppstoreAddOutlined />模板</span>, children: <TplTab /> },
                                { key: 'sec', label: <span><SafetyCertificateOutlined />安全</span>, children: <SecTab mfa={mfaStatus} refresh={loadBaseData} /> }
                            ]}
                        />
                    </Content>
                    <Footer style={{ textAlign: 'center' }}>Powered by YZFN</Footer>

                    <Modal title="修改密码" open={pwdModalVisible} onCancel={() => setPwdModalVisible(false)} footer={null}>
                        <Form onFinish={handlePwdChange} layout="vertical">
                            <Form.Item name="old" label="旧密码" rules={[{required: true}]}><Input.Password /></Form.Item>
                            <Form.Item name="new" label="新密码" rules={[{required: true, min: 6}]}><Input.Password /></Form.Item>
                            <Form.Item name="confirm" label="确认新密码" rules={[{required: true}]}><Input.Password /></Form.Item>
                            <Button type="primary" htmlType="submit" block>确认修改</Button>
                        </Form>
                    </Modal>
                </Layout>
            );
        };

        // --- 子组件：仪表盘 ---
        const DashTab = ({ stats }) => {
            const chartRef = useRef(null);
            const containerRef = useRef(null);

            useEffect(() => {
                if (stats?.access_stats?.daily_stats && containerRef.current) {
                    if (chartRef.current) chartRef.current.destroy();
                    
                    const data = [];
                    stats.access_stats.daily_stats.forEach(d => {
                        const date = d.date.substring(5);
                        data.push({ date, type: '总访问', value: d.total });
                        data.push({ date, type: '订阅访问', value: d.subscription });
                        data.push({ date, type: '网页生成', value: d.api_generation });
                    });
                    
                    chartRef.current = new Line(containerRef.current, {
                        data: data.reverse(),
                        xField: 'date',
                        yField: 'value',
                        seriesField: 'type',
                        smooth: true,
                        color: ['#1890ff', '#52c41a', '#faad14'],
                        legend: { position: 'top' }
                    });
                    chartRef.current.render();
                }
            }, [stats]);

            return (
                <Space direction="vertical" size="large" style={{ display: 'flex' }}>
                    <Row gutter={[16, 16]}>
                        <Col xs={12} sm={6}><Card><Statistic title="优选域名" value={stats.domains} /></Card></Col>
                        <Col xs={12} sm={6}><Card><Statistic title="活跃 IP" value={stats.ips} /></Card></Col>
                        <Col xs={12} sm={6}><Card><Statistic title="配置分组" value={stats.uuids} /></Card></Col>
                        <Col xs={12} sm={6}><Card><Statistic title="配置模板" value={stats.templates} /></Card></Col>
                    </Row>
                    <Card title="访问趋势分析">
                        <div ref={containerRef} style={{ height: 300 }} />
                    </Card>
                </Space>
            );
        };

        // --- 子组件：通用表格抽象钩子 ---
        const useTableData = (endpoint) => {
            const [data, setData] = useState([]);
            const [total, setTotal] = useState(0);
            const [loading, setLoading] = useState(false);
            const [pagination, setPagination] = useState({ current: 1, pageSize: 10 });
            const [sorter, setSorter] = useState({ field: 'created_at', order: 'desc' });

            const fetchData = async () => {
                setLoading(true);
                try {
                    const res = await request(\`\${endpoint}?page=\${pagination.current}&size=\${pagination.pageSize}&sort=\${sorter.field}&order=\${sorter.order}\`);
                    setData(res.data);
                    setTotal(res.total);
                } catch(e) {}
                setLoading(false);
            };

            useEffect(() => { fetchData(); }, [pagination, sorter]);

            const handleTableChange = (pag, filters, sorterInfo) => {
                setPagination(pag);
                if (sorterInfo.field) {
                    setSorter({ field: sorterInfo.field, order: sorterInfo.order === 'ascend' ? 'asc' : 'desc' });
                }
            };

            return { data, total, loading, pagination, handleTableChange, refresh: fetchData };
        };

        // --- 子组件：域名管理 ---
        const DomTab = () => {
            const { data, total, loading, pagination, handleTableChange, refresh } = useTableData('domains');
            const [form] = Form.useForm();
            const [visible, setVisible] = useState(false);
            const [editId, setEditId] = useState(null);

            const onFinish = async (values) => {
                try {
                    const method = editId ? 'PUT' : 'POST';
                    await request('domains', { method, body: JSON.stringify({ id: editId, ...values }) });
                    message.success('保存成功');
                    setVisible(false);
                    refresh();
                } catch(e) { message.error(e.message); }
            };

            const del = async (rec) => {
                try { await request('domains', { method: 'DELETE', body: JSON.stringify({ id: rec.id, source: rec.source }) }); refresh(); } 
                catch(e) { message.error(e.message); }
            };

            const cols = [
                { title: '域名', dataIndex: 'domain', sorter: true },
                { title: '备注', dataIndex: 'remark', sorter: true },
                { title: '来源', dataIndex: 'source', sorter: true, render: t => <Tag color={t==='Cloudflare'?'orange':'blue'}>{t}</Tag> },
                { title: '操作', render: (_, r) => (
                    <Space>
                        <Button size="small" type="link" onClick={() => { setEditId(r.id); form.setFieldsValue(r); setVisible(true); }}>编辑</Button>
                        <Popconfirm title="确定删除?" onConfirm={() => del(r)}><Button size="small" type="link" danger>删除</Button></Popconfirm>
                    </Space>
                )}
            ];

            return (
                <Card>
                    <Space style={{ marginBottom: 16 }}>
                        <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditId(null); form.resetFields(); setVisible(true); }}>添加域名</Button>
                    </Space>
                    <Table columns={cols} dataSource={data} rowKey="id" pagination={{...pagination, total}} onChange={handleTableChange} loading={loading} />
                    
                    <Modal title={editId ? "编辑" : "新增"} open={visible} onCancel={() => setVisible(false)} footer={null}>
                        <Form form={form} onFinish={onFinish} layout="vertical">
                            <Form.Item name="domain" label="域名" rules={[{required:true}]}><Input /></Form.Item>
                            <Form.Item name="remark" label="备注"><Input /></Form.Item>
                            <Form.Item name="source" label="来源" rules={[{required:true}]}>
                                <Select options={[{label:'Cloudflare',value:'Cloudflare'},{label:'腾讯云EdgeOne',value:'腾讯云EdgeOne'},{label:'阿里云ESA',value:'阿里云ESA'}]} />
                            </Form.Item>
                            <Button type="primary" htmlType="submit" block>保存</Button>
                        </Form>
                    </Modal>
                </Card>
            );
        };

        // --- 子组件：IP管理 ---
        const IpTab = ({ autoUpdate, refreshData }) => {
            const { data, total, loading, pagination, handleTableChange, refresh } = useTableData('ips');
            const [switches, setSwitches] = useState({ global: false, v4: false, v6: false, v7: false });

            useEffect(() => {
                if(autoUpdate) setSwitches({ global: !!autoUpdate.global_enabled, v4: !!autoUpdate.hostmonit_v4, v6: !!autoUpdate.hostmonit_v6, v7: !!autoUpdate.vps789 });
            }, [autoUpdate]);

            const saveSettings = async () => {
                try {
                    await request('settings/auto-update', { method: 'POST', body: JSON.stringify({ global_enabled: switches.global, hostmonit_v4: switches.v4, hostmonit_v6: switches.v6, vps789: switches.v7 }) });
                    message.success('设置已保存');
                    refreshData();
                } catch(e) { message.error(e.message); }
            };

            const triggerUpdate = async () => {
                try {
                    message.loading({content: '执行中...', key: 'ipUp'});
                    const res = await request('ips/update', { method: 'POST', body: JSON.stringify({ global_enabled: switches.global, hostmonit_v4: switches.v4, hostmonit_v6: switches.v6, vps789: switches.v7 }) });
                    message.success({content: res.message || '更新完成', key: 'ipUp'});
                    refresh();
                } catch(e) { message.error({content: e.message, key: 'ipUp'}); }
            };

            const del = async (ip) => {
                await request('ips', { method: 'DELETE', body: JSON.stringify({ ip }) }); refresh();
            };

            const cols = [
                { title: 'IP', dataIndex: 'ip', sorter: true },
                { title: '类型', dataIndex: 'ip_type', sorter: true },
                { title: '运营商', dataIndex: 'carrier', sorter: true },
                { title: '来源', dataIndex: 'source', sorter: true },
                { title: '操作', render: (_, r) => <Popconfirm title="确定删除?" onConfirm={()=>del(r.ip)}><Button size="small" type="link" danger>删除</Button></Popconfirm>}
            ];

            return (
                <Card>
                    <Space size="large" style={{ marginBottom: 16, background: '#fafafa', padding: 16, borderRadius: 8, width: '100%', flexWrap: 'wrap' }}>
                        <span>主开关 <Switch checked={switches.global} onChange={v => setSwitches({...switches, global: v})} /></span>
                        <span>v4接口 <Switch checked={switches.v4} onChange={v => setSwitches({...switches, v4: v})} /></span>
                        <span>v6接口 <Switch checked={switches.v6} onChange={v => setSwitches({...switches, v6: v})} /></span>
                        <span>Vps789 <Switch checked={switches.v7} onChange={v => setSwitches({...switches, v7: v})} /></span>
                        <Button icon={<SafetyCertificateOutlined />} onClick={saveSettings}>保存配置</Button>
                        <Button type="primary" icon={<SyncOutlined />} onClick={triggerUpdate}>立即更新</Button>
                    </Space>
                    <Table columns={cols} dataSource={data} rowKey="ip" pagination={{...pagination, total}} onChange={handleTableChange} loading={loading} />
                </Card>
            );
        };

        // --- 子组件：UUID分组与配置管理 ---
        const UuidTab = () => {
            const { data, total, loading, pagination, handleTableChange, refresh } = useTableData('uuids');
            const [drawerData, setDrawerData] = useState(null); // 存储选中的 uuid
            const [configs, setConfigs] = useState([]);
            const [cLoading, setCLoading] = useState(false);
            const [form] = Form.useForm();
            const [cForm] = Form.useForm();
            const [isModalOpen, setIsModalOpen] = useState(false);
            const [isConfigModalOpen, setIsConfigModalOpen] = useState(false);
            const [editConfigId, setEditConfigId] = useState(null);

            const loadConfigs = async (uuid) => {
                setCLoading(true);
                try { const res = await request(\`uuid-configs?uuid=\${uuid}&page=1&size=100\`); setConfigs(res.data); } catch(e){}
                setCLoading(false);
            };

            useEffect(() => { if (drawerData) loadConfigs(drawerData); }, [drawerData]);

            const delU = async (uuid) => { await request('uuids', { method: 'DELETE', body: JSON.stringify({uuid}) }); refresh(); };
            
            const handleInfoSubmit = async (val) => {
                await request('uuids', { method: 'POST', body: JSON.stringify(val) });
                setIsModalOpen(false); refresh();
            };

            const handleConfigSubmit = async (val) => {
                if (editConfigId) await request('uuid-configs', { method: 'PUT', body: JSON.stringify({id: editConfigId, ...val}) });
                else await request('uuid-configs', { method: 'POST', body: JSON.stringify({uuid: drawerData, ...val}) });
                setIsConfigModalOpen(false); loadConfigs(drawerData); refresh();
            };

            const delC = async (id) => { await request('uuid-configs', { method: 'DELETE', body: JSON.stringify({id}) }); loadConfigs(drawerData); refresh(); };

            const cols = [
                { title: 'UUID', dataIndex: 'uuid', sorter: true, render: t => <Text code>{t}</Text> },
                { title: '所有者', dataIndex: 'owner' },
                { title: '节点数', dataIndex: 'count', sorter: true },
                { title: '状态', dataIndex: 'status', render: t => <Tag color={t==='active'?'green':'red'}>{t}</Tag> },
                { title: '操作', render: (_, r) => (
                    <Space>
                        <Button size="small" type="primary" onClick={() => setDrawerData(r.uuid)}>配置管理</Button>
                        <Button size="small" onClick={() => { form.setFieldsValue(r); setIsModalOpen(true); }}>编辑</Button>
                        <Popconfirm title="确定删除整组?" onConfirm={()=>delU(r.uuid)}><Button size="small" danger>删除</Button></Popconfirm>
                    </Space>
                )}
            ];

            const cCols = [
                { title: '协议', dataIndex: 'protocol' },
                { title: '备注', dataIndex: 'remark' },
                { title: '托管', dataIndex: 'domain_hosting' },
                { title: '配置数据', dataIndex: 'config_data', render: t => <div style={{maxWidth: 300}} className="ant-typography ant-typography-ellipsis" title={t}>{t}</div> },
                { title: '操作', render: (_, r) => (
                    <Space>
                        <Button size="small" type="link" onClick={() => { setEditConfigId(r.id); cForm.setFieldsValue(r); setIsConfigModalOpen(true); }}>编辑</Button>
                        <Popconfirm title="确定删除?" onConfirm={()=>delC(r.id)}><Button size="small" type="link" danger>删除</Button></Popconfirm>
                    </Space>
                )}
            ];

            return (
                <Card>
                    <Table columns={cols} dataSource={data} rowKey="uuid" pagination={{...pagination, total}} onChange={handleTableChange} loading={loading} />
                    
                    <Modal title="编辑信息" open={isModalOpen} onCancel={() => setIsModalOpen(false)} footer={null}>
                        <Form form={form} onFinish={handleInfoSubmit} layout="vertical">
                            <Form.Item name="uuid" hidden><Input /></Form.Item>
                            <Form.Item name="owner" label="所有者"><Input /></Form.Item>
                            <Form.Item name="email" label="邮箱"><Input /></Form.Item>
                            <Form.Item name="status" label="状态"><Select options={[{label:'活跃',value:'active'},{label:'停用',value:'inactive'}]} /></Form.Item>
                            <Button type="primary" htmlType="submit" block>保存</Button>
                        </Form>
                    </Modal>

                    <Drawer title={\`管理节点: \${drawerData}\`} width={800} open={!!drawerData} onClose={() => setDrawerData(null)}>
                        <Button type="dashed" block icon={<PlusOutlined />} onClick={() => { setEditConfigId(null); cForm.resetFields(); setIsConfigModalOpen(true); }} style={{ marginBottom: 16 }}>添加节点配置 (支持多行批量)</Button>
                        <Table columns={cCols} dataSource={configs} rowKey="id" pagination={false} loading={cLoading} size="small" />
                    </Drawer>

                    <Modal title={editConfigId ? "编辑节点" : "新增节点"} open={isConfigModalOpen} onCancel={() => setIsConfigModalOpen(false)} footer={null}>
                        <Form form={cForm} onFinish={handleConfigSubmit} layout="vertical">
                            <Form.Item name="domain_hosting" label="域名托管" initialValue="Cloudflare"><Select options={[{label:'Cloudflare',value:'Cloudflare'},{label:'腾讯云EdgeOne',value:'腾讯云EdgeOne'},{label:'其他',value:'其他'}]} /></Form.Item>
                            <Form.Item name="config_data" label="配置内容" rules={[{required:true}]}><Input.TextArea rows={6} /></Form.Item>
                            <Button type="primary" htmlType="submit" block>保存</Button>
                        </Form>
                    </Modal>
                </Card>
            );
        };

        // --- 子组件：模板管理 ---
        const TplTab = () => {
            const { data, total, loading, pagination, handleTableChange, refresh } = useTableData('templates');
            const [form] = Form.useForm();
            const [visible, setVisible] = useState(false);
            const [editId, setEditId] = useState(null);

            const onFinish = async (values) => {
                try {
                    await request('templates', { method: editId ? 'PUT' : 'POST', body: JSON.stringify({ id: editId, ...values, is_default: values.is_default ? 1 : 0 }) });
                    message.success('保存成功'); setVisible(false); refresh();
                } catch(e) { message.error(e.message); }
            };

            const del = async (id) => {
                try { await request('templates', { method: 'DELETE', body: JSON.stringify({ id }) }); refresh(); } catch(e) { message.error(e.message); }
            };

            const setDefault = async (id) => {
                await request('templates/set-default', { method: 'POST', body: JSON.stringify({ id }) }); refresh();
            };

            const cols = [
                { title: '名称', dataIndex: 'name', sorter: true },
                { title: 'URL', dataIndex: 'url', render: t => <a href={t} target="_blank">链接</a> },
                { title: '默认', dataIndex: 'is_default', render: t => t ? <Tag color="green">是</Tag> : '否' },
                { title: '操作', render: (_, r) => (
                    <Space>
                        <Button size="small" type="link" onClick={() => { setEditId(r.id); form.setFieldsValue({...r, is_default: r.is_default===1}); setVisible(true); }}>编辑</Button>
                        {!r.is_default && <Button size="small" type="link" onClick={() => setDefault(r.id)}>设为默认</Button>}
                        <Popconfirm title="确定删除?" onConfirm={() => del(r.id)}><Button size="small" type="link" danger>删除</Button></Popconfirm>
                    </Space>
                )}
            ];

            return (
                <Card>
                    <Space style={{ marginBottom: 16 }}>
                        <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditId(null); form.resetFields(); setVisible(true); }}>添加模板</Button>
                    </Space>
                    <Table columns={cols} dataSource={data} rowKey="id" pagination={{...pagination, total}} onChange={handleTableChange} loading={loading} />
                    
                    <Modal title={editId ? "编辑" : "新增"} open={visible} onCancel={() => setVisible(false)} footer={null}>
                        <Form form={form} onFinish={onFinish} layout="vertical" initialValues={{ is_default: false }}>
                            <Form.Item name="name" label="名称" rules={[{required:true}]}><Input /></Form.Item>
                            <Form.Item name="url" label="URL" rules={[{required:true}]}><Input /></Form.Item>
                            <Form.Item name="description" label="描述"><Input /></Form.Item>
                            <Form.Item name="is_default" valuePropName="checked"><Switch checkedChildren="默认" unCheckedChildren="非默认" /></Form.Item>
                            <Button type="primary" htmlType="submit" block>保存</Button>
                        </Form>
                    </Modal>
                </Card>
            );
        };

        // --- 子组件：安全设置 (MFA) ---
        const SecTab = ({ mfa, refresh }) => {
            const [pwdVisible, setPwdVisible] = useState(false);
            const [action, setAction] = useState(null); // 'enable', 'disable', 'regen'
            const [setupData, setSetupData] = useState(null);
            const [backups, setBackups] = useState(null);
            const [pwdForm] = Form.useForm();
            const [totpForm] = Form.useForm();

            const handleAuth = async (values) => {
                setPwdVisible(false);
                try {
                    if (action === 'enable') {
                        const payload = JSON.parse(atob(token.split('.')[1]));
                        const res = await request('mfa/init', { method: 'POST', body: JSON.stringify({ username: payload.sub, password: values.password }) });
                        setSetupData({...res, username: payload.sub});
                    } else if (action === 'disable') {
                        await request('mfa/disable', { method: 'POST', body: JSON.stringify({ password: values.password }) });
                        message.success('已禁用MFA'); refresh();
                    } else if (action === 'regen') {
                        const res = await request('mfa/backup-codes/regenerate', { method: 'POST', body: JSON.stringify({ password: values.password }) });
                        setBackups(res.backup_codes); refresh();
                    }
                } catch(e) { message.error(e.message); }
            };

            const verifyMfa = async (values) => {
                try {
                    const res = await request('mfa/verify-first', { method: 'POST', body: JSON.stringify({ username: setupData.username, totp_code: values.code, secret: setupData.secret }) });
                    setBackups(res.backup_codes);
                    setSetupData(null);
                    refresh();
                    message.success('MFA启用成功');
                } catch(e) { message.error(e.message); }
            };

            return (
                <Card>
                    <Row gutter={[24, 24]}>
                        <Col span={24}>
                            <Title level={4}>双重验证 (MFA)</Title>
                            {mfa.mfa_enabled ? (
                                <Space direction="vertical" size="large">
                                    <Tag color="success" style={{fontSize:14, padding: '4px 8px'}}>✅ 已启用</Tag>
                                    <Text>剩余备份码: <b>{mfa.backup_codes_remaining}</b> 个</Text>
                                    <Space>
                                        <Button danger onClick={() => { setAction('disable'); setPwdVisible(true); }}>禁用双重验证</Button>
                                        <Button onClick={() => { setAction('regen'); setPwdVisible(true); }}>重新生成备份码</Button>
                                    </Space>
                                </Space>
                            ) : (
                                <Space direction="vertical">
                                    <Tag color="error" style={{fontSize:14, padding: '4px 8px'}}>⚠️ 未启用</Tag>
                                    <Text type="secondary">为了您的账户安全，建议开启双重验证。</Text>
                                    {!setupData && <Button type="primary" onClick={() => { setAction('enable'); setPwdVisible(true); }}>开启 MFA</Button>}
                                </Space>
                            )}
                        </Col>
                        
                        {setupData && (
                            <Col span={24}>
                                <Card type="inner" title="配置双重验证">
                                    <Row gutter={24}>
                                        <Col xs={24} sm={12} style={{ textAlign: 'center' }}>
                                            <img src={setupData.qr_url} alt="QR" width="200" style={{ border: '1px solid #eee', padding: 8, borderRadius: 8 }}/>
                                            <div style={{ marginTop: 12 }}>
                                                <Text type="secondary">密钥: </Text>
                                                <Text copyable code>{setupData.secret}</Text>
                                            </div>
                                        </Col>
                                        <Col xs={24} sm={12}>
                                            <Form layout="vertical" onFinish={verifyMfa} form={totpForm}>
                                                <Form.Item name="code" label="验证码" rules={[{required: true, len: 6}]} extra="请使用 Authenticator App 扫描左侧二维码，并输入6位动态码">
                                                    <Input size="large" placeholder="000000" maxLength={6} style={{ textAlign: 'center', letterSpacing: 8 }} />
                                                </Form.Item>
                                                <Space>
                                                    <Button type="primary" htmlType="submit">验证并启用</Button>
                                                    <Button onClick={() => setSetupData(null)}>取消</Button>
                                                </Space>
                                            </Form>
                                        </Col>
                                    </Row>
                                </Card>
                            </Col>
                        )}
                    </Row>

                    <Modal title="验证当前密码" open={pwdVisible} onCancel={() => setPwdVisible(false)} footer={null}>
                        <Form onFinish={handleAuth} form={pwdForm}>
                            <Form.Item name="password" rules={[{required:true}]}><Input.Password placeholder="输入登录密码" /></Form.Item>
                            <Button type="primary" htmlType="submit" block>确认</Button>
                        </Form>
                    </Modal>

                    <Modal title="请妥善保管备份码" open={!!backups} onCancel={() => setBackups(null)} footer={[<Button type="primary" onClick={()=>setBackups(null)}>我已保存</Button>]}>
                        <Text type="danger">备份码仅显示一次，每个仅能使用一次。请将其复制并保存在安全的地方。</Text>
                        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 16 }}>
                            {backups?.map(c => <Tag key={c} style={{fontSize: 16, padding: 8, textAlign: 'center'}}>{c}</Tag>)}
                        </div>
                    </Modal>
                </Card>
            );
        };

        const root = ReactDOM.createRoot(document.getElementById('root'));
        root.render(<App />);
    </script>
</body>
</html>
`;

// =================================================================
//  WORKER ENTRY POINT
// =================================================================
export default {
    async fetch(request, env, ctx) {
        const url = new URL(request.url);
        ctx.waitUntil(initializeDatabaseSettings(env));

        if (url.pathname.startsWith('/api')) {
            return await handleApi(request, env);
        }

        if (url.pathname === '/login') {
            const siteKey = env.TURNSTILE_SITE_KEY || '';
            const html = loginHtml.replace('__TURNSTILE_SITE_KEY__', siteKey);
            return new Response(html, { headers: { 'Content-Type': 'text/html;charset=UTF-8', 'Cache-Control': CACHE_CONTROL } });
        }

        return new Response(adminHtml, { headers: { 'Content-Type': 'text/html;charset=UTF-8', 'Cache-Control': CACHE_CONTROL } });
    },

    async scheduled(event, env, ctx) {
        ctx.waitUntil((async () => {
            try { await runIpUpdateTask(env); } catch (e) {}
        })());
    }
};