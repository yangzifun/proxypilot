const express = require('express');
const router = express.Router();
const { db } = require('../../database/connection');
const { requireAdminAuth } = require('../../middleware/auth');

// 获取模板列表
router.get('/', async (req, res) => {
  try {
    const templates = await db('config_templates')
      .select('*')
      .orderBy('is_default', 'desc')
      .orderBy('created_at', 'desc');
    
    res.json({
      success: true,
      data: templates.map(template => ({
        ...template,
        created_at: new Date(template.created_at).toISOString(),
        updated_at: new Date(template.updated_at).toISOString()
      }))
    });
  } catch (error) {
    console.error('获取模板列表失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取单个模板
router.get('/:id', async (req, res) => {
  try {
    const { id } = req.params;
    const template = await db('config_templates')
      .where({ id })
      .first();
    
    if (!template) {
      return res.status(404).json({
        error: '模板不存在',
        success: false
      });
    }
    
    res.json({
      success: true,
      data: {
        ...template,
        created_at: new Date(template.created_at).toISOString(),
        updated_at: new Date(template.updated_at).toISOString()
      }
    });
  } catch (error) {
    console.error('获取模板失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 获取默认模板
router.get('/default', async (req, res) => {
  try {
    const template = await db('config_templates')
      .where({ is_default: 1 })
      .first();
    
    if (!template) {
      // 如果没有默认模板，返回第一个模板
      const firstTemplate = await db('config_templates')
        .first();
      
      if (!firstTemplate) {
        return res.status(404).json({
          error: '没有可用的模板',
          success: false
        });
      }
      
      res.json({
        success: true,
        data: {
          ...firstTemplate,
          created_at: new Date(firstTemplate.created_at).toISOString(),
          updated_at: new Date(firstTemplate.updated_at).toISOString()
        }
      });
    } else {
      res.json({
        success: true,
        data: {
          ...template,
          created_at: new Date(template.created_at).toISOString(),
          updated_at: new Date(template.updated_at).toISOString()
        }
      });
    }
  } catch (error) {
    console.error('获取默认模板失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 创建模板（需要管理员权限）
router.post('/', requireAdminAuth, async (req, res) => {
  try {
    const { name, url, description, is_default } = req.body;
    
    if (!name || !url) {
      return res.status(400).json({
        error: '模板名称和URL不能为空',
        success: false
      });
    }
    
    const existing = await db('config_templates')
      .where({ name })
      .first();
    
    if (existing) {
      return res.status(400).json({
        error: '模板名称已存在',
        success: false
      });
    }
    
    // 如果设置为默认，先取消其他默认模板
    if (is_default) {
      await db('config_templates')
        .where({ is_default: 1 })
        .update({ is_default: 0 });
    }
    
    const template = await db('config_templates')
      .insert({
        name,
        url,
        description: description || '',
        is_default: is_default ? 1 : 0,
        created_at: Date.now(),
        updated_at: Date.now()
      })
      .returning('*');
    
    res.json({
      success: true,
      data: template[0]
    });
  } catch (error) {
    console.error('创建模板失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 更新模板（需要管理员权限）
router.put('/:id', requireAdminAuth, async (req, res) => {
  try {
    const { id } = req.params;
    const updates = req.body;
    
    const template = await db('config_templates')
      .where({ id })
      .first();
    
    if (!template) {
      return res.status(404).json({
        error: '模板不存在',
        success: false
      });
    }
    
    // 如果更新名称，检查是否重复
    if (updates.name && updates.name !== template.name) {
      const existing = await db('config_templates')
        .where({ name: updates.name })
        .first();
      
      if (existing) {
        return res.status(400).json({
          error: '模板名称已存在',
          success: false
        });
      }
    }
    
    // 如果设置为默认，先取消其他默认模板
    if (updates.is_default) {
      await db('config_templates')
        .where({ is_default: 1 })
        .update({ is_default: 0 });
    }
    
    updates.updated_at = Date.now();
    
    const updated = await db('config_templates')
      .where({ id })
      .update(updates)
      .returning('*');
    
    res.json({
      success: true,
      data: updated[0]
    });
  } catch (error) {
    console.error('更新模板失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 删除模板（需要管理员权限）
router.delete('/:id', requireAdminAuth, async (req, res) => {
  try {
    const { id } = req.params;
    
    const template = await db('config_templates')
      .where({ id })
      .first();
    
    if (!template) {
      return res.status(404).json({
        error: '模板不存在',
        success: false
      });
    }
    
    // 不能删除默认模板
    if (template.is_default) {
      return res.status(400).json({
        error: '不能删除默认模板',
        success: false
      });
    }
    
    await db('config_templates')
      .where({ id })
      .delete();
    
    res.json({
      success: true,
      message: '模板删除成功'
    });
  } catch (error) {
    console.error('删除模板失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

// 设置默认模板（需要管理员权限）
router.post('/:id/set-default', requireAdminAuth, async (req, res) => {
  try {
    const { id } = req.params;
    
    const template = await db('config_templates')
      .where({ id })
      .first();
    
    if (!template) {
      return res.status(404).json({
        error: '模板不存在',
        success: false
      });
    }
    
    // 取消所有默认模板
    await db('config_templates')
      .where({ is_default: 1 })
      .update({ is_default: 0 });
    
    // 设置新的默认模板
    await db('config_templates')
      .where({ id })
      .update({ is_default: 1, updated_at: Date.now() });
    
    res.json({
      success: true,
      message: '默认模板设置成功'
    });
  } catch (error) {
    console.error('设置默认模板失败:', error);
    res.status(500).json({
      error: '服务器错误',
      success: false
    });
  }
});

module.exports = router;