#!/bin/sh
# =================================================================
#  ProxyPilot Agent 安装器（V9.2 / Go 版）—— 唯一的 shell 组件（只负责安装，不采集不执行任务）
#
#  流程：架构识别 → keycheck（向 Worker 验证密钥）→ 取 <base>/manifest.json（版本 + sha256）
#        → 从对象存储下载二进制 → sha256 强制校验 → 写配置(600) 与 systemd 服务 → 自动 doctor
#
#  安装：curl -fsSL <origin>/api/agent/install.sh | sudo sh -s -- --url <origin>
#        （令牌交互输入；自动化可加 --key ag_xxx，但注意命令行历史泄露风险）
#  卸载：sudo proxypilot-agent uninstall   （或 sh -s -- --uninstall）
#
#  可选参数：
#    --key <密钥>        接入密钥（交互方式：默认终端提示输入，不回显）
#    --url <地址>        Worker 地址（默认用下载本脚本时的域名）
#    --base <地址>       二进制所在对象存储基址（默认由 Worker 注入，可用 --base 覆盖）
#    --sha256 <hex>      强制指定二进制 sha256（桶里暂时没有 manifest.json 时的兜底）
#    --interval <秒>     兜底轮询间隔（10–900，服务端会用 next_poll_sec 覆盖）
#    --uninstall         卸载（停服务并删除二进制 / 配置 / systemd 单元）
#    --no-singbox        不安装 sing-box（默认会随 agent 一起装好并启动默认配置）
#
#  依赖：curl、uname、sha256sum（或 shasum / openssl）；systemd 缺失时回落 crontab。
#  说明：二进制来自公开可读的对象存储（不含任何密钥）；安装时用密钥向 Worker 做一次 keycheck，
#        之后密钥只用于 poll / ack，并写入 600 权限的 /etc/proxypilot-agent.conf。
# =================================================================

ORIGIN='https://your-worker.example.com'
BINARY_BASE='https://s3.proxypilot.yangzifun.org/agent'
KEY=''
SHA_OVERRIDE=''
INTERVAL=60
UNINSTALL=0
WITH_SINGBOX=1
DISABLE_SSH=0

BIN=/usr/local/bin/proxypilot-agent
CONF=/etc/proxypilot-agent.conf
UNIT=/etc/systemd/system/proxypilot-agent.service
# V8.1 shell agent 的遗留文件（安装时自动清理）
LEGACY_BIN=/usr/local/bin/proxypilot-agent.sh
LEGACY_TIMER=/etc/systemd/system/proxypilot-agent.timer

log() { echo "[proxypilot-agent] $*"; }
die() { log "$*"; exit 1; }

# ---------------- JSON 取值（纯 POSIX tr/sed/head；同时容忍「美化」与「紧凑」两种响应格式） ----------------
json_bool() {   # json_bool "<json文本>" <字段名> → true / false
    printf '%s' "$1" | tr -d ' \t\r\n' | sed -n "s/.*\"$2\":\([a-z]*\).*/\1/p" | head -n 1
}
json_str() {    # json_str "<json文本>" <字段名> → 字符串值（去掉引号）
    printf '%s' "$1" | sed -n "s/.*\"$2\" *: *\"\([^\"]*\)\".*/\1/p" | head -n 1
}

while [ $# -gt 0 ]; do
    case "$1" in
        --key|--token)
            if [ $# -ge 2 ]; then KEY="$2"; shift 2; else shift; fi ;;
        --url)
            if [ $# -ge 2 ]; then ORIGIN="$2"; shift 2; else shift; fi ;;
        --base)
            if [ $# -ge 2 ]; then BINARY_BASE="$2"; shift 2; else shift; fi ;;
        --sha256)
            if [ $# -ge 2 ]; then SHA_OVERRIDE="$2"; shift 2; else shift; fi ;;
        --interval)
            if [ $# -ge 2 ]; then INTERVAL="$2"; shift 2; else shift; fi ;;
        --uninstall) UNINSTALL=1; shift ;;
        --no-singbox) WITH_SINGBOX=0; shift ;;
        --disable-ssh) DISABLE_SSH=1; shift ;;
        *) shift ;;
    esac
done

[ "$(id -u)" = 0 ] || die '请用 root 执行（sudo）'

# ---------------- 卸载 ----------------
if [ "$UNINSTALL" = 1 ]; then
    log '停止服务并清理文件 ...'
    systemctl disable --now proxypilot-agent.service 2>/dev/null || true
    systemctl disable --now proxypilot-agent.timer 2>/dev/null || true
    rm -f "$UNIT" "$LEGACY_TIMER" "$BIN" "$LEGACY_BIN" "$CONF"
    systemctl daemon-reload 2>/dev/null || true
    log '已卸载（状态目录 /var/lib/proxypilot-agent 保留，可手动删除）'
    exit 0
fi

# 审计低-07：安装命令不再把令牌放在命令行（会进 shell history / ps / 截图）。
# 未通过 --key 提供时，从终端交互读取（curl | sh 的 stdin 是脚本本身，必须用 /dev/tty）。
if [ -z "$KEY" ]; then
    if [ -r /dev/tty ] && [ -t 0 ] 2>/dev/null || [ -r /dev/tty ]; then
        printf '接入令牌（ag_...，后台「服务器管理」里复制；输入不回显）: ' > /dev/tty 2>/dev/null || true
        if command -v stty >/dev/null 2>&1; then stty -echo </dev/tty 2>/dev/null || true; fi
        IFS= read -r KEY </dev/tty || true
        if command -v stty >/dev/null 2>&1; then stty echo </dev/tty 2>/dev/null || true; fi
        printf '\n' >/dev/tty 2>/dev/null || true
    fi
fi
[ -n "$KEY" ] || die '缺少接入令牌：可交互执行（终端会提示输入），或显式加 --key ag_xxx（注意命令行历史泄露风险）'
case "$KEY" in ag_*) : ;; *) die '接入密钥格式无效（应以 ag_ 开头）' ;; esac
[ -n "$ORIGIN" ] || die '缺少 Worker 地址（--url，或直接在后台复制完整安装命令）'

case "$INTERVAL" in ''|*[!0-9]*) INTERVAL=60 ;; esac
[ "$INTERVAL" -ge 10 ] || INTERVAL=10
[ "$INTERVAL" -le 900 ] || INTERVAL=900

# ---------------- 架构识别 ----------------
OS_RAW=$(uname -s 2>/dev/null || echo '')
[ "$OS_RAW" = 'Linux' ] || die "仅支持 Linux（当前 $OS_RAW）"
ARCH_RAW=$(uname -m 2>/dev/null || echo '')
case "$ARCH_RAW" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "暂不支持的架构：$ARCH_RAW（可用：amd64 / arm64）" ;;
esac
log "平台：linux-$ARCH；Worker：$ORIGIN；二进制基址：$BINARY_BASE"

# ---------------- 密钥校验（keycheck：拼错立刻失败，避免 agent 反复重试） ----------------
CHECK=$(curl -fsSL -m 30 -H "X-Agent-Token: $KEY" "$ORIGIN/api/agent/keycheck" 2>/dev/null) \
    || die "密钥校验失败（$ORIGIN/api/agent/keycheck）：请确认密钥正确、服务器已启用、域名可达"
# 注意：Worker 的 jsonResponse 是「美化 JSON」（多行 + "ok": true），这里必须做格式无关解析
[ "$(json_bool "$CHECK" ok)" = 'true' ] \
    || die "密钥校验未通过：$(printf '%s' "$CHECK" | tr '\n\r' '  ' | cut -c1-200)"
SRV_NAME=$(json_str "$CHECK" server_name)
log "密钥校验通过（服务器：${SRV_NAME:-未知}）"

# ---------------- 取 manifest（版本 + sha256） ----------------
# 审计低-07：之前这里无条件覆盖成写死的桶地址，导致 --base 与后台配置的分发基址
# 全部失效；现在只在未指定时才使用默认值。
[ -n "$BINARY_BASE" ] || BINARY_BASE='https://s3.proxypilot.yangzifun.org/agent'
MANIFEST=$(curl -fsSL -m 30 "$BINARY_BASE/manifest.json" 2>/dev/null) || MANIFEST=''
MANIFEST_FLAT=$(printf '%s' "$MANIFEST" | tr -d ' \t\r\n')   # 压缩空白：美化 / 紧凑两种格式都能解析
VERSION=$(printf '%s' "$MANIFEST_FLAT" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p' | head -n 1)
SHA=$(printf '%s' "$MANIFEST_FLAT" | sed -n "s/.*\"linux-$ARCH\":[^}]*\"sha256\":\"\([0-9a-fA-F]*\)\".*/\1/p" | head -n 1)
if [ -n "$SHA" ]; then
    # manifest 可用时以 manifest 为准；--sha256 只允许与之一致，禁止静默覆盖。
    if [ -n "$SHA_OVERRIDE" ]; then
        SHA_L=$(printf '%s' "$SHA" | tr 'A-Z' 'a-z')
        OVR_L=$(printf '%s' "$SHA_OVERRIDE" | tr 'A-Z' 'a-z')
        [ "$SHA_L" = "$OVR_L" ] || die 'manifest 与 --sha256 不一致（可能被篡改或版本不对）：已中止安装'
        log '--sha256 与 manifest 一致'
    fi
elif [ -n "$SHA_OVERRIDE" ]; then
    SHA=$(printf '%s' "$SHA_OVERRIDE" | tr 'A-Z' 'a-z')
    log 'manifest 不可用，使用 --sha256 指定的校验值'
else
    die "无法从 $BINARY_BASE/manifest.json 取得 linux-$ARCH 的 sha256：请确认已上传 manifest.json（或改用 --sha256 <hex>）"
fi
[ -n "$VERSION" ] || VERSION='unknown'
log "目标版本：$VERSION；期望 sha256：$(printf '%s' "$SHA" | cut -c1-12)…"

# ---------------- 下载二进制（公开对象存储，无需密钥） ----------------
TMP=$(mktemp 2>/dev/null || echo "/tmp/proxypilot-agent.$$")
trap 'rm -f "$TMP"' EXIT INT TERM
BIN_URL="$BINARY_BASE/proxypilot-agent-linux-$ARCH"
log "下载二进制：$BIN_URL"
curl -fsSL -m 300 -o "$TMP" "$BIN_URL" \
    || die "二进制下载失败（$BIN_URL）：请确认已上传该架构产物且桶允许公开读取"
[ -s "$TMP" ] || die '下载内容为空'

# ---------------- sha256 校验（必须通过；校验工具缺失即中止） ----------------
if command -v sha256sum >/dev/null 2>&1; then
    GOT=$(sha256sum "$TMP" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    GOT=$(shasum -a 256 "$TMP" | awk '{print $1}')
elif command -v openssl >/dev/null 2>&1; then
    GOT=$(openssl dgst -sha256 "$TMP" 2>/dev/null | awk '{print $NF}')
else
    die '缺少 sha256sum / shasum / openssl，无法校验二进制完整性'
fi
[ -n "$GOT" ] || die '无法计算下载内容的 sha256'
if [ "$(printf '%s' "$GOT" | tr 'A-Z' 'a-z')" != "$(printf '%s' "$SHA" | tr 'A-Z' 'a-z')" ]; then
    die "sha256 校验失败（期望 $SHA，实际 $GOT）：已中止安装，请重试或核对发布产物"
fi
log 'sha256 校验通过'

# ---------------- 安装二进制 ----------------
if command -v install >/dev/null 2>&1; then
    install -m 700 "$TMP" "$BIN" || die "安装二进制失败（$BIN）"
else
    cp "$TMP" "$BIN" || die "复制二进制失败（$BIN）"
    chmod 700 "$BIN" || die "设置权限失败（$BIN）"
fi
log "已安装二进制：$BIN（$VERSION）"

# ---------------- 写配置（600） ----------------
umask 077
{
    echo '# ProxyPilot Agent 配置（由安装脚本生成；修改后 systemctl restart proxypilot-agent）'
    echo 'PROXYPILOT_URL="'"$ORIGIN"'"'
    echo 'PROXYPILOT_TOKEN="'"$KEY"'"'
    echo "INTERVAL=$INTERVAL"
    echo 'STATE_DIR="/var/lib/proxypilot-agent"'
    echo 'SERVICE_LIST="sshd sing-box mtg cloudflared clash mihomo nginx"'
} > "$CONF" || die "写入配置失败（$CONF）"
chmod 600 "$CONF"
log "已写入配置：$CONF（600）"

# ---------------- 写 systemd 服务 ----------------
{
    echo '[Unit]'
    echo 'Description=ProxyPilot Agent (heartbeat / metrics / config deploy)'
    echo 'After=network-online.target'
    echo 'Wants=network-online.target'
    echo ''
    echo '[Service]'
    echo 'Type=simple'
    echo "ExecStart=$BIN run"
    echo 'Restart=always'
    echo 'RestartSec=5'
    echo 'StandardOutput=journal'
    echo 'StandardError=journal'
    echo ''
    echo '[Install]'
    echo 'WantedBy=multi-user.target'
} > "$UNIT" || die "写入 systemd 单元失败（$UNIT）"

# ---------------- 清理 V8.1 shell agent 遗留 ----------------
if [ -f "$LEGACY_TIMER" ] || [ -f "$LEGACY_BIN" ]; then
    log '检测到 V8.1 shell agent 遗留，正在清理 ...'
    systemctl disable --now proxypilot-agent.timer 2>/dev/null || true
    rm -f "$LEGACY_TIMER" "$LEGACY_BIN"
fi

# ---------------- 启动 ----------------
systemctl daemon-reload 2>/dev/null || true
if systemctl enable --now proxypilot-agent.service 2>/dev/null; then
    log '服务已启动：systemctl status proxypilot-agent'
else
    log '未检测到 systemd：请用 cron 兜底，例如：*/5 * * * * /usr/local/bin/proxypilot-agent once'
fi

# ---------------- 可选：停用 SSH（防止被爆破） ----------------
if [ "$DISABLE_SSH" = 1 ]; then
    if systemctl is-active --quiet proxypilot-agent 2>/dev/null; then
        SSH_UNIT=''
        if systemctl list-unit-files 2>/dev/null | grep -q '^sshd\.service'; then SSH_UNIT=sshd;
        elif systemctl list-unit-files 2>/dev/null | grep -q '^ssh\.service'; then SSH_UNIT=ssh; fi
        if [ -n "$SSH_UNIT" ]; then
            systemctl disable --now "$SSH_UNIT" 2>/dev/null || true
            systemctl disable --now ssh.socket 2>/dev/null || true
            log "已停用 SSH（$SSH_UNIT）。需要恢复：sudo systemctl enable --now $SSH_UNIT"
        else
            log '未发现 sshd/ssh 服务，跳过停用 SSH'
        fi
    else
        log 'Agent 未运行，为防失联跳过停用 SSH'
    fi
fi


# ---------------- 同步安装 sing-box（V11.6：默认配置 + systemd 服务） ----------------
if [ "$WITH_SINGBOX" = 1 ]; then
    log '安装 sing-box（默认配置 + 服务）...'
    BOOT=$(curl -fsSL -m 30 -H "X-Agent-Token: $KEY" "$ORIGIN/api/agent/bootstrap?arch=$ARCH" 2>/dev/null) || BOOT=''
    if [ "$(json_bool "$BOOT" ok)" = 'true' ]; then
        SB_TARGET=$(json_str "$BOOT" target)
        SB_SERVICE=$(json_str "$BOOT" service)
        [ -n "$SB_TARGET" ] || SB_TARGET=/etc/sing-box/sb.json
        [ -n "$SB_SERVICE" ] || SB_SERVICE=sing-box
        SB_DIR=$(dirname "$SB_TARGET")
        mkdir -p "$SB_DIR"
        # 重要：已有配置时绝不覆盖。
        # 节点重装/重启后执行本脚本时，bootstrap 下发的只是「默认模板」，
        # 而面板可能早已下发过完整配置（含全部入站与授权用户）。
        # 之前无条件覆盖，导致：面板 applied_version 仍是旧值、以为已同步，
        # 节点实际跑的是 4 入站默认配置 —— 服务 active 但少了协议，且无人察觉。
        # 实测故障：曼谷1（server 35），2026-11-12。
        CFG_B64=$(json_str "$BOOT" config_b64)
        KEEP_EXISTING=0
        if [ -s "$SB_TARGET" ] && sing-box check -c "$SB_TARGET" >/dev/null 2>&1; then
            KEEP_EXISTING=1
        elif [ -s "$SB_TARGET" ] && [ -z "$(command -v sing-box)" ]; then
            KEEP_EXISTING=1
        fi
        if [ "$KEEP_EXISTING" = 1 ]; then
            log "检测到现有可用配置，保留不覆盖：$SB_TARGET（如需重置请加 --force-config）"
        fi
        if [ -n "$CFG_B64" ] && [ "$KEEP_EXISTING" = 0 ]; then
            if printf '%s' "$CFG_B64" | base64 -d > "$SB_TARGET" 2>/dev/null; then :
            else printf '%s' "$CFG_B64" | openssl base64 -d -A > "$SB_TARGET"; fi
            chmod 600 "$SB_TARGET"
            log "已写入默认配置：$SB_TARGET"
        fi
        if [ ! -f "$SB_DIR/cert.crt" ] || [ ! -f "$SB_DIR/private.key" ]; then
            if command -v openssl >/dev/null 2>&1; then
                if openssl req -x509 -newkey rsa:2048 -days 3650 -nodes -keyout "$SB_DIR/private.key" -out "$SB_DIR/cert.crt" -subj '/CN=www.bing.com' >/dev/null 2>&1; then
                    log '已生成自签证书（anytls / hysteria2 用）'
                else
                    log '证书生成失败：anytls / hysteria2 将无法启动'
                fi
            else
                log '缺少 openssl：跳过证书生成（anytls / hysteria2 将无法启动）'
            fi
        fi
        SB_URL=$(json_str "$BOOT" binary_url)
        SB_SHA=$(json_str "$BOOT" binary_sha256)
        # 审计中-02：组件（sing-box）必须校验 sha256；没有 sha256 就拒绝安装，
        # 不再回退到「无校验的 GitHub 直连下载」。
        if [ -n "$SB_URL" ] && [ -z "$SB_SHA" ]; then
            log 'sing-box 下载地址缺少 sha256（后台未配置 component_sha_*）：拒绝安装未校验组件'
            SB_URL=''
        fi
        if [ -n "$SB_URL" ]; then
            SBTMP=$(mktemp 2>/dev/null || echo "/tmp/sing-box.$$")
            if curl -fsSL -m 300 -o "$SBTMP" "$SB_URL" && [ -s "$SBTMP" ]; then
                SBOK=0
                if command -v sha256sum >/dev/null 2>&1; then
                    SBGOT=$(sha256sum "$SBTMP" | awk '{print $1}')
                elif command -v shasum >/dev/null 2>&1; then
                    SBGOT=$(shasum -a 256 "$SBTMP" | awk '{print $1}')
                elif command -v openssl >/dev/null 2>&1; then
                    SBGOT=$(openssl dgst -sha256 "$SBTMP" 2>/dev/null | awk '{print $NF}')
                else
                    SBGOT=''
                fi
                if [ -z "$SBGOT" ]; then
                    log '缺少 sha256 校验工具（sha256sum/shasum/openssl），拒绝安装 sing-box'
                elif [ "$(printf '%s' "$SBGOT" | tr 'A-Z' 'a-z')" != "$(printf '%s' "$SB_SHA" | tr 'A-Z' 'a-z')" ]; then
                    log 'sing-box sha256 校验失败，已跳过（未安装）'
                else
                    SBOK=1
                fi
                if [ "$SBOK" = 1 ]; then
                    if install -m 755 "$SBTMP" /usr/local/bin/sing-box 2>/dev/null; then :
                    else cp "$SBTMP" /usr/local/bin/sing-box && chmod 755 /usr/local/bin/sing-box; fi
                    log 'sing-box 已安装并通过 sha256 校验：/usr/local/bin/sing-box'
                fi
                rm -f "$SBTMP"
            else
                log "sing-box 下载失败（$SB_URL）：可稍后在后台推送时重试"
            fi
        else
            log '未配置可校验的 sing-box 下载地址（需同时配置 component_base_url 与 component_sha_*），跳过二进制安装'
        fi
        SB_UNIT="/etc/systemd/system/$SB_SERVICE.service"
        if [ -x /usr/local/bin/sing-box ] && [ ! -f "$SB_UNIT" ]; then
            {
                echo '[Unit]'
                echo "Description=sing-box ($SB_SERVICE)"
                echo 'After=network-online.target'
                echo 'Wants=network-online.target'
                echo ''
                echo '[Service]'
                echo "ExecStart=/usr/local/bin/sing-box run -c $SB_TARGET"
                echo 'Restart=always'
                echo 'RestartSec=5'
                echo 'LimitNOFILE=65536'
                echo ''
                echo '[Install]'
                echo 'WantedBy=multi-user.target'
            } > "$SB_UNIT"
        fi
        systemctl daemon-reload 2>/dev/null || true
        if [ -x /usr/local/bin/sing-box ]; then
            if /usr/local/bin/sing-box check -c "$SB_TARGET" >/dev/null 2>&1; then
                if systemctl enable --now "$SB_SERVICE" >/dev/null 2>&1; then
                    log "已启用并启动服务：$SB_SERVICE"
                else
                    log "启用 $SB_SERVICE 失败：请查看 journalctl -u $SB_SERVICE"
                fi
            else
                log 'sing-box 配置校验未通过，未启动服务（可稍后在后台推送替换）'
            fi
        fi
    else
        log 'bootstrap 未通过，跳过 sing-box 安装（agent 本身仍可正常使用）'
    fi
fi

# ---------------- 自检（非致命） ----------------
if [ -x "$BIN" ]; then
    log '连通性自检（doctor：只做一次 poll，不执行任务）...'
    "$BIN" doctor -c "$CONF" || log '自检未通过：请查看 proxypilot-agent doctor 与 journalctl -u proxypilot-agent'
fi

log '安装完成'
exit 0
