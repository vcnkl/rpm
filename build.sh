#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
OUT="${RPM_BUILD_OUTPUT:-$ROOT/.build/rpm}"

usage() {
	echo "usage: ./build.sh [--tui-only]" >&2
	exit 2
}

mode="build"
case "${1:-}" in
	"")
		;;
	--tui-only)
		mode="tui-only"
		;;
	*)
		usage
		;;
esac

if command -v corepack >/dev/null 2>&1; then
	corepack enable
fi

cd "$ROOT/ui/env-tui"
yarn install --immutable
yarn build

if [ "$mode" = "tui-only" ]; then
	exit 0
fi

mkdir -p "$(dirname "$OUT")"
cd "$ROOT"
go build -tags tui_bundle -o "$OUT" ./main.go
echo "Built $OUT"
