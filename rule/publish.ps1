# =================================================================
#  发布 rule/ 到 S3 兼容对象存储（Windows / PowerShell）
#
#  用法：
#    $env:AWS_ACCESS_KEY_ID = "你的 Key"
#    $env:AWS_SECRET_ACCESS_KEY = "你的 Secret"
#    ./publish.ps1 -DryRun        # 只打印命令
#    ./publish.ps1                # 上传
#
#  默认目标：https://s3.proxypilot.yangzifun.org 的 rule 桶
#    rule/se_asia.list  →  s3://rule/se_asia.list
#    rule/configs/*.yaml → s3://rule/configs/*.yaml
# =================================================================
param(
    [string]$Endpoint = "https://s3.proxypilot.yangzifun.org",
    [string]$Bucket = "rule",
    [string]$Source = ".",
    [string]$Region = "us-east-1",
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$Endpoint = $Endpoint.TrimEnd('/')
if (-not [IO.Path]::IsPathRooted($Source)) { $Source = Join-Path $PSScriptRoot $Source }

if (-not $DryRun -and (-not $env:AWS_ACCESS_KEY_ID -or -not $env:AWS_SECRET_ACCESS_KEY)) {
    Write-Error "缺少 AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY"
}
$aws = Get-Command aws -ErrorAction SilentlyContinue
if (-not $DryRun -and -not $aws) { Write-Error "未找到 aws-cli：请安装 AWS CLI 后重试" }

$contentTypes = @{
    ".list" = "text/plain; charset=utf-8"
    ".yaml" = "text/yaml; charset=utf-8"
    ".yml"  = "text/yaml; charset=utf-8"
    ".md"   = "text/markdown; charset=utf-8"
    ".txt"  = "text/plain; charset=utf-8"
}
$defaultType = "application/octet-stream"

$files = Get-ChildItem -Path $Source -Recurse -File | Where-Object { $_.Name -ne 'publish.ps1' }
Write-Host ("==> 待上传 {0} 个文件 → s3://{1}/（{2}）" -f $files.Count, $Bucket, $Endpoint)

foreach ($f in $files) {
    $rel = $f.FullName.Substring($Source.Length).TrimStart('\', '/').Replace('\', '/')
    $ext = [IO.Path]::GetExtension($f.Name).ToLowerInvariant()
    $ct = if ($contentTypes.ContainsKey($ext)) { $contentTypes[$ext] } else { $defaultType }
    $awsArgs = @("--endpoint-url", $Endpoint, "s3", "cp", $f.FullName, "s3://$Bucket/$rel",
        "--content-type", $ct, "--acl", "public-read", "--region", $Region)
    if ($DryRun) {
        Write-Host ("[dry-run] aws " + ($awsArgs -join " "))
    } else {
        Write-Host "==> $rel"
        & aws @awsArgs
        if ($LASTEXITCODE -ne 0) { Write-Error "上传失败：$rel" }
    }
}

Write-Host "==> 完成。验证："
foreach ($f in $files) {
    $rel = $f.FullName.Substring($Source.Length).TrimStart('\', '/').Replace('\', '/')
    Write-Host "    curl -I $Endpoint/$Bucket/$rel"
}
