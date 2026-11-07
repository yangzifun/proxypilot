#!/usr/bin/env bash
# =====================================================================
#  ProxyPilot 卸载脚本（单机直接安装，无容器）
#
#  用法（root）：
#    ./uninstall.sh            # 停服并删除程序与配置，保留数据库
#    ./uninstall.sh --purge    # 连数据库与系统用户一起删除
# =====================================================================
set -Eeuo pipefail

APP_NAME=proxypilot
APP_DIR=/opt/proxypilot
SERVICE="/etc/systemd/system/${APP_NAME}.service"
PURGE=0

log() { printf '\033[1;34m[uninstall]\033[0m %s\n' "$*"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --purge) PURGE=1; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) printf '未知参数：%s\n' "$1" >&2; exit 1 ;;
  esac
done

if [[ $EUID -ne 0 ]]; then
  echo "请用 root 运行" >&2
  exit 1
fi

DB_NAME=proxypilot
DB_USER=proxypilot
if [[ -f "$APP_DIR/.env" ]]; then
  DSN=$(grep -m1 '^PP_MYSQL_DSN=' "$APP_DIR/.env" | cut -d= -f2- || true)
  if [[ -n "$DSN" ]]; then
    parsed_db=$(printf '%s' "$DSN" | sed -n 's#.*)/\([^?]*\).*#\1#p')
    parsed_user=$(printf '%s' "$DSN" | sed -n 's#^\([^:]*\):.*#\1#p')
    [[ -n "$parsed_db" ]] && DB_NAME="$parsed_db"
    [[ -n "$parsed_user" ]] && DB_USER="$parsed_user"
  fi
fi

log "停止并禁用服务"
systemctl disable --now "$APP_NAME" 2>/dev/null || true
rm -f "$SERVICE"
systemctl daemon-reload

if [[ $PURGE -eq 1 ]]; then
  log "删除数据库 ${DB_NAME} 与用户 ${DB_USER}"
  mysql -uroot -e "DROP DATABASE IF EXISTS \`${DB_NAME}\`;" 2>/dev/null || true
  mysql -uroot -e "DROP USER IF EXISTS '${DB_USER}'@'127.0.0.1';" 2>/dev/null || true
  mysql -uroot -e "DROP USER IF EXISTS '${DB_USER}'@'localhost';" 2>/dev/null || true
  log "删除系统用户 ${APP_NAME}"
  userdel "$APP_NAME" 2>/dev/null || true
fi

log "删除安装目录 ${APP_DIR}"
rm -rf "$APP_DIR"

echo
if [[ $PURGE -eq 1 ]]; then
  log "已完全卸载（含数据库）"
else
  log "已卸载程序与配置；数据库 ${DB_NAME} 仍保留，如需彻底清理请加 --purge"
fi