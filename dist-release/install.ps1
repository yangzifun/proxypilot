# ProxyPilot 客户端安装脚本（按用户安装，不需要管理员）
# 用法：右键 install.cmd → 以管理员身份运行？不需要，双击即可。

$ErrorActionPreference = 'Stop'

$source = Join-Path $PSScriptRoot 'ProxyPilot.exe'
if (-not (Test-Path $source)) {
    Write-Host "找不到 ProxyPilot.exe（请把本脚本和 exe 放在同一目录）" -ForegroundColor Red
    exit 1
}

$installDir = Join-Path $env:LOCALAPPDATA 'Programs\ProxyPilot'
$exePath    = Join-Path $installDir 'ProxyPilot.exe'

Write-Host "安装到：$installDir"
New-Item -ItemType Directory -Force -Path $installDir | Out-Null

# 关掉正在运行的旧版本，避免文件占用
Get-Process ProxyPilot -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

Copy-Item $source $exePath -Force
Write-Host "已复制可执行文件"

# 快捷方式（开始菜单 + 桌面）
$shell = New-Object -ComObject WScript.Shell
function New-Shortcut($path) {
    $lnk = $shell.CreateShortcut($path)
    $lnk.TargetPath       = $exePath
    $lnk.WorkingDirectory = $installDir
    $lnk.IconLocation     = "$exePath,0"
    $lnk.Description      = 'ProxyPilot MASQUE 客户端'
    $lnk.Save()
}

$startMenu = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
New-Shortcut (Join-Path $startMenu 'ProxyPilot.lnk') | Out-Null
$desktop = [Environment]::GetFolderPath('Desktop')
New-Shortcut (Join-Path $desktop 'ProxyPilot.lnk') | Out-Null
Write-Host "已创建开始菜单和桌面快捷方式"

# 卸载信息（控制面板 → 程序和功能）
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ProxyPilot'
New-Item -Path $uninstallKey -Force | Out-Null
Set-ItemProperty -Path $uninstallKey -Name DisplayName     -Value 'ProxyPilot 客户端'
Set-ItemProperty -Path $uninstallKey -Name DisplayVersion  -Value '1.0.0'
Set-ItemProperty -Path $uninstallKey -Name Publisher       -Value 'ProxyPilot'
Set-ItemProperty -Path $uninstallKey -Name DisplayIcon     -Value $exePath
Set-ItemProperty -Path $uninstallKey -Name InstallLocation -Value $installDir
Set-ItemProperty -Path $uninstallKey -Name UninstallString -Value ("powershell -NoProfile -ExecutionPolicy Bypass -File `"" + (Join-Path $installDir 'uninstall.ps1') + "`"")
Set-ItemProperty -Path $uninstallKey -Name NoModify        -Value 1 -Type DWord
Set-ItemProperty -Path $uninstallKey -Name NoRepair        -Value 1 -Type DWord

# 卸载脚本
@'
$ErrorActionPreference = 'SilentlyContinue'
$dir = Join-Path $env:LOCALAPPDATA 'Programs\ProxyPilot'
Get-Process ProxyPilot | Stop-Process -Force
Remove-Item (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\ProxyPilot.lnk') -Force
Remove-Item (Join-Path ([Environment]::GetFolderPath('Desktop')) 'ProxyPilot.lnk') -Force
Remove-Item 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ProxyPilot' -Recurse -Force
Remove-Item $dir -Recurse -Force
Write-Host 'ProxyPilot 已卸载'
'@ | Set-Content (Join-Path $installDir 'uninstall.ps1') -Encoding UTF8

Write-Host ""
Write-Host "安装完成，正在启动…" -ForegroundColor Green
Start-Process -FilePath $exePath
