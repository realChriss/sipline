#!/bin/sh
set -e
cd "$(dirname "$0")"
export CGO_ENABLED=0
for os in windows linux darwin; do
  for arch in amd64 arm64; do
    out=dist/sipline-$os-$arch
    [ "$os" = windows ] && out=$out.exe
    GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$out" .
    echo "$out"
  done
done
