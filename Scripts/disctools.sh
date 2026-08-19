#!/bin/bash
VERSION="1.2.0"
BASE="/media/fat/Scripts/.config/disctools"
BIN="$BASE/disctools"

if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "Disc Tools v$VERSION"
  exit 0
fi

mkdir -p "$BASE/bin" "$BASE/temp" "$BASE/logs" "$BASE/fonts"
chmod +x "$BIN" "$BASE"/bin/* 2>/dev/null || true

if [ ! -x "$BIN" ]; then
  echo "Disc Tools binary is missing: $BIN"
  exit 1
fi

printf '\033[?25l'
trap 'printf "\033[?25h"' EXIT
exec "$BIN" "$@"
