# Swarm Peripheral Operator (SPO)

Hardware orchestration for Docker Swarm: services declare the devices they need, and SPO handles discovery, allocation, placement, and pre-start device injection without privileged containers.

> **Status: Phase 0 (feasibility).** Nothing here is production-ready. The architecture is gated on
> [#1](https://github.com/constructorfleet/SwarmPeripheralOperator/issues/1). Do not deploy.

## Components (planned)

| Binary | Role |
|---|---|
| `spo-runtime` | OCI runtime wrapper that injects allocated devices before the task starts |
| `spo-controller` | Cluster control plane on Swarm managers |
| `spo-agent` | Per-node discovery and inventory |
| `spo` | Operator CLI |

## Development

Requires Go (see `go.mod`), Docker, `golangci-lint`, and `gitleaks`.

```bash
make check     # fmt-check, vet, lint, test, vuln
make build     # linux binaries in ./bin
make secrets   # gitleaks over git history
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
