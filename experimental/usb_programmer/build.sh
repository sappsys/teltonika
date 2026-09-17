#!/usr/bin/env bash
# Build usb_programmer for the current OS/arch (CGO disabled).
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

export CGO_ENABLED=0
export GOOS="${GOOS:-$(go env GOOS)}"
export GOARCH="${GOARCH:-$(go env GOARCH)}"

OUT="${OUT:-usb_programmer}"
echo "==> building ${OUT} (${GOOS}/${GOARCH})"
go test ./usb/
go build -o "$OUT" .
echo "Built: $DIR/$OUT"
echo "Example: ./$OUT --config example/example.txt"
