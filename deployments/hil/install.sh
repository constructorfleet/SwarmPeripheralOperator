#!/bin/sh
# Installs SPO on one Swarm manager for hardware-in-the-loop testing. Not for production.
#
# Env:
#   SPO_HIL_DEVICES  space-separated KIND:id=/dev/path for non-GPU devices, e.g. "SPO_USB:serial0=/dev/ttyUSB0"
#   SPO_HIL_GPU      auto (default) | 0. auto adds NVIDIA GPUs via nvidia-smi and nvidia-ctk.
#   SPO_HIL_OVERWRITE_DAEMON_JSON=1  replace an existing /etc/docker/daemon.json (backup kept)
#   SPO_HIL_RESTART_CMD  how to restart dockerd (default: systemctl restart docker)
#   SPO_BIN_DIR      directory holding spo-runtime and spo-controller (default: ./bin)
set -eu

BIN_DIR=${SPO_BIN_DIR:-./bin}
ETC=/etc/spo
CDI=/etc/cdi
RESTART=${SPO_HIL_RESTART_CMD:-systemctl restart docker}
DAEMON_JSON=/etc/docker/daemon.json

die() { echo "spo-hil-install: $*" >&2; exit 1; }
say() { echo "==> $*"; }

[ "$(id -u)" = 0 ] || die "run as root"
command -v docker >/dev/null || die "docker not found"
command -v openssl >/dev/null || die "openssl not found"
for b in spo-runtime spo-controller; do [ -x "$BIN_DIR/$b" ] || die "$BIN_DIR/$b missing; run 'make build' first"; done
[ "$(docker info -f '{{.Swarm.LocalNodeState}}')" = active ] || die "node is not in a Swarm; run 'docker swarm init'"
[ "$(docker info -f '{{.Swarm.ControlAvailable}}')" = true ] || die "run on a Swarm manager"
RUNC=$(command -v runc || true)
[ -n "$RUNC" ] || for p in /usr/bin/runc /usr/sbin/runc /usr/local/bin/runc; do [ -x "$p" ] && RUNC=$p && break; done
[ -n "$RUNC" ] || die "runc not found"

# kind|cdiKind|id|path, one per line
entries=""
add() { entries="$entries$1|$2|$3|$4
"; }

for spec in ${SPO_HIL_DEVICES:-}; do
	kind=${spec%%:*}; rest=${spec#*:}; id=${rest%%=*}; path=${rest#*=}
	[ -c "$path" ] || die "$path is not a character device"
	[ -L "$path" ] && die "$path is a symlink; use the real device node"
	class=$(echo "${kind#SPO_}" | tr 'A-Z_' 'a-z-')
	add "$kind" "spo.local/$class" "$id" "$path"
done

GPU=0
if [ "${SPO_HIL_GPU:-auto}" != 0 ] && command -v nvidia-smi >/dev/null; then
	command -v nvidia-ctk >/dev/null || die "nvidia-smi found but nvidia-ctk missing; install NVIDIA Container Toolkit"
	GPU=1
	nvidia-smi --query-gpu=index,uuid --format=csv,noheader | while IFS=', ' read -r idx uuid; do
		echo "SPO_GPU|nvidia.com/gpu|$uuid|/dev/nvidia$idx"
	done > /tmp/spo-hil-gpus
	entries="$entries$(cat /tmp/spo-hil-gpus)
"
fi
[ -n "$(echo "$entries" | tr -d '\n')" ] || die "no devices: set SPO_HIL_DEVICES or install an NVIDIA GPU"

say "installing binaries to /opt/spo/bin"
install -d /opt/spo/bin
install -m 0755 "$BIN_DIR/spo-runtime" "$BIN_DIR/spo-controller" /opt/spo/bin/

say "writing CDI specs to $CDI"
install -d -m 0755 "$CDI"
hooks=""; prefixes=""
if [ "$GPU" = 1 ]; then
	nvidia-ctk cdi generate --device-name-strategy=uuid --format=json --output="$CDI/nvidia.json" >/dev/null
	chmod 0644 "$CDI/nvidia.json"
	# Allowlist exactly what the generated spec uses. Review it: SPO_HIL_INSTALL prints both lists.
	hooks=$(grep -A1 '"hookName"' "$CDI/nvidia.json" | sed -n 's/.*"path": *"\([^"]*\)".*/\1/p' | sort -u)
	# Both the path and its symlink target must sit under an allowed prefix.
	prefixes=$(sed -n 's/.*"hostPath": *"\([^"]*\)".*/\1/p' "$CDI/nvidia.json" | grep -v '^/dev/' | sort -u |
		while read -r hp; do dirname "$hp"; dirname "$(readlink -f "$hp")"; done | sort -u)
fi
echo "$entries" | grep . | grep -v '^SPO_GPU|' | cut -d'|' -f1,2 | sort -u | while IFS='|' read -r kind cdikind; do
	devs=$(echo "$entries" | grep "^$kind|" | while IFS='|' read -r _ _ id path; do
		printf '{"name":"%s","containerEdits":{"deviceNodes":[{"path":"%s"}]}},' "$id" "$path"; done)
	printf '{"cdiVersion":"0.6.0","kind":"%s","devices":[%s]}\n' "$cdikind" "${devs%,}" > "$CDI/spo-$(echo "$kind" | tr 'A-Z_' 'a-z-').json"
done
chmod 0644 "$CDI"/*.json

say "configuring dockerd ($DAEMON_JSON)"
resources=$(echo "$entries" | grep . | while IFS='|' read -r kind _ id _; do printf '"%s=%s",' "$kind" "$id"; done)
new_daemon=$(printf '{\n  "runtimes": {"spo": {"path": "/opt/spo/bin/spo-runtime"}},\n  "default-runtime": "spo",\n  "node-generic-resources": [%s]\n}\n' "${resources%,}")
if [ -s "$DAEMON_JSON" ] && ! grep -q '/opt/spo/bin/spo-runtime' "$DAEMON_JSON"; then
	if [ "${SPO_HIL_OVERWRITE_DAEMON_JSON:-0}" != 1 ]; then
		echo "$DAEMON_JSON already exists. Merge this into it, or rerun with SPO_HIL_OVERWRITE_DAEMON_JSON=1 (a backup is kept):" >&2
		echo "$new_daemon" >&2
		exit 1
	fi
	cp -p "$DAEMON_JSON" "$DAEMON_JSON.spo-backup"
	say "backed up existing config to $DAEMON_JSON.spo-backup"
fi
install -d /etc/docker
echo "$new_daemon" > "$DAEMON_JSON"
say "restarting dockerd: $RESTART"
sh -c "$RESTART"
i=0; until docker info >/dev/null 2>&1; do i=$((i+1)); [ $i -lt 60 ] || die "dockerd did not come back"; sleep 1; done
[ "$(docker info -f '{{.DefaultRuntime}}')" = spo ] || die "dockerd did not adopt default-runtime spo"

NODE=$(docker info -f '{{.Swarm.NodeID}}')
IP=$(docker info -f '{{.Swarm.NodeAddr}}')
say "issuing certificates (node $NODE, controller $IP)"
install -d -m 0755 "$ETC"
cd "$ETC"
openssl ecparam -name prime256v1 -genkey -noout -out ca.key 2>/dev/null
openssl req -x509 -new -key ca.key -subj /CN=spo-hil-ca -days 7 -out ca.crt \
	-addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign 2>/dev/null
issue() {
	openssl ecparam -name prime256v1 -genkey -noout -out "$1.key" 2>/dev/null
	openssl req -new -key "$1.key" -subj "/CN=$2" -out "$1.csr" 2>/dev/null
	printf '%b' "$3" > "$1.ext"
	openssl x509 -req -in "$1.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -days 7 -out "$1.crt" -extfile "$1.ext" 2>/dev/null
	rm -f "$1.csr" "$1.ext"
}
issue controller spo-controller "subjectAltName=DNS:spo-controller,IP:$IP\nextendedKeyUsage=serverAuth\nkeyUsage=digitalSignature\n"
issue node "$NODE" "extendedKeyUsage=clientAuth\nkeyUsage=digitalSignature\n"
chmod 0600 ./*.key
cd - >/dev/null

json_list() { echo "$1" | grep . | while read -r v; do printf '"%s",' "$v"; done; }
kinds=$(echo "$entries" | grep . | cut -d'|' -f1,2 | sort -u | while IFS='|' read -r kind cdikind; do printf '"%s":"%s",' "$kind" "$cdikind"; done)
hl=$(json_list "$hooks"); pl=$(json_list "$prefixes")
cat > "$ETC/runtime.json" <<JSON
{
  "runc": "$RUNC",
  "kinds": {${kinds%,}},
  "controller": {"url": "https://$IP:7443", "serverName": "spo-controller",
    "caFile": "$ETC/ca.crt", "certFile": "$ETC/node.crt", "keyFile": "$ETC/node.key"},
  "allowedHooks": [${hl%,}],
  "allowedMountPrefixes": [${pl%,}],
  "auditLog": "/var/log/spo/runtime.jsonl"
}
JSON
chown -R root:root "$ETC" "$CDI"
chmod -R go-w "$ETC" "$CDI"

say "deploying spo-controller"
docker service rm spo-controller >/dev/null 2>&1 || true
for s in ca.crt controller.crt controller.key; do
	docker secret rm "spo-$s" >/dev/null 2>&1 || true
	docker secret create "spo-$s" "$ETC/$s" >/dev/null
done
ckinds=$(echo "$entries" | grep . | cut -d'|' -f1 | sort -u | tr '\n' ',' | sed 's/,$//')
docker service create -d -q --name spo-controller --constraint "node.id==$NODE" \
	--mount type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock \
	--mount type=bind,src=/opt/spo/bin,dst=/opt/spo/bin,readonly \
	--secret source=spo-ca.crt,target=spo-ca.crt --secret source=spo-controller.crt,target=spo-controller.crt \
	--secret source=spo-controller.key,target=spo-controller.key \
	--publish published=7443,target=7443,mode=host \
	busybox:latest /opt/spo/bin/spo-controller --kinds "$ckinds" >/dev/null
i=0; until docker service ps -f desired-state=running --format '{{.CurrentState}}' spo-controller | grep -q Running; do
	i=$((i+1)); [ $i -lt 90 ] || die "spo-controller did not start: $(docker service ps --no-trunc spo-controller)"; sleep 1
done

say "writing $ETC/hil.json for 'make hil'"
devjson=$(echo "$entries" | grep . | cut -d'|' -f1 | sort -u | while read -r kind; do
	ids=$(echo "$entries" | grep "^$kind|" | while IFS='|' read -r _ _ id path; do printf '"%s":"%s",' "$id" "$path"; done)
	gpu=false; [ "$kind" = SPO_GPU ] && gpu=true
	printf '{"kind":"%s","gpu":%s,"ids":{%s}},' "$kind" "$gpu" "${ids%,}"
done)
printf '{\n  "devices": [%s]\n}\n' "${devjson%,}" > "$ETC/hil.json"

say "done"
echo "devices:"; echo "$entries" | grep . | sed 's/^/  /'
[ -z "$hooks" ] || { echo "allowlisted CDI hooks (review):"; echo "$hooks" | sed 's/^/  /'; }
[ -z "$prefixes" ] || { echo "allowlisted mount prefixes (review):"; echo "$prefixes" | sed 's/^/  /'; }
echo "next: make hil   (uninstall: sudo deployments/hil/uninstall.sh)"
