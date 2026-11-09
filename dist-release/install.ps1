# ProxyPilot VPN 客户端安装（Windows）
#
# 行为：
#   1) 复制程序到 %LOCALAPPDATA%\Programs\ProxyPilot
#   2) 调用 `ProxyPilot.exe -install` 释放 Wintun 驱动并**注册虚拟网卡**
#   3) 可选：开机后台运行（写入 HKCU Run，带 -background，启动即最小化到托盘）
#   4) 创建开始菜单与桌面快捷方式
#   5) 写入卸载信息（设置 → 应用 里可卸载）
#
# 用法：
#   install.cmd                    # 交互式（会询问是否开机后台运行）
#   powershell -File install.ps1 -Background     # 强制开机后台运行
#   powershell -File install.ps1 -NoBackground   # 不设置开机自启

param(
    [switch]$Background,
    [switch]$NoBackground
)

$ErrorActionPreference = 'Stop'
$exeName = 'ProxyPilot-App-1.0-windows-amd64.exe'
$source  = Join-Path $PSScriptRoot $exeName
if (-not (Test-Path $source)) {
    Write-Host "找不到 $exeName（请把本脚本和 exe 放在同一目录）" -ForegroundColor Red
    exit 1
}

# ---------- 管理员检查（注册网卡必须） ----------
$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($id)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "需要管理员权限注册虚拟网卡，正在申请提权…" -ForegroundColor Yellow
    $argLine = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
    if ($Background) { $argLine += ' -Background' }
    if ($NoBackground) { $argLine += ' -NoBackground' }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = 'powershell.exe'
    $psi.Arguments = $argLine
    $psi.Verb = 'runas'
    try { [System.Diagnostics.Process]::Start($psi) | Out-Null } catch { Write-Host "提权被取消，安装中止" -ForegroundColor Red }
    exit 0
}

$installDir = Join-Path $env:LOCALAPPDATA 'Programs\ProxyPilot'
$exePath    = Join-Path $installDir 'ProxyPilot.exe'
$runKey     = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'

Write-Host "安装目录：$installDir" -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path $installDir | Out-Null

# 关掉旧版本，避免文件占用
Get-Process ProxyPilot -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600

Write-Host "复制程序…"
Copy-Item $source $exePath -Force

# ---------- 注册驱动与虚拟网卡 ----------
Write-Host "注册 Wintun 驱动与虚拟网卡…" -ForegroundColor Cyan
$installJson = Join-Path $env:TEMP 'proxypilot-install.json'
Remove-Item $installJson -Force -ErrorAction SilentlyContinue

$proc = Start-Process -FilePath $exePath -ArgumentList @('-install', "-install-dir=$installDir") -Wait -PassThru -WindowStyle Hidden
if ($proc.ExitCode -ne 0) {
    Write-Host "注册虚拟网卡失败（退出码 $($proc.ExitCode)）" -ForegroundColor Red
    Write-Host "可手动运行：`"$exePath`" -install  查看详细错误" -ForegroundColor Yellow
    exit 1
}

$adapter = 'ProxyPilot VPN'
if (Test-Path $installJson) {
    try {
        $info = Get-Content $installJson -Raw | ConvertFrom-Json
        if ($info.adapter) { $adapter = $info.adapter }
        if ($info.driver_path) { Write-Host "驱动：$($info.driver_path)" }
    } catch { }
}

Start-Sleep -Milliseconds 800
$nic = Get-NetAdapter -Name $adapter -ErrorAction SilentlyContinue
if ($nic) {
    Write-Host "虚拟网卡已注册：$adapter（$($nic.InterfaceDescription)）" -ForegroundColor Green
} else {
    Write-Host "警告：未找到名为 $adapter 的网卡，连接时会再次尝试创建" -ForegroundColor Yellow
}

# ---------- 开机后台运行（托盘常驻） ----------
$wantBackground = $false
if ($Background) {
    $wantBackground = $true
} elseif (-not $NoBackground) {
    $answer = Read-Host "是否随 Windows 启动并在后台（托盘）运行？[Y/n]"
    $wantBackground = ($answer -eq '' -or $answer -match '^[Yy]')
}

if ($wantBackground) {
    $cmd = "`"$exePath`" -background"
    New-Item -Path $runKey -Force | Out-Null
    Set-ItemProperty -Path $runKey -Name 'ProxyPilot' -Value $cmd
    Write-Host "已设置开机后台运行（启动后仅托盘，不弹窗口）" -ForegroundColor Green
} else {
    Remove-ItemProperty -Path $runKey -Name 'ProxyPilot' -ErrorAction SilentlyContinue
    Write-Host "未设置开机自启"
}

# ---------- 快捷方式 ----------
Write-Host "创建快捷方式…"
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

Write-Host ""
Write-Host "安装完成 ✅" -ForegroundColor Green
Write-Host "  程序      : $exePath"
Write-Host "  网卡      : $adapter"
Write-Host "  后台运行  : $(if ($wantBackground) { '开机自启，托盘常驻' } else { '否' })"
Write-Host "  卸载      : 设置 → 应用 → ProxyPilot VPN，或运行 uninstall.cmd"
Write-Host ""
Write-Host "使用提示："
Write-Host "  · 关闭窗口不会退出，程序缩到右下角托盘继续运行"
Write-Host "  · 托盘图标左键单击显示/隐藏窗口，右键有 连接 / 断开 / 设置 / 退出"
Write-Host "  · 首次连接采用「仅组网网段」，需要全局接管在首选项里切换"