# ProxyPilot VPN 客户端卸载（Windows）
#
# 删除：开机自启项、虚拟网卡、程序目录、快捷方式、卸载登记项。

$ErrorActionPreference = 'Continue'

$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($id)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "需要管理员权限移除虚拟网卡，正在申请提权…" -ForegroundColor Yellow
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = 'powershell.exe'
    $psi.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
    $psi.Verb = 'runas'
    try { [System.Diagnostics.Process]::Start($psi) | Out-Null } catch { }
    exit 0
}

$installDir = Join-Path $env:LOCALAPPDATA 'Programs\ProxyPilot'
$exePath    = Join-Path $installDir 'ProxyPilot.exe'
$adapter    = 'ProxyPilot VPN'
$runKey     = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'

# 1) 先撤掉开机自启，避免卸载后又被拉起来
Remove-ItemProperty -Path $runKey -Name 'ProxyPilot' -ErrorAction SilentlyContinue
Write-Host "已移除开机自启项"

# 2) 结束进程
Get-Process ProxyPilot -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600

# 3) 让程序自己清理注册表与网卡
if (Test-Path $exePath) {
    Start-Process -FilePath $exePath -ArgumentList @('-uninstall') -Wait -PassThru -WindowStyle Hidden | Out-Null
}

# 4) 移除网卡设备
$nic = Get-NetAdapter -Name $adapter -ErrorAction SilentlyContinue
if ($nic) {
    Write-Host "移除虚拟网卡：$adapter"
    Disable-NetAdapter -Name $adapter -Confirm:$false -ErrorAction SilentlyContinue
    try {
        Get-PnpDevice -Class Net -ErrorAction SilentlyContinue |
            Where-Object { $_.FriendlyName -like "*$adapter*" } |
            ForEach-Object { pnputil /remove-device $_.InstanceId 2>$null | Out-Null }
    } catch { }
}

# 5) 注册表卸载项
$key = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ProxyPilot'
if (Test-Path $key) { Remove-Item $key -Recurse -Force -ErrorAction SilentlyContinue }

# 6) 目录与快捷方式
if (Test-Path $installDir) { Remove-Item $installDir -Recurse -Force -ErrorAction SilentlyContinue }
Remove-Item (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\ProxyPilot.lnk') -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path ([Environment]::GetFolderPath('Desktop')) 'ProxyPilot.lnk') -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $env:TEMP 'proxypilot-install.json') -Force -ErrorAction SilentlyContinue

Write-Host "卸载完成 ✅" -ForegroundColor Green