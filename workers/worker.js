/* =================================================================
 *  Cloudflare Worker: All-in-One Proxy Tool
 *  功能：
 *  1. 配置管理器（包含域名托管属性）
 *  2. 订阅文件生成器（支持Clash/Mihomo格式）
 *  3. 批量配置生成器
 *  4. 统计分析功能
 *  5. 配置筛选和翻页功能
 *  6. UUID信息管理功能（简化版）
 * 
 *  移动端适配优化版
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

  // 检查各种协议前缀 (新增对 socks:// 的支持，统一映射为 socks5)
  const protocolPatterns =[
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

function isValidUuid(uuid) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(uuid || '');
}

// =================================================================
//  DATABASE OPERATIONS (数据库操作)
// =================================================================

async function uuidExistsForManagement(uuid, env) {
  if (!uuid || !env.DB) return false;

  const configCount = await env.DB.prepare(
      'SELECT COUNT(*) as count FROM configs WHERE uuid = ?'
  ).bind(uuid).first();

  if ((configCount?.count || 0) > 0) return true;

  try {
      const tableCheck = await env.DB.prepare(
          "SELECT name FROM sqlite_master WHERE type='table' AND name='uuid_info'"
      ).first();

      if (!tableCheck) return false;

      const uuidInfo = await env.DB.prepare(
          'SELECT uuid FROM uuid_info WHERE uuid = ? LIMIT 1'
      ).bind(uuid).first();

      return !!uuidInfo;
  } catch (e) {
      console.error("检查UUID是否存在失败:", e.message);
      return false;
  }
}

// 获取配置列表（支持筛选、分页和全部获取）
async function fetchConfigsByUuidFromDB(uuid, env, options = {}) {
  const db = env.DB;
  if (!db) return { results:[], total: 0 };

  try {
      // 构建查询条件
      let conditions = ['uuid = ?'];
      const params = [uuid];

      if (options.protocol && options.protocol !== 'all') {
          conditions.push('protocol = ?');
          params.push(options.protocol);
      }

      if (options.domain_hosting && options.domain_hosting !== 'all') {
          conditions.push('domain_hosting = ?');
          params.push(options.domain_hosting);
      }

      const whereClause = conditions.length > 0 ? 'WHERE ' + conditions.join(' AND ') : '';

      // 获取总数
      const countQuery = `SELECT COUNT(*) as total FROM configs ${whereClause}`;
      const countResult = await db.prepare(countQuery).bind(...params).first();
      const total = countResult?.total || 0;

      // 构建查询
      let query = `SELECT id, config_data, protocol, remark, domain_hosting FROM configs ${whereClause} ORDER BY id ASC`;

      // 检查是否需要获取所有数据（用于订阅接口）
      if (options.getAll) {
          // 获取所有数据，不分页
          const { results } = await db.prepare(query).bind(...params).all();

          return {
              results,
              total,
              page: 1,
              pageSize: total,
              totalPages: 1
          };
      } else {
          // 获取分页数据
          const page = options.page || 1;
          const pageSize = options.pageSize || 10;
          const offset = (page - 1) * pageSize;

          query += ` LIMIT ? OFFSET ?`;
          params.push(pageSize, offset);

          const { results } = await db.prepare(query).bind(...params).all();

          return {
              results,
              total,
              page,
              pageSize,
              totalPages: Math.ceil(total / pageSize)
          };
      }

  } catch (e) {
      console.error(`Error fetching configs for UUID ${uuid}:`, e.message);
      return { results:[], total: 0, page: 1, pageSize: 10, totalPages: 0 };
  }
}

// 批量删除配置
async function deleteConfigsByIds(env, ids) {
  const db = env.DB;
  if (!db) throw new Error("D1 数据库未绑定。");

  if (!ids || ids.length === 0) {
    return { success: true, deleted: 0 };
  }

  try {
    const placeholders = ids.map(() => '?').join(',');
    const query = `DELETE FROM configs WHERE id IN (${placeholders})`;
    
    // D1 的 .run() 返回包含 meta 信息的对象
    const result = await db.prepare(query).bind(...ids).run();

    // 兼容不同的 D1 SDK 版本获取影响行数
    const deletedCount = result.meta?.changes ?? result.changes ?? 0;

    return {
      success: true,
      deleted: deletedCount
    };
  } catch (e) {
    console.error("批量删除配置失败:", e.message);
    return {
      success: false,
      error: e.message,
      deleted: 0
    };
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
          recent_logs: recentLogs?.results ||[]
      };

  } catch (e) {
      console.error("获取UUID访问统计失败:", e.message);
      return {
          success: false,
          error: "数据库查询失败: " + e.message
      };
  }
}

// 获取UUID信息（简化版，不返回时间字段）
async function fetchUuidInfoFromDB(uuid, env) {
  const db = env.DB;
  if (!db) return { success: false, error: "数据库未连接" };

  try {
      const tableCheck = await db.prepare(
          "SELECT name FROM sqlite_master WHERE type='table' AND name='uuid_info'"
      ).first();

      if (!tableCheck) {
          return {
              success: true,
              exists: false,
              message: "UUID信息表不存在，请确保已升级到v3.0.3+版本"
          };
      }

      // 只查询基础信息，不查询访问统计和时间字段
      const uuidInfo = await db.prepare(`
          SELECT uuid, owner, email, contact_info, status, remark
          FROM uuid_info
          WHERE uuid = ?
      `).bind(uuid).first();

      if (!uuidInfo) {
          return {
              success: true,
              exists: false,
              message: "UUID信息不存在"
          };
      }

      return {
          success: true,
          exists: true,
          data: uuidInfo
      };

  } catch (e) {
      console.error("获取UUID信息失败:", e.message);
      return {
          success: false,
          error: "数据库查询失败: " + e.message
      };
  }
}

// 创建或更新UUID信息（简化版，不更新时间字段）
async function upsertUuidInfoToDB(uuid, info, env) {
  const db = env.DB;
  if (!db) return { success: false, error: "数据库未连接" };

  try {
      // 检查表是否存在
      const tableCheck = await db.prepare(
          "SELECT name FROM sqlite_master WHERE type='table' AND name='uuid_info'"
      ).first();

      if (!tableCheck) {
          return {
              success: false,
              error: "UUID信息表不存在，请确保已升级到v3.0.3+版本"
          };
      }

      // 检查UUID是否存在
      const existingInfo = await db.prepare(
          "SELECT id FROM uuid_info WHERE uuid = ?"
      ).bind(uuid).first();

      if (existingInfo) {
          // 更新现有记录（不更新时间字段）
          const result = await db.prepare(`
              UPDATE uuid_info 
              SET owner = ?, email = ?, contact_info = ?, status = ?, remark = ?
              WHERE uuid = ?
          `).bind(
              info.owner,
              info.email,
              info.contact_info || '',
              info.status || 'active',
              info.remark || '',
              uuid
          ).run();

          return {
              success: true,
              action: 'updated',
              changes: result.changes || 0
          };
      } else {
          // 插入新记录（简化版，不插入时间字段）
          const result = await db.prepare(`
              INSERT INTO uuid_info 
              (uuid, owner, email, contact_info, status, remark)
              VALUES (?, ?, ?, ?, ?, ?)
          `).bind(
              uuid,
              info.owner,
              info.email,
              info.contact_info || '',
              info.status || 'active',
              info.remark || ''
          ).run();

          return {
              success: true,
              action: 'created',
              changes: result.changes || 0
          };
      }

  } catch (e) {
      console.error("创建或更新UUID信息失败:", e.message);
      return {
          success: false,
          error: "数据库操作失败: " + e.message
      };
  }
}

// 更新UUID访问统计（保留此函数用于其他功能）
async function updateUuidAccessStats(uuid, env) {
  const db = env.DB;
  if (!db) return { success: false, error: "数据库未连接" };

  try {
      // 检查表是否存在
      const tableCheck = await db.prepare(
          "SELECT name FROM sqlite_master WHERE type='table' AND name='uuid_info'"
      ).first();

      if (!tableCheck) {
          return { success: false, error: "UUID信息表不存在" };
      }

      const currentTime = Math.floor(Date.now() / 1000) * 1000;

      // 更新访问次数和最后访问时间
      const result = await db.prepare(`
          UPDATE uuid_info 
          SET last_accessed = ?, access_count = access_count + 1
          WHERE uuid = ?
      `).bind(currentTime, uuid).run();

      return {
          success: true,
          changes: result.changes || 0
      };

  } catch (e) {
      console.error("更新UUID访问统计失败:", e);
      return {
          success: false,
          error: "数据库操作失败: " + e.message
      };
  }
}

// 获取所有UUID信息列表（分页，简化版） - 保留给管理员使用
async function fetchAllUuidInfoFromDB(env, options = {}) {
  const db = env.DB;
  if (!db) return { results:[], total: 0 };

  try {
      const tableCheck = await db.prepare(
          "SELECT name FROM sqlite_master WHERE type='table' AND name='uuid_info'"
      ).first();

      if (!tableCheck) {
          return {
              results:[],
              total: 0,
              page: 1,
              pageSize: 10,
              totalPages: 0
          };
      }

      // 构建查询条件
      let conditions = [];
      const params =[];

      if (options.search) {
          conditions.push('(uuid LIKE ? OR owner LIKE ? OR email LIKE ?)');
          const searchTerm = `%${options.search}%`;
          params.push(searchTerm, searchTerm, searchTerm);
      }

      if (options.status && options.status !== 'all') {
          conditions.push('status = ?');
          params.push(options.status);
      }

      const whereClause = conditions.length > 0 ? 'WHERE ' + conditions.join(' AND ') : '';

      // 获取总数
      const countQuery = `SELECT COUNT(*) as total FROM uuid_info ${whereClause}`;
      const countResult = await db.prepare(countQuery).bind(...params).first();
      const total = countResult?.total || 0;

      // 构建查询（只查询基础信息）
      let query = `
          SELECT uuid, owner, email, contact_info, status, remark
          FROM uuid_info 
          ${whereClause}
          ORDER BY created_at DESC
      `;

      const page = options.page || 1;
      const pageSize = options.pageSize || 10;
      const offset = (page - 1) * pageSize;

      query += ` LIMIT ? OFFSET ?`;
      params.push(pageSize, offset);

      const { results } = await db.prepare(query).bind(...params).all();

      return {
          results,
          total,
          page,
          pageSize,
          totalPages: Math.ceil(total / pageSize)
      };

  } catch (e) {
      console.error("获取UUID信息列表失败:", e.message);
      return { results:[], total: 0, page: 1, pageSize: 10, totalPages: 0 };
  }
}

// 删除UUID信息
async function deleteUuidInfoFromDB(uuid, env) {
  const db = env.DB;
  if (!db) return { success: false, error: "数据库未连接" };

  try {
      const result = await db.prepare(
          "DELETE FROM uuid_info WHERE uuid = ?"
      ).bind(uuid).run();

      return {
          success: true,
          deleted: result.changes || 0
      };

  } catch (e) {
      console.error("删除UUID信息失败:", e.message);
      return {
          success: false,
          error: "数据库操作失败: " + e.message
      };
  }
}

// 获取 IP (仅读取 SELECT)
async function fetchIpsFromDB(ipType, carrierType, env) {
  const db = env.DB;
  if (!db) throw new Error("D1 数据库未绑定。");

  let query = 'SELECT DISTINCT ip FROM cfips WHERE 1=1';
  const params =[];

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
      return[];
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

      // 同时更新UUID访问统计
      await updateUuidAccessStats(uuid, env);
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
  const { uuid, config_data, domain_hosting = 'Cloudflare', allow_create_uuid = false } = body;
  if (!uuid || !config_data) return jsonResponse({ error: '字段缺失' }, 400);
  if (!isValidUuid(uuid)) return jsonResponse({ error: 'UUID格式无效' }, 400);

  const uuidExists = await uuidExistsForManagement(uuid, env);
  if (!uuidExists && !allow_create_uuid) {
      return jsonResponse({ error: 'UUID不存在，请通过生成按钮生成新的UUID后再添加配置' }, 404);
  }

  const validDomainHostings =[
      'Cloudflare', '阿里ESA', '腾讯Edgeone', 'AWS Cloudfront',
      'Gcore', 'Fastly', 'CacheFly', 'LightCDN', 'Vercel', 'Netlify',
      '无', '其他'
  ];
  const hostingValue = validDomainHostings.includes(domain_hosting) ? domain_hosting : 'Cloudflare';

  const lines = config_data.split('\n').map(l => l.trim()).filter(Boolean);
  const stmts =[];
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
try { 
    body = await request.json(); 
} catch (e) { 
    return jsonResponse({ error: '无效JSON' }, 400); 
}

const { id, config_data, domain_hosting = 'Cloudflare' } = body;
if (!id) return jsonResponse({ error: '缺少配置ID' }, 400);
if (!body.uuid) return jsonResponse({ error: '缺少UUID，无法校验编辑权限' }, 400);
if (!isValidUuid(body.uuid)) return jsonResponse({ error: 'UUID格式无效' }, 400);

const protocol = getProtocol(config_data);
if (protocol === 'unknown') return jsonResponse({ error: '不支持的配置格式' }, 400);
const remark = extractRemarkFromConfig(config_data, protocol);

const validDomainHostings =[
    'Cloudflare', '阿里ESA', '腾讯Edgeone', 'AWS Cloudfront',
    'Gcore', 'Fastly', 'CacheFly', 'LightCDN', 'Vercel', 'Netlify',
    '无', '其他'
];
const hostingValue = validDomainHostings.includes(domain_hosting) ? domain_hosting : 'Cloudflare';

try {
    // 强制转换 id 为数字，确保与数据库类型一致
    const targetId = parseInt(id);
    const existingConfig = await env.DB.prepare('SELECT id FROM configs WHERE id = ? AND uuid = ?')
        .bind(targetId, body.uuid)
        .first();

    if (!existingConfig) {
        return jsonResponse({ error: '配置不存在，或不属于当前UUID，禁止编辑' }, 404);
    }

    const res = await env.DB.prepare('UPDATE configs SET config_data = ?, protocol = ?, remark = ?, domain_hosting = ?, updated_at = ? WHERE id = ? AND uuid = ?')
        .bind(config_data, protocol, remark, hostingValue, Date.now(), targetId, body.uuid)
        .run();

    // 只要执行没有报错，就返回成功。
    // changes 为 0 通常是因为 ID 未找到或数据库认为数据无变化，但在管理后台应视为"处理完成"
    return jsonResponse({ 
        success: true, 
        message: res.changes > 0 ? 'Updated' : 'No changes made',
        rows_affected: res.changes 
    });
} catch (e) { 
    console.error("Update error:", e.message);
    return jsonResponse({ error: e.message }, 500); 
}
}


async function handleCheckUuid(uuid, env) {
  if (!uuid) return jsonResponse({ success: false, exists: false, error: 'UUID Required' }, 400);
  if (!isValidUuid(uuid)) {
      return jsonResponse({ success: true, exists: false, valid: false, error: 'UUID格式无效' }, 400);
  }

  const exists = await uuidExistsForManagement(uuid, env);
  return jsonResponse({
      success: true,
      valid: true,
      exists,
      message: exists ? 'UUID存在，可以进入配置管理' : 'UUID不存在，请通过生成按钮生成新的UUID'
  });
}

async function handleGetConfigs(uuid, env, url) {
  // 获取查询参数
  const urlObj = new URL(url);
  const protocol = urlObj.searchParams.get('protocol') || 'all';
  const domain_hosting = urlObj.searchParams.get('domain_hosting') || 'all';
  const page = parseInt(urlObj.searchParams.get('page') || '1');
  const pageSize = parseInt(urlObj.searchParams.get('pageSize') || '10');

  const result = await fetchConfigsByUuidFromDB(uuid, env, {
      protocol,
      domain_hosting,
      page,
      pageSize
  });

  return jsonResponse({
      uuid,
      configs: result.results,
      pagination: {
          page: result.page,
          pageSize: result.pageSize,
          total: result.total,
          totalPages: result.totalPages
      },
      filters: {
          protocol,
          domain_hosting
      }
  });
}

async function handleGetUuidStats(uuid, env, request) {
  if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);
  const urlObj = new URL(request.url);
  const days = urlObj.searchParams.get('days') || 30;
  const stats = await fetchUuidAccessStatsFromDB(uuid, env, parseInt(days));
  return jsonResponse(stats);
}

async function handleGetUuidInfo(uuid, env) {
  if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);
  const info = await fetchUuidInfoFromDB(uuid, env);
  return jsonResponse(info);
}

async function handlePostUuidInfo(request, env) {
  let body;
  try { body = await request.json(); } catch (e) { return jsonResponse({ error: '无效 JSON' }, 400); }

  const { uuid, owner, email, contact_info, status, remark } = body;

  if (!uuid || !owner || !email) {
      return jsonResponse({ error: '缺少必要字段: uuid, owner, email' }, 400);
  }

  const validStatuses =['active', 'inactive', 'suspended'];
  const finalStatus = validStatuses.includes(status) ? status : 'active';

  const info = {
      owner,
      email,
      contact_info: contact_info || '',
      status: finalStatus,
      remark: remark || ''
  };

  const result = await upsertUuidInfoToDB(uuid, info, env);
  return jsonResponse(result);
}

async function handleGetAllUuidInfo(env, url) {
  const urlObj = new URL(url);
  const page = parseInt(urlObj.searchParams.get('page') || '1');
  const pageSize = parseInt(urlObj.searchParams.get('pageSize') || '10');
  const search = urlObj.searchParams.get('search') || '';
  const status = urlObj.searchParams.get('status') || 'all';

  const result = await fetchAllUuidInfoFromDB(env, {
      page,
      pageSize,
      search,
      status
  });

  return jsonResponse({
      uuid_info: result.results,
      pagination: {
          page: result.page,
          pageSize: result.pageSize,
          total: result.total,
          totalPages: result.totalPages
      },
      filters: {
          search,
          status
      }
  });
}

async function handleDeleteUuidInfo(uuid, env) {
  if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);
  const result = await deleteUuidInfoFromDB(uuid, env);
  return jsonResponse(result);
}

async function handleDelete(type, value, env) {
  if (type === 'batch') {
    try {
      const ids = JSON.parse(value);
      if (!Array.isArray(ids) || ids.length === 0) {
        return jsonResponse({ error: '无效的ID列表' }, 400);
      }

      // 调用修复后的函数
      const result = await deleteConfigsByIds(env, ids);
      
      if (!result.success) {
        return jsonResponse({ error: result.error }, 500);
      }

      // 返回给前端，确保包含 deleted 字段
      return jsonResponse({ 
        success: true, 
        deleted: result.deleted, // 这里必须对应
        message: `成功删除 ${result.deleted} 条配置`
      });
    } catch (e) {
      return jsonResponse({ error: '无效的请求数据' }, 400);
    }
  } else {
    // 单个删除保持不变
    const sql = type === 'uuid' ? 'DELETE FROM configs WHERE uuid = ?' : 'DELETE FROM configs WHERE id = ?';
    await env.DB.prepare(sql).bind(value).run();
    return jsonResponse({ success: true });
  }
}

// 订阅输出 (Raw Base64)
async function handleRawSubscription(uuid, env) {
  if (!uuid) return jsonResponse({ error: 'UUID Required' }, 400);
  const configs = await fetchConfigsByUuidFromDB(uuid, env, { getAll: true });
  if (!configs.results || configs.results.length === 0) return new Response("UUID Not Found or Empty", { status: 404 });

  const configList = configs.results.map(c => c.config_data);
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
function replaceAddressesInConfigs(baseConfigsData, addressList, disableOptimization = false) {
  let generatedConfigs =[];
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

      if (disableOptimization) continue;

      // 只有 Cloudflare 的配置才进行优选替换
      if (domainHosting !== 'Cloudflare') continue;

      // 处理协议
      if (configType === 'trojan' || configType === 'vless' ||
          configType === 'shadowsocks' || configType === 'hysteria2' ||
          configType === 'tuic' || configType === 'anytls' ||
          configType === 'any-reality' || configType === 'reality' ||
          configType === 'socks5') {

          processedBaseConfig = baseConfig;
      } else if (configType === 'vmess') {
          const encodedJson = baseConfig.substring('vmess://'.length);
          try {
              processedBaseConfig = JSON.parse(b64_to_utf8(encodedJson));
          } catch (e) {
              pushError(`VMess配置解码失败`);
              continue;
          }
      } else {
          continue;
      }

      // 对Cloudflare配置进行优选替换
      for (const newAddr of addressList) {
          const cleanAddrForName = newAddr.replace(/[\[\]]/g, '');
          let formattedAddr = newAddr;
          if (newAddr.includes(':') && !newAddr.startsWith('[') && !newAddr.endsWith(']')) {
              formattedAddr = `[${newAddr}]`;
          }

          if (configType !== 'vmess') {
              try {
                  const url = new URL(processedBaseConfig);
                  const originalHost = url.hostname; // 关键：记录原始域名

                  // --- 修复逻辑开始 ---
                  // 1. 如果是 TLS 协议且没有 SNI，将原始域名填入 SNI
                  if (!url.searchParams.has('sni') && originalHost && !validAddressRegex.test(originalHost)) {
                      url.searchParams.set('sni', originalHost);
                  }
                  // 2. 如果是 WebSocket/HTTP 传输且没有 Host，将原始域名填入 Host
                  const netType = url.searchParams.get('type');
                  if ((netType === 'ws' || netType === 'h2' || netType === 'grpc' || netType === 'xhttp') && !url.searchParams.has('host')) {
                      url.searchParams.set('host', originalHost);
                  }
                  // --- 修复逻辑结束 ---

                  const originalName = url.hash ? decodeURIComponent(url.hash.substring(1)) : `${configType}-node`;
                  url.hash = encodeURIComponent(`${originalName}-${cleanAddrForName}`);
                  url.hostname = formattedAddr; // 只替换连接地址

                  generatedConfigs.push(url.toString());
              } catch (e) {
                  pushError(`处理 ${configType} 出错: ${e.message}`);
              }
          } else if (configType === 'vmess') {
              try {
                  const tempVmessObj = JSON.parse(JSON.stringify(processedBaseConfig));
                  const originalHost = tempVmessObj.add; // 关键：记录原始地址

                  // --- VMess 修复逻辑 ---
                  if (!tempVmessObj.sni && originalHost && !validAddressRegex.test(originalHost)) {
                      tempVmessObj.sni = originalHost;
                  }
                  if (!tempVmessObj.host && originalHost && !validAddressRegex.test(originalHost)) {
                      tempVmessObj.host = originalHost;
                  }
                  // --- VMess 修复逻辑结束 ---

                  const originalName = tempVmessObj.ps || tempVmessObj.remark || 'vmess-node';
                  tempVmessObj.ps = `${originalName}-${cleanAddrForName}`;
                  tempVmessObj.add = formattedAddr; // 只替换连接地址
                  
                  if (tempVmessObj.remark) delete tempVmessObj.remark;
                  generatedConfigs.push(`vmess://${utf8_to_b64(JSON.stringify(tempVmessObj))}`);
              } catch (e) {
                  pushError(`VMess编码失败: ${e.message}`);
              }
          }
      }
  }
  return [...new Set(generatedConfigs)];
}

// 手动 or UUID 生成 (POST)
async function generateConfigs(request, env) {
  try {
      const body = await request.json();
      const addressListText = body.addressList || body.ipList;
      const { baseConfigUuid, baseConfig: baseConfigFromRequest, disableOptimization = false } = body;

      let baseConfigsData =[];

      if (baseConfigUuid) {
          const configsFromDb = await fetchConfigsByUuidFromDB(baseConfigUuid, env, { getAll: true });
          // 修复：使用 configsFromDb.results 而不是 configsFromDb
          if (!configsFromDb.results || configsFromDb.results.length === 0) {
              return jsonResponse({ error: `未找到 UUID 为 "${baseConfigUuid}" 的任何基础配置。` }, 404);
          }
          baseConfigsData = configsFromDb.results;

          const hostingStats = {};
          configsFromDb.results.forEach(cfg => {
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

      let addressList =[];
      if (!disableOptimization) {
          // 只有不禁用优选时，才处理地址列表
          if (!addressListText || addressListText.trim() === '') {
              return jsonResponse({ error: "优选列表不能为空。" }, 400);
          }
          addressList = addressListText.split('\n').map(a => a.trim()).filter(a => a.length > 0);
      } else {
          addressList =[]; // 禁用优选，地址列表为空
      }

      const generatedConfigs = replaceAddressesInConfigs(baseConfigsData, addressList, disableOptimization);

      const totalConfigs = baseConfigsData.length;
      const cloudflareConfigs = baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length;
      const otherHostingCount = totalConfigs - cloudflareConfigs;

      const successCount = generatedConfigs.filter(c => !c.startsWith('[错误]')).length;
      const errorCount = generatedConfigs.length - successCount;

      const expectedNonCloudflare = otherHostingCount;
      const expectedCloudflare = cloudflareConfigs * (addressList.length + 1);
      const expectedTotal = expectedNonCloudflare + expectedCloudflare;

      let message = `生成完成！成功 ${successCount} 条`;
      if (disableOptimization) {
          message += ` (优选已禁用，只输出原配置)`;
      } else {
          message += `，预期输出 ${expectedTotal} 条`;
          if (errorCount > 0) message += `，失败 ${errorCount} 条`;
      }

      if (!disableOptimization && otherHostingCount > 0) {
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
              actual_output: successCount,
              disable_optimization: disableOptimization
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

  const configsFromDb = await fetchConfigsByUuidFromDB(uuid, env, { getAll: true });
  // 修复：使用 .results
  if (configsFromDb.results.length === 0) return jsonResponse({ error: `UUID Not Found` }, 404);
  const baseConfigsData = configsFromDb.results;

  const totalConfigs = baseConfigsData.length;
  const cloudflareConfigs = baseConfigsData.filter(cfg => cfg.domain_hosting === 'Cloudflare').length;
  const otherConfigs = totalConfigs - cloudflareConfigs;

  console.log(`订阅UUID ${uuid}: 总配置数${totalConfigs}, Cloudflare配置${cloudflareConfigs}, 其他域名托管配置${otherConfigs}`);

  const type = urlParams.get('type') || 'ip';
  const disableOptimization = urlParams.get('disable') === 'true';
  let addressList =[];

  if (!disableOptimization) {
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
  }

  const generatedConfigs = replaceAddressesInConfigs(baseConfigsData, addressList, disableOptimization);
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
          'X-Domain-Hosting-Filter': `Cloudflare=${cloudflareConfigs}, Other=${otherConfigs}`,
          'X-Disable-Optimization': disableOptimization ? 'true' : 'false'
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
              udp: true, // 显式开启 UDP
              'packet-encoding': 'packetaddr', // 设置包编码
              'global-padding': false,
              'authenticated-length': false,
              tls: vmessObj.tls === 'tls',
              'skip-cert-verify': true,
              network: vmessObj.net || 'tcp'
          };

          // 处理 TLS 相关高级字段
          if (clashConfig.tls) {
              clashConfig.servername = vmessObj.sni || vmessObj.host || vmessObj.add;
              clashConfig.alpn =['h2', 'http/1.1'];
              clashConfig.fingerprint = vmessObj.fp || ''; // 对应 fingerprint
              clashConfig['client-fingerprint'] = vmessObj.fp || 'chrome'; // 对应 client-fingerprint
              
              // 如果 vmess 包含 reality 属性 (部分自定义格式)
              if (vmessObj.security === 'reality' || vmessObj.pbk) {
                  clashConfig['reality-opts'] = {
                      'public-key': vmessObj.pbk || '',
                      'short-id': vmessObj.sid || ''
                  };
              }
          }

          // 处理传输层
          if (vmessObj.net === 'ws') {
              clashConfig['ws-opts'] = {
                  path: vmessObj.path || '/',
                  headers: vmessObj.host ? { Host: vmessObj.host } : {}
              };
          } else if (vmessObj.net === 'h2') {
              clashConfig['h2-opts'] = {
                  path: vmessObj.path || '/',
                  host: vmessObj.host ?[vmessObj.host] :[]
              };
          } else if (vmessObj.net === 'grpc') {
              clashConfig['grpc-opts'] = {
                  'grpc-service-name': vmessObj.path || ''
              };
          }

          // 添加 smux 配置
          clashConfig.smux = {
              enabled: false
          };

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
          udp: true, // 开启 UDP
          sni: url.searchParams.get('sni') || url.hostname,
          alpn: ['h2', 'http/1.1'], // 默认 ALPN
          'client-fingerprint': url.searchParams.get('fp') || 'chrome', // 对应 client-fingerprint
          fingerprint: url.searchParams.get('fp') || '', // 对应 fingerprint
          'skip-cert-verify': true,
          // 默认添加禁用状态的 ss-opts
          'ss-opts': {
              enabled: false,
              method: 'aes-128-gcm',
              password: 'example'
          },
          network: 'tcp', // 默认设为 tcp，后面会被 ws/grpc 覆盖
          smux: {
              enabled: false
          }
      };

      // 填充 Reality 特有配置 (针对支持 Reality 的 Trojan)
      if (url.searchParams.get('security') === 'reality' || url.searchParams.get('pbk')) {
          clashConfig['reality-opts'] = {
              'public-key': url.searchParams.get('pbk') || '',
              'short-id': url.searchParams.get('sid') || ''
          };
      }

      // 处理传输网络层
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
if (protocol === 'vless' || protocol === 'any-reality' || protocol === 'reality' || protocol === 'anyreality') {
  try {
      const url = new URL(configStr);
      const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `VLESS-${url.hostname}-${url.port}`;

      const security = url.searchParams.get('security');
      const isReality = security === 'reality';
      const isTls = security === 'tls' || isReality;

      const clashConfig = {
          name: name,
          type: 'vless',
          server: url.hostname,
          port: parseInt(url.port) || 443,
          uuid: url.username,
          udp: true,
          tls: isTls,
          'skip-cert-verify': true,
          servername: url.searchParams.get('sni') || url.hostname,
          flow: url.searchParams.get('flow') || '',
          'packet-encoding': 'xudp',
          encryption: url.searchParams.get('encryption') || 'none',
          'client-fingerprint': url.searchParams.get('fp') || 'chrome'
      };

      // 填充 Reality 特有配置
      if (isReality || url.searchParams.get('pbk')) {
          clashConfig['reality-opts'] = {
              'public-key': url.searchParams.get('pbk') || '',
              'short-id': url.searchParams.get('sid') || ''
          };
      }

      // 处理传输网络层 (新增 xhttp 支持)
      const net = url.searchParams.get('type') || 'tcp';
      clashConfig.network = net;

      if (net === 'ws') {
          clashConfig['ws-opts'] = {
              path: url.searchParams.get('path') || '/',
              headers: { Host: url.searchParams.get('host') || '' }
          };
      } else if (net === 'grpc') {
          clashConfig['grpc-opts'] = {
              'grpc-service-name': url.searchParams.get('serviceName') || url.searchParams.get('path') || ''
          };
      } else if (net === 'xhttp') { // --- 新增 xhttp 逻辑 ---
          clashConfig['xhttp-opts'] = {
              path: url.searchParams.get('path') || '/',
              mode: url.searchParams.get('mode') || 'auto',
              extra: {} 
          };
          const host = url.searchParams.get('host');
          if (host) clashConfig['xhttp-opts'].headers = { Host: host };
      } else if (net === 'h2') {
          clashConfig['h2-opts'] = {
              path: url.searchParams.get('path') || '/',
              host:[url.searchParams.get('host') || '']
          };
      }

      return clashConfig;
  } catch (error) {
      console.error('转换VLESS/Reality配置失败:', error, configStr);
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

      // 自动识别并解码 Base64 格式或明文格式的用户信息
      let userInfoRaw = url.username;
      try {
          // 部分 SS 链接将 method:password 整体进行 Base64 编码
          const decoded = atob(userInfoRaw);
          if (decoded.includes(':')) {
              userInfoRaw = decoded;
          }
      } catch (e) {
          // 解码失败说明可能已经是明文或采用了其他编码，保持原样
      }

      const userInfo = userInfoRaw.split(':');
      if (userInfo.length >= 2) {
          method = userInfo[0];
          password = userInfo.slice(1).join(':');
      }

      const clashConfig = {
          name: name,
          type: 'ss',
          server: url.hostname,
          port: parseInt(url.port) || 8388,
          cipher: method,
          password: password,
          udp: true, // 显式开启 UDP
          'udp-over-tcp': false, // 按照要求设为 false
          'udp-over-tcp-version': 2, // 按照要求设为 2
          'ip-version': 'ipv4', // 按照要求硬编码为 ipv4
          smux: {
              enabled: false
          }
      };

      // 处理插件 (Plugin) 信息
      const pluginParam = url.searchParams.get('plugin');
      if (pluginParam) {
          clashConfig.plugin = pluginParam.split(';')[0]; // 获取插件名
          
          // 处理插件选项 plugin-opts
          const pluginOptsRaw = url.searchParams.get('plugin-opts') || "";
          const opts = {};
          
          // 如果 URL 参数中包含 plugin-opts 字符串 (如 "mode=tls;host=example.com")
          if (pluginOptsRaw) {
              pluginOptsRaw.split(';').forEach(pair => {
                  const [k, v] = pair.split('=');
                  if (k) opts[k] = v || true;
              });
              clashConfig['plugin-opts'] = opts;
          } else if (pluginParam.includes(';')) {
              // 兼容另一种写法: plugin=obfs-local;obfs=tls
              const parts = pluginParam.split(';');
              parts.slice(1).forEach(pair => {
                  const[k, v] = pair.split('=');
                  if (k) opts[k] = v || true;
              });
              clashConfig['plugin-opts'] = opts;
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
          // 支持多端口范围，如果 URL 参数中没有 ports，则使用基础 port
          ports: url.searchParams.get('ports') || url.port,
          password: url.username,
          sni: url.searchParams.get('sni') || url.hostname,
          'skip-cert-verify': url.searchParams.get('insecure') === '1', // 根据参数决定，默认 false
          fingerprint: url.searchParams.get('fp') || '', // 从参数 fp 获取指纹
          alpn: (url.searchParams.get('alpn') || 'h3').split(',')
      };

      // 处理上行和下行速度，并自动补充 " Mbps" 单位
      const upMbps = url.searchParams.get('up') || url.searchParams.get('upspeed');
      const downMbps = url.searchParams.get('down') || url.searchParams.get('downspeed');
      
      if (upMbps) {
          clashConfig.up = upMbps.toLowerCase().includes('mbps') ? upMbps : `${upMbps} Mbps`;
      }
      if (downMbps) {
          clashConfig.down = downMbps.toLowerCase().includes('mbps') ? downMbps : `${downMbps} Mbps`;
      }

      // 处理混淆 obfs
      const obfsParam = url.searchParams.get('obfs');
      if (obfsParam && obfsParam !== 'none') {
          clashConfig.obfs = obfsParam;
          clashConfig['obfs-password'] = url.searchParams.get('obfs-password') || url.searchParams.get('obfsParam');
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
      
      // 提取凭据信息
      let uuid = url.username;
      let password = url.password || '';
      
      if (uuid.includes(':')) {
          const parts = uuid.split(':');
          uuid = parts[0];
          password = parts[1];
      } else if (uuid.includes('%3A')) {
          const decodedUser = decodeURIComponent(uuid);
          const parts = decodedUser.split(':');
          uuid = parts[0];
          password = parts[1];
      }

      const clashConfig = {
          name: name,
          type: 'tuic',
          server: url.hostname,
          port: parseInt(url.port) || 443,
          uuid: uuid,
          password: password,
          token: url.searchParams.get('token') || '', // 支持 token 字段
          sni: url.searchParams.get('sni') || url.hostname,
          'skip-cert-verify': url.searchParams.get('insecure') === '1',
          alpn: (url.searchParams.get('alpn') || 'h3').split(','),
          'disable-sni': url.searchParams.get('disable-sni') === 'true', // 新增
          'reduce-rtt': true, // 按照要求硬编码为 true
          'request-timeout': 8000, // 按照要求硬编码为 8000
          'udp-relay-mode': url.searchParams.get('udp-relay-mode') || 'native',
          'congestion-controller': url.searchParams.get('congestion_control') || url.searchParams.get('congestion-controller') || 'bbr'
      };

      // 额外处理 fast-open
      const fastOpen = url.searchParams.get('fast-open');
      if (fastOpen) {
          clashConfig['fast-open'] = fastOpen === 'true' || fastOpen === '1';
      }

      return clashConfig;
  } catch (error) {
      console.error('转换Tuic配置失败:', error);
      return null;
  }
}

// 处理AnyTLS配置
if (protocol === 'anytls') {
  try {
      const url = new URL(configStr);
      const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `AnyTLS-${url.hostname}-${url.port}`;
      
      const clashConfig = {
          name: name,
          type: 'anytls',
          server: url.hostname,
          port: parseInt(url.port) || 443,
          password: url.username, 
          'client-fingerprint': url.searchParams.get('fp') || 'chrome', // 默认 chrome
          udp: true,
          // 按照模板要求硬编码会话管理参数
          'idle-session-check-interval': 30,
          'idle-session-timeout': 30,
          'min-idle-session': 0,
          sni: url.searchParams.get('sni') || url.searchParams.get('host') || url.hostname,
          alpn: (url.searchParams.get('alpn') || 'h2,http/1.1').split(','),
          'skip-cert-verify': true // 按照模板要求默认开启
      };

      // 处理可能的传输层 (如果 AnyTLS 链接中携带了 ws 或 grpc)
      const typeParam = url.searchParams.get('type') || '';
      if (typeParam === 'ws') {
          clashConfig.network = 'ws';
          clashConfig['ws-opts'] = {
              path: url.searchParams.get('path') || '/',
              headers: { Host: url.searchParams.get('host') || clashConfig.sni }
          };
      } else if (typeParam === 'grpc') {
          clashConfig.network = 'grpc';
          clashConfig['grpc-opts'] = {
              'grpc-service-name': url.searchParams.get('serviceName') || url.searchParams.get('path') || ''
          };
      }

      return clashConfig;
  } catch (error) {
      console.error('转换AnyTLS配置失败:', error);
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

// 处理Socks5配置
if (protocol === 'socks5') {
  try {
      const url = new URL(configStr);
      const name = url.hash ? decodeURIComponent(url.hash.substring(1)) : `Socks5-${url.hostname}-${url.port}`;

      const clashConfig = {
          name: name,
          type: 'socks5',
          server: url.hostname,
          port: parseInt(url.port) || 1080,
          username: url.username || '',
          password: url.password || ''
      };

      return clashConfig;
  } catch (error) {
      console.error('转换Socks5配置失败:', error, configStr);
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
      const disableOptimization = url.searchParams.get('disable') === 'true';

      if (!uuid) {
          return jsonResponse({ error: '缺少必要参数: uuid' }, 400);
      }

      if (!template) {
          return jsonResponse({ error: '缺少必要参数: template' }, 400);
      }

      const configsFromDb = await fetchConfigsByUuidFromDB(uuid, env, { getAll: true });
      // 修复：使用 .results
      if (configsFromDb.results.length === 0) {
          return jsonResponse({ error: `未找到 UUID 为 "${uuid}" 的配置` }, 404);
      }
      const baseConfigsData = configsFromDb.results;

      let addressList =[];
      if (!disableOptimization) {
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
      }

      const generatedRawConfigs = replaceAddressesInConfigs(baseConfigsData, addressList, disableOptimization);
      const validRawConfigs = generatedRawConfigs.filter(c => !c.startsWith('[错误]'));

      if (validRawConfigs.length === 0) {
          return jsonResponse({ error: '未能生成有效的节点配置' }, 400);
      }

      let clashProxies =[];

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
              'X-Protocol-Counts': Object.entries(protocolStats).map(([k, v]) => `${k}:${v}`).join(','),
              'X-Disable-Optimization': disableOptimization ? 'true' : 'false'
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
      const uuidInfo = new URLPattern({ pathname: '/manage/uuid-info/:uuid' });
      const uuidCheck = new URLPattern({ pathname: '/manage/uuid-check/:uuid' });
      const allUuidInfo = new URLPattern({ pathname: '/manage/all-uuid-info' });

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
              if (uuidMatch) return await handleGetConfigs(uuidMatch.pathname.groups.uuid, env, url);

              // ===== API: UUID入口校验 =====
              const uuidCheckMatch = uuidCheck.exec(url);
              if (uuidCheckMatch) {
                  return await handleCheckUuid(uuidCheckMatch.pathname.groups.uuid, env);
              }

              // ===== API: UUID访问统计 =====
              const statsMatch = uuidStats.exec(url);
              if (statsMatch) {
                  return await handleGetUuidStats(statsMatch.pathname.groups.uuid, env, request);
              }

              // ===== API: UUID信息管理 =====
              const uuidInfoMatch = uuidInfo.exec(url);
              if (uuidInfoMatch) {
                  return await handleGetUuidInfo(uuidInfoMatch.pathname.groups.uuid, env);
              }

              // ===== API: 获取所有UUID信息列表 =====
              if (path === '/manage/all-uuid-info') {
                  return await handleGetAllUuidInfo(env, url);
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

              // ===== API: 创建或更新UUID信息 =====
              if (path === '/manage/uuid-info') return await handlePostUuidInfo(request, env);
          }

          if (method === 'PUT') {
              // ===== API: 配置更新 =====
              if (path === '/manage/configs') return await handleUpdateConfig(request, env);
          }

           if (method === 'DELETE') {
              // 1. 优先处理具体的批量删除路径 (最具体)
              if (path === '/manage/configs/batch') {
                  const body = await request.json();
                  const { ids } = body;
                  if (!ids || !Array.isArray(ids)) {
                      return jsonResponse({ error: '无效的请求数据，需要ids数组' }, 400);
                  }
                  // 直接调用批量删除逻辑
                  const result = await deleteConfigsByIds(env, ids);
                  return jsonResponse({ 
                    success: true, 
                    deleted: result.deleted, 
                    message: `成功删除 ${result.deleted} 条配置`
                  });
              }

              // 2. 处理按 ID 删除 (ID 匹配)
              const idMatch = manageID.exec(url);
              if (idMatch) return await handleDelete('id', idMatch.pathname.groups.id, env);

              // 3. 处理按 UUID 删除 (放在最后，因为它会拦截所有 /manage/configs/xxx)
              const uuidMatch = manageUUID.exec(url);
              if (uuidMatch) return await handleDelete('uuid', uuidMatch.pathname.groups.uuid, env);

              // 4. 处理删除 UUID 详细信息
              const deleteUuidInfoMatch = uuidInfo.exec(url);
              if (deleteUuidInfoMatch) {
                  return await handleDeleteUuidInfo(deleteUuidInfoMatch.pathname.groups.uuid, env);
              }
          }

          return new Response('404 Not Found', { status: 404 });
      } catch (err) {
          console.error("Worker error:", err);
          return new Response("Internal Error: " + err.message, { status: 500 });
      }
  }
};

// =================================================================
//  UNIFIED FRONTEND PAGE (整合的前端页面) - 移动端适配优化版
// =================================================================

const unifiedPageHtmlContent = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="mobile-web-app-capable" content="yes">
<meta name="theme-color" content="#ffffff">
<link rel="icon" href="https://s3.yangzifun.org/logo.ico" type="image/x-icon">
<title>代理配置管理工具</title>
<!-- Tailwind CSS -->
<script src="https://cdn.tailwindcss.com"></script>
<!-- Lucide Icons -->
<script src="https://unpkg.com/lucide@latest"></script>
<script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
<style>
  /* 移动端适配的CSS样式 */
  * {
    box-sizing: border-box;
    -webkit-tap-highlight-color: transparent;
  }

  /* 移动端触摸优化 */
  button, 
  input[type="button"], 
  input[type="submit"],
  a.btn-like {
    min-height: 44px;
    min-width: 44px;
    touch-action: manipulation;
  }

  /* 移动端表单优化 */
  input, 
  select, 
  textarea {
    font-size: 16px !important; /* 防止iOS缩放 */
    padding: 12px !important;
  }

  /* 移动端滚动优化 */
  .table-container {
    -webkit-overflow-scrolling: touch;
    overflow-x: auto;
  }

  /* 移动端模态框优化 */
  @media (max-width: 640px) {
    .modal-content {
      margin: 0;
      border-radius: 0;
      max-height: 100vh;
      height: 100vh;
      overflow-y: auto;
    }
  
    .modal-header {
      position: sticky;
      top: 0;
      z-index: 10;
      background: white;
    }
  
    .modal-body {
      padding-bottom: env(safe-area-inset-bottom);
    }
  }

  /* 移动端标签页优化 */
  .tab-btn {
    min-width: 80px;
    text-align: center;
    padding: 12px 16px;
  }

  /* 移动端卡片间距优化 */
  @media (max-width: 640px) {
    .mobile-card {
      margin-bottom: 12px;
      padding: 16px;
    }
  
    .mobile-padding {
      padding-left: 12px;
      padding-right: 12px;
    }
  }

  /* 移动端按钮组优化 */
  .mobile-btn-group {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  @media (min-width: 640px) {
    .mobile-btn-group {
      flex-direction: row;
    }
  }

  /* 安全区域适配 */
  .safe-area-top {
    padding-top: env(safe-area-inset-top);
  }

  .safe-area-bottom {
    padding-bottom: env(safe-area-inset-bottom);
  }

  /* 自定义样式补充 */
  .config-data-cell { 
    white-space: normal; 
    word-break: break-all; 
    max-width: 200px; 
  }

  .animate-slide-in {
    animation: slideIn 0.5s forwards;
  }

  @keyframes slideIn {
    from { transform: translateX(100%); opacity: 0; }
    to { transform: translateX(0); opacity: 1; }
  }

  .animate-fade-out {
    animation: fadeOut 0.5s forwards;
  }

  @keyframes fadeOut {
    from { opacity: 1; }
    to { opacity: 0; transform: translateX(100%); }
  }

  /* 域名托管徽章样式 */
  .hosting-cloudflare { background-color: #F6821F; color: white; }
  .hosting-aliyun { background-color: #FE6A00; color: white; }
  .hosting-tencent { background-color: #4A86FF; color: white; }
  .hosting-aws { background-color: #232B37; color: white; }
  .hosting-gcore { background-color: #FF4C00; color: white; }
  .hosting-fastly { background-color: #F7272C; color: white; }
  .hosting-cachefly { background-color: #391376; color: white; }
  .hosting-lightcdn { background-color: #F78B2C; color: white; }
  .hosting-vercel { background-color: #000; color: white; }
  .hosting-netlify { background-color: #05B7B5; color: white; }
  .hosting-none { background-color: #f8f9fa; color: #6c757d; }
  .hosting-other { background-color: #f0f0f0; color: #666; }

  /* UUID状态徽章 */
  .uuid-status-active { background-color: #10b981; color: white; }
  .uuid-status-inactive { background-color: #6b7280; color: white; }
  .uuid-status-suspended { background-color: #ef4444; color: white; }

  /* 访问类型徽章 */
  .type-subscription { background-color: #dbeafe; color: #1e40af; }
  .type-apigen { background-color: #d1fae5; color: #065f46; }

  /* 自定义单选按钮样式 */
  .radio-option {
    position: relative;
    padding: 12px 16px;
    background-color: #f3f4f6;
    border: 2px solid #d1d5db;
    border-radius: 0.375rem;
    color: #4b5563;
    cursor: pointer;
    transition: all 0.3s;
    min-height: 44px;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  @media (max-width: 640px) {
    .radio-option {
      padding: 10px 12px;
      font-size: 14px;
    }
  }

  .radio-option:hover {
    background-color: #e5e7eb;
  }

  .radio-option input[type="radio"] {
    position: absolute;
    opacity: 0;
    width: 0;
    height: 0;
  }

  .radio-option input[type="radio"]:checked + span {
    background-color: #6b7280;
    color: white;
    border-color: #6b7280;
  }

  /* 加载动画 */
  .loader {
    display: inline-block;
    width: 16px;
    height: 16px;
    border: 2px solid white;
    border-bottom-color: transparent;
    border-radius: 50%;
    animation: rotation 1s linear infinite;
    margin-right: 8px;
    vertical-align: middle;
  }

  @keyframes rotation {
    0% { transform: rotate(0deg); }
    100% { transform: rotate(360deg); }
  }

  /* 表格滚动条样式 */
  .table-container {
    overflow-x: auto;
    border: 2px solid #d1d5db;
    border-radius: 0.375rem;
  }

  .table-container::-webkit-scrollbar {
    height: 6px;
  }

  .table-container::-webkit-scrollbar-track {
    background: #f1f1f1;
    border-radius: 4px;
  }

  .table-container::-webkit-scrollbar-thumb {
    background: #c1c1c1;
    border-radius: 4px;
  }

  .table-container::-webkit-scrollbar-thumb:hover {
    background: #a8a8a8;
  }

  /* 移动端表格优化 */
  @media (max-width: 640px) {
    .mobile-table {
      font-size: 12px;
    }
  
    .mobile-table td,
    .mobile-table th {
      padding: 8px 4px;
    }
  }

  /* 移动端下拉菜单优化 */
  @media (max-width: 640px) {
    .mobile-dropdown {
      position: fixed;
      left: 0;
      right: 0;
      bottom: 0;
      top: auto;
      border-radius: 16px 16px 0 0;
      max-height: 70vh;
      overflow-y: auto;
    }
  }
</style>
</head>
<body class="bg-white min-h-screen font-sans text-gray-800">
<!-- 配置管理按钮和UUID下拉菜单 -->
<div id="configManagerBtn" class="fixed top-3 right-3 z-50 flex items-center gap-2">
  <!-- 配置管理按钮（未登录时显示） -->
  <button id="configManagerOpenBtn" class="flex items-center gap-1 px-3 py-2 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors shadow-sm text-sm">
    <i data-lucide="settings" class="w-4 h-4"></i>
    <span class="hidden sm:inline">配置管理</span>
  </button>

  <!-- UUID显示和下拉菜单（登录后显示） -->
  <div id="uuidDropdownContainer" class="hidden relative">
    <button id="uuidDropdownBtn" class="flex items-center gap-1 px-3 py-2 bg-teal-600 text-white rounded-lg border border-teal-600 hover:bg-teal-700 transition-colors shadow-sm font-mono text-sm">
      <span id="uuidDisplay" class="truncate max-w-[80px] sm:max-w-[120px]">UUID: ...</span>
      <i data-lucide="chevron-down" class="w-3 h-3 sm:w-4 sm:h-4"></i>
    </button>

    <div id="uuidDropdownMenu" class="hidden absolute right-0 top-full mt-1 bg-white border border-gray-300 rounded-lg shadow-lg min-w-[180px] z-50 mobile-dropdown">
      <div class="px-3 py-2 border-b border-gray-200 text-gray-700 text-sm font-mono break-all">
        UUID: <span id="dropdownUuid"></span>
      </div>
      <a href="javascript:void(0)" onclick="openUuidInfoModal()" class="flex items-center gap-2 px-3 py-2 text-gray-700 hover:bg-gray-100 transition-colors">
        <i data-lucide="user" class="w-4 h-4"></i>
        编辑UUID信息
      </a>
      <hr class="border-gray-200 mx-2">
      <a href="javascript:void(0)" onclick="logout()" class="flex items-center gap-2 px-3 py-2 text-red-600 hover:bg-red-50 transition-colors">
        <i data-lucide="log-out" class="w-4 h-4"></i>
        退出登录
      </a>
    </div>
  </div>
</div>

<!-- UUID信息管理模态框 -->
<div id="uuidInfoModal" class="hidden fixed inset-0 bg-black bg-opacity-50 z-50 flex items-center justify-center p-4 safe-area-top safe-area-bottom">
  <div class="bg-white rounded-xl shadow-2xl max-w-md w-full max-h-[90vh] overflow-y-auto modal-content">
    <div class="flex justify-between items-center border-b border-gray-200 px-4 py-3 sm:px-6 sm:py-4 modal-header">
      <h3 class="text-lg font-semibold text-gray-800">UUID信息管理</h3>
      <button onclick="closeUuidInfoModal()" class="text-gray-500 hover:text-gray-700 transition-colors p-1">
        <i data-lucide="x" class="w-5 h-5"></i>
      </button>
    </div>

    <div class="p-4 sm:p-6 modal-body">
      <div class="mb-4 bg-blue-50 border-l-4 border-blue-500 p-3 rounded-lg">
        <div class="flex items-start gap-2">
          <i data-lucide="info" class="w-5 h-5 text-blue-500 mt-0.5"></i>
          <p class="text-sm text-gray-700">
            <strong>注意：</strong> 在此处管理UUID的详细信息，包括所属人、联系方式、状态等。
            这些信息仅用于管理和统计，不会影响配置的正常使用。
          </p>
        </div>
      </div>

      <div id="uuidInfoPersonalContent">
        <div class="mb-4">
          <label class="block text-sm font-medium text-gray-700 mb-1">UUID</label>
          <input type="text" id="uuidInfoUuid" readonly 
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm focus:outline-none">
        </div>

        <div class="grid grid-cols-1 gap-4 mb-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">所属人姓名/标识 *</label>
            <input type="text" id="uuidInfoOwner" placeholder="请输入所属人姓名或标识"
                   class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">邮箱地址 *</label>
            <input type="email" id="uuidInfoEmail" placeholder="请输入邮箱地址"
                   class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
          </div>
        </div>

        <div class="mb-4">
          <label class="block text-sm font-medium text-gray-700 mb-1">其他联系信息</label>
          <input type="text" id="uuidInfoContact" placeholder="电话、微信等其他联系方式（可选）"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        </div>

        <div class="grid grid-cols-1 gap-4 mb-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">状态</label>
            <select id="uuidInfoStatus" 
                    class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
              <option value="active">活跃 (active)</option>
              <option value="inactive">停用 (inactive)</option>
              <option value="suspended">暂停 (suspended)</option>
            </select>
          </div>
        </div>

        <div class="mb-6">
          <label class="block text-sm font-medium text-gray-700 mb-1">备注信息</label>
          <textarea id="uuidInfoRemark" rows="3" placeholder="可在此添加备注信息..."
                    class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none resize-y"></textarea>
        </div>

        <div class="flex justify-end gap-3 mobile-btn-group">
          <button onclick="closeUuidInfoModal()" 
                  class="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors">
            取消
          </button>
          <button id="uuidInfoSaveBtn" onclick="saveUuidInfo()"
                  class="flex-1 px-4 py-2 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors">
            保存信息
          </button>
        </div>
      </div>
    </div>
  </div>
</div>

<!-- 配置管理入口模态框 -->
<div id="configManagerModal" class="hidden fixed inset-0 bg-black bg-opacity-50 z-50 flex items-center justify-center p-4 safe-area-top safe-area-bottom">
  <div class="bg-white rounded-xl shadow-2xl max-w-md w-full modal-content">
    <div class="flex justify-between items-center border-b border-gray-200 px-4 py-3 sm:px-6 sm:py-4 modal-header">
      <h3 class="text-lg font-semibold text-gray-800">配置管理入口</h3>
      <button onclick="closeConfigManagerModal()" class="text-gray-500 hover:text-gray-700 transition-colors p-1">
        <i data-lucide="x" class="w-5 h-5"></i>
      </button>
    </div>

    <div class="p-4 sm:p-6 modal-body">
      <div class="mb-4">
        <label for="configUuidInput" class="block text-sm font-medium text-gray-700 mb-1">
          请输入您的 UUID：
        </label>
        <input type="text" id="configUuidInput" placeholder="输入UUID以管理配置"
               class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        <div class="mt-2 bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
          <p class="text-sm text-gray-700">
            已存在的 UUID 通过校验后才可进入配置管理。
            <br>如果您没有 UUID，请点击下方生成按钮创建新的配置分组。
          </p>
        </div>
      </div>

      <div class="flex justify-end gap-3 mobile-btn-group flex-wrap">
        <button onclick="generateNewUuid()"
                class="flex-1 px-4 py-2 border border-gray-700 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors">
          生成UUID
        </button>
        <button onclick="closeConfigManagerModal()"
                class="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors">
          取消
        </button>
        <button onclick="enterConfigManagement()"
                class="flex-1 px-4 py-2 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors">
          进入配置管理
        </button>
      </div>
    </div>
  </div>
</div>

<!-- Toast 容器 -->
<div id="toast-container" class="fixed top-3 right-3 z-50 flex flex-col gap-2 max-w-[90vw]"></div>

<!-- 编辑配置模态框 -->
<div id="editModalOverlay" class="hidden fixed inset-0 bg-black bg-opacity-50 z-50 flex items-center justify-center p-4 safe-area-top safe-area-bottom">
  <div class="bg-white rounded-xl shadow-2xl max-w-2xl w-full max-h-[90vh] overflow-y-auto modal-content">
    <div class="flex justify-between items-center border-b border-gray-200 px-4 py-3 sm:px-6 sm:py-4 modal-header">
      <h3 class="text-lg font-semibold text-gray-800">编辑配置 (Editor)</h3>
      <button onclick="closeEditModal()" class="text-gray-500 hover:text-gray-700 transition-colors p-1">
        <i data-lucide="x" class="w-5 h-5"></i>
      </button>
    </div>

    <div class="p-4 sm:p-6 modal-body">
      <input type="hidden" id="edit-id">
      <input type="hidden" id="edit-protocol">

      <div class="grid grid-cols-1 gap-4 mb-4">
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">别名 (Remarks)</label>
          <input type="text" id="edit-ps"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">端口 (Port)</label>
          <input type="number" id="edit-port"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">域名托管 (Hosting)</label>
          <select id="edit-domain-hosting"
                  class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
            <option value="Cloudflare">Cloudflare</option>
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
        </div>
      </div>

      <div class="mb-4">
        <label class="block text-sm font-medium text-gray-700 mb-1">地址 (Address)</label>
        <input type="text" id="edit-add"
               class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
      </div>

      <div class="mb-4">
        <label class="block text-sm font-medium text-gray-700 mb-1">UUID / Password</label>
        <input type="text" id="edit-id-uuid"
               class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
      </div>

      <div class="grid grid-cols-1 gap-4 mb-4">
        <div>
  <label class="block text-sm font-medium text-gray-700 mb-1">传输 (Net)</label>
  <select id="edit-net"
          class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
    <option value="tcp">TCP</option>
    <option value="ws">WebSocket</option>
    <option value="grpc">gRPC</option>
    <option value="xhttp">xhttp</option> <!-- 新增 -->
    <option value="h2">HTTP/2</option>
  </select>
</div>
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">伪装 (Type)</label>
          <input type="text" id="edit-type" placeholder="none"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        </div>
      </div>

      <div class="grid grid-cols-1 gap-4 mb-4">
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">伪装域名 (Host)</label>
          <input type="text" id="edit-host"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">路径 (Path)</label>
          <input type="text" id="edit-path"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        </div>
      </div>

      <div class="grid grid-cols-1 gap-4 mb-6">
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">TLS</label>
          <select id="edit-tls"
                  class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
            <option value="">关闭</option>
            <option value="tls">开启 TLS</option>
          </select>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">SNI</label>
          <input type="text" id="edit-sni"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
        </div>
      </div>

      <div class="flex justify-end gap-3 mobile-btn-group">
        <button onclick="closeEditModal()"
                class="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors">
          取消
        </button>
        <button onclick="saveEditedConfig()"
                class="flex-1 px-4 py-2 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors">
          保存修改
        </button>
      </div>
    </div>
  </div>
</div>

<!-- 主内容容器 -->
<div class="min-h-screen flex flex-col items-center justify-center px-3 py-6 sm:px-6 lg:px-8 mobile-padding safe-area-top safe-area-bottom">
  <div class="w-full max-w-6xl">
    <!-- 标题区域 -->
    <div class="text-center mb-6">
      <h1 class="text-2xl sm:text-3xl lg:text-4xl font-bold text-gray-800 mb-2">代理配置管理工具</h1>
      <p class="text-gray-600 text-base sm:text-lg">一站式代理配置管理、生成与订阅服务</p>
    </div>

    <!-- Tab 导航 -->
    <div class="flex border-b border-gray-200 mb-6 overflow-x-auto">
      <button onclick="switchTab('batch')" 
              class="tab-btn px-4 py-3 font-medium text-gray-600 border-b-2 border-transparent hover:text-gray-800 transition-colors data-[active=true]:text-gray-800 data-[active=true]:border-gray-800 whitespace-nowrap"
              data-active="true">
        配置生成
      </button>
      <button onclick="switchTab('clash')" 
              class="tab-btn px-4 py-3 font-medium text-gray-600 border-b-2 border-transparent hover:text-gray-800 transition-colors data-[active=false]:text-gray-600 whitespace-nowrap"
              data-active="false">
        订阅生成
      </button>
      <button id="manageTabBtn" onclick="switchTab('manage')" 
              class="tab-btn px-4 py-3 font-medium text-gray-600 border-b-2 border-transparent hover:text-gray-800 transition-colors hidden whitespace-nowrap"
              data-active="false">
        配置管理
      </button>
    </div>

    <!-- 配置生成 Tab -->
    <div id="batch-tab" class="tab-content space-y-4 sm:space-y-6">
      <!-- 基础配置卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="settings" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">1. 基础配置</h2>
        </div>

        <div class="mb-4 bg-blue-50 border-l-4 border-blue-500 p-3 sm:p-4 rounded-lg">
          <div class="flex items-start gap-2">
            <i data-lucide="info" class="w-5 h-5 text-blue-500 mt-0.5"></i>
            <p class="text-sm text-gray-700">
              <strong>注意：</strong> 所有配置都会输出原配置。
              <br>域名托管属性为"Cloudflare"的配置会进行优选替换（每条原配置 + 优选地址数条新配置）。
              <br>其他域名托管配置（如阿里ESA、腾讯Edgeone、AWS Cloudfront、Gcore、Fastly等）只输出原配置，不进行优选替换。
            </p>
          </div>
        </div>

        <div class="flex flex-wrap gap-2 sm:gap-3 mb-4">
          <label class="radio-option">
            <input type="radio" name="genConfigSource" value="manual" checked>
            <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">手动粘贴</span>
          </label>
          <label class="radio-option">
            <input type="radio" name="genConfigSource" value="uuid">
            <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">从UUID获取</span>
          </label>
        </div>

        <div id="genManualConfigInput">
          <textarea id="genBaseConfigInput" placeholder="在此粘贴一个或多个基础配置，每行一个。"
                    class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none font-mono text-sm resize-y" rows="4"></textarea>
        </div>

        <div id="genUuidConfigInput" class="hidden">
          <input type="text" id="genBaseConfigUuidInput" placeholder="UUID将自动填充"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm mb-3 focus:outline-none">
          <div class="bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
            <p class="text-sm text-gray-700">
              在"配置管理"标签页添加和管理UUID。
              <br>所有配置都会输出原配置，只有域名托管属性为"Cloudflare"的配置会进行优选替换。
            </p>
          </div>
        </div>
      </div>

      <!-- 优选列表卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="list" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">2. 优选列表 (IP/域名)</h2>
        </div>

        <div class="mb-4">
          <label class="block text-sm font-medium text-gray-700 mb-2">地址类型</label>
          <div class="flex flex-wrap gap-2 sm:gap-3">
            <label class="radio-option">
              <input type="radio" name="genAddressType" value="ip" checked onchange="toggleBatchAddressOptions()">
              <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">IP 地址</span>
            </label>
            <label class="radio-option">
              <input type="radio" name="genAddressType" value="domain" onchange="toggleBatchAddressOptions()">
              <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">优选域名</span>
            </label>
            <label class="radio-option">
              <input type="radio" name="genAddressType" value="disable" onchange="toggleBatchAddressOptions()">
              <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">禁用优选</span>
            </label>
          </div>
        </div>

        <div id="batchIpOptionsContainer">
          <div class="mb-4">
            <label class="block text-sm font-medium text-gray-700 mb-2">IP 类型</label>
            <div class="flex flex-wrap gap-2 sm:gap-3">
              <label class="radio-option">
                <input type="radio" name="genIpType" value="all" checked>
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">全部</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="genIpType" value="v4">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">仅IPv4</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="genIpType" value="v6">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">仅IPv6</span>
              </label>
            </div>
          </div>
  
          <div class="mb-4 sm:mb-6">
            <label class="block text-sm font-medium text-gray-700 mb-2">运营商</label>
            <div class="flex flex-wrap gap-2 sm:gap-3">
              <label class="radio-option">
                <input type="radio" name="genCarrierType" value="all" checked>
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">全部</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="genCarrierType" value="CM">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">移动</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="genCarrierType" value="CU">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">联通</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="genCarrierType" value="CT">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">电信</span>
              </label>
            </div>
          </div>
        </div>

        <div id="batchDomainInfoBox" class="hidden mb-4 bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
          <p class="text-sm text-gray-700">
            将使用数据库中存储的 Cloudflare 优选域名列表进行替换。
          </p>
        </div>

        <div id="batchDisableInfoBox" class="hidden mb-4 bg-amber-50 border-l-4 border-amber-500 p-3 rounded-lg">
          <p class="text-sm text-gray-700">
            <strong>优选已禁用</strong>：将只输出原配置，不进行优选替换。
          </p>
        </div>

        <button id="genFetchBtn" onclick="genFetchAddresses()"
                class="w-full mb-4 px-4 py-3 bg-gray-100 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
          获取优选列表
        </button>

        <div id="batchSubscriptionLinkBox" class="hidden mb-4">
          <div class="flex flex-col sm:flex-row gap-2">
            <input type="text" id="batchSubscriptionLink" readonly
                   class="flex-1 px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm focus:outline-none">
            <button onclick="copyBatchSubscriptionLink()"
                    class="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
              复制
            </button>
          </div>
        </div>

        <textarea id="genAddressListInput" placeholder="点击上方按钮获取，或在此手动粘贴 IP 或 域名 列表..."
                  class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none font-mono text-sm resize-y" rows="4"></textarea>
      </div>

      <!-- 生成结果卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="package" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">3. 生成结果</h2>
        </div>

        <div id="batchGeneratedLinkBox" class="hidden mb-4">
          <div class="flex flex-col sm:flex-row items-center gap-2 mb-2">
            <span class="text-sm font-medium text-gray-700">订阅:</span>
            <div class="w-full sm:flex-1 flex flex-col sm:flex-row gap-2">
              <input type="text" id="batchGeneratedConfigLink" readonly
                     class="flex-1 px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm focus:outline-none">
              <button onclick="copyBatchGeneratedLink()"
                      class="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
                复制
              </button>
            </div>
          </div>
        </div>

        <textarea id="genResultTextarea" readonly placeholder="点击下方'生成配置'按钮..."
                  class="w-full px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm resize-y focus:outline-none" rows="6"></textarea>

        <div id="batchQrCodeContainer" class="hidden mt-4 p-4 bg-white border border-gray-200 rounded-lg shadow-sm">
          <label class="block text-sm font-medium text-gray-700 mb-2">订阅链接二维码</label>
          <div class="text-center mb-3">
            <img id="batchQrCodeImage" src="" alt="订阅链接二维码" class="inline-block border border-gray-300 rounded-lg max-w-[150px] sm:max-w-[200px]">
          </div>
          <div class="flex flex-wrap justify-center gap-2">
            <button onclick="downloadBatchQRCode()"
                    class="px-3 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 text-sm">
              下载二维码
            </button>
            <button onclick="shareBatchQRCode()"
                    class="px-3 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 text-sm">
              分享二维码
            </button>
            <button onclick="refreshBatchQRCode()"
                    class="px-3 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 text-sm">
              刷新
            </button>
          </div>
          <p class="text-xs text-gray-500 text-center mt-3">
            扫描二维码即可导入订阅配置。二维码内容为上方订阅链接。
          </p>
        </div>
      </div>

      <!-- 操作按钮 -->
      <div class="flex flex-col sm:flex-row gap-3 mobile-btn-group">
        <button id="genGenerateButton" onclick="genGenerateConfigs()"
                class="flex-1 px-4 py-3 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
          生成配置
        </button>
        <button id="copyResultButton" onclick="copyResults()"
                class="flex-1 px-4 py-3 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
          复制结果 (Base64)
        </button>
      </div>
    </div>

    <!-- 订阅生成 Tab -->
    <div id="clash-tab" class="tab-content hidden space-y-4 sm:space-y-6">
      <!-- 配置信息卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="settings" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">1. 配置信息</h2>
        </div>

        <div class="mb-4 bg-blue-50 border-l-4 border-blue-500 p-3 sm:p-4 rounded-lg">
          <div class="flex items-start gap-2">
            <i data-lucide="info" class="w-5 h-5 text-blue-500 mt-0.5"></i>
            <p class="text-sm text-gray-700">
              <strong>注意：</strong> 所有配置都会输出原配置。
              <br>域名托管属性为"Cloudflare"的配置会进行优选替换（每条原配置 + 优选地址数条新配置）。
              <br>其他域名托管配置（如阿里ESA、腾讯Edgeone、AWS Cloudfront、Gcore、Fastly等）只输出原配置，不进行优选替换。
            </p>
          </div>
        </div>

        <div class="mb-4 sm:mb-6">
          <input type="text" id="clashConfigUuid" placeholder="UUID将自动填充"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm mb-3 focus:outline-none">
          <div class="bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
            <p class="text-sm text-gray-700">
              在"配置管理"标签页添加和管理配置，获取UUID后在此输入。
              <br>如果您已登录，UUID将自动填充。
            </p>
          </div>
        </div>

        <div class="p-3 sm:p-4 bg-blue-50 border border-blue-200 rounded-lg shadow-sm">
          <div class="flex items-center gap-2 mb-3">
            <i data-lucide="shield" class="w-5 h-5 text-blue-600"></i>
            <h3 class="font-semibold text-gray-800">支持的协议类型</h3>
          </div>
          <div class="flex flex-wrap gap-1 sm:gap-2 mb-3">
            <span class="px-2 py-1 bg-blue-100 text-blue-700 border border-blue-200 rounded-full text-xs sm:text-sm">VMess</span>
            <span class="px-2 py-1 bg-blue-100 text-blue-700 border border-blue-200 rounded-full text-xs sm:text-sm">VLESS</span>
            <span class="px-2 py-1 bg-blue-100 text-blue-700 border border-blue-200 rounded-full text-xs sm:text-sm">Trojan</span>
            <span class="px-2 py-1 bg-blue-100 text-blue-700 border border-blue-200 rounded-full text-xs sm:text-sm">Shadowsocks</span>
            <span class="px-2 py-1 bg-blue-100 text-blue-700 border border-blue-200 rounded-full text-xs sm:text-sm">Socks/Socks5</span>
            <span class="px-2 py-1 bg-orange-100 text-orange-700 border border-orange-200 rounded-full text-xs sm:text-sm">Hysteria2</span>
            <span class="px-2 py-1 bg-orange-100 text-orange-700 border border-orange-200 rounded-full text-xs sm:text-sm">Tuic</span>
            <span class="px-2 py-1 bg-orange-100 text-orange-700 border border-orange-200 rounded-full text-xs sm:text-sm">AnyTLS</span>
            <span class="px-2 py-1 bg-orange-100 text-orange-700 border border-orange-200 rounded-full text-xs sm:text-sm">Any-reality</span>
            <span class="px-2 py-1 bg-orange-100 text-orange-700 border border-orange-200 rounded-full text-xs sm:text-sm">Reality</span>
          </div>
          <p class="text-sm text-gray-600">
            现在支持更多现代协议，包括高速传输协议和最新的加密技术。
          </p>
        </div>
      </div>

      <!-- 模板选择卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="file-text" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">2. 模板选择</h2>
        </div>

        <div class="mb-4">
          <label class="block text-sm font-medium text-gray-700 mb-2">选择配置文件模板</label>
          <div class="template-select-container">
            <select id="templateSelect" onchange="handleTemplateChange()"
                    class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none mb-3">
              <option value="">正在加载模板...</option>
            </select>
            <div id="templateDescription" class="text-sm text-gray-600">
              请从下拉列表中选择一个配置文件模板
            </div>
          </div>
        </div>

        <div id="customTemplateInput" class="hidden mb-4">
          <input type="text" id="customTemplateUrl" placeholder="输入自定义模板URL，如: https://raw.githubusercontent.com/your-repo/config.yaml"
                 class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none">
          <div class="mt-2 bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
            <p class="text-sm text-gray-700">
              请输入可公开访问的YAML配置文件的完整URL地址。
            </p>
          </div>
        </div>

        <div id="templateLoading" class="hidden text-center py-6 sm:py-8">
          <div class="inline-block w-8 h-8 border-2 border-gray-300 border-t-gray-600 rounded-full animate-spin mb-3"></div>
          <p class="text-gray-600">正在加载模板列表...</p>
        </div>

        <div id="templateError" class="hidden mb-4 bg-red-50 border-l-4 border-red-500 p-3 sm:p-4 rounded-lg">
          <p class="text-sm text-red-700">
            无法加载模板列表，请稍后重试。
          </p>
        </div>

        <div class="bg-gray-50 border-l-4 border-gray-500 p-3 sm:p-4 rounded-lg">
          <p class="text-sm text-gray-700">
            <strong>模板说明：</strong>
            <br>• 所有模板均从数据库读取
            <br>• 模板包含完整的规则和策略组配置
            <br>• 选择不同的模板可以获得不同的分流规则和优化策略
            <br>• <a href="https://clash-builder.yangzifun.org/" target="_blank" class="text-gray-700 hover:text-gray-900 underline">在线模板生成</a>
            <br><br><strong>注意：</strong>如果数据库中没有配置模板，将无法生成配置文件。
          </p>
        </div>
      </div>

      <!-- 优选地址配置卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="globe" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">3. 优选地址配置</h2>
        </div>

        <div class="mb-4">
          <label class="block text-sm font-medium text-gray-700 mb-2">地址类型</label>
          <div class="flex flex-wrap gap-2 sm:gap-3">
            <label class="radio-option">
              <input type="radio" name="clashAddressType" value="ip" checked onchange="toggleClashAddressOptions()">
              <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">IP 地址</span>
            </label>
            <label class="radio-option">
              <input type="radio" name="clashAddressType" value="domain" onchange="toggleClashAddressOptions()">
              <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">优选域名</span>
            </label>
            <label class="radio-option">
              <input type="radio" name="clashAddressType" value="disable" onchange="toggleClashAddressOptions()">
              <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">禁用优选</span>
            </label>
          </div>
        </div>

        <div id="clashIpOptionsContainer">
          <div class="mb-4">
            <label class="block text-sm font-medium text-gray-700 mb-2">IP 类型</label>
            <div class="flex flex-wrap gap-2 sm:gap-3">
              <label class="radio-option">
                <input type="radio" name="clashIpType" value="all" checked>
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">全部</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="clashIpType" value="v4">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">仅IPv4</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="clashIpType" value="v6">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">仅IPv6</span>
              </label>
            </div>
          </div>
  
          <div class="mb-4">
            <label class="block text-sm font-medium text-gray-700 mb-2">运营商</label>
            <div class="flex flex-wrap gap-2 sm:gap-3">
              <label class="radio-option">
                <input type="radio" name="clashCarrier" value="all" checked>
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">全部</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="clashCarrier" value="CM">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">移动</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="clashCarrier" value="CU">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">联通</span>
              </label>
              <label class="radio-option">
                <input type="radio" name="clashCarrier" value="CT">
                <span class="px-3 sm:px-4 py-2 rounded-lg text-sm sm:text-base">电信</span>
              </label>
            </div>
          </div>
        </div>

        <div id="clashDomainInfoBox" class="hidden bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
          <p class="text-sm text-gray-700">
            将使用数据库中存储的 Cloudflare 优选域名列表进行替换。
          </p>
        </div>

        <div id="clashDisableInfoBox" class="hidden bg-amber-50 border-l-4 border-amber-500 p-3 rounded-lg">
          <p class="text-sm text-gray-700">
            <strong>优选已禁用</strong>：将只输出原配置，不进行优选替换。
          </p>
        </div>
      </div>

      <!-- 生成结果卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="package" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">4. 生成结果</h2>
        </div>

        <div class="mb-4 sm:mb-6">
          <button id="generateClashBtn" onclick="generateClashConfig()"
                  class="w-full px-4 py-3 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 disabled:opacity-50 disabled:cursor-not-allowed"
                  disabled>
            生成Clash订阅文件
          </button>
        </div>

        <div class="subscription-link-box hidden p-3 sm:p-4 bg-white border border-gray-200 rounded-lg shadow-sm" id="clashSubscriptionLinkBox">
          <label class="block text-sm font-medium text-gray-700 mb-2">订阅链接</label>
          <div class="flex flex-col sm:flex-row gap-2 mb-4">
            <input type="text" id="clashGeneratedLink" readonly
                   class="flex-1 px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm focus:outline-none">
            <button onclick="copyClashGeneratedLink()"
                    class="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 whitespace-nowrap">
              复制
            </button>
          </div>

          <div id="clashQrCodeContainer" class="hidden mt-4 p-3 sm:p-4 bg-gray-50 border border-gray-200 rounded-lg shadow-sm">
            <label class="block text-sm font-medium text-gray-700 mb-2">订阅链接二维码</label>
            <div class="text-center mb-3">
              <img id="clashQrCodeImage" src="" alt="订阅链接二维码" class="inline-block border border-gray-300 rounded-lg max-w-[150px] sm:max-w-[200px]">
            </div>
            <div class="flex flex-wrap justify-center gap-2">
              <button onclick="downloadClashQRCode()"
                      class="px-3 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 text-sm">
                下载二维码
              </button>
              <button onclick="shareClashQRCode()"
                      class="px-3 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 text-sm">
                分享
              </button>
              <button onclick="refreshClashQRCode()"
                      class="px-3 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 text-sm">
                刷新
              </button>
            </div>
            <p class="text-xs text-gray-500 text-center mt-3">
              扫描二维码即可导入Clash配置文件到客户端。二维码内容为上方订阅链接。
            </p>
          </div>
        </div>

        <div class="bg-blue-50 border-l-4 border-blue-500 p-3 sm:p-4 rounded-lg">
          <p class="text-sm text-gray-700">
            <strong>新特性：</strong> 现在支持更多现代协议，包括Hysteria2、Tuic、AnyTLS、Any-reality等。
            <br>配置文件基于您选择的模板生成，包含完整的规则和策略组。
            <br>生成的订阅链接可直接在Clash/Mihomo客户端中使用。
          </p>
        </div>
      </div>
    </div>

    <!-- 配置管理 Tab -->
    <div id="manage-tab" class="tab-content hidden space-y-4 sm:space-y-6">
      <!-- 当前UUID卡片 -->
      <div class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="key" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">1. 当前UUID</h2>
        </div>

        <div class="flex flex-col sm:flex-row gap-3 mb-4">
          <input type="text" id="queryUuidInput" placeholder="UUID将自动加载" readonly
                 class="flex-1 px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm focus:outline-none">
          <div class="flex flex-col sm:flex-row gap-2 w-full sm:w-auto">
            <button id="queryBtn" onclick="manageQueryByUuid()"
                    class="flex-1 px-4 py-3 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
              刷新配置
            </button>
            <button onclick="openConfigManagerModal()"
                    class="flex-1 px-4 py-3 bg-amber-500 text-white rounded-lg border border-amber-500 hover:bg-amber-600 transition-colors focus:outline-none focus:ring-2 focus:ring-amber-500">
              切换UUID
            </button>
          </div>
        </div>

        <div id="manageSubLinkDisplay" class="hidden mt-4">
          <div class="flex flex-col sm:flex-row gap-2">
            <input type="text" id="manageSubUrlLink" readonly
                   class="flex-1 px-3 py-2 border border-gray-300 rounded-lg bg-gray-50 font-mono text-sm focus:outline-none">
            <button onclick="copyManageSubLink()"
                    class="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 whitespace-nowrap">
              复制
            </button>
          </div>
        </div>

        <div class="mt-4 bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
          <p class="text-sm text-gray-700">
            <strong>提示：</strong> UUID已自动保存。您可以在其他标签页（配置生成、订阅生成）中使用同一个UUID。
            <br>如需切换UUID，请点击"切换UUID"按钮。
          </p>
        </div>
      </div>

      <!-- 配置列表卡片 -->
      <div id="manageResultCard" class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 hidden shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="list" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">2. 配置列表</h2>
        </div>

        <!-- 筛选栏 -->
        <div class="flex flex-col sm:flex-row flex-wrap gap-3 mb-4">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-gray-700">协议:</span>
            <select id="protocolFilter" onchange="applyFilters()"
                    class="flex-1 sm:w-auto px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-gray-500">
              <option value="all">全部</option>
              <option value="vmess">VMess</option>
              <option value="vless">VLESS</option>
              <option value="trojan">Trojan</option>
              <option value="shadowsocks">Shadowsocks</option>
              <option value="hysteria2">Hysteria2</option>
              <option value="tuic">Tuic</option>
              <option value="anytls">AnyTLS</option>
              <option value="any-reality">Any-reality</option>
              <option value="reality">Reality</option>
              <option value="socks5">Socks/Socks5</option>
            </select>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-gray-700">域名托管:</span>
            <select id="hostingFilter" onchange="applyFilters()"
                    class="flex-1 sm:w-auto px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-gray-500">
              <option value="all">全部</option>
              <option value="Cloudflare">Cloudflare</option>
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
          </div>
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-gray-700">每页:</span>
            <select id="pageSizeSelect" onchange="changePageSize()"
                    class="flex-1 sm:w-auto px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-gray-500">
              <option value="5">5条</option>
              <option value="10" selected>10条</option>
              <option value="20">20条</option>
              <option value="50">50条</option>
              <option value="100">100条</option>
            </select>
          </div>
        </div>

        <!-- 批量操作栏 -->
        <div id="bulkActionsBar" class="hidden mb-4 p-3 bg-gray-100 border border-gray-200 rounded-lg">
          <div class="flex flex-col sm:flex-row justify-between items-center gap-3">
            <div class="text-sm font-medium text-gray-700">
              已选择 <span id="selectedCountNum" class="font-bold">0</span> 条
            </div>
            <button id="bulkDeleteBtn" onclick="deleteSelectedConfigs()"
                    class="w-full sm:w-auto px-4 py-2 bg-red-600 text-white rounded-lg border border-red-600 hover:bg-red-700 transition-colors focus:outline-none focus:ring-2 focus:ring-red-500 disabled:opacity-50 disabled:cursor-not-allowed"
                    disabled>
              批量删除
            </button>
          </div>
        </div>

        <!-- 配置列表表格 -->
        <div id="queryResultsContainer" class="mb-6"></div>

        <!-- 分页控件 -->
        <div id="paginationContainer" class="hidden pt-4 border-t border-gray-200">
          <div class="flex flex-col sm:flex-row justify-between items-center gap-3">
            <div class="text-sm text-gray-600 mb-2 sm:mb-0" id="paginationInfo">
              第 1 页，共 1 页，总计 0 条
            </div>
            <div class="flex gap-2">
              <button id="prevPageBtn" onclick="changePage(-1)"
                      class="px-3 py-2 bg-gray-100 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 disabled:opacity-50 disabled:cursor-not-allowed"
                      disabled>
                上一页
              </button>
              <span id="currentPage" class="px-3 py-2 bg-gray-50 border border-gray-200 rounded-lg text-gray-700 flex items-center justify-center min-w-[40px]">1</span>
              <button id="nextPageBtn" onclick="changePage(1)"
                      class="px-3 py-2 bg-gray-100 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500 disabled:opacity-50 disabled:cursor-not-allowed"
                      disabled>
                下一页
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 访问统计卡片 -->
      <div id="manageStatsCard" class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 hidden shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="bar-chart" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">3. 访问统计</h2>
        </div>

        <div class="flex flex-wrap gap-2 mb-4">
          <select id="statsDays" onchange="loadUuidStats()"
                  class="flex-1 sm:flex-none px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-gray-500">
            <option value="7">最近7天</option>
            <option value="14">最近14天</option>
            <option value="30" selected>最近30天</option>
            <option value="60">最近60天</option>
          </select>
          <button onclick="loadUuidStats()"
                  class="flex-1 sm:flex-none px-3 py-2 bg-gray-100 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
            刷新
          </button>
          <button onclick="switchChartType('total')" id="chartTotalBtn"
                  class="flex-1 sm:flex-none px-3 py-2 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
            总访问
          </button>
          <button onclick="switchChartType('split')" id="chartSplitBtn"
                  class="flex-1 sm:flex-none px-3 py-2 bg-gray-100 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
            分类统计
          </button>
        </div>

        <div id="manageStatsSummary" class="grid grid-cols-2 gap-3 mb-6"></div>

        <div class="mb-6" style="height: 250px;">
          <canvas id="manageStatsChart"></canvas>
        </div>

        <div>
          <h3 class="font-semibold text-gray-800 mb-3">最近访问记录</h3>
          <div id="manageRecentLogs" class="border border-gray-200 rounded-lg p-3 max-h-48 overflow-y-auto bg-white">
            <div class="text-center py-6 text-gray-500">加载中...</div>
          </div>
        </div>
      </div>

      <!-- 添加新节点卡片 -->
      <div id="manageAddCard" class="bg-gray-50 border border-gray-200 rounded-xl p-4 sm:p-6 hidden shadow-sm mobile-card">
        <div class="flex items-center gap-2 mb-3 sm:mb-4">
          <i data-lucide="plus" class="w-5 h-5 text-gray-700"></i>
          <h2 class="text-xl font-semibold text-gray-800">4. 添加新节点</h2>
        </div>

        <div class="mb-4">
          <label class="block text-sm font-medium text-gray-700 mb-2">域名托管服务 (Domain Hosting)</label>
          <select id="domainHostingSelect"
                  class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none mb-3">
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
          <div class="bg-gray-50 border-l-4 border-gray-500 p-3 rounded-lg">
            <p class="text-sm text-gray-700">
              <strong>说明：</strong> 选择此配置使用的域名托管服务。这有助于管理和分类不同CDN厂商的配置。
              <br>新增选项：Gcore, Fastly, CacheFly, LightCDN, Vercel, Netlify，"无"表示不使用域名托管。
            </p>
          </div>
        </div>

        <div class="mb-4 sm:mb-6">
          <label class="block text-sm font-medium text-gray-700 mb-2">配置数据 (可批量添加)</label>
          <textarea id="addConfigData" placeholder="支持批量添加：
vmess://...
vless://...
trojan://..."
                    class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-gray-500 focus:border-gray-500 focus:outline-none font-mono text-sm resize-y" rows="4"></textarea>
        </div>

        <button onclick="manageAddConfig()"
                class="w-full px-4 py-3 bg-gray-700 text-white rounded-lg border border-gray-700 hover:bg-gray-800 transition-colors focus:outline-none focus:ring-2 focus:ring-gray-500">
          添加到当前 UUID
        </button>
      </div>
    </div>

    <!-- 页脚 -->
    <footer class="mt-8 sm:mt-12 text-center text-gray-600 text-sm">
      <p>Powered by 
        <a href="https://www.yangzifun.org" class="text-gray-700 hover:text-gray-900 transition-colors">YZFN</a> | 
        <a href="https://www.yangzifun.org/security.html" class="text-gray-700 hover:text-gray-900 transition-colors">安全声明</a> | 
        <a href="https://github.com/yangzifun/proxypilot" class="text-gray-700 hover:text-gray-900 transition-colors">Github</a>
      </p>
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
  let templatesList =[];
  let generatedUuidForEntry = '';

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

  // Config management pagination variables
  let currentPage = 1;
  let currentPageSize = 10;
  let currentTotal = 0;
  let currentTotalPages = 0;
  let currentProtocolFilter = 'all';
  let currentHostingFilter = 'all';
  let selectedConfigs = new Set();

  // ============================================
  // Local Storage Functions (本地存储功能)
  // ============================================

  // Local storage functions for UUID persistence
  function saveUuidToStorage(uuid, isGenerated = false) {
    localStorage.setItem('proxy_manager_uuid', uuid);
    localStorage.setItem('proxy_manager_last_access', Date.now().toString());
    if (isGenerated) {
      localStorage.setItem('proxy_manager_generated_uuid', uuid);
    } else {
      localStorage.removeItem('proxy_manager_generated_uuid');
    }
  }

  function getUuidFromStorage() {
    const uuid = localStorage.getItem('proxy_manager_uuid');
    const lastAccess = localStorage.getItem('proxy_manager_last_access');

    // Check if UUID is still valid (less than 30 days old)
    if (uuid && lastAccess) {
      const thirtyDays = 30 * 24 * 60 * 60 * 1000; // 30 days in milliseconds
      const now = Date.now();
      if (now - parseInt(lastAccess) < thirtyDays) {
        return uuid;
      }
    }
    return null;
  }

  function clearUuidFromStorage() {
    localStorage.removeItem('proxy_manager_uuid');
    localStorage.removeItem('proxy_manager_last_access');
    localStorage.removeItem('proxy_manager_generated_uuid');
  }

  function isGeneratedUuid(uuid) {
    return uuid && localStorage.getItem('proxy_manager_generated_uuid') === uuid;
  }

  function isValidUuidInput(uuid) {
    return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(uuid || '');
  }

  // ============================================
  // Configuration Management Functions
  // ============================================

  function openConfigManagerModal() {
    const modal = document.getElementById('configManagerModal');
    if (!modal) {
      console.error('配置管理模态框未找到');
      return;
    }

    modal.classList.remove('hidden');

    // 聚焦到输入框
    const input = document.getElementById('configUuidInput');
    if (input) {
      input.focus();

      // 自动填充存储的 UUID
      const storedUuid = getUuidFromStorage();
      if (storedUuid) {
        input.value = storedUuid;
      }
    }
  }

  function closeConfigManagerModal() {
    document.getElementById('configManagerModal').classList.add('hidden');
  }

  function generateNewUuid() {
    const newUuid = 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
      const r = Math.random() * 16 | 0;
      const v = c === 'x' ? r : (r & 0x3 | 0x8);
      return v.toString(16);
    });
    generatedUuidForEntry = newUuid;
    document.getElementById('configUuidInput').value = newUuid;
    showToast('已生成新的 UUID，可用于创建新的配置分组', 'success');
  }

  async function enterConfigManagement() {
    const uuid = document.getElementById('configUuidInput').value.trim();
    if (!uuid) {
      showToast('请输入 UUID', 'error');
      return;
    }

    if (!isValidUuidInput(uuid)) {
      showToast('UUID格式无效，请检查或点击生成按钮', 'error');
      return;
    }

    const isGenerated = uuid === generatedUuidForEntry || isGeneratedUuid(uuid);
    if (!isGenerated) {
      try {
        const response = await fetch(\`\${WORKER_DOMAIN}/manage/uuid-check/\${encodeURIComponent(uuid)}\`);
        const result = await response.json();
        if (!response.ok || !result.success || !result.exists) {
          showToast(result.error || result.message || 'UUID不存在，请通过生成按钮生成新的UUID', 'error');
          return;
        }
      } catch (error) {
        showToast('UUID校验失败: ' + error.message, 'error');
        return;
      }
    }

    closeConfigManagerModal();

    // Save UUID to storage
    saveUuidToStorage(uuid, isGenerated);

    // Update logout button
    updateLogoutButton();

    // Show manage tab button
    document.getElementById('manageTabBtn').classList.remove('hidden');

    // Switch to manage tab
    switchTab('manage', uuid);
  }

  function updateLogoutButton() {
    const configManagerOpenBtn = document.getElementById('configManagerOpenBtn');
    const uuidDropdownContainer = document.getElementById('uuidDropdownContainer');
    const uuidDisplay = document.getElementById('uuidDisplay');
    const dropdownUuid = document.getElementById('dropdownUuid');

    const storedUuid = getUuidFromStorage();

    if (storedUuid) {
      // 有UUID，显示下拉菜单，隐藏配置管理按钮
      if (configManagerOpenBtn) configManagerOpenBtn.style.display = 'none';
      if (uuidDropdownContainer) {
        uuidDropdownContainer.classList.remove('hidden');

        // 设置UUID显示（截短显示）
        const shortUuid = storedUuid.substring(0, 8) + '...';
        uuidDisplay.textContent = 'UUID: ' + shortUuid;
        uuidDisplay.title = 'UUID: ' + storedUuid;
        dropdownUuid.textContent = storedUuid;

        // 添加下拉菜单交互
        setupUuidDropdown();
      }
    } else {
      // 没有UUID，显示配置管理按钮，隐藏下拉菜单
      if (configManagerOpenBtn) configManagerOpenBtn.style.display = 'inline-block';
      if (uuidDropdownContainer) uuidDropdownContainer.classList.add('hidden');
    }
  }

  // 设置UUID下拉菜单交互
  function setupUuidDropdown() {
    const dropdownBtn = document.getElementById('uuidDropdownBtn');
    const dropdownMenu = document.getElementById('uuidDropdownMenu');

    if (!dropdownBtn || !dropdownMenu) return;

    // 点击按钮显示/隐藏下拉菜单
    dropdownBtn.addEventListener('click', function(e) {
      e.stopPropagation();
      const isVisible = !dropdownMenu.classList.contains('hidden');
      dropdownMenu.classList.toggle('hidden', isVisible);
    });

    // 点击其他地方关闭下拉菜单
    document.addEventListener('click', function() {
      dropdownMenu.classList.add('hidden');
    });

    // 防止下拉菜单内部点击关闭
    dropdownMenu.addEventListener('click', function(e) {
      e.stopPropagation();
    });
  }

  // 退出登录函数
  function logout() {
    if (confirm('确定要退出登录吗？这将清除当前保存的UUID。')) {
      clearUuidFromStorage();
      // 关闭下拉菜单
      const dropdownMenu = document.getElementById('uuidDropdownMenu');
      if (dropdownMenu) {
        dropdownMenu.classList.add('hidden');
      }
      // 重新加载页面
      location.reload();
    }
  }

  // 监听配置管理模态框的ESC键关闭
  document.addEventListener('keydown', function(e) {
    const configManagerModal = document.getElementById('configManagerModal');
    const uuidInfoModal = document.getElementById('uuidInfoModal');

    if (e.key === 'Escape') {
      if (configManagerModal && !configManagerModal.classList.contains('hidden')) {
        closeConfigManagerModal();
      }
      if (uuidInfoModal && !uuidInfoModal.classList.contains('hidden')) {
        closeUuidInfoModal();
      }
    }
  });

  // ============================================
  // UUID信息管理功能（简化版）
  // ============================================

  // 打开UUID信息管理模态框
  function openUuidInfoModal() {
    const storedUuid = getUuidFromStorage();
    if (!storedUuid) {
      showToast('请先登录或输入UUID', 'error');
      return;
    }

    const modal = document.getElementById('uuidInfoModal');
    modal.classList.remove('hidden');

    // 加载当前UUID信息
    loadCurrentUuidInfo(storedUuid);

    // 确保保存按钮状态正确
    const saveBtn = document.getElementById('uuidInfoSaveBtn');
    if (saveBtn && !saveBtn.originalText) {
      saveBtn.originalText = saveBtn.innerHTML;
    }
  }

  // 关闭UUID信息管理模态框
  function closeUuidInfoModal() {
    document.getElementById('uuidInfoModal').classList.add('hidden');

    // 重置保存按钮状态
    const saveBtn = document.getElementById('uuidInfoSaveBtn');
    if (saveBtn && saveBtn.originalText) {
      saveBtn.innerHTML = saveBtn.originalText;
      saveBtn.disabled = false;
    }
  }

  // 加载当前UUID信息（简化版，不加载时间字段）
  async function loadCurrentUuidInfo(uuid) {
    const saveBtn = document.getElementById('uuidInfoSaveBtn');

    // 保存原始文本
    if (!saveBtn.originalText) {
      saveBtn.originalText = saveBtn.innerHTML;
    }

    // 设置加载状态
    saveBtn.disabled = true;
    saveBtn.innerHTML = '<div class="loader"></div>加载中...';

    try {
      const response = await fetch(\`\${WORKER_DOMAIN}/manage/uuid-info/\${uuid}\`);
      const data = await response.json();

      if (data.success) {
        if (data.exists) {
          // 填充表单（只填充基础信息）
          document.getElementById('uuidInfoUuid').value = data.data.uuid;
          document.getElementById('uuidInfoOwner').value = data.data.owner;
          document.getElementById('uuidInfoEmail').value = data.data.email;
          document.getElementById('uuidInfoContact').value = data.data.contact_info || '';
          document.getElementById('uuidInfoStatus').value = data.data.status;
          document.getElementById('uuidInfoRemark').value = data.data.remark || '';

          showToast('UUID信息加载成功', 'success');
        } else {
          // UUID信息不存在，显示默认值
          document.getElementById('uuidInfoUuid').value = uuid;
          document.getElementById('uuidInfoOwner').value = '';
          document.getElementById('uuidInfoEmail').value = '';
          document.getElementById('uuidInfoContact').value = '';
          document.getElementById('uuidInfoStatus').value = 'active';
          document.getElementById('uuidInfoRemark').value = '';

          showToast('UUID信息不存在，可以创建新的', 'info');
        }
      } else {
        showToast(data.error || '加载UUID信息失败', 'error');
      }
    } catch (error) {
      console.error('加载UUID信息失败:', error);
      showToast('加载UUID信息失败: ' + error.message, 'error');
    } finally {
      // 无论成功失败都重置按钮状态
      saveBtn.disabled = false;
      saveBtn.innerHTML = saveBtn.originalText;
    }
  }

  // 保存UUID信息 - 修复：移除了邮箱验证，只保留必填字段检查
  async function saveUuidInfo() {
    const uuid = document.getElementById('uuidInfoUuid').value.trim();
    const owner = document.getElementById('uuidInfoOwner').value.trim();
    const email = document.getElementById('uuidInfoEmail').value.trim();
    const contact = document.getElementById('uuidInfoContact').value.trim();
    const status = document.getElementById('uuidInfoStatus').value;
    const remark = document.getElementById('uuidInfoRemark').value.trim();

    if (!uuid) {
      showToast('UUID不能为空', 'error');
      return;
    }

    if (!owner) {
      showToast('所属人姓名/标识不能为空', 'error');
      document.getElementById('uuidInfoOwner').focus();
      return;
    }

    if (!email) {
      showToast('邮箱地址不能为空', 'error');
      document.getElementById('uuidInfoEmail').focus();
      return;
    }

    // 修复：移除了邮箱格式验证，只检查是否为空
    // 因为有些用户可能使用非标准邮箱格式

    const saveBtn = document.getElementById('uuidInfoSaveBtn');

    // 保存按钮原始文本
    if (!saveBtn.originalText) {
      saveBtn.originalText = saveBtn.innerHTML;
    }

    // 正确设置加载状态
    saveBtn.disabled = true;
    saveBtn.innerHTML = '<div class="loader"></div>保存中...';

    try {
      const response = await fetch(\`\${WORKER_DOMAIN}/manage/uuid-info\`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          uuid,
          owner,
          email,
          contact_info: contact,
          status,
          remark
        })
      });

      const data = await response.json();

      if (data.success) {
        showToast(\`UUID信息\${data.action === 'created' ? '创建' : '更新'}成功\`, 'success');

        // 重新加载信息以获取更新时间
        setTimeout(() => {
          loadCurrentUuidInfo(uuid);
        }, 500);
      } else {
        showToast(data.error || '保存UUID信息失败', 'error');

        // 出错时重置按钮状态
        saveBtn.disabled = false;
        saveBtn.innerHTML = saveBtn.originalText;
      }
    } catch (error) {
      console.error('保存UUID信息失败:', error);
      showToast('保存UUID信息失败: ' + error.message, 'error');

      // 捕获异常时重置按钮状态
      saveBtn.disabled = false;
      saveBtn.innerHTML = saveBtn.originalText;
    }
  }

  // 更新 setButtonLoading 函数以确保一致性
  function setButtonLoading(btn, ld, txt) { 
    if (ld) { 
      // 保存原始文本，而不是整个innerHTML
      if (!btn.originalText) {
        btn.originalText = btn.innerHTML;
      }
      btn.disabled = true; 
      btn.innerHTML = '<div class="loader"></div>' + (txt || '处理中...'); 
    } else { 
      btn.disabled = false; 
      if (btn.originalText) {
        btn.innerHTML = btn.originalText;
      }
    } 
  }

  // ============================================
  // Tab Switching Functions
  // ============================================

  function switchTab(tabName, forceUuid = null) {
    // If switching to manage tab and we have a stored UUID, use it
    if (tabName === 'manage' && !forceUuid) {
      const storedUuid = getUuidFromStorage();
      if (storedUuid) {
        forceUuid = storedUuid;
      } else {
        // No stored UUID, show modal
        openConfigManagerModal();
        return;
      }
    }

    // Hide all tabs
    document.querySelectorAll('.tab-content').forEach(tab => {
      tab.classList.add('hidden');
    });

    // Deactivate all tab buttons
    document.querySelectorAll('.tab-btn').forEach(btn => {
      btn.setAttribute('data-active', 'false');
      btn.classList.remove('text-gray-800', 'border-gray-800');
      btn.classList.add('text-gray-600', 'border-transparent');
    });

    // Show selected tab
    const tabElement = document.getElementById(\`\${tabName}-tab\`);
    if (tabElement) {
      tabElement.classList.remove('hidden');
    }

    // Activate selected tab button
    const tabBtn = document.querySelector(\`.tab-btn[onclick*="\${tabName}"]\`);
    if (tabBtn) {
      tabBtn.setAttribute('data-active', 'true');
      tabBtn.classList.remove('text-gray-600', 'border-transparent');
      tabBtn.classList.add('text-gray-800', 'border-gray-800');
    }

    // If it's manage tab and we have a UUID, load it
    if (tabName === 'manage' && forceUuid) {
      setTimeout(() => {
        loadUuidData(forceUuid);
      }, 100);
    }

    // Load templates if switching to clash tab
    if (tabName === 'clash') {
      setTimeout(() => {
        loadTemplates();
      }, 100);
    }

    // Update UUID inputs if switching to batch or clash tabs
    if (tabName === 'batch' || tabName === 'clash') {
      setTimeout(() => {
        updateUuidInputs();
      }, 100);
    }
  }

  // New function to load UUID data
  function loadUuidData(uuid) {
    currentUuid = uuid;

    // Fill UUID input
    document.getElementById('queryUuidInput').value = uuid;

    // Show manage sections
    document.getElementById('manageResultCard').classList.remove('hidden');
    document.getElementById('manageStatsCard').classList.remove('hidden');
    document.getElementById('manageAddCard').classList.remove('hidden');

    // Show subscription link
    const subLink = WORKER_DOMAIN + '/sub/' + uuid;
    const subInput = document.getElementById('manageSubUrlLink');
    subInput.value = subLink;
    document.getElementById('manageSubLinkDisplay').classList.remove('hidden');

    // Load configs and stats
    manageQueryByUuid();
    showToast('已加载 UUID: ' + uuid.substring(0, 8) + '...', 'success');
  }

  function updateUuidInputs() {
    const storedUuid = getUuidFromStorage();
    if (storedUuid) {
      // Update batch generation UUID input
      const batchUuidInput = document.getElementById('genBaseConfigUuidInput');
      if (batchUuidInput) {
        batchUuidInput.value = storedUuid;
      }

      // Update clash generation UUID input
      const clashUuidInput = document.getElementById('clashConfigUuid');
      if (clashUuidInput) {
        clashUuidInput.value = storedUuid;
      }
    }
  }

  // ============================================
  // Utility functions
  // ============================================

  function showToast(msg, type='info') { 
    const c = document.getElementById('toast-container'); 
    const t = document.createElement('div'); 
    t.className = 'px-3 py-2 rounded-lg shadow-lg border border-gray-300 bg-white text-gray-800 animate-slide-in text-sm'; 

    // 设置图标
    let icon = 'ℹ️';
    if (type === 'success') icon = '✅';
    if (type === 'error') icon = '❌';

    t.innerHTML = \`<span>\${icon} \${msg}</span>\`; 
    c.appendChild(t); 

    // 5秒后移除
    setTimeout(()=>{
      t.classList.add('animate-fade-out');
      setTimeout(()=>t.remove(), 500);
    }, 4500); 
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

  // ============================================
  // Batch Generation Tab Functions
  // ============================================

  function toggleBatchAddressOptions() {
    const type = document.querySelector('input[name="genAddressType"]:checked').value;
    const ipContainer = document.getElementById('batchIpOptionsContainer');
    const domainBox = document.getElementById('batchDomainInfoBox');
    const disableBox = document.getElementById('batchDisableInfoBox');
    const fetchBtn = document.getElementById('genFetchBtn');
    const addressTextarea = document.getElementById('genAddressListInput');
    const subscriptionBox = document.getElementById('batchSubscriptionLinkBox');

    if (type === 'domain') {
      ipContainer.classList.add('hidden');
      domainBox.classList.remove('hidden');
      disableBox.classList.add('hidden');
      fetchBtn.classList.remove('hidden');
      addressTextarea.disabled = false;
      subscriptionBox.classList.remove('hidden');
    } else if (type === 'disable') {
      ipContainer.classList.add('hidden');
      domainBox.classList.add('hidden');
      disableBox.classList.remove('hidden');
      fetchBtn.classList.add('hidden');
      addressTextarea.disabled = true;
      addressTextarea.value = ''; // 清空地址列表
      subscriptionBox.classList.add('hidden');
    } else {
      ipContainer.classList.remove('hidden');
      domainBox.classList.add('hidden');
      disableBox.classList.add('hidden');
      fetchBtn.classList.remove('hidden');
      addressTextarea.disabled = false;
      subscriptionBox.classList.remove('hidden');
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

    subBox.classList.add('hidden');

    setButtonLoading(btn, true, '');

    try {
      const addrType = document.querySelector('input[name="genAddressType"]:checked').value;

      // 如果选择了禁用，则不获取地址列表
      if (addrType === 'disable') {
        showToast('优选已禁用，不需要获取地址列表', 'info');
        setButtonLoading(btn, false, '获取优选列表');
        return;
      }

      let url = \`\${WORKER_DOMAIN}/fetch-addresses?type=\${addrType}\`;

      if (addrType === 'ip') {
        const ipType = document.querySelector('input[name="genIpType"]:checked').value;
        const carrier = document.querySelector('input[name="genCarrierType"]:checked').value;
        url += \`&ipType=\${ipType}&carrierType=\${carrier}&source=database\`;

        const subUrl = \`\${WORKER_DOMAIN}/batch-ip?type=ip&ipType=\${ipType}&carrier=\${carrier}\`;
        subInput.value = subUrl;
        subBox.classList.remove('hidden');

      } else {
        const subUrl = \`\${WORKER_DOMAIN}/batch-ip?type=domain\`;
        subInput.value = subUrl;
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

    linkBox.classList.add('hidden');
    qrCodeContainer.classList.add('hidden');

    setButtonLoading(btn, true, '');

    try {
      const configSource = document.querySelector('input[name="genConfigSource"]:checked').value;
      const addressType = document.querySelector('input[name="genAddressType"]:checked').value;
      const addressList = document.getElementById('genAddressListInput').value;

      let payload = {};

      // 检查是否禁用优选
      const disableOptimization = addressType === 'disable';

      if (!disableOptimization) {
        payload.addressList = addressList;
      }

      payload.disableOptimization = disableOptimization;

      if (configSource === 'uuid') {
        const uuid = document.getElementById('genBaseConfigUuidInput').value.trim();
        if(!uuid) throw new Error("请输入 UUID");
        payload.baseConfigUuid = uuid;

        const addrType = addressType;
        let linkParams = \`type=\${addrType}\`;
  
        if (disableOptimization) {
          linkParams += \`&disable=true\`;
        } else if(addrType === 'ip') {
          const ipType = document.querySelector('input[name="genIpType"]:checked').value;
          const carrier = document.querySelector('input[name="genCarrierType"]:checked').value;
          linkParams += \`&ipType=\${ipType}&carrier=\${carrier}\`;
        }
  
        const subLink = \`\${WORKER_DOMAIN}/batch-configs/\${uuid}?\${linkParams}\`;
        linkInput.value = subLink;
        linkBox.classList.remove('hidden');

        // Generate QR code
        generateBatchQRCode(subLink);

      } else {
        const manualConfig = document.getElementById('genBaseConfigInput').value.trim();
        if(!manualConfig) throw new Error("请输入基础配置");
        payload.baseConfig = manualConfig;

        if (disableOptimization) {
          showToast('手动模式，优选已禁用，只输出原配置', 'info');
        } else {
          showToast('手动模式不支持二维码生成，请使用UUID模式获取订阅链接', 'info');
        }
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

  // 更新后的manageQueryByUuid函数，支持筛选和分页
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
    document.getElementById('manageSubLinkDisplay').classList.remove('hidden');

    // 获取筛选参数
    const protocolFilter = document.getElementById('protocolFilter').value;
    const hostingFilter = document.getElementById('hostingFilter').value;
    const pageSize = parseInt(document.getElementById('pageSizeSelect').value);

    // 更新当前筛选状态
    currentProtocolFilter = protocolFilter;
    currentHostingFilter = hostingFilter;
    currentPageSize = pageSize;

    // 构建查询URL
    let queryUrl = \`\${WORKER_DOMAIN}/manage/configs/\${u}?page=\${currentPage}&pageSize=\${pageSize}\`;

    if (protocolFilter !== 'all') {
      queryUrl += \`&protocol=\${encodeURIComponent(protocolFilter)}\`;
    }

    if (hostingFilter !== 'all') {
      queryUrl += \`&domain_hosting=\${encodeURIComponent(hostingFilter)}\`;
    }

    try{
      const r = await fetch(queryUrl);
      const c = document.getElementById('queryResultsContainer');
      if(r.status === 404){ 
        c.innerHTML='<p class="p-5 text-center text-gray-500">未找到配置，请直接添加。</p>'; 
        document.getElementById('paginationContainer').classList.add('hidden');
        document.getElementById('bulkActionsBar').classList.add('hidden');
      } else {
        const d = await r.json();

        // 更新分页信息
        currentTotal = d.pagination.total;
        currentTotalPages = d.pagination.totalPages;

        // 渲染表格
        let h = '<div class="table-container"><table class="w-full mobile-table"><thead><tr class="bg-gray-100"><th class="p-2 sm:p-3 text-left"><input type="checkbox" id="selectAllCheckbox" class="mr-2" onclick="toggleSelectAll()">全选</th><th class="p-2 sm:p-3 text-left">备注</th><th class="p-2 sm:p-3 text-left">协议</th><th class="p-2 sm:p-3 text-left">域名托管</th><th class="p-2 sm:p-3 text-left">配置</th><th class="p-2 sm:p-3 text-left">操作</th></tr></thead><tbody>';

        if (!d.configs || d.configs.length === 0) {
          h += '<tr><td colspan="6" class="p-5 text-center text-gray-500">未找到符合条件的配置</td></tr>';
        } else {
          d.configs.forEach(Row=>{
            const sc = JSON.stringify(Row).replace(/"/g, '&quot;');
            const hosting = Row.domain_hosting || 'Cloudflare';
            const hostingClass = hostingStyleMap[hosting] || 'hosting-other';
            const isSelected = selectedConfigs.has(Row.id) ? 'checked' : '';

            h+=\`<tr class="border-t border-gray-200 hover:bg-gray-50">
              <td class="p-2 sm:p-3"><input type="checkbox" class="config-checkbox" data-id="\${Row.id}" \${isSelected} onchange="toggleConfigSelection(\${Row.id}, this.checked)"></td>
              <td class="p-2 sm:p-3">\${Row.remark||'-'}</td>
              <td class="p-2 sm:p-3 font-mono text-sm">\${Row.protocol}</td>
              <td class="p-2 sm:p-3"><span class="px-2 py-1 rounded text-xs \${hostingClass}">\${hosting}</span></td>
              <td class="p-2 sm:p-3 config-data-cell font-mono text-xs sm:text-sm">\${Row.config_data.substring(0,30)}...</td>
              <td class="p-2 sm:p-3">
                <button class="px-2 py-1 border border-gray-300 text-gray-700 rounded hover:bg-gray-50 mr-1 sm:mr-2 text-xs sm:text-sm" data-config="\${sc}" onclick="openEditModal(JSON.parse(this.dataset.config))">编辑</button>
                <button class="px-2 py-1 bg-red-600 text-white rounded border border-red-600 hover:bg-red-700 text-xs sm:text-sm" onclick="delOne(\${Row.id})">删除</button>
              </td>
            </tr>\`;
          });
        }
        h+='</tbody></table></div>'; 
        c.innerHTML = h;

        // 更新分页控件
        updatePaginationControls();

        // 更新批量操作栏
        updateBulkActionsBar();
      }

      // Load statistics
      await loadUuidStats();

    } catch(e){ 
      showToast(e.message,'error'); 
    } finally{ 
      setButtonLoading(b, false, b.innerHTML); 
    }
  }

  // 应用筛选
  function applyFilters() {
    currentPage = 1; // 重置到第一页
    selectedConfigs.clear(); // 清空选择
    manageQueryByUuid();
  }

  // 更改每页显示数量
  function changePageSize() {
    const newPageSize = parseInt(document.getElementById('pageSizeSelect').value);
    if (newPageSize !== currentPageSize) {
      currentPageSize = newPageSize;
      currentPage = 1; // 重置到第一页
      selectedConfigs.clear(); // 清空选择
      manageQueryByUuid();
    }
  }

  // 更改页码
  function changePage(delta) {
    const newPage = currentPage + delta;
    if (newPage >= 1 && newPage <= currentTotalPages) {
      currentPage = newPage;
      manageQueryByUuid();
    }
  }

  // 更新分页控件
  function updatePaginationControls() {
    const paginationContainer = document.getElementById('paginationContainer');
    const prevPageBtn = document.getElementById('prevPageBtn');
    const nextPageBtn = document.getElementById('nextPageBtn');
    const currentPageSpan = document.getElementById('currentPage');
    const paginationInfo = document.getElementById('paginationInfo');

    if (currentTotal === 0) {
      paginationContainer.classList.add('hidden');
      return;
    }

    paginationContainer.classList.remove('hidden');
    prevPageBtn.disabled = currentPage <= 1;
    nextPageBtn.disabled = currentPage >= currentTotalPages;
    currentPageSpan.textContent = currentPage;
    paginationInfo.textContent = \`第 \${currentPage} 页，共 \${currentTotalPages} 页，总计 \${currentTotal} 条\`;
  }

  // 配置选择相关函数
  function toggleConfigSelection(id, checked) {
    if (checked) {
      selectedConfigs.add(id);
    } else {
      selectedConfigs.delete(id);
      // 取消全选复选框
      document.getElementById('selectAllCheckbox').checked = false;
    }
    updateBulkActionsBar();
  }

  function toggleSelectAll() {
    const selectAllCheckbox = document.getElementById('selectAllCheckbox');
    const configCheckboxes = document.querySelectorAll('.config-checkbox');
    const isChecked = selectAllCheckbox.checked;

    selectedConfigs.clear();

    if (isChecked) {
      configCheckboxes.forEach(checkbox => {
        const id = parseInt(checkbox.dataset.id);
        selectedConfigs.add(id);
        checkbox.checked = true;
      });
    } else {
      configCheckboxes.forEach(checkbox => {
        checkbox.checked = false;
      });
    }

    updateBulkActionsBar();
  }

  // 更新批量操作栏
  function updateBulkActionsBar() {
    const bulkActionsBar = document.getElementById('bulkActionsBar');
    const selectedCountNum = document.getElementById('selectedCountNum');
    const bulkDeleteBtn = document.getElementById('bulkDeleteBtn');
    const selectedCount = selectedConfigs.size;

    if (selectedCount > 0) {
      bulkActionsBar.classList.remove('hidden');
      selectedCountNum.textContent = selectedCount;
      bulkDeleteBtn.disabled = false;
    } else {
      bulkActionsBar.classList.add('hidden');
      bulkDeleteBtn.disabled = true;
    }
  }

  // 批量删除配置 - 修复版本
  async function deleteSelectedConfigs() {
    if (selectedConfigs.size === 0) {
      showToast('请先选择要删除的配置', 'error');
      return;
    }

    if (!confirm(\`确定要删除选中的 \${selectedConfigs.size} 条配置吗？\`)) {
      return;
    }

    const ids = Array.from(selectedConfigs).map(id => parseInt(id));

    try {
      const response = await fetch(\`\${WORKER_DOMAIN}/manage/configs/batch\`, {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ids })
      });

      const result = await response.json();

      if (result.success) {
        showToast(\`成功删除 \${result.deleted ?? 0} 条配置\`, 'success');
        selectedConfigs.clear();
        manageQueryByUuid();
      } else {
        showToast(result.error || '删除失败', 'error');
      }
    } catch (error) {
      showToast('网络错误: ' + error.message, 'error');
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
      const response = await fetch(\`\${WORKER_DOMAIN}/manage/configs\`, {
        method: 'POST', 
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
          uuid: u,
          config_data: d,
          domain_hosting: hosting,
          allow_create_uuid: isGeneratedUuid(u)
        })
      });

      const result = await response.json();
      if (result.success) {
        showToast('添加成功','success'); 
        saveUuidToStorage(u, false);
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
      await fetch(\`\${WORKER_DOMAIN}/manage/configs/id/\${id}\`,{method:'DELETE'}); 
      // 如果该配置被选中，从选中列表中移除
      selectedConfigs.delete(id);
      manageQueryByUuid(); 
    } 
  }

  async function deleteAll(){ 
    const u = document.getElementById('queryUuidInput').value; 
    if(confirm('确认清空所有配置?')) { 
      await fetch(\`\${WORKER_DOMAIN}/manage/configs/\${u}\`,{method:'DELETE'}); 
      selectedConfigs.clear();
      manageQueryByUuid(); 
    } 
  }

  function copyManageSubLink(){ 
    navigator.clipboard.writeText(document.getElementById('manageSubUrlLink').value)
      .then(()=>showToast('已复制','success'))
      .catch(()=>showToast('复制失败','error')); 
  }

  // Edit modal functions - 修复版本
  function openEditModal(config) {
    document.getElementById('edit-id').value = config.id;
    let p = 'unknown'; 
    const l = config.config_data;

    const protocolPatterns =[
      { prefix: 'vmess://', type: 'vmess' },
      { prefix: 'vless://', type: 'vless' },
      { prefix: 'trojan://', type: 'trojan' },
      { prefix: 'hysteria2://', type: 'hysteria2' },
      { prefix: 'tuic://', type: 'tuic' },
      { prefix: 'anytls://', type: 'anytls' },
      { prefix: 'socks5://', type: 'socks5' },
      { prefix: 'socks://', type: 'socks5' },
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

    document.getElementById('edit-protocol').value = p;['ps','add','port','id-uuid','net','type','host','path','tls','sni'].forEach(k=>document.getElementById('edit-'+k).value='');

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
    document.getElementById('editModalOverlay').classList.remove('hidden');
  }

  function closeEditModal() { 
    document.getElementById('editModalOverlay').classList.add('hidden'); 
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
    // 构建配置字符串的逻辑保持不变
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
      try {
        let u = new URL(proto + '://' + uuid + '@' + add + ':' + port);
        if (net !== 'tcp' && net) u.searchParams.set('type', net);
         if (net === 'xhttp') {
        u.searchParams.set('mode', 'auto');
        } // 默认设为 auto，也可扩展 UI 供用户选择
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
      } catch(e) {
        showToast('生成的URL无效: ' + e.message, 'error');
        return;
      }
    }

    const b = document.querySelector('#editModalOverlay button[onclick="saveEditedConfig()"]');
    const o = b ? b.innerHTML : '保存修改';
    if (b) setButtonLoading(b, true);

    try {
      const r = await fetch(\`\${WORKER_DOMAIN}/manage/configs\`, {
        method: 'PUT',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
          id: parseInt(id), // 确保传给后端的是数字
          uuid: currentUuid || document.getElementById('queryUuidInput').value.trim(),
          config_data: nL,
          domain_hosting: domainHosting
        })
      });
  
      const result = await r.json();
      if (result.success) {
        showToast('保存成功', 'success');
        closeEditModal(); // 关闭窗口
    
        // 延迟一小会儿刷新列表，确保数据库操作已同步
        setTimeout(() => {
          manageQueryByUuid(); 
        }, 300);
      } else {
        showToast(result.error || '保存失败', 'error');
      }
    } catch (e) {
      console.error("Save error:", e);
      showToast('请求失败: ' + e.message, 'error');
    } finally {
      if (b) setButtonLoading(b, false, o);
    }
  }

  // Statistics functions
  async function loadUuidStats() {
    if (!currentUuid) return;

    const days = document.getElementById('statsDays').value;
    try {
      const response = await fetch(\`\${WORKER_DOMAIN}/manage/stats/\${currentUuid}?days=\${days}\`);
      const data = await response.json();

      if (data.success) {
        displayStatsSummary(data);
        renderStatsChart(data.daily_stats);
        displayRecentLogs(data.recent_logs);
        updateChartButtons();
      } else {
        showToast(data.error || '无法加载统计数据', 'error');
        document.getElementById('manageStatsSummary').innerHTML = \`
          <div class="text-center p-5 text-gray-500">
            <p>\${data.error || '无统计信息'}</p>
            <p class="text-sm">请确保已升级到v2.0+版本并启用访问日志记录</p>
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
      <div class="bg-white border border-gray-200 rounded-lg p-3">
        <div class="text-xl sm:text-2xl font-bold text-blue-600">\${stats.total_access}</div>
        <div class="text-xs sm:text-sm text-gray-700">总访问次数</div>
        <div class="text-xs text-gray-500 mt-1">首次: \${stats.first_access ? formatDate(stats.first_access) : '从未'}</div>
      </div>
      <div class="bg-white border border-gray-200 rounded-lg p-3">
        <div class="text-xl sm:text-2xl font-bold text-green-600">\${stats.today_total}</div>
        <div class="text-xs sm:text-sm text-gray-700">今日访问</div>
        <div class="text-xs text-gray-500 mt-1">订阅:\${stats.today_subscription} | 网页:\${stats.today_apigen}</div>
      </div>
      <div class="bg-white border border-gray-200 rounded-lg p-3">
        <div class="text-xl sm:text-2xl font-bold text-purple-600">\${stats.subscription_count}</div>
        <div class="text-xs sm:text-sm text-gray-700">订阅访问</div>
        <div class="text-xs text-gray-500 mt-1">占比 \${subscriptionPercent}%</div>
      </div>
      <div class="bg-white border border-gray-200 rounded-lg p-3">
        <div class="text-xl sm:text-2xl font-bold text-orange-600">\${stats.apigen_count}</div>
        <div class="text-xs sm:text-sm text-gray-700">网页生成</div>
        <div class="text-xs text-gray-500 mt-1">占比 \${apigenPercent}%</div>
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

    let datasets =[];

    switch (currentChartType) {
      case 'total':
        datasets =[{
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
        datasets =[
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
      container.innerHTML = '<div class="text-center py-6 text-gray-500">暂无访问记录</div>';
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
        <div class="flex justify-between items-center py-2 border-b border-gray-100 last:border-b-0">
          <div class="flex items-center gap-2">
            <span class="text-xs sm:text-sm font-mono text-gray-600">\${formatDateTime(log.created_at)}</span>
            <span class="px-2 py-1 rounded text-xs \${typeClass}">\${typeText}</span>
          </div>
          <div class="text-xs text-gray-500 truncate max-w-[120px] sm:max-w-[200px]" title="\${log.user_agent || '未知'}">
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
    document.getElementById('chartTotalBtn').classList.toggle('bg-gray-700', currentChartType === 'total');
    document.getElementById('chartTotalBtn').classList.toggle('text-white', currentChartType === 'total');
    document.getElementById('chartTotalBtn').classList.toggle('bg-gray-100', currentChartType !== 'total');
    document.getElementById('chartTotalBtn').classList.toggle('text-gray-700', currentChartType !== 'total');

    document.getElementById('chartSplitBtn').classList.toggle('bg-gray-700', currentChartType === 'split');
    document.getElementById('chartSplitBtn').classList.toggle('text-white', currentChartType === 'split');
    document.getElementById('chartSplitBtn').classList.toggle('bg-gray-100', currentChartType !== 'split');
    document.getElementById('chartSplitBtn').classList.toggle('text-gray-700', currentChartType !== 'split');
  }

  // ============================================
  // Clash Subscription Tab Functions
  // ============================================

  function toggleClashAddressOptions() {
    const type = document.querySelector('input[name="clashAddressType"]:checked').value;
    const ipContainer = document.getElementById('clashIpOptionsContainer');
    const domainBox = document.getElementById('clashDomainInfoBox');
    const disableBox = document.getElementById('clashDisableInfoBox');

    if (type === 'domain') {
      ipContainer.classList.add('hidden');
      domainBox.classList.remove('hidden');
      disableBox.classList.add('hidden');
    } else if (type === 'disable') {
      ipContainer.classList.add('hidden');
      domainBox.classList.add('hidden');
      disableBox.classList.remove('hidden');
    } else {
      ipContainer.classList.remove('hidden');
      domainBox.classList.add('hidden');
      disableBox.classList.add('hidden');
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

      // 检查是否禁用优选
      if (addressType === 'disable') {
        subscriptionUrl.searchParams.set('disable', 'true');
      } else if (addressType === 'ip') {
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

  // ============================================
  // Initialize
  // ============================================

  document.addEventListener('DOMContentLoaded', function() {
    // Initialize domain hosting select
    const hostingSelect = document.getElementById('domainHostingSelect');
    hostingSelect.value = 'Cloudflare';

    // Initialize tab functionality
    toggleBatchAddressOptions();
    toggleClashAddressOptions();

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

    // 为配置管理按钮添加事件监听器
    const configManagerOpenBtn = document.getElementById('configManagerOpenBtn');
    if (configManagerOpenBtn) {
      configManagerOpenBtn.addEventListener('click', function(e) {
        e.preventDefault();
        openConfigManagerModal();
      });
      console.log('配置管理按钮事件监听器已绑定');
    } else {
      console.error('配置管理按钮未找到');
    }

    // Check for stored UUID on page load
    const storedUuid = getUuidFromStorage();
    if (storedUuid) {
      // Show manage tab button
      document.getElementById('manageTabBtn').classList.remove('hidden');

      // Update logout button
      updateLogoutButton();

      // Update UUID inputs
      updateUuidInputs();

      // If current tab is manage, load the data
      const activeTab = document.querySelector('.tab-content:not(.hidden)');
      if (activeTab && activeTab.id === 'manage-tab') {
        loadUuidData(storedUuid);
      }
    }

    // Load templates if clash tab is active
    const activeTab = document.querySelector('.tab-content:not(.hidden)');
    if (activeTab && activeTab.id === 'clash-tab') {
      loadTemplates();
    }

    // 初始化Lucide图标
    lucide.createIcons();
  
    // 移动端触摸优化：防止双击缩放
    let lastTouchEnd = 0;
    document.addEventListener('touchend', function(event) {
      const now = Date.now();
      if (now - lastTouchEnd <= 300) {
        event.preventDefault();
      }
      lastTouchEnd = now;
    }, false);
  
    // 移动端优化：防止300ms点击延迟
    if ('touchAction' in document.documentElement.style) {
      document.body.style.touchAction = 'manipulation';
    }
  });
</script>
</body>
</html>
`
