#!/usr/bin/env bash
set -euo pipefail
rmi_dir=/run/jmeter-rmi
case "${1:-controller}" in
  init-keystore)
    umask 077
    if [[ -e "$rmi_dir/rmi_keystore.jks" || -e "$rmi_dir/rmi.properties" ]]; then
      [[ -f "$rmi_dir/rmi_keystore.jks" && -f "$rmi_dir/rmi.properties" ]] || {
        echo 'Incomplete RMI credentials: inspect the existing files before retrying.' >&2; exit 2;
      }
      export JMETER_RMI_PASSWORD
      JMETER_RMI_PASSWORD=$(sed -n 's/^server.rmi.ssl.keystore.password=//p' "$rmi_dir/rmi.properties")
      [[ "$JMETER_RMI_PASSWORD" =~ ^[0-9a-f]{48}$ ]] || exit 2
      keytool -list -alias rmi -keystore "$rmi_dir/rmi_keystore.jks" -storepass:env JMETER_RMI_PASSWORD >/dev/null
      echo 'Existing RMI keystore validated.'
      exit 0
    fi
    temporary=$(mktemp -d "$rmi_dir/.init.XXXXXX")
    trap 'rm -rf -- "$temporary"' EXIT
    export JMETER_RMI_PASSWORD
    JMETER_RMI_PASSWORD=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
    keytool -genkeypair -alias rmi -keyalg RSA -keysize 2048 -validity 365 \
      -dname 'CN=jmeter-rmi,OU=Pressure,O=PressureCooker,C=CN' \
      -ext 'SAN=dns:controller-a,dns:controller-b,dns:worker-1,dns:worker-2,dns:worker-3,dns:worker-4,dns:worker-5,dns:worker-6,dns:localhost,ip:127.0.0.1' \
      -storetype JKS -keystore "$temporary/rmi_keystore.jks" \
      -storepass:env JMETER_RMI_PASSWORD -keypass:env JMETER_RMI_PASSWORD >/dev/null
    cat > "$temporary/rmi.properties" <<EOF
server.rmi.ssl.disable=false
server.rmi.ssl.keystore.type=JKS
server.rmi.ssl.keystore.file=/run/jmeter-rmi/rmi_keystore.jks
server.rmi.ssl.keystore.alias=rmi
server.rmi.ssl.keystore.password=$JMETER_RMI_PASSWORD
server.rmi.ssl.truststore.type=JKS
server.rmi.ssl.truststore.file=/run/jmeter-rmi/rmi_keystore.jks
server.rmi.ssl.truststore.password=$JMETER_RMI_PASSWORD
EOF
    chmod 600 "$temporary/rmi_keystore.jks" "$temporary/rmi.properties"
    mv "$temporary/rmi_keystore.jks" "$rmi_dir/"
    mv "$temporary/rmi.properties" "$rmi_dir/"
    echo 'RMI SSL keystore created.'
    ;;
  agent)
    exec /opt/cluster/bin/jmeter-executor
    ;;
  controller)
    export JMETER_AGENT_ROLE=controller
    exec /opt/cluster/bin/jmeter-executor
    ;;
  check-controller)
    [[ -x "$JMETER_HOME/bin/jmeter" && -s "$rmi_dir/rmi_keystore.jks" && -s "$rmi_dir/rmi.properties" ]]
    ;;
  worker)
    export JMETER_AGENT_ROLE=worker
    exec /opt/cluster/bin/jmeter-executor
    ;;
  fixture)
    export JMETER_AGENT_ROLE=fixture
    export JMETER_AGENT_PORT=${JMETER_AGENT_PORT:-8080}
    exec /opt/cluster/bin/jmeter-executor
    ;;
  *) exec "$@" ;;
esac
