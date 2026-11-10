#!/usr/bin/env bash
set -euo pipefail
cluster_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
go_bin=${GO:-go}
observer_home=${JMETER_HOME:-$cluster_dir/runtime/toolcache/apache-jmeter-5.6.3}
[[ -f "$observer_home/lib/ext/ApacheJMeter_core.jar" ]] || { echo 'Set JMETER_HOME to an official Apache JMeter 5.6.3 distribution and JAVA_HOME to a JDK' >&2; exit 2; }
bash "$cluster_dir/tools/jmeter-thread-observer/build.sh" "$observer_home" "$cluster_dir/vendor/pressurecooker-thread-observer.jar"
export CGO_ENABLED=0 GOOS=linux GOARCH=amd64
"$go_bin" -C "$cluster_dir/controlplane/manager-go" build -trimpath -ldflags='-s -w' -o "$cluster_dir/vendor/jmeter-manager-linux-amd64" .
"$go_bin" -C "$cluster_dir/controlplane/mcp-go" build -trimpath -ldflags='-s -w' -o "$cluster_dir/vendor/jmeter-mcp-linux-amd64" .
"$go_bin" -C "$cluster_dir/controlplane/execution" build -trimpath -ldflags='-s -w' -o "$cluster_dir/vendor/jmeter-executor-linux-amd64" ./cmd/jmeter-executor
"$go_bin" -C "$cluster_dir/tools/platform-ops" build -trimpath -ldflags='-s -w' -o "$cluster_dir/vendor/pressurecooker-ops-linux-amd64" .
# The deployment image has no compiler. Keep a matching utility for the build host too.
host_os=$("$go_bin" env GOHOSTOS)
host_arch=$("$go_bin" env GOHOSTARCH)
if [[ "$host_os-$host_arch" != linux-amd64 ]]; then
  GOOS="$host_os" GOARCH="$host_arch" "$go_bin" -C "$cluster_dir/tools/platform-ops" build -trimpath -ldflags='-s -w' -o "$cluster_dir/vendor/pressurecooker-ops-$host_os-$host_arch" .
fi
if command -v sha256sum >/dev/null; then
  (cd "$cluster_dir/vendor" && sha256sum jmeter-manager-linux-amd64 jmeter-mcp-linux-amd64 jmeter-executor-linux-amd64 pressurecooker-ops-linux-amd64 > master-binaries.sha256)
else
  (cd "$cluster_dir/vendor" && shasum -a 256 jmeter-manager-linux-amd64 jmeter-mcp-linux-amd64 jmeter-executor-linux-amd64 pressurecooker-ops-linux-amd64 > master-binaries.sha256)
fi
if command -v sha256sum >/dev/null; then
  (cd "$cluster_dir/vendor" && sha256sum pressurecooker-thread-observer.jar > thread-observer.sha256)
  (cd "$cluster_dir" && sha256sum tools/jmeter-thread-observer/build.sh tools/jmeter-thread-observer/src/io/pressurecooker/jmeter/ThreadObservationListener.java > vendor/thread-observer-sources.sha256)
else
  (cd "$cluster_dir/vendor" && shasum -a 256 pressurecooker-thread-observer.jar > thread-observer.sha256)
  (cd "$cluster_dir" && shasum -a 256 tools/jmeter-thread-observer/build.sh tools/jmeter-thread-observer/src/io/pressurecooker/jmeter/ThreadObservationListener.java > vendor/thread-observer-sources.sha256)
fi
bash "$cluster_dir/bin/check-thread-observer.sh"
printf '%s\n' 'Built Go master, MCP adapter, executor, host utilities and official-JMeter thread observer'
