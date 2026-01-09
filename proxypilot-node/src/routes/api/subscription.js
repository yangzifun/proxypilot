const express = require('express');
const router = express.Router();
const SubscriptionService = require('../../services/subscriptionService');
const ConfigService = require('../../services/configService');

// 获取Clash订阅
router.get('/:uuid/clash', async (req, res) => {
  try {
    const { uuid } = req.params;
    const { template } = req.query;
    
    const result = await SubscriptionService.generateClashConfig(uuid, template);
    
    if (result.success) {
      // 记录访问日志
      await ConfigService.logAccess(
        uuid,
        'subscription',
        req.ip,
        req.headers['user-agent']
      );
      
      res.set({
        'Content-Type': 'text/yaml; charset=utf-8',
        'Content-Disposition': 'inline',
        'Cache-Control': 'no-cache, no-store, must-revalidate',
        'Pragma': 'no-cache',
        'Expires': '0'
      });
      
      res.send(result.config);
    } else {
      res.status(404).json({
        error: result.error || '未找到配置',
        success: false,
        uuid
      });
    }
  } catch (error) {
    console.error('生成Clash配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取原始订阅
router.get('/:uuid/raw', async (req, res) => {
  try {
    const { uuid } = req.params;
    
    const result = await SubscriptionService.generateRawSubscription(uuid);
    
    if (result.success) {
      // 记录访问日志
      await ConfigService.logAccess(
        uuid,
        'subscription',
        req.ip,
        req.headers['user-agent']
      );
      
      res.set({
        'Content-Type': 'text/plain; charset=utf-8',
        'Content-Disposition': 'inline',
        'Cache-Control': 'no-cache, no-store, must-revalidate',
        'Pragma': 'no-cache',
        'Expires': '0'
      });
      
      res.send(result.subscription);
    } else {
      res.status(404).json({
        error: result.error || '未找到配置',
        success: false,
        uuid
      });
    }
  } catch (error) {
    console.error('生成原始订阅失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取订阅信息
router.get('/:uuid/info', async (req, res) => {
  try {
    const { uuid } = req.params;
    
    const stats = await SubscriptionService.getSubscriptionStats(uuid);
    
    res.json({
      success: true,
      uuid,
      ...stats
    });
  } catch (error) {
    console.error('获取订阅信息失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 批量获取订阅
router.get('/batch/:uuids/:format', async (req, res) => {
  try {
    const { uuids, format } = req.params;
    const uuidList = uuids.split(',');
    const { template } = req.query;
    
    if (uuidList.length === 0) {
      return res.status(400).json({
        error: '缺少UUID列表',
        success: false
      });
    }
    
    const results = await SubscriptionService.batchGenerateConfigs(uuidList, format);
    
    // 记录访问日志
    const logPromises = uuidList.map(uuid => 
      ConfigService.logAccess(
        uuid,
        'subscription',
        req.ip,
        req.headers['user-agent']
      )
    );
    await Promise.all(logPromises);
    
    // 如果有多个UUID，合并配置
    if (format === 'clash') {
      const successfulResults = results.filter(r => r.success);
      if (successfulResults.length === 0) {
        return res.status(404).json({
          error: '所有UUID均未找到配置',
          success: false,
          results
        });
      }
      
      // 合并代理配置
      let allProxies = [];
      successfulResults.forEach(result => {
        const config = result.config || '';
        // 简化处理：提取代理部分
        const proxiesMatch = config.match(/proxies:\s*\n([\s\S]*?)\n\s*\n/);
        if (proxiesMatch) {
          const proxies = proxiesMatch[1];
          allProxies.push(proxies);
        }
      });
      
      // 生成合并的配置
      const mergedConfig = SubscriptionService.getDefaultClashTemplate()
        .replace('{{proxies}}', allProxies.join('\n\n'))
        .replace('{{proxy_names}}', successfulResults.map((_, i) => `节点${i + 1}`).join(', '))
        .replace('{{timestamp}}', new Date().toISOString())
        .replace('{{uuid}}', 'batch-subscription');
      
      res.set({
        'Content-Type': 'text/yaml; charset=utf-8',
        'Content-Disposition': 'inline',
        'Cache-Control': 'no-cache, no-store, must-revalidate',
        'Pragma': 'no-cache',
        'Expires': '0'
      });
      
      res.send(mergedConfig);
    } else if (format === 'raw') {
      // 对于原始格式，返回JSON
      res.json({
        success: true,
        results,
        summary: {
          total: results.length,
          success: results.filter(r => r.success).length,
          failed: results.filter(r => !r.success).length
        }
      });
    } else {
      res.status(400).json({
        error: '不支持的文件格式',
        success: false
      });
    }
  } catch (error) {
    console.error('批量获取订阅失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 生成订阅链接
router.post('/generate-links', async (req, res) => {
  try {
    const { uuids, format = 'clash', baseUrl } = req.body;
    
    if (!Array.isArray(uuids) || uuids.length === 0) {
      return res.status(400).json({
        error: '缺少UUID列表',
        success: false
      });
    }
    
    const serverBaseUrl = baseUrl || `${req.protocol}://${req.get('host')}`;
    const links = uuids.map(uuid => ({
      uuid,
      url: SubscriptionService.generateSubscriptionUrl(serverBaseUrl, uuid, format),
      format
    }));
    
    res.json({
      success: true,
      links,
      batch_url: SubscriptionService.generateBatchSubscriptionUrl(serverBaseUrl, uuids, format)
    });
  } catch (error) {
    console.error('生成订阅链接失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 验证订阅有效性
router.get('/validate/:uuid', async (req, res) => {
  try {
    const { uuid } = req.params;
    
    const validation = await SubscriptionService.validateUUID(uuid);
    
    res.json({
      success: true,
      uuid,
      ...validation
    });
  } catch (error) {
    console.error('验证订阅失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取支持的格式列表
router.get('/formats', (req, res) => {
  res.json({
    success: true,
    formats: [
      {
        id: 'clash',
        name: 'Clash',
        description: 'Clash 客户端配置',
        content_type: 'text/yaml',
        extension: '.yaml'
      },
      {
        id: 'raw',
        name: '原始订阅',
        description: 'Base64编码的原始VMess配置',
        content_type: 'text/plain',
        extension: '.txt'
      }
    ]
  });
});

// 自动重定向到首选格式
router.get('/:uuid', async (req, res) => {
  try {
    const { uuid } = req.params;
    const { format = 'clash' } = req.query;
    
    // 默认重定向到Clash格式
    res.redirect(`/api/subscription/${uuid}/${format}`);
  } catch (error) {
    console.error('订阅重定向失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 健康检查端点
router.get('/health/:uuid', async (req, res) => {
  try {
    const { uuid } = req.params;
    
    const configs = await require('../../database/connection').db('configs')
      .where({ uuid })
      .count('id as count')
      .first();
    
    const lastAccess = await require('../../database/connection').db('config_access_logs')
      .where({ uuid })
      .max('created_at as last_access')
      .first();
    
    res.json({
      success: true,
      uuid,
      config_count: parseInt(configs.count),
      last_access: lastAccess.last_access ? new Date(lastAccess.last_access).toISOString() : null,
      status: 'active'
    });
  } catch (error) {
    console.error('健康检查失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

module.exports = router;