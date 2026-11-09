# ProxyPilot 客户端安装（按用户安装，无需管理员）
$ErrorActionPreference = 'Stop'
$exeName = 'ProxyPilot-V35.2-windows-amd64.exe'
$source  = Join-Path $PSScriptRoot $exeName
if (-not (Test-Path $source)) { Write-Host "找不到 $exeName（请与本脚本同目录）" -ForegroundColor Red; exit 1 }

$installDir = Join-Path $env:LOCALAPPDATA 'Programs\ProxyPilot'
$exePath    = Join-Path $installDir 'ProxyPilot.exe'
Write-Host "安装到：$installDir"
New-Item -ItemType Directory -Force -Path $installDir | Out-Null

Get-Process ProxyPilot -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500
Copy-Item $source $exePath -Force

$shell = New-Object -ComObject WScript.Shell
function New-Shortcut($path) {
    $lnk = $shell.CreateShortcut($path)
    $lnk.TargetPath = $exePath
    $lnk.WorkingDirectory = $installDir
    $lnk.IconLocation = "$exePath,0"
    $lnk.Description = 'ProxyPilot VPN 客户端'
    $lnk.Save()
}
$startMenu = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
New-Shortcut (Join-Path $startMenu 'ProxyPilot.lnk') | Out-Null
New-Shortcut (Join-Path ([Environment]::GetFolderPath('Desktop')) 'ProxyPilot.lnk') | Out-Null

Write-Host '安装完成' -ForegroundColor Green
Write-Host '提示：启动时会请求管理员权限（创建虚拟网卡、修改路由所必需）'