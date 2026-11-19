# Contributing

## Workflow

1. Work from a GitHub issue. Phase 1+ issues stay blocked until the Phase 0 gate (#1) passes.
2. Use a dedicated worktree and branch: `<type>/<issue>-<slug>`.
3. Write a failing test first, then the minimum code to pass it.
4. Run `make check` and `make secrets` before pushing.
5. Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/). Reference the issue in the footer (`Refs: #5`).
6. Open a PR with the template in [AGENTS.md](AGENTS.md) section 10.

## Tests

- Unit tests: `make test`.
- Tests must be deterministic and clean up the containers and networks they create.

## Code style

`gofmt`, `goimports`, and `golangci-lint` (`.golangci.yml`). No new lint suppressions without a narrow, written reason.

## Releases

[Release Please](https://github.com/googleapis/release-please) owns versions, tags, and `CHANGELOG.md`.

- Merges to `main` update an open release PR from the Conventional Commit history.
- Merging the release PR tags the version and publishes the GitHub release.
- Do not tag manually, edit `.release-please-manifest.json`, or edit `CHANGELOG.md` by hand.
- `feat` bumps minor, `fix` bumps patch. Before 1.0.0, breaking changes bump minor.
