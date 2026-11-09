#!/usr/bin/env bash
# =====================================================================
#  ProxyPilot 磁盘体检与回收（单机部署）
#
#  用法（root）：
#    ./maintenance.sh            # 只体检并给出建议
#    ./maintenance.sh --fix      # 执行安全回收（清 binlog / 清旧日志 / 清旧备份）
#
#  建议用 root 的 crontab 每日执行一次 --fix：
#    17 4 * * * /opt/proxypilot/deploy/maintenance.sh --fix >/dev/null 2>&1
# =====================================================================
set -uo pipefail

APP_DIR=/opt/proxypilot
DO_FIX=0
[[ "${1:-}" == "--fix" ]] && DO_FIX=1

log()  { printf '\033[1;34m[maintenance]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*"; }

# ---------- 1. 磁盘概况 ----------
log "磁盘使用"
df -h / | tail -1

USED_PCT=$(df / | awk 'NR==2 {gsub("%","",$5); print $5}')
AVAIL=$(df -h / | awk 'NR==2 {print $4}')

if [[ "${USED_PCT:-0}" -ge 90 ]]; then
  warn "根分区已用 ${USED_PCT}%（可用 ${AVAIL}），MySQL 在写满时会变为不可写，面板会出现「所有页面无数据」"
fi

# ---------- 2. 定位大目录 ----------
log "占用最大的目录（Top 6）"
du -sh /var/lib/mysql /var/log /opt/proxypilot /root /tmp 2>/dev/null | sort -h | tail -6

# ---------- 3. binlog ----------
if mysqladmin ping -h 127.0.0.1 --silent >/dev/null 2>&1; then
  BINLOG_CNT=$(ls /var/lib/mysql/binlog.* 2>/dev/null | wc -l)
  BINLOG_SIZE=$(du -ch /var/lib/mysql/binlog.* 2>/dev/null | tail -1 | awk '{print $1}')
  LOG_BIN=$(mysql -uroot -N -e "SHOW VARIABLES LIKE 'log_bin'" 2>/dev/null | awk '{print $2}')
  log "binlog: log_bin=${LOG_BIN:-?} 文件数=${BINLOG_CNT} 占用=${BINLOG_SIZE:-0}"
  if [[ "${LOG_BIN:-OFF}" == "ON" ]]; then
    warn "binlog 仍处于开启状态，长期累积会写满磁盘；建议在 my.cnf 里加 skip-log-bin"
    if [[ $DO_FIX -eq 1 ]]; then
      log "清理过期 binlog（PURGE BINARY LOGS BEFORE NOW）"
      mysql -uroot -e "PURGE BINARY LOGS BEFORE NOW();" 2>&1 | head -2
    fi
  fi
else
  warn "MySQL 未响应，跳过 binlog 检查（先确认 systemctl status mysql）"
fi

# ---------- 4. 数据库体量 ----------
if mysqladmin ping -h 127.0.0.1 --silent >/dev/null 2>&1; then
  log "数据库 Top 5 表"
  mysql -uroot -N proxypilot -e "
    SELECT table_name, ROUND((data_length+index_length)/1024/1024,1)
    FROM information_schema.tables WHERE table_schema='proxypilot'
    ORDER BY (data_length+index_length) DESC LIMIT 5;" 2>/dev/null
fi

# ---------- 5. 日志与旧备份 ----------
if [[ $DO_FIX -eq 1 ]]; then
  log "回收 systemd journal 至 50MB"
  journalctl --vacuum-size=50M 2>&1 | tail -1

  log "清理 7 天前的 /tmp 内容"
  find /tmp -mindepth 1 -mtime +7 -exec rm -rf {} + 2>/dev/null || true

  log "保留最近 2 个二进制备份"
  ls -1t "$APP_DIR"/proxypilot.bak.* 2>/dev/null | tail -n +3 | while read -r f; do rm -f "$f"; done

  log "清理安装脚本临时文件"
  rm -f /root/chk_install.sh /root/chk_upgrade.sh /root/chk_uninstall.sh 2>/dev/null || true
fi

echo
log "完成。当前磁盘：$(df -h / | tail -1 | awk '{print $3" / "$2"  可用 "$4"  使用率 "$5}')"
if [[ $DO_FIX -eq 0 ]]; then
  log "如需自动回收，加 --fix 重新执行"
fi