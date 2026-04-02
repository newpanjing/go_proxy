#!/bin/bash

# Go Proxy 本机编译脚本

VERSION=${1:-"dev"}
BINARY_NAME="go-proxy"

GREEN='\033[0;32m'
NC='\033[0m'

# 从 git tag 获取版本号
if [ "$VERSION" = "dev" ] && git describe --tags --exact-match >/dev/null 2>&1; then
    VERSION=$(git describe --tags --abbrev=0 | sed 's/^v//')
fi

echo "Building ${BINARY_NAME} v${VERSION} for $(go env GOOS)/$(go env GOARCH) ..."

CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o "$BINARY_NAME" \
    ./cmd/go-proxy/

if [ $? -eq 0 ]; then
    SIZE=$(du -h "$BINARY_NAME" | cut -f1)
    echo -e "${GREEN}OK${NC} -> ./${BINARY_NAME} ($SIZE)"
else
    echo "FAILED"
    exit 1
fi
