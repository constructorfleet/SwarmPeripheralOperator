# AGENTS.md

# Autonomous Engineering Standards

This document defines mandatory engineering practices for all AI coding agents operating in this repository.

**These requirements are not suggestions.** Agents MUST follow them unless a more specific repository policy explicitly supersedes them.

The objective is to produce secure, maintainable, tested, reviewable software while allowing multiple agents to work concurrently without interfering with one another.

## 1. Core Engineering Principles

All agents MUST:

1. **Work in isolation.** Use a dedicated Git worktree and branch for every independently assigned task.
2. **Work from tracked issues.** Every meaningful change must correspond to an issue or work item in the repository's configured tracking system.
3. **Practice test-driven development (TDD).** Write failing tests before implementing or changing behavior.
4. **Test all code.** New and modified behavior must have appropriate automated test coverage.
5. **Follow existing conventions.** Respect the repository's architecture, coding standards, and tooling.
6. **Use Conventional Commits.** All commits must follow the Conventional Commits specification.
7. **Enforce quality gates.** Tests, linting, formatting, type checking, and security scanning must pass.
8. **Keep changes focused.** Prefer small, independently reviewable changes over large modifications.
9. **Preserve traceability.** Link changes, commits, reviews, and tests to their originating work items.
10. **Never conceal failures.** Report blocked verification, incomplete work, and security findings explicitly.

**Passing compilation is not sufficient evidence of correctness.**

---

## 2. Repository Discovery

Before modifying code, agents MUST inspect the repository to understand:

- Project structure and languages
- Package managers and build systems
- Existing test frameworks
- Linting and formatting configurations
- Type-checking and static-analysis tools
- CI/CD workflows
- Security-scanning configurations
- Contribution guidelines
- Branching and release conventions
- Repository hosting provider and issue tracker
- Existing architecture and design decisions

Prefer existing tooling over introducing new dependencies.

Do not replace repository-established practices without a documented technical justification.

### Source of truth

Use the following precedence:

1. Applicable organization security and engineering policies
2. Repository-specific instructions and contribution guidelines
3. This `AGENTS.md`
4. Existing code and established conventions
5. General language and framework best practices

Never interpret a lower-priority instruction as permission to bypass a higher-priority security or quality requirement.

---

## 3. Mandatory Git Worktree Isolation

**Agents MUST NOT implement changes directly in the primary working tree.**

Every task requires an isolated Git worktree and dedicated feature branch.

### Branch naming

Use the repository's established branch naming convention.

If none exists, use:

```text
<type>/<issue-id>-<short-description>
```

Examples:

```text
feat/123-add-flow-highlighting
fix/456-correct-cycle-detection
refactor/789-simplify-graph-indexing
```

### Worktree creation

Example:

```bash
git fetch origin

git worktree add \
  ../worktrees/123-flow-highlighting \
  -b feat/123-flow-highlighting \
  origin/main
```

Replace `main` with the actual default branch.

The worktree MUST be outside the primary checkout and MUST have its own branch.

Before creating it, check whether an appropriate worktree or branch already exists.

### Isolation requirements

- One independently assigned task per worktree.
- Never have multiple agents modify the same worktree concurrently.
- Never switch the primary checkout to implement a task.
- Never reuse another agent's branch without explicit coordination.
- Never reset, clean, delete, or overwrite another agent's worktree.
- Never assume uncommitted changes belong to the current agent.
- Coordinate modifications to shared files before editing them.

Worktree isolation does not guarantee isolation of external resources. Agents must also avoid collisions in databases, ports, containers, generated output, caches, and test environments.

### Shared resources

Where concurrent tasks share resources, use task-specific names or namespaces.

Examples:

- Unique temporary directories
- Unique database names or schemas
- Unique container project names
- Dynamically assigned test ports
- Isolated test fixtures
- Worktree-local build output

### Worktree cleanup

After a task has been merged and the worktree has no uncommitted changes:

```bash
git worktree remove ../worktrees/123-flow-highlighting
git worktree prune
```

Never remove a worktree containing uncommitted or unmerged work without explicit authorization.

---

## 4. Issue-Driven Development

**Every meaningful code change MUST originate from a tracked issue or work item.**

Use the issue tracker associated with the repository's hosting platform.

| Hosting platform | Tracking system |
|---|---|
| GitLab | GitLab Work Items / Issues |
| GitHub | GitHub Issues |
| Gitea / Forgejo | Issues |
| Azure DevOps | Azure Boards Work Items |
| Other | Configured repository tracker |

Use the repository's configured external tracker when one is explicitly established.

### Before implementation

Agents MUST:

1. Identify the relevant issue or work item.
2. Read its description, acceptance criteria, and related discussions.
3. Check for dependencies and blocking work.
4. Verify that the task is not already being implemented.
5. Confirm the expected behavior and scope.
6. Create a tracked issue if none exists and the agent has authorization.
7. Associate the development branch with the issue.

If issue creation is unavailable, document the intended issue and report the tracking limitation. Do not invent issue IDs.

### Issue requirements

An actionable issue should include:

- Problem statement
- Desired behavior
- Acceptance criteria
- Relevant technical constraints
- Dependencies
- Testing expectations

### During implementation

Agents SHOULD update the issue when:

- Work begins
- The implementation approach materially changes
- A blocker is encountered
- New requirements or dependencies are discovered
- The change is ready for review

Avoid excessive status commentary that adds no useful information.

### Completion

Every completed task MUST have:

- An associated issue
- A dedicated implementation branch
- Appropriate automated tests
- Passing quality gates
- Conventional Commits
- A pull request or merge request
- A summary of changes and verification

Use the hosting provider's supported syntax to link and close issues when the change is merged.

Do not close issues merely because implementation has started or local tests have passed.

---

## 5. Test-Driven Development (TDD)

**All behavior-changing code MUST be developed using the Red-Green-Refactor cycle.**

### RED: Write a failing test

Before implementing a behavior change:

1. Identify the expected behavior.
2. Write a test that demonstrates the requirement.
3. Execute the test.
4. Confirm it fails for the expected reason.

A test that fails because of a syntax error, broken fixture, missing dependency, or unrelated problem does not satisfy the RED phase.

### GREEN: Implement the minimum solution

Implement the smallest change that causes the failing test to pass.

Avoid speculative features, unrelated refactoring, or unnecessary abstractions.

### REFACTOR: Improve the implementation

After the test passes:

1. Improve readability and maintainability.
2. Remove unnecessary duplication.
3. Preserve externally observable behavior.
4. Rerun affected tests.
5. Verify formatting, linting, and type checks.

Repeat the cycle for each meaningful behavior increment.

### Mandatory coverage expectations

All new or changed behavior MUST be exercised by appropriate automated tests.

Use the relevant test levels:

- **Unit tests:** Pure functions, algorithms, validation, and business logic
- **Integration tests:** Interactions between modules, services, APIs, and persistence
- **Component tests:** UI rendering, state changes, and user interactions
- **End-to-end tests:** Critical user journeys and system boundaries
- **Contract tests:** Public APIs, schemas, adapters, and protocol compatibility
- **Regression tests:** Previously identified defects

Bug fixes MUST include a regression test that fails before the correction and passes afterward.

Security fixes SHOULD include tests that reproduce the vulnerable condition safely.

### Test quality

Tests MUST:

- Be deterministic
- Be independently executable
- Assert meaningful behavior
- Avoid unnecessary coupling to implementation details
- Avoid real external services unless explicitly designated as integration tests
- Clean up resources they create
- Use isolated fixtures and test data

Do not substitute snapshots for meaningful behavioral assertions when more precise assertions are practical.

### Prohibited shortcuts

Agents MUST NOT:

- Delete a failing test to make a build pass
- Add `.skip`, `.only`, or equivalent markers to evade verification
- Remove assertions merely to satisfy tests
- Mock the behavior under test instead of testing it
- Lower coverage thresholds to hide missing coverage
- Modify production behavior solely to satisfy an incorrect test
- Claim tests passed without executing them

Legitimate test changes are permitted when requirements change, but must be explained and reviewed.

### Coverage

Honor repository-defined coverage thresholds.

When no thresholds exist, establish a baseline and prevent coverage regressions.

Coverage percentages are supporting signals, not substitutes for meaningful tests.

New branches, error conditions, and security-sensitive behavior must receive explicit test attention.

### Exceptions

Documentation-only, formatting-only, and other non-behavioral changes may not require new behavioral tests.

Such changes still require applicable verification.

Existing untested behavior must be characterized with tests before substantial refactoring.

If TDD cannot reasonably be applied to a specific change, document the exception and obtain review rather than silently bypassing the requirement.

---

## 6. Coding Standards and Style

All changes MUST conform to repository coding conventions.

### Required checks

Run applicable checks for:

- Formatting
- Linting
- Type checking
- Static analysis
- Compilation
- API compatibility

Use the repository's configured tools.

For TypeScript/React projects, these may include:

- ESLint
- Prettier or Biome
- TypeScript compiler
- Vitest or Jest
- React Testing Library
- Playwright

Do not install competing tools merely because an agent prefers them.

### Code quality expectations

- Prefer explicit types over unsafe casts.
- Validate data at trust boundaries.
- Handle errors intentionally.
- Avoid dead code and unnecessary abstractions.
- Keep functions and modules focused.
- Avoid hidden global state.
- Preserve public API compatibility unless a breaking change is authorized.
- Document non-obvious decisions.
- Follow repository accessibility requirements for UI code.

Do not introduce lint suppressions, type-checking bypasses, or broad `any` types without a narrow, documented justification.

New suppressions must be reviewed.

---

## 7. Security and Vulnerability Scanning

**Security verification is mandatory for every change.**

Agents MUST use the configured security tooling and run all applicable scans before submitting changes.

### Required categories

| Check | Requirement |
|---|---|
| Dependency vulnerability scanning | Mandatory for applicable ecosystems |
| Secret detection | Mandatory |
| Static application security testing | Mandatory where supported |
| Container image scanning | Mandatory for container changes |
| Infrastructure-as-code scanning | Mandatory for IaC changes |
| License/policy compliance | Required when configured |
| Dynamic security testing | Required for applicable security-sensitive changes when available |

Example tools include:

- npm audit, pnpm audit, or equivalent
- OSV-Scanner
- Trivy
- Semgrep
- Gitleaks
- GitLab security scanners
- CodeQL
- Checkov

Prefer organization-approved tools.

### Security gate requirements

Agents MUST:

1. Scan dependencies for known vulnerabilities.
2. Check changes for exposed secrets.
3. Review security-sensitive code paths.
4. Validate untrusted inputs.
5. Review new dependencies for necessity and maintenance status.
6. Verify authorization and tenant boundaries where applicable.
7. Report findings with severity and affected components.

### Findings

Critical and high-severity findings introduced by a change MUST block completion.

Existing findings must be reported and evaluated under repository policy.

Medium and lower-severity findings must follow established organizational risk acceptance rules.

Agents MUST NOT:

- Disable scanners to make pipelines pass
- Suppress findings without documented justification
- Ignore vulnerabilities because they are inconvenient
- Embed credentials, tokens, or secrets in code
- Introduce unmaintained dependencies without review
- Weaken security policies without authorization

If a scanner is unavailable, report the missing verification explicitly.

A missing scan is not a passing scan.

---

## 8. Conventional Commits

All commits MUST follow Conventional Commits 1.0.0.

### Format

```text
<type>(<optional-scope>): <description>

<optional-body>

<optional-footers>
```

### Supported commit types

| Type | Purpose |
|---|---|
| `feat` | New functionality |
| `fix` | Bug correction |
| `refactor` | Internal code restructuring |
| `test` | Test additions or changes |
| `docs` | Documentation changes |
| `style` | Formatting-only changes |
| `perf` | Performance improvements |
| `build` | Build system changes |
| `ci` | CI/CD changes |
| `chore` | Maintenance |
| `revert` | Revert previous changes |

Examples:

```text
feat(graph): add upstream dependency highlighting

fix(traversal): prevent infinite loops in cyclic graphs

test(core): cover disconnected path traversal

docs(mcp): document embedded app integration

feat(schema)!: require stable graph identifiers
```

Breaking changes MUST use `!` or a `BREAKING CHANGE:` footer.

### Commit requirements

- Make focused, logically complete commits.
- Do not commit failing intermediate states intended for shared branches.
- Reference tracked issues using repository conventions.
- Describe what changed and why.
- Do not use meaningless messages such as `fix stuff` or `updates`.
- Do not mix unrelated features and fixes.

Follow repository squash-merge conventions and ensure the final squash commit remains Conventional Commit compliant.

Release automation relies on correctly classified commits.

---

## 9. Quality Gates

**No task is complete until all applicable quality gates pass.**

### Required verification sequence

1. Relevant unit tests
2. Relevant integration/component tests
3. Full applicable test suite
4. Formatting check
5. Linting
6. Type checking
7. Build/package validation
8. Dependency vulnerability scan
9. Secret scanning
10. Applicable SAST, container, and IaC scans
11. Diff and repository status review

Run targeted tests during TDD cycles and the full applicable validation suite before requesting review.

### CI requirements

The repository MUST have CI checks enforcing the required gates.

Local validation is not a replacement for CI.

A change must not be merged while required checks are failing, skipped, or pending unless an explicitly authorized organizational exception applies.

If required automation does not exist, create a tracked infrastructure task and disclose the gap.

### Verification reporting

Agents MUST distinguish between:

- **Passed:** Executed successfully
- **Failed:** Executed with an unsuccessful result
- **Blocked:** Could not run due to an identified dependency or environment problem
- **Not applicable:** Not relevant to the change, with explanation

Never report a check as passing based on inference.

---

## 10. Pull Requests and Merge Requests

All changes MUST be submitted through the repository hosting platform's review workflow.

### PR/MR description

Include:

```markdown
## Summary

What changed and why?

## Issue / Work Item

Closes #123

## Implementation

Key technical decisions.

## Tests

- Unit:
- Integration:
- End-to-end:
- Regression:

## Quality Gates

- Formatting:
- Lint:
- Types:
- Build:
- Security scans:

## Risks

Known limitations, compatibility concerns,
or operational considerations.

## Breaking Changes

None / Describe migration requirements.
```

Adapt issue-linking syntax to the repository host.

### Review requirements

- Keep diffs focused.
- Review the complete diff before submission.
- Remove temporary debugging artifacts.
- Avoid unrelated formatting changes.
- Update documentation when user-facing behavior or public APIs change.
- Include migration instructions for breaking changes.
- Wait for required CI checks and approvals.

Agents MUST NOT self-approve, bypass protected branches, or merge without authorization.

---

## 11. Parallel Agent Coordination

When multiple agents operate simultaneously:

1. Assign distinct issues or independently scoped sub-tasks.
2. Give each agent a dedicated worktree and branch.
3. Identify shared-file dependencies before implementation.
4. Avoid concurrent edits to the same module when possible.
5. Coordinate changes to shared schemas and public interfaces.
6. Integrate through reviewed commits and merge requests.
7. Run integration tests after combining changes.

### Dependency management

If Task B depends on Task A:

- Document the dependency in the issue tracker.
- Prefer completing and integrating Task A first.
- Use explicitly coordinated stacked branches only when necessary.
- Do not independently duplicate foundational changes.

### Conflicts

Agents MUST NOT resolve conflicts by blindly accepting one side or overwriting another agent's work.

Inspect conflicting changes, preserve intended behavior, and rerun tests.

---

## 12. Documentation Requirements

Agents MUST update relevant documentation when modifying:

- Public APIs
- Configuration
- Installation procedures
- Deployment processes
- Developer workflows
- Architecture
- Security-sensitive behavior
- User-visible functionality

Documentation examples should be validated where practical.

Do not document hypothetical functionality as already implemented.

Significant architectural decisions SHOULD receive an Architecture Decision Record (ADR).

---

## 13. Dependency Management

Before introducing a dependency:

1. Determine whether existing dependencies already solve the problem.
2. Assess maintenance activity.
3. Review licensing.
4. Check known vulnerabilities.
5. Evaluate package size and transitive dependencies.
6. Confirm compatibility with the existing runtime and build system.

Use lockfiles and reproducible installation procedures.

Do not introduce wildcard dependency versions without an established repository policy.

Commit appropriate lockfile changes alongside package manifest changes.

---

## 14. Release Management

Follow the repository's release process.

When automated semantic releases are configured:

- Use Conventional Commits to communicate version impact.
- Do not manually change released versions unless required by the release workflow.
- Do not create release tags manually.
- Do not publish packages outside approved CI/CD workflows.
- Do not bypass release merge request approval.
- Preserve generated changelog and release-manifest integrity.

For repositories using a Release Please-style workflow, ordinary development MRs must remain separate from release MRs.

Agents may prepare release changes but must not independently publish production releases without authorization.

---

## 15. Definition of Done

A task is complete only when all applicable conditions below are satisfied.

- [ ] A tracked issue/work item exists and is linked.
- [ ] Implementation occurred in an isolated worktree.
- [ ] The change is scoped to the tracked requirement.
- [ ] Tests were written before behavior-changing code.
- [ ] RED-GREEN-REFACTOR was followed.
- [ ] New and modified behavior has automated test coverage.
- [ ] Regression tests exist for bug fixes.
- [ ] Applicable tests pass.
- [ ] Formatting checks pass.
- [ ] Linting passes.
- [ ] Type checking passes.
- [ ] Build/package validation passes.
- [ ] Dependency vulnerability scanning passes under policy.
- [ ] Secret scanning passes.
- [ ] Applicable security checks pass.
- [ ] Documentation is updated.
- [ ] Commits follow Conventional Commits.
- [ ] The complete diff has been reviewed.
- [ ] A PR/MR references the originating issue.
- [ ] Required CI checks and approvals are satisfied.
- [ ] Known risks and limitations are disclosed.

**An unchecked mandatory requirement means the task is not done.**

A submitted PR/MR may be reported as ready for review, but not as merged or released until those events actually occur.

---

## 16. Required Agent Workflow

Use the following sequence for every implementation task.

### Step 1: Understand

- Inspect repository instructions and conventions.
- Identify the issue or work item.
- Read acceptance criteria.
- Identify affected components.
- Review dependencies and security considerations.

### Step 2: Isolate

- Fetch the latest default branch.
- Create a dedicated task branch.
- Create an isolated Git worktree.
- Verify the worktree is clean.
- Establish task-isolated development resources.

### Step 3: Plan

- Identify the smallest viable implementation.
- Define expected behavior.
- Identify required tests.
- Determine affected public interfaces.
- Identify compatibility and security risks.

### Step 4: Test First

- Write failing behavioral tests.
- Execute them.
- Confirm failure is caused by missing or incorrect behavior.

### Step 5: Implement

- Write the minimum production code.
- Make tests pass.
- Refactor without changing behavior.
- Repeat as necessary.

### Step 6: Validate

- Run relevant tests.
- Run the full applicable suite.
- Run formatting and lint checks.
- Run type checking.
- Build the project.
- Run security and dependency scans.
- Inspect the final diff.

### Step 7: Commit

- Stage only intended changes.
- Create focused Conventional Commits.
- Reference the tracked issue.
- Verify the working tree state.

### Step 8: Review

- Push the task branch.
- Create or update the PR/MR.
- Include verification results.
- Address review feedback.
- Rerun affected checks.

### Step 9: Complete

- Confirm required CI checks.
- Confirm required approvals.
- Report final status accurately.
- Allow the authorized merge workflow to proceed.
- Clean up the worktree only after safe integration.

---

## 17. Failure Handling and Escalation

If any required step cannot be completed:

1. Do not hide the failure.
2. Identify the actual cause.
3. Attempt reasonable remediation within task scope.
4. Record the blocker in the associated issue.
5. Report incomplete verification.
6. Do not declare the task complete.

Agents MUST NOT weaken tests, static analysis, security policy, or CI configuration merely to obtain a passing status.

If a requirement cannot be implemented safely, stop the affected work and explain the constraint.

A correct, explicitly incomplete implementation is preferable to an apparently complete but unverified change.

---

## 18. Final Agent Report

At the end of each task, provide a concise report containing:

```markdown
## Task
Issue/work item reference

## Worktree
Path and branch

## Changes
Summary of implementation

## Tests
Added/updated tests and execution results

## Quality
Formatting, linting, typing, build, security

## Commit(s)
Conventional Commit identifiers

## Review
PR/MR link and status

## Outstanding
Known risks, failures, or follow-up work
```

Do not claim that actions, tests, reviews, merges, releases, or deployments occurred unless they actually did.

---

## Final Rule

**No issue, no implementation.  
No worktree, no modification.  
No failing test first, no behavior-changing code.  
No passing tests, no completion.  
No quality gates, no merge.  
No security verification, no release.**

The purpose of these rules is not ceremony. It is to ensure every change is isolated, traceable, secure, tested, and reproducible.

Agents are expected to optimize for engineering correctness and maintainability rather than the appearance of rapid progress.
