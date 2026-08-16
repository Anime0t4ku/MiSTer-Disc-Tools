#!/bin/sh
set -eu
cd "$(dirname "$0")"

OUT="Scripts/.config/disctools/bin/chdman"
URL="https://raw.githubusercontent.com/emmercm/chdman-js/main/packages/chdman-linux-arm/chdman-armv7"
TMP="${OUT}.download"
mkdir -p "$(dirname "$OUT")"

if [ -x "$OUT" ]; then
  echo "[SKIP] chdman already present"
  exit 0
fi

command -v curl >/dev/null 2>&1 || { echo "[FAIL] curl is required to fetch chdman" >&2; exit 1; }

printf '[....] Fetching chdman ARMv7\n'
rm -f "$TMP"
if ! curl -fsSL --retry 3 "$URL" -o "$TMP"; then
  rm -f "$TMP"
  echo "[FAIL] Could not download chdman ARMv7" >&2
  exit 1
fi
mv "$TMP" "$OUT"
chmod +x "$OUT"
echo "[OK] chdman fetched"
