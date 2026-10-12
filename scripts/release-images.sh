#!/bin/sh
# 分别构建 linux/amd64 与 linux/arm64 镜像。需要本机安装 Docker buildx。
# 用法：scripts/release-images.sh gonghao:local
set -eu
tag="${1:-gonghao:local}"
if ! command -v docker >/dev/null 2>&1; then
  echo "需要本机的 docker，脚本不会在没有 Docker 的环境里假装构建成功。" >&2
  exit 1
fi
docker buildx build --platform linux/amd64 -t "${tag}-amd64" --load .
docker buildx build --platform linux/arm64 -t "${tag}-arm64" --load .
echo "已构建 ${tag}-amd64 与 ${tag}-arm64"
