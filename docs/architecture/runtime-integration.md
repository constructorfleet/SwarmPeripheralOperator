# Runtime integration

`spo-runtime` is an OCI runtime wrapper. dockerd invokes it for every container (it is the default runtime), and it always execs runc in the end.

## Lifecycle position

```
dockerd ── containerd ── containerd-shim-runc-v2 ── spo-runtime create ── runc create ── runc start ── workload
                                                    ^ edits bundle/config.json here; no workload process exists yet
```

Only `create` is intercepted. Every other subcommand (`start`, `kill`, `delete`, `state`, `features`, `--version`) execs runc unchanged.

## Create path

1. Parse argv (`internal/ociargs`) for the bundle, container ID, `--log`, and `--log-format`.
2. Load `/etc/spo/runtime.json` (or `$SPO_RUNTIME_CONFIG`). Missing file: no SPO kinds, pure passthrough. Malformed file: every `create` fails; other subcommands ignore the config so containers can still be stopped and deleted.
3. Read only `process.env`. If there is no `DOCKER_RESOURCE_<KIND>` for a configured kind, exec runc without touching the bundle.
4. Read labels and name via `GET /v1.44/containers/json?all=1&filters={"id":[cid]}` and require an exact ID match. Inspect would deadlock (ADR 0001, F3). The name must be `/<service>.<slot|node>.<taskID>`: images can set labels, but not names.
5. `GET /v1/tasks/{task}/allocation` from the controller over mTLS (TLS 1.3, pinned CA). Retry transport errors and 503 until `lookupTimeout`.
6. Require that the response's task, service, and node IDs equal the labels, that its container ID (if the manager has one) equals the argv ID, and that every hinted kind has an assignment.
7. Map each assignment `KIND=id` to the CDI name `<cdiKind>=id`. Validate the edits against policy. Inject with `tags.cncf.io/container-device-interface`.
8. Write `config.json` atomically. Unknown object fields are kept. If an array element holds a field unknown to runtime-spec 1.3, the edit is refused rather than dropping it. Append a JSON line to the audit log.
9. Exec runc.

On any failure in steps 4-8, the runtime writes a runc-format error to `--log` and exits 1. dockerd reports `OCI runtime create failed: … spo-runtime: …` in the task error.

## CDI policy

| Rule | Reason |
|---|---|
| Spec dirs and files owned by root, not group or world writable | CDI edits are host-privileged |
| Hooks only from `allowedHooks` (exact path) | Hooks run as root on the host |
| Mount host paths under `allowedMountPrefixes`, checked before and after symlink resolution | Prevents host file exposure |
| Mount type `bind` only; options limited to `bind rbind ro rw nosuid nodev noexec private rprivate slave rslave` | Blocks overlay/proc mounts and `suid`/`dev` |
| Device paths clean, absolute, under `/dev` | Path traversal |
| Host device `Lstat` must be a char/block node, and type and major:minor must match the spec | Symlink redirection, stale specs |
| `1:1`, `1:2`, `1:4` (`mem`, `kmem`, `port`) always denied, plus `deniedDevices` | Host memory and I/O ports |
| `netDevices`, `intelRdt` rejected | Out of scope |

Allowlisted hooks run with whatever args and env the spec supplies. Allowlist only hooks that are safe with any arguments.

## Configuration

```json
{
  "runc": "/usr/bin/runc",
  "kinds": { "SPO_RNG": "spo.local/rng", "SPO_SERIAL": "spo.local/serial" },
  "controller": {
    "url": "https://10.0.0.2:7443", "serverName": "spo-controller",
    "caFile": "/etc/spo/ca.crt", "certFile": "/etc/spo/node.crt", "keyFile": "/etc/spo/node.key"
  },
  "cdiSpecDirs": ["/etc/cdi", "/var/run/cdi"],
  "allowedHooks": [],
  "allowedMountPrefixes": [],
  "lookupTimeout": "10s",
  "auditLog": "/var/log/spo/runtime.jsonl"
}
```

`daemon.json` on each node:

```json
{
  "runtimes": { "spo": { "path": "/usr/local/bin/spo-runtime" } },
  "default-runtime": "spo",
  "node-generic-resources": ["SPO_RNG=rng0", "SPO_SERIAL=serial0"]
}
```

## Limits

- Swarm cannot select a runtime per service, so the wrapper must be the default runtime.
- An unhinted container makes no network call and no file write. Its only extra cost is reading the config and `config.json`.
- Tested only with the `io.containerd.runc.v2` shim and runc. crun or youki would need `runc` set to their path, and are untested.
