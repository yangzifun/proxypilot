#!/usr/bin/env bash
# =====================================================================
# ProxyPilot CSP 上线自检（只读，不改任何数据）
#
# 背景：V1.2 使用「每请求 nonce + 强制 CSP」，但线上前面有 Cloudflare 与缓存，
# HTML 与 CSP 头可能来自不同响应，nonce 对不上 → 全部脚本被拦 → 后台白屏。
# 当前设计：CSP 为常量（无 nonce）、HTML 无可执行内联脚本、动态数据走
# <script type="application/json" id="pp-boot"> 数据块、脚本全部同源外部文件。
#
# 用法：
#   bash scripts/csp-selfcheck.sh [BASE_URL]
#   bash scripts/csp-selfcheck.sh http://127.0.0.1:18080
#   bash scripts/csp-selfcheck.sh https://proxypilot.yangzifun.org
#
# 检查内容（逐页 /admin、/client-auth、/admin/settings、/admin/certs、/）：
#   1) HTTP 200；
#   2) 有强制 Content-Security-Policy，且没有 Content-Security-Policy-Report-Only；
#   3) CSP 里没有 nonce-，script-src 含 'self'、不含 unsafe-inline/unsafe-eval；
#   4) HTML 每个 <script> 都有 src（同源或 CSP 白名单 https 源）或 type 是非执行数据块；
#   5) HTML 没有 nonce= 属性、没有 on*= 内联事件处理器、没有可执行内联 boot 脚本；
#   6) /admin、/ 有 #pp-boot 数据块，JSON 可解析且 mode 正确；
#   7) 页面引用的同源外部脚本都能 200 取到；
#   8) 连续两次请求的 CSP 头完全相同（防止每请求 nonce 回归）。
#
# 退出码：0 全部通过；1 有失败项。
# =====================================================================
set -u

BASE="${1:-http://127.0.0.1:8080}"
BASE="${BASE%/}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

FAIL=0
OK=0
CSP=""
SEEN_SRC=""
fail() { echo "[FAIL] $*"; FAIL=$((FAIL + 1)); }
ok() { echo "[ OK ] $*"; OK=$((OK + 1)); }

fetch() { # $1=path $2=bodyfile $3=headerfile
  curl -fsS -m 20 -H 'Cache-Control: no-cache' -H 'Pragma: no-cache' \
    -D "$3" -o "$2" "$BASE$1" 2>"$TMP/curl.err"
}

csp_of() { # $1=headerfile
  grep -i '^content-security-policy:' "$1" 2>/dev/null | head -n1 \
    | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r'
}

# header_ok 成功时把 CSP 写入全局 CSP 并返回 0。
header_ok() { # $1=path $2=headerfile
  local path="$1" hdr="$2"
  CSP="$(csp_of "$hdr")"
  if [ -z "$CSP" ]; then fail "$path 缺少强制 Content-Security-Policy 响应头"; return 1; fi
  if grep -qi '^content-security-policy-report-only:' "$hdr"; then
    fail "$path 仍返回 Content-Security-Policy-Report-Only"; return 1
  fi
  case "$CSP" in
    *nonce-*) fail "$path 的 CSP 含 nonce（缓存/代理下会与 HTML 不一致，必须移除）"; return 1 ;;
  esac
  echo "$CSP" | grep -q "script-src[^;]*'self'" || { fail "$path 的 script-src 未包含 'self'"; return 1; }
  if echo "$CSP" | sed -n 's/.*script-src\([^;]*\).*/\1/p' | grep -q 'unsafe-inline'; then
    fail "$path 的 script-src 含 unsafe-inline"; return 1
  fi
  if echo "$CSP" | sed -n 's/.*script-src\([^;]*\).*/\1/p' | grep -q 'unsafe-eval'; then
    fail "$path 的 script-src 含 unsafe-eval"; return 1
  fi
  return 0
}

# 页面里每个 <script> 必须是外部脚本或非执行数据块；有失败返回 1。
scripts_ok() { # $1=path $2=bodyfile
  local path="$1" body="$2" tags tag lower src typ err=0
  tags="$(grep -io '<script[^>]*>' "$body" 2>/dev/null || true)"
  if [ -z "$tags" ]; then fail "$path 没有解析到任何 <script>（页面结构异常）"; return 1; fi
  while IFS= read -r tag; do
    [ -z "$tag" ] && continue
    lower="$(printf '%s' "$tag" | tr 'A-Z' 'a-z')"
    case "$lower" in
      *nonce=*) fail "$path 的 <script> 仍带 nonce 属性：$tag"; err=1; continue ;;
    esac
    src="$(printf '%s' "$tag" | sed -n 's/.*[[:space:]]src="\([^"]*\)".*/\1/p')"
    if [ -n "$src" ]; then
      case "$src" in
        /*) ;;
        https://challenges.cloudflare.com/*) ;;
        https://static.cloudflareinsights.com/*) ;;
        *) fail "$path 的外部脚本不在 CSP 白名单内：$src"; err=1 ;;
      esac
      case " $SEEN_SRC " in *" $src "*) ;; *) SEEN_SRC="$SEEN_SRC $src" ;; esac
      continue
    fi
    typ="$(printf '%s' "$tag" | sed -n 's/.*[[:space:]]type="\([^"]*\)".*/\1/p' | tr 'A-Z' 'a-z')"
    case "$typ" in
      application/json|application/ld+json|importmap|speculationrules|text/template|text/x-template|text/plain) ;;
      *) fail "$path 存在可执行内联脚本（强制 CSP 会拦截）：$tag"; err=1 ;;
    esac
  done <<< "$tags"
  if grep -Eio '<[a-z][^>]*[[:space:]]on[a-z]+[[:space:]]*=' "$body" >/dev/null 2>&1; then
    fail "$path 存在 on*= 内联事件处理器（强制 CSP 会拦截）"
    err=1
  fi
  return "$err"
}

# 同源外部脚本必须都能取到（防止忘记注册路由/embed 文件）。
script_files_ok() { # $1=path
  local path="$1" src code err=0
  for src in $SEEN_SRC; do
    case "$src" in /*) ;; *) continue ;; esac
    code="$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$BASE$src" || echo 000)"
    if [ "$code" != "200" ]; then
      fail "$path 引用的脚本 $src 返回 $code"
      err=1
    else
      ok "$path 引用脚本 $src 可用"
    fi
  done
  return "$err"
}

boot_block_ok() { # $1=path $2=bodyfile $3=expected_mode
  local path="$1" body="$2" mode="$3" json
  if ! grep -q 'id="pp-boot"' "$body"; then fail "$path 缺少 #pp-boot 数据块"; return 1; fi
  if ! grep -q 'type="application/json"' "$body"; then fail "$path 的 #pp-boot 不是 application/json 数据块"; return 1; fi
  if command -v python3 >/dev/null 2>&1; then
    json="$(python3 - "$body" <<'PY'
import json, re, sys
html = open(sys.argv[1], encoding='utf-8', errors='replace').read()
m = re.search(r'<script type="application/json" id="pp-boot">(.*?)</script>', html, re.S)
if not m:
    print('MISSING'); raise SystemExit(0)
try:
    d = json.loads(m.group(1))
except Exception as e:
    print('BADJSON:' + str(e)); raise SystemExit(0)
print('OK:' + str(d.get('mode', '')))
PY
)"
    case "$json" in
      OK:*)
        if [ "${json#OK:}" = "$mode" ]; then
          ok "$path #pp-boot JSON 正常（mode=$mode）"
        else
          fail "$path #pp-boot mode=${json#OK:}，期望 $mode"; return 1
        fi ;;
      *) fail "$path #pp-boot 不是合法 JSON：$json"; return 1 ;;
    esac
  else
    if grep -q "\"mode\":\"$mode\"" "$body"; then
      ok "$path #pp-boot 数据块存在（无 python3，跳过 JSON 解析）"
    else
      fail "$path #pp-boot 缺少 mode=$mode"; return 1
    fi
  fi
  return 0
}

check_page() { # $1=path $2=boot_mode(可空)
  local path="$1" mode="${2:-}" body="$TMP/body" hdr="$TMP/hdr"
  if ! fetch "$path" "$body" "$hdr"; then
    fail "$path 请求失败：$(tr '\n' ' ' < "$TMP/curl.err")"; return
  fi
  if ! header_ok "$path" "$hdr"; then return; fi
  ok "$path 使用强制 CSP（无 nonce）"
  SEEN_SRC=""
  if scripts_ok "$path" "$body"; then ok "$path 无可执行内联脚本"; fi
  [ -n "$mode" ] && boot_block_ok "$path" "$body" "$mode"
  script_files_ok "$path"
  if grep -qi 'nonce=' "$body"; then fail "$path 的 HTML 仍含 nonce= 属性"; fi
  if grep -Eq 'window\.__PP_BOOT__[[:space:]]*=' "$body"; then fail "$path 仍是可执行内联 boot 脚本"; fi
  printf '%s' "$CSP" > "$TMP/csp$(printf '%s' "$path" | tr '/' '_')"
}

echo "== ProxyPilot CSP 自检：$BASE =="
check_page "/admin" "admin"
check_page "/client-auth" ""
check_page "/admin/settings" ""
check_page "/admin/certs" ""
check_page "/" "portal"

# 两次请求的 CSP 必须完全一致（防止有人把每请求 nonce 加回来）
if fetch "/admin" "$TMP/b1" "$TMP/h1" && fetch "/admin" "$TMP/b2" "$TMP/h2"; then
  c1="$(csp_of "$TMP/h1")"
  c2="$(csp_of "$TMP/h2")"
  if [ -n "$c1" ] && [ "$c1" = "$c2" ]; then
    ok "连续两次请求 CSP 头完全一致（无每请求变量）"
  else
    fail "两次请求 CSP 头不一致：$c1 vs $c2"
  fi
else
  fail "/admin 二次请求失败，无法校验 CSP 一致性"
fi

echo "-----------------------------------------------"
echo "通过：$OK 项；失败：$FAIL 项"
if [ "$FAIL" -gt 0 ]; then
  echo "结论：FAIL —— 不要上线，先修复上面列出的问题。"
  exit 1
fi
echo "结论：PASS —— CSP 强制且不依赖 nonce，可进入人工浏览器检查清单。"
exit 0