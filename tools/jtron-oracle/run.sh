#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
go_tron_root="$(cd "$script_dir/../.." && pwd)"
java_tron_root="${JAVA_TRON_ROOT:-$(cd "$go_tron_root/../java-tron" && pwd)}"
java_home="${JAVA_HOME:-$(/usr/libexec/java_home -v 17)}"
cache_dir="${JTRON_ORACLE_CACHE:-$go_tron_root/.cache/jtron-oracle}"
classpath_file="$cache_dir/classpath"

mkdir -p "$cache_dir"

JAVA_HOME="$java_home" "$java_tron_root/gradlew" \
  --no-daemon \
  -p "$java_tron_root" \
  -I "$script_dir/gradle/init.gradle" \
  -PoracleClasspathFile="$classpath_file" \
  :framework:writeJtronOracleClasspath </dev/null >&2

exec "$java_home/bin/java" \
  -DjavaTronRoot="$java_tron_root" \
  -cp "$(cat "$classpath_file")" \
  org.tron.tools.vmoracle.JtronOracleMain
