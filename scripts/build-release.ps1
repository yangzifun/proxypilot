# =====================================================================
#  构建 ProxyPilot 全部发布产物到 dist-release/
#
#  用法（仓库根目录）：
#    powershell -ExecutionPolicy Bypass -File scripts\build-release.ps1
#    powershell -ExecutionPolicy Bypass -File scripts\build-release.ps1 -SkipTests
#
#  产物：dist-release/ 下的服务端、Agent、VPN 客户端（Windows/Linux/macOS）、
#        安装脚本、SHA256SUMS.txt 与 MANIFEST.txt
# =====================================================================
[CmdletBinding()]
param([switch]$SkipTests)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$out  = Join-Path $root 'dist-release'
$env:GOTOOLCHAIN = 'go1.27.3'
$env:CGO_ENABLED = '0'

function Step($msg) { Write-Host ""; Write-Host "== $msg" -ForegroundColor Cyan }
function Ok($msg)   { Write-Host "   $msg" -ForegroundColor Green }

$mainGo  = Join-Path $root 'server/cmd/proxypilot/main.go'
$panelVer = (Select-String -Path $mainGo -Pattern 'var version = "([^"]+)"').Matches[0].Groups[1].Value
$agentGo  = Join-Path $root 'agent/internal/agent/agent.go'
$agentVer = (Select-String -Path $agentGo -Pattern 'const Version = "([^"]+)"').Matches[0].Groups[1].Value
Step "版本：面板 $panelVer / Agent $agentVer"

if (-not (Test-Path $out)) { New-Item -ItemType Directory -Path $out | Out-Null }

function Build-Go {
    param(
        [string]$Dir,
        [string]$Goos,
        [string]$Goarch,
        [string]$Out,
        [string[]]$Tags = @(),
        [string]$LdFlags = '-s -w',
        [string]$Pkg = '.'
    )
    $env:GOOS = $Goos
    $env:GOARCH = $Goarch
    $goArgs = @('build', '-trimpath', "-ldflags=$LdFlags", '-o', $Out)
    if ($Tags.Count -gt 0) { $goArgs += "-tags=$($Tags -join ',')" }
    $goArgs += $Pkg
    Push-Location $Dir
    try {
        $buildOut = & go @goArgs 2>&1
        if ($LASTEXITCODE -ne 0) {
            $buildOut | Select-Object -Last 20 | Write-Host
            throw "构建失败：$Out"
        }
    } finally {
        Pop-Location
    }
    $mb = [math]::Round((Get-Item $Out).Length / 1MB, 2)
    Ok "$([IO.Path]::GetFileName($Out))  ($mb MB)"
}

if (-not $SkipTests) {
    Step '单元测试（server / agent）'
    Push-Location (Join-Path $root 'server'); go test ./... 2>&1 | Select-Object -Last 3; Pop-Location
    Push-Location (Join-Path $root 'agent');  go test ./internal/... 2>&1 | Select-Object -Last 3; Pop-Location
}

Step '服务端'
$srvDir = Join-Path $root 'server'
Build-Go -Dir $srvDir -Pkg './cmd/proxypilot' -Goos linux -Goarch amd64 -Out (Join-Path $out "proxypilot-$panelVer-linux-amd64")
Build-Go -Dir $srvDir -Pkg './cmd/proxypilot' -Goos linux -Goarch arm64 -Out (Join-Path $out "proxypilot-$panelVer-linux-arm64")

Step 'Agent'
$agDir = Join-Path $root 'agent'
$env:GOFLAGS = '-mod=mod'
Build-Go -Dir $agDir -Pkg './cmd/proxypilot-agent' -Goos linux -Goarch amd64 -Out (Join-Path $out "proxypilot-agent-$agentVer-linux-amd64")
Build-Go -Dir $agDir -Pkg './cmd/proxypilot-agent' -Goos linux -Goarch arm64 -Out (Join-Path $out "proxypilot-agent-$agentVer-linux-arm64")
$env:GOFLAGS = ''

Step 'VPN 客户端'
$vcDir = Join-Path $root 'vpnclient'
# Windows GUI：Wails v3，无控制台窗口，清单要求管理员
Build-Go -Dir $vcDir -Goos windows -Goarch amd64 -Tags @('desktop') `
    -LdFlags '-H windowsgui -s -w' -Out (Join-Path $out "ProxyPilot-$panelVer-windows-amd64.exe")
# Windows CLI：可运行 -tun-selftest 等自检
Build-Go -Dir $vcDir -Goos windows -Goarch amd64 `
    -Out (Join-Path $out "proxypilot-vpn-$panelVer-windows-amd64-cli.exe")
# Linux / macOS
Build-Go -Dir $vcDir -Goos linux  -Goarch amd64 -Out (Join-Path $out "proxypilot-vpn-$panelVer-linux-amd64")
Build-Go -Dir $vcDir -Goos linux  -Goarch arm64 -Out (Join-Path $out "proxypilot-vpn-$panelVer-linux-arm64")
Build-Go -Dir $vcDir -Goos darwin -Goarch amd64 -Out (Join-Path $out "proxypilot-vpn-$panelVer-darwin-amd64")
Build-Go -Dir $vcDir -Goos darwin -Goarch arm64 -Out (Join-Path $out "proxypilot-vpn-$panelVer-darwin-arm64")

Step '脚本与文档'
Copy-Item (Join-Path $root 'deploy/install.sh')     $out -Force
Copy-Item (Join-Path $root 'deploy/upgrade.sh')     $out -Force
Copy-Item (Join-Path $root 'deploy/uninstall.sh')   $out -Force
Copy-Item (Join-Path $root 'deploy/maintenance.sh') $out -Force
Copy-Item (Join-Path $root 'deploy/README.md') (Join-Path $out 'DEPLOY-README.md') -Force
Ok '安装 / 升级 / 卸载 / 维护脚本已复制'

Step 'Windows 客户端安装脚本'
$installPs1 = @'
# ProxyPilot 客户端安装（按用户安装，无需管理员）
$ErrorActionPreference = 'Stop'
$exeName = 'ProxyPilot-__VER__-windows-amd64.exe'
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
'@
$installPs1 = $installPs1.Replace('__VER__', $panelVer)
$installPs1Path = Join-Path $out 'install.ps1'
[System.IO.File]::WriteAllText($installPs1Path, $installPs1, (New-Object System.Text.UTF8Encoding($true)))

$cmdText = "@echo off`r`nchcp 65001 >nul`r`npowershell -NoProfile -ExecutionPolicy Bypass -File `"%~dp0install.ps1`"`r`nif errorlevel 1 pause`r`n"
[System.IO.File]::WriteAllText((Join-Path $out 'install.cmd'), $cmdText, [System.Text.Encoding]::ASCII)
Ok 'install.cmd / install.ps1 已生成'

Step '生成 SHA256SUMS.txt / MANIFEST.txt'
$files = Get-ChildItem $out -File | Where-Object { $_.Name -notin @('SHA256SUMS.txt', 'MANIFEST.txt') } | Sort-Object Name
$lines = @()
foreach ($f in $files) {
    $h = (Get-FileHash $f.FullName -Algorithm SHA256).Hash.ToLower()
    $lines += "$h  $($f.Name)"
}
[System.IO.File]::WriteAllText((Join-Path $out 'SHA256SUMS.txt'), ($lines -join "`n") + "`n", [System.Text.Encoding]::ASCII)

$manifest = @()
$manifest += "ProxyPilot 发布包"
$manifest += "面板版本 : $panelVer"
$manifest += "Agent    : $agentVer"
$manifest += "构建时间 : $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"
$manifest += ""
$manifest += "文件清单："
foreach ($f in $files) {
    $manifest += ("  {0,-50} {1,7:N1} MB" -f $f.Name, ($f.Length / 1MB))
}
$manifest += ""
$manifest += "安装："
$manifest += "  服务端    sudo ./install.sh --binary ./proxypilot-$panelVer-linux-amd64"
$manifest += "  升级      sudo ./upgrade.sh --binary ./proxypilot-$panelVer-linux-amd64"
$manifest += "  Windows   双击 install.cmd（启动时请求管理员权限）"
$manifest += "  客户端自检 ProxyPilot-$panelVer-windows-amd64-cli.exe -tun-selftest"
[System.IO.File]::WriteAllText((Join-Path $out 'MANIFEST.txt'), ($manifest -join "`n") + "`n", (New-Object System.Text.UTF8Encoding($false)))

Step '完成'
$all = Get-ChildItem $out -File
$sum = ($all | Measure-Object -Property Length -Sum).Sum
Ok ("dist-release：{0} 个文件，共 {1:N1} MB" -f $all.Count, ($sum / 1MB))
$all | Sort-Object Name | ForEach-Object {
    Write-Host ("   {0,-50} {1,7:N1} MB" -f $_.Name, ($_.Length / 1MB))
}