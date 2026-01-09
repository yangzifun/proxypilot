const { db } = require('../database/connection');

class ConfigService {
  // 从 worker.js 迁移的协议解析函数
  
  // 解析 VMess 配置
  static parseVMess(configStr) {
    try {
      const config = JSON.parse(configStr);
      return {
        protocol: 'vmess',
        ps: config.ps || '',
        add: config.add || '',
        port: config.port || '',
        id: config.id || '',
        aid: config.aid || '',
        net: config.net || '',
        type: config.type || '',
        host: config.host || '',
        path: config.path || '',
        tls: config.tls || '',
        sni: config.sni || ''
      };
    } catch (error) {
      throw new Error('Invalid VMess configuration');
    }
  }

  // 解析 VLESS 配置
  static parseVLESS(configStr) {
    try {
      const config = JSON.parse(configStr);
      return {
        protocol: 'vless',
        ps: config.ps || '',
        add: config.add || '',
        port: config.port || '',
        id: config.id || '',
        flow: config.flow || '',
        net: config.net || '',
        type: config.type || '',
        host: config.host || '',
        path: config.path || '',
        tls: config.tls || '',
        sni: config.sni || '',
        fp: config.fp || ''
      };
    } catch (error) {
      throw new Error('Invalid VLESS configuration');
    }
  }

  // 解析 Trojan 配置
  static parseTrojan(configStr) {
    const match = configStr.match(/trojan:\/\/([^@]+)@([^:]+):(\d+)(?:\?([^#]+))?#(.+)/);
    if (!match) throw new Error('Invalid Trojan configuration');
    
    const [, password, address, port, query, remark] = match;
    const params = new URLSearchParams(query || '');
    
    return {
      protocol: 'trojan',
      password,
      address,
      port: parseInt(port),
      remark: decodeURIComponent(remark || ''),
      sni: params.get('sni') || '',
      type: params.get('type') || '',
      host: params.get('host') || '',
      path: params.get('path') || '',
      encryption: params.get('encryption') || ''
    };
  }

  // 解析协议配置
  static parseProtocolConfig(configStr) {
    if (configStr.startsWith('vmess://')) {
      const decoded = Buffer.from(configStr.substring(8), 'base64').toString();
      return this.parseVMess(decoded);
    } else if (configStr.startsWith('vless://')) {
      const decoded = configStr.substring(8);
      return this.parseVLESS(decodeURIComponent(decoded));
    } else if (configStr.startsWith('trojan://')) {
      return this.parseTrojan(configStr);
    } else if (configStr.startsWith('ss://')) {
      // Shadowsocks 解析
      return { protocol: 'ss', raw: configStr };
    } else if (configStr.startsWith('hysteria2://')) {
      // Hysteria2 解析
      return { protocol: 'hysteria2', raw: configStr };
    } else {
      throw new Error('Unsupported protocol');
    }
  }

  // 创建配置
  static async createConfig(uuid, configData) {
    try {
      const parsedConfig = this.parseProtocolConfig(configData);
      
      const result = await db('configs').insert({
        uuid,
        ip: parsedConfig.add || parsedConfig.address || '',
        domain: '',
        port: parsedConfig.port || 0,
        meta: JSON.stringify(parsedConfig),
        created_at: Date.now(),
        updated_at: Date.now()
      }).returning('*');
      
      return {
        success: true,
        config: result[0]
      };
    } catch (error) {
      return {
        success: false,
        error: error.message
      };
    }
  }

  // 批量创建配置
  static async batchCreateConfigs(uuid, configs) {
    const results = [];
    
    for (const configData of configs) {
      try {
        const result = await this.createConfig(uuid, configData);
        results.push(result);
      } catch (error) {
        results.push({
          success: false,
          error: error.message
        });
      }
    }
    
    return results;
  }

  // 获取配置列表
  static async getConfigs(uuid, page = 1, limit = 20) {
    const offset = (page - 1) * limit;
    
    const configs = await db('configs')
      .where({ uuid })
      .orderBy('created_at', 'desc')
      .limit(limit)
      .offset(offset);
    
    const total = await db('configs')
      .where({ uuid })
      .count('id as count')
      .first();
    
    return {
      success: true,
      data: configs.map(config => ({
        ...config,
        meta: config.meta ? JSON.parse(config.meta) : {}
      })),
      pagination: {
        page,
        limit,
        total: parseInt(total.count),
        totalPages: Math.ceil(total.count / limit)
      }
    };
  }

  // 获取单个配置
  static async getConfig(id) {
    const config = await db('configs')
      .where({ id })
      .first();
    
    if (!config) {
      return {
        success: false,
        error: 'Configuration not found'
      };
    }
    
    return {
      success: true,
      data: {
        ...config,
        meta: config.meta ? JSON.parse(config.meta) : {}
      }
    };
  }

  // 更新配置
  static async updateConfig(id, updates) {
    const config = await db('configs')
      .where({ id })
      .first();
    
    if (!config) {
      return {
        success: false,
        error: 'Configuration not found'
      };
    }
    
    // 如果有新的配置数据，重新解析
    if (updates.configData) {
      try {
        const parsedConfig = this.parseProtocolConfig(updates.configData);
        updates.meta = JSON.stringify(parsedConfig);
        
        if (parsedConfig.add || parsedConfig.address) {
          updates.ip = parsedConfig.add || parsedConfig.address;
        }
        if (parsedConfig.port) {
          updates.port = parsedConfig.port;
        }
      } catch (error) {
        return {
          success: false,
          error: error.message
        };
      }
    }
    
    updates.updated_at = Date.now();
    
    const result = await db('configs')
      .where({ id })
      .update(updates)
      .returning('*');
    
    return {
      success: true,
      config: result[0]
    };
  }

  // 删除配置
  static async deleteConfig(id) {
    const config = await db('configs')
      .where({ id })
      .first();
    
    if (!config) {
      return {
        success: false,
        error: 'Configuration not found'
      };
    }
    
    await db('configs')
      .where({ id })
      .delete();
    
    return {
      success: true,
      message: 'Configuration deleted successfully'
    };
  }

  // 批量删除配置
  static async batchDeleteConfigs(ids) {
    const result = await db('configs')
      .whereIn('id', ids)
      .delete();
    
    return {
      success: true,
      deletedCount: result,
      message: `Deleted ${result} configurations`
    };
  }

  // 获取 UUID 统计信息
  static async getUUIDStats(uuid) {
    const totalConfigs = await db('configs')
      .where({ uuid })
      .count('id as count')
      .first();
    
    const lastUpdated = await db('configs')
      .where({ uuid })
      .max('updated_at as last_updated')
      .first();
    
    return {
      uuid,
      totalConfigs: parseInt(totalConfigs.count),
      lastUpdated: lastUpdated.last_updated,
      created: await this.getFirstConfigDate(uuid)
    };
  }

  // 获取第一个配置的创建日期
  static async getFirstConfigDate(uuid) {
    const firstConfig = await db('configs')
      .where({ uuid })
      .orderBy('created_at', 'asc')
      .first();
    
    return firstConfig ? firstConfig.created_at : null;
  }

  // 搜索配置
  static async searchConfigs(uuid, query, page = 1, limit = 20) {
    const offset = (page - 1) * limit;
    
    const configs = await db('configs')
      .where({ uuid })
      .andWhere(function() {
        this.where('ip', 'like', `%${query}%`)
          .orWhere('meta', 'like', `%${query}%`)
          .orWhere('domain', 'like', `%${query}%`);
      })
      .orderBy('created_at', 'desc')
      .limit(limit)
      .offset(offset);
    
    const total = await db('configs')
      .where({ uuid })
      .andWhere(function() {
        this.where('ip', 'like', `%${query}%`)
          .orWhere('meta', 'like', `%${query}%`)
          .orWhere('domain', 'like', `%${query}%`);
      })
      .count('id as count')
      .first();
    
    return {
      success: true,
      data: configs.map(config => ({
        ...config,
        meta: config.meta ? JSON.parse(config.meta) : {}
      })),
      pagination: {
        page,
        limit,
        total: parseInt(total.count),
        totalPages: Math.ceil(total.count / limit)
      }
    };
  }

  // 记录访问日志
  static async logAccess(uuid, queryType, clientIp, userAgent) {
    await db('config_access_logs').insert({
      uuid,
      query_type: queryType,
      client_ip: clientIp,
      user_agent: userAgent,
      created_at: new Date()
    });
  }

  // 获取访问统计
  static async getAccessStats(uuid, days = 30) {
    const startDate = new Date();
    startDate.setDate(startDate.getDate() - days);
    
    const stats = await db('config_access_logs')
      .select(
        db.raw("DATE(created_at) as date"),
        db.raw("COUNT(*) as total"),
        db.raw("SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END) as subscription"),
        db.raw("SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END) as api_generation"),
        db.raw("COUNT(DISTINCT uuid) as unique_uuids")
      )
      .where('created_at', '>=', startDate)
      .groupByRaw("DATE(created_at)")
      .orderBy('date', 'desc');
    
    return stats;
  }
}

module.exports = ConfigService;