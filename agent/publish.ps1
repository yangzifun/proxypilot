# =================================================================
#  发布 ProxyPilot Agent 到 S3 兼容对象存储（Windows / PowerShell）
#
#  用法：
#    $env:AWS_ACCESS_KEY_ID = "你的 Key"
#    $env:AWS_SECRET_ACCESS_KEY = "你的 Secret"
#    ./publish.ps1 -Endpoint https://s3.proxypilot.yangzifun.org -Bucket agent
#    ./publish.ps1 -Endpoint https://s3.proxypilot.yangzifun.org -Bucket agent -DryRun   # 只打印命令
#
#  Cloudflare R2（推荐）：
#    $env:AWS_ACCESS_KEY_ID = "R2 Access Key"
#    $env:AWS_SECRET_ACCESS_KEY = "R2 Secret Key"
#    ./publish.ps1 -R2AccountId <account_id> -Bucket proxypilot-agent
#
#  说明：先执行 ./build.sh（Git Bash / WSL / MSYS 均可）生成 dist/；
#        本脚本按「扁平布局」上传：<base>/proxypilot-agent-linux-<arch> 与 <base>/manifest.json。
#        也可不用脚本：用 mc / aws-cli / 控制台手工上传 dist/ 下的文件（见 build.sh 末尾提示）。
# =================================================================
param(
    [string]$Endpoint = "https://s3.proxypilot.yangzifun.org",
    [string]$Bucket = "agent",
    [string]$Dist = "dist",
    [string]$Region = "us-east-1",
    [string]$R2AccountId = "",
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
if ($R2AccountId) {
    # R2 的 S3 兼容端点；自定义域只影响读取（Worker 的 AGENT_BINARY_BASE_URL）
    $Endpoint = "https://$R2AccountId.r2.cloudflarestorage.com"
}
$Endpoint = $Endpoint.TrimEnd('/')
if (-not [IO.Path]::IsPathRooted($Dist)) { $Dist = Join-Path $PSScriptRoot $Dist }   # 允许从任意目录调用

if (-not (Test-Path (Join-Path $Dist "manifest.json"))) {
    Write-Error "找不到 $Dist/manifest.json：请先执行 ./build.sh"
}
if (-not $DryRun -and (-not $env:AWS_ACCESS_KEY_ID -or -not $env:AWS_SECRET_ACCESS_KEY)) {
    Write-Error "缺少 AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY（S3 兼容存储的访问凭据）"
}
$aws = Get-Command aws -ErrorAction SilentlyContinue
if (-not $DryRun -and -not $aws) {
    Write-Error "未找到 aws-cli：请安装 AWS CLI（或改用 mc / 控制台上传，见 build.sh 末尾提示）"
}

$manifest = Get-Content (Join-Path $Dist "manifest.json") -Raw -Encoding UTF8 | ConvertFrom-Json

Write-Host "==> 版本 $($manifest.version)（$($manifest.released_at)）→ s3://$Bucket/（$Endpoint）"

$files = @()
foreach ($prop in $manifest.files.PSObject.Properties) {
    $f = $prop.Value.file
    if (-not $f) { Write-Error "manifest 缺少 file 字段（请用最新 build.sh 重新生成）" }
    $files += $f
}
$files += "manifest.json"
# 对象存储版安装脚本（如已生成）
if (Test-Path (Join-Path $Dist "install.sh")) { $files += "install.sh" }

$contentTypes = @{ "manifest.json" = "application/json"; "install.sh" = "text/x-shellscript; charset=utf-8"; "default" = "application/octet-stream" }
foreach ($name in $files) {
    $path = Join-Path $Dist $name
    if (-not (Test-Path $path)) { Write-Error "缺少产物 $path" }
    $ct = if ($contentTypes.ContainsKey($name)) { $contentTypes[$name] } else { $contentTypes["default"] }
    $awsArgs = @("--endpoint-url", $Endpoint, "s3", "cp", $path, "s3://$Bucket/$name",
        "--content-type", $ct, "--acl", "public-read", "--region", $Region)
    if ($DryRun) {
        Write-Host ("[dry-run] aws " + ($awsArgs -join " "))
    } else {
        Write-Host "==> 上传 $name"
        & aws @awsArgs
        if ($LASTEXITCODE -ne 0) { Write-Error "上传失败：$name" }
    }
}

Write-Host "==> 完成。验证："
foreach ($name in $files) { Write-Host "    curl -I $Endpoint/$Bucket/$name" }
Write-Host "    Worker 侧无需绑定；若基址不同，配置环境变量 AGENT_BINARY_BASE_URL=$Endpoint/$Bucket"