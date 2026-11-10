#!/usr/bin/env bash
set -euo pipefail

project_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
go_bin=${GO:-go}
integration=0
case ${1:-} in
  '') ;;
  --integration) integration=1 ;;
  *) printf '%s\n' 'Usage: bin/test-controlplane.sh [--integration]' >&2; exit 2 ;;
esac
[[ $# -le 1 ]] || { printf '%s\n' 'Only --integration is supported' >&2; exit 2; }

command -v node >/dev/null || { printf '%s\n' 'Node runtime is required for MCP contract tests' >&2; exit 2; }
[[ -f "$project_dir/controlplane/mcp/node_modules/jmeter-mcp-server/dist/index.js" ]] || {
  printf '%s\n' 'Install the official MCP runtime before running the baseline (bin/prepare-mcp-cache.sh)' >&2; exit 2;
}

# Default verification is independent of provisioned databases and credentials.
export JMETER_GO_INTEGRATION=$integration JMETER_EXECUTION_IT=$integration
if [[ $integration == 1 ]]; then
  case ${JMETER_MYSQL_HOST:-} in
    localhost|127.0.0.1) ;;
    *) printf '%s\n' '--integration requires a local disposable MySQL instance' >&2; exit 2 ;;
  esac
  [[ ${JMETER_MYSQL_DATABASE:-} == pressurecooker_test* ]] || {
    printf '%s\n' '--integration requires a pressurecooker_test database' >&2; exit 2;
  }
  case ${JMETER_REDIS_ADDR:-} in
    127.0.0.1:26379|localhost:26379) ;;
    *) printf '%s\n' '--integration requires local test Redis on port 26379' >&2; exit 2 ;;
  esac
fi

# File-serving tests intentionally reject symlink ancestors, including macOS /var.
export TMPDIR
TMPDIR=$(cd -- "${TMPDIR:-/tmp}" && pwd -P)
for module in manager-go execution mcp-go; do
  (cd -- "$project_dir/controlplane/$module" && "$go_bin" test -count=1 -timeout=10m ./...)
done
(cd -- "$project_dir/tools/platform-ops" && "$go_bin" test -count=1 ./...)
(cd -- "$project_dir/controlplane/frontend" && npm test && npm run build)
