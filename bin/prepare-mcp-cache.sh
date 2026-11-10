#!/usr/bin/env bash
set -euo pipefail
cluster_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cache_dir=$(mktemp -d)
trap 'rm -rf -- "$cache_dir"' EXIT
cd "$cluster_dir/controlplane/mcp"
npm ci --omit=dev --ignore-scripts --no-audit --no-fund --cache "$cache_dir"
COPYFILE_DISABLE=1 tar -czf "$cluster_dir/vendor/jmeter-mcp-npm-cache.tgz" -C "$cache_dir" _cacache
printf '%s\n' 'Prepared integrity-checked locked npm dependencies for offline Docker build'
