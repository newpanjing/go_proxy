#!/bin/bash

# Go Proxy 多平台打包脚本

VERSION=${1:-"1.0.0"}
OUTPUT_DIR="dist"
BINARY_NAME="go-proxy"

# 清理
rm -rf $OUTPUT_DIR
mkdir -p $OUTPUT_DIR

# 颜色输出
GREEN='\033[0;32m'
NC='\033[0m'

echo "Building Go Proxy v${VERSION}..."
echo ""

# 打包目标平台
PLATFORMS=(
    "darwin/arm64"
    "darwin/amd64"
    "linux/arm64"
    "linux/amd64"
    "windows/amd64"
)

for PLATFORM in "${PLATFORMS[@]}"; do
    IFS='/' read -r GOOS GOARCH <<< "$PLATFORM"

    OUTPUT="${OUTPUT_DIR}/${BINARY_NAME}-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
        OUTPUT="${OUTPUT}.exe"
    fi

    echo -n "  Building ${GOOS}/${GOARCH} ... "
    CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags="-s -w -X main.Version=${VERSION}" \
        -o "$OUTPUT" \
        ./cmd/go-proxy/

    if [ $? -eq 0 ]; then
        SIZE=$(du -h "$OUTPUT" | cut -f1)
        echo -e "${GREEN}OK${NC} ($SIZE)"
    else
        echo "FAILED"
        exit 1
    fi
done

echo ""
echo "All builds completed in ${OUTPUT_DIR}/"
ls -lh $OUTPUT_DIR/
