# 模板管理功能设计

## 现有功能分析

1. **数据库结构**：已存在 `config_templates` 表
   - id: INTEGER PRIMARY KEY AUTOINCREMENT
   - name: TEXT UNIQUE NOT NULL (模板名称)
   - url: TEXT NOT NULL (模板URL)
   - description: TEXT (模板描述)
   - is_default: INTEGER DEFAULT 0 (是否默认模板)
   - created_at, updated_at: 时间戳

2. **模板初始化数据**：已包含8个默认模板
   - full_config.yaml
   - lite_config.yaml
   - global_config.yaml
   - cn_config.yaml
   - config.yaml
   - geoip_config.yaml
   - load-balance_config.yaml
   - stream_config.yaml

3. **模板使用**：在subs_worker.js中使用 `fetchTemplateFromDB` 函数从数据库获取模板

## 需要实现的功能

### A. API接口 (mg_worker.js)
1. 获取模板列表 (GET /api/templates)
2. 获取单个模板详情 (GET /api/templates/{id})
3. 添加新模板 (POST /api/templates)
4. 更新模板 (PUT /api/templates/{id})
5. 删除模板 (DELETE /api/templates/{id})
6. 设置默认模板 (POST /api/templates/{id}/set-default)

### B. 管理页面 (mg_worker.js的管理后台)
1. 在导航栏添加"模板管理"标签页
2. 模板列表展示
3. 模板添加表单
4. 模板编辑功能
5. 模板删除功能
6. 设置默认模板功能

### C. 前端页面设计
1. 表格展示：名称、URL、描述、是否默认、创建时间、操作
2. 表单字段：
   - 模板名称（必填，唯一）
   - 模板URL（必填）
   - 模板描述（可选）
   - 是否设为默认模板（复选框）
3. 操作按钮：
   - 编辑
   - 删除
   - 设为默认

## 实现步骤

1. 在mg_worker.js的handleApi函数中添加模板相关的API路由
2. 实现模板管理的API处理函数
3. 在管理页面HTML中添加模板管理标签页
4. 添加模板管理的前端JavaScript逻辑
5. 测试功能
