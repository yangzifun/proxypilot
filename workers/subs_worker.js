/* =================================================================
 * Cloudflare Worker: All-in-One Proxy Tool (Strict Read-Only Version)
 * =================================================================
 * [SECURITY NOTE]: 
 * This version allows NO IP updates (Auto or Manual).
 * It ONLY reads existing data from the D1 Database.
 * ================================================================= */

// =================================================================
//  GLOBAL UTILITIES
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
        },
    });
}

function getProtocol(configStr) {
    if (!configStr || typeof configStr !== 'string') return 'unknown';
    if (configStr.startsWith('vmess://')) return 'vmess';
    if (configStr.startsWith('vless://')) return 'vless';
    if (configStr.startsWith('trojan://')) return 'trojan';
    // Clash/Mihomo 配置文件直接提供，不需要协议前缀，但在某些地方需要判断
    if (configStr.includes('type: ss')) return 'ss';
    // Add other protocols if needed, e.g., hysteria2, tuic etc.
    return 'unknown';
}

// =================================================================
//  SECTION 0: Access Logging
// =================================================================

/**
 * 记录配置访问日志
 * @param {Object} env - Worker环境变量
 * @param {string} uuid - 用户UUID
 * @param {string} queryType - 查询类型: 'subscription'(订阅) 或 'api-generation'(网页生成) 或 'clash-config'
 * @param {string} clientIp - 客户端IP地址
 * @param {string} userAgent - 客户端User-Agent
 */
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
        // 不中断主流程，仅记录错误
    }
}

/**
 * 获取客户端IP地址 (支持Cloudflare Worker)
 * @param {Request} request - HTTP请求对象
 * @returns {string} 客户端IP地址
 */
function getClientIp(request) {
    // Cloudflare Workers可以通过cf-connecting-ip头获取真实IP
    const cfIp = request.headers.get('cf-connecting-ip');
    if (cfIp) return cfIp;
    
    // 回退到X-Forwarded-For
    const forwardedFor = request.headers.get('x-forwarded-for');
    if (forwardedFor) {
        // 取第一个IP（原始客户端IP）
        return forwardedFor.split(',')[0].trim();
    }
    
    // 最终回退方案
    return 'unknown';
}

/**
 * 获取访问统计
 * @param {Object} env - Worker环境变量
 * @param {string} uuid - UUID（可选，不提供则查询所有）
 * @param {Object} options - 查询选项
 * @returns {Promise<Object>} 统计结果
 */
async function getAccessStats(env, uuid = null, options = {}) {
    try {
        const db = env.DB;
        if (!db) throw new Error("D1数据库未绑定。");
        
        let query = 'SELECT uuid, query_type, COUNT(*) as count, DATE(created_at) as date FROM config_access_logs WHERE 1=1';
        const params = [];
        
        if (uuid) {
            query += ' AND uuid = ?';
            params.push(uuid);
        }
        
        // 时间范围过滤
        if (options.startDate) {
            query += ' AND created_at >= ?';
            params.push(options.startDate);
        }
        
        if (options.endDate) {
            query += ' AND created_at <= ?';
            params.push(options.endDate);
        }
        
        query += ' GROUP BY uuid, query_type, DATE(created_at) ORDER BY date DESC, count DESC';
        
        const { results } = await db.prepare(query).bind(...params).all();
        return { success: true, data: results };
    } catch (e) {
        console.error("获取访问统计失败:", e.message);
        return { success: false, error: e.message };
    }
}

// =================================================================
//  SECTION 1: Data Fetching (STRICTLY READ ONLY)
// =================================================================

// 获取 IP (仅读取 SELECT) - 已增加 DISTINCT 去重
async function fetchIpsFromDB(ipType, carrierType, env) {
    const db = env.DB;
    if (!db) throw new Error("D1 数据库未绑定。");

    let query = 'SELECT DISTINCT ip FROM cfips WHERE 1=1'; // 增加 DISTINCT
    const params = [];

    if (ipType.toLowerCase() !== 'all') {
        query += ' AND ip_type = ?';
        params.push(ipType.toLowerCase());
    }
    if (carrierType.toLowerCase() !== 'all') {
        query += ' AND carrier = ?';
        params.push(carrierType.toUpperCase());
    }
    query += ' ORDER BY ip'; // 增加排序以确保一致性

    const { results } = await db.prepare(query).bind(...params).all();
    return results;
}

// 获取域名 (仅读取 SELECT) - 已增加 DISTINCT 去重
async function fetchDomainsFromDB(env) {
    const db = env.DB;
    if (!db) throw new Error("D1 数据库未绑定。");
    try {
        const { results } = await db.prepare('SELECT DISTINCT domain FROM cf_domains ORDER BY domain').all(); // 增加 DISTINCT
        return results;
    } catch (e) {
        console.error("Fetching domains failed:", e.message);
        return [];
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
            // IP 逻辑
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

// =================================================================
//  SECTION 2: Config Generation Logic
// =================================================================

// 读取基础配置 (SELECT) - 修改：增加域名托管字段查询
async function fetchConfigsByUuidFromDB(uuid, env) {
    const db = env.DB;
    if (!db) return [];
    try {
        const stmt = db.prepare('SELECT id, config_data, protocol, remark, domain_hosting FROM configs WHERE uuid = ? ORDER BY id ASC');
        const { results } = await stmt.bind(uuid).all();
        return results;  // 返回完整对象，包含domain_hosting字段
    } catch (e) {
        console.error(`Error fetching configs for UUID ${uuid}:`, e.message);
        return [];
    }
}

// 核心替换逻辑 - 修改：确保所有原配置都被输出，但只有Cloudflare配置进行优选替换
function replaceAddressesInConfigs(baseConfigsData, addressList) {
    let generatedConfigs = [];
    const addressExtractionRegex = /@(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|\[[0-9a-fA-F:\.]+\]|(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,})(?::\d+)?(?:[\/?#]|$)/;
    const validAddressRegex = /^(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|\[[0-9a-fA-F:\.]+\]|(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,})$/;

    for (const configObj of baseConfigsData) {
        const baseConfig = configObj.config_data;
        const domainHosting = configObj.domain_hosting || 'Cloudflare'; // 默认为Cloudflare
        const configId = configObj.id || 0;
        const remark = configObj.remark || '';
        
        let configType = getProtocol(baseConfig);
        let processedBaseConfig;
        const pushError = (msg) => generatedConfigs.push(`[错误] ${msg}`);

        // 首先，无论域名属性是什么，先输出原配置
        generatedConfigs.push(baseConfig);
        
        // 检查域名托管属性，只有Cloudflare的配置才进行优选替换
        if (domainHosting !== 'Cloudflare') {
            // 非Cloudflare配置只输出原配置，不进行优选替换
            console.log(`配置ID ${configId} 的域名托管属性为"${domainHosting}"，只输出原配置，不进行优选替换`);
            continue;  // 跳过优选替换循环
        }
        
        // 只处理 vmess, vless, trojan 协议
        if (configType === 'trojan' || configType === 'vless') {
            const addressMatch = baseConfig.match(addressExtractionRegex);
            if (!addressMatch) {
                pushError(`配置中未找到地址，已跳过。`);
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

            if (configType === 'trojan' || configType === 'vless') {
                try {
                    const url = new URL(processedBaseConfig);
                    const originalName = url.hash ? decodeURIComponent(url.hash.substring(1)) : `${configType}-node`;
                    const newName = `${originalName}-${cleanAddrForName}`;
                    url.hash = encodeURIComponent(newName);
                    url.hostname = formattedAddr; 
                    generatedConfigs.push(url.toString());
                } catch (e) {
                    pushError(`处理 ${configType} 出错: ${e.message}`);
                }
            } else if (configType === 'vmess') {
                const tempVmessObj = JSON.parse(JSON.stringify(processedBaseConfig));
                const originalName = tempVmessObj.ps || tempVmessObj.remark || 'vmess-node';
                tempVmessObj.ps = `${originalName}-${cleanAddrForName}`;
                
                // 处理IPv6地址格式
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
    return generatedConfigs;
}

// 手动 or UUID 生成 (POST) - 已增加生成订阅节点配置去重
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
            
            // 统计域名托管类型
            const hostingStats = {};
            configsFromDb.forEach(cfg => {
                const hosting = cfg.domain_hosting || 'Cloudflare';
                hostingStats[hosting] = (hostingStats[hosting] || 0) + 1;
            });
            console.log(`UUID ${baseConfigUuid} 域名托管统计:`, hostingStats);
            
        } else if (baseConfigFromRequest) {
            // 手动粘贴的配置没有domain_hosting信息，全部按照Cloudflare处理（进行优选替换）
            const configLines = baseConfigFromRequest.split('\n').map(s => s.trim()).filter(Boolean);
            if (configLines.length === 0) return jsonResponse({ error: "基础配置不能为空。" }, 400);
            
            baseConfigsData = configLines.map((config_data, index) => ({
                id: index,
                config_data,
                protocol: getProtocol(config_data),
                remark: null,
                domain_hosting: 'Cloudflare' // 手动粘贴的配置默认为Cloudflare，进行优选替换
            }));
        } else {
            return jsonResponse({ error: "必须提供基础配置或UUID。" }, 400);
        }

        if (!addressListText || addressListText.trim() === '') {
            return jsonResponse({ error: "优选列表不能为空。" }, 400);
        }

        const addressList = addressListText.split('\n').map(a => a.trim()).filter(a => a.length > 0);
        const generatedConfigs = replaceAddressesInConfigs(baseConfigsData, addressList);
        
        // --- 增加生成订阅节点配置去重 ---
        const uniqueGeneratedConfigs = [...new Set(generatedConfigs)];
        // --- 去重结束 ---

        // 统计生成结果
        const totalConfigs = baseConfigsData.length;
        const cloudflareConfigs = baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length;
        const otherHostingCount = totalConfigs - cloudflareConfigs;
        
        // 计算成功条数（排除错误消息）
        const successCount = uniqueGeneratedConfigs.filter(c => !c.startsWith('[错误]')).length;
        const errorCount = uniqueGeneratedConfigs.filter(c => c.startsWith('[错误]')).length; // 错误信息也应该被去重

        // 计算预期输出条数（仅为参考，实际输出已去重）
        const expectedNonCloudflare = otherHostingCount; 
        const expectedCloudflare = cloudflareConfigs * (addressList.length + 1); 
        const expectedTotal = expectedNonCloudflare + expectedCloudflare;
        
        let message = `生成完成！成功 ${successCount} 条 (去重后)，预期生成 ${expectedTotal} 条`;
        if (errorCount > 0) message += `，失败 ${errorCount} 条`;
        
        // 添加域名托管过滤信息
        if (otherHostingCount > 0) {
            message += ` (注：其中有 ${otherHostingCount} 个非Cloudflare配置只输出原配置，${cloudflareConfigs} 个Cloudflare配置进行了优选替换)`;
        }
        
        // 如果使用UUID生成，记录访问日志
        if (baseConfigUuid) {
            const clientIp = getClientIp(request);
            const userAgent = request.headers.get('user-agent') || 'unknown';
            await logConfigAccess(env, baseConfigUuid, 'api-generation', clientIp, userAgent);
        }
        
        return jsonResponse({ 
            configs: uniqueGeneratedConfigs, // 返回去重后的配置
            message: message,
            stats: {
                total_configs_in_db: totalConfigs,
                cloudflare_configs_in_db: cloudflareConfigs,
                other_hosting_configs_in_db: otherHostingCount,
                address_list_count: addressList.length,
                expected_output_before_dedupe: expectedTotal,
                actual_unique_output_success: successCount,
                actual_unique_output_errors: errorCount
            }
        });
    } catch (e) {
        console.error("生成配置出错:", e.message);
        return jsonResponse({ error: "内部错误: " + e.message }, 500);
    }
}

// 订阅链接生成配置 (GET) - 已增加生成订阅节点配置去重
async function handleGetBatchConfigs(uuid, urlParams, env, request) {
    if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);

    const baseConfigsData = await fetchConfigsByUuidFromDB(uuid, env);
    if (baseConfigsData.length === 0) return jsonResponse({ error: `UUID Not Found` }, 404);
    
    // 统计数据
    const totalConfigs = baseConfigsData.length;
    const cloudflareConfigs = baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length;
    const otherConfigs = totalConfigs - cloudflareConfigs;
    
    console.log(`订阅UUID ${uuid}: 总配置数${totalConfigs}, Cloudflare配置${cloudflareConfigs}, 其他域名托管配置${otherConfigs}`);

    const type = urlParams.get('type') || 'ip';
    let addressList = [];

    try {
        if (type === 'domain') {
            const domains = await fetchDomainsFromDB(env); // 已经去重
            addressList = domains.map(d => d.domain);
        } else {
            // IP
            const ipType = urlParams.get('ipType') || 'all';
            const carrier = urlParams.get('carrier') || 'all';
            const ips = await fetchIpsFromDB(ipType, carrier, env); // 已经去重
            addressList = ips.map(i => i.ip);
        }
    } catch (e) {
        return jsonResponse({ error: "DB Error: " + e.message }, 500);
    }

    if (addressList.length === 0) {
        return jsonResponse({ error: `No valid addresses found for type=${type}` }, 404);
    }

    const generatedConfigs = replaceAddressesInConfigs(baseConfigsData, addressList);
    
    // --- 增加生成订阅节点配置去重 ---
    const uniqueGeneratedConfigs = [...new Set(generatedConfigs)];
    // --- 去重结束 ---

    const filteredConfigs = uniqueGeneratedConfigs.filter(c => !c.startsWith('[错误]'));

    if (filteredConfigs.length === 0) {
        return new Response("Generation failed. No valid unique configs after deduplication.", { status: 400 });
    }

    const body = btoa(filteredConfigs.join('\n'));

    // 记录订阅访问日志
    const clientIp = getClientIp(request);
    const userAgent = request.headers.get('user-agent') || 'unknown';
    await logConfigAccess(env, uuid, 'subscription', clientIp, userAgent);

    return new Response(body, {
        status: 200,
        headers: {
            'Content-Type': 'text/plain;charset=UTF-8',
            'Subscription-User-Info': 'upload=0; download=0; total=10737418240000; expire=2524608000',
            'Profile-Update-Interval': '24',
            // 添加域名托管过滤信息头
            'X-Domain-Hosting-Filter': `Cloudflare=${cloudflareConfigs}, Other=${otherConfigs}`,
            'X-Total-Proxies-Unique': filteredConfigs.length.toString()
        },
    });
}

// =================================================================
//  SECTION 2.5: Clash/Mihomo Config Generation
// =================================================================

// 获取Clash配置模板
async function fetchClashConfigTemplate() {
    try {
        const response = await fetch('https://raw.githubusercontent.com/yangzifun/rule/refs/heads/main/config.yaml');
        if (!response.ok) {
            console.warn(`无法从GitHub获取模板，尝试使用本地默认模板: ${response.status} ${response.statusText}`);
            // Fallback to a hardcoded minimal template if GitHub is unreachable
            return `
# Clash/Mihomo 配置文件 (默认模板 - GitHub模板获取失败)
# 生成时间: ${new Date().toISOString()}

# 节点信息
proxies:
  - {name: 直连, type: direct}

# 全局配置 
port: 7890
socks-port: 7891
redir-port: 7892
mixed-port: 7893
tproxy-port: 7894
allow-lan: true
bind-address: "*"
ipv6: false
unified-delay: true
tcp-concurrent: true
log-level: warning
find-process-mode: 'off'
global-client-fingerprint: chrome
keep-alive-idle: 600
keep-alive-interval: 15
disable-keep-alive: false
profile:
  store-selected: true
  store-fake-ip: true

# DNS模块
dns:
  enable: true
  listen: 0.0.0.0:1053
  ipv6: false
  respect-rules: true
  fake-ip-range: 28.0.0.1/8
  fake-ip-filter-mode: blacklist
  fake-ip-filter:
    - "rule-set:private_domain,cn_domain"
    - "+.msftconnecttest.com"
    - "+.msftncsi.com"
    - "time.*.com"
    - "+.market.xiaomi.com"
  default-nameserver:
   - 223.5.5.5
  proxy-server-nameserver:
    - https://doh.yangzifun.org/dns-query
    - https://doh.pub/dns-query
  nameserver:
    - 8.8.8.8
    - 1.1.1.1
 

# 锚点配置
rule-anchor:
  ip: &ip {type: http, interval: 86400, behavior: ipcidr, format: mrs}
  domain: &domain {type: http, interval: 86400, behavior: domain, format: mrs}
  class: &class {type: http, interval: 86400, behavior: classical, format: text}

# 出站策略 (示例，可能需要根据你的实际模板进行调整)
proxy-groups:
  - {name: 🚀 默认代理, type: select, include-all: true, filter: "^((?!(直连)).)*$", proxies: [⚡ 自动选择, 🌐 全部节点]}
  - {name: ⚡ 自动选择, type: url-test, include-all: true, tolerance: 20, interval: 300, filter: "^((?!(直连)).)*$"}
  - {name: 🌐 全部节点, type: select, include-all: true}
  - {name: 🎯 直连, type: select, proxies: [DIRECT, 🚀 默认代理]}

# 规则匹配
rules:
  - MATCH,🚀 默认代理
`;
        }
        return await response.text();
    } catch (error) {
        console.error('获取Clash模板失败:', error);
        throw new Error('无法获取Clash配置文件模板。');
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
                name: vmessObj.ps || vmessObj.remark || `VMess-${vmessObj.add}-${vmessObj.port}`, // 更具体的默认名称
                type: 'vmess',
                server: vmessObj.add,
                port: parseInt(vmessObj.port) || 443,
                uuid: vmessObj.id,
                alterId: vmessObj.aid || 0,
                cipher: vmessObj.scy || 'auto',
                tls: vmessObj.tls === 'tls',
                'skip-cert-verify': true // Clash属性名
            };
            
            // 处理网络类型
            if (vmessObj.net === 'ws') {
                clashConfig.network = 'ws';
                clashConfig['ws-opts'] = { // Clash属性名
                    path: vmessObj.path || '/',
                    headers: vmessObj.host ? { Host: vmessObj.host } : {}
                };
            } else if (vmessObj.net === 'h2') {
                clashConfig.network = 'h2';
                clashConfig['h2-opts'] = { // Mihomo/Clash Premium
                    path: vmessObj.path || '/',
                    host: vmessObj.host ? [vmessObj.host] : []
                };
            } else if (vmessObj.net === 'grpc') {
                clashConfig.network = 'grpc';
                clashConfig['grpc-opts'] = { // Mihomo/Clash Premium
                    'grpc-service-name': vmessObj.path || '' // Clash属性名
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
                'skip-cert-verify': true // Clash属性名
            };
            
            // 处理网络类型
            const typeParam = url.searchParams.get('type') || ''; // 'ws' for websocket
            if (typeParam === 'ws') {
                clashConfig.network = 'ws';
                const path = url.searchParams.get('path') || '/';
                const host = url.searchParams.get('host');
                
                clashConfig['ws-opts'] = {
                    path: path,
                    headers: host ? { Host: host } : {}
                };
            }
            
            return clashConfig;
        } catch (error) {
            console.error('转换Trojan配置失败:', error, configStr);
            return null;
        }
    }
    
    // 处理VLESS配置（Mihomo支持）
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
                'skip-cert-verify': true // Clash属性名
            };
            
            // 处理流设置
            const typeParam = url.searchParams.get('type') || ''; // 'ws' for websocket, 'grpc'
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
            // mihomo might support xtls, reality etc. Need to parse more params if desired.
            
            return clashConfig;
        } catch (error) {
            console.error('转换VLESS配置失败:', error, configStr);
            return null;
        }
    }
    
    // 处理Shadowsocks配置
    // 注意：ss:// 链接通常包含加密方式和密码，这里做简化处理。
    // 更复杂的SS链接可能需要解析插件 (plugin) 信息。
    if (configStr.startsWith('ss://')) {
        try {
            // Regex to capture base64-encoded userinfo and hostname:port
            const ssRegex = /ss:\/\/((?:[a-zA-Z0-9+\/]{4})*(?:[a-zA-Z0-9+\/]{2}==|[a-zA-Z0-9+\/]{3}=)?)@([^:/]+):(\d+)(?:#(.+))?/;
            const match = configStr.match(ssRegex);

            if (match) {
                const [_, userinfoBase64, server, port, nameEncoded] = match;
                const userinfo = b64_to_utf8(userinfoBase64); // "method:password"
                const [cipher, password] = userinfo.split(':', 2);
                const name = nameEncoded ? decodeURIComponent(nameEncoded) : `SS-${server}-${port}`;

                return {
                    name: name,
                    type: 'ss',
                    server: server,
                    port: parseInt(port),
                    cipher: cipher || 'aes-256-gcm', // Default to a common one if not specified in userinfo
                    password: password
                };
            } else {
                 console.warn('SS链接格式不符合预期:', configStr);
            }
        } catch (error) {
            console.error('转换SS配置失败:', error, configStr);
        }
    }
    
    return null;
}

// 生成完整的Clash配置文件 - 已增加 Clash 节点配置去重
async function generateClashConfig(request, env) {
    try {
        const url = new URL(request.url);
        const uuid = url.searchParams.get('uuid');
        const addressType = url.searchParams.get('type') || 'ip';
        const ipType = url.searchParams.get('ipType') || 'all';
        const carrier = url.searchParams.get('carrier') || 'all';
        
        // 验证必要参数
        if (!uuid) {
            return jsonResponse({ error: '缺少必要参数: uuid' }, 400);
        }

        // 获取基础配置
        const baseConfigsData = await fetchConfigsByUuidFromDB(uuid, env);
        if (baseConfigsData.length === 0) {
            return jsonResponse({ error: `未找到 UUID 为 "${uuid}" 的配置` }, 404);
        }

        // 获取优选地址列表
        let addressList = [];
        if (addressType === 'domain') {
            const domains = await fetchDomainsFromDB(env); // 已经去重
            addressList = domains.map(d => d.domain);
        } else {
            const ips = await fetchIpsFromDB(ipType, carrier, env); // 已经去重
            addressList = ips.map(i => i.ip);
        }

        if (addressList.length === 0) {
            return jsonResponse({ error: `未找到优选地址列表` }, 404);
        }

        // 生成节点配置 (原始链接字符串形式)
        const generatedRawConfigs = replaceAddressesInConfigs(baseConfigsData, addressList);
        // 对原始链接字符串去重
        const uniqueRawConfigs = [...new Set(generatedRawConfigs)];
        const validRawConfigs = uniqueRawConfigs.filter(c => !c.startsWith('[错误]'));
        
        if (validRawConfigs.length === 0) {
            return jsonResponse({ error: '未能生成有效的节点配置' }, 400);
        }

        // 转换为Clash格式
        let clashProxies = [];
        
        // 添加直连节点 (通常直连无需去重，但为了代码统一性，在最终去重时会处理)
        clashProxies.push({name: '直连', type: 'direct'});
        
        // 转换每个配置
        for (const config of validRawConfigs) {
            const clashConfig = convertToClashFormat(config); // index is not strictly needed for unique naming
            if (clashConfig) {
                clashProxies.push(clashConfig);
            }
        }

        // --- 增加 Clash 节点配置对象去重 ---
        // 使用Map进行去重，键是每个代理配置的稳定JSON字符串，值是代理对象本身
        // 确保 'name' 字段是唯一的，因为Clash需要唯一的名称
        const uniqueClashProxiesMap = new Map();
        for (const proxy of clashProxies) {
            // 对代理对象进行排序，以确保 JSON.stringify 结果一致，便于比较
            const stableString = JSON.stringify(proxy, Object.keys(proxy).sort());
            if (!uniqueClashProxiesMap.has(stableString)) {
                uniqueClashProxiesMap.set(stableString, proxy);
            } else {
                console.log("检测到重复的Clash代理配置，已跳过:", proxy.name);
            }
        }
        clashProxies = [...uniqueClashProxiesMap.values()];

        // 重新分配名称，确保所有代理的名称都是唯一的
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
        // --- 去重结束 ---


        if (clashProxies.length <= 1 && clashProxies[0].name === '直连') { // 只有直连节点
            return jsonResponse({ error: '未能生成有效的代理节点' }, 400);
        }
        
        // 获取模板
        const template = await fetchClashConfigTemplate();
        
        // 解析YAML模板
        let finalConfig = template;
        
        // 找到proxies部分并替换
        const proxiesMarker = '# 节点信息\nproxies:';
        let proxiesStartIndex = template.indexOf(proxiesMarker);
        
        if (proxiesStartIndex === -1) {
            // 如果找不到准确的标记，尝试找到 'proxies:' 这一行
            proxiesStartIndex = template.indexOf('\nproxies:');
            if (proxiesStartIndex !== -1) {
                proxiesStartIndex++; // 移动到 'proxies:' 行的开头
            }
        }

        if (proxiesStartIndex !== -1) {
            // 计算代理列表应该插入的位置（在 'proxies:' 行的下一行）
            const insertPosition = template.indexOf('\n', proxiesStartIndex + 'proxies:'.length);
            
            // 找到 proxies 部分结束的位置，通常是下一个顶级键或者文件末尾
            // 简单的方法是找到两个换行符的空行，或者下一个非注释的顶级键
            let proxiesEndIndex = template.indexOf('\n\n', insertPosition);
            if (proxiesEndIndex === -1 || proxiesEndIndex < insertPosition) {
                proxiesEndIndex = template.length; // 如果没有空行，就到文件末尾
            }
            
            // 构建新的proxies部分
            const newProxiesContent = clashProxies.map(p => `  - ${JSON.stringify(p)}`).join('\n');
            
            // 替换/插入proxies部分
            finalConfig = template.substring(0, insertPosition + 1) + 
                         newProxiesContent + 
                         template.substring(proxiesEndIndex);
        } else {
            // 如果模板中完全没有 'proxies:' 这一段，则在文件末尾添加
            console.warn("Clash模板中未找到 'proxies:' 标记，将节点添加到文件末尾。");
            finalConfig = template + `\n\n# ===== 生成的节点配置 =====\nproxies:\n${clashProxies.map(p => `  - ${JSON.stringify(p)}`).join('\n')}`;
        }
        
        // 记录访问日志
        const clientIp = getClientIp(request);
        const userAgent = request.headers.get('user-agent') || 'unknown';
        await logConfigAccess(env, uuid, 'clash-config', clientIp, userAgent);

        // 返回YAML文件
        return new Response(finalConfig, {
            status: 200,
            headers: {
                'Content-Type': 'text/yaml;charset=UTF-8',
                'Content-Disposition': `attachment; filename="clash-config-${uuid}-${new Date().toISOString().split('T')[0]}.yaml"`,
                'X-Generated-Date': new Date().toISOString(),
                'X-Total-Proxies-Unique': clashProxies.length.toString(),
                'X-Cloudflare-Configs': baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length.toString(),
                'X-Address-List-Count': addressList.length.toString()
            }
        });

    } catch (error) {
        console.error('生成Clash配置出错:', error);
        return jsonResponse({ error: `内部错误: ${error.message}` }, 500);
    }
}

// =================================================================
//  ADMIN API (用于后台管理统计)
// =================================================================

/**
 * 处理访问统计API请求
 */
async function handleAccessStats(request, env, url) {
    const uuid = url.searchParams.get('uuid');
    const startDate = url.searchParams.get('start_date');
    const endDate = url.searchParams.get('end_date');
    
    const options = {};
    if (startDate) options.startDate = startDate;
    if (endDate) options.endDate = endDate;
    
    const stats = await getAccessStats(env, uuid, options);
    
    if (!stats.success) {
        return jsonResponse({ error: `获取统计失败: ${stats.error}` }, 500);
    }
    
    // 计算汇总统计
    const summary = {
        total_requests: stats.data.reduce((sum, item) => sum + item.count, 0),
        unique_uuids: [...new Set(stats.data.map(item => item.uuid))].length,
        subscription_requests: stats.data
            .filter(item => item.query_type === 'subscription')
            .reduce((sum, item) => sum + item.count, 0),
        api_generation_requests: stats.data
            .filter(item => item.query_type === 'api-generation')
            .reduce((sum, item) => sum + item.count, 0)
    };
    
    // 按日期统计
    const dailyStats = {};
    stats.data.forEach(item => {
        if (!dailyStats[item.date]) {
            dailyStats[item.date] = {
                total: 0,
                subscription: 0,
                api_generation: 0,
                uuids: new Set()
            };
        }
        dailyStats[item.date].total += item.count;
        dailyStats[item.date].uuids.add(item.uuid);
        
        if (item.query_type === 'subscription') {
            dailyStats[item.date].subscription += item.count;
        } else if (item.query_type === 'api-generation') {
            dailyStats[item.date].api_generation += item.count;
        }
    });
    
    // 转换为数组格式便于前端显示
    const dailyStatsArray = Object.entries(dailyStats).map(([date, data]) => ({
        date,
        total: data.total,
        subscription: data.subscription,
        api_generation: data.api_generation,
        unique_uuids: data.uuids.size
    })).sort((a, b) => new Date(b.date) - new Date(a.date)); // 按日期降序
    
    return jsonResponse({
        summary,
        daily_stats: dailyStatsArray,
        raw_data: stats.data
    });
}

// =================================================================
//  MAIN WORKER ROUTING AND EXPORT
// =================================================================

export default {
    async fetch(request, env, ctx) {
        const url = new URL(request.url);
        const path = url.pathname;
        const method = request.method;
        const DOMAIN_NAME = url.origin;
        const EXTERNAL_CONFIG_MANAGER_URL = "https://config-cfst.api.yangzifun.org/";

        const batchUUID = new URLPattern({ pathname: '/batch-configs/:uuid' });

        try {
            if (method === 'GET') {
                if (path === '/') {
                    return new Response(generatePageHtmlContent.replace(/YOUR_WORKER_DOMAIN_PATH/g, DOMAIN_NAME), { headers: { 'Content-Type': 'text/html;charset=UTF-8' } });
                }
                
                // Redirect to Manager (External)
                if (path === '/manage') {
                    return Response.redirect(EXTERNAL_CONFIG_MANAGER_URL, 302);
                }

                // API: Get List Text
                if (path === '/batch-ip') {
                    return await handleGetBatchAddresses(url, env);
                }

                // API: Subscription
                const batchMatch = batchUUID.exec(url);
                if (batchMatch) {
                    return await handleGetBatchConfigs(batchMatch.pathname.groups.uuid, url.searchParams, env, request);
                }

                // API: Frontend Fetch (Strictly DB)
                if (path === '/fetch-addresses') {
                    const type = url.searchParams.get('type') || 'ip';
                    if (type === 'domain') {
                        const domains = await fetchDomainsFromDB(env);
                        return jsonResponse({ list: domains.map(d => d.domain), message: `获取到 ${domains.length} 个域名` });
                    } else {
                        // IP Logic: Force DB read, ignore any 'source=api' params
                        const ips = await fetchIpsFromDB(
                            url.searchParams.get('ipType') || 'all',
                            url.searchParams.get('carrierType') || 'all',
                            env
                        );
                        return jsonResponse({ list: ips.map(i => i.ip), message: `获取到 ${ips.length} 个IP` }); 
                    }
                }
                // Legacy API
                if (path === '/fetch-ips') {
                     const ips = await fetchIpsFromDB(url.searchParams.get('ipType') || 'all', url.searchParams.get('carrierType') || 'all', env);
                     return jsonResponse({ ips: ips.map(i => i.ip), message: `Success` });
                }
                
                // Admin API: 访问统计 (可添加认证机制)
                if (path === '/admin/stats') {
                    // 这里可以添加认证逻辑，例如检查API密钥
                    // const apiKey = url.searchParams.get('api_key');
                    // if (apiKey !== env.ADMIN_API_KEY) {
                    //     return jsonResponse({ error: 'Unauthorized' }, 401);
                    // }
                    return await handleAccessStats(request, env, url);
                }
                
                // ===== 新增：Clash配置文件生成接口 =====
                if (path === '/clash-config') {
                    return await generateClashConfig(request, env);
                }
            }

            if (method === 'POST') {
                if (path === '/generate') return generateConfigs(request, env);
            }

            return new Response('404 Not Found', { status: 404 });
        } catch (err) {
            return new Response("Internal Error: " + err.message, { status: 500 });
        }
    },
    
    // [SECURITY] No scheduled() function is exported here.
    // This prevents Cloudflare from triggering any automatic Cron Triggers.
};

// =================================================================
//  SECTION 4: FRONTEND CONTENT
// =================================================================

const newGlobalStyle = `
html { font-size: 87.5%; }
body, html { margin: 0; padding: 0; min-height: 100%; background-color: #fff; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
.container { width: 100%; min-height: 100vh; display: flex; flex-direction: column; justify-content: center; align-items: center; padding: 40px 20px; box-sizing: border-box; }
.content-group { width: 100%; max-width: 700px; text-align: center; z-index: 10; box-sizing: border-box; }
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
textarea, input[type="text"] { width: 100%; padding: 10px; border: 2px solid #89949B; border-radius: 4px; background: #fff; font-family: 'SF Mono', 'Courier New', monospace; font-size: 0.9rem; box-sizing: border-box; resize: vertical; }
textarea:focus, input[type="text"]:focus { outline: none; border-color: #3d474d; }
.radio-group { display: flex; flex-wrap: wrap; gap: 10px; }
.radio-group label { padding: 6px 14px; background: #E8EBED; border: 2px solid #89949B; border-radius: 4px; color: #5a666d; font-size: 0.85rem; cursor: pointer; transition: all 0.3s; }
.radio-group input[type="radio"] { display: none; }
.radio-group input[type="radio"]:checked + span { background: #89949B; color: white; }
.radio-group label:hover { background: #d1d5d8; }
.radio-group input[type="radio"]:checked + span:hover { background: #89949B; color: white; }
.info-box { background-color: #e8ebed; color: #5a666d; border-left: 4px solid #89949B; padding: 12px 16px; border-radius: 4px; font-size: 0.85rem; text-align: left; line-height: 1.5; margin: 16px 0; }
.info-box a { color: #3d474d; font-weight: bold; text-decoration: none; word-break: break-all; }
.info-box a:hover { text-decoration: underline; }
.footer { margin-top: 40px; text-align: center; color: #89949B; font-size: 0.8rem; }
.footer a { color: #89949B; text-decoration: none; }
.footer a:hover { text-decoration: underline; }
.hidden { display: none; }
#toast-container { position: fixed; top: 20px; right: 20px; z-index: 9999; display: flex; flex-direction: column; gap: 10px; }
.toast { display: flex; align-items: center; padding: 12px 18px; border-radius: 4px; box-shadow: 0 4px 12px rgba(0,0,0,0.1); font-weight: 500; font-size: 0.9rem; border: 2px solid #89949B; background: #fff; color: #3d474d; opacity: 0; transform: translateX(100%); animation: slideIn 0.5s forwards, fadeOut 0.5s 4.5s forwards; }
@keyframes slideIn { to { opacity: 1; transform: translateX(0); } }
@keyframes fadeOut { from { opacity: 1; } to { opacity: 0; transform: translateX(100%); } }
.loader { width: 16px; height: 16px; border: 2px solid white; border-bottom-color: transparent; border-radius: 50%; display: inline-block; box-sizing: border-box; animation: rotation 1s linear infinite; margin-right: 8px; }
@keyframes rotation { 0% { transform: rotate(0deg); } 100% { transform: rotate(360deg); } }
@media (max-width: 768px) { html { font-size: 100%; } .container { padding: 20px 15px; justify-content: flex-start; } .profile-name { font-size: 1.8rem; } .profile-quote { font-size: 0.95rem; margin-bottom: 20px; } .card { padding: 20px 15px; margin-bottom: 20px; } .card h2 { font-size: 1.3rem; } .nav-btn { padding: 9px 12px; font-size: 0.9rem; } }
`;

const generatePageHtmlContent = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <link rel="icon" href="https://s3.yangzifun.org/logo.ico" type="image/x-icon">
  <title>代理订阅生成</title>
  <style>${newGlobalStyle}</style>
  <style>
    /* 二维码样式 */
    #qrCodeContainer {
      margin-top: 16px;
      text-align: center;
      padding: 16px;
      background: #f8f9fa;
      border: 1px solid #E8EBED;
      border-radius: 8px;
    }
    #qrCodeContainer.hidden {
      display: none;
    }
    #qrCodeImage {
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
    
    /* Clash配置生成样式 */
    .clash-config-section {
      margin-top: 20px;
      padding: 20px;
      background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
      border-radius: 12px;
      color: white;
      text-align: center;
    }
    .clash-config-title {
      font-size: 1.5rem;
      margin-bottom: 15px;
      font-weight: bold;
    }
    .clash-config-description {
      font-size: 0.95rem;
      margin-bottom: 20px;
      opacity: 0.9;
    }
    .clash-config-params {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      justify-content: center;
      margin-bottom: 20px;
    }
    .clash-config-params select {
      padding: 8px 12px;
      border-radius: 6px;
      border: 2px solid rgba(255,255,255,0.3);
      background: rgba(255,255,255,0.1);
      color: white;
      font-size: 0.9rem;
      min-width: 120px;
    }
    .clash-config-params select option {
      background: #3d474d;
      color: white;
    }
    .clash-btn {
      background: white;
      color: #667eea;
      border: none;
      padding: 12px 24px;
      border-radius: 6px;
      font-weight: bold;
      font-size: 1rem;
      cursor: pointer;
      transition: all 0.3s;
    }
    .clash-btn:hover {
      transform: translateY(-2px);
      box-shadow: 0 6px 12px rgba(0,0,0,0.2);
    }
    .clash-btn.loading {
      opacity: 0.7;
      cursor: not-allowed;
    }
    /* 新增的Clash订阅链接和二维码样式 */
    #clashSubscriptionLinkBox {
        margin-top: 15px;
        background: rgba(255,255,255,0.1);
        border: 1px solid rgba(255,255,255,0.3);
        border-radius: 8px;
        padding: 10px 15px;
        display: flex;
        align-items: center;
        gap: 10px;
        word-break: break-all;
    }
    #clashGeneratedLink {
        flex-grow: 1;
        background: rgba(255,255,255,0.1);
        border: 1px solid rgba(255,255,255,0.3);
        color: white;
    }
    #clashQrCodeContainer {
      margin-top: 16px;
      text-align: center;
      padding: 16px;
      background: rgba(255,255,255,0.1); /* Adjusted to match section style */
      border: 1px solid rgba(255,255,255,0.3); /* Adjusted to match section style */
      border-radius: 8px;
    }
    #clashQrCodeImage {
      max-width: 200px;
      height: auto;
      border: 1px solid rgba(255,255,255,0.3); /* Adjusted to match section style */
      border-radius: 4px;
      margin: 10px 0;
    }
    #clashQrCodeContainer .qr-code-label, #clashQrCodeContainer .qr-code-info {
        color: white; /* Ensure text is visible on dark background */
    }
    /* End New Clash subscription styles */
  </style>
</head>
<body>
  <div id="toast-container"></div>
  <div class="container">
    <div class="content-group">
      <h1 class="profile-name">代理配置批量生成</h1>
      <p class="profile-quote">支持 IP 优选与域名优选的批量替换工具</p>

      <div class="nav-grid">
        <a href="/" class="nav-btn primary">批量生成</a>
        <a href="https://config.proxypilot.yangzifun.org/" target="_blank" class="nav-btn">配置管理</a>
      </div>

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
            <textarea id="genBaseConfigInput" placeholder="在此粘贴一个或多个基础配置，每行一个。" rows="6"></textarea>
        </div>
        <div id="genUuidConfigInput" class="form-group hidden">
            <input type="text" id="genBaseConfigUuidInput" placeholder="输入已存储的UUID，如 my-configs">
            <div class="info-box">在 <a href="https://config-cfst.api.yangzifun.org/" target="_blank">配置管理</a> 页面添加和管理UUID。
            <br>所有配置都会输出原配置，只有域名托管属性为"Cloudflare"的配置会进行优选替换。</div>
        </div>
      </div>

      <div class="card">
        <h2>2. 优选列表 (IP/域名)</h2>
        <div class="form-group">
          <label>地址类型</label>
          <div class="radio-group">
            <label><input type="radio" name="genAddressType" value="ip" checked onchange="toggleAddressOptions()"><span>IP 地址</span></label>
            <label><input type="radio" name="genAddressType" value="domain" onchange="toggleAddressOptions()"><span>优选域名</span></label>
          </div>
        </div>

        <div id="ipOptionsContainer">
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

        <div id="domainInfoBox" class="info-box hidden">
           将使用数据库中存储的 Cloudflare 优选域名列表进行替换。
        </div>

        <button id="genFetchBtn" class="nav-btn" style="width:100%; margin-bottom: 16px;" onclick="genFetchAddresses()">获取优选列表</button>
        
        <div id="ipSubscriptionLinkBox" class="form-group hidden" style="display:none; gap:10px;">
            <input type="text" id="ipSubscriptionLink" readonly style="margin-bottom:0; color:#5a666d; background-color: #f8f9fa;">
            <button class="nav-btn" onclick="copyIpSubscriptionLink()" style="white-space: nowrap;">复制</button>
        </div>

        <textarea id="genAddressListInput" placeholder="点击上方按钮获取，或在此手动粘贴 IP 或 域名 列表..." rows="6"></textarea>
      </div>

      <div class="card">
        <h2>3. 生成结果</h2>
        <div id="generatedLinkBox" class="form-group hidden" style="display:none; gap:10px;">
          <label style="align-self:center; white-space:nowrap; margin-bottom:0; margin-right:5px;">订阅:</label>
          <input type="text" id="generatedConfigLink" readonly style="margin-bottom:0; color:#5a666d; background-color: #f8f9fa;">
          <button class="nav-btn" onclick="copyGeneratedLink()" style="white-space: nowrap;">复制</button>
        </div>
        <textarea id="genResultTextarea" readonly placeholder="点击下方"生成配置"按钮..." rows="8"></textarea>
        
        <!-- 二维码区域 -->
        <div id="qrCodeContainer" class="hidden">
          <label class="qr-code-label">订阅链接二维码</label>
          <div id="qrCodeImageContainer">
            <img id="qrCodeImage" src="" alt="订阅链接二维码">
          </div>
          <div class="qr-code-buttons">
            <button class="nav-btn" onclick="downloadQRCode()">下载二维码</button>
            <button class="nav-btn" onclick="shareQRCode()">分享二维码</button>
            <button class="nav-btn" onclick="refreshQRCode()">刷新</button>
          </div>
          <div class="qr-code-info">
            扫描二维码即可导入订阅配置。二维码内容为上方订阅链接。
          </div>
        </div>
      </div>

      <button id="genGenerateButton" class="nav-btn primary" style="width:100%; padding: 12px;" onclick="genGenerateConfigs()">生成配置</button>
      <button id="copyResultButton" class="nav-btn" style="width:100%; margin-top: 10px;" onclick="copyResults()">复制结果 (Base64)</button>
      
      <!-- ===== 新增：Clash配置文件生成区域 ===== -->
      <div class="clash-config-section">
        <h2 class="clash-config-title">🚀 一键生成 Clash/Mihomo 订阅文件</h2>
        <p class="clash-config-description">使用模板和优选地址，生成可直接使用的配置文件</p>
        
        <div class="form-group">
          <input type="text" id="clashConfigUuid" placeholder="输入UUID（必须已在配置管理中创建）" style="width: 100%; max-width: 300px; margin-bottom: 15px;">
          <div class="info-box" style="background: rgba(255,255,255,0.1); border-color: rgba(255,255,255,0.3);">
            <strong>说明：</strong> 使用你的UUID，基于优选地址和模板生成完整配置文件
          </div>
        </div>
        
        <div class="clash-config-params">
          <select id="clashAddressType">
            <option value="ip">IP地址</option>
            <option value="domain">域名</option>
          </select>
          <select id="clashIpType">
            <option value="all">全部IP</option>
            <option value="v4">仅IPv4</option>
            <option value="v6">仅IPv6</option>
          </select>
          <select id="clashCarrier">
            <option value="all">全部运营商</option>
            <option value="CM">移动</option>
            <option value="CU">联通</option>
            <option value="CT">电信</option>
          </select>
        </div>
        
        <button id="generateClashBtn" class="clash-btn" onclick="generateClashConfig()">生成Clash配置文件</button>
        
        <!-- Clash订阅链接及二维码区域 -->
        <div id="clashSubscriptionLinkBox" class="form-group hidden" style="display:none;">
            <label style="align-self:center; white-space:nowrap; margin-bottom:0; margin-right:5px; color:white;">订阅链接:</label>
            <input type="text" id="clashGeneratedLink" readonly style="margin-bottom:0; color:#5a666d; background-color: #f8f9fa;">
            <button class="nav-btn" onclick="copyClashGeneratedLink()" style="white-space: nowrap;">复制</button>
        </div>
        <div id="clashQrCodeContainer" class="hidden">
          <label class="qr-code-label">订阅链接二维码</label>
          <div id="clashQrCodeImageContainer">
            <img id="clashQrCodeImage" src="" alt="Clash订阅链接二维码">
          </div>
          <div class="qr-code-buttons">
            <button class="nav-btn" onclick="downloadClashQRCode()">下载二维码</button>
            <button class="nav-btn" onclick="shareClashQRCode()">分享二维码</button>
            <button class="nav-btn" onclick="refreshClashQRCode()">刷新</button>
          </div>
          <div class="qr-code-info">
            扫描二维码即可导入Clash配置文件。二维码内容为上方订阅链接。
          </div>
        </div>
        <!-- End Clash订阅链接及二维码区域 -->

        <div class="info-box" style="margin-top: 15px; background: rgba(255,255,255,0.1); border-color: rgba(255,255,255,0.3);">
          配置文件基于 <a href="https://raw.githubusercontent.com/yangzifun/rule/refs/heads/main/config.yaml" target="_blank" style="color: white; font-weight: bold;">此模板</a> 生成，包含完整的规则和策略组
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
    let currentSubscriptionLink = ''; // For raw proxy subscription
    let currentClashSubscriptionLink = ''; // For Clash config subscription
    
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
        btn.disabled=true; 
        btn.innerHTML='<span class="loader"></span>处理中...'; 
      } else { 
        btn.disabled=false; 
        btn.innerHTML=txt; 
      } 
    }
    
    // ===========================================
    // 通用订阅链接二维码相关函数 (用于 raw proxy list)
    // ===========================================
    function generateQRCode(subscriptionLink) {
      if (!subscriptionLink) return;
      
      currentSubscriptionLink = subscriptionLink;
      const qrCodeContainer = document.getElementById('qrCodeContainer');
      const qrCodeImage = document.getElementById('qrCodeImage');
      
      const encodedUrl = encodeURIComponent(subscriptionLink);
      const qrCodeUrl = \`\${QR_CODE_API}?text=\${encodedUrl}&cellSize=4&margin=0&errorCorrectionLevel=L&typeNumber=0\`;
      
      qrCodeImage.src = qrCodeUrl;
      qrCodeImage.alt = \`订阅链接二维码: \${subscriptionLink.substring(0, 50)}...\`;
      
      qrCodeContainer.classList.remove('hidden');
      
      qrCodeImage.onload = function() { showToast('二维码生成成功', 'success'); };
      qrCodeImage.onerror = function() { showToast('二维码生成失败，请重试', 'error'); };
    }
    
    function downloadQRCode() {
      if (!currentSubscriptionLink) { showToast('没有可用的订阅链接', 'error'); return; }
      const qrCodeImage = document.getElementById('qrCodeImage');
      const link = document.createElement('a');
      link.href = qrCodeImage.src;
      link.download = 'subscription-qrcode.png';
      link.click();
      showToast('二维码已下载', 'success');
    }
    
    function shareQRCode() {
      if (!currentSubscriptionLink) { showToast('没有可用的订阅链接', 'error'); return; }
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
    
    function refreshQRCode() {
      if (!currentSubscriptionLink) { showToast('没有可用的订阅链接', 'error'); return; }
      generateQRCode(currentSubscriptionLink);
      showToast('二维码已刷新', 'info');
    }
    // ===========================================
    // END 通用订阅链接二维码相关函数
    // ===========================================

    // ===========================================
    // Clash 配置订阅链接二维码相关函数
    // ===========================================
    function generateClashQRCode(subscriptionLink) {
      if (!subscriptionLink) return;
      
      currentClashSubscriptionLink = subscriptionLink;
      const qrCodeContainer = document.getElementById('clashQrCodeContainer');
      const qrCodeImage = document.getElementById('clashQrCodeImage');
      
      const encodedUrl = encodeURIComponent(subscriptionLink);
      const qrCodeUrl = \`\${QR_CODE_API}?text=\${encodedUrl}&cellSize=4&margin=0&errorCorrectionLevel=L&typeNumber=0\`;
      
      qrCodeImage.src = qrCodeUrl;
      qrCodeImage.alt = \`Clash订阅链接二维码: \${subscriptionLink.substring(0, 50)}...\`;
      
      qrCodeContainer.classList.remove('hidden');
      
      qrCodeImage.onload = function() { showToast('Clash二维码生成成功', 'success'); };
      qrCodeImage.onerror = function() { showToast('Clash二维码生成失败，请重试', 'error'); };
    }

    function downloadClashQRCode() {
      if (!currentClashSubscriptionLink) { showToast('没有可用的Clash订阅链接', 'error'); return; }
      const qrCodeImage = document.getElementById('clashQrCodeImage');
      const link = document.createElement('a');
      link.href = qrCodeImage.src;
      link.download = 'clash-subscription-qrcode.png';
      link.click();
      showToast('Clash二维码已下载', 'success');
    }

    function shareClashQRCode() {
      if (!currentClashSubscriptionLink) { showToast('没有可用的Clash订阅链接', 'error'); return; }
      if (navigator.share) {
        navigator.share({
          title: 'Clash代理配置',
          text: '扫描二维码导入Clash配置文件',
          url: currentClashSubscriptionLink,
        })
        .then(() => showToast('分享Clash链接成功', 'success'))
        .catch(error => {
          console.error('分享Clash链接失败:', error);
          navigator.clipboard.writeText(currentClashSubscriptionLink)
            .then(() => showToast('分享失败，已复制Clash链接到剪贴板', 'info'));
        });
      } else {
        navigator.clipboard.writeText(currentClashSubscriptionLink)
          .then(() => showToast('Clash链接已复制到剪贴板', 'success'));
      }
    }

    function refreshClashQRCode() {
      if (!currentClashSubscriptionLink) { showToast('没有可用的Clash订阅链接', 'error'); return; }
      generateClashQRCode(currentClashSubscriptionLink);
      showToast('Clash二维码已刷新', 'info');
    }
    // ===========================================
    // END Clash 配置订阅链接二维码相关函数
    // ===========================================
    
    function toggleAddressOptions() {
        const type = document.querySelector('input[name="genAddressType"]:checked').value;
        const ipContainer = document.getElementById('ipOptionsContainer');
        const domainBox = document.getElementById('domainInfoBox');
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
        const subBox = document.getElementById('ipSubscriptionLinkBox');
        const subInput = document.getElementById('ipSubscriptionLink');
        
        // Reset sub box
        subBox.style.display = 'none';
        subBox.classList.add('hidden');

        setButtonLoading(btn, true, '');
        
        try {
            const addrType = document.querySelector('input[name="genAddressType"]:checked').value;
            let url = \`\${WORKER_DOMAIN}/fetch-addresses?type=\${addrType}\`;
            
            if (addrType === 'ip') {
                const ipType = document.querySelector('input[name="genIpType"]:checked').value;
                const carrier = document.querySelector('input[name="genCarrierType"]:checked').value;
                url += \`&ipType=\${ipType}&carrierType=\${carrier}&source=database\`; // Always database
                
                // Show subscription link logic
                const subUrl = \`\${WORKER_DOMAIN}/batch-ip?type=ip&ipType=\${ipType}&carrier=\${carrier}\`;
                subInput.value = subUrl;
                subBox.style.display = 'flex';
                subBox.classList.remove('hidden');

            } else {
                // Domain subscription link
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
        const linkBox = document.getElementById('generatedLinkBox');
        const linkInput = document.getElementById('generatedConfigLink');
        const qrCodeContainer = document.getElementById('qrCodeContainer');

        // Reset
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
                
                // Construct subscription link for UUID mode
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
                
                // 生成二维码
                generateQRCode(subLink);

            } else {
                const manualConfig = document.getElementById('genBaseConfigInput').value.trim();
                if(!manualConfig) throw new Error("请输入基础配置");
                payload.baseConfig = manualConfig;
                
                // 手动模式不生成二维码，因为没有订阅链接
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
                
                // 显示统计信息
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
        const b64 = btoa(unescape(encodeURIComponent(content))); // utf-8 safe b64
        navigator.clipboard.writeText(b64).then(() => showToast('已复制 Base64 编码结果', 'success'));
    }
    
    function copyGeneratedLink() {
        const link = document.getElementById('generatedConfigLink').value;
        if(!link) return;
        navigator.clipboard.writeText(link).then(() => showToast('订阅链接已复制', 'success'));
    }

    function copyIpSubscriptionLink() {
        const link = document.getElementById('ipSubscriptionLink').value;
        if(!link) return;
        navigator.clipboard.writeText(link).then(() => showToast('列表地址已复制', 'success'));
    }
    
    // ===== 新增：Clash配置文件生成功能 =====
    async function generateClashConfig() {
        const btn = document.getElementById('generateClashBtn');
        const uuid = document.getElementById('clashConfigUuid').value.trim();
        const addressType = document.getElementById('clashAddressType').value;
        const ipType = document.getElementById('clashIpType').value;
        const carrier = document.getElementById('clashCarrier').value;

        // Reset previous link/QR
        const clashLinkBox = document.getElementById('clashSubscriptionLinkBox');
        const clashLinkInput = document.getElementById('clashGeneratedLink');
        const clashQrContainer = document.getElementById('clashQrCodeContainer');
        clashLinkBox.style.display = 'none';
        clashLinkBox.classList.add('hidden');
        clashQrContainer.classList.add('hidden');
        clashLinkInput.value = ''; // Clear previous value
        currentClashSubscriptionLink = ''; // Clear stored link

        if (!uuid) {
            showToast('请输入UUID', 'error');
            return;
        }
        
        setButtonLoading(btn, true, '正在生成...');
        
        try {
            // 构建请求URL，此URL用于Clash客户端订阅
            const subscriptionUrl = new URL(\`\${WORKER_DOMAIN}/clash-config\`);
            subscriptionUrl.searchParams.set('uuid', uuid);
            subscriptionUrl.searchParams.set('type', addressType);
            
            if (addressType === 'ip') {
                subscriptionUrl.searchParams.set('ipType', ipType);
                subscriptionUrl.searchParams.set('carrier', carrier);
            }
            
            // 使用构建的URL作为订阅链接
            const clashSubscriptionLink = subscriptionUrl.toString();
            
            // 尝试获取文件内容（不下载，仅用于确认URL可用性或预加载）
            // 实际上，Clash客户端会直接访问这个URL，这里可以跳过直接下载
            // 但为了给用户一个"下载成功"的即时反馈，我们仍然触发一次下载
            const response = await fetch(subscriptionUrl); // 这里依然触发一次Worker的/clash-config接口
            
            if (!response.ok) {
                const errorData = await response.json().catch(() => ({}));
                throw new Error(errorData.error || \`生成失败: \${response.status}\`);
            }
            
            // 创建下载链接 (保留原有下载功能)
            const blob = await response.blob();
            const downloadUrl = URL.createObjectURL(blob);
            
            const a = document.createElement('a');
            a.href = downloadUrl;
            a.download = \`clash-config-\${uuid}-\${new Date().toISOString().split('T')[0]}.yaml\`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(downloadUrl);
            
            showToast('Clash配置文件生成成功，已开始下载', 'success');

            // 显示Clash订阅链接
            clashLinkInput.value = clashSubscriptionLink;
            clashLinkBox.style.display = 'flex';
            clashLinkBox.classList.remove('hidden');
            
            // 生成Clash订阅链接的二维码
            generateClashQRCode(clashSubscriptionLink);
            
        } catch (error) {
            console.error('生成Clash配置失败:', error);
            showToast(\`生成失败: \${error.message}\`, 'error');
        } finally {
            setButtonLoading(btn, false, '生成Clash配置文件');
        }
    }

    function copyClashGeneratedLink() {
        const link = document.getElementById('clashGeneratedLink').value;
        if(!link) return;
        navigator.clipboard.writeText(link).then(() => showToast('Clash订阅链接已复制', 'success'));
    }
    
    // 监听地址类型变化，更新IP类型选择框的显示
    document.getElementById('clashAddressType').addEventListener('change', function() {
        const ipTypeSelect = document.getElementById('clashIpType');
        const carrierSelect = document.getElementById('clashCarrier');
        if (this.value === 'ip') {
            ipTypeSelect.style.display = 'inline-block'; // 保持水平布局
            carrierSelect.style.display = 'inline-block'; // 保持水平布局
        } else {
            ipTypeSelect.style.display = 'none';
            carrierSelect.style.display = 'none';
        }
    });

    // 页面加载时初始化IP/域名选择器的显示状态
    document.addEventListener('DOMContentLoaded', () => {
        toggleAddressOptions();
        // 确保Clash配置生成部分的IP/运营商选择器初始状态正确
        document.getElementById('clashAddressType').dispatchEvent(new Event('change'));
    });
  </script>
</body>
</html>
`;
