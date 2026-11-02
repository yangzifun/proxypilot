#!/usr/bin/env sh
# 生产冒烟检查（不依赖登录）。用法：server/tools/smoke.sh https://pp.example.com
set -eu
BASE="${1:-http://127.0.0.1:8080}"
BASE="${BASE%/}"
fail=0

check() {
    name="$1"; url="$2"; expect="$3"
    code=$(curl -s -o /tmp/pp-smoke-body -w '%{http_code}' "$url" || echo 000)
    if [ "$code" = "$expect" ]; then
        printf 'OK   %-22s %s\n' "$name" "$code"
    else
        printf 'FAIL %-22s %s (expect %s)\n' "$name" "$code" "$expect"
        fail=1
    fi
}

check "healthz"       "$BASE/healthz" 200
check "admin shell"   "$BASE/admin" 200
check "portal shell"  "$BASE/" 200
check "app.js"        "$BASE/admin-app.js" 200
check "app.css"       "$BASE/admin-app.css" 200
check "world.svg"     "$BASE/assets/world.svg" 200
check "login -> admin" "$BASE/login" 302
check "manage -> /"   "$BASE/manage" 302

# boot 注入检查
if curl -s "$BASE/admin" | grep -q '"mode":"admin"'; then
    echo "OK   admin boot 注入"
else
    echo "FAIL admin boot 注入缺失"; fail=1
fi
if curl -s "$BASE/" | grep -q '"mode":"portal"'; then
    echo "OK   portal boot 注入"
else
    echo "FAIL portal boot 注入缺失"; fail=1
fi

# 订阅占位 UUID 应为 404/400（用于确认路由存在而不是 501）
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/sub/00000000-0000-4000-8000-000000000000" || echo 000)
if [ "$code" = "404" ] || [ "$code" = "400" ]; then
    echo "OK   sub route ($code)"
else
    echo "FAIL sub route $code"; fail=1
fi

echo
if [ "$fail" = "0" ]; then
    echo "冒烟检查通过：$BASE"
else
    echo "存在失败项：$BASE"
    exit 1
fi
