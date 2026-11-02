#!/usr/bin/env bash
# =================================================================
#  构建 ProxyPilot Agent（多架构静态二进制 + SHA256SUMS + manifest.json）
#
#  用法：
#    ./build.sh                    # 版本自动取自 internal/agent.Version（加 v 前缀）
#    VERSION=v1.0.1 ./build.sh     # 指定版本（同时注入 -ldflags -X main.version）
#    TARGETS="linux/amd64" ./build.sh
#
#  产物（dist/）：
#    proxypilot-agent-linux-amd64 / -linux-arm64   静态二进制（CGO_ENABLED=0）
#    SHA256SUMS                                    校验和
#    manifest.json                                 与二进制一起上传到对象存储（安装脚本据此校验 sha256）
#
#  要求：Go >= 1.21（脚本内固定 GOTOOLCHAIN=local，使用本机工具链，不联网下载）
# =================================================================
set -euo pipefail
cd "$(dirname "$0")"

# V11.2 构建修复：禁止 Go 自动下载工具链（受限网络/离线环境不会卡在 "toolchain not available"）
export GOTOOLCHAIN=local

# Go 版本自检（go.mod 下限 1.21）：低于下限时给出明确提示，而不是晦涩的工具链报错
GO_MIN_MAJOR=1
GO_MIN_MINOR=21
if ! command -v go >/dev/null 2>&1; then
    echo "未找到 go 命令：请先安装 Go >= ${GO_MIN_MAJOR}.${GO_MIN_MINOR}（https://go.dev/dl/）" >&2
    exit 1
fi
GO_VER_RAW="$(go version)"
GO_MAJOR="$(echo "$GO_VER_RAW" | sed -n 's/.*go\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1/p' | head -n 1)"
GO_MINOR="$(echo "$GO_VER_RAW" | sed -n 's/.*go\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\2/p' | head -n 1)"
if [ -z "$GO_MAJOR" ] || [ -z "$GO_MINOR" ]; then
    echo "无法解析 Go 版本（go version 输出：$GO_VER_RAW）" >&2
    exit 1
fi
if [ "$GO_MAJOR" -lt "$GO_MIN_MAJOR" ] || { [ "$GO_MAJOR" -eq "$GO_MIN_MAJOR" ] && [ "$GO_MINOR" -lt "$GO_MIN_MINOR" ]; }; then
    echo "Go 版本过低：本机 go${GO_MAJOR}.${GO_MINOR}，本工程要求 >= go${GO_MIN_MAJOR}.${GO_MIN_MINOR}" >&2
    echo "请升级 Go（https://go.dev/dl/），或在 Go >= ${GO_MIN_MAJOR}.${GO_MIN_MINOR} 的机器上构建" >&2
    exit 1
fi
echo "==> Go：$GO_VER_RAW"

VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
    VERSION="v$(sed -n 's/.*Version = "\([^"]*\)".*/\1/p' internal/agent/agent.go | head -n 1)"
fi
case "$VERSION" in
    v*) : ;;
    *) VERSION="v$VERSION" ;;
esac
[ -n "$VERSION" ] && [ "$VERSION" != "v" ] || { echo "无法识别版本，请用 VERSION=vX.Y.Z ./build.sh" >&2; exit 1; }

TARGETS="${TARGETS:-linux/amd64 linux/arm64}"
DIST="dist"
rm -rf "$DIST"
mkdir -p "$DIST"

LDFLAGS="-s -w -X main.version=$VERSION"
echo "==> 版本 $VERSION，目标：$TARGETS"

for target in $TARGETS; do
    os="${target%%/*}"
    arch="${target##*/}"
    out="$DIST/proxypilot-agent-$os-$arch"
    echo "==> 构建 $os/$arch"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/proxypilot-agent
done

cd "$DIST"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum proxypilot-agent-* > SHA256SUMS
else
    shasum -a 256 proxypilot-agent-* > SHA256SUMS
fi

RELEASED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
{
    printf '{\n  "version": "%s",\n  "released_at": "%s",\n  "files": {\n' "$VERSION" "$RELEASED_AT"
    first=1
    for target in $TARGETS; do
        os="${target%%/*}"
        arch="${target##*/}"
        name="proxypilot-agent-$os-$arch"
        size="$(wc -c < "$name" | tr -d ' ')"
        sha="$(awk -v f="$name" '{ n=$2; sub(/^\*/, "", n); if (n == f) print $1 }' SHA256SUMS | head -n 1)"
        [ -n "$sha" ] || { echo "无法读取 $name 的校验和" >&2; exit 1; }
        if [ "$first" = "1" ]; then first=0; else printf ',\n'; fi
        printf '    "%s-%s": { "file": "%s", "size": %s, "sha256": "%s" }' \
            "$os" "$arch" "$name" "$size" "$sha"
    done
    printf '\n  }\n}\n'
} > manifest.json

cat SHA256SUMS
echo
echo "==> 产物："
ls -l . | sed 's/^/    /'
echo
echo "==> 上传到对象存储（任选其一；目标：<base>/ 扁平存放，含 manifest.json）"
echo "    基址示例：https://s3.proxypilot.yangzifun.org/agent"
echo "    1) Windows：\$env:AWS_ACCESS_KEY_ID='...'; \$env:AWS_SECRET_ACCESS_KEY='...'; ./publish.ps1 -Endpoint https://s3.proxypilot.yangzifun.org -Bucket agent"
echo "    2) aws-cli（S3 兼容）："
echo "       aws --endpoint-url https://s3.proxypilot.yangzifun.org s3 cp dist/ s3://agent/ --recursive --exclude '*' --include 'proxypilot-agent-*' --include 'manifest.json'"
echo "    3) mc（MinIO）："
echo "       mc cp dist/proxypilot-agent-linux-amd64 dist/proxypilot-agent-linux-arm64 dist/manifest.json myminio/proxypilot/"
echo "    4) 控制台 / 网盘手工上传：把 dist/proxypilot-agent-* 与 dist/manifest.json 放到桶根目录"
echo
echo "==> 完成后：Worker 侧无需任何绑定；如需换基址，配置环境变量 AGENT_BINARY_BASE_URL（默认 https://s3.proxypilot.yangzifun.org/agent）"