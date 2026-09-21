#!/bin/sh
# Установка lazy1c: определяет ОС/архитектуру, качает бинарник последнего
# релиза и проверяет sha256. Использование:
#   curl -fsSL https://raw.githubusercontent.com/0x3654/lazy1c/master/scripts/install.sh | sh
# или локально: sh scripts/install.sh [каталог]   (по умолчанию — текущий)
set -eu

REPO="0x3654/lazy1c"
DEST="${1:-.}"

os=$(uname -s)
arch=$(uname -m)
case "$os" in
  Darwin) os="darwin" ;;
  Linux)  os="linux" ;;
  *) echo "неизвестная ОС: $os — для Windows скачайте lazy1c_windows_amd64.exe со страницы релизов"; exit 1 ;;
esac
case "$arch" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64)  arch="amd64" ;;
  *) echo "неизвестная архитектура: $arch"; exit 1 ;;
esac
name="lazy1c_${os}_${arch}"
base="https://github.com/${REPO}/releases/latest/download"

echo "→ ${name} → ${DEST}/lazy1c"
curl -fL "${base}/${name}" -o "${DEST}/lazy1c"

# sha256 из checksums-файла релиза
if curl -fsL "${base}/sha256-checksums.txt" -o "${DEST}/.lazy1c_sums"; then
  want=$(grep " ${name}\$" "${DEST}/.lazy1c_sums" | awk '{print $1}')
  if [ -n "$want" ]; then
    got=$(sha256sum "${DEST}/lazy1c" 2>/dev/null | awk '{print $1}' || shasum -a 256 "${DEST}/lazy1c" | awk '{print $1}')
    [ "$want" = "$got" ] || { echo "sha256 не совпал!"; rm -f "${DEST}/lazy1c" "${DEST}/.lazy1c_sums"; exit 1; }
    echo "✓ sha256 ok"
  fi
  rm -f "${DEST}/.lazy1c_sums"
fi

chmod +x "${DEST}/lazy1c"
echo "Готово: ${DEST}/lazy1c — положите на PATH и создайте lazy1c.toml (шаблон lazy1c.example.toml)"
