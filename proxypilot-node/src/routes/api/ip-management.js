const express = require('express');
const router = express.Router();
const IPUpdateService = require('../../services/ipUpdateService');
const { requireAdminAuth } = require('../../middleware/auth');

// 获取IP列表
router.get('/ips', async (req, res) => {
  try {
    const page = parseInt(req.query.page) || 1;
    const limit = parseInt(req.query.limit) || 50;
    const filters = {
      ip_type: req.query.ip_type,
      carrier: req.query.carrier,
      source: req.query.source,
      ip: req.query.ip
    };
    
    const result = await IPUpdateService.getIPList(page, limit, filters);
    
    res.json(result);
  } catch (error) {
    console.error('获取IP列表失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 搜索IP
router.get('/ips/search', async (req, res) => {
  try {
    const { q } = req.query;
    const page = parseInt(req.query.page) || 1;
    const limit = parseInt(req.query.limit) || 50;
    
    if (!q) {
      return res.status(400).json({
        error: '缺少搜索关键词',
        success: false
      });
    }
    
    const result = await IPUpdateService.searchIPs(q, page, limit);
    
    res.json(result);
  } catch (error) {
    console.error('搜索IP失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取IP统计
router.get('/stats', async (req, res) => {
  try {
    const result = await IPUpdateService.getIPStats();
    
    res.json(result);
  } catch (error) {
    console.error('获取IP统计失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取更新设置（需要管理员权限）
router.get('/settings', requireAdminAuth, async (req, res) => {
  try {
    const settings = await IPUpdateService.getUpdateSettings();
    const lastExecution = await IPUpdateService.getLastExecutionTime();
    
    res.json({
      success: true,
      settings,
      last_execution: lastExecution ? lastExecution.toISOString() : null
    });
  } catch (error) {
    console.error('获取更新设置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 更新设置（需要管理员权限）
router.post('/settings', requireAdminAuth, async (req, res) => {
  try {
    const newSettings = req.body;
    
    const result = await IPUpdateService.updateSettings(newSettings);
    
    res.json(result);
  } catch (error) {
    console.error('更新设置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 手动触发IP更新（需要管理员权限）
router.post('/update', requireAdminAuth, async (req, res) => {
  try {
    console.log('手动触发IP更新...');
    
    const result = await IPUpdateService.runIpUpdateTask();
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(500).json(result);
    }
  } catch (error) {
    console.error('手动触发IP更新失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 测试IP连接（需要管理员权限）
router.post('/test-ip', requireAdminAuth, async (req, res) => {
  try {
    const { ip, port } = req.body;
    
    if (!ip) {
      return res.status(400).json({
        error: 'IP地址不能为空',
        success: false
      });
    }
    
    const result = await IPUpdateService.testIPConnection(
      ip,
      port || 443
    );
    
    res.json(result);
  } catch (error) {
    console.error('测试IP连接失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 批量测试IP（需要管理员权限）
router.post('/batch-test-ips', requireAdminAuth, async (req, res) => {
  try {
    const { ips, port } = req.body;
    
    if (!Array.isArray(ips) || ips.length === 0) {
      return res.status(400).json({
        error: 'IP列表不能为空',
        success: false
      });
    }
    
    const result = await IPUpdateService.batchTestIPs(
      ips,
      port || 443
    );
    
    res.json(result);
  } catch (error) {
    console.error('批量测试IP失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 删除IP（需要管理员权限）
router.delete('/ip/:id', requireAdminAuth, async (req, res) => {
  try {
    const { id } = req.params;
    
    const result = await IPUpdateService.deleteIP(id);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(404).json(result);
    }
  } catch (error) {
    console.error('删除IP失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 批量删除IP（需要管理员权限）
router.post('/batch-delete-ips', requireAdminAuth, async (req, res) => {
  try {
    const { ids } = req.body;
    
    if (!Array.isArray(ids) || ids.length === 0) {
      return res.status(400).json({
        error: 'ID列表不能为空',
        success: false
      });
    }
    
    const result = await IPUpdateService.batchDeleteIPs(ids);
    
    res.json(result);
  } catch (error) {
    console.error('批量删除IP失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取源信息
router.get('/sources', (req, res) => {
  res.json({
    success: true,
    sources: [
      {
        id: 'hostmonit_v4',
        name: 'Hostmonit IPv4',
        description: '从 hostmonit.com 获取的IPv4地址',
        enabled_by_default: true
      },
      {
        id: 'hostmonit_v6',
        name: 'Hostmonit IPv6',
        description: '从 hostmonit.com 获取的IPv6地址',
        enabled_by_default: false
      },
      {
        id: 'vps789',
        name: 'VPS789',
        description: '从 cf.vps789.com 获取的IP地址',
        enabled_by_default: true
      }
    ]
  });
});

// 获取运营商列表
router.get('/carriers', (req, res) => {
  res.json({
    success: true,
    carriers: [
      { id: 'CM', name: '中国移动' },
      { id: 'CT', name: '中国电信' },
      { id: 'CU', name: '中国联通' },
      { id: 'OTHER', name: '其他运营商' },
      { id: 'ALL', name: '所有运营商' }
    ]
  });
});

// 获取IP类型列表
router.get('/ip-types', (req, res) => {
  res.json({
    success: true,
    ip_types: [
      { id: 'v4', name: 'IPv4' },
      { id: 'v6', name: 'IPv6' }
    ]
  });
});

module.exports = router;