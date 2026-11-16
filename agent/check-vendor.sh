#!/usr/bin/env bash
# =================================================================
#  Agent vendor 守卫（task-25）
#
#  背景：线上 Agent 曾因 agent/vendor 里的 tunnel 模块陈旧（缺少
#  「单连接双通道 / H3-Datagram / X-VPN-Lane / 已停用 DATAGRAM」修复）
#  而把 UDP（含 DNS）下行塞进客户端未协商的 QUIC DATAGRAM 通道且无回退，
#  导致用户 UDP/DNS 全部超时（详见 myworkspace/reports/vpn-dns-diagnosis.md）。
#
#  本脚本用「go mod vendor -o 复算 + diff」校验 vendor/ 与工作区模块
#  （tunnel/ 通过 replace 指向 ../tunnel）完全一致，并硬校验 tunnel 的
#  私有协议修复符号存在；不一致时退出码 1，build.sh 会据此直接中止。
#
#  用法：
#    ./check-vendor.sh        # 校验（一致 exit 0，不一致 exit 1）
#    ./check-vendor.sh -q     # 静默模式：只在失败时输出
#  修复：
#    cd agent && go mod vendor   # 同步 vendor 后重新构建（禁止 -mod=mod 绕过）
# =================================================================
set -uo pipefail
cd "$(dirname "$0")"

QUIET=0
if [ "${1:-}" = "-q" ]; then QUIET=1; fi
say() { if [ "$QUIET" = "0" ]; then echo "$@"; fi; }
fail() { echo "[check-vendor] FAIL: $*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "未找到 go 命令（构建需要 Go >= 1.21）"
[ -d vendor ] || fail "agent/vendor 不存在（修复：cd agent && go mod vendor）"
[ -f vendor/modules.txt ] || fail "agent/vendor/modules.txt 不存在（vendor 不完整）"

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t agent-vendor)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

say "[check-vendor] 复算 vendor（go mod vendor -o ...）..."
if ! go mod vendor -o "$TMP/vendor" >"$TMP/vendor.log" 2>&1; then
    sed -n '1,40p' "$TMP/vendor.log" >&2
    fail "go mod vendor 复算失败（检查模块缓存/网络；修复：cd agent && go mod vendor）"
fi

# --strip-trailing-cr：Windows 工作区（core.autocrlf）可能把 LF 检出为 CRLF，
# 这里只忽略行尾 CR，不影响文件真实内容比较。
if ! diff -r --strip-trailing-cr "$TMP/vendor" vendor >"$TMP/vendor.diff" 2>&1; then
    echo "[check-vendor] FAIL: vendor/ 与工作区模块不一致（vendor 已陈旧）：" >&2
    sed -n '1,60p' "$TMP/vendor.diff" | sed 's/^/  /' >&2
    fail "请执行：cd agent && go mod vendor（同步后重试；禁止用 -mod=mod 绕过）"
fi

# 额外硬校验：tunnel 私有协议修复符号必须存在于 vendor（防止 vendor 与
# 工作区 tunnel/ 一起回退成旧代码时仍「一致」而放过）。
VT="vendor/github.com/yzfn/proxypilot/tunnel/node"
[ -f "$VT/server.go" ] || fail "缺少 $VT/server.go（tunnel 未同步）"
[ -f "$VT/stream.go" ] || fail "缺少 $VT/stream.go（tunnel 未同步）"
grep -q '已停用 DATAGRAM' "$VT/server.go" || fail "vendor 的 tunnel 缺少「已停用 DATAGRAM」降级逻辑"
grep -q '单连接双通道' "$VT/stream.go" || fail "vendor 的 tunnel 缺少「单连接双通道」逻辑"
grep -q 'X-VPN-Lane' "$VT/stream.go" || fail "vendor 的 tunnel 缺少「X-VPN-Lane」多路通道逻辑"
grep -q 'H3-Datagram' "$VT/"*.go || fail "vendor 的 tunnel 缺少「H3-Datagram」能力协商逻辑"

say "[check-vendor] OK：vendor/ 与工作区模块一致，tunnel 私有协议修复符号齐全"
exit 0