#!/usr/bin/env bash
# Run on the Linux amd64 deployment host; never modifies a system Go installation.
set -euo pipefail
cluster_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
cache_dir=${MINIO_BUILD_DIR:-"$cluster_dir/runtime/minio-build"}
mkdir -p "$cache_dir" "$cluster_dir/vendor"
cache_dir=$(cd -- "$cache_dir" && pwd)
cd "$cache_dir"
source_name=minio-RELEASE.2025-10-15T17-29-55Z
source_sha=be6d0bd3696c3a13a35f02d3a0280b64319c67918b4501c5c3d87f96d000085c
go_sha=0335f314b6e7bfe08c3d0cfaa7c19db961b7b99fb20be62b0a826c992ad14e0f
[[ "$(uname -s)-$(uname -m)" == Linux-x86_64 ]] || { echo 'Build requires Linux amd64' >&2; exit 2; }
[[ -f go.tgz ]] || curl -fL --retry 2 https://dl.google.com/go/go1.25.3.linux-amd64.tar.gz -o go.tgz
printf '%s  %s\n' "$go_sha" go.tgz | sha256sum -c -
[[ -x go/bin/go ]] || tar -xzf go.tgz
[[ -f source.tgz ]] || curl -fL --retry 2 "https://codeload.github.com/minio/minio/tar.gz/refs/tags/RELEASE.2025-10-15T17-29-55Z" -o source.tgz
printf '%s  %s\n' "$source_sha" source.tgz | sha256sum -c -
[[ -d "$source_name" ]] || tar -xzf source.tgz
cd "$source_name"
GOPROXY=${GOPROXY:-https://goproxy.cn,direct} GOSUMDB=${GOSUMDB:-sum.golang.google.cn} GOTOOLCHAIN=local GOMAXPROCS=8 "$cache_dir/go/bin/go" build -p 8 -trimpath -ldflags '-s -w -X github.com/minio/minio/cmd.Version=2025-10-15T17:29:55Z -X github.com/minio/minio/cmd.ReleaseTag=RELEASE.2025-10-15T17-29-55Z' -o "$cluster_dir/vendor/minio-linux-amd64" .
sha256sum "$cluster_dir/vendor/minio-linux-amd64"
