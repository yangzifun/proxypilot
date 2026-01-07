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
        { prefix: 'anyreality://', type: 'any-reality' },
        { prefix: 'reality://', type: 'reality' }
    ];

    for (const pattern of protocolPatterns) {
        if (configStr.toLowerCase().startsWith(pattern.prefix)) {
            return pattern.type;
        }
    }

    return 'unknown';
}

// =================================================================
//  SECTION 0: Access Logging
// =================================================================

/**
 * 记录配置访问日志
 * @param {Object} env - Worker环境变量
 * @param {string} uuid - 用户UUID
 * @param {string} queryType - 查询类型: 'api-generation'(网页生成)
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

// =================================================================
//  SECTION 1: Data Fetching (STRICTLY READ ONLY)
// =================================================================

// 获取 IP (仅读取 SELECT) - 已增加 DISTINCT 去重
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

// 获取域名 (仅读取 SELECT) - 已增加 DISTINCT 去重
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

// =================================================================
//  SECTION 2.5: Clash/Mihomo Config Generation
// =================================================================

// 读取基础配置 (SELECT)
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

// 增强的地址提取正则表达式，支持更多协议格式
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

                    // 如果是IPv6，需要调整URL格式
                    const newUrlString = url.toString();
                    generatedConfigs.push(newUrlString);
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

    // 去重生成的配置
    return [...new Set(generatedConfigs)];
}

// 获取Clash配置模板 - 仅从数据库或自定义URL获取
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

// 将单个协议配置转换为Clash格式 - 增强版本，支持更多协议
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

            // 处理网络类型
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

            // 处理网络类型
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
                'skip-cert-verify': true
            };

            // 处理流设置
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

            // 解析密码和方法
            let password = '';
            let method = 'aes-256-gcm';

            // SS协议格式: ss://method:password@host:port
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

            // 检查是否有插件参数
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

            // 处理其他参数
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

            // 处理其他参数
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

    // 处理AnyTLS配置（通常基于VLESS/Xray）
    if (protocol === 'anytls') {
        try {
            const url = new URL(configStr);
            const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `AnyTLS-${url.hostname}-${url.port}`;

            // 根据特征判断具体协议类型
            let type = 'vless'; // 默认VLESS
            let uuid = url.username;
            // let password = ''; // password is not typically used for VLESS, removed as it's unused

            // 检查是否有特定参数
            // const flowParam = url.searchParams.get('flow'); // unused in clash config
            // const securityParam = url.searchParams.get('security'); // unused in clash config

            const clashConfig = {
                name: name,
                type: type,
                server: url.hostname,
                port: parseInt(url.port) || 443,
                uuid: uuid,
                tls: true,
                'skip-cert-verify': true
            };

            // 处理流设置
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

            // 处理SNI
            const sniParam = url.searchParams.get('sni') || url.searchParams.get('host');
            if (sniParam) {
                clashConfig.sni = sniParam;
            }

            // 处理ALPN
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

            // 默认使用VLESS作为底层协议
            const clashConfig = {
                name: name,
                type: 'vless',
                server: url.hostname,
                port: parseInt(url.port) || 443,
                uuid: url.username,
                'skip-cert-verify': true,
                'client-fingerprint': 'chrome' // FIX: Added quotes around client-fingerprint
            };

            // Reality特定参数
            const realityConfig = {
                type: 'reality',
                'server-name': url.searchParams.get('sni') || url.hostname,
                'public-key': url.searchParams.get('pbk') || '',
                'short-id': url.searchParams.get('sid') || ''
            };

            // 合并配置
            Object.assign(clashConfig, realityConfig);

            // 处理流设置
            const typeParam = url.searchParams.get('type') || '';
            if (typeParam === 'grpc') {
                clashConfig.network = 'grpc';
                clashConfig['grpc-opts'] = {
                    'grpc-service-name': url.searchParams.get('serviceName') || ''
                };
            } else if (typeParam === 'tcp') {
                clashConfig.network = 'tcp';
            }

            // 处理指纹
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

// 生成完整的Clash配置文件 - 已增加 Clash 节点配置去重
async function generateClashConfig(request, env) {
    try {
        const url = new URL(request.url);
        const uuid = url.searchParams.get('uuid');
        const addressType = url.searchParams.get('type') || 'ip';
        const ipType = url.searchParams.get('ipType') || 'all';
        const carrier = url.searchParams.get('carrier') || 'all';
        const template = url.searchParams.get('template') || '';

        // 验证必要参数
        if (!uuid) {
            return jsonResponse({ error: '缺少必要参数: uuid' }, 400);
        }

        if (!template) {
            return jsonResponse({ error: '缺少必要参数: template' }, 400);
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
        const validRawConfigs = generatedRawConfigs.filter(c => !c.startsWith('[错误]'));

        if (validRawConfigs.length === 0) {
            return jsonResponse({ error: '未能生成有效的节点配置' }, 400);
        }

        // 转换为Clash格式
        let clashProxies = [];

        // 添加直连节点
        clashProxies.push({name: '直连', type: 'direct'});

        // 转换每个配置
        for (const config of validRawConfigs) {
            const clashConfig = convertToClashFormat(config);
            if (clashConfig) {
                clashProxies.push(clashConfig);
            }
        }

        // --- 增加 Clash 节点配置对象去重 ---
        const uniqueClashProxiesMap = new Map();
        for (const proxy of clashProxies) {
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

        if (clashProxies.length <= 1 && clashProxies[0].name === '直连') {
            return jsonResponse({ error: '未能生成有效的代理节点' }, 400);
        }

        // 根据template参数选择模板URL
        let templateUrl;
        if (template.startsWith('http://') || template.startsWith('https://')) {
            // 自定义模板URL
            templateUrl = template;
        } else {
            // 从数据库中获取模板
            const dbTemplate = await fetchTemplateFromDB(env, template);
            if (!dbTemplate) {
                return jsonResponse({ error: `未在数据库中找到模板 "${template}"` }, 404);
            }
            templateUrl = dbTemplate.url;
        }

        // 获取模板
        const templateContent = await fetchClashConfigTemplate(templateUrl);

        // 解析YAML模板
        let finalConfig = templateContent;

        // 找到proxies部分并替换
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

        // 记录访问日志 - 根据referer判断是网页生成还是订阅获取
        const clientIp = getClientIp(request);
        const userAgent = request.headers.get('user-agent') || 'unknown';

        // 判断请求来源：如果有referer并且referer包含当前域，则认为是网页生成
        // 否则认为是客户端订阅
        const referer = request.headers.get('referer');
        const isWebPageGeneration = referer && referer.includes(url.origin);
        const queryType = isWebPageGeneration ? 'api-generation' : 'subscription';

        await logConfigAccess(env, uuid, queryType, clientIp, userAgent);
        console.log(`记录${isWebPageGeneration ? '网页生成' : '订阅获取'}访问: uuid=${uuid}, clientIp=${clientIp}`);

        // 统计支持的协议类型
        const protocolStats = {};
        clashProxies.forEach(proxy => {
            if (proxy.type !== 'direct') {
                protocolStats[proxy.type] = (protocolStats[proxy.type] || 0) + 1;
            }
        });

        // 返回YAML文件
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

// 从数据库获取模板URL - 修改为只从数据库读取
async function fetchTemplateFromDB(env, templateName) {
    try {
        const db = env.DB;
        if (!db) {
            throw new Error("D1数据库未绑定，无法获取模板");
        }

        // 获取模板及其描述信息
        const template = await db.prepare(
            'SELECT name, url, description FROM config_templates WHERE name = ?'
        ).bind(templateName).first();

        if (template && template.url) {
            console.log(`从数据库找到模板: ${template.name} -> ${template.url}`);
            return template;
        }

        // 数据库中未找到模板，返回null
        console.log(`数据库中未找到模板: ${templateName}`);
        return null;
    } catch (e) {
        console.error(`从数据库获取模板失败: ${e.message}`);
        throw new Error(`无法从数据库获取模板: ${e.message}`);
    }
}

// 从数据库获取所有模板列表
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

// =================================================================
//  MAIN WORKER ROUTING AND EXPORT
// =================================================================

export default {
    async fetch(request, env, ctx) {
        const url = new URL(request.url);
        const path = url.pathname;
        const method = request.method;
        const DOMAIN_NAME = url.origin;

        try {
            if (method === 'GET') {
                if (path === '/') {
                    return new Response(generatePageHtmlContent.replace(/YOUR_WORKER_DOMAIN_PATH/g, DOMAIN_NAME), { headers: { 'Content-Type': 'text/html;charset=UTF-8' } });
                }

                // ===== 新增：获取模板列表接口 =====
                if (path === '/api/templates') {
                    try {
                        const templates = await fetchAllTemplatesFromDB(env);
                        return jsonResponse(templates);
                    } catch (error) {
                        return jsonResponse({ error: `获取模板列表失败: ${error.message}` }, 500);
                    }
                }

                // ===== 仅保留：Clash配置文件生成接口 =====
                if (path === '/clash-config') {
                    return await generateClashConfig(request, env);
                }
            }

            return new Response('404 Not Found', { status: 404 });
        } catch (err) {
            return new Response("Internal Error: " + err.message, { status: 500 });
        }
    }
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
textarea, input[type="text"], select { width: 100%; padding: 10px; border: 2px solid #89949B; border-radius: 4px; background: #fff; font-family: 'SF Mono', 'Courier New', monospace; font-size: 0.9rem; box-sizing: border-box; resize: vertical; }
textarea:focus, input[type="text"]:focus, select:focus { outline: none; border-color: #3d474d; }
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
  <title>订阅文件生成</title>
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
    
    /* 参数选择器样式 */
    .config-params {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      justify-content: center;
      margin-bottom: 20px;
    }
    .config-params select {
      padding: 8px 12px;
      border-radius: 6px;
      border: 2px solid #89949B;
      background: #fff;
      color: #5a666d;
      font-size: 0.9rem;
      min-width: 120px;
    }
    
    /* 订阅链接区域 */
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
    
    /* 模板选择样式 */
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
    #customTemplateInput {
      margin-top: 10px;
    }
    
    /* 加载状态 */
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
    
    /* 协议支持说明 */
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
  </style>
</head>
<body>
  <div id="toast-container"></div>
  <div class="container">
    <div class="content-group">
      <h1 class="profile-name">订阅文件生成</h1>
      <p class="profile-quote">基于优选地址和配置文件模板，一键生成完整的Clash/Mihomo订阅</p>

      <div class="nav-grid">
        <a href="/" class="nav-btn primary">订阅管理</a>
        <a href="https://cfst.yangzifun.org/" target="_blank" class="nav-btn">批量生成</a>
        <a href="https://config.proxypilot.yangzifun.org/" target="_blank" class="nav-btn">配置管理</a>
      </div>

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
            在<a href="https://config.proxypilot.yangzifun.org/" target="_blank">配置管理</a> 页面添加和管理配置，获取UUID后在此输入。
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
            <label><input type="radio" name="clashAddressType" value="ip" checked onchange="toggleAddressOptions()"><span>IP 地址</span></label>
            <label><input type="radio" name="clashAddressType" value="domain" onchange="toggleAddressOptions()"><span>优选域名</span></label>
          </div>
        </div>

        <div id="ipOptionsContainer">
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

        <div id="domainInfoBox" class="info-box hidden">
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
          
          <div id="qrCodeContainer" class="hidden">
            <label class="qr-code-label">订阅链接二维码</label>
            <div id="qrCodeImageContainer">
              <img id="qrCodeImage" src="" alt="订阅链接二维码">
            </div>
            <div class="qr-code-buttons">
              <button class="nav-btn" onclick="downloadQRCode()">下载二维码</button>
              <button class="nav-btn" onclick="shareQRCode()">分享</button>
              <button class="nav-btn" onclick="refreshQRCode()">刷新</button>
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

      <footer class="footer">
         <p>Powered by <a href="https://www.yangzifun.org">YZFN</a> | <a href="https://www.yangzifun.org/security.html">安全声明</a></p>
      </footer>
    </div>
  </div>

  <script>
    const WORKER_DOMAIN = "YOUR_WORKER_DOMAIN_PATH";
    const QR_CODE_API = "https://qrcode.api.yangzifun.org/qr";
    const toastIcons = { success: '✅', error: '❌', info: 'ℹ️' };
    let currentSubscriptionLink = '';
    let templatesList = [];
    
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
    
    // 二维码相关函数
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
      link.download = 'clash-subscription-qrcode.png';
      link.click();
      showToast('二维码已下载', 'success');
    }
    
    function shareQRCode() {
      if (!currentSubscriptionLink) { showToast('没有可用的订阅链接', 'error'); return; }
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
    
    function refreshQRCode() {
      if (!currentSubscriptionLink) { showToast('没有可用的订阅链接', 'error'); return; }
      generateQRCode(currentSubscriptionLink);
      showToast('二维码已刷新', 'info');
    }
    
    // 地址类型切换
    function toggleAddressOptions() {
        const type = document.querySelector('input[name="clashAddressType"]:checked').value;
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
    
    // 模板类型切换
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
            // 从缓存的模板列表中查找描述
            const template = templatesList.find(t => t.name === selectedValue);
            if (template && template.description) {
                templateDescription.textContent = template.description;
            } else {
                templateDescription.textContent = '数据库模板配置。';
            }
        }
    }
    
    // 获取模板参数值
    function getTemplateParam() {
        const templateSelect = document.getElementById('templateSelect');
        const selectedValue = templateSelect.value;
        
        if (selectedValue === 'custom') {
            const customUrl = document.getElementById('customTemplateUrl').value.trim();
            if (!customUrl) {
                showToast('请输入自定义模板URL', 'error');
                return null;
            }
            // 验证URL格式
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
    
    // 从服务器加载模板列表
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
            
            // 清空选择框
            templateSelect.innerHTML = '';
            
            // 添加自定义模板选项
            const customOption = document.createElement('option');
            customOption.value = 'custom';
            customOption.textContent = '自定义模板';
            templateSelect.appendChild(customOption);
            
            // 添加数据库中的模板选项
            if (templates.length === 0) {
                const noTemplateOption = document.createElement('option');
                noTemplateOption.value = '';
                noTemplateOption.textContent = '数据库中没有可用模板';
                templateSelect.appendChild(noTemplateOption);
                showToast('数据库中没有配置模板', 'error');
            } else {
                // 查找默认模板
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
                
                // 如果没有设置默认模板，选择第一个
                if (!defaultTemplate && templates.length > 0) {
                    templateSelect.value = templates[0].name;
                }
                
                // 更新模板描述
                const selectedValue = templateSelect.value;
                const selectedTemplate = templates.find(t => t.name === selectedValue);
                const templateDescription = document.getElementById('templateDescription');
                if (selectedTemplate && selectedTemplate.description) {
                    templateDescription.textContent = selectedTemplate.description;
                }
                
                // 启用生成按钮
                generateClashBtn.disabled = false;
            }
            
            showToast('模板列表加载成功', 'success');
        } catch (error) {
            console.error('加载模板失败:', error);
            templateError.classList.remove('hidden');
            showToast(\`加载模板失败: \${error.message}\`, 'error');
            
            // 添加错误选项
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
    
    // Clash配置文件生成功能
    async function generateClashConfig() {
        const btn = document.getElementById('generateClashBtn');
        const uuid = document.getElementById('clashConfigUuid').value.trim();
        const addressType = document.querySelector('input[name="clashAddressType"]:checked').value;
        const ipType = document.querySelector('input[name="clashIpType"]:checked').value;
        const carrier = document.querySelector('input[name="clashCarrier"]:checked').value;
        
        // 获取模板参数
        const templateParam = getTemplateParam();
        if (templateParam === null) return;

        // 重置之前的链接和二维码
        const clashLinkBox = document.getElementById('clashSubscriptionLinkBox');
        const clashLinkInput = document.getElementById('clashGeneratedLink');
        const qrCodeContainer = document.getElementById('qrCodeContainer');
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
            // 构建请求URL，此URL用于Clash客户端订阅
            const subscriptionUrl = new URL(\`\${WORKER_DOMAIN}/clash-config\`);
            subscriptionUrl.searchParams.set('uuid', uuid);
            subscriptionUrl.searchParams.set('type', addressType);
            subscriptionUrl.searchParams.set('template', templateParam);
            
            if (addressType === 'ip') {
                subscriptionUrl.searchParams.set('ipType', ipType);
                subscriptionUrl.searchParams.set('carrier', carrier);
            }
            
            // 使用构建的URL作为订阅链接
            const clashSubscriptionLink = subscriptionUrl.toString();
            
            // 触发文件下载
            const response = await fetch(subscriptionUrl);
            
            if (!response.ok) {
                const errorData = await response.json().catch(() => ({}));
                throw new Error(errorData.error || \`生成失败: \${response.status}\`);
            }
            
            // 创建下载链接
            const blob = await response.blob();
            const downloadUrl = URL.createObjectURL(blob);
            
            // 触发下载
            const a = document.createElement('a');
            a.href = downloadUrl;
            a.download = \`clash-config-\${uuid}-\${new Date().toISOString().split('T')[0]}.yaml\`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(downloadUrl);
            
            showToast('Clash订阅文件生成成功，已开始下载', 'success');

            // 显示订阅链接
            clashLinkInput.value = clashSubscriptionLink;
            clashLinkBox.classList.remove('hidden');
            
            // 生成二维码
            generateQRCode(clashSubscriptionLink);
            
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

    // 页面加载时初始化
    document.addEventListener('DOMContentLoaded', () => {
        toggleAddressOptions();
        loadTemplates();
    });
  </script>
</body>
</html>
`;
