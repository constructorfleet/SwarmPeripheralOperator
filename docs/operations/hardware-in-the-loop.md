# Hardware-in-the-loop testing

Runs the Phase 0 gate suite against real devices on a real Swarm. This is the evidence #1 still needs (#8).

> Use a dedicated test host. `install.sh` makes `spo-runtime` the default runtime for **every** container, and it restarts Docker, which restarts all running containers.

## Requirements

- Linux with cgroup v2, Docker Engine ≥ 25, Go (see `go.mod`), and `openssl`.
- This host is a Swarm manager (`docker swarm init` for a single node).
- GPU: NVIDIA driver plus NVIDIA Container Toolkit (`nvidia-smi` and `nvidia-ctk` on `PATH`).
- USB: the real device node, such as `/dev/ttyUSB0`. Symlinks such as `/dev/serial/by-id/…` are rejected on purpose.

## Run

```bash
make build
sudo SPO_HIL_DEVICES="SPO_USB:serial0=/dev/ttyUSB0" deployments/hil/install.sh
make hil
```

`install.sh`:

- detects NVIDIA GPUs and uses their UUIDs as IDs
- writes CDI specs, `/etc/docker/daemon.json`, certificates, and `/etc/spo/runtime.json`
- restarts Docker
- deploys `spo-controller`
- writes `/etc/spo/hil.json` for the tests

It refuses to replace an existing `daemon.json` unless you set `SPO_HIL_OVERWRITE_DAEMON_JSON=1`, and it keeps a backup when it does. Before you run `make hil`, review the CDI hooks and mount prefixes it prints: it allowlists exactly what `nvidia-ctk` generated.

Uninstall restores the backup and the default runtime:

```bash
sudo deployments/hil/uninstall.sh
```

## What a pass proves

| Test | Gate |
|---|---|
| `TestGateSwarmTaskReceivesAssignedDevice` | The task opens **only** its assigned device. It runs unprivileged with no manual devices, the device is in the spec runc started from, and the audit log has the entry. |
| `TestHILGPUSeesOnlyAssignedGPU` | `nvidia-smi -L` lists exactly the UUID Swarm assigned |
| `TestHILGPUComputation` | The CUDA `vectorAdd` sample prints `Test PASSED` |
| `TestGateExclusiveAllocationAndReclaim` | With every device held, another service stays `Pending`, then starts after the holder's container `die` |
| `TestGateForgedResourceEnvFailsClosed`, `TestGateImageForgedTaskLabelsRejected` | Forged env and forged image labels get nothing |
| other `TestGate*` | Lifecycle, stack deploy, unprivileged `mknod`, controller outage |

The suite tests the first device kind in `hil.json` for the generic checks. List the device you care most about first. GPU tests skip unless a device has `"gpu": true`.

Post the `make hil` output on #1. A maintainer decides whether the gate passes.

## Manual config

`make hil` reads `HIL_CONFIG` (default `/etc/spo/hil.json`). To write one by hand, see [`examples/hil/hil.example.json`](../../examples/hil/hil.example.json). Fields:

| Field | Default | Meaning |
|---|---|---|
| `devices[].kind` | required | Swarm generic resource kind |
| `devices[].ids` | required | advertised value → host device path |
| `devices[].gpu` | `false` | run the NVIDIA checks |
| `managerHost` | local daemon | `DOCKER_HOST` value for a manager, when this node is a worker |
| `controllerService` | `spo-controller` | scaled to 0 and back by the outage test |
| `auditLog` | `/var/log/spo/runtime.jsonl` | |
| `probeImage`, `gpuImage`, `cudaSampleImage` | busybox, `nvidia/cuda:12.4.1-base-ubuntu22.04`, `nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0` | |

## Validation of this procedure

`make hil-selftest` runs `install.sh`, `make hil`'s suite in host mode, and `uninstall.sh` inside a disposable Docker-in-Docker node. Kernel char devices stand in for the hardware. CI runs it on every relevant change.
