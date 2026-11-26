# =====================================================================
#  publish-agent-dist.ps1
#
#  把 dist-release 中的最新 Agent 二进制 + manifest + 安装脚本同步到
#  R2 存储桶的 agent/ 目录，避免新机器安装到旧版本（例如 1.2）。
#
#  用法：
#    $env:PP_ADMIN_TOKEN = '<后台管理员 JWT>'
#    ./scripts/publish-agent-dist.ps1 -PanelUrl https://proxypilot.yangzifun.org
#
#  可选：
#    -Version 1.4.2        指定 Agent 版本（默认从 agent/internal/agent/agent.go 读取）
#    -SkipInstallScript    不上传 install.sh
#    -DryRun               只打印将上传的内容
#
#  依赖：curl.exe（Windows 10/11 自带）。
# =====================================================================
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string] $PanelUrl,
    [string] $Token = $env:PP_ADMIN_TOKEN,
    [string] $Version,
    [switch] $SkipInstallScript,
    [switch] $DryRun
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$releaseDir = Join-Path $root 'dist-release'
$agentGo = Join-Path $root 'agent/internal/agent/agent.go'

if (-not $Token) {
    throw '缺少管理员 Token：请设置 $env:PP_ADMIN_TOKEN，或传 -Token'
}

if (-not $Version) {
    if (-not (Test-Path $agentGo)) { throw "找不到 $agentGo" }
    $m = Select-String -Path $agentGo -Pattern 'const Version = "([^"]+)"'
    if (-not $m) { throw '无法从 agent.go 读取 Version' }
    $Version = $m.Matches[0].Groups[1].Value
}
$Version = $Version.TrimStart('v')

$inputs = @{
    'linux-amd64' = Join-Path $releaseDir "proxypilot-agent-$Version-linux-amd64"
    'linux-arm64' = Join-Path $releaseDir "proxypilot-agent-$Version-linux-arm64"
}

$files = @{}
foreach ($arch in $inputs.Keys) {
    $p = $inputs[$arch]
    if (-not (Test-Path $p)) {
        throw "缺少 Agent 产物：$p（请先执行 scripts/build-release.ps1）"
    }
    $hash = (Get-FileHash $p -Algorithm SHA256).Hash.ToLower()
    $size = (Get-Item $p).Length
    $files[$arch] = @{
        name   = 'proxypilot-agent-linux-' + $arch.Replace('linux-', '')
        file   = 'proxypilot-agent-linux-' + $arch.Replace('linux-', '')
        size   = $size
        sha256 = $hash
    }
    Write-Host ("[{0}] {1} bytes sha256={2}" -f $arch, $size, $hash)
}

$manifest = [ordered]@{
    version     = "v$Version"
    released_at = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    files       = $files
}
$manifestPath = Join-Path ([System.IO.Path]::GetTempPath()) "pp-agent-manifest-$Version.json"
# 安装脚本用 grep/sed 读取 JSON；不能写 BOM，否则首行可能无法匹配。
$manifestJson = $manifest | ConvertTo-Json -Depth 8
[System.IO.File]::WriteAllText($manifestPath, $manifestJson, (New-Object System.Text.UTF8Encoding($false)))

function Invoke-Upload {
    param([string] $File, [string] $Key)
    Write-Host "上传 $Key ($((Get-Item $File).Length) bytes)"
    if ($DryRun) { return }
    $resp = & curl.exe -sS --fail-with-body -X POST `
        -H "Authorization: Bearer $Token" `
        -F "file=@$File" `
        -F "key=$Key" `
        "$($PanelUrl.TrimEnd('/'))/api/attachments/upload"
    if ($LASTEXITCODE -ne 0) { throw "上传失败：$Key`n$resp" }
    $obj = $resp | ConvertFrom-Json
    if (-not $obj.success) { throw "上传返回失败：$Key`n$resp" }
    Write-Host "  OK -> $($obj.key) ($($obj.size) bytes)"
}

try {
    foreach ($arch in $inputs.Keys) {
        $file = $inputs[$arch]
        # 桶内固定使用不带版本号的文件名，manifest.json 的 file 字段与之一致；
        # 安装脚本只按 manifest 下载，不能上传成带 1.4.2 的版本化文件名。
        $key = 'agent/' + $files[$arch].file
        Invoke-Upload -File $file -Key $key
    }
    Invoke-Upload -File $manifestPath -Key 'agent/manifest.json'

    if (-not $SkipInstallScript) {
        $installTmp = Join-Path ([System.IO.Path]::GetTempPath()) "pp-agent-install-$Version.sh"
        & curl.exe -fsSL "$($PanelUrl.TrimEnd('/'))/api/agent/install.sh" -o $installTmp
        if ($LASTEXITCODE -ne 0) { throw '下载 install.sh 失败' }
        Invoke-Upload -File $installTmp -Key 'agent/install.sh'
        Remove-Item $installTmp -Force -ErrorAction SilentlyContinue
    }
} finally {
    Remove-Item $manifestPath -Force -ErrorAction SilentlyContinue
}

# 刷新面板的 manifest 缓存
if (-not $DryRun) {
    try {
        & curl.exe -sS -H "Authorization: Bearer $Token" "$($PanelUrl.TrimEnd('/'))/api/agent/latest?refresh=1" | Out-Null
    } catch { }
}

Write-Host ""
Write-Host "完成：Agent v$Version 已发布到 $PanelUrl/agent"
Write-Host "请用新机器执行一次安装命令，确认版本为 v$Version。"