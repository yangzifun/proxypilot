#!/usr/bin/env bash
# 把 Agent 产物发布到 R2（对象键前缀 agent/）
set -uo pipefail

SRC=/opt/proxypilot/agent-dist
VER=$(python3 -c "import json;print(json.load(open('$SRC/manifest.json'))['version'])")
DEST=/root/publish/agent${VER//./}
mkdir -p "$DEST"
cp -f "$SRC/manifest.json" "$SRC/SHA256SUMS" "$SRC/proxypilot-agent-linux-amd64" "$SRC/proxypilot-agent-linux-arm64" "$DEST/"

echo "=== 发布版本 $VER → $DEST ==="
ls -l "$DEST" | head -6

cd /root/publish
sqlv() { mysql -N -e "SELECT value FROM app_settings WHERE \`key\`='$1'" proxypilot 2>/dev/null; }
ENC=$(sqlv cloudflare_api_token)
ACC=$(sqlv cloudflare_account_id)
BKT=$(sqlv r2_bucket)
AKEYS=$(sqlv agent_key_secret)
JWT=$(sqlv jwt_secret)

if [ -z "$ENC" ] || [ -z "$ACC" ] || [ -z "$BKT" ]; then
  echo "缺少 R2 凭据（cloudflare_api_token / cloudflare_account_id / r2_bucket）" >&2
  exit 1
fi
# r2put 自身要用这两个密钥解开 token
export PP_AGENT_KEY_SECRET="$AKEYS" PP_JWT_SECRET="$JWT"
export R2_TOKEN="$(./r2dec "$ENC")" R2_ACCOUNT="$ACC" R2_BUCKET="$BKT"

fail=0
put() {
  if ./r2put "$1" "$2" "$3" >/dev/null 2>&1; then
    echo "  ✅ $2 ($(stat -c%s "$1") 字节)"
  else
    echo "  ❌ $2 上传失败"; fail=1
  fi
}

put "$DEST/manifest.json" agent/manifest.json application/json
put "$DEST/SHA256SUMS" agent/SHA256SUMS text/plain
put "$DEST/proxypilot-agent-linux-amd64" agent/proxypilot-agent-linux-amd64 application/octet-stream
put "$DEST/proxypilot-agent-linux-arm64" agent/proxypilot-agent-linux-arm64 application/octet-stream

echo "=== 回读校验 ==="
REMOTE=$(curl -s -m 20 https://s3.proxypilot.yangzifun.org/agent/manifest.json)
echo "$REMOTE" | head -c 220; echo
if echo "$REMOTE" | grep -q "\"$VER\""; then
  echo "R2 manifest 版本 = $VER ✅"
else
  echo "R2 manifest 版本不匹配 ❌"; fail=1
fi

# 校验 R2 上的二进制 sha256 与本地一致
LOCAL_SHA=$(sha256sum "$DEST/proxypilot-agent-linux-amd64" | awk '{print $1}')
REMOTE_SHA=$(curl -s -m 60 https://s3.proxypilot.yangzifun.org/agent/proxypilot-agent-linux-amd64 | sha256sum | awk '{print $1}')
if [ "$LOCAL_SHA" = "$REMOTE_SHA" ]; then
  echo "R2 二进制 sha256 一致 ✅ ($LOCAL_SHA)"
else
  echo "R2 二进制 sha256 不一致 ❌ 本地=$LOCAL_SHA 远程=$REMOTE_SHA"; fail=1
fi

exit $fail