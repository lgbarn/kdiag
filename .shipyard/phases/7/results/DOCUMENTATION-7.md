# Documentation Report
**Phase:** 7 — Isolated Bug Fixes
**Date:** 2026-03-11

## Summary

- API/Code docs: 1 file reviewed (`cmd/eks/node.go`) — 1 user-visible behavior change requiring doc update
- Architecture updates: none — no structural changes
- User-facing docs: 1 doc gap found in `docs/commands/eks-node.md`; `docs/commands/shell.md` is already accurate

## API Documentation

### `cmd/eks/node.go` — `runNode`

- **File:** cmd/eks/node.go
- **Public interfaces affected:** none (all changes are internal to `runNode`)
- **Documentation status:** gap found — `--status` flag constraint undocumented

The `--status` flag now enforces a hard dependency on `--show-pods`. Passing `--status` without `--show-pods` exits immediately with:

```
Error: --status requires --show-pods
```

This constraint is already expressed in the flag's `Usage` string (`"requires --show-pods"`), but the command reference doc (`docs/commands/eks-node.md`) does not document `--show-pods` or `--status` at all. Both flags were added in an earlier phase and appear only in README examples; the command reference is the authoritative reference users consult for flag semantics.

### `cmd/shell.go` — `runPodShell`

- **File:** cmd/shell.go
- **Public interfaces affected:** none
- **Documentation status:** no change required

The RBAC pre-flight check is now simplified: the `checks` variable from `CheckEphemeralContainerRBAC` is reused for the forbidden-on-create error path instead of repeating the check. Behavior is identical — the same permissions are checked, the same error messages are produced. `docs/commands/shell.md` accurately describes the RBAC behavior and requires no update.

## Architecture Updates

No component boundaries, data flows, or design decisions changed in this phase. All three fixes are corrections within existing code paths.

## User Documentation

### Gap: `docs/commands/eks-node.md` is missing `--show-pods` and `--status` flag documentation

- **File:** docs/commands/eks-node.md
- **Type:** Command reference
- **Status:** Needs update

The Flags table covers only `--profile` and `--region`. The `--show-pods` and `--status` flags are absent. Because `--status` now returns a hard error without `--show-pods`, a user who discovers `--status` in `--help` output and runs it alone will get an error with no reference doc to explain why.

**Recommended addition to the Flags table:**

| Flag | Default | Description |
|------|---------|-------------|
| `--show-pods` | `false` | List pods on each node with a daemonset/workload breakdown |
| `--status` | — | Filter output to nodes matching this status: `EXHAUSTED`, `WARNING`, or `OK`. Requires `--show-pods`. |

**Recommended addition to the Examples section:**

```bash
# Show pods on each node with workload breakdown
kdiag eks node --show-pods

# Show only exhausted nodes with their pods
kdiag eks node --show-pods --status EXHAUSTED

# Filter to WARNING nodes — useful for capacity planning before pressure hits
kdiag eks node --show-pods --status WARNING
```

The existing README examples (lines 121–123) already cover these flag combinations correctly and do not need changes.

## Gaps

1. **`docs/commands/eks-node.md` Flags table** — `--show-pods` and `--status` are undocumented. `--status` now has a validated dependency (`--status requires --show-pods`) that users will encounter without any reference documentation to explain it.

## Recommendations

1. Update `docs/commands/eks-node.md` Flags table and Examples section as shown above. This is a one-paragraph change with three example commands.
2. No changes needed to README, `docs/commands/shell.md`, or any other file. The shell fix is an internal refactor with identical external behavior; the node write-error fix is silent in normal operation.
