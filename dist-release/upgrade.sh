#!/usr/bin/env bash
# =====================================================================
#  ProxyPilot 升级脚本（单机直接安装，无容器）
#
#  用法（root）：
#    ./upgrade.sh --binary ./proxypilot            # 用本地二进制升级
#    ./upgrade.sh --url https://x/proxypilot        # 从 URL 下载后升级
#    ./upgrade.sh --binary ./proxypilot --agent-dist ./agent-dist
#
#  行为：备份现有二进制 → 替换 → 重启 → 健康检查 → 失败自动回滚
# =====================================================================
set -Eeuo pipefail

APP_NAME=proxypilot
APP_DIR=/opt/proxypilot
BINARY=""
URL=""
AGENT_DIST=""
PORT=""

log()  { printf '\033[1;34m[upgrade]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary)     BINARY="$2"; shift 2 ;;
    --url)        URL="$2"; shift 2 ;;
    --agent-dist) AGENT_DIST="$2"; shift 2 ;;
    --port)       PORT="$2"; shift 2 ;;
    -h|--help)    sed -n '2,14p' "$0"; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "请用 root 运行"

[[ -f "$APP_DIR/.env" ]] || die "未检测到 $APP_DIR/.env，请先执行 deploy/install.sh"
[[ -n "$PORT" ]] || PORT=$(grep -m1 '^PP_ADDR=' "$APP_DIR/.env" | sed 's/.*://' || true)
[[ -n "$PORT" ]] || PORT=8080

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [[ -n "$URL" ]]; then
  log "下载新版本：$URL"
  curl -fsSL --max-time 300 -o "$TMP/proxypilot" "$URL" || die "下载失败"
  BINARY="$TMP/proxypilot"
fi
[[ -n "$BINARY" && -f "$BINARY" ]] || die "未提供二进制（--binary 或 --url）"

TS=$(date +%Y%m%d%H%M%S)
log "备份当前二进制 → proxypilot.bak.$TS"
cp -a "$APP_DIR/proxypilot" "$APP_DIR/proxypilot.bak.$TS"

log "替换二进制"
install -m 0755 "$BINARY" "$APP_DIR/proxypilot.new"
mv -f "$APP_DIR/proxypilot.new" "$APP_DIR/proxypilot"

if [[ -n "$AGENT_DIST" && -d "$AGENT_DIST" ]]; then
  log "同步 Agent 分发文件"
  mkdir -p "$APP_DIR/agent-dist"
  cp -a "$AGENT_DIST"/. "$APP_DIR/agent-dist"/
  chown -R "$APP_NAME:$APP_NAME" "$APP_DIR/agent-dist" 2>/dev/null || true
fi

log "重启服务"
systemctl restart "$APP_NAME"

ok=0
for _ in $(seq 1 45); do
  if curl -fsS -m 3 "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then ok=1; break; fi
  sleep 1
done

if [[ $ok -eq 1 ]]; then
  log "升级成功 ✅  $(curl -fsS "http://127.0.0.1:$PORT/healthz" || true)"
  log "清理旧备份（保留最近 3 个）"
  ls -1t "$APP_DIR"/proxypilot.bak.* 2>/dev/null | tail -n +4 | while read -r f; do rm -f "$f"; done
else
  printf '\033[1;31m[error]\033[0m 新版本未就绪，开始回滚\n'
  mv -f "$APP_DIR/proxypilot.bak.$TS" "$APP_DIR/proxypilot"
  systemctl restart "$APP_NAME"
  die "已回滚到上一版本，请检查 journalctl -u $APP_NAME -n 100 --no-pager"
fi