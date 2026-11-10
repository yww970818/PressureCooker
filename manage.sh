#!/usr/bin/env bash
set -euo pipefail
cluster_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "$cluster_dir"
compose() { docker compose --env-file cluster.env -f compose.yaml "$@"; }
prepare() {
  if [[ ! -f cluster.env ]]; then
    umask 077
    printf 'COMPOSE_PROJECT_NAME=pressurecooker\nJMETER_UID=%s\nJMETER_GID=%s\nJMETER_IMAGE=pressurecooker-platform:local\nJMETER_MANAGER_IMAGE=pressurecooker-manager:local\n' "$(id -u)" "$(id -g)" > cluster.env
  fi
  mkdir -p runtime/{state,results,secrets/{platform,rmi},minio,backups,restore}
  for node in controller-a controller-b worker-{1..6} smoke-target; do
    mkdir -p "runtime/data/$node" "runtime/work/$node"
  done
  chmod 700 runtime/secrets runtime/secrets/{platform,rmi}
  bash bin/platform-ops.sh prepare-secrets
}
api() { compose exec -T manager /app/bin/jmeter-manager api "$@"; }
prepare
case "${1:-help}" in
  config) compose config --quiet ;;
  build)
    bash bin/check-thread-observer.sh
    bash bin/fetch-node.sh
    [[ -x vendor/jmeter-manager-linux-amd64 && -x vendor/jmeter-mcp-linux-amd64 && -x vendor/jmeter-executor-linux-amd64 && -x vendor/pressurecooker-ops-linux-amd64 ]] || { echo 'Build Go master first: GO=/path/to/go bin/build-master.sh' >&2; exit 2; }
    (cd vendor && sha256sum -c master-binaries.sha256)
    [[ -f vendor/jmeter-mcp-npm-cache.tgz ]] || { echo 'Prepare locked npm cache first: bin/prepare-mcp-cache.sh' >&2; exit 2; }
    [[ -f controlplane/frontend/dist/index.html ]] || { echo 'Build frontend first: cd controlplane/frontend && npm ci && npm run build' >&2; exit 2; }
    manager_base_image=$(compose config --format json | bash bin/platform-ops.sh json-field services manager build args MANAGER_BASE_IMAGE)
    jdk_base_image=$(compose config --format json | bash bin/platform-ops.sh json-field services controller-a build args JDK_BASE_IMAGE)
    docker build -f Dockerfile.controlbase --build-arg "JDK_BASE_IMAGE=$jdk_base_image" -t "$manager_base_image" .
    compose build controller-a manager minio
    ;;
  init) compose run --rm --no-deps init; bash bin/prepare-executor-security.sh ;;
  up)
    compose run --rm --no-deps init
    bash bin/prepare-executor-security.sh
    compose up -d --wait --wait-timeout 90 minio
    compose run --rm --no-deps -T --entrypoint /app/bin/jmeter-manager manager init-minio
    compose --profile selftest stop smoke-target
    compose up -d --remove-orphans --wait --wait-timeout 180
    ;;
  api) shift; api "$@" ;;
  ps) compose ps -a ;;
  logs) shift; compose logs --tail 100 "$@" ;;
  smoke)
    [[ $# -eq 4 && -n "$2" && -n "$3" && -n "$4" ]] || { echo '用法：manage.sh smoke PROJECT_ID CLUSTER_ID ENVIRONMENT_ID' >&2; exit 2; }
    smoke_project=$2
    smoke_cluster=$3
    smoke_environment=$4
    compose --profile selftest up -d --wait --wait-timeout 90 smoke-target
    trap 'compose --profile selftest stop smoke-target >/dev/null' EXIT
    bash bin/platform-smoke.sh --url "${JMETER_API_URL:-http://127.0.0.1:18090}" --token-file "$cluster_dir/runtime/secrets/platform/admin-token" --plan "$cluster_dir/plans/platform-smoke.jmx" --project "$smoke_project" --cluster "$smoke_cluster" --environment "$smoke_environment"
    ;;
  run)
    shift; [[ $# -ge 1 ]] || { echo '用法：manage.sh run plan.jmx --project PROJECT_ID --cluster CLUSTER_ID --environment ENVIRONMENT_ID [-Gkey=value ...]' >&2; exit 2; }
    plan=$1; shift
    run_project=''
    run_cluster=''
    run_environment=''
    args=()
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --project)
          [[ $# -ge 2 && -n "$2" && -z "$run_project" ]] || { echo '请提供一个 --project PROJECT_ID' >&2; exit 2; }
          run_project=$2; shift 2 ;;
        --cluster)
          [[ $# -ge 2 && -n "$2" && -z "$run_cluster" ]] || { echo '请提供一个 --cluster CLUSTER_ID' >&2; exit 2; }
          run_cluster=$2; shift 2 ;;
        --environment)
          [[ $# -ge 2 && -n "$2" && -z "$run_environment" ]] || { echo '请提供一个 --environment ENVIRONMENT_ID' >&2; exit 2; }
          run_environment=$2; shift 2 ;;
        -G*=*) args+=(--property "${1#-G}"); shift ;;
        *) echo '请使用 --project PROJECT_ID、--cluster CLUSTER_ID、--environment ENVIRONMENT_ID 和 -Gkey=value；其他控制参数请使用 manage.sh api。' >&2; exit 2 ;;
      esac
    done
    [[ -n "$run_project" ]] || { echo '请提供 --project PROJECT_ID 选择项目。' >&2; exit 2; }
    [[ -n "$run_cluster" ]] || { echo '请先通过 manage.sh api cluster 查询集群，并提供 --cluster CLUSTER_ID。' >&2; exit 2; }
    [[ -n "$run_environment" ]] || { echo '请提供 --environment ENVIRONMENT_ID 选择项目环境。' >&2; exit 2; }
    plan_json=$(api upload "plans/$plan" --project "$run_project" --name "${plan%.jmx}")
    plan_id=$(bash bin/platform-ops.sh json-field id <<<"$plan_json")
    run_key=cli-$(date -u +%Y%m%dT%H%M%SZ)-$$
    api start "$plan_id" --project "$run_project" --cluster "$run_cluster" --environment "$run_environment" --name "$run_key" --key "$run_key" "${args[@]}"
    ;;
  backup|import-history|restore)
    action=$1; shift
    # Local administrative commands execute inside configured manager volume mappings.
    compose exec -T manager /app/bin/jmeter-manager "$action" "$@"
    ;;
  down) compose down ;;
  help|*) printf '%s\n' 'Commands: config | build | init | up | ps | logs [service] | api <command> | smoke PROJECT_ID CLUSTER_ID ENVIRONMENT_ID | run plan.jmx --project PROJECT_ID --cluster CLUSTER_ID --environment ENVIRONMENT_ID [-Gkey=value] | backup /backup/new | import-history /results --project PROJECT_ID | restore /backup/source /restore/empty | down' ;;
esac
