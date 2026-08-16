#!/bin/sh
set -eu
cd "$(dirname "$0")"

VER="1.2.6"
URL="https://downloads.sourceforge.net/cdrdao/cdrdao-${VER}.tar.bz2"
CACHE="third_party/cdrdao-${VER}.tar.bz2"
SRC="third_party/cdrdao-${VER}"
PREFIX="$PWD/third_party/cdrdao-arm"
OUT="$PWD/Scripts/.config/disctools/bin"
LOG="$PWD/third_party/build-logs/cdrdao.log"
TOOLCHAIN="/opt/gcc-arm-10.2-2020.11-x86_64-arm-none-linux-gnueabihf/bin"

mkdir -p third_party "$OUT" "$(dirname "$LOG")"
if [ -x "$OUT/cdrdao" ] && [ -x "$OUT/toc2cue" ] && [ -x "$OUT/cue2toc" ]; then
  echo "[SKIP] cdrdao helpers already present"
  exit 0
fi

[ -d "$TOOLCHAIN" ] && PATH="$TOOLCHAIN:$PATH"
export PATH
: "${CC:=arm-none-linux-gnueabihf-gcc}"
: "${CXX:=arm-none-linux-gnueabihf-g++}"
: "${HOST_CC:=gcc}"

for tool in "$CC" "$CXX" "$HOST_CC" curl tar make; do
  command -v "$tool" >/dev/null 2>&1 || { echo "[FAIL] Required cdrdao build tool missing: $tool" >&2; exit 1; }
done

if [ ! -f "$CACHE" ]; then
  printf '[....] Downloading cdrdao %s\n' "$VER"
  curl -fsSL --retry 3 "$URL" -o "$CACHE"
fi

printf '[....] Building cdrdao %s for ARMv7\n' "$VER"
rm -rf "$SRC" "$PREFIX"
tar -xjf "$CACHE" -C third_party
: > "$LOG"

fail() {
  echo "[FAIL] cdrdao build failed" >&2
  tail -n 50 "$LOG" >&2 || true
  exit 1
}

cd "$SRC"
./configure \
  --host=arm-none-linux-gnueabihf \
  --prefix="$PREFIX" \
  --without-lame \
  --without-ogg-support \
  CC="$CC" CXX="$CXX" \
  CFLAGS="-O2" CXXFLAGS="-O2" \
  LDFLAGS="-static" >>"$LOG" 2>&1 || fail

# PCCTS antlr/dlg are build-host generators. Build them natively so the
# cross-build never tries to execute ARM binaries on the WSL host.
make -C pccts/antlr clean >>"$LOG" 2>&1 || true
make -C pccts/antlr CC="$HOST_CC" CFLAGS="-O2" LDFLAGS="" antlr >>"$LOG" 2>&1 || fail
make -C pccts/dlg clean >>"$LOG" 2>&1 || true
make -C pccts/dlg CC="$HOST_CC" CFLAGS="-O2" LDFLAGS="" dlg >>"$LOG" 2>&1 || fail
touch pccts/antlr/antlr pccts/dlg/dlg

make -j"${JOBS:-$(nproc 2>/dev/null || echo 2)}" >>"$LOG" 2>&1 || fail

for f in dao/cdrdao utils/toc2cue utils/cue2toc; do
  [ -x "$f" ] || { echo "[FAIL] Expected cdrdao output missing: $f" >&2; exit 1; }
done

cp dao/cdrdao "$OUT/cdrdao"
cp utils/toc2cue "$OUT/toc2cue"
cp utils/cue2toc "$OUT/cue2toc"
chmod +x "$OUT/cdrdao" "$OUT/toc2cue" "$OUT/cue2toc"
echo "[OK] cdrdao helpers built"
