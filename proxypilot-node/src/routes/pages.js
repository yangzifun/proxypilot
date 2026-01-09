const express = require('express');
const router = express.Router();
const path = require('path');

// 首页
router.get('/', (req, res) => {
  res.send(`
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ProxyPilot - 代理配置管理系统</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            max-width: 800px;
            margin: 0 auto;
            padding: 20px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            color: white;
        }
        .container {
            background: rgba(255, 255, 255, 0.1);
            backdrop-filter: blur(10px);
            border-radius: 20px;
            padding: 40px;
            box-shadow: 0 8px 32px rgba(0, 0, 0, 0.2);
        }
        h1 {
            text-align: center;
            margin-bottom: 30px;
            font-size: 2.5em;
            text-shadow: 2px 2px 4px rgba(0, 0, 0, 0.3);
        }
        .card {
            background: rgba(255, 255, 255, 0.15);
            border-radius: 15px;
            padding: 25px;
            margin-bottom: 20px;
            border: 1px solid rgba(255, 255, 255, 0.2);
        }
        h2 {
            margin-top: 0;
            color: #fff;
            font-size: 1.5em;
        }
        .feature-list {
            list-style: none;
            padding: 0;
        }
        .feature-list li {
            padding: 10px 0;
            border-bottom: 1px solid rgba(255, 255, 255, 0.1);
            display: flex;
            align-items: center;
        }
        .feature-list li:before {
            content: "✓";
            color: #4CAF50;
            font-weight: bold;
            margin-right: 10px;
            font-size: 1.2em;
        }
        .button {
            display: inline-block;
            background: linear-gradient(135deg, #4CAF50, #45a049);
            color: white;
            padding: 12px 24px;
            border-radius: 50px;
            text-decoration: none;
            font-weight: bold;
            margin: 10px 5px;
            border: none;
            cursor: pointer;
            transition: transform 0.3s, box-shadow 0.3s;
        }
        .button:hover {
            transform: translateY(-2px);
            box-shadow: 0 5px 15px rgba(0, 0, 0, 0.3);
        }
        .button-secondary {
            background: linear-gradient(135deg, #2196F3, #1976D2);
        }
        .button-danger {
            background: linear-gradient(135deg, #f44336, #d32f2f);
        }
        .api-info {
            background: rgba(0, 0, 0, 0.2);
            padding: 15px;
            border-radius: 10px;
            font-family: monospace;
            word-break: break-all;
            margin: 10px 0;
        }
        .endpoint {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin: 5px 0;
        }
        .method {
            background: #4CAF50;
            color: white;
            padding: 3px 8px;
            border-radius: 4px;
            font-size: 0.8em;
            font-weight: bold;
        }
        .method.get { background: #2196F3; }
        .method.post { background: #FF9800; }
        .method.put { background: #9C27B0; }
        .method.delete { background: #f44336; }
    </style>
</head>
<body>
    <div class="container">
        <h1>🚀 ProxyPilot</h1>
        
        <div class="card">
            <h2>✨ 功能特性</h2>
            <ul class="feature-list">
                <li>代理配置管理 - 支持 VMess、VLESS、Trojan、Shadowsocks 等协议</li>
                <li>订阅链接生成 - 自动生成 Clash、原始订阅等格式</li>
                <li>IP 自动更新 - 定时从多个源获取 Cloudflare 优选 IP</li>
                <li>MFA 双重验证 - 增强管理员账户安全性</li>
                <li>实时访问统计 - 监控配置使用情况和访问日志</li>
                <li>API 接口 - 完整的 RESTful API 支持</li>
            </ul>
        </div>

        <div class="card">
            <h2>📊 系统状态</h2>
            <div style="display: flex; justify-content: space-between; flex-wrap: wrap;">
                <div style="text-align: center; margin: 10px;">
                    <div style="font-size: 2em; font-weight: bold;">✅</div>
                    <div>运行正常</div>
                </div>
                <div style="text-align: center; margin: 10px;">
                    <div style="font-size: 2em; font-weight: bold;">3000</div>
                    <div>服务端口</div>
                </div>
                <div style="text-align: center; margin: 10px;">
                    <div style="font-size: 2em; font-weight: bold;">📈</div>
                    <div>在线状态</div>
                </div>
            </div>
        </div>

        <div class="card">
            <h2>🔗 快速链接</h2>
            <div>
                <a href="/admin" class="button">🔐 管理后台</a>
                <a href="/health" class="button">🩺 健康检查</a>
                <a href="/api-docs" class="button">📚 API 文档</a>
            </div>
        </div>

        <div class="card">
            <h2>🔌 API 接口示例</h2>
            <div class="api-info">
                <div class="endpoint">
                    <span>创建配置</span>
                    <span class="method post">POST</span>
                    <span>/api/configs/:uuid</span>
                </div>
                <div class="endpoint">
                    <span>获取订阅</span>
                    <span class="method get">GET</span>
                    <span>/api/subscription/:uuid/clash</span>
                </div>
                <div class="endpoint">
                    <span>获取配置列表</span>
                    <span class="method get">GET</span>
                    <span>/api/configs/:uuid</span>
                </div>
                <div class="endpoint">
                    <span>更新配置</span>
                    <span class="method put">PUT</span>
                    <span>/api/configs/config/:id</span>
                </div>
                <div class="endpoint">
                    <span>删除配置</span>
                    <span class="method delete">DELETE</span>
                    <span>/api/configs/config/:id</span>
                </div>
            </div>
            <p style="font-size: 0.9em; opacity: 0.9; margin-top: 10px;">
                * 详细 API 文档请访问 <a href="/api-docs" style="color: #4CAF50;">/api-docs</a>
            </p>
        </div>

        <div class="card">
            <h2>⚡ 立即开始</h2>
            <div style="text-align: center;">
                <a href="/admin" class="button button-secondary">🚀 登录管理后台</a>
                <a href="https://github.com/yangzifun/proxypilot" target="_blank" class="button">📖 查看文档</a>
            </div>
        </div>
    </div>
</body>
</html>
  `);
});

// 管理后台页面
router.get('/admin', (req, res) => {
  res.send(`
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ProxyPilot - 管理后台</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            max-width: 600px;
            margin: 0 auto;
            padding: 40px 20px;
            background: linear-gradient(135deg, #f5f7fa 0%, #c3cfe2 100%);
            min-height: 100vh;
        }
        .login-container {
            background: white;
            border-radius: 20px;
            padding: 40px;
            box-shadow: 0 10px 30px rgba(0, 0, 0, 0.1);
        }
        h1 {
            text-align: center;
            color: #333;
            margin-bottom: 30px;
            font-size: 2em;
        }
        .logo {
            text-align: center;
            font-size: 3em;
            margin-bottom: 20px;
        }
        .form-group {
            margin-bottom: 20px;
        }
        label {
            display: block;
            margin-bottom: 8px;
            color: #555;
            font-weight: 500;
        }
        input {
            width: 100%;
            padding: 12px 15px;
            border: 2px solid #e0e0e0;
            border-radius: 10px;
            font-size: 16px;
            transition: border-color 0.3s;
            box-sizing: border-box;
        }
        input:focus {
            outline: none;
            border-color: #4CAF50;
        }
        .btn {
            width: 100%;
            padding: 14px;
            background: linear-gradient(135deg, #4CAF50, #45a049);
            color: white;
            border: none;
            border-radius: 10px;
            font-size: 16px;
            font-weight: bold;
            cursor: pointer;
            transition: transform 0.3s, box-shadow 0.3s;
        }
        .btn:hover {
            transform: translateY(-2px);
            box-shadow: 0 5px 15px rgba(76, 175, 80, 0.4);
        }
        .message {
            padding: 15px;
            border-radius: 10px;
            margin: 20px 0;
            text-align: center;
            font-weight: bold;
        }
        .message.info {
            background: #e3f2fd;
            color: #1976d2;
            border: 1px solid #bbdefb;
        }
        .message.success {
            background: #e8f5e9;
            color: #2e7d32;
            border: 1px solid #c8e6c9;
        }
        .message.warning {
            background: #fff3e0;
            color: #f57c00;
            border: 1px solid #ffe0b2;
        }
        .back-link {
            text-align: center;
            margin-top: 20px;
        }
        .back-link a {
            color: #666;
            text-decoration: none;
            font-size: 0.9em;
        }
        .back-link a:hover {
            color: #4CAF50;
        }
    </style>
</head>
<body>
    <div class="login-container">
        <div class="logo">🔐</div>
        <h1>ProxyPilot 管理登录</h1>
        
        <div class="message info">
            默认账号：admin / admin123<br>
            首次登录后请修改默认密码并设置双重验证
        </div>
        
        <form id="loginForm" onsubmit="event.preventDefault(); handleLogin()">
            <div class="form-group">
                <label for="username">用户名</label>
                <input type="text" id="username" placeholder="输入用户名" required>
            </div>
            
            <div class="form-group">
                <label for="password">密码</label>
                <input type="password" id="password" placeholder="输入密码" required>
            </div>
            
            <button type="submit" class="btn">登录</button>
        </form>
        
        <div id="mfaSection" style="display: none; margin-top: 30px;">
            <div class="message warning">
                需要双重验证，请输入验证码或备份码
            </div>
            
            <div class="form-group">
                <label for="mfaCode">验证码</label>
                <input type="text" id="mfaCode" placeholder="6位数字验证码">
            </div>
            
            <div style="text-align: center; margin: 15px 0; color: #666;">
                或
            </div>
            
            <div class="form-group">
                <label for="backupCode">备份码</label>
                <input type="text" id="backupCode" placeholder="8位字母数字备份码">
            </div>
            
            <button onclick="verifyMFA()" class="btn">验证</button>
        </div>
        
        <div class="back-link">
            <a href="/">← 返回首页</a>
        </div>
    </div>
    
    <script>
        let authToken = '';
        
        async function handleLogin() {
            const username = document.getElementById('username').value;
            const password = document.getElementById('password').value;
            
            try {
                const response = await fetch('/api/admin/login', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify({ username, password })
                });
                
                const data = await response.json();
                
                if (data.success) {
                    if (data.requires_mfa) {
                        authToken = data.token;
                        document.getElementById('mfaSection').style.display = 'block';
                        document.getElementById('loginForm').style.display = 'none';
                        showMessage('需要完成双重验证', 'warning');
                    } else {
                        authToken = data.token;
                        showMessage('登录成功！跳转中...', 'success');
                        setTimeout(() => {
                            // 在实际应用中，这里会跳转到管理仪表盘
                            showAdminDashboard();
                        }, 1500);
                    }
                } else {
                    showMessage(data.error || '登录失败', 'warning');
                }
            } catch (error) {
                showMessage('网络错误，请稍后重试', 'warning');
            }
        }
        
        async function verifyMFA() {
            const mfaCode = document.getElementById('mfaCode').value;
            const backupCode = document.getElementById('backupCode').value;
            
            if (!mfaCode && !backupCode) {
                showMessage('请输入验证码或备份码', 'warning');
                return;
            }
            
            try {
                const response = await fetch('/api/admin/mfa/verify', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'Authorization': 'Bearer ' + authToken
                    },
                    body: JSON.stringify({
                        code: mfaCode || backupCode,
                        use_backup_code: !!backupCode
                    })
                });
                
                const data = await response.json();
                
                if (data.success) {
                    authToken = data.token;
                    showMessage('验证成功！跳转中...', 'success');
                    setTimeout(() => {
                        showAdminDashboard();
                    }, 1500);
                } else {
                    showMessage(data.error || '验证失败', 'warning');
                }
            } catch (error) {
                showMessage('网络错误，请稍后重试', 'warning');
            }
        }
        
        function showAdminDashboard() {
            document.body.innerHTML = \`
            <div style="max-width: 800px; margin: 0 auto; padding: 40px 20px;">
                <div style="text-align: center; margin-bottom: 40px;">
                    <h1>👨‍💼 ProxyPilot 管理面板</h1>
                    <p>登录成功！管理功能已就绪。</p>
                </div>
                
                <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(250px, 1fr)); gap: 20px; margin-bottom: 40px;">
                    <div style="background: white; padding: 25px; border-radius: 15px; box-shadow: 0 5px 15px rgba(0,0,0,0.1);">
                        <h3>📊 配置管理</h3>
                        <p>查看、创建、编辑代理配置</p>
                        <button onclick="alert('配置管理功能')" style="margin-top: 15px; padding: 10px 20px; background: #4CAF50; color: white; border: none; border-radius: 8px; cursor: pointer;">进入</button>
                    </div>
                    
                    <div style="background: white; padding: 25px; border-radius: 15px; box-shadow: 0 5px 15px rgba(0,0,0,0.1);">
                        <h3>🔗 订阅管理</h3>
                        <p>管理订阅链接和访问统计</p>
                        <button onclick="alert('订阅管理功能')" style="margin-top: 15px; padding: 10px 20px; background: #2196F3; color: white; border: none; border-radius: 8px; cursor: pointer;">进入</button>
                    </div>
                    
                    <div style="background: white; padding: 25px; border-radius: 15px; box-shadow: 0 5px 15px rgba(0,0,0,0.1);">
                        <h3>🌐 IP 管理</h3>
                        <p>查看和更新 Cloudflare IP</p>
                        <button onclick="alert('IP管理功能')" style="margin-top: 15px; padding: 10px 20px; background: #FF9800; color: white; border: none; border-radius: 8px; cursor: pointer;">进入</button>
                    </div>
                    
                    <div style="background: white; padding: 25px; border-radius: 15px; box-shadow: 0 5px 15px rgba(0,0,0,0.1);">
                        <h3>⚙️ 系统设置</h3>
                        <p>系统配置和用户管理</p>
                        <button onclick="alert('系统设置功能')" style="margin-top: 15px; padding: 10px 20px; background: #9C27B0; color: white; border: none; border-radius: 8px; cursor: pointer;">进入</button>
                    </div>
                </div>
                
                <div style="text-align: center;">
                    <a href="/" style="color: #666; text-decoration: none;">← 返回首页</a>
                </div>
            </div>
            \`;
        }
        
        function showMessage(text, type) {
            const messageDiv = document.createElement('div');
            messageDiv.className = 'message ' + type;
            messageDiv.textContent = text;
            messageDiv.style.marginTop = '20px';
            
            const existingMessage = document.querySelector('.message');
            if (existingMessage) {
                existingMessage.remove();
            }
            
            document.querySelector('.login-container').appendChild(messageDiv);
            
            setTimeout(() => {
                if (messageDiv.parentNode) {
                    messageDiv.remove();
                }
            }, 5000);
        }
    </script>
</body>
</html>
  `);
});

// 健康检查页面
router.get('/health', (req, res) => {
  res.json({
    status: 'ok',
    timestamp: new Date().toISOString(),
    service: 'ProxyPilot Node.js',
    version: '1.0.0',
    endpoints: [
      '/api/configs',
      '/api/subscription',
      '/api/admin',
      '/api/templates',
      '/api/ip-management'
    ]
  });
});

// API文档页面
router.get('/api-docs', (req, res) => {
  res.send(`
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ProxyPilot - API 文档</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            max-width: 1000px;
            margin: 0 auto;
            padding: 20px;
            background: #f8f9fa;
            color: #333;
        }
        header {
            text-align: center;
            margin-bottom: 40px;
        }
        h1 {
            color: #2c3e50;
            font-size: 2.5em;
        }
        .api-section {
            background: white;
            border-radius: 10px;
            padding: 25px;
            margin-bottom: 30px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
        }
        .endpoint {
            background: #f8f9fa;
            border-left: 4px solid #4CAF50;
            padding: 15px;
            margin: 15px 0;
            border-radius: 4px;
        }
        .method {
            display: inline-block;
            padding: 5px 10px;
            border-radius: 4px;
            font-weight: bold;
            margin-right: 10px;
        }
        .get { background: #e3f2fd; color: #1976d2; }
        .post { background: #fff3e0; color: #f57c00; }
        .put { background: #f3e5f5; color: #7b1fa2; }
        .delete { background: #ffebee; color: #d32f2f; }
        .path {
            font-family: monospace;
            font-size: 1.1em;
        }
        .description {
            color: #666;
            margin: 10px 0;
        }
        pre {
            background: #2c3e50;
            color: #ecf0f1;
            padding: 15px;
            border-radius: 6px;
            overflow-x: auto;
            margin: 15px 0;
        }
        code {
            font-family: 'Courier New', monospace;
        }
        .back-link {
            text-align: center;
            margin-top: 40px;
        }
        .back-link a {
            color: #4CAF50;
            text-decoration: none;
            font-weight: bold;
        }
    </style>
</head>
<body>
    <header>
        <h1>📚 ProxyPilot API 文档</h1>
        <p>完整 RESTful API 接口文档，用于配置管理和订阅生成</p>
    </header>
    
    <div class="api-section">
        <h2>🔧 配置管理 API</h2>
        
        <div class="endpoint">
            <div class="method post">POST</div>
            <span class="path">/api/configs/:uuid</span>
            <div class="description">创建新配置</div>
            <pre><code>请求示例：
{
    "config": "vmess://eyJ2IjoiMiIsInBzIjoiVk1lc3MgTm9kZSIsImFkZCI6IjEwNC4xNi4xMzIuMjI5IiwicG9ydCI6IjQ0MyIsImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiYWlkIjoiMCIsIm5ldCI6InRjcCIsInR5cGUiOiJub25lIn0="
}</code></pre>
        </div>
        
        <div class="endpoint">
            <div class="method get">GET</div>
            <span class="path">/api/configs/:uuid</span>
            <div class="description">获取配置列表（支持分页）</div>
        </div>
        
        <div class="endpoint">
            <div class="method get">GET</div>
            <span class="path">/api/configs/:uuid/stats</span>
            <div class="description">获取 UUID 统计信息</div>
        </div>
    </div>
    
    <div class="api-section">
        <h2>🔗 订阅 API</h2>
        
        <div class="endpoint">
            <div class="method get">GET</div>
            <span class="path">/api/subscription/:uuid/clash</span>
            <div class="description">获取 Clash 订阅配置（YAML格式）</div>
        </div>
        
        <div class="endpoint">
            <div class="method get">GET</div>
            <span class="path">/api/subscription/:uuid/raw</span>
            <div class="description">获取原始订阅（Base64编码）</div>
        </div>
        
        <div class="endpoint">
            <div class="method get">GET</div>
            <span class="path">/api/subscription/batch/:uuids/:format</span>
            <div class="description">批量获取订阅配置</div>
        </div>
    </div>
    
    <div class="api-section">
        <h2>👨‍💼 管理 API</h2>
        
        <div class="endpoint">
            <div class="method post">POST</div>
            <span class="path">/api/admin/login</span>
            <div class="description">管理员登录</div>
        </div>
        
        <div class="endpoint">
            <div class="method get">GET</div>
            <span class="path">/api/admin/stats</span>
            <div class="description">获取系统统计信息</div>
        </div>
    </div>
    
    <div class="api-section">
        <h2>🌐 IP 管理 API</h2>
        
        <div class="endpoint">
            <div class="method get">GET</div>
            <span class="path">/api/ip-management/ips</span>
            <div class="description">获取 IP 列表</div>
        </div>
        
        <div class="endpoint">
            <div class="method post">POST</div>
            <span class="path">/api/ip-management/update</span>
            <div class="description">手动触发 IP 更新</div>
        </div>
    </div>
    
    <div class="back-link">
        <a href="/">← 返回首页</a>
    </div>
</body>
</html>
  `);
});

// 404页面
router.use((req, res) => {
  res.status(404).send(`
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>404 - 页面未找到</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            background: linear-gradient(135deg, #f5f7fa 0%, #c3cfe2 100%);
            margin: 0;
        }
        .error-container {
            text-align: center;
            padding: 40px;
            background: white;
            border-radius: 20px;
            box-shadow: 0 10px 30px rgba(0, 0, 0, 0.1);
            max-width: 500px;
        }
        h1 {
            font-size: 6em;
            color: #f44336;
            margin: 0;
            line-height: 1;
        }
        h2 {
            color: #333;
            margin: 20px 0 10px;
        }
        p {
            color: #666;
            margin-bottom: 30px;
        }
        .button {
            display: inline-block;
            background: linear-gradient(135deg, #4CAF50, #45a049);
            color: white;
            padding: 12px 24px;
            border-radius: 50px;
            text-decoration: none;
            font-weight: bold;
            transition: transform 0.3s;
        }
        .button:hover {
            transform: translateY(-2px);
        }
    </style>
</head>
<body>
    <div class="error-container">
        <h1>404</h1>
        <h2>页面未找到</h2>
        <p>抱歉，您访问的页面不存在或已被移动。</p>
        <a href="/" class="button">返回首页</a>
    </div>
</body>
</html>
  `);
});

module.exports = router;