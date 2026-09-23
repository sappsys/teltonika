#!/usr/bin/env bash
# Build usb_programmer for Linux and Windows (CGO disabled).
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

export CGO_ENABLED=0

echo "==> go test ./usb/"
go test ./usb/

echo "==> usb_programmer (linux/amd64)"
GOOS=linux GOARCH=amd64 go build -o "$DIR/usb_programmer" .

echo "==> usb_programmer.exe (windows/amd64)"
GOOS=windows GOARCH=amd64 go build -o "$DIR/usb_programmer.exe" .

echo
echo "Built:"
echo "  $DIR/usb_programmer"
echo "  $DIR/usb_programmer.exe"
echo
echo "Example:"
echo "  ./usb_programmer --config example/example.txt --keyword WORD"
