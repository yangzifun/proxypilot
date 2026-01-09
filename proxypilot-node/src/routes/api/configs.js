const express = require('express');
const router = express.Router();
const ConfigService = require('../../services/configService');

// 创建配置
router.post('/:uuid', async (req, res) => {
  try {
    const { uuid } = req.params;
    const { config } = req.body;
    
    if (!config) {
      return res.status(400).json({
        error: '缺少配置数据',
        success: false
      });
    }
    
    const result = await ConfigService.createConfig(uuid, config);
    
    // 记录访问日志
    await ConfigService.logAccess(
      uuid,
      'api-generation',
      req.ip,
      req.headers['user-agent']
    );
    
    if (result.success) {
      res.json({
        success: true,
        config: result.config
      });
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('创建配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 批量创建配置
router.post('/:uuid/batch', async (req, res) => {
  try {
    const { uuid } = req.params;
    const { configs } = req.body;
    
    if (!Array.isArray(configs) || configs.length === 0) {
      return res.status(400).json({
        error: '缺少配置数据',
        success: false
      });
    }
    
    const result = await ConfigService.batchCreateConfigs(uuid, configs);
    
    // 记录访问日志
    await ConfigService.logAccess(
      uuid,
      'api-generation',
      req.ip,
      req.headers['user-agent']
    );
    
    const successCount = result.filter(r => r.success).length;
    const failCount = result.filter(r => !r.success).length;
    
    res.json({
      success: true,
      results: result,
      summary: {
        total: configs.length,
        success: successCount,
        failed: failCount
      }
    });
  } catch (error) {
    console.error('批量创建配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取配置列表
router.get('/:uuid', async (req, res) => {
  try {
    const { uuid } = req.params;
    const page = parseInt(req.query.page) || 1;
    const limit = parseInt(req.query.limit) || 20;
    
    const result = await ConfigService.getConfigs(uuid, page, limit);
    
    // 记录访问日志
    await ConfigService.logAccess(
      uuid,
      'api-generation',
      req.ip,
      req.headers['user-agent']
    );
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(404).json(result);
    }
  } catch (error) {
    console.error('获取配置列表失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取单个配置
router.get('/config/:id', async (req, res) => {
  try {
    const { id } = req.params;
    const result = await ConfigService.getConfig(id);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(404).json(result);
    }
  } catch (error) {
    console.error('获取配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 更新配置
router.put('/config/:id', async (req, res) => {
  try {
    const { id } = req.params;
    const updates = req.body;
    
    const result = await ConfigService.updateConfig(id, updates);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('更新配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 删除配置
router.delete('/config/:id', async (req, res) => {
  try {
    const { id } = req.params;
    const result = await ConfigService.deleteConfig(id);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(404).json(result);
    }
  } catch (error) {
    console.error('删除配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 批量删除配置
router.post('/batch-delete', async (req, res) => {
  try {
    const { ids } = req.body;
    
    if (!Array.isArray(ids) || ids.length === 0) {
      return res.status(400).json({
        error: '缺少ID列表',
        success: false
      });
    }
    
    const result = await ConfigService.batchDeleteConfigs(ids);
    
    res.json(result);
  } catch (error) {
    console.error('批量删除配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取UUID统计信息
router.get('/:uuid/stats', async (req, res) => {
  try {
    const { uuid } = req.params;
    const result = await ConfigService.getUUIDStats(uuid);
    
    res.json({
      success: true,
      ...result
    });
  } catch (error) {
    console.error('获取统计信息失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 搜索配置
router.get('/:uuid/search', async (req, res) => {
  try {
    const { uuid } = req.params;
    const { q } = req.query;
    const page = parseInt(req.query.page) || 1;
    const limit = parseInt(req.query.limit) || 20;
    
    if (!q) {
      return res.status(400).json({
        error: '缺少搜索关键词',
        success: false
      });
    }
    
    const result = await ConfigService.searchConfigs(uuid, q, page, limit);
    
    res.json(result);
  } catch (error) {
    console.error('搜索配置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取访问统计
router.get('/:uuid/access-stats', async (req, res) => {
  try {
    const { uuid } = req.params;
    const days = parseInt(req.query.days) || 30;
    
    const stats = await ConfigService.getAccessStats(uuid, days);
    
    res.json({
      success: true,
      stats,
      uuid
    });
  } catch (error) {
    console.error('获取访问统计失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 验证UUID
router.get('/validate/:uuid', async (req, res) => {
  try {
    const { uuid } = req.params;
    
    const configs = await require('../../database/connection').db('configs')
      .where({ uuid })
      .first();
    
    res.json({
      success: true,
      exists: !!configs
    });
  } catch (error) {
    console.error('验证UUID失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

module.exports = router;