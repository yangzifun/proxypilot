#!/usr/bin/env bash
# =====================================================================
#  ProxyPilot 单机安装脚本（直接安装在服务器上，不使用任何容器）
#
#  适用：Debian 12/13、Ubuntu 22.04/24.04/26.04、Rocky/AlmaLinux 9
#  做法：系统包管理器装 MySQL + 二进制定时运行 + systemd 托管
#
#  用法（root）：
#    ./install.sh                     # 交互式安装到 /opt/proxypilot
#    ./install.sh --yes               # 全自动（自动生成密钥与口令）
#    ./install.sh --port 80           # 指定监听端口（默认 8080）
#    ./install.sh --binary ./proxypilot --agent-dist ./agent-dist
#    ./install.sh --dsn "user:pass@tcp(127.0.0.1:3306)/proxypilot?..."  # 用已有 MySQL
#
#  幂等：重复执行只会升级二进制与配置，不会动数据库已有数据。
# =====================================================================
set -Eeuo pipefail

APP_NAME=proxypilot
APP_DIR=/opt/proxypilot
DATA_DIR=$APP_DIR/data
BUCKET_DIR=$DATA_DIR/bucket
DIST_DIR=$APP_DIR/agent-dist
SERVICE=/etc/systemd/system/$APP_NAME.service

PORT=8080
BINARY=""
AGENT_DIST=""
EXTERNAL_DSN=""
ASSUME_YES=0
DB_NAME=proxypilot
DB_USER=proxypilot
DB_PASS=""
ADMIN_PASS=""
PUBLIC_URL=""

log()  { printf '\033[1;34m[install]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port)       PORT="$2"; shift 2 ;;
    --binary)     BINARY="$2"; shift 2 ;;
    --agent-dist) AGENT_DIST="$2"; shift 2 ;;
    --dsn)        EXTERNAL_DSN="$2"; shift 2 ;;
    --db-name)    DB_NAME="$2"; shift 2 ;;
    --db-user)    DB_USER="$2"; shift 2 ;;
    --db-pass)    DB_PASS="$2"; shift 2 ;;
    --admin-pass) ADMIN_PASS="$2"; shift 2 ;;
    --public-url) PUBLIC_URL="$2"; shift 2 ;;
    -y|--yes)     ASSUME_YES=1; shift ;;
    -h|--help)    sed -n '2,20p' "$0"; exit 0 ;;
    *) die "未知参数：$1（--help 查看用法）" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "请用 root 运行（sudo ./install.sh）"

# ---------- 1. 系统识别 ----------
. /etc/os-release 2>/dev/null || die "无法识别系统（缺少 /etc/os-release）"
OS_ID=${ID:-unknown}
OS_LIKE=${ID_LIKE:-}
log "系统：${PRETTY_NAME:-$OS_ID}"

case " $OS_ID $OS_LIKE " in
  *" debian "*|*" ubuntu "*)
    PKG=deb
    ;;
  *" rhel "*|*" centos "*|*" fedora "*|*" rocky "*|*" almalinux "*)
    PKG=rpm
    ;;
  *)
    die "不支持的系统：$OS_ID（仅支持 Debian/Ubuntu 与 RHEL 系）"
    ;;
esac

need_pkg() { command -v "$1" >/dev/null 2>&1; }

install_pkgs() {
  if [[ $PKG == deb ]]; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq "$@"
  else
    dnf install -y -q "$@" || yum install -y -q "$@"
  fi
}

# 基础工具
missing=()
for b in curl tar openssl ss; do
  need_pkg "$b" || missing+=("$b")
done
if [[ ${#missing[@]} -gt 0 ]]; then
  log "安装基础工具：${missing[*]}"
  install_pkgs "${missing[@]}" || true
fi
need_pkg curl || die "缺少 curl，无法继续"

# ---------- 2. 数据库 ----------
mysql_ready() {
  [[ -n "${EXTERNAL_DSN}" ]] && return 0
  mysqladmin ping -h 127.0.0.1 --silent >/dev/null 2>&1
}

if [[ -n "$EXTERNAL_DSN" ]]; then
  log "使用外部 MySQL（--dsn 已指定），跳过本机安装"
  DSN="$EXTERNAL_DSN"
else
  if ! need_pkg mysqld && ! need_pkg mariadbd; then
    log "安装 MySQL Server（系统包管理器）"
    if [[ $PKG == deb ]]; then
      install_pkgs mysql-server
    else
      install_pkgs mysql-server || install_pkgs mariadb-server
    fi
  else
    log "检测到已安装 MySQL/MariaDB，复用"
  fi

  if [[ $PKG == deb ]]; then
    systemctl enable --now mysql 2>/dev/null || systemctl enable --now mariadb 2>/dev/null || true
  else
    systemctl enable --now mysqld 2>/dev/null || systemctl enable --now mariadb 2>/dev/null || true
  fi

  for _ in $(seq 1 60); do mysql_ready && break; sleep 1; done
  mysql_ready || die "MySQL 启动失败，请检查 journalctl -u mysql"

  [[ -n "$DB_PASS" ]] || DB_PASS=$(openssl rand -hex 16)
  log "创建数据库 $DB_NAME 与用户 $DB_USER（如已存在则跳过）"
  mysql -uroot <<SQL
CREATE DATABASE IF NOT EXISTS \`$DB_NAME\` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER IF NOT EXISTS '$DB_USER'@'127.0.0.1' IDENTIFIED BY '$DB_PASS';
CREATE USER IF NOT EXISTS '$DB_USER'@'localhost' IDENTIFIED BY '$DB_PASS';
GRANT ALL PRIVILEGES ON \`$DB_NAME\`.* TO '$DB_USER'@'127.0.0.1';
GRANT ALL PRIVILEGES ON \`$DB_NAME\`.* TO '$DB_USER'@'localhost';
FLUSH PRIVILEGES;
SQL
  DSN="$DB_USER:$DB_PASS@tcp(127.0.0.1:3306)/$DB_NAME?charset=utf8mb4&loc=UTC&parseTime=false"
fi

# ---------- 3. 目录与用户 ----------
id -u "$APP_NAME" >/dev/null 2>&1 || useradd --system --home "$APP_DIR" --shell /usr/sbin/nologin "$APP_NAME"
mkdir -p "$APP_DIR" "$BUCKET_DIR" "$DIST_DIR" "$APP_DIR/uploads"
chown -R "$APP_NAME:$APP_NAME" "$APP_DIR"
chmod 750 "$APP_DIR"

# ---------- 4. 二进制 ----------
if [[ -z "$BINARY" ]]; then
  # 未指定时，尝试用仓库内构建脚本产出的位置
  for cand in ./proxypilot ./dist/proxypilot-linux-amd64 ../dist/proxypilot-linux-amd64; do
    [[ -f "$cand" ]] && BINARY="$cand" && break
  done
fi
[[ -n "$BINARY" && -f "$BINARY" ]] || die "找不到 proxypilot 二进制，请用 --binary 指定"

log "安装二进制：$BINARY"
install -m 0755 "$BINARY" "$APP_DIR/proxypilot.new"
[[ -f "$APP_DIR/proxypilot" ]] && cp -a "$APP_DIR/proxypilot" "$APP_DIR/proxypilot.bak.$(date +%Y%m%d%H%M%S)"
mv -f "$APP_DIR/proxypilot.new" "$APP_DIR/proxypilot"

# ---------- 5. Agent 分发包（可选） ----------
if [[ -n "$AGENT_DIST" && -d "$AGENT_DIST" ]]; then
  log "拷贝 Agent 分发文件：$AGENT_DIST → $DIST_DIR"
  cp -a "$AGENT_DIST"/. "$DIST_DIR"/
  chown -R "$APP_NAME:$APP_NAME" "$DIST_DIR"
fi

# ---------- 6. .env ----------
ENV_FILE=$APP_DIR/.env
if [[ -f "$ENV_FILE" ]]; then
  log "保留现有 .env（只在缺失字段时补齐）"
  grep -q '^PP_JWT_SECRET=.' "$ENV_FILE" || echo "PP_JWT_SECRET=$(openssl rand -base64 48 | tr -d '\n')" >> "$ENV_FILE"
else
  log "生成 $ENV_FILE"
  JWT=$(openssl rand -base64 48 | tr -d '\n')
  [[ -n "$ADMIN_PASS" ]] || ADMIN_PASS=$(openssl rand -base64 12 | tr -dc 'A-Za-z0-9' | head -c 16)
  cat > "$ENV_FILE" <<EOF
# ProxyPilot 运行配置（由 deploy/install.sh 生成）
PP_ADDR=:$PORT
PP_MYSQL_DSN=$DSN
PP_JWT_SECRET=$JWT
PP_ADMIN_PASSWORD=$ADMIN_PASS
PP_AUTO_MIGRATE=1
PP_BUCKET_DIR=$BUCKET_DIR
PP_AGENT_DIST_DIR=$DIST_DIR
PP_MAIL_FROM=notice@example.com
PP_MAIL_PROVIDER=auto
EOF
  [[ -n "$PUBLIC_URL" ]] && echo "PP_PUBLIC_BASE_URL=$PUBLIC_URL" >> "$ENV_FILE"
  chmod 600 "$ENV_FILE"
fi
chown "$APP_NAME:$APP_NAME" "$ENV_FILE"

ADMIN_SHOWN=$(grep -m1 '^PP_ADMIN_PASSWORD=' "$ENV_FILE" | cut -d= -f2- || true)

# ---------- 7. systemd ----------
log "写入 systemd 单元：$SERVICE"
cat > "$SERVICE" <<EOF
[Unit]
Description=ProxyPilot（Go + MySQL 控制面板）
After=network-online.target mysql.service mariadb.service
Wants=network-online.target

[Service]
Type=simple
User=$APP_NAME
Group=$APP_NAME
WorkingDirectory=$APP_DIR
EnvironmentFile=$ENV_FILE
ExecStart=$APP_DIR/proxypilot
Restart=always
RestartSec=3
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$APP_NAME" >/dev/null 2>&1 || true
systemctl restart "$APP_NAME"

# ---------- 8. 健康检查 ----------
log "等待服务就绪..."
ok=0
for _ in $(seq 1 60); do
  if curl -fsS -m 3 "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then ok=1; break; fi
  sleep 1
done

echo
if [[ $ok -eq 1 ]]; then
  HEALTH=$(curl -fsS -m 3 "http://127.0.0.1:$PORT/healthz" || true)
  log "安装完成 ✅  $HEALTH"
else
  warn "服务未在 60 秒内就绪，请执行： journalctl -u $APP_NAME -n 80 --no-pager"
fi

IP=$(curl -fsS -m 3 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')
cat <<EOF

==================== 安装结果 ====================
 安装目录    : $APP_DIR
 配置文件    : $ENV_FILE
 监听地址    : 0.0.0.0:$PORT
 管理后台    : http://${IP:-<服务器IP>}:$PORT/admin
 管理员账号  : admin
 管理员口令  : ${ADMIN_SHOWN:-（沿用 .env 中原有口令）}
 Agent 分发   : http://${IP:-<服务器IP>}:$PORT/agent-dist
 日志        : journalctl -u $APP_NAME -f
==================================================

建议下一步：
  1) 登录后台，在「系统设置」里配置对外访问地址、登录方式、SMTP/OIDC
  2) 若通过 Cloudflare 反代，请把本机 $PORT 端口仅对回环/内网开放
  3) 安装节点 Agent：见 agent/install.sh
EOF