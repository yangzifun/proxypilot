const express = require('express');
const router = express.Router();
const AdminService = require('../../services/adminService');
const { requireAdminAuth } = require('../../middleware/auth');

// 管理员登录
router.post('/login', async (req, res) => {
  try {
    const { username, password } = req.body;
    
    if (!username || !password) {
      return res.status(400).json({
        error: '用户名和密码不能为空',
        success: false
      });
    }
    
    const result = await AdminService.login(username, password);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(401).json(result);
    }
  } catch (error) {
    console.error('登录失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// MFA验证
router.post('/mfa/verify', requireAdminAuth, async (req, res) => {
  try {
    const { code, use_backup_code } = req.body;
    
    if (!code) {
      return res.status(400).json({
        error: '验证码不能为空',
        success: false
      });
    }
    
    const result = await AdminService.verifyMFA(
      req.user.sub,
      code,
      use_backup_code
    );
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(401).json(result);
    }
  } catch (error) {
    console.error('MFA验证失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 刷新token
router.post('/refresh-token', async (req, res) => {
  try {
    const { refreshToken } = req.body;
    
    if (!refreshToken) {
      return res.status(400).json({
        error: '刷新令牌不能为空',
        success: false
      });
    }
    
    const result = await AdminService.refreshToken(refreshToken);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(401).json(result);
    }
  } catch (error) {
    console.error('刷新token失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取系统统计信息（需要管理员权限）
router.get('/stats', requireAdminAuth, async (req, res) => {
  try {
    const result = await AdminService.getSystemStats();
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(500).json(result);
    }
  } catch (error) {
    console.error('获取系统统计失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取访问日志（需要管理员权限）
router.get('/access-logs', requireAdminAuth, async (req, res) => {
  try {
    const page = parseInt(req.query.page) || 1;
    const limit = parseInt(req.query.limit) || 50;
    const filters = {
      uuid: req.query.uuid,
      query_type: req.query.query_type,
      start_date: req.query.start_date,
      end_date: req.query.end_date
    };
    
    const result = await AdminService.getAccessLogs(page, limit, filters);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(500).json(result);
    }
  } catch (error) {
    console.error('获取访问日志失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取用户信息（需要管理员权限）
router.get('/user-info', requireAdminAuth, async (req, res) => {
  try {
    const result = await AdminService.getUserInfo(req.user.sub);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(404).json(result);
    }
  } catch (error) {
    console.error('获取用户信息失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 修改密码（需要管理员权限）
router.post('/change-password', requireAdminAuth, async (req, res) => {
  try {
    const { old_password, new_password } = req.body;
    
    if (!old_password || !new_password) {
      return res.status(400).json({
        error: '旧密码和新密码不能为空',
        success: false
      });
    }
    
    const result = await AdminService.changePassword(
      req.user.sub,
      old_password,
      new_password
    );
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('修改密码失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 设置MFA（需要管理员权限）
router.get('/mfa/setup', requireAdminAuth, async (req, res) => {
  try {
    const result = await AdminService.setupMFA(req.user.sub);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('设置MFA失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 完成MFA设置（需要管理员权限）
router.post('/mfa/complete-setup', requireAdminAuth, async (req, res) => {
  try {
    const { code, secret } = req.body;
    
    if (!code || !secret) {
      return res.status(400).json({
        error: '验证码和密钥不能为空',
        success: false
      });
    }
    
    const result = await AdminService.completeMFASetup(
      req.user.sub,
      code,
      secret
    );
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('完成MFA设置失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 禁用MFA（需要管理员权限）
router.post('/mfa/disable', requireAdminAuth, async (req, res) => {
  try {
    const { password } = req.body;
    
    if (!password) {
      return res.status(400).json({
        error: '密码不能为空',
        success: false
      });
    }
    
    const result = await AdminService.disableMFA(req.user.sub, password);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('禁用MFA失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取备份码（需要管理员权限）
router.get('/mfa/backup-codes', requireAdminAuth, async (req, res) => {
  try {
    const result = await AdminService.getBackupCodes(req.user.sub);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(500).json(result);
    }
  } catch (error) {
    console.error('获取备份码失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 重新生成备份码（需要管理员权限）
router.post('/mfa/regenerate-backup-codes', requireAdminAuth, async (req, res) => {
  try {
    const { password } = req.body;
    
    if (!password) {
      return res.status(400).json({
        error: '密码不能为空',
        success: false
      });
    }
    
    const result = await AdminService.regenerateBackupCodes(
      req.user.sub,
      password
    );
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('重新生成备份码失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 导出配置数据（需要管理员权限）
router.get('/export/configs', requireAdminAuth, async (req, res) => {
  try {
    const { uuid } = req.query;
    
    const result = await AdminService.exportConfigData(uuid);
    
    if (result.success) {
      res.set({
        'Content-Type': 'application/json',
        'Content-Disposition': `attachment; filename="configs-export-${Date.now()}.json"`
      });
      res.send(JSON.stringify(result, null, 2));
    } else {
      res.status(500).json(result);
    }
  } catch (error) {
    console.error('导出配置数据失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 导入配置数据（需要管理员权限）
router.post('/import/configs', requireAdminAuth, async (req, res) => {
  try {
    const { configs } = req.body;
    
    if (!Array.isArray(configs)) {
      return res.status(400).json({
        error: '配置数据格式错误',
        success: false
      });
    }
    
    const result = await AdminService.importConfigData(configs);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(400).json(result);
    }
  } catch (error) {
    console.error('导入配置数据失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 清除访问日志（需要管理员权限）
router.post('/clear-logs', requireAdminAuth, async (req, res) => {
  try {
    const daysToKeep = parseInt(req.body.days) || 30;
    
    const result = await AdminService.clearAccessLogs(daysToKeep);
    
    if (result.success) {
      res.json(result);
    } else {
      res.status(500).json(result);
    }
  } catch (error) {
    console.error('清除访问日志失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 验证管理员token
router.get('/validate', requireAdminAuth, (req, res) => {
  res.json({
    success: true,
    user: req.user,
    message: 'Token有效'
  });
});

module.exports = router;