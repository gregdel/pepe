#!/bin/sh
set -eu

app=polochon-tui
out_dir=dist

mkdir -p "$out_dir"

build() {
	goos=$1
	goarch=$2
	output="$out_dir/$app-$goos-$goarch"

	printf "==> building %s\n" "$output"

	env \
		CGO_ENABLED=0 \
		GOOS="$goos" \
		GOARCH="$goarch" \
		go build -trimpath -ldflags="-s -w" -o "$output" .
}

build linux amd64
build linux arm64
