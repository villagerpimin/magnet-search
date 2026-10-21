#!/usr/bin/env bash
# 交叉编译各平台的二进制到 dist/。
# 用法：
#   scripts/build.sh              # 构建全部平台
#   scripts/build.sh windows      # 只构建指定平台
set -euo pipefail

cd "$(dirname "$0")/.."
out_dir="dist"
mkdir -p "$out_dir"

# 平台列表：GOOS/GOARCH/输出名后缀
platforms=(
  "windows amd64 .exe"
  "windows arm64 .exe"
  "linux amd64"
  "linux arm64"
  "darwin arm64"
  "darwin amd64"
)

want="${1:-all}"

for entry in "${platforms[@]}"; do
  read -r goos goarch ext <<<"$entry"
  if [ "$want" != "all" ] && [ "$want" != "$goos" ] && [ "$want" != "$goos/$goarch" ]; then
    continue
  fi
  # 文件名带上平台与架构，避免不同架构互相覆盖
  name="msearch-${goos}-${goarch}"
  if [ "$goos" = "windows" ]; then
    name="msearch-${goos}-${goarch}.exe"
  fi
  # CGO_ENABLED=0 保证纯静态链接，Windows 上不需要任何运行时或 VC++ 运行库
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w" -o "$out_dir/$name" ./cmd/msearch
  printf "  %-22s %s\n" "$out_dir/$name" "$(du -h "$out_dir/$name" | cut -f1)"
done

echo "完成，产物在 $out_dir/"
