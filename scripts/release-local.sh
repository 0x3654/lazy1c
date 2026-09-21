#!/bin/bash
# Локальная проверка релиза: те же цели, что в .github/workflows/release.yml,
# но сборка в контейнере (хост не пачкаем). Артефакты — в dist/ (в .gitignore).
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p dist
RUN="docker run --rm -v $PWD:/src -w /src -v lazy1c-gomod:/go/pkg/mod -v lazy1c-gocache:/root/.cache/go-build golang:1.27-alpine"
build() { # os arch out
  echo "→ $3"
  $RUN sh -c "CGO_ENABLED=0 GOOS=$1 GOARCH=$2 go build -trimpath -o $3 ."
}
build darwin arm64 dist/lazy1c_darwin_arm64
build darwin amd64 dist/lazy1c_darwin_amd64
build linux  amd64 dist/lazy1c_linux_amd64
build linux  arm64 dist/lazy1c_linux_arm64
build windows amd64 dist/lazy1c_windows_amd64.exe
cd dist && shasum -a 256 * > sha256-checksums.txt 2>/dev/null || sha256sum * > sha256-checksums.txt
cat sha256-checksums.txt
