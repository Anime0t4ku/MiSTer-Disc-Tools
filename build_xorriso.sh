#!/bin/sh
set -eu
cd "$(dirname "$0")"

VER="1.5.6.pl02"
URL="https://ftp.gnu.org/gnu/xorriso/xorriso-${VER}.tar.gz"
CACHE="third_party/xorriso-${VER}.tar.gz"
EXTRACT="third_party/xorriso-src-${VER}"
OUT="$PWD/Scripts/.config/disctools/bin/xorriso"
LOG="$PWD/third_party/build-logs/xorriso.log"
TOOLCHAIN="/opt/gcc-arm-10.2-2020.11-x86_64-arm-none-linux-gnueabihf/bin"

mkdir -p third_party "$(dirname "$OUT")" "$(dirname "$LOG")"
if [ -x "$OUT" ]; then
  echo "[SKIP] xorriso already present"
  exit 0
fi

[ -d "$TOOLCHAIN" ] && PATH="$TOOLCHAIN:$PATH"
export PATH
: "${CC:=arm-none-linux-gnueabihf-gcc}"
for tool in "$CC" curl tar make; do
  command -v "$tool" >/dev/null 2>&1 || { echo "[FAIL] Required xorriso build tool missing: $tool" >&2; exit 1; }
done

if [ ! -f "$CACHE" ]; then
  printf '[....] Downloading xorriso %s\n' "$VER"
  curl -fsSL --retry 3 "$URL" -o "$CACHE"
fi

printf '[....] Building xorriso %s for ARMv7\n' "$VER"
rm -rf "$EXTRACT"
mkdir -p "$EXTRACT"
tar -xzf "$CACHE" -C "$EXTRACT"

SRC=""
if [ -f "$EXTRACT/configure" ]; then
  SRC="$EXTRACT"
else
  for d in "$EXTRACT"/*; do
    if [ -d "$d" ] && [ -f "$d/configure" ]; then
      SRC="$d"
      break
    fi
  done
fi
[ -n "$SRC" ] || { echo "[FAIL] Could not locate extracted xorriso source" >&2; exit 1; }

: > "$LOG"
fail() {
  echo "[FAIL] xorriso build failed" >&2
  tail -n 50 "$LOG" >&2 || true
  exit 1
}

cd "$SRC"
./configure \
  --host=arm-none-linux-gnueabihf \
  --disable-shared \
  --enable-static \
  CC="$CC" CFLAGS="-O2" LDFLAGS="-static" >>"$LOG" 2>&1 || fail
make -j"${JOBS:-$(nproc 2>/dev/null || echo 2)}" >>"$LOG" 2>&1 || fail

BIN=""
for candidate in xorriso/xorriso ./xorriso; do
  if [ -x "$candidate" ] && [ -f "$candidate" ]; then
    BIN="$candidate"
    break
  fi
done
[ -n "$BIN" ] || { echo "[FAIL] Expected xorriso binary missing" >&2; exit 1; }

cp "$BIN" "$OUT"
chmod +x "$OUT"
echo "[OK] xorriso built"
