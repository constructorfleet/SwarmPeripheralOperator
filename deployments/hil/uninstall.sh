#!/bin/sh
# Reverts deployments/hil/install.sh.
set -eu
RESTART=${SPO_HIL_RESTART_CMD:-systemctl restart docker}
DAEMON_JSON=/etc/docker/daemon.json
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }

docker service rm spo-controller >/dev/null 2>&1 || true
for s in ca.crt controller.crt controller.key; do docker secret rm "spo-$s" >/dev/null 2>&1 || true; done
if [ -f "$DAEMON_JSON.spo-backup" ]; then
	mv "$DAEMON_JSON.spo-backup" "$DAEMON_JSON"
elif grep -q '/opt/spo/bin/spo-runtime' "$DAEMON_JSON" 2>/dev/null; then
	rm -f "$DAEMON_JSON"
fi
sh -c "$RESTART"
i=0; until docker info >/dev/null 2>&1; do i=$((i+1)); [ $i -lt 60 ] || { echo "dockerd did not come back" >&2; exit 1; }; sleep 1; done
rm -rf /etc/spo /opt/spo /etc/cdi/spo-*.json /etc/cdi/nvidia.json
echo "default runtime: $(docker info -f '{{.DefaultRuntime}}')"
