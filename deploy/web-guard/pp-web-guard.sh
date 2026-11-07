#!/bin/sh
# Restrict site ports 80/443 to mesh (WARP) and Cloudflare origin IPs only.
set -u
CHAIN=PP_WEB80
MESH4=100.96.0.0/12
MESH6=2606:4700:cf1:1000::/64

CF4=$(curl -s --max-time 12 https://www.cloudflare.com/ips-v4 || true)
CF6=$(curl -s --max-time 12 https://www.cloudflare.com/ips-v6 || true)

if [ -z "$CF4" ]; then
  echo "ERROR: cannot fetch Cloudflare ranges, nothing changed"
  exit 1
fi

iptables -N "$CHAIN" 2>/dev/null || true
iptables -F "$CHAIN"
iptables -A "$CHAIN" -s "$MESH4" -j ACCEPT
iptables -A "$CHAIN" -s 127.0.0.0/8 -j ACCEPT
for c in $CF4; do
  iptables -A "$CHAIN" -s "$c" -j ACCEPT
done
iptables -A "$CHAIN" -j DROP

ip6tables -N "$CHAIN" 2>/dev/null || true
ip6tables -F "$CHAIN" 2>/dev/null || true
ip6tables -A "$CHAIN" -s "$MESH6" -j ACCEPT 2>/dev/null || true
ip6tables -A "$CHAIN" -s ::1/128 -j ACCEPT 2>/dev/null || true
for c in $CF6; do
  ip6tables -A "$CHAIN" -s "$c" -j ACCEPT 2>/dev/null || true
done
ip6tables -A "$CHAIN" -j DROP 2>/dev/null || true

for p in 80 443; do
  iptables -C INPUT -p tcp --dport $p -j "$CHAIN" 2>/dev/null || iptables -I INPUT 1 -p tcp --dport $p -j "$CHAIN"
  ip6tables -C INPUT -p tcp --dport $p -j "$CHAIN" 2>/dev/null || ip6tables -I INPUT 1 -p tcp --dport $p -j "$CHAIN" 2>/dev/null || true
done

echo "OK: 80/443 restricted to mesh + Cloudflare"
echo "CF4_RULES=$(iptables -S $CHAIN | grep -c 'ACCEPT')"
