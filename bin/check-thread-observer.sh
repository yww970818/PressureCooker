#!/usr/bin/env bash
set -euo pipefail
cluster_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
digest() {
  if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}
fail() { echo 'Build the current thread observer first: JAVA_HOME=/path/to/jdk JMETER_HOME=/path/to/apache-jmeter-5.6.3 GO=/path/to/go bin/build-master.sh' >&2; exit 2; }
for artifact in pressurecooker-thread-observer.jar thread-observer.sha256 thread-observer-sources.sha256; do
  [[ -f "$cluster_dir/vendor/$artifact" ]] || fail
done
cd "$cluster_dir"
digest tools/jmeter-thread-observer/build.sh tools/jmeter-thread-observer/src/io/pressurecooker/jmeter/ThreadObservationListener.java | cmp -s - vendor/thread-observer-sources.sha256 || fail
(cd vendor && digest -c thread-observer.sha256) >/dev/null || fail
