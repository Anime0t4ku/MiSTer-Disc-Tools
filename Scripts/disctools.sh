#!/bin/bash
VERSION="1.5.0"
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

SAM_SCRIPT="/media/fat/Scripts/MiSTer_SAM_on.sh"
SAM_WAS_ENABLED=0
SAM_PREPARED=0

sam_autoplay_running() {
  ps 2>/dev/null | grep -q '[M]iSTer_SAM_MCP.py'
}

prepare_sam() {
  # Disc Tools runs as a native Linux app over the MiSTer menu core. Keep
  # SAM autoplay from launching a core while a disc operation is in progress.
  if [ -f "$SAM_SCRIPT" ] && sam_autoplay_running; then
    SAM_WAS_ENABLED=1
    "$SAM_SCRIPT" disable >/dev/null 2>&1 || true
  fi
  SAM_PREPARED=1
}

restore_sam() {
  [ "$SAM_PREPARED" = "1" ] || return
  SAM_PREPARED=0

  if [ "$SAM_WAS_ENABLED" = "1" ] && [ -f "$SAM_SCRIPT" ]; then
    "$SAM_SCRIPT" enable >/dev/null 2>&1 || true
  fi
}

cleanup() {
  restore_sam
  printf '\033[?25h'
}

printf '\033[?25l'
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

prepare_sam

"$BIN" "$@"
exit $?
