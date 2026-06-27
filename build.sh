#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
OUT="${RPM_BUILD_OUTPUT:-$ROOT/.build/rpm}"

mkdir -p "$(dirname "$OUT")"
cd "$ROOT"
go build -o "$OUT" ./main.go
echo "Built $OUT"
