# Plan 1.1: Isolated Fixes — RBAC Duplicate, Flag Validation, Write Error

## Context
Three independent low-risk fixes from the concerns cleanup that share no file dependencies with Phases 5–6. Each is a single-function change.

## Dependencies
None.

## Tasks

### Task 1: Remove duplicate RBAC check in shell ephemeral path
**Files:** `cmd/shell.go`
**Action:** modify
**Description:**
In `runPodShell`, lines 122–131 handle `errors.IsForbidden(err)` after `CreateEphemeralContainer` fails. This block calls `CheckEphemeralContainerRBAC` a second time (line 123), duplicating the pre-flight check already done at line 97.

Replace the `IsForbidden` block (lines 122–131) with a simpler version that reuses the `checks` variable from line 97:
```go
if errors.IsForbidden(err) {
    rbacMsg := k8s.FormatRBACError(checks)
    if rbacMsg != "" {
        return fmt.Errorf("forbidden creating ephemeral container\n\n%s", rbacMsg)
    }
    return fmt.Errorf("error: forbidden creating ephemeral container in pod %q — check your RBAC permissions", podName)
}
```
This eliminates the second `CheckEphemeralContainerRBAC` call and the `checks2`/`rbacCheckErr` variables.

**Acceptance Criteria:**
- `grep -n "CheckEphemeralContainerRBAC" cmd/shell.go` returns exactly 1 hit (line 97)
- `go build ./...` passes
- `go test ./cmd/...` passes

### Task 2: Enforce --status requires --show-pods on eks node
**Files:** `cmd/eks/node.go`
**Action:** modify
**Description:**
Add a guard at the top of `runNode` (after flag parsing, before any API calls). Insert before the existing logic:
```go
if statusFilter != "" && !showPods {
    return fmt.Errorf("--status requires --show-pods")
}
```
The `statusFilter` variable is at line 77, `showPods` at line 76 (package-level vars).

**Acceptance Criteria:**
- `go build ./...` passes
- Running `kdiag eks node --status EXHAUSTED` (without `--show-pods`) returns error containing "--status requires --show-pods"

### Task 3: Handle discarded write error in cmd/eks/node.go
**Files:** `cmd/eks/node.go`
**Action:** modify
**Description:**
At line 271, replace:
```go
_, _ = os.Stdout.WriteString(outgoingString(report.Summary.CheckedNodes, report.Summary.SkippedNodes, atRisk+warningCount))
```
with:
```go
if _, err := os.Stdout.WriteString(outgoingString(report.Summary.CheckedNodes, report.Summary.SkippedNodes, atRisk+warningCount)); err != nil {
    return err
}
```
This is consistent with the rest of the codebase's error handling for stdout writes.

**Acceptance Criteria:**
- `grep -n "_, _" cmd/eks/node.go` returns zero hits
- `go build ./...` passes
- `go vet ./...` passes

## Verification
```bash
cd /Users/lgbarn/Personal/kdiag
go build ./...
go test ./...
go vet ./...
grep -c "CheckEphemeralContainerRBAC" cmd/shell.go  # should be 1
grep -c "_, _" cmd/eks/node.go  # should be 0
```
