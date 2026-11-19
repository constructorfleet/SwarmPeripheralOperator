# Docker / Swarm device compatibility

Tested facts are marked **tested** and name the test or experiment that shows them. Facts taken from upstream documentation are marked **sourced**. Nothing here is inferred from API shape alone.

## Device features: `docker run` vs Swarm services

Engine 29.10 CLI, `docker run --help` vs `docker service create --help` (**tested**):

| Feature | `docker run` | Swarm service | Compose under `docker stack deploy` |
|---|---|---|---|
| `--device` / `devices:` | yes | **no** | ignored: `Ignoring unsupported options: devices` (**tested**, 29.10) |
| `--device-cgroup-rule` | yes | **no** | rejected by schema: `additional properties 'device_cgroup_rules' … not allowed` (**tested**, 29.10) |
| `--gpus` / CDI device requests | yes | **no** | n/a |
| `--annotation` (OCI annotations) | yes | **no** | n/a |
| `--runtime` (per-container runtime) | yes | **no** | rejected by schema (**tested**, 29.10) |
| `--privileged` | yes | **no** | ignored: `Ignoring unsupported options: privileged` (**tested**, 29.10) |
| `--cap-add` | yes | yes | yes |
| Generic resources (discrete and named) | n/a | yes | `deploy.resources.reservations.generic_resources` (**tested**: `TestGateStackDeployWithComposeGenericResources`) |
| Daemon `default-runtime` used for tasks | yes | yes (**tested**: ADR 0001 F1) | yes |
| CDI spec dirs (`cdi-spec-dirs`) | yes | **not reachable**: no field carries device names | n/a |

Docker CLI CDI support (`--device vendor/class=name`) does **not** extend to Swarm. Swarm task specs have no device or annotation field.

## What blocks native Swarm device requests

1. SwarmKit `ContainerSpec` has no devices, device requests, CDI names, annotations, or runtime selection.
2. The executor (`daemon/cluster/executor/container`) builds `HostConfig` from `ContainerSpec`, so it cannot set `Devices`, `DeviceRequests`, or `Annotations`.
3. The only Swarm channel for per-task hardware identity is generic resources. Named values are bound by the scheduler and exported as `DOCKER_RESOURCE_<KIND>` env, which service env can also set (**tested**, ADR 0001 F6).
4. Workers cannot read task specs. `/tasks` is manager-only, so a node-local component cannot verify a binding without a manager (**tested**).

## Runtime-visible identity during `create`

| Source | Available at `create` | Trusted | Notes |
|---|---|---|---|
| Container ID (argv) | yes | yes | from containerd-shim |
| OCI annotations | **empty** for Swarm tasks | n/a | **tested** |
| `process.env` | yes | **no** | service owner controls it |
| `GET /containers/{id}/json` | **blocks** (container lock) | yes | **tested**: hung until a 10s timeout |
| `GET /containers/json?filters=id` | yes | yes | prefix match; requires an exact ID |
| `com.docker.swarm.*` labels | yes | yes | the executor overwrites forged values (**tested**: `TestGateExecutorOverwritesForgedSwarmLabels`) |
| `task.GenericResources` | managers only | yes | **tested** |

## Tested matrix (feasibility suite)

Full suite: `make integration` with `SPO_DIND_IMAGE=docker:<v>-dind`, 10 gate tests. The runc version is reported by Docker under the default runtime's name (`spo`).

| Engine | containerd | runc | Host | Devices | Result |
|---|---|---|---|---|---|
| 29.10.0 | v2.4.2 | 1.5.3 | Docker Desktop 4.99 VM, linux/arm64, kernel 7.0.14, cgroup v2 | `/dev/hwrng`, `/dev/ttyS1` | 10/10 pass |
| 28.5.2 | v1.7.28 | 1.3.3 | same | same | 10/10 pass |
| 27.5.1 | v1.7.25 | 1.2.4 | same | same | 10/10 pass |
| 26.1.4 | v1.7.18 | 1.1.12 | same | same | 10/10 pass |
| 25.0.5 | v1.7.13 | 1.1.12 | same | same | 10/10 pass |
| 29.10.0 | v2.4.2 | 1.5.3 | GitHub `ubuntu-latest`, linux/amd64, cgroup v2 | `/dev/hwrng`, `/dev/ttyS1` | 10/10 pass ([run 44451465370](https://github.com/constructorfleet/SwarmPeripheralOperator/actions/runs/44451465370)) |
| 28.5.2 | v1.7.28 | 1.3.3 | same | same | 10/10 pass (same run) |

CI runs the 29 and 28 rows on every relevant change (`.github/workflows/feasibility.yml`).

### Support boundaries

- **Minimum Engine 25.** SPO calls Engine API v1.44. Older engines are not tested and are not supported.
- **cgroup v1:** not tested.
- **Rootless Docker:** not tested.
- **Runtimes other than runc** (crun, youki): not tested.
- **Docker Desktop** is a test host only, not a target.
- **Hardware:** the devices are real kernel char devices outside Docker's default cgroup allowlist: virtio-rng (`10:183`) and an 8250 UART (`4:65`). **No GPU or USB device has been tested** (#8).

## Existing solutions

| Solution | Mechanism | Pre-start | Trusted identity | Scheduling | Limits for SPO's goals |
|---|---|---|---|---|---|
| [leinardi/swarm-device-access](https://github.com/leinardi/swarm-device-access) (Apache-2.0, active) | Privileged daemon on Docker `start` events; adds cgroup v1/v2 BPF rules for `/dev` bind mounts | **no**: rules are added after start | opt-in labels and allow/deny globs; any service that bind-mounts `/dev` paths can claim them | none (operator uses constraints) | access is granted after the workload starts, no exclusivity, and device paths are hard-coded in services (**sourced**: README) |
| [allfro/device-mapping-manager](https://github.com/allfro/device-mapping-manager) (no license, inactive since 2024-01) | Same idea, predecessor of the above | no | none: grants every `/dev` bind mount | none | reported failing on Engine 29 (**sourced**: swarm-device-access README) |
| NVIDIA Container Toolkit + `generic_resources` | GPUs advertised as named generic resources; `nvidia-container-runtime` reads `DOCKER_RESOURCE_GPU` from env | yes | **env**, which the service can forge (see F6) | Swarm, by GPU ID | NVIDIA only. The env channel is forgeable, and `NVIDIA_VISIBLE_DEVICES` can be set directly (**sourced**: toolkit docs; not tested here) |
| `docker run --device` from a socket-holding service | Wrapper service launches plain containers | yes | n/a | none | not a Swarm task: no update, rollback, secrets, or cleanup |
| Kubernetes device plugins / DRA | kubelet gRPC plugins allocate device IDs at admission; DRA adds structured claims; CDI injection by the CRI runtime | yes | kubelet-owned | scheduler (DRA) | not available in Swarm. It is the model SPO's split follows (scheduler binds, node injects via CDI). |

SPO combines Swarm-scheduled named resources (like the NVIDIA path) with a manager-verified identity and CDI injection at `create`. The verified identity is what closes the env-forgery gap.
