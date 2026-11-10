#!/usr/bin/env bash
# Prefer a host binary; Go is only needed when running directly from source.
set -euo pipefail
cluster_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
case $(uname -s) in
  Linux) platform_os=linux ;;
  Darwin) platform_os=darwin ;;
  *) echo 'Unsupported host OS for platform-ops; use a Linux or macOS host.' >&2; exit 2 ;;
esac
case $(uname -m) in
  x86_64|amd64) platform_arch=amd64 ;;
  aarch64|arm64) platform_arch=arm64 ;;
  *) echo 'Unsupported host architecture for platform-ops.' >&2; exit 2 ;;
esac
ops_binary="$cluster_dir/vendor/pressurecooker-ops-$platform_os-$platform_arch"
if [[ -x "$ops_binary" ]]; then
  exec "$ops_binary" --root "$cluster_dir" "$@"
fi
go_bin=${GO:-go}
if command -v "$go_bin" >/dev/null 2>&1; then
  exec "$go_bin" -C "$cluster_dir/tools/platform-ops" run . --root "$cluster_dir" "$@"
fi
printf 'Missing host utility: %s\nBuild on a development host with GO=/path/to/go bin/build-master.sh and copy the matching binary to vendor/, or set GO to an available Go compiler.\n' "$ops_binary" >&2
exit 2
