/* =================================================================
 *  Cloudflare Worker: All-in-One Proxy Tool
 *  功能：
 *  1. 配置管理器（包含域名托管属性）
 *  2. 订阅文件生成器（支持Clash/Mihomo格式）
 *  3. 批量配置生成器
 *  4. 统计分析功能
 * ================================================================= */

// =================================================================
//  GLOBAL UTILITIES (通用工具函数)
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
        console.error("Base64 decoding error:", e);
        throw new Error("无效的 Base64 字符串，无法解码 VMess 配置。");
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

    // 检查各种协议前缀
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
        { prefix: 'socks5://', type: 'socks5' }
    ];

    for (const pattern of protocolPatterns) {
        if (configStr.toLowerCase().startsWith(pattern.prefix)) {
            return pattern.type;
        }
    }

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
            protocol === 'any-reality' || protocol === 'anyreality' ||
            protocol === 'reality' || protocol === 'shadowsocks') {
            const url = new URL(configStr);
            if (url.hash) return decodeURIComponent(url.hash.substring(1));
        }
    } catch (e) { }
    return null;
}

/**
 * 获取客户端IP地址 (支持Cloudflare Worker)
 */
function getClientIp(request) {
    const cfIp = request.headers.get('cf-connecting-ip');
    if (cfIp) return cfIp;

    const forwardedFor = request.headers.get('x-forwarded-for');
    if (forwardedFor) {
        return forwardedFor.split(',')[0].trim();
    }

    return 'unknown';
}

// =================================================================
//  DATABASE OPERATIONS (数据库操作)
// =================================================================

// 获取配置列表
async function fetchConfigsByUuidFromDB(uuid, env) {
    const db = env.DB;
    if (!db) return [];
    try {
        const stmt = db.prepare('SELECT id, config_data, protocol, remark, domain_hosting FROM configs WHERE uuid = ? ORDER BY id ASC');
        const { results } = await stmt.bind(uuid).all();
        return results;
    } catch (e) {
        console.error(`Error fetching configs for UUID ${uuid}:`, e.message);
        return [];
    }
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

// 获取 IP (仅读取 SELECT)
async function fetchIpsFromDB(ipType, carrierType, env) {
    const db = env.DB;
    if (!db) throw new Error("D1 数据库未绑定。");

    let query = 'SELECT DISTINCT ip FROM cfips WHERE 1=1';
    const params = [];

    if (ipType.toLowerCase() !== 'all') {
        query += ' AND ip_type = ?';
        params.push(ipType.toLowerCase());
    }
    if (carrierType.toLowerCase() !== 'all') {
        query += ' AND carrier = ?';
        params.push(carrierType.toUpperCase());
    }
    query += ' ORDER BY ip';

    const { results } = await db.prepare(query).bind(...params).all();
    return results;
}

// 获取域名 (仅读取 SELECT)
async function fetchDomainsFromDB(env) {
    const db = env.DB;
    if (!db) throw new Error("D1 数据库未绑定。");
    try {
        const { results } = await db.prepare('SELECT DISTINCT domain FROM cf_domains ORDER BY domain').all();
        return results;
    } catch (e) {
        console.error("Fetching domains failed:", e.message);
        return [];
    }
}

// 获取所有模板列表
async function fetchAllTemplatesFromDB(env) {
    try {
        const db = env.DB;
        if (!db) {
            throw new Error("D1数据库未绑定，无法获取模板列表");
        }

        const { results } = await db.prepare(
            'SELECT name, url, description, is_default FROM config_templates ORDER BY is_default DESC, name ASC'
        ).all();

        return results;
    } catch (e) {
        console.error(`从数据库获取模板列表失败: ${e.message}`);
        throw new Error(`无法从数据库获取模板列表: ${e.message}`);
    }
}

// 从数据库获取模板URL
async function fetchTemplateFromDB(env, templateName) {
    try {
        const db = env.DB;
        if (!db) {
            throw new Error("D1数据库未绑定，无法获取模板");
        }

        const template = await db.prepare(
            'SELECT name, url, description FROM config_templates WHERE name = ?'
        ).bind(templateName).first();

        if (template && template.url) {
            console.log(`从数据库找到模板: ${template.name} -> ${template.url}`);
            return template;
        }

        console.log(`数据库中未找到模板: ${templateName}`);
        return null;
    } catch (e) {
        console.error(`从数据库获取模板失败: ${e.message}`);
        throw new Error(`无法从数据库获取模板: ${e.message}`);
    }
}

// 记录配置访问日志
async function logConfigAccess(env, uuid, queryType, clientIp, userAgent) {
    try {
        const db = env.DB;
        if (!db) {
            console.error("D1数据库未绑定，无法记录访问日志");
            return;
        }

        const stmt = db.prepare(
            'INSERT INTO config_access_logs (uuid, query_type, client_ip, user_agent) VALUES (?, ?, ?, ?)'
        );

        await stmt.bind(uuid, queryType, clientIp || 'unknown', userAgent || 'unknown').run();
        console.log(`成功记录访问日志: uuid=${uuid}, type=${queryType}`);
    } catch (e) {
        console.error("记录访问日志失败:", e.message);
    }
}

// =================================================================
//  CONFIG MANAGER API HANDLERS (配置管理API)
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
    return jsonResponse({ success: true });
}

async function handleUpdateConfig(request, env) {
    let body;
    try { body = await request.json(); } catch (e) { return jsonResponse({ error: '无效JSON' }, 400); }
    const { id, config_data, domain_hosting = 'Cloudflare' } = body;

    const protocol = getProtocol(config_data);
    if (protocol === 'unknown') return jsonResponse({ error: '不支持的配置格式' }, 400);
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
    } catch (e) { return jsonResponse({ error: e.message }, 500); }
}

async function handleGetConfigs(uuid, env) {
    const results = await fetchConfigsByUuidFromDB(uuid, env);
    return jsonResponse({ uuid, configs: results });
}

async function handleGetUuidStats(uuid, env) {
    if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);
    const urlObj = new URL(request.url);
    const days = urlObj.searchParams.get('days') || 30;
    const stats = await fetchUuidAccessStatsFromDB(uuid, env, parseInt(days));
    return jsonResponse(stats);
}

async function handleDelete(type, value, env) {
    const sql = type === 'uuid' ? 'DELETE FROM configs WHERE uuid = ?' : 'DELETE FROM configs WHERE id = ?';
    await env.DB.prepare(sql).bind(value).run();
    return jsonResponse({ success: true });
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
//  BATCH CONFIG GENERATION API (批量配置生成API)
// =================================================================

const addressExtractionRegex = /(?:@|:\/\/)(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|\[[0-9a-fA-F:\.]+\]|(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,})(?::\d+)?(?:[\/?#]|$)/;

// 核心替换逻辑 - 确保所有原配置都被输出，但只有Cloudflare配置进行优选替换
function replaceAddressesInConfigs(baseConfigsData, addressList) {
    let generatedConfigs = [];
    const validAddressRegex = /^(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|\[[0-9a-fA-F:\.]+\]|(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,})$/;

    for (const configObj of baseConfigsData) {
        const baseConfig = configObj.config_data;
        const domainHosting = configObj.domain_hosting || 'Cloudflare';
        const configId = configObj.id || 0;

        let configType = getProtocol(baseConfig);
        let processedBaseConfig;
        const pushError = (msg) => generatedConfigs.push(`[错误] ${msg}`);

        // 首先，无论域名属性是什么，先输出原配置
        generatedConfigs.push(baseConfig);

        // 检查域名托管属性，只有Cloudflare的配置才进行优选替换
        if (domainHosting !== 'Cloudflare') {
            // 非Cloudflare配置只输出原配置，不进行优选替换
            console.log(`配置ID ${configId} 的域名托管属性为"${domainHosting}"，只输出原配置，不进行优选替换`);
            continue;
        }

        // 处理 vmess, vless, trojan 协议
        if (configType === 'trojan' || configType === 'vless' ||
            configType === 'shadowsocks' || configType === 'hysteria2' ||
            configType === 'tuic' || configType === 'anytls' ||
            configType === 'any-reality' || configType === 'reality') {

            const addressMatch = baseConfig.match(addressExtractionRegex);
            if (!addressMatch) {
                pushError(`${configType}配置中未找到地址，已跳过。`);
                continue;
            }
            processedBaseConfig = baseConfig;
        } else if (configType === 'vmess') {
            const encodedJson = baseConfig.substring('vmess://'.length);
            try {
                let vmessObj = JSON.parse(b64_to_utf8(encodedJson));
                if (!vmessObj.add) {
                    pushError(`VMess配置中未找到 "add" 字段，已跳过。`);
                    continue;
                }
                processedBaseConfig = vmessObj;
            } catch (e) {
                pushError(`VMess配置解码失败，已跳过。`);
                continue;
            }
        } else {
            pushError(`不支持的类型: ${configType}，已跳过。`);
            continue;
        }

        // 对Cloudflare配置进行优选替换
        for (const newAddr of addressList) {
            if (!validAddressRegex.test(newAddr) && !newAddr.includes(':')) {
                // simple validation
            }
            const cleanAddrForName = newAddr.replace(/[\[\]]/g, '');

            // 处理IPv6地址格式
            let formattedAddr = newAddr;
            if (newAddr.includes(':') && !newAddr.startsWith('[') && !newAddr.endsWith(']')) {
                formattedAddr = `[${newAddr}]`;
            }

            if (configType === 'trojan' || configType === 'vless' ||
                configType === 'shadowsocks' || configType === 'hysteria2' ||
                configType === 'tuic' || configType === 'anytls' ||
                configType === 'any-reality' || configType === 'reality') {

                try {
                    const url = new URL(processedBaseConfig);
                    const originalName = url.hash ? decodeURIComponent(url.hash.substring(1)) : `${configType}-node`;
                    const newName = `${originalName}-${cleanAddrForName}`;
                    url.hash = encodeURIComponent(newName);
                    url.hostname = formattedAddr;

                    const newUrlString = url.toString();
                    generatedConfigs.push(newUrlString);
                } catch (e) {
                    pushError(`处理 ${configType} 出错: ${e.message}`);
                }
            } else if (configType === 'vmess') {
                const tempVmessObj = JSON.parse(JSON.stringify(processedBaseConfig));
                const originalName = tempVmessObj.ps || tempVmessObj.remark || 'vmess-node';
                tempVmessObj.ps = `${originalName}-${cleanAddrForName}`;

                let formattedAddr = newAddr;
                if (newAddr.includes(':') && !newAddr.startsWith('[') && !newAddr.endsWith(']')) {
                    formattedAddr = `[${newAddr}]`;
                }
                tempVmessObj.add = formattedAddr;
                if (tempVmessObj.remark) delete tempVmessObj.remark;

                try {
                    generatedConfigs.push(`vmess://${utf8_to_b64(JSON.stringify(tempVmessObj))}`);
                } catch (e) {
                    pushError(`VMess编码失败: ${e.message}`);
                }
            }
        }
    }

    // 去重生成的配置
    return [...new Set(generatedConfigs)];
}

// 手动 or UUID 生成 (POST)
async function generateConfigs(request, env) {
    try {
        const body = await request.json();
        const addressListText = body.addressList || body.ipList;
        const { baseConfigUuid, baseConfig: baseConfigFromRequest } = body;

        let baseConfigsData = [];

        if (baseConfigUuid) {
            const configsFromDb = await fetchConfigsByUuidFromDB(baseConfigUuid, env);
            if (configsFromDb.length === 0) {
                return jsonResponse({ error: `未找到 UUID 为 "${baseConfigUuid}" 的任何基础配置。` }, 404);
            }
            baseConfigsData = configsFromDb;

            const hostingStats = {};
            configsFromDb.forEach(cfg => {
                const hosting = cfg.domain_hosting || 'Cloudflare';
                hostingStats[hosting] = (hostingStats[hosting] || 0) + 1;
            });
            console.log(`UUID ${baseConfigUuid} 域名托管统计:`, hostingStats);

        } else if (baseConfigFromRequest) {
            const configLines = baseConfigFromRequest.split('\n').map(s => s.trim()).filter(Boolean);
            if (configLines.length === 0) return jsonResponse({ error: "基础配置不能为空。" }, 400);

            baseConfigsData = configLines.map((config_data, index) => ({
                id: index,
                config_data,
                protocol: getProtocol(config_data),
                remark: null,
                domain_hosting: 'Cloudflare'
            }));
        } else {
            return jsonResponse({ error: "必须提供基础配置或UUID。" }, 400);
        }

        if (!addressListText || addressListText.trim() === '') {
            return jsonResponse({ error: "优选列表不能为空。" }, 400);
        }

        const addressList = addressListText.split('\n').map(a => a.trim()).filter(a => a.length > 0);
        const generatedConfigs = replaceAddressesInConfigs(baseConfigsData, addressList);

        const totalConfigs = baseConfigsData.length;
        const cloudflareConfigs = baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length;
        const otherHostingCount = totalConfigs - cloudflareConfigs;

        const successCount = generatedConfigs.filter(c => !c.startsWith('[错误]')).length;
        const errorCount = generatedConfigs.length - successCount;

        const expectedNonCloudflare = otherHostingCount;
        const expectedCloudflare = cloudflareConfigs * (addressList.length + 1);
        const expectedTotal = expectedNonCloudflare + expectedCloudflare;

        let message = `生成完成！成功 ${successCount} 条，预期输出 ${expectedTotal} 条`;
        if (errorCount > 0) message += `，失败 ${errorCount} 条`;

        if (otherHostingCount > 0) {
            message += ` (注：其中有 ${otherHostingCount} 个非Cloudflare配置只输出原配置，${cloudflareConfigs} 个Cloudflare配置进行了优选替换)`;
        }

        if (baseConfigUuid) {
            const clientIp = getClientIp(request);
            const userAgent = request.headers.get('user-agent') || 'unknown';
            await logConfigAccess(env, baseConfigUuid, 'api-generation', clientIp, userAgent);
        }

        return jsonResponse({
            configs: generatedConfigs,
            message: message,
            stats: {
                total_configs: totalConfigs,
                cloudflare_configs: cloudflareConfigs,
                other_hosting_configs: otherHostingCount,
                address_list_count: addressList.length,
                expected_output: expectedTotal,
                actual_output: successCount
            }
        });
    } catch (e) {
        console.error("生成配置出错:", e.message);
        return jsonResponse({ error: "内部错误: " + e.message }, 500);
    }
}

// 统一的批量获取接口
async function handleGetBatchAddresses(url, env) {
    const type = url.searchParams.get('type') || 'ip';

    try {
        if (type === 'domain') {
            const domainData = await fetchDomainsFromDB(env);
            if (domainData.length === 0) {
                return new Response("Database is empty (no domains found).", { status: 404 });
            }
            const listText = domainData.map(item => item.domain).join('\n');
            return new Response(listText, { headers: { 'Content-Type': 'text/plain;charset=UTF-8' } });
        } else {
            const ipType = url.searchParams.get('ipType') || 'all';
            const carrier = url.searchParams.get('carrier') || 'all';
            const ipData = await fetchIpsFromDB(ipType, carrier, env);

            if (ipData.length === 0) {
                return new Response(`No IPs found in DB for ipType=${ipType} and carrier=${carrier}.`, { status: 404 });
            }
            const listText = ipData.map(item => item.ip).join('\n');
            return new Response(listText, { headers: { 'Content-Type': 'text/plain;charset=UTF-8' } });
        }

    } catch (e) {
        console.error("Error in /batch-ip handler:", e.message);
        return new Response("Internal Error: " + e.message, { status: 500 });
    }
}

// 订阅链接生成配置 (GET)
async function handleGetBatchConfigs(uuid, urlParams, env, request) {
    if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);

    const baseConfigsData = await fetchConfigsByUuidFromDB(uuid, env);
    if (baseConfigsData.length === 0) return jsonResponse({ error: `UUID Not Found` }, 404);

    const totalConfigs = baseConfigsData.length;
    const cloudflareConfigs = baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length;
    const otherConfigs = totalConfigs - cloudflareConfigs;

    console.log(`订阅UUID ${uuid}: 总配置数${totalConfigs}, Cloudflare配置${cloudflareConfigs}, 其他域名托管配置${otherConfigs}`);

    const type = urlParams.get('type') || 'ip';
    let addressList = [];

    try {
        if (type === 'domain') {
            const domains = await fetchDomainsFromDB(env);
            addressList = domains.map(d => d.domain);
        } else {
            const ipType = urlParams.get('ipType') || 'all';
            const carrier = urlParams.get('carrier') || 'all';
            const ips = await fetchIpsFromDB(ipType, carrier, env);
            addressList = ips.map(i => i.ip);
        }
    } catch (e) {
        return jsonResponse({ error: "DB Error: " + e.message }, 500);
    }

    if (addressList.length === 0) {
        return jsonResponse({ error: `No valid addresses found for type=${type}` }, 404);
    }

    const generatedConfigs = replaceAddressesInConfigs(baseConfigsData, addressList);
    const filteredConfigs = generatedConfigs.filter(c => !c.startsWith('[错误]'));

    if (filteredConfigs.length === 0) {
        return new Response("Generation failed.", { status: 400 });
    }

    const body = btoa(filteredConfigs.join('\n'));

    const clientIp = getClientIp(request);
    const userAgent = request.headers.get('user-agent') || 'unknown';
    await logConfigAccess(env, uuid, 'subscription', clientIp, userAgent);

    return new Response(body, {
        status: 200,
        headers: {
            'Content-Type': 'text/plain;charset=UTF-8',
            'Subscription-User-Info': 'upload=0; download=0; total=10737418240000; expire=2524608000',
            'Profile-Update-Interval': '24',
            'X-Domain-Hosting-Filter': `Cloudflare=${cloudflareConfigs}, Other=${otherConfigs}`
        },
    });
}

// =================================================================
//  CLASH/MIHOMO CONFIG GENERATION (Clash配置文件生成)
// =================================================================

// 获取Clash配置模板
async function fetchClashConfigTemplate(templateUrl) {
    try {
        const response = await fetch(templateUrl);
        if (!response.ok) {
            console.error(`无法从URL获取模板: ${response.status} ${response.statusText}`);
            throw new Error(`无法从 ${templateUrl} 获取Clash配置文件模板。`);
        }
        return await response.text();
    } catch (error) {
        console.error('获取Clash模板失败:', error);
        throw new Error(`无法从 ${templateUrl} 获取Clash配置文件模板: ${error.message}`);
    }
}

// 将单个协议配置转换为Clash格式
function convertToClashFormat(configStr) {
    const protocol = getProtocol(configStr);

    // 处理VMess配置
    if (protocol === 'vmess') {
        try {
            const encodedJson = configStr.substring('vmess://'.length);
            const vmessObj = JSON.parse(b64_to_utf8(encodedJson));

            const clashConfig = {
                name: vmessObj.ps || vmessObj.remark || `VMess-${vmessObj.add}-${vmessObj.port}`,
                type: 'vmess',
                server: vmessObj.add,
                port: parseInt(vmessObj.port) || 443,
                uuid: vmessObj.id,
                alterId: vmessObj.aid || 0,
                cipher: vmessObj.scy || 'auto',
                tls: vmessObj.tls === 'tls',
                'skip-cert-verify': true
            };

            if (vmessObj.net === 'ws') {
                clashConfig.network = 'ws';
                clashConfig['ws-opts'] = {
                    path: vmessObj.path || '/',
                    headers: vmessObj.host ? { Host: vmessObj.host } : {}
                };
            } else if (vmessObj.net === 'h2') {
                clashConfig.network = 'h2';
                clashConfig['h2-opts'] = {
                    path: vmessObj.path || '/',
                    host: vmessObj.host ? [vmessObj.host] : []
                };
            } else if (vmessObj.net === 'grpc') {
                clashConfig.network = 'grpc';
                clashConfig['grpc-opts'] = {
                    'grpc-service-name': vmessObj.path || ''
                };
            }

            return clashConfig;
        } catch (error) {
            console.error('转换VMess配置失败:', error, configStr);
            return null;
        }
    }

    // 处理Trojan配置
    if (protocol === 'trojan') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `Trojan-${url.hostname}-${url.port}`;

            const clashConfig = {
                name: name,
                type: 'trojan',
                server: url.hostname,
                port: parseInt(url.port) || 443,
                password: url.username,
                sni: url.searchParams.get('sni') || url.hostname,
                'skip-cert-verify': true
            };

            const typeParam = url.searchParams.get('type') || '';
            if (typeParam === 'ws') {
                clashConfig.network = 'ws';
                const path = url.searchParams.get('path') || '/';
                const host = url.searchParams.get('host');

                clashConfig['ws-opts'] = {
                    path: path,
                    headers: host ? { Host: host } : {}
                };
            } else if (typeParam === 'grpc') {
                clashConfig.network = 'grpc';
                clashConfig['grpc-opts'] = {
                    'grpc-service-name': url.searchParams.get('serviceName') || ''
                };
            }

            return clashConfig;
        } catch (error) {
            console.error('转换Trojan配置失败:', error, configStr);
            return null;
        }
    }

    // 处理VLESS配置
    if (protocol === 'vless') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `VLESS-${url.hostname}-${url.port}`;

            const clashConfig = {
                name: name,
                type: 'vless',
                server: url.hostname,
                port: parseInt(url.port) || 443,
                uuid: url.username,
                tls: true,
                'skip-cert-verify': true
            };

            const typeParam = url.searchParams.get('type') || '';
            if (typeParam === 'ws') {
                clashConfig.network = 'ws';
                const path = url.searchParams.get('path') || '/';
                const host = url.searchParams.get('host');

                clashConfig['ws-opts'] = {
                    path: path,
                    headers: host ? { Host: host } : {}
                };
            } else if (typeParam === 'grpc') {
                clashConfig.network = 'grpc';
                clashConfig['grpc-opts'] = {
                    'grpc-service-name': url.searchParams.get('serviceName') || ''
                };
            } else if (typeParam === 'h2') {
                clashConfig.network = 'h2';
                const path = url.searchParams.get('path') || '/';
                const host = url.searchParams.get('host');

                clashConfig['h2-opts'] = {
                    path: path,
                    host: host ? [host] : []
                };
            }

            return clashConfig;
        } catch (error) {
            console.error('转换VLESS配置失败:', error, configStr);
            return null;
        }
    }

    // 处理Shadowsocks配置
    if (protocol === 'shadowsocks') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `SS-${url.hostname}-${url.port}`;

            let password = '';
            let method = 'aes-256-gcm';

            const userInfo = atob(url.username).split(':');
            if (userInfo.length >= 2) {
                method = userInfo[0];
                password = userInfo.slice(1).join(':');
            }

            const clashConfig = {
                name: name,
                type: 'ss',
                server: url.hostname,
                port: parseInt(url.port) || 8388,
                password: password,
                cipher: method,
                'udp-over-tcp': true
            };

            const pluginParam = url.searchParams.get('plugin');
            if (pluginParam) {
                clashConfig.plugin = pluginParam;
                const pluginOpts = url.searchParams.get('plugin-opts');
                if (pluginOpts) {
                    clashConfig['plugin-opts'] = pluginOpts;
                }
            }

            return clashConfig;
        } catch (error) {
            console.error('转换Shadowsocks配置失败:', error, configStr);
            return null;
        }
    }

    // 处理Hysteria2配置
    if (protocol === 'hysteria2') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `Hysteria2-${url.hostname}-${url.port}`;

            const clashConfig = {
                name: name,
                type: 'hysteria2',
                server: url.hostname,
                port: parseInt(url.port) || 443,
                password: url.username,
                sni: url.searchParams.get('sni') || url.hostname,
                'skip-cert-verify': true
            };

            const alpnParam = url.searchParams.get('alpn');
            if (alpnParam) {
                clashConfig.alpn = alpnParam.split(',');
            }

            const obfsParam = url.searchParams.get('obfs');
            if (obfsParam) {
                clashConfig.obfs = obfsParam;
                const obfsPassword = url.searchParams.get('obfs-password');
                if (obfsPassword) {
                    clashConfig['obfs-password'] = obfsPassword;
                }
            }

            const downMbps = url.searchParams.get('downspeed');
            const upMbps = url.searchParams.get('upspeed');
            if (downMbps) {
                clashConfig.down = downMbps;
            }
            if (upMbps) {
                clashConfig.up = upMbps;
            }

            return clashConfig;
        } catch (error) {
            console.error('转换Hysteria2配置失败:', error, configStr);
            return null;
        }
    }

    // 处理Tuic配置
    if (protocol === 'tuic') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `Tuic-${url.hostname}-${url.port}`;

            const clashConfig = {
                name: name,
                type: 'tuic',
                server: url.hostname,
                port: parseInt(url.port) || 443,
                uuid: url.username,
                password: url.password || '',
                sni: url.searchParams.get('sni') || url.hostname,
                'skip-cert-verify': true
            };

            const alpnParam = url.searchParams.get('alpn');
            if (alpnParam) {
                clashConfig.alpn = alpnParam.split(',');
            }

            const disableSni = url.searchParams.get('disable-sni');
            if (disableSni === 'true') {
                delete clashConfig.sni;
            }

            const congestionControl = url.searchParams.get('congestion-controller');
            if (congestionControl) {
                clashConfig['congestion-controller'] = congestionControl;
            }

            const udpRelayMode = url.searchParams.get('udp-relay-mode');
            if (udpRelayMode) {
                clashConfig['udp-relay-mode'] = udpRelayMode;
            }

            return clashConfig;
        } catch (error) {
            console.error('转换Tuic配置失败:', error, configStr);
            return null;
        }
    }

    // 处理AnyTLS配置
    if (protocol === 'anytls') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `AnyTLS-${url.hostname}-${url.port}`;

            let type = 'vless';
            let uuid = url.username;

            const clashConfig = {
                name: name,
                type: type,
                server: url.hostname,
                port: parseInt(url.port) || 443,
                uuid: uuid,
                tls: true,
                'skip-cert-verify': true
            };

            const typeParam = url.searchParams.get('type') || '';
            if (typeParam === 'ws') {
                clashConfig.network = 'ws';
                const path = url.searchParams.get('path') || '/';
                const host = url.searchParams.get('host');

                clashConfig['ws-opts'] = {
                    path: path,
                    headers: host ? { Host: host } : {}
                };
            } else if (typeParam === 'grpc') {
                clashConfig.network = 'grpc';
                clashConfig['grpc-opts'] = {
                    'grpc-service-name': url.searchParams.get('serviceName') || ''
                };
            }

            const sniParam = url.searchParams.get('sni') || url.searchParams.get('host');
            if (sniParam) {
                clashConfig.sni = sniParam;
            }

            const alpnParam = url.searchParams.get('alpn');
            if (alpnParam) {
                clashConfig.alpn = alpnParam.split(',');
            }

            return clashConfig;
        } catch (error) {
            console.error('转换AnyTLS配置失败:', error, configStr);
            return null;
        }
    }

    // 处理Reality/Any-reality配置
    if (protocol === 'any-reality' || protocol === 'reality') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `Reality-${url.hostname}-${url.port}`;

            const clashConfig = {
                name: name,
                type: 'vless',
                server: url.hostname,
                port: parseInt(url.port) || 443,
                uuid: url.username,
                'skip-cert-verify': true,
                'client-fingerprint': 'chrome'
            };

            const realityConfig = {
                type: 'reality',
                'server-name': url.searchParams.get('sni') || url.hostname,
                'public-key': url.searchParams.get('pbk') || '',
                'short-id': url.searchParams.get('sid') || ''
            };

            Object.assign(clashConfig, realityConfig);

            const typeParam = url.searchParams.get('type') || '';
            if (typeParam === 'grpc') {
                clashConfig.network = 'grpc';
                clashConfig['grpc-opts'] = {
                    'grpc-service-name': url.searchParams.get('serviceName') || ''
                };
            } else if (typeParam === 'tcp') {
                clashConfig.network = 'tcp';
            }

            const fpParam = url.searchParams.get('fp');
            if (fpParam) {
                clashConfig['client-fingerprint'] = fpParam;
            }

            return clashConfig;
        } catch (error) {
            console.error('转换Reality配置失败:', error, configStr);
            return null;
        }
    }

    return null;
}

// 生成完整的Clash配置文件
async function generateClashConfig(request, env) {
    try {
        const url = new URL(request.url);
        const uuid = url.searchParams.get('uuid');
        const addressType = url.searchParams.get('type') || 'ip';
        const ipType = url.searchParams.get('ipType') || 'all';
        const carrier = url.searchParams.get('carrier') || 'all';
        const template = url.searchParams.get('template') || '';

        if (!uuid) {
            return jsonResponse({ error: '缺少必要参数: uuid' }, 400);
        }

        if (!template) {
            return jsonResponse({ error: '缺少必要参数: template' }, 400);
        }

        const baseConfigsData = await fetchConfigsByUuidFromDB(uuid, env);
        if (baseConfigsData.length === 0) {
            return jsonResponse({ error: `未找到 UUID 为 "${uuid}" 的配置` }, 404);
        }

        let addressList = [];
        if (addressType === 'domain') {
            const domains = await fetchDomainsFromDB(env);
            addressList = domains.map(d => d.domain);
        } else {
            const ips = await fetchIpsFromDB(ipType, carrier, env);
            addressList = ips.map(i => i.ip);
        }

        if (addressList.length === 0) {
            return jsonResponse({ error: `未找到优选地址列表` }, 404);
        }

        const generatedRawConfigs = replaceAddressesInConfigs(baseConfigsData, addressList);
        const validRawConfigs = generatedRawConfigs.filter(c => !c.startsWith('[错误]'));

        if (validRawConfigs.length === 0) {
            return jsonResponse({ error: '未能生成有效的节点配置' }, 400);
        }

        let clashProxies = [];

        clashProxies.push({ name: '直连', type: 'direct' });

        for (const config of validRawConfigs) {
            const clashConfig = convertToClashFormat(config);
            if (clashConfig) {
                clashProxies.push(clashConfig);
            }
        }

        const uniqueClashProxiesMap = new Map();
        for (const proxy of clashProxies) {
            const stableString = JSON.stringify(proxy, Object.keys(proxy).sort());
            if (!uniqueClashProxiesMap.has(stableString)) {
                uniqueClashProxiesMap.set(stableString, proxy);
            }
        }
        clashProxies = [...uniqueClashProxiesMap.values()];

        const existingNames = new Set();
        for (let i = 0; i < clashProxies.length; i++) {
            let baseName = clashProxies[i].name;
            let currentName = baseName;
            let counter = 1;
            while (existingNames.has(currentName)) {
                currentName = `${baseName}-${counter++}`;
            }
            clashProxies[i].name = currentName;
            existingNames.add(currentName);
        }

        if (clashProxies.length <= 1 && clashProxies[0].name === '直连') {
            return jsonResponse({ error: '未能生成有效的代理节点' }, 400);
        }

        let templateUrl;
        if (template.startsWith('http://') || template.startsWith('https://')) {
            templateUrl = template;
        } else {
            const dbTemplate = await fetchTemplateFromDB(env, template);
            if (!dbTemplate) {
                return jsonResponse({ error: `未在数据库中找到模板 "${template}"` }, 404);
            }
            templateUrl = dbTemplate.url;
        }

        const templateContent = await fetchClashConfigTemplate(templateUrl);

        let finalConfig = templateContent;

        const proxiesMarker = '# 节点信息\nproxies:';
        let proxiesStartIndex = templateContent.indexOf(proxiesMarker);

        if (proxiesStartIndex === -1) {
            proxiesStartIndex = templateContent.indexOf('\nproxies:');
            if (proxiesStartIndex !== -1) {
                proxiesStartIndex++;
            }
        }

        if (proxiesStartIndex !== -1) {
            const insertPosition = templateContent.indexOf('\n', proxiesStartIndex + 'proxies:'.length);

            let proxiesEndIndex = templateContent.indexOf('\n\n', insertPosition);
            if (proxiesEndIndex === -1 || proxiesEndIndex < insertPosition) {
                proxiesEndIndex = templateContent.length;
            }

            const newProxiesContent = clashProxies.map(p => `  - ${JSON.stringify(p)}`).join('\n');

            finalConfig = templateContent.substring(0, insertPosition + 1) +
                newProxiesContent +
                templateContent.substring(proxiesEndIndex);
        } else {
            console.warn("Clash模板中未找到 'proxies:' 标记，将节点添加到文件末尾。");
            finalConfig = templateContent + `\n\n# ===== 生成的节点配置 =====\nproxies:\n${clashProxies.map(p => `  - ${JSON.stringify(p)}`).join('\n')}`;
        }

        const clientIp = getClientIp(request);
        const userAgent = request.headers.get('user-agent') || 'unknown';

        const referer = request.headers.get('referer');
        const isWebPageGeneration = referer && referer.includes(url.origin);
        const queryType = isWebPageGeneration ? 'api-generation' : 'subscription';

        await logConfigAccess(env, uuid, queryType, clientIp, userAgent);
        console.log(`记录${isWebPageGeneration ? '网页生成' : '订阅获取'}访问: uuid=${uuid}, clientIp=${clientIp}`);

        const protocolStats = {};
        clashProxies.forEach(proxy => {
            if (proxy.type !== 'direct') {
                protocolStats[proxy.type] = (protocolStats[proxy.type] || 0) + 1;
            }
        });

        return new Response(finalConfig, {
            status: 200,
            headers: {
                'Content-Type': 'text/yaml;charset=UTF-8',
                'Content-Disposition': `attachment; filename="clash-config-${uuid}-${new Date().toISOString().split('T')[0]}.yaml"`,
                'X-Generated-Date': new Date().toISOString(),
                'X-Total-Proxies-Unique': clashProxies.length.toString(),
                'X-Cloudflare-Configs': baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length.toString(),
                'X-Address-List-Count': addressList.length.toString(),
                'X-Template-Used': templateUrl,
                'X-Supported-Protocols': Object.keys(protocolStats).join(','),
                'X-Protocol-Counts': Object.entries(protocolStats).map(([k, v]) => `${k}:${v}`).join(',')
            }
        });

    } catch (error) {
        console.error('生成Clash配置出错:', error);
        return jsonResponse({ error: `内部错误: ${error.message}` }, 500);
    }
}

// =================================================================
//  MAIN ROUTER (主路由)
// =================================================================

export default {
    async fetch(request, env, ctx) {
        const url = new URL(request.url);
        const path = url.pathname;
        const method = request.method;
        const DOMAIN_NAME = url.origin;

        // URL模式匹配
        const manageUUID = new URLPattern({ pathname: '/manage/configs/:uuid' });
        const manageID = new URLPattern({ pathname: '/manage/configs/id/:id' });
        const subUUID = new URLPattern({ pathname: '/sub/:uuid' });
        const uuidStats = new URLPattern({ pathname: '/manage/stats/:uuid' });
        const batchUUID = new URLPattern({ pathname: '/batch-configs/:uuid' });

        try {
            if (method === 'GET') {
                // 根路径 - 显示整合的页面
                if (path === '/') {
                    return new Response(unifiedPageHtmlContent.replace(/YOUR_WORKER_DOMAIN_PATH/g, DOMAIN_NAME), {
                        headers: { 'Content-Type': 'text/html;charset=UTF-8' }
                    });
                }

                // ===== API: 原始订阅输出 =====
                const subMatch = subUUID.exec(url);
                if (subMatch) return await handleRawSubscription(subMatch.pathname.groups.uuid, env);

                // ===== API: 配置管理 =====
                const uuidMatch = manageUUID.exec(url);
                if (uuidMatch) return await handleGetConfigs(uuidMatch.pathname.groups.uuid, env);

                // ===== API: UUID访问统计 =====
                const statsMatch = uuidStats.exec(url);
                if (statsMatch) {
                    const urlObj = new URL(request.url);
                    const days = urlObj.searchParams.get('days') || 30;
                    const stats = await fetchUuidAccessStatsFromDB(statsMatch.pathname.groups.uuid, env, parseInt(days));
                    return jsonResponse(stats);
                }

                // ===== API: 批量配置订阅 =====
                const batchMatch = batchUUID.exec(url);
                if (batchMatch) {
                    return await handleGetBatchConfigs(batchMatch.pathname.groups.uuid, url.searchParams, env, request);
                }

                // ===== API: 获取IP/域名列表 =====
                if (path === '/batch-ip') {
                    return await handleGetBatchAddresses(url, env);
                }

                // ===== API: 前端获取地址列表 =====
                if (path === '/fetch-addresses') {
                    const type = url.searchParams.get('type') || 'ip';
                    if (type === 'domain') {
                        const domains = await fetchDomainsFromDB(env);
                        return jsonResponse({ list: domains.map(d => d.domain), message: `获取到 ${domains.length} 个域名` });
                    } else {
                        const ips = await fetchIpsFromDB(
                            url.searchParams.get('ipType') || 'all',
                            url.searchParams.get('carrierType') || 'all',
                            env
                        );
                        return jsonResponse({ list: ips.map(i => i.ip), message: `获取到 ${ips.length} 个IP` });
                    }
                }

                // ===== API: 获取模板列表 =====
                if (path === '/api/templates') {
                    try {
                        const templates = await fetchAllTemplatesFromDB(env);
                        return jsonResponse(templates);
                    } catch (error) {
                        return jsonResponse({ error: `获取模板列表失败: ${error.message}` }, 500);
                    }
                }

                // ===== API: Clash配置文件生成 =====
                if (path === '/clash-config') {
                    return await generateClashConfig(request, env);
                }

                // ===== 旧版API兼容 =====
                if (path === '/fetch-ips') {
                    const ips = await fetchIpsFromDB(url.searchParams.get('ipType') || 'all', url.searchParams.get('carrierType') || 'all', env);
                    return jsonResponse({ ips: ips.map(i => i.ip), message: `Success` });
                }
            }

            if (method === 'POST') {
                // ===== API: 配置管理 =====
                if (path === '/manage/configs') return await handleAddConfig(request, env);

                // ===== API: 批量配置生成 =====
                if (path === '/generate') return await generateConfigs(request, env);
            }

            if (method === 'PUT') {
                // ===== API: 配置更新 =====
                if (path === '/manage/configs') return await handleUpdateConfig(request, env);
            }

            if (method === 'DELETE') {
                // ===== API: 配置删除 =====
                const idMatch = manageID.exec(url);
                if (idMatch) return await handleDelete('id', idMatch.pathname.groups.id, env);
                const uuidMatch = manageUUID.exec(url);
                if (uuidMatch) return await handleDelete('uuid', uuidMatch.pathname.groups.uuid, env);
            }

            return new Response('404 Not Found', { status: 404 });
        } catch (err) {
            console.error("Worker error:", err);
            return new Response("Internal Error: " + err.message, { status: 500 });
        }
    }
};

// =================================================================
//  UNIFIED FRONTEND PAGE (整合的前端页面)
// =================================================================

// 全局样式
const globalStyle = `
html { font-size: 87.5%; } body, html { margin: 0; padding: 0; min-height: 100%; background-color: #fff; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
.container { width: 100%; min-height: 100vh; display: flex; flex-direction: column; justify-content: center; align-items: center; padding: 40px 20px; box-sizing: border-box; }
.content-group { width: 100%; max-width: 1200px; text-align: center; z-index: 10; box-sizing: border-box; }
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
.loader { width: 16px; height: 16px; border: 2px solid white; border-bottom-color: transparent; border-radius: 50%; display: inline-block; box-sizing: border-box; animation: rotation 1s linear infinite; margin-right: 8px; }
@keyframes rotation { 0% { transform: rotate(0deg); } 100% { transform: rotate(360deg); } }

/* Tab Navigation */
.tab-nav { display: flex; border-bottom: 2px solid #E8EBED; margin-bottom: 20px; }
.tab-btn { padding: 12px 24px; background: transparent; border: none; border-bottom: 2px solid transparent; font-size: 1rem; color: #89949B; cursor: pointer; transition: all 0.3s; }
.tab-btn:hover { color: #5a666d; }
.tab-btn.active { color: #3d474d; border-bottom-color: #3d474d; font-weight: 500; }

/* Tab Content */
.tab-content { display: none; }
.tab-content.active { display: block; }

/* Table & Modal Specific */
.table-container { overflow-x: auto; border: 2px solid #89949B; border-radius: 4px; background: #fff; margin-top:20px;}
table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
th, td { padding: 10px 14px; text-align: left; border-bottom: 2px solid #E8EBED; white-space: nowrap; }
th { font-weight: bold; color: #3d474d; background-color: #f0f2f5; }
.config-data-cell { white-space: normal; word-break: break-all; max-width: 200px; font-size: 0.8rem; color: #666; }
.actions-cell button { margin-right: 5px; padding: 4px 8px; font-size: 0.8rem; }
.domain-hosting-cell { font-size: 0.85rem; font-weight: 500; }

/* Domain Hosting Badges */
.hosting-badge { 
    display: inline-block; 
    padding: 2px 8px; 
    border-radius: 12px; 
    font-size: 0.75rem; 
    font-weight: 500; 
    line-height: 1.4;
}
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

/* Modal Styles */
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

/* Statistics Styles */
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

/* Enhanced Select Styles */
.hosting-select { 
    padding: 10px 12px; 
    border: 2px solid #89949B; 
    border-radius: 4px; 
    background-color: #fff;
    font-size: 0.9rem;
    color: #5a666d;
    font-family: 'SF Mono', 'Courier New', monospace;
    cursor: pointer;
    transition: all 0.2s ease;
    appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12' fill='none'%3E%3Cpath d='M2.5 4.5L6 8L9.5 4.5' stroke='%235a666d' stroke-width='1.5' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 12px center;
    background-size: 12px;
    padding-right: 32px;
}
.hosting-select:hover { 
    border-color: #5a666d; 
    background-color: #f8f9fa;
}
.hosting-select:focus { 
    outline: none; 
    border-color: #3d474d; 
    box-shadow: 0 0 0 3px rgba(93, 105, 109, 0.1);
}
.hosting-select:disabled { 
    opacity: 0.6; 
    cursor: not-allowed;
    background-color: #f0f2f5;
}
.hosting-option { 
    padding: 10px 12px; 
    font-size: 0.9rem;
    font-family: 'SF Mono', 'Courier New', monospace;
}
.hosting-option:hover {
    background-color: #f0f2f5;
}

/* Radio Group Styles */
.radio-group { display: flex; flex-wrap: wrap; gap: 10px; }
.radio-group label { padding: 6px 14px; background: #E8EBED; border: 2px solid #89949B; border-radius: 4px; color: #5a666d; font-size: 0.85rem; cursor: pointer; transition: all 0.3s; }
.radio-group input[type="radio"] { display: none; }
.radio-group input[type="radio"]:checked + span { background: #89949B; color: white; }
.radio-group label:hover { background: #d1d5d8; }
.radio-group input[type="radio"]:checked + span:hover { background: #89949B; color: white; }

/* QR Code Styles */
.qr-code-container {
  margin-top: 16px;
  text-align: center;
  padding: 16px;
  background: #f8f9fa;
  border: 1px solid #E8EBED;
  border-radius: 8px;
}
.qr-code-container.hidden {
  display: none;
}
.qr-code-image {
  max-width: 200px;
  height: auto;
  border: 1px solid #ddd;
  border-radius: 4px;
  margin: 10px 0;
}
.qr-code-label {
  display: block;
  color: #5a666d;
  font-weight: 500;
  margin-bottom: 8px;
  font-size: 0.9rem;
}
.qr-code-buttons {
  display: flex;
  gap: 8px;
  justify-content: center;
  margin-top: 12px;
}
.qr-code-buttons .nav-btn {
  font-size: 0.85rem;
  padding: 6px 12px;
}
.qr-code-info {
  font-size: 0.8rem;
  color: #89949B;
  margin-top: 8px;
}

/* Subscription Link Area */
.subscription-link-box {
  margin-top: 15px;
  background: #f8f9fa;
  border: 1px solid #E8EBED;
  border-radius: 8px;
  padding: 15px;
}
.subscription-link-box label {
  display: block;
  color: #5a666d;
  font-weight: 500;
  margin-bottom: 8px;
  font-size: 0.9rem;
}
.link-input-container {
  display: flex;
  gap: 10px;
  margin-bottom: 15px;
}
.link-input-container input {
  flex-grow: 1;
  background: #fff;
  border: 2px solid #89949B;
  color: #5a666d;
}

/* Template Selection */
.template-select-container {
  margin-top: 10px;
}
.template-select-container select {
  width: 100%;
  margin-bottom: 10px;
}
.template-description {
  font-size: 0.85rem;
  color: #89949B;
  margin-top: 8px;
  line-height: 1.4;
}

/* Loading State */
.loading-state {
  text-align: center;
  padding: 20px;
  color: #89949B;
}
.loading-state .loader {
  width: 32px;
  height: 32px;
  border: 3px solid #89949B;
  border-bottom-color: transparent;
  margin-bottom: 10px;
}

/* Protocol Support */
.protocol-support {
  margin-top: 20px;
  padding: 15px;
  background: #f0f7ff;
  border: 1px solid #cce5ff;
  border-radius: 8px;
}
.protocol-support h3 {
  margin-top: 0;
  color: #0066cc;
  font-size: 1.1rem;
}
.protocol-list {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 10px;
}
.protocol-badge {
  display: inline-block;
  padding: 4px 10px;
  background: #e6f2ff;
  border: 1px solid #99ccff;
  border-radius: 4px;
  font-size: 0.8rem;
  color: #0066cc;
  font-weight: 500;
}
.protocol-badge.new {
  background: #fff0e6;
  border-color: #ffcc99;
  color: #ff6600;
}

/* Responsive Design */
@media (max-width: 768px) { 
  html { font-size: 100%; } 
  .container { padding: 20px 15px; justify-content: flex-start; } 
  .profile-name { font-size: 1.8rem; } 
  .profile-quote { font-size: 0.95rem; margin-bottom: 20px; } 
  .card { padding: 20px 15px; margin-bottom: 20px; } 
  .card h2 { font-size: 1.3rem; } 
  .nav-btn { padding: 9px 12px; font-size: 0.9rem; } 
  .tab-btn { padding: 10px 16px; font-size: 0.9rem; }
}
`;

// =================================================================
//  UNIFIED PAGE CONTENT (整合的页面内容)
// =================================================================

const unifiedPageHtmlContent = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <link rel="icon" href="https://s3.yangzifun.org/logo.ico" type="image/x-icon">
  <title>代理配置管理工具</title>
  <style>${globalStyle}</style>
  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
</head>
<body>
  <div id="toast-container"></div>
  
  <!-- Editor Modal -->
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
      <h1 class="profile-name">代理配置管理工具</h1>
      <p class="profile-quote">一站式代理配置管理、生成与订阅服务</p>
      
      <!-- Tab Navigation -->
      <div class="tab-nav">
        <button class="tab-btn active" onclick="switchTab('batch')">批量生成</button>
        <button class="tab-btn" onclick="switchTab('manage')">配置管理</button>
        <button class="tab-btn" onclick="switchTab('clash')">订阅生成</button>
      </div>

      <!-- Batch Generation Tab -->
      <div id="batch-tab" class="tab-content active">
        <div class="card">
          <h2>1. 基础配置</h2>
          <div class="form-group">
            <div class="info-box">
              <strong>注意：</strong> 所有配置都会输出原配置。
              <br>域名托管属性为"Cloudflare"的配置会进行优选替换（每条原配置 + 优选地址数条新配置）。
              <br>其他域名托管配置（如阿里ESA、腾讯Edgeone、AWS Cloudfront、Gcore、Fastly等）只输出原配置，不进行优选替换。
            </div>
          </div>
          <div class="form-group radio-group">
              <label><input type="radio" name="genConfigSource" value="manual" checked><span>手动粘贴</span></label>
              <label><input type="radio" name="genConfigSource" value="uuid"><span>从UUID获取</span></label>
          </div>
          <div id="genManualConfigInput" class="form-group">
              <textarea id="genBaseConfigInput" placeholder="在此粘贴一个或多个基础配置，每行一个。" rows="4"></textarea>
          </div>
          <div id="genUuidConfigInput" class="form-group hidden">
              <input type="text" id="genBaseConfigUuidInput" placeholder="输入已存储的UUID">
              <div class="info-box">在"配置管理"标签页添加和管理UUID。
              <br>所有配置都会输出原配置，只有域名托管属性为"Cloudflare"的配置会进行优选替换。</div>
          </div>
        </div>

        <div class="card">
          <h2>2. 优选列表 (IP/域名)</h2>
          <div class="form-group">
            <label>地址类型</label>
            <div class="radio-group">
              <label><input type="radio" name="genAddressType" value="ip" checked onchange="toggleBatchAddressOptions()"><span>IP 地址</span></label>
              <label><input type="radio" name="genAddressType" value="domain" onchange="toggleBatchAddressOptions()"><span>优选域名</span></label>
            </div>
          </div>

          <div id="batchIpOptionsContainer">
              <div class="form-group">
                <label>IP 类型</label>
                <div class="radio-group">
                  <label><input type="radio" name="genIpType" value="all" checked><span>全部</span></label>
                  <label><input type="radio" name="genIpType" value="v4"><span>仅IPv4</span></label>
                  <label><input type="radio" name="genIpType" value="v6"><span>仅IPv6</span></label>
                </div>
              </div>
              <div class="form-group">
                <label>运营商</label>
                <div class="radio-group">
                  <label><input type="radio" name="genCarrierType" value="all" checked><span>全部</span></label>
                  <label><input type="radio" name="genCarrierType" value="CM"><span>移动</span></label>
                  <label><input type="radio" name="genCarrierType" value="CU"><span>联通</span></label>
                  <label><input type="radio" name="genCarrierType" value="CT"><span>电信</span></label>
                </div>
              </div>
          </div>

          <div id="batchDomainInfoBox" class="info-box hidden">
             将使用数据库中存储的 Cloudflare 优选域名列表进行替换。
          </div>

          <button id="genFetchBtn" class="nav-btn" style="width:100%; margin-bottom: 16px;" onclick="genFetchAddresses()">获取优选列表</button>
          
          <div id="batchSubscriptionLinkBox" class="form-group hidden" style="display:none; gap:10px;">
              <input type="text" id="batchSubscriptionLink" readonly style="margin-bottom:0; color:#5a666d; background-color: #f8f9fa;">
              <button class="nav-btn" onclick="copyBatchSubscriptionLink()" style="white-space: nowrap;">复制</button>
          </div>

          <textarea id="genAddressListInput" placeholder="点击上方按钮获取，或在此手动粘贴 IP 或 域名 列表..." rows="4"></textarea>
        </div>

        <div class="card">
          <h2>3. 生成结果</h2>
          <div id="batchGeneratedLinkBox" class="form-group hidden" style="display:none; gap:10px;">
            <label style="align-self:center; white-space:nowrap; margin-bottom:0; margin-right:5px;">订阅:</label>
            <input type="text" id="batchGeneratedConfigLink" readonly style="margin-bottom:0; color:#5a666d; background-color: #f8f9fa;">
            <button class="nav-btn" onclick="copyBatchGeneratedLink()" style="white-space: nowrap;">复制</button>
          </div>
          <textarea id="genResultTextarea" readonly placeholder="点击下方"生成配置"按钮..." rows="6"></textarea>
          
          <div id="batchQrCodeContainer" class="qr-code-container hidden">
            <label class="qr-code-label">订阅链接二维码</label>
            <div id="batchQrCodeImageContainer">
              <img id="batchQrCodeImage" src="" alt="订阅链接二维码">
            </div>
            <div class="qr-code-buttons">
              <button class="nav-btn" onclick="downloadBatchQRCode()">下载二维码</button>
              <button class="nav-btn" onclick="shareBatchQRCode()">分享二维码</button>
              <button class="nav-btn" onclick="refreshBatchQRCode()">刷新</button>
            </div>
            <div class="qr-code-info">
              扫描二维码即可导入订阅配置。二维码内容为上方订阅链接。
            </div>
          </div>
        </div>

        <div class="form-group" style="display: flex; gap: 10px; margin-top: 20px;">
          <button id="genGenerateButton" class="nav-btn primary" style="flex: 1;" onclick="genGenerateConfigs()">生成配置</button>
          <button id="copyResultButton" class="nav-btn" style="flex: 1;" onclick="copyResults()">复制结果 (Base64)</button>
        </div>
      </div>

      <!-- Config Management Tab -->
      <div id="manage-tab" class="tab-content">
        <div class="card">
          <h2>1. 检索订阅</h2>
          <div class="form-group" style="display:flex; gap:10px;">
             <input type="text" id="queryUuidInput" placeholder="请输入您的 UUID" style="margin-bottom:0;">
             <button id="queryBtn" class="nav-btn primary" onclick="manageQueryByUuid()">查询</button>
          </div>
          
          <div id="manageSubLinkDisplay" class="form-group hidden" style="display:none; gap:10px; margin-top:10px;">
             <input type="text" id="manageSubUrlLink" readonly style="margin-bottom:0; color:#5a666d; background-color: #f8f9fa;">
             <button class="nav-btn" onclick="copyManageSubLink()" style="white-space: nowrap;">复制</button>
          </div>
        </div>

        <div id="manageResultCard" class="card hidden">
          <h2>2. 配置列表</h2>
          <div style="text-align:right; margin-bottom:10px;">
             <button class="nav-btn" style="background:#d44; color:#fff; border-color:#d44;" onclick="deleteAll()">删除本组所有</button>
          </div>
          <div id="queryResultsContainer"></div>
        </div>

        <div id="manageStatsCard" class="card hidden">
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
          
          <div id="manageStatsSummary" class="stat-grid"></div>
          
          <div class="chart-container">
            <canvas id="manageStatsChart"></canvas>
          </div>
          
          <div style="margin-top: 20px;">
            <h3 style="font-size: 1.1rem; margin: 0 0 10px 0;">最近访问记录</h3>
            <div id="manageRecentLogs" class="access-log-container">
              <div style="text-align: center; padding: 20px; color: #6b7280;">加载中...</div>
            </div>
          </div>
        </div>

        <div id="manageAddCard" class="card hidden">
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
              <strong>说明：</strong> 选择此配置使用的域名托管服务。这有助于管理和分类不同CDN厂商的配置。
              <br>新增选项：Gcore, Fastly, CacheFly, LightCDN, Vercel, Netlify，"无"表示不使用域名托管。
            </div>
          </div>
          <div class="form-group">
            <label>配置数据 (可批量添加)</label>
            <textarea id="addConfigData" placeholder="支持批量添加：
vmess://...
vless://...
trojan://..." rows="4"></textarea>
          </div>
          <button onclick="manageAddConfig()" class="nav-btn primary" style="width:100%">添加到当前 UUID</button>
        </div>
      </div>

      <!-- Clash Subscription Tab -->
      <div id="clash-tab" class="tab-content">
        <div class="card">
          <h2>1. 配置信息</h2>
          <div class="form-group">
            <div class="info-box">
              <strong>注意：</strong> 所有配置都会输出原配置。
              <br>域名托管属性为"Cloudflare"的配置会进行优选替换（每条原配置 + 优选地址数条新配置）。
              <br>其他域名托管配置（如阿里ESA、腾讯Edgeone、AWS Cloudfront、Gcore、Fastly等）只输出原配置，不进行优选替换。
            </div>
          </div>
          <div class="form-group">
            <input type="text" id="clashConfigUuid" placeholder="输入UUID（必须在配置管理中创建配置）">
            <div class="info-box">
              在"配置管理"标签页添加和管理配置，获取UUID后在此输入。
            </div>
          </div>
          
          <div class="protocol-support">
            <h3>支持的协议类型</h3>
            <div class="protocol-list">
              <span class="protocol-badge">VMess</span>
              <span class="protocol-badge">VLESS</span>
              <span class="protocol-badge">Trojan</span>
              <span class="protocol-badge">Shadowsocks</span>
              <span class="protocol-badge new">Hysteria2</span>
              <span class="protocol-badge new">Tuic</span>
              <span class="protocol-badge new">AnyTLS</span>
              <span class="protocol-badge new">Any-reality</span>
              <span class="protocol-badge new">Reality</span>
            </div>
            <p style="margin-top: 10px; font-size: 0.85rem; color: #666;">
              现在支持更多现代协议，包括高速传输协议和最新的加密技术。
            </p>
          </div>
        </div>

        <div class="card">
          <h2>2. 模板选择</h2>
          <div class="form-group">
            <label>选择配置文件模板</label>
            <div class="template-select-container">
              <select id="templateSelect" onchange="handleTemplateChange()">
                <option value="">正在加载模板...</option>
              </select>
              <div id="templateDescription" class="template-description">
                请从下拉列表中选择一个配置文件模板
              </div>
            </div>
          </div>
          
          <div id="customTemplateInput" class="form-group hidden">
            <input type="text" id="customTemplateUrl" placeholder="输入自定义模板URL，如: https://raw.githubusercontent.com/your-repo/config.yaml">
            <div class="info-box">
              请输入可公开访问的YAML配置文件的完整URL地址。
            </div>
          </div>
          
          <div id="templateLoading" class="loading-state hidden">
            <div class="loader"></div>
            <p>正在加载模板列表...</p>
          </div>
          
          <div id="templateError" class="info-box hidden">
            无法加载模板列表，请稍后重试。
          </div>
          
          <div id="templateInfoBox" class="info-box">
            <strong>模板说明：</strong>
            <br>• 所有模板均从数据库读取
            <br>• 模板包含完整的规则和策略组配置
            <br>• 选择不同的模板可以获得不同的分流规则和优化策略
            <br><br><strong>注意：</strong>如果数据库中没有配置模板，将无法生成配置文件。
          </div>
        </div>

        <div class="card">
          <h2>3. 优选地址配置</h2>
          <div class="form-group">
            <label>地址类型</label>
            <div class="radio-group">
              <label><input type="radio" name="clashAddressType" value="ip" checked onchange="toggleClashAddressOptions()"><span>IP 地址</span></label>
              <label><input type="radio" name="clashAddressType" value="domain" onchange="toggleClashAddressOptions()"><span>优选域名</span></label>
            </div>
          </div>

          <div id="clashIpOptionsContainer">
              <div class="form-group">
                <label>IP 类型</label>
                <div class="radio-group">
                  <label><input type="radio" name="clashIpType" value="all" checked><span>全部</span></label>
                  <label><input type="radio" name="clashIpType" value="v4"><span>仅IPv4</span></label>
                  <label><input type="radio" name="clashIpType" value="v6"><span>仅IPv6</span></label>
                </div>
              </div>
              <div class="form-group">
                <label>运营商</label>
                <div class="radio-group">
                  <label><input type="radio" name="clashCarrier" value="all" checked><span>全部</span></label>
                  <label><input type="radio" name="clashCarrier" value="CM"><span>移动</span></label>
                  <label><input type="radio" name="clashCarrier" value="CU"><span>联通</span></label>
                  <label><input type="radio" name="clashCarrier" value="CT"><span>电信</span></label>
                </div>
              </div>
          </div>

          <div id="clashDomainInfoBox" class="info-box hidden">
            将使用数据库中存储的 Cloudflare 优选域名列表进行替换。
          </div>
        </div>

        <div class="card">
          <h2>4. 生成结果</h2>
          <div class="form-group">
            <button id="generateClashBtn" class="nav-btn primary" style="width:100%; padding: 12px;" onclick="generateClashConfig()" disabled>生成Clash订阅文件</button>
          </div>
          
          <div class="subscription-link-box hidden" id="clashSubscriptionLinkBox">
            <label>订阅链接</label>
            <div class="link-input-container">
              <input type="text" id="clashGeneratedLink" readonly>
              <button class="nav-btn" onclick="copyClashGeneratedLink()">复制</button>
            </div>
            
            <div id="clashQrCodeContainer" class="qr-code-container hidden">
              <label class="qr-code-label">订阅链接二维码</label>
              <div id="clashQrCodeImageContainer">
                <img id="clashQrCodeImage" src="" alt="订阅链接二维码">
              </div>
              <div class="qr-code-buttons">
                <button class="nav-btn" onclick="downloadClashQRCode()">下载二维码</button>
                <button class="nav-btn" onclick="shareClashQRCode()">分享</button>
                <button class="nav-btn" onclick="refreshClashQRCode()">刷新</button>
              </div>
              <div class="qr-code-info">
                扫描二维码即可导入Clash配置文件到客户端。二维码内容为上方订阅链接。
              </div>
            </div>
          </div>
          
          <div class="info-box">
            <strong>新特性：</strong> 现在支持更多现代协议，包括Hysteria2、Tuic、AnyTLS、Any-reality等。
            <br>配置文件基于您选择的模板生成，包含完整的规则和策略组。
            <br>生成的订阅链接可直接在Clash/Mihomo客户端中使用。
          </div>
        </div>
      </div>

      <footer class="footer">
         <p>Powered by <a href="https://www.yangzifun.org">YZFN</a> | <a href="https://www.yangzifun.org/security.html">安全声明</a></p>
      </footer>
    </div>
  </div>

  <script>
    const WORKER_DOMAIN = "YOUR_WORKER_DOMAIN_PATH";
    const QR_CODE_API = "https://qrcode.api.yangzifun.org/qr";
    const toastIcons = { success: '✅', error: '❌', info: 'ℹ️' };
    
    // Shared variables
    let currentUuid = '';
    let statsChart = null;
    let currentChartType = 'split';
    let currentSubscriptionLink = '';
    let templatesList = [];
    
    // Domain hosting style mapping
    const hostingStyleMap = {
      'Cloudflare': 'hosting-cloudflare',
      '阿里ESA': 'hosting-aliyun',
      '腾讯Edgeone': 'hosting-tencent',
      'AWS Cloudfront': 'hosting-aws',
      'Gcore': 'hosting-gcore',
      'Fastly': 'hosting-fastly',
      'CacheFly': 'hosting-cachefly',
      'LightCDN': 'hosting-lightcdn',
      'Vercel': 'hosting-vercel',
      'Netlify': 'hosting-netlify',
      '无': 'hosting-none',
      '其他': 'hosting-other'
    };
    
    // Utility functions
    function showToast(msg, type='info') { 
      const c = document.getElementById('toast-container'); 
      const t = document.createElement('div'); 
      t.className = 'toast'; 
      t.innerHTML = \`<span>\${toastIcons[type]} \${msg}</span>\`; 
      c.appendChild(t); 
      setTimeout(()=>t.remove(), 5000); 
    }
    
    function setButtonLoading(btn, ld, txt) { 
      if(ld){ 
        btn.ds = btn.innerHTML;
        btn.disabled = true; 
        btn.innerHTML = '<span class="loader"></span>处理中...'; 
      } else { 
        btn.disabled = false; 
        if(btn.ds) btn.innerHTML = btn.ds; 
      } 
    }
    
    function b64DecodeUnicode(str) { 
      return decodeURIComponent(atob(str).split('').map(c=>'%'+('00'+c.charCodeAt(0).toString(16)).slice(-2)).join('')); 
    }
    
    function b64EncodeUnicode(str) { 
      return btoa(encodeURIComponent(str).replace(/%([0-9A-F]{2})/g,(m,p1)=>String.fromCharCode(parseInt(p1,16)))); 
    }
    
    function formatDate(dateStr) {
      if (!dateStr) return '';
      const date = new Date(dateStr);
      const month = (date.getMonth() + 1).toString().padStart(2, '0');
      const day = date.getDate().toString().padStart(2, '0');
      return \`\${month}/\${day}\`;
    }
    
    function formatDateTime(dateStr) {
      if (!dateStr) return '';
      const date = new Date(dateStr);
      const month = (date.getMonth() + 1).toString().padStart(2, '0');
      const day = date.getDate().toString().padStart(2, '0');
      const hours = date.getHours().toString().padStart(2, '0');
      const minutes = date.getMinutes().toString().padStart(2, '0');
      return \`\${month}/\${day} \${hours}:\${minutes}\`;
    }
    
    // Tab switching
    function switchTab(tabName) {
      // Hide all tabs
      document.querySelectorAll('.tab-content').forEach(tab => {
        tab.classList.remove('active');
      });
      
      // Deactivate all tab buttons
      document.querySelectorAll('.tab-btn').forEach(btn => {
        btn.classList.remove('active');
      });
      
      // Show selected tab
      document.getElementById(\`\${tabName}-tab\`).classList.add('active');
      
      // Activate selected tab button
      event.target.classList.add('active');
      
      // Load templates if switching to clash tab
      if (tabName === 'clash') {
        loadTemplates();
      }
    }
    
    // ============================================
    // Batch Generation Tab Functions
    // ============================================
    
    function toggleBatchAddressOptions() {
      const type = document.querySelector('input[name="genAddressType"]:checked').value;
      const ipContainer = document.getElementById('batchIpOptionsContainer');
      const domainBox = document.getElementById('batchDomainInfoBox');
      if (type === 'domain') {
        ipContainer.classList.add('hidden');
        domainBox.classList.remove('hidden');
      } else {
        ipContainer.classList.remove('hidden');
        domainBox.classList.add('hidden');
      }
    }
    
    // Tab switching for Config Source
    const sourceRadios = document.querySelectorAll('input[name="genConfigSource"]');
    sourceRadios.forEach(r => r.addEventListener('change', (e) => {
      if(e.target.value === 'uuid') {
        document.getElementById('genManualConfigInput').classList.add('hidden');
        document.getElementById('genUuidConfigInput').classList.remove('hidden');
      } else {
        document.getElementById('genManualConfigInput').classList.remove('hidden');
        document.getElementById('genUuidConfigInput').classList.add('hidden');
      }
    }));
    
    async function genFetchAddresses() {
      const btn = document.getElementById('genFetchBtn');
      const textarea = document.getElementById('genAddressListInput');
      const subBox = document.getElementById('batchSubscriptionLinkBox');
      const subInput = document.getElementById('batchSubscriptionLink');
      
      subBox.style.display = 'none';
      subBox.classList.add('hidden');
      
      setButtonLoading(btn, true, '');
      
      try {
        const addrType = document.querySelector('input[name="genAddressType"]:checked').value;
        let url = \`\${WORKER_DOMAIN}/fetch-addresses?type=\${addrType}\`;
        
        if (addrType === 'ip') {
          const ipType = document.querySelector('input[name="genIpType"]:checked').value;
          const carrier = document.querySelector('input[name="genCarrierType"]:checked').value;
          url += \`&ipType=\${ipType}&carrierType=\${carrier}&source=database\`;
          
          const subUrl = \`\${WORKER_DOMAIN}/batch-ip?type=ip&ipType=\${ipType}&carrier=\${carrier}\`;
          subInput.value = subUrl;
          subBox.style.display = 'flex';
          subBox.classList.remove('hidden');
          
        } else {
          const subUrl = \`\${WORKER_DOMAIN}/batch-ip?type=domain\`;
          subInput.value = subUrl;
          subBox.style.display = 'flex';
          subBox.classList.remove('hidden');
        }
        
        const res = await fetch(url);
        const data = await res.json();
        
        if (data.error) throw new Error(data.error);
        
        if (data.list && Array.isArray(data.list)) {
          textarea.value = data.list.join('\\n');
          showToast(data.message || \`获取成功: \${data.list.length} 条\`, 'success');
        } else {
          throw new Error("返回数据格式错误");
        }
      } catch (e) {
        showToast(e.message, 'error');
        textarea.value = "";
      } finally {
        setButtonLoading(btn, false, '获取优选列表');
      }
    }
    
    async function genGenerateConfigs() {
      const btn = document.getElementById('genGenerateButton');
      const resultArea = document.getElementById('genResultTextarea');
      const linkBox = document.getElementById('batchGeneratedLinkBox');
      const linkInput = document.getElementById('batchGeneratedConfigLink');
      const qrCodeContainer = document.getElementById('batchQrCodeContainer');
      
      linkBox.style.display = 'none';
      linkBox.classList.add('hidden');
      qrCodeContainer.classList.add('hidden');
      
      setButtonLoading(btn, true, '');
      
      try {
        const configSource = document.querySelector('input[name="genConfigSource"]:checked').value;
        const addressList = document.getElementById('genAddressListInput').value;
        
        let payload = { addressList };
        
        if (configSource === 'uuid') {
          const uuid = document.getElementById('genBaseConfigUuidInput').value.trim();
          if(!uuid) throw new Error("请输入 UUID");
          payload.baseConfigUuid = uuid;
          
          const addrType = document.querySelector('input[name="genAddressType"]:checked').value;
          let linkParams = \`type=\${addrType}\`;
          if(addrType === 'ip') {
            const ipType = document.querySelector('input[name="genIpType"]:checked').value;
            const carrier = document.querySelector('input[name="genCarrierType"]:checked').value;
            linkParams += \`&ipType=\${ipType}&carrier=\${carrier}\`;
          }
          const subLink = \`\${WORKER_DOMAIN}/batch-configs/\${uuid}?\${linkParams}\`;
          linkInput.value = subLink;
          linkBox.style.display = 'flex';
          linkBox.classList.remove('hidden');
          
          // Generate QR code
          generateBatchQRCode(subLink);
          
        } else {
          const manualConfig = document.getElementById('genBaseConfigInput').value.trim();
          if(!manualConfig) throw new Error("请输入基础配置");
          payload.baseConfig = manualConfig;
          
          showToast('手动模式不支持二维码生成，请使用UUID模式获取订阅链接', 'info');
        }
        
        const res = await fetch(\`\${WORKER_DOMAIN}/generate\`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        });
        const data = await res.json();
        
        if (data.error) throw new Error(data.error);
        
        if (data.configs && Array.isArray(data.configs)) {
          resultArea.value = data.configs.join('\\n');
          showToast(data.message || "生成成功", 'success');
          
          if (data.stats) {
            console.log("生成统计:", data.stats);
          }
        }
      } catch (e) {
        showToast(e.message, 'error');
      } finally {
        setButtonLoading(btn, false, '生成配置');
      }
    }
    
    function copyResults() {
      const content = document.getElementById('genResultTextarea').value;
      if (!content) return showToast('没有可复制的内容', 'error');
      const b64 = btoa(unescape(encodeURIComponent(content)));
      navigator.clipboard.writeText(b64).then(() => showToast('已复制 Base64 编码结果', 'success'));
    }
    
    function copyBatchGeneratedLink() {
      const link = document.getElementById('batchGeneratedConfigLink').value;
      if(!link) return;
      navigator.clipboard.writeText(link).then(() => showToast('订阅链接已复制', 'success'));
    }
    
    function copyBatchSubscriptionLink() {
      const link = document.getElementById('batchSubscriptionLink').value;
      if(!link) return;
      navigator.clipboard.writeText(link).then(() => showToast('列表地址已复制', 'success'));
    }
    
    // Batch QR Code functions
    function generateBatchQRCode(subscriptionLink) {
      if (!subscriptionLink) return;
      
      currentSubscriptionLink = subscriptionLink;
      const qrCodeContainer = document.getElementById('batchQrCodeContainer');
      const qrCodeImage = document.getElementById('batchQrCodeImage');
      
      const encodedUrl = encodeURIComponent(subscriptionLink);
      const qrCodeUrl = \`\${QR_CODE_API}?text=\${encodedUrl}&cellSize=4&margin=0&errorCorrectionLevel=L&typeNumber=0\`;
      
      qrCodeImage.src = qrCodeUrl;
      qrCodeImage.alt = \`订阅链接二维码: \${subscriptionLink.substring(0, 50)}...\`;
      
      qrCodeContainer.classList.remove('hidden');
      
      qrCodeImage.onload = function() {
        showToast('二维码生成成功', 'success');
      };
      
      qrCodeImage.onerror = function() {
        showToast('二维码生成失败，请重试', 'error');
      };
    }
    
    function downloadBatchQRCode() {
      if (!currentSubscriptionLink) {
        showToast('没有可用的订阅链接', 'error');
        return;
      }
      
      const qrCodeImage = document.getElementById('batchQrCodeImage');
      const link = document.createElement('a');
      link.href = qrCodeImage.src;
      link.download = 'batch-subscription-qrcode.png';
      link.click();
      showToast('二维码已下载', 'success');
    }
    
    function shareBatchQRCode() {
      if (!currentSubscriptionLink) {
        showToast('没有可用的订阅链接', 'error');
        return;
      }
      
      const qrCodeImage = document.getElementById('batchQrCodeImage');
      
      if (navigator.share) {
        navigator.share({
          title: '代理订阅配置',
          text: '扫描二维码导入代理配置',
          url: currentSubscriptionLink,
        })
        .then(() => showToast('分享成功', 'success'))
        .catch(error => {
          console.error('分享失败:', error);
          navigator.clipboard.writeText(currentSubscriptionLink)
            .then(() => showToast('分享失败，已复制链接到剪贴板', 'info'));
        });
      } else {
        navigator.clipboard.writeText(currentSubscriptionLink)
          .then(() => showToast('链接已复制到剪贴板', 'success'));
      }
    }
    
    function refreshBatchQRCode() {
      if (!currentSubscriptionLink) {
        showToast('没有可用的订阅链接', 'error');
        return;
      }
      
      generateBatchQRCode(currentSubscriptionLink);
      showToast('二维码已刷新', 'info');
    }
    
    // ============================================
    // Config Management Tab Functions
    // ============================================
    
    async function manageQueryByUuid(){
      const b = document.getElementById('queryBtn'); 
      setButtonLoading(b, true);
      const u = document.getElementById('queryUuidInput').value.trim();
      if(!u){ 
        showToast('请输入 UUID','error'); 
        setButtonLoading(b, false, b.innerHTML); 
        return;
      }
      
      currentUuid = u;
      
      // Show sections
      document.getElementById('manageResultCard').classList.remove('hidden');
      document.getElementById('manageStatsCard').classList.remove('hidden');
      document.getElementById('manageAddCard').classList.remove('hidden');
      const l = \`\${WORKER_DOMAIN}/sub/\${u}\`; 
      const input = document.getElementById('manageSubUrlLink');
      input.value = l; 
      document.getElementById('manageSubLinkDisplay').style.display = 'flex';
      document.getElementById('manageSubLinkDisplay').classList.remove('hidden');
      
      try{
        const r = await fetch(\`/manage/configs/\${u}\`);
        const c = document.getElementById('queryResultsContainer');
        if(r.status === 404){ 
          c.innerHTML='<p style="padding:20px;text-align:center;color:#89949B">未找到配置，请直接添加。</p>'; 
        } else {
          const d = await r.json();
          let h = '<div class="table-container"><table><thead><tr><th>备注</th><th>协议</th><th>域名托管</th><th>配置</th><th>操作</th></tr></thead><tbody>';
          d.configs.forEach(Row=>{
            const sc = JSON.stringify(Row).replace(/"/g, '&quot;');
            const hosting = Row.domain_hosting || 'Cloudflare';
            const hostingClass = hostingStyleMap[hosting] || 'hosting-other';
            h+=\`<tr>
              <td>\${Row.remark||'-'}</td>
              <td>\${Row.protocol}</td>
              <td class="domain-hosting-cell"><span class="hosting-badge \${hostingClass}">\${hosting}</span></td>
              <td class="config-data-cell">\${Row.config_data.substring(0,40)}...</td>
              <td class="actions-cell">
                <button class="nav-btn" data-config="\${sc}" onclick="openEditModal(JSON.parse(this.dataset.config))">编辑</button>
                <button class="nav-btn" style="background:#d44;color:#fff;border-color:#d44" onclick="delOne(\${Row.id})">删除</button>
              </td>
            </tr>\`;
          });
          h+='</tbody></table></div>'; 
          c.innerHTML = h;
        }
        
        // Load statistics
        await loadUuidStats();
        
      } catch(e){ 
        showToast(e.message,'error'); 
      } finally{ 
        setButtonLoading(b, false, b.innerHTML); 
      }
    }
    
    async function manageAddConfig(){
      const u = document.getElementById('queryUuidInput').value; 
      const d = document.getElementById('addConfigData').value;
      const hosting = document.getElementById('domainHostingSelect').value;
      
      if(!u || !d) {
        showToast('UUID或配置为空','error');
        return;
      }
      
      try {
        const response = await fetch('/manage/configs', {
          method: 'POST', 
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({
            uuid: u,
            config_data: d,
            domain_hosting: hosting
          })
        });
        
        const result = await response.json();
        if (result.success) {
          showToast('添加成功','success'); 
          document.getElementById('addConfigData').value=''; 
          manageQueryByUuid();
        } else {
          showToast(result.error || '添加失败','error');
        }
      } catch (error) {
        showToast('网络错误: ' + error.message, 'error');
      }
    }
    
    async function delOne(id){ 
      if(confirm('确认删除?')) { 
        await fetch(\`/manage/configs/id/\${id}\`,{method:'DELETE'}); 
        manageQueryByUuid(); 
      } 
    }
    
    async function deleteAll(){ 
      const u = document.getElementById('queryUuidInput').value; 
      if(confirm('确认清空所有配置?')) { 
        await fetch(\`/manage/configs/\${u}\`,{method:'DELETE'}); 
        manageQueryByUuid(); 
      } 
    }
    
    function copyManageSubLink(){ 
      navigator.clipboard.writeText(document.getElementById('manageSubUrlLink').value)
        .then(()=>showToast('已复制','success'))
        .catch(()=>showToast('复制失败','error')); 
    }
    
    // Edit modal functions
    function openEditModal(config) {
      document.getElementById('edit-id').value = config.id;
      let p = 'unknown'; 
      const l = config.config_data;
      
      const protocolPatterns = [
        { prefix: 'vmess://', type: 'vmess' },
        { prefix: 'vless://', type: 'vless' },
        { prefix: 'trojan://', type: 'trojan' },
        { prefix: 'hysteria2://', type: 'hysteria2' },
        { prefix: 'tuic://', type: 'tuic' },
        { prefix: 'anytls://', type: 'anytls' },
        { prefix: 'socks5://', type: 'socks5' },
        { prefix: 'any-reality://', type: 'any-reality' },
        { prefix: 'anyreality://', type: 'anyreality' },
        { prefix: 'reality://', type: 'reality' },
        { prefix: 'ss://', type: 'ss' }
      ];
      
      for (const pattern of protocolPatterns) {
        if (l.toLowerCase().startsWith(pattern.prefix)) {
          p = pattern.type;
          break;
        }
      }
      
      document.getElementById('edit-protocol').value = p;
      ['ps','add','port','id-uuid','net','type','host','path','tls','sni'].forEach(k=>document.getElementById('edit-'+k).value='');
      
      const hostingSelect = document.getElementById('edit-domain-hosting');
      hostingSelect.value = config.domain_hosting || 'Cloudflare';
      
      try {
        if (p === 'vmess') {
          const c = JSON.parse(b64DecodeUnicode(l.substring(8)));
          document.getElementById('edit-ps').value = c.ps||''; 
          document.getElementById('edit-add').value = c.add||''; 
          document.getElementById('edit-port').value = c.port||''; 
          document.getElementById('edit-id-uuid').value = c.id||''; 
          document.getElementById('edit-net').value = c.net||'tcp'; 
          document.getElementById('edit-type').value = c.type||''; 
          document.getElementById('edit-host').value = c.host||''; 
          document.getElementById('edit-path').value = c.path||''; 
          document.getElementById('edit-tls').value = c.tls||''; 
          document.getElementById('edit-sni').value = c.sni||'';
        } else if (p === 'vless' || p === 'trojan' || 
                  p === 'hysteria2' || p === 'tuic' ||
                  p === 'anytls' || p === 'socks5' ||
                  p === 'any-reality' || p === 'anyreality' ||
                  p === 'reality' || p === 'ss') {
          const u = new URL(l);
          document.getElementById('edit-ps').value = u.hash?decodeURIComponent(u.hash.substring(1)):''; 
          document.getElementById('edit-add').value = u.hostname; 
          document.getElementById('edit-port').value = u.port; 
          document.getElementById('edit-id-uuid').value = u.username; 
          document.getElementById('edit-net').value = u.searchParams.get('type')||'tcp'; 
          document.getElementById('edit-type').value = u.searchParams.get('headerType')||''; 
          document.getElementById('edit-host').value = u.searchParams.get('host')||''; 
          document.getElementById('edit-path').value = u.searchParams.get('path')||u.searchParams.get('serviceName')||''; 
          document.getElementById('edit-tls').value = u.searchParams.get('security')==='tls'?'tls':''; 
          document.getElementById('edit-sni').value = u.searchParams.get('sni')||'';
        }
      } catch(e) { 
        alert('解析失败'); 
        return; 
      }
      document.getElementById('editModalOverlay').classList.add('open');
    }
    
    function closeEditModal() { 
      document.getElementById('editModalOverlay').classList.remove('open'); 
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
      const domainHosting = document.getElementById('edit-domain-hosting').value;
      
      let nL = '';
      if (proto === 'vmess') {
        nL = 'vmess://' + b64EncodeUnicode(JSON.stringify({
          v: "2", ps, add, port, id: uuid, aid: "0", scy: "auto",
          net, type, host, path, tls, sni
        }));
      } else if (proto === 'vless' || proto === 'trojan' || 
                proto === 'hysteria2' || proto === 'tuic' ||
                proto === 'anytls' || proto === 'socks5' ||
                proto === 'any-reality' || proto === 'anyreality' ||
                proto === 'reality' || proto === 'ss') {
        let u = new URL(proto + '://' + uuid + '@' + add + ':' + port);
        if (net !== 'tcp') u.searchParams.set('type', net);
        if (type) u.searchParams.set('headerType', type);
        if (tls === 'tls') {
          u.searchParams.set('security', 'tls');
          if (sni) u.searchParams.set('sni', sni);
        }
        if (host) u.searchParams.set('host', host);
        if (path) {
          if (net === 'grpc') u.searchParams.set('serviceName', path);
          else u.searchParams.set('path', path);
        }
        u.hash = encodeURIComponent(ps);
        nL = u.toString();
      }
      
      const b = document.querySelector('.modal .primary');
      const o = b.innerHTML;
      setButtonLoading(b, true);
      try {
        const r = await fetch('/manage/configs', {
          method: 'PUT',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({
            id, 
            config_data: nL,
            domain_hosting: domainHosting
          })
        });
        const result = await r.json();
        if (result.success) {
          showToast('保存成功', 'success');
          closeEditModal();
          manageQueryByUuid();
        }
      } catch (e) {
        showToast(e.message, 'error');
      } finally {
        setButtonLoading(b, false, o);
      }
    }
    
    // Statistics functions
    async function loadUuidStats() {
      if (!currentUuid) return;
      
      const days = document.getElementById('statsDays').value;
      try {
        const response = await fetch(\`/manage/stats/\${currentUuid}?days=\${days}\`);
        const data = await response.json();
        
        if (data.success) {
          displayStatsSummary(data);
          renderStatsChart(data.daily_stats);
          displayRecentLogs(data.recent_logs);
          updateChartButtons();
        } else {
          showToast(data.error || '无法加载统计数据', 'error');
          document.getElementById('manageStatsSummary').innerHTML = \`
            <div style="text-align: center; padding: 20px; color: #6b7280;">
              <p>\${data.error || '无统计信息'}</p>
              <p><small>请确保已升级到v2.0+版本并启用访问日志记录</small></p>
            </div>
          \`;
        }
      } catch (error) {
        showToast('获取统计失败: ' + error.message, 'error');
      }
    }
    
    function displayStatsSummary(stats) {
      const container = document.getElementById('manageStatsSummary');
      
      const subscriptionPercent = stats.total_access > 0 ? 
        Math.round(stats.subscription_count / stats.total_access * 100) : 0;
      const apigenPercent = stats.total_access > 0 ? 
        Math.round(stats.apigen_count / stats.total_access * 100) : 0;
      
      container.innerHTML = \`
        <div class="stat-box">
          <span class="stat-num">\${stats.total_access}</span>
          <span class="stat-label">总访问次数</span>
          <span class="stat-sub">首次: \${stats.first_access ? formatDate(stats.first_access) : '从未'}</span>
        </div>
        <div class="stat-box">
          <span class="stat-num">\${stats.today_total}</span>
          <span class="stat-label">今日访问</span>
          <span class="stat-sub">订阅:\${stats.today_subscription} | 网页:\${stats.today_apigen}</span>
        </div>
        <div class="stat-box">
          <span class="stat-num">\${stats.subscription_count}</span>
          <span class="stat-label">订阅访问</span>
          <span class="stat-sub">占比 \${subscriptionPercent}%</span>
        </div>
        <div class="stat-box">
          <span class="stat-num">\${stats.apigen_count}</span>
          <span class="stat-label">网页生成</span>
          <span class="stat-sub">占比 \${apigenPercent}%</span>
        </div>
      \`;
    }
    
    function renderStatsChart(dailyStats) {
      const ctx = document.getElementById('manageStatsChart').getContext('2d');
      
      const dates = dailyStats.map(item => formatDate(item.date));
      const totals = dailyStats.map(item => item.total);
      const subscriptions = dailyStats.map(item => item.subscription);
      const apigens = dailyStats.map(item => item.api_generation);
      
      if (statsChart) {
        statsChart.destroy();
      }
      
      let datasets = [];
      
      switch (currentChartType) {
        case 'total':
          datasets = [{
            label: '总访问量',
            data: totals,
            borderColor: '#3b82f6',
            backgroundColor: 'rgba(59, 130, 246, 0.1)',
            borderWidth: 2,
            fill: true,
            tension: 0.4
          }];
          break;
          
        case 'split':
          datasets = [
            {
              label: '订阅访问',
              data: subscriptions,
              borderColor: '#10b981',
              backgroundColor: 'rgba(16, 185, 129, 0.1)',
              borderWidth: 2,
              fill: true,
              tension: 0.4
            },
            {
              label: '网页生成',
              data: apigens,
              borderColor: '#f59e0b',
              backgroundColor: 'rgba(245, 158, 11, 0.1)',
              borderWidth: 2,
              fill: true,
              tension: 0.4
            }
          ];
          break;
      }
      
      statsChart = new Chart(ctx, {
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
              text: \`UUID: \${currentUuid.substring(0, 8)}... 访问趋势\`
            },
            tooltip: {
              mode: 'index',
              intersect: false
            }
          },
          scales: {
            x: {
              grid: {
                display: false
              },
              title: {
                display: true,
                text: '日期'
              }
            },
            y: {
              beginAtZero: true,
              ticks: {
                precision: 0
              },
              title: {
                display: true,
                text: '访问次数'
              }
            }
          },
          interaction: {
            intersect: false,
            mode: 'index'
          }
        }
      });
    }
    
    function displayRecentLogs(logs) {
      const container = document.getElementById('manageRecentLogs');
      
      if (!logs || logs.length === 0) {
        container.innerHTML = '<div style="text-align: center; padding: 20px; color: #6b7280;">暂无访问记录</div>';
        return;
      }
      
      let html = '';
      logs.forEach(log => {
        const typeClass = log.query_type === 'subscription' ? 'type-subscription' : 'type-apigen';
        const typeText = log.query_type === 'subscription' ? '订阅' : '网页';
        const userAgentShort = log.user_agent ? 
          (log.user_agent.length > 30 ? log.user_agent.substring(0, 30) + '...' : log.user_agent) : 
          '未知';
        
        html += \`
          <div class="access-log-item">
            <div>
              <span class="timestamp">\${formatDateTime(log.created_at)}</span>
              <span class="log-type \${typeClass}">\${typeText}</span>
            </div>
            <div style="color: #6b7280; font-size: 0.8rem;" title="\${log.user_agent || '未知'}">
              \${userAgentShort}
            </div>
          </div>
        \`;
      });
      
      container.innerHTML = html;
    }
    
    function switchChartType(type) {
      currentChartType = type;
      updateChartButtons();
      loadUuidStats();
    }
    
    function updateChartButtons() {
      document.getElementById('chartTotalBtn').classList.toggle('active', currentChartType === 'total');
      document.getElementById('chartSplitBtn').classList.toggle('active', currentChartType === 'split');
    }
    
    // ============================================
    // Clash Subscription Tab Functions
    // ============================================
    
    function toggleClashAddressOptions() {
      const type = document.querySelector('input[name="clashAddressType"]:checked').value;
      const ipContainer = document.getElementById('clashIpOptionsContainer');
      const domainBox = document.getElementById('clashDomainInfoBox');
      if (type === 'domain') {
        ipContainer.classList.add('hidden');
        domainBox.classList.remove('hidden');
      } else {
        ipContainer.classList.remove('hidden');
        domainBox.classList.add('hidden');
      }
    }
    
    function handleTemplateChange() {
      const templateSelect = document.getElementById('templateSelect');
      const selectedValue = templateSelect.value;
      const customTemplateInput = document.getElementById('customTemplateInput');
      const templateDescription = document.getElementById('templateDescription');
      const templateInfoBox = document.getElementById('templateInfoBox');
      
      if (selectedValue === 'custom') {
        customTemplateInput.classList.remove('hidden');
        templateDescription.textContent = '使用自定义模板URL，请输入可公开访问的YAML配置文件URL。';
        templateInfoBox.classList.add('hidden');
      } else if (selectedValue === '') {
        customTemplateInput.classList.add('hidden');
        templateDescription.textContent = '请从下拉列表中选择一个配置文件模板';
      } else {
        customTemplateInput.classList.add('hidden');
        const template = templatesList.find(t => t.name === selectedValue);
        if (template && template.description) {
          templateDescription.textContent = template.description;
        } else {
          templateDescription.textContent = '数据库模板配置。';
        }
      }
    }
    
    function getTemplateParam() {
      const templateSelect = document.getElementById('templateSelect');
      const selectedValue = templateSelect.value;
      
      if (selectedValue === 'custom') {
        const customUrl = document.getElementById('customTemplateUrl').value.trim();
        if (!customUrl) {
          showToast('请输入自定义模板URL', 'error');
          return null;
        }
        if (!customUrl.startsWith('http://') && !customUrl.startsWith('https://')) {
          showToast('请输入有效的URL地址（以http://或https://开头）', 'error');
          return null;
        }
        return customUrl;
      } else if (!selectedValue) {
        showToast('请选择一个模板', 'error');
        return null;
      } else {
        return selectedValue;
      }
    }
    
    async function loadTemplates() {
      const templateSelect = document.getElementById('templateSelect');
      const templateLoading = document.getElementById('templateLoading');
      const templateError = document.getElementById('templateError');
      const generateClashBtn = document.getElementById('generateClashBtn');
      
      templateSelect.disabled = true;
      templateLoading.classList.remove('hidden');
      templateError.classList.add('hidden');
      generateClashBtn.disabled = true;
      
      try {
        const response = await fetch(\`\${WORKER_DOMAIN}/api/templates\`);
        
        if (!response.ok) {
          throw new Error(\`加载失败: \${response.status}\`);
        }
        
        const templates = await response.json();
        templatesList = templates;
        
        templateSelect.innerHTML = '';
        
        const customOption = document.createElement('option');
        customOption.value = 'custom';
        customOption.textContent = '自定义模板';
        templateSelect.appendChild(customOption);
        
        if (templates.length === 0) {
          const noTemplateOption = document.createElement('option');
          noTemplateOption.value = '';
          noTemplateOption.textContent = '数据库中没有可用模板';
          templateSelect.appendChild(noTemplateOption);
          showToast('数据库中没有配置模板', 'error');
        } else {
          const defaultTemplate = templates.find(t => t.is_default === 1);
          
          templates.forEach(template => {
            const option = document.createElement('option');
            option.value = template.name;
            option.textContent = template.name;
            if (defaultTemplate && template.name === defaultTemplate.name) {
              option.selected = true;
            }
            templateSelect.appendChild(option);
          });
          
          if (!defaultTemplate && templates.length > 0) {
            templateSelect.value = templates[0].name;
          }
          
          const selectedValue = templateSelect.value;
          const selectedTemplate = templates.find(t => t.name === selectedValue);
          const templateDescription = document.getElementById('templateDescription');
          if (selectedTemplate && selectedTemplate.description) {
            templateDescription.textContent = selectedTemplate.description;
          }
          
          generateClashBtn.disabled = false;
        }
        
        showToast('模板列表加载成功', 'success');
      } catch (error) {
        console.error('加载模板失败:', error);
        templateError.classList.remove('hidden');
        showToast(\`加载模板失败: \${error.message}\`, 'error');
        
        templateSelect.innerHTML = '';
        const errorOption = document.createElement('option');
        errorOption.value = '';
        errorOption.textContent = '加载模板失败';
        templateSelect.appendChild(errorOption);
      } finally {
        templateLoading.classList.add('hidden');
        templateSelect.disabled = false;
        handleTemplateChange();
      }
    }
    
    async function generateClashConfig() {
      const btn = document.getElementById('generateClashBtn');
      const uuid = document.getElementById('clashConfigUuid').value.trim();
      const addressType = document.querySelector('input[name="clashAddressType"]:checked').value;
      const ipType = document.querySelector('input[name="clashIpType"]:checked').value;
      const carrier = document.querySelector('input[name="clashCarrier"]:checked').value;
      
      const templateParam = getTemplateParam();
      if (templateParam === null) return;
      
      const clashLinkBox = document.getElementById('clashSubscriptionLinkBox');
      const clashLinkInput = document.getElementById('clashGeneratedLink');
      const qrCodeContainer = document.getElementById('clashQrCodeContainer');
      clashLinkBox.classList.add('hidden');
      qrCodeContainer.classList.add('hidden');
      clashLinkInput.value = '';
      currentSubscriptionLink = '';
      
      if (!uuid) {
        showToast('请输入UUID', 'error');
        return;
      }
      
      setButtonLoading(btn, true, '正在生成...');
      
      try {
        const subscriptionUrl = new URL(\`\${WORKER_DOMAIN}/clash-config\`);
        subscriptionUrl.searchParams.set('uuid', uuid);
        subscriptionUrl.searchParams.set('type', addressType);
        subscriptionUrl.searchParams.set('template', templateParam);
        
        if (addressType === 'ip') {
          subscriptionUrl.searchParams.set('ipType', ipType);
          subscriptionUrl.searchParams.set('carrier', carrier);
        }
        
        const clashSubscriptionLink = subscriptionUrl.toString();
        
        const response = await fetch(subscriptionUrl);
        
        if (!response.ok) {
          const errorData = await response.json().catch(() => ({}));
          throw new Error(errorData.error || \`生成失败: \${response.status}\`);
        }
        
        const blob = await response.blob();
        const downloadUrl = URL.createObjectURL(blob);
        
        const a = document.createElement('a');
        a.href = downloadUrl;
        a.download = \`clash-config-\${uuid}-\${new Date().toISOString().split('T')[0]}.yaml\`;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(downloadUrl);
        
        showToast('Clash订阅文件生成成功，已开始下载', 'success');
        
        clashLinkInput.value = clashSubscriptionLink;
        clashLinkBox.classList.remove('hidden');
        
        generateClashQRCode(clashSubscriptionLink);
        
      } catch (error) {
        console.error('生成Clash配置失败:', error);
        showToast(\`生成失败: \${error.message}\`, 'error');
      } finally {
        setButtonLoading(btn, false, '生成Clash订阅文件');
      }
    }
    
    function copyClashGeneratedLink() {
      const link = document.getElementById('clashGeneratedLink').value;
      if(!link) return;
      navigator.clipboard.writeText(link).then(() => showToast('订阅链接已复制', 'success'));
    }
    
    // Clash QR Code functions
    function generateClashQRCode(subscriptionLink) {
      if (!subscriptionLink) return;
      
      currentSubscriptionLink = subscriptionLink;
      const qrCodeContainer = document.getElementById('clashQrCodeContainer');
      const qrCodeImage = document.getElementById('clashQrCodeImage');
      
      const encodedUrl = encodeURIComponent(subscriptionLink);
      const qrCodeUrl = \`\${QR_CODE_API}?text=\${encodedUrl}&cellSize=4&margin=0&errorCorrectionLevel=L&typeNumber=0\`;
      
      qrCodeImage.src = qrCodeUrl;
      qrCodeImage.alt = \`订阅链接二维码: \${subscriptionLink.substring(0, 50)}...\`;
      
      qrCodeContainer.classList.remove('hidden');
      
      qrCodeImage.onload = function() { 
        showToast('二维码生成成功', 'success'); 
      };
      qrCodeImage.onerror = function() { 
        showToast('二维码生成失败，请重试', 'error'); 
      };
    }
    
    function downloadClashQRCode() {
      if (!currentSubscriptionLink) { 
        showToast('没有可用的订阅链接', 'error'); 
        return; 
      }
      const qrCodeImage = document.getElementById('clashQrCodeImage');
      const link = document.createElement('a');
      link.href = qrCodeImage.src;
      link.download = 'clash-subscription-qrcode.png';
      link.click();
      showToast('二维码已下载', 'success');
    }
    
    function shareClashQRCode() {
      if (!currentSubscriptionLink) { 
        showToast('没有可用的订阅链接', 'error'); 
        return; 
      }
      if (navigator.share) {
        navigator.share({
          title: 'Clash代理配置',
          text: '扫描二维码导入Clash配置文件',
          url: currentSubscriptionLink,
        })
        .then(() => showToast('分享成功', 'success'))
        .catch(error => {
          console.error('分享失败:', error);
          navigator.clipboard.writeText(currentSubscriptionLink)
            .then(() => showToast('分享失败，已复制链接到剪贴板', 'info'));
        });
      } else {
        navigator.clipboard.writeText(currentSubscriptionLink)
          .then(() => showToast('链接已复制到剪贴板', 'success'));
      }
    }
    
    function refreshClashQRCode() {
      if (!currentSubscriptionLink) { 
        showToast('没有可用的订阅链接', 'error'); 
        return; 
      }
      generateClashQRCode(currentSubscriptionLink);
      showToast('二维码已刷新', 'info');
    }
    
    // Initialize
    document.addEventListener('DOMContentLoaded', function() {
      // Initialize domain hosting select
      const hostingSelect = document.getElementById('domainHostingSelect');
      hostingSelect.value = 'Cloudflare';
      
      // Initialize tab functionality
      toggleBatchAddressOptions();
      
      // Set up radio button listeners for batch tab
      const batchSourceRadios = document.querySelectorAll('input[name="genConfigSource"]');
      batchSourceRadios.forEach(r => r.addEventListener('change', (e) => {
        if(e.target.value === 'uuid') {
          document.getElementById('genManualConfigInput').classList.add('hidden');
          document.getElementById('genUuidConfigInput').classList.remove('hidden');
        } else {
          document.getElementById('genManualConfigInput').classList.remove('hidden');
          document.getElementById('genUuidConfigInput').classList.add('hidden');
        }
      }));
    });
  </script>
</body>
</html>
`;


