# Agent 分发版本修复（存储桶与新装机器 1.2）

日期：2026-11-26
面板版本：V1.6.25（本次不升级面板）

## 问题

新安装机器执行安装命令后拿到 Agent **1.2**，而面板与本地分发目录已经是 **1.4.2**。

## 根因

1. `agent_binary_base_url` 当前指向面板 `/agent-dist`；旧安装命令/脚本仍会从对象存储桶读取 `agent/manifest.json`。
2. R2 存储桶中的 `agent/manifest.json` 仍为：
   - `version: 1.2`
   - `released_at: 2026-11-12`
3. 本地 `/opt/proxypilot/agent-dist/install.sh` 是旧版安装器；安装命令会优先拿这个脚本，脚本再按旧桶 manifest 下载 1.2。
4. 发布流程没有把新的 Agent 二进制 / manifest / install.sh 上传到桶，导致桶长期停留在旧版本。

## 修复

### 1. 更新存储桶

已上传到 `agent/`：

- `proxypilot-agent-linux-amd64`
- `proxypilot-agent-linux-arm64`
- `manifest.json`
- `install.sh`

当前桶状态：

```json
{
  "version": "v1.4.2",
  "files": {
    "linux-amd64": "sha256 31286cdb...a79a2a9",
    "linux-arm64": "sha256 4e8ebc89...368d59"
  }
}
```

桶内二进制实测：

```text
proxypilot-agent 1.4.2（linux/amd64，go go1.27.3）
```

### 2. 更新本机安装脚本

`/opt/proxypilot/agent-dist/install.sh` 已替换为面板当前渲染版本：

- 备份旧文件：`install.sh.bak.agent-latest-*`
- 新版 `BINARY_BASE` 指向面板 `/agent-dist`
- 本地 manifest 已是 `v1.4.2`

### 3. 新增发布脚本

新增：

```text
scripts/publish-agent-dist.ps1
```

用途：

- 从 `dist-release` 读取最新 Agent 二进制；
- 计算 SHA-256 / size；
- 生成不带 BOM 的 `manifest.json`；
- 上传二进制、manifest、install.sh 到 R2；
- 自动刷新面板 manifest 缓存。

用法：

```powershell
$env:PP_ADMIN_TOKEN = '<后台管理员 JWT>'
./scripts/publish-agent-dist.ps1 -PanelUrl https://proxypilot.yangzifun.org
```

支持：

- `-Version 1.4.2`
- `-SkipInstallScript`
- `-DryRun`

## 验证

- 桶 manifest：`v1.4.2`；
- 桶 amd64 二进制 SHA 与 manifest 一致；
- 桶二进制运行 `version` 输出 `1.4.2`；
- 本地面板 `/agent-dist/manifest.json`：`v1.4.2`；
- 本地面板 `/api/agent/install.sh` 与桶 `install.sh` 一致；
- 新机器安装命令现在会从 `/agent-dist` 获取 1.4.2，桶回退也是 1.4.2。

## 后续发布要求

每次发布新 Agent 后必须执行一次：

```text
scripts/publish-agent-dist.ps1
```

否则新机器仍可能拿到桶里的旧版本。

建议把该步骤加入发布清单：

1. `build-release.ps1`
2. 部署面板
3. `publish-agent-dist.ps1`
4. 用一台新机器执行安装命令，确认 `proxypilot-agent version` 为新版本。