#!/bin/sh
set -eu
cd "$(dirname "$0")"

VERSION="1.3.0"
TOOLCHAIN="/opt/gcc-arm-10.2-2020.11-x86_64-arm-none-linux-gnueabihf/bin"
[ -d "$TOOLCHAIN" ] && PATH="$TOOLCHAIN:$PATH"
export PATH

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "[FAIL] Missing required build tool: $1" >&2
    exit 1
  }
}

check_arm() {
  f="$1"
  label="$2"
  [ -x "$f" ] || {
    echo "[FAIL] $label was not produced: $f" >&2
    exit 1
  }
  info=$(file -b "$f")
  case "$info" in
    *ARM*) echo "[OK] $label" ;;
    *)
      echo "[FAIL] $label is not an ARM executable" >&2
      echo "       $info" >&2
      exit 1
      ;;
  esac
}

# Only prerequisites used directly by this top-level script. Helper build
# scripts validate their own compiler/download requirements only when needed.
need go
need file

mkdir -p Scripts/.config/disctools/bin \
         Scripts/.config/disctools/temp \
         Scripts/.config/disctools/logs \
         Scripts/.config/disctools/fonts \
         third_party/build-logs

printf 'Building Disc Tools v%s\n' "$VERSION"

./fetch_chdman.sh
check_arm Scripts/.config/disctools/bin/chdman "chdman ARMv7"

./build_cdrdao.sh
check_arm Scripts/.config/disctools/bin/cdrdao "cdrdao ARMv7"
check_arm Scripts/.config/disctools/bin/toc2cue "toc2cue ARMv7"
check_arm Scripts/.config/disctools/bin/cue2toc "cue2toc ARMv7"

./build_xorriso.sh
check_arm Scripts/.config/disctools/bin/xorriso "xorriso ARMv7"

printf '[....] Disc Tools ARMv7\n'
CC="${CC:-arm-none-linux-gnueabihf-gcc}"
need "$CC"
if ! GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=1 CC="$CC" CGO_LDFLAGS="${CGO_LDFLAGS:-} -latomic" \
  go build -trimpath -ldflags="-s -w" -o Scripts/.config/disctools/disctools . \
  >third_party/build-logs/disctools.log 2>&1; then
  echo "[FAIL] Disc Tools build failed" >&2
  tail -n 40 third_party/build-logs/disctools.log >&2 || true
  exit 1
fi
check_arm Scripts/.config/disctools/disctools "Disc Tools ARMv7"

chmod +x Scripts/.config/disctools/disctools Scripts/disctools.sh Scripts/.config/disctools/bin/*

printf '\nBuild complete.\n'
printf 'Output: Scripts/disctools.sh + Scripts/.config/disctools/\n'
