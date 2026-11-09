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

Step 'Windows 客户端安装/卸载脚本'
$winDir = Join-Path $root 'deploy/windows'
foreach ($f in @('install.cmd', 'install.ps1', 'uninstall.cmd', 'uninstall.ps1')) {
    $dst = Join-Path $out $f
    $txt = [System.IO.File]::ReadAllText((Join-Path $winDir $f), [System.Text.Encoding]::UTF8)
    $txt = $txt.Replace('__VER__', $panelVer)
    $enc = if ($f -like '*.ps1') { New-Object System.Text.UTF8Encoding($true) } else { [System.Text.Encoding]::ASCII }
    [System.IO.File]::WriteAllText($dst, $txt, $enc)
}
Ok 'install.cmd / install.ps1 / uninstall.cmd / uninstall.ps1 已生成'

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
$manifest += "  Windows   双击 install.cmd（自动提权，安装时注册虚拟网卡）"
$manifest += "            也可以直接运行 ProxyPilot-$panelVer-windows-amd64.exe，会自动提示先安装"
$manifest += "  卸载      double-click uninstall.cmd 或 设置 → 应用 → ProxyPilot VPN"
$manifest += "  客户端自检 ProxyPilot-$panelVer-windows-amd64-cli.exe -tun-selftest"
[System.IO.File]::WriteAllText((Join-Path $out 'MANIFEST.txt'), ($manifest -join "`n") + "`n", (New-Object System.Text.UTF8Encoding($false)))

Step '完成'
$all = Get-ChildItem $out -File
$sum = ($all | Measure-Object -Property Length -Sum).Sum
Ok ("dist-release：{0} 个文件，共 {1:N1} MB" -f $all.Count, ($sum / 1MB))
$all | Sort-Object Name | ForEach-Object {
    Write-Host ("   {0,-50} {1,7:N1} MB" -f $_.Name, ($_.Length / 1MB))
}