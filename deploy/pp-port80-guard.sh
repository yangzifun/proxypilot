#!/usr/bin/env bash
# 面板 80 端口仅允许回环（Cloudflare Tunnel / 本机反代），禁止公网直连。
# 安装：cp 到 /usr/local/sbin/pp-port80-guard.sh，配合 pp-port80-guard.service 开机执行。
set -euo pipefail

ensure() {
  local bin="$1"; shift
  local table="$1"; shift
  local spec="$*"
  if ! "$bin" -C "$table" $spec 2>/dev/null; then
    "$bin" -I "$table" ${POS:-1} $spec
  fi
}

# 先放行 lo，再丢弃其余 80/tcp（IPv4 + IPv6）
POS=1 ensure iptables INPUT -i lo -p tcp --dport 80 -j ACCEPT
POS=2 ensure iptables INPUT -p tcp --dport 80 -j DROP
POS=1 ensure ip6tables INPUT -i lo -p tcp --dport 80 -j ACCEPT
POS=2 ensure ip6tables INPUT -p tcp --dport 80 -j DROP
echo "port80 guard applied"
