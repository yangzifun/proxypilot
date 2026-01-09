const { db } = require('../database/connection');
const fetch = require('node-fetch');

class IPUpdateService {
  // 从 hostmonit 获取 Cloudflare IP
  static async fetchFromHostmonit(type = 'v4') {
    const apiKey = process.env.HOSTMONIT_API_KEY || 'iDetkOys';
    const url = 'https://api.hostmonit.com/get_optimization_ip';
    
    try {
      const response = await fetch(url, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Referer': 'https://stock.hostmonit.com/',
          'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'
        },
        body: JSON.stringify({
          key: apiKey,
          type: type === 'v4' ? 'v4' : 'v6'
        })
      });
      
      if (!response.ok) {
        throw new Error(`API request failed: ${response.status}`);
      }
      
      const data = await response.json();
      
      if (data.code === 200 && data.info === 'success') {
        return {
          success: true,
          ips: data.data || [],
          source: 'hostmonit',
          type: type,
          count: data.data ? data.data.length : 0
        };
      } else {
        throw new Error(`API error: ${data.info || 'Unknown error'}`);
      }
    } catch (error) {
      return {
        success: false,
        error: error.message,
        source: 'hostmonit',
        type: type
      };
    }
  }

  // 从 vps789 获取 Cloudflare IP
  static async fetchFromVPS789() {
    const url = 'https://cf.vps789.com/';
    
    try {
      const response = await fetch(url, {
        headers: {
          'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'
        }
      });
      
      if (!response.ok) {
        throw new Error(`Request failed: ${response.status}`);
      }
      
      const html = await response.text();
      
      // 解析HTML获取IP列表（简化解析）
      const ipRegex = /(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})/g;
      const ips = [];
      let match;
      
      while ((match = ipRegex.exec(html)) !== null) {
        const ip = match[1];
        // 验证IP格式
        if (this.isValidIP(ip)) {
          ips.push(ip);
        }
      }
      
      // 去重
      const uniqueIPs = [...new Set(ips)];
      
      return {
        success: true,
        ips: uniqueIPs.map(ip => ({
          ip: ip,
          ip_type: 'v4',
          carrier: 'ALL', // vps789 不提供运营商信息
          source: 'vps789'
        })),
        source: 'vps789',
        type: 'v4',
        count: uniqueIPs.length
      };
    } catch (error) {
      return {
        success: false,
        error: error.message,
        source: 'vps789',
        type: 'v4'
      };
    }
  }

  // 验证IP地址
  static isValidIP(ip) {
    const ipv4Regex = /^(\d{1,3}\.){3}\d{1,3}$/;
    const ipv6Regex = /^([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}$/;
    
    if (ipv4Regex.test(ip)) {
      const parts = ip.split('.');
      return parts.every(part => {
        const num = parseInt(part, 10);
        return num >= 0 && num <= 255;
      });
    }
    
    return ipv6Regex.test(ip);
  }

  // 获取IP类型
  static getIPType(ip) {
    if (ip.includes('.')) return 'v4';
    if (ip.includes(':')) return 'v6';
    return 'unknown';
  }

  // 获取运营商信息（从IP识别）
  static getCarrierFromIP(ip) {
    // 这里可以扩展为更精确的运营商识别
    // 目前根据常见IP段进行简单判断
    const ipSegments = ip.split('.');
    
    // 中国移动
    if (ip.startsWith('223.') || ip.startsWith('39.')) {
      return 'CM';
    }
    
    // 中国电信
    if (ip.startsWith('27.') || ip.startsWith('36.')) {
      return 'CT';
    }
    
    // 中国联通
    if (ip.startsWith('42.') || ip.startsWith('58.')) {
      return 'CU';
    }
    
    // 其他情况
    return 'OTHER';
  }

  // 保存IP到数据库
  static async saveIPs(ips, source) {
    const results = [];
    const timestamp = Date.now();
    
    for (const ipData of ips) {
      try {
        // 检查IP是否已存在
        const existing = await db('cfips')
          .where({ ip: ipData.ip })
          .first();
        
        if (existing) {
          // 更新现有记录
          await db('cfips')
            .where({ ip: ipData.ip })
            .update({
              ip_type: ipData.ip_type || this.getIPType(ipData.ip),
              carrier: ipData.carrier || this.getCarrierFromIP(ipData.ip),
              source: source,
              updated_at: timestamp
            });
          
          results.push({
            ip: ipData.ip,
            action: 'updated',
            success: true
          });
        } else {
          // 插入新记录
          await db('cfips').insert({
            ip: ipData.ip,
            ip_type: ipData.ip_type || this.getIPType(ipData.ip),
            carrier: ipData.carrier || this.getCarrierFromIP(ipData.ip),
            source: source,
            created_at: timestamp,
            updated_at: timestamp
          });
          
          results.push({
            ip: ipData.ip,
            action: 'inserted',
            success: true
          });
        }
      } catch (error) {
        results.push({
          ip: ipData.ip,
          action: 'failed',
          success: false,
          error: error.message
        });
      }
    }
    
    return results;
  }

  // 执行IP更新任务
  static async runIpUpdateTask() {
    console.log('Starting IP update task...');
    
    const updateSettings = await this.getUpdateSettings();
    const results = {
      timestamp: new Date().toISOString(),
      sources: {},
      summary: {
        total: 0,
        success: 0,
        failed: 0
      }
    };
    
    // 检查全局启用设置
    if (!updateSettings.global_enabled) {
      console.log('IP update is globally disabled.');
      return {
        success: false,
        message: 'IP更新功能已全局禁用',
        results
      };
    }
    
    // 从 hostmonit 获取IPv4
    if (updateSettings.hostmonit_v4) {
      console.log('Fetching from hostmonit (IPv4)...');
      const hostmonitV4Result = await this.fetchFromHostmonit('v4');
      
      results.sources.hostmonit_v4 = {
        success: hostmonitV4Result.success,
        count: hostmonitV4Result.success ? hostmonitV4Result.count : 0,
        error: hostmonitV4Result.error
      };
      
      if (hostmonitV4Result.success && hostmonitV4Result.ips) {
        const saveResult = await this.saveIPs(
          hostmonitV4Result.ips.map(ip => ({
            ip: ip.ip,
            ip_type: 'v4',
            carrier: ip.line || 'ALL'
          })),
          'hostmonit_v4'
        );
        
        results.summary.total += saveResult.length;
        results.summary.success += saveResult.filter(r => r.success).length;
        results.summary.failed += saveResult.filter(r => !r.success).length;
      }
    }
    
    // 从 hostmonit 获取IPv6
    if (updateSettings.hostmonit_v6) {
      console.log('Fetching from hostmonit (IPv6)...');
      const hostmonitV6Result = await this.fetchFromHostmonit('v6');
      
      results.sources.hostmonit_v6 = {
        success: hostmonitV6Result.success,
        count: hostmonitV6Result.success ? hostmonitV6Result.count : 0,
        error: hostmonitV6Result.error
      };
      
      if (hostmonitV6Result.success && hostmonitV6Result.ips) {
        const saveResult = await this.saveIPs(
          hostmonitV6Result.ips.map(ip => ({
            ip: ip.ip,
            ip_type: 'v6',
            carrier: ip.line || 'ALL'
          })),
          'hostmonit_v6'
        );
        
        results.summary.total += saveResult.length;
        results.summary.success += saveResult.filter(r => r.success).length;
        results.summary.failed += saveResult.filter(r => !r.success).length;
      }
    }
    
    // 从 vps789 获取IP
    if (updateSettings.vps789) {
      console.log('Fetching from vps789...');
      const vps789Result = await this.fetchFromVPS789();
      
      results.sources.vps789 = {
        success: vps789Result.success,
        count: vps789Result.success ? vps789Result.count : 0,
        error: vps789Result.error
      };
      
      if (vps789Result.success && vps789Result.ips) {
        const saveResult = await this.saveIPs(vps789Result.ips, 'vps789');
        
        results.summary.total += saveResult.length;
        results.summary.success += saveResult.filter(r => r.success).length;
        results.summary.failed += saveResult.filter(r => !r.success).length;
      }
    }
    
    // 更新最后执行时间
    await this.updateLastExecutionTime();
    
    console.log('IP update task completed.');
    
    return {
      success: true,
      message: `IP更新完成，成功: ${results.summary.success}, 失败: ${results.summary.failed}, 总计: ${results.summary.total}`,
      results
    };
  }

  // 获取更新设置
  static async getUpdateSettings() {
    const settings = await db('auto_update_settings')
      .select('source', 'enabled')
      .whereIn('source', [
        'global_enabled',
        'hostmonit_v4',
        'hostmonit_v6',
        'vps789'
      ]);
    
    const result = {
      global_enabled: true,
      hostmonit_v4: true,
      hostmonit_v6: false,
      vps789: true
    };
    
    settings.forEach(setting => {
      result[setting.source] = setting.enabled === 1;
    });
    
    return result;
  }

  // 更新设置
  static async updateSettings(newSettings) {
    const updates = [];
    
    for (const [source, enabled] of Object.entries(newSettings)) {
      updates.push(
        db('auto_update_settings')
          .insert({
            source,
            enabled: enabled ? 1 : 0,
            updated_at: Date.now()
          })
          .onConflict('source')
          .merge()
      );
    }
    
    await Promise.all(updates);
    
    return {
      success: true,
      message: '更新设置已保存'
    };
  }

  // 更新最后执行时间
  static async updateLastExecutionTime() {
    await db('auto_update_settings')
      .insert({
        source: 'last_executed',
        enabled: 0,
        updated_at: Date.now()
      })
      .onConflict('source')
      .merge();
  }

  // 获取最后执行时间
  static async getLastExecutionTime() {
    const record = await db('auto_update_settings')
      .where({ source: 'last_executed' })
      .first();
    
    return record ? new Date(record.updated_at) : null;
  }

  // 获取IP列表
  static async getIPList(page = 1, limit = 50, filters = {}) {
    const offset = (page - 1) * limit;
    
    let query = db('cfips')
      .select('*')
      .orderBy('updated_at', 'desc');
    
    // 应用过滤器
    if (filters.ip_type) {
      query = query.where('ip_type', filters.ip_type);
    }
    
    if (filters.carrier) {
      query = query.where('carrier', filters.carrier);
    }
    
    if (filters.source) {
      query = query.where('source', filters.source);
    }
    
    if (filters.ip) {
      query = query.where('ip', 'like', `%${filters.ip}%`);
    }
    
    const ips = await query
      .limit(limit)
      .offset(offset);
    
    const total = await query.clone().count('id as count').first();
    
    return {
      success: true,
      data: ips.map(ip => ({
        ...ip,
        created_at: new Date(ip.created_at).toISOString(),
        updated_at: new Date(ip.updated_at).toISOString()
      })),
      pagination: {
        page,
        limit,
        total: parseInt(total.count),
        totalPages: Math.ceil(total.count / limit)
      }
    };
  }

  // 搜索IP
  static async searchIPs(query, page = 1, limit = 50) {
    const offset = (page - 1) * limit;
    
    const ips = await db('cfips')
      .where(function() {
        this.where('ip', 'like', `%${query}%`)
          .orWhere('carrier', 'like', `%${query}%`)
          .orWhere('source', 'like', `%${query}%`);
      })
      .orderBy('updated_at', 'desc')
      .limit(limit)
      .offset(offset);
    
    const total = await db('cfips')
      .where(function() {
        this.where('ip', 'like', `%${query}%`)
          .orWhere('carrier', 'like', `%${query}%`)
          .orWhere('source', 'like', `%${query}%`);
      })
      .count('id as count')
      .first();
    
    return {
      success: true,
      data: ips.map(ip => ({
        ...ip,
        created_at: new Date(ip.created_at).toISOString(),
        updated_at: new Date(ip.updated_at).toISOString()
      })),
      pagination: {
        page,
        limit,
        total: parseInt(total.count),
        totalPages: Math.ceil(total.count / limit)
      }
    };
  }

  // 删除IP
  static async deleteIP(id) {
    const ip = await db('cfips')
      .where({ id })
      .first();
    
    if (!ip) {
      return {
        success: false,
        error: 'IP not found'
      };
    }
    
    await db('cfips')
      .where({ id })
      .delete();
    
    return {
      success: true,
      message: `IP ${ip.ip} 已删除`
    };
  }

  // 批量删除IP
  static async batchDeleteIPs(ids) {
    const result = await db('cfips')
      .whereIn('id', ids)
      .delete();
    
    return {
      success: true,
      deletedCount: result,
      message: `已删除 ${result} 个IP`
    };
  }

  // 获取IP统计
  static async getIPStats() {
    const [
      totalCount,
      byType,
      byCarrier,
      bySource,
      recentUpdates
    ] = await Promise.all([
      db('cfips').count('id as count').first(),
      db('cfips')
        .select('ip_type', db.raw('COUNT(*) as count'))
        .groupBy('ip_type')
        .orderBy('count', 'desc'),
      db('cfips')
        .select('carrier', db.raw('COUNT(*) as count'))
        .groupBy('carrier')
        .orderBy('count', 'desc'),
      db('cfips')
        .select('source', db.raw('COUNT(*) as count'))
        .groupBy('source')
        .orderBy('count', 'desc'),
      db('cfips')
        .select(
          db.raw("DATE(FROM_UNIXTIME(updated_at/1000)) as date"),
          db.raw("COUNT(*) as count")
        )
        .where('updated_at', '>=', Date.now() - (30 * 24 * 60 * 60 * 1000))
        .groupByRaw("DATE(FROM_UNIXTIME(updated_at/1000))")
        .orderBy('date', 'desc')
        .limit(30)
    ]);
    
    return {
      success: true,
      stats: {
        total: parseInt(totalCount.count),
        by_type: byType.map(item => ({
          type: item.ip_type,
          count: parseInt(item.count)
        })),
        by_carrier: byCarrier.map(item => ({
          carrier: item.carrier,
          count: parseInt(item.count)
        })),
        by_source: bySource.map(item => ({
          source: item.source,
          count: parseInt(item.count)
        })),
        recent_updates: recentUpdates.map(item => ({
          date: item.date,
          count: parseInt(item.count)
        }))
      }
    };
  }

  // 手动测试IP连接
  static async testIPConnection(ip, port = 443) {
    const url = `https://${ip}:${port}`;
    
    try {
      const startTime = Date.now();
      const response = await fetch(url, {
        method: 'HEAD',
        timeout: 5000,
        headers: {
          'User-Agent': 'Mozilla/5.0'
        }
      });
      const endTime = Date.now();
      
      return {
        success: true,
        ip,
        port,
        latency: endTime - startTime,
        status: response.status,
        statusText: response.statusText,
        message: '连接成功'
      };
    } catch (error) {
      return {
        success: false,
        ip,
        port,
        error: error.message,
        message: '连接失败'
      };
    }
  }

  // 批量测试IP
  static async batchTestIPs(ips, port = 443) {
    const results = [];
    
    for (const ip of ips) {
      const result = await this.testIPConnection(ip, port);
      results.push(result);
      
      // 避免过快的请求
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    
    const successCount = results.filter(r => r.success).length;
    const failCount = results.filter(r => !r.success).length;
    
    return {
      success: true,
      results,
      summary: {
        total: results.length,
        success: successCount,
        failed: failCount,
        success_rate: (successCount / results.length * 100).toFixed(2)
      }
    };
  }
}

module.exports = IPUpdateService;