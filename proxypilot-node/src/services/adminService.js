const { db } = require('../database/connection');
const bcrypt = require('bcrypt');
const { TOTP, setupMFA, verifyMFACode, verifyAndSetupMFA } = require('../utils/totp');
const { generateAdminToken, generateRefreshToken, verifyRefreshToken } = require('../utils/jwt');

class AdminService {
  // 管理员登录
  static async login(username, password) {
    const user = await db('admin_users')
      .where({ username })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户名或密码错误'
      };
    }
    
    // 验证密码
    const isValid = await bcrypt.compare(password, user.password_hash);
    if (!isValid) {
      return {
        success: false,
        error: '用户名或密码错误'
      };
    }
    
    const requiresMfa = user.mfa_enabled === 1;
    
    if (requiresMfa) {
      // 需要MFA验证
      const token = generateAdminToken(username, true, false);
      
      return {
        success: true,
        requires_mfa: true,
        token,
        user: {
          username: user.username,
          mfa_enabled: true
        }
      };
    } else {
      // 直接登录成功
      const token = generateAdminToken(username, false, false);
      const refreshToken = generateRefreshToken(username);
      
      return {
        success: true,
        token,
        refreshToken,
        user: {
          username: user.username,
          mfa_enabled: false
        }
      };
    }
  }

  // MFA验证
  static async verifyMFA(username, code, useBackupCode = false) {
    const user = await db('admin_users')
      .where({ username })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户不存在'
      };
    }
    
    if (!user.mfa_enabled) {
      return {
        success: false,
        error: 'MFA未启用'
      };
    }
    
    let isValid = false;
    
    if (useBackupCode) {
      // 验证备份码
      const backupCode = await db('mfa_backup_codes')
        .where({
          username,
          code: code.toUpperCase()
        })
        .first();
      
      if (backupCode && backupCode.used === 0) {
        // 标记备份码为已使用
        await db('mfa_backup_codes')
          .where({ id: backupCode.id })
          .update({
            used: 1,
            used_at: Date.now()
          });
        
        // 更新最后备份登录时间
        await db('admin_users')
          .where({ username })
          .update({
            last_backup_login: Date.now()
          });
        
        isValid = true;
      }
    } else {
      // 验证TOTP码
      isValid = await verifyMFACode(user.mfa_secret, code);
      
      if (isValid) {
        // 更新最后MFA登录时间
        await db('admin_users')
          .where({ username })
          .update({
            last_mfa_login: Date.now()
          });
      }
    }
    
    if (!isValid) {
      return {
        success: false,
        error: useBackupCode ? '备份码无效' : '验证码无效'
      };
    }
    
    // 生成完整权限的token
    const token = generateAdminToken(username, true, true);
    const refreshToken = generateRefreshToken(username);
    
    return {
      success: true,
      token,
      refreshToken,
      user: {
        username: user.username,
        mfa_enabled: true,
        mfa_verified: true
      }
    };
  }

  // 刷新token
  static async refreshToken(refreshToken) {
    const payload = verifyRefreshToken(refreshToken);
    
    if (!payload) {
      return {
        success: false,
        error: '刷新令牌无效'
      };
    }
    
    const user = await db('admin_users')
      .where({ username: payload.sub })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户不存在'
      };
    }
    
    const requiresMfa = user.mfa_enabled === 1;
    const newToken = generateAdminToken(user.username, requiresMfa, false);
    const newRefreshToken = generateRefreshToken(user.username);
    
    return {
      success: true,
      token: newToken,
      refreshToken: newRefreshToken,
      user: {
        username: user.username,
        mfa_enabled: requiresMfa
      }
    };
  }

  // 设置MFA
  static async setupMFA(username) {
    const user = await db('admin_users')
      .where({ username })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户不存在'
      };
    }
    
    if (user.mfa_enabled) {
      return {
        success: false,
        error: 'MFA已启用'
      };
    }
    
    const setupData = await setupMFA(username);
    
    return {
      success: true,
      ...setupData
    };
  }

  // 完成MFA设置
  static async completeMFASetup(username, code, secret) {
    try {
      const result = await verifyAndSetupMFA(username, code, secret);
      
      if (result.success) {
        // 保存MFA密钥到数据库
        await db('admin_users')
          .where({ username })
          .update({
            mfa_enabled: 1,
            mfa_secret: secret
          });
        
        // 保存备份码
        const backupPromises = result.backupCodes.map(code => 
          db('mfa_backup_codes').insert({
            username,
            code,
            created_at: Date.now(),
            used: 0
          })
        );
        
        await Promise.all(backupPromises);
        
        return {
          success: true,
          backupCodes: result.backupCodes,
          message: 'MFA设置成功'
        };
      } else {
        return {
          success: false,
          error: '验证失败'
        };
      }
    } catch (error) {
      return {
        success: false,
        error: error.message
      };
    }
  }

  // 禁用MFA
  static async disableMFA(username, password) {
    const user = await db('admin_users')
      .where({ username })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户不存在'
      };
    }
    
    // 验证密码
    const isValid = await bcrypt.compare(password, user.password_hash);
    if (!isValid) {
      return {
        success: false,
        error: '密码错误'
      };
    }
    
    // 禁用MFA
    await db('admin_users')
      .where({ username })
      .update({
        mfa_enabled: 0,
        mfa_secret: null
      });
    
    // 删除备份码
    await db('mfa_backup_codes')
      .where({ username })
      .delete();
    
    return {
      success: true,
      message: 'MFA已禁用'
    };
  }

  // 获取备份码
  static async getBackupCodes(username) {
    const codes = await db('mfa_backup_codes')
      .where({ username })
      .select('code', 'used', 'used_at')
      .orderBy('created_at', 'desc');
    
    return {
      success: true,
      codes: codes.map(code => ({
        code: code.code,
        used: code.used === 1,
        usedAt: code.used_at > 0 ? new Date(code.used_at).toISOString() : null
      }))
    };
  }

  // 重新生成备份码
  static async regenerateBackupCodes(username, password) {
    const user = await db('admin_users')
      .where({ username })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户不存在'
      };
    }
    
    // 验证密码
    const isValid = await bcrypt.compare(password, user.password_hash);
    if (!isValid) {
      return {
        success: false,
        error: '密码错误'
      };
    }
    
    // 删除旧备份码
    await db('mfa_backup_codes')
      .where({ username })
      .delete();
    
    // 生成新备份码
    const newCodes = TOTP.generateBackupCodes(10);
    const backupPromises = newCodes.map(code => 
      db('mfa_backup_codes').insert({
        username,
        code,
        created_at: Date.now(),
        used: 0
      })
    );
    
    await Promise.all(backupPromises);
    
    return {
      success: true,
      codes: newCodes,
      message: '备份码已重新生成'
    };
  }

  // 修改密码
  static async changePassword(username, oldPassword, newPassword) {
    const user = await db('admin_users')
      .where({ username })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户不存在'
      };
    }
    
    // 验证旧密码
    const isValid = await bcrypt.compare(oldPassword, user.password_hash);
    if (!isValid) {
      return {
        success: false,
        error: '旧密码错误'
      };
    }
    
    // 生成新密码哈希
    const newHash = await bcrypt.hash(newPassword, 10);
    
    // 更新密码
    await db('admin_users')
      .where({ username })
      .update({
        password_hash: newHash
      });
    
    return {
      success: true,
      message: '密码修改成功'
    };
  }

  // 获取用户信息
  static async getUserInfo(username) {
    const user = await db('admin_users')
      .where({ username })
      .first();
    
    if (!user) {
      return {
        success: false,
        error: '用户不存在'
      };
    }
    
    const backupCodes = await db('mfa_backup_codes')
      .where({ username })
      .count('id as count')
      .first();
    
    const remainingBackupCodes = await db('mfa_backup_codes')
      .where({ username, used: 0 })
      .count('id as count')
      .first();
    
    return {
      success: true,
      user: {
        username: user.username,
        mfa_enabled: user.mfa_enabled === 1,
        created_at: new Date(user.created_at).toISOString(),
        last_mfa_login: user.last_mfa_login > 0 ? new Date(user.last_mfa_login).toISOString() : null,
        last_backup_login: user.last_backup_login > 0 ? new Date(user.last_backup_login).toISOString() : null,
        backup_codes: {
          total: parseInt(backupCodes.count),
          remaining: parseInt(remainingBackupCodes.count)
        }
      }
    };
  }

  // 获取系统统计信息
  static async getSystemStats() {
    const [
      configCount,
      userCount,
      accessLogCount,
      ipCount,
      domainCount
    ] = await Promise.all([
      db('configs').count('id as count').first(),
      db('configs').countDistinct('uuid as count').first(),
      db('config_access_logs').count('id as count').first(),
      db('cfips').count('id as count').first(),
      db.raw(`
        SELECT 
          (SELECT COUNT(*) FROM cf_domains) + 
          (SELECT COUNT(*) FROM edgeone_domains) as count
      `).first()
    ]);
    
    const recentAccess = await db('config_access_logs')
      .select(
        db.raw("DATE(created_at) as date"),
        db.raw("COUNT(*) as count")
      )
      .where('created_at', '>=', db.raw("NOW() - INTERVAL '7 days'"))
      .groupByRaw("DATE(created_at)")
      .orderBy('date', 'desc')
      .limit(7);
    
    const popularUUIDs = await db('config_access_logs')
      .select('uuid', db.raw('COUNT(*) as count'))
      .groupBy('uuid')
      .orderBy('count', 'desc')
      .limit(10);
    
    return {
      success: true,
      stats: {
        configs: parseInt(configCount.count),
        unique_users: parseInt(userCount.count),
        access_logs: parseInt(accessLogCount.count),
        ips: parseInt(ipCount.count),
        domains: parseInt(domainCount.rows[0]?.count || 0),
        recent_access: recentAccess.map(day => ({
          date: day.date,
          count: parseInt(day.count)
        })),
        popular_uuids: popularUUIDs.map(item => ({
          uuid: item.uuid,
          access_count: parseInt(item.count)
        }))
      }
    };
  }

  // 获取访问日志
  static async getAccessLogs(page = 1, limit = 50, filters = {}) {
    const offset = (page - 1) * limit;
    
    let query = db('config_access_logs')
      .select('*')
      .orderBy('created_at', 'desc');
    
    // 应用过滤器
    if (filters.uuid) {
      query = query.where('uuid', 'like', `%${filters.uuid}%`);
    }
    
    if (filters.query_type) {
      query = query.where('query_type', filters.query_type);
    }
    
    if (filters.start_date) {
      query = query.where('created_at', '>=', new Date(filters.start_date));
    }
    
    if (filters.end_date) {
      query = query.where('created_at', '<=', new Date(filters.end_date));
    }
    
    const logs = await query
      .limit(limit)
      .offset(offset);
    
    const total = await query.clone().count('id as count').first();
    
    return {
      success: true,
      logs: logs.map(log => ({
        ...log,
        created_at: log.created_at.toISOString()
      })),
      pagination: {
        page,
        limit,
        total: parseInt(total.count),
        totalPages: Math.ceil(total.count / limit)
      }
    };
  }

  // 清除访问日志
  static async clearAccessLogs(daysToKeep = 30) {
    const cutoffDate = new Date();
    cutoffDate.setDate(cutoffDate.getDate() - daysToKeep);
    
    const result = await db('config_access_logs')
      .where('created_at', '<', cutoffDate)
      .delete();
    
    return {
      success: true,
      deletedCount: result,
      message: `已清除 ${result} 条旧日志记录`
    };
  }

  // 导出配置数据
  static async exportConfigData(uuid = null) {
    let configs;
    
    if (uuid) {
      configs = await db('configs')
        .where({ uuid })
        .select('*');
    } else {
      configs = await db('configs')
        .select('*')
        .limit(1000); // 限制导出数量
    }
    
    // 处理meta字段
    const processedConfigs = configs.map(config => ({
      ...config,
      meta: config.meta ? JSON.parse(config.meta) : {},
      created_at: new Date(config.created_at).toISOString(),
      updated_at: new Date(config.updated_at).toISOString()
    }));
    
    return {
      success: true,
      data: processedConfigs,
      count: processedConfigs.length,
      export_time: new Date().toISOString()
    };
  }

  // 导入配置数据
  static async importConfigData(configs) {
    const results = [];
    
    for (const config of configs) {
      try {
        // 验证必要字段
        if (!config.uuid || !config.meta) {
          results.push({
            success: false,
            error: '缺少必要字段',
            config
          });
          continue;
        }
        
        // 插入数据库
        const inserted = await db('configs').insert({
          uuid: config.uuid,
          ip: config.ip || '',
          domain: config.domain || '',
          port: config.port || 0,
          meta: typeof config.meta === 'string' ? config.meta : JSON.stringify(config.meta),
          created_at: config.created_at ? new Date(config.created_at).getTime() : Date.now(),
          updated_at: config.updated_at ? new Date(config.updated_at).getTime() : Date.now()
        }).returning('*');
        
        results.push({
          success: true,
          config: inserted[0]
        });
      } catch (error) {
        results.push({
          success: false,
          error: error.message,
          config
        });
      }
    }
    
    const successCount = results.filter(r => r.success).length;
    const failCount = results.filter(r => !r.success).length;
    
    return {
      success: true,
      results,
      summary: {
        total: results.length,
        success: successCount,
        failed: failCount
      }
    };
  }
}

module.exports = AdminService;