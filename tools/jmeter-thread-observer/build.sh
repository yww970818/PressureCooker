#!/usr/bin/env bash
set -euo pipefail

observer_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
jmeter_home="${1:-${JMETER_HOME:-}}"
if [[ -z "$jmeter_home" || ! -f "$jmeter_home/lib/ext/ApacheJMeter_core.jar" ]]; then
  echo 'Usage: build.sh JMETER_HOME [OUTPUT_JAR]; official JMeter 5.6.3 is required' >&2
  exit 2
fi
output_jar="${2:-$observer_dir/.build/pressurecooker-thread-observer.jar}"
java_bin="${JAVA_HOME:+$JAVA_HOME/bin/}"
javac_bin="${java_bin}javac"
jar_bin="${java_bin}jar"
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/pressurecooker-observer.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
mkdir -p -- "$build_dir/classes" "$(dirname -- "$output_jar")"
"$javac_bin" -encoding UTF-8 -source 8 -target 8 \
  -classpath "$jmeter_home/lib/ext/ApacheJMeter_core.jar:$jmeter_home/lib/*" \
  -d "$build_dir/classes" \
  "$observer_dir/src/io/pressurecooker/jmeter/ThreadObservationListener.java"
"$jar_bin" cf "$output_jar" -C "$build_dir/classes" .
printf '%s\n' "$output_jar"
