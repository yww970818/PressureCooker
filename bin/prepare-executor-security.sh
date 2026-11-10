#!/usr/bin/env bash
# Run on the Docker host while pressure tasks are idle, before recreating nodes.
set -euo pipefail
cluster_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$cluster_dir"
for secret in agent-token redis-password minio-access-key minio-secret-key; do
  secret_file="runtime/secrets/platform/$secret"
  [[ -f "$secret_file" && ! -L "$secret_file" ]] || { echo "Missing regular secret: $secret_file" >&2; exit 1; }
  [[ $(stat -c '%u' "$secret_file") != 10001 && $(stat -c '%a' "$secret_file") == 600 ]] || { echo "Secret must be mode 600 and not owned by JVM UID 10001: $secret_file" >&2; exit 1; }
done
for role in controller-a controller-b worker-1 worker-2 worker-3 worker-4 worker-5 worker-6; do
  directory="runtime/work/$role"
  [[ -d "$directory" && ! -L "$directory" ]] || { echo "Missing regular work directory: $directory" >&2; exit 1; }
  [[ $(stat -c '%u' "$directory") != 10001 ]] || { echo "Work directory must not be owned by JVM UID 10001: $directory" >&2; exit 1; }
  chmod 755 "$directory"
done
mkdir -p runtime/secrets/rmi-jvm
image=${JMETER_SECURITY_PREPARE_IMAGE:-}
if [[ -z "$image" ]]; then
  image=$(docker compose --env-file cluster.env -f compose.yaml config --format json | bash bin/platform-ops.sh json-field services controller-a image)
fi
docker run --rm --network none --read-only --user 0:0 --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE \
  --security-opt no-new-privileges:true --entrypoint /bin/sh \
  -v "$cluster_dir/runtime/secrets/rmi:/source:ro" \
  -v "$cluster_dir/runtime/secrets/rmi-jvm:/destination" "$image" -ec '
    test -f /source/rmi_keystore.jks && test -f /source/rmi.properties
    chown 0:10001 /destination
    chmod 750 /destination
    cp /source/rmi_keystore.jks /source/rmi.properties /destination/
    chown 0:10001 /destination/rmi_keystore.jks /destination/rmi.properties
    chmod 440 /destination/rmi_keystore.jks /destination/rmi.properties
  '
printf '%s\n' 'Executor work paths and isolated RMI copies prepared; platform credentials remain private.'
