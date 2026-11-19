#!/bin/sh
# Validates the HIL path in a disposable Docker-in-Docker node:
# deployments/hil/install.sh, the host-mode suite, then deployments/hil/uninstall.sh.
# Kernel char devices stand in for GPU and USB hardware.
set -eu

IMAGE=${SPO_DIND_IMAGE:-docker:29-dind}
NAME=spo-hil-selftest-$$
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d)
ARCH=$(docker version -f '{{.Server.Arch}}')
# dind has no systemd. Stop dockerd, wait, start it detached.
RESTART='pkill -x dockerd; while pgrep -x dockerd >/dev/null; do sleep 1; done; setsid dockerd >/var/log/dockerd.log 2>&1 </dev/null &'

cleanup() {
	[ -n "${SPO_IT_KEEP:-}" ] || docker rm -f -v "$NAME" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT
nx() { docker exec "$@"; }

echo "==> build ($ARCH)"
(cd "$ROOT" && make -s build GOOS=linux GOARCH="$ARCH" BIN="$WORK/bin" >/dev/null)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go test -c -tags integration -o "$WORK/bin/spo-hil.test" ./tests/integration/)

echo "==> start $IMAGE"
# --init: PID 1 must reap the stopped dockerd, or pgrep sees a zombie forever.
docker run -d --init --privileged --name "$NAME" --entrypoint sh \
	-v "$WORK/bin:/src/bin:ro" -v "$ROOT/deployments/hil:/src/hil:ro" "$IMAGE" -c 'sleep infinity' >/dev/null
nx -d "$NAME" sh -c 'dockerd-entrypoint.sh dockerd >/var/log/dockerd.log 2>&1'
i=0; until nx "$NAME" docker info >/dev/null 2>&1; do i=$((i+1)); [ $i -lt 60 ]; sleep 1; done
nx "$NAME" apk add -q openssl
nx "$NAME" docker swarm init >/dev/null
docker image inspect busybox:latest >/dev/null 2>&1 || docker pull -q busybox:latest >/dev/null
docker save busybox:latest | nx -i "$NAME" docker load -q >/dev/null

pick() { for p in "$@"; do nx "$NAME" test -c "$p" && { echo "$p"; return; }; done; echo "no device among $*" >&2; exit 1; }
RNG=$(pick /dev/hwrng /dev/full)
SER=$(pick /dev/ttyS1 /dev/ttyS0)

echo "==> install.sh (SPO_RNG=$RNG SPO_SERIAL=$SER)"
nx -e SPO_BIN_DIR=/src/bin -e SPO_HIL_GPU=0 -e "SPO_HIL_DEVICES=SPO_RNG:rng0=$RNG SPO_SERIAL:serial0=$SER" \
	-e "SPO_HIL_RESTART_CMD=$RESTART" "$NAME" /src/hil/install.sh

echo "==> host-mode suite"
nx -e SPO_IT_MODE=host -e SPO_HIL_CONFIG=/etc/spo/hil.json "$NAME" \
	/src/bin/spo-hil.test -test.v -test.count=1 -test.timeout=30m

echo "==> uninstall.sh"
out=$(nx -e "SPO_HIL_RESTART_CMD=$RESTART" "$NAME" /src/hil/uninstall.sh)
echo "$out"
echo "$out" | grep -q 'default runtime: runc' || { echo "uninstall did not restore runc" >&2; exit 1; }
nx "$NAME" test ! -e /etc/spo || { echo "uninstall left /etc/spo" >&2; exit 1; }
echo "==> selftest passed"
