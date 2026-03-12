# Review: Plan 1.1

## Verdict: MINOR_ISSUES

---

## Stage 1: Spec Compliance

**Verdict: PASS**

### Task 1: Remove duplicate RBAC check in shell ephemeral path
- **Status: PASS**
- **Evidence:** `cmd/shell.go` lines 122–128 implement exactly the pattern from the plan spec. `grep -c "CheckEphemeralContainerRBAC" cmd/shell.go` returns `1` (pre-flight call at line 97 only). The `checks2` and `rbacCheckErr` variables are absent from the file. `go build ./...` and `go test ./cmd/...` pass clean.
- **Notes:** See Minor finding below regarding a latent dead-code path introduced by the reuse of `checks`.

### Task 2: Enforce --status requires --show-pods on eks node
- **Status: PASS**
- **Evidence:** `cmd/eks/node.go` lines 94–96 contain the guard exactly as specified. It is placed before the Kubernetes client construction, satisfying "before any API calls." `go build ./...` passes. The guard correctly reads the cobra-populated package-level vars `statusFilter` and `showPods`.
- **Notes:** None.

### Task 3: Handle discarded write error in cmd/eks/node.go
- **Status: PASS**
- **Evidence:** `cmd/eks/node.go` line 275 uses the `if _, err := ...; err != nil { return err }` pattern. `grep -c "_, _" cmd/eks/node.go` returns `0`. `go build ./...` and `go vet ./...` pass clean.
- **Notes:** None.

---

## Stage 2: Code Quality

### Critical
None.

### Minor

- **Dead-code branch in the `IsForbidden` handler — `cmd/shell.go` lines 122–128**

  The reuse of `checks` from the pre-flight RBAC call (line 97) is logically correct as written, but the `rbacMsg != ""` branch (line 124) can never be reached. Here is why: the pre-flight block at lines 101–103 returns early if `FormatRBACError(checks)` is non-empty. Only if `FormatRBACError(checks) == ""` (i.e., all permissions were granted) does execution proceed to `CreateEphemeralContainer`. Therefore `FormatRBACError(checks)` in the `IsForbidden` handler at line 123 will always return `""`, and the `if rbacMsg != "" { ... }` branch is unreachable dead code. The only reachable path is the fallback generic message at line 127.

  This is not a regression — the pre-existing duplicate call had the same logical problem (a second RBAC check right after a clean pre-flight would also return no failures under normal circumstances). The fix achieves its stated goal of eliminating the redundant network call, and the resulting error message is reasonable. However the dead-code branch adds confusion for future maintainers.

  **Remediation options (choose one):**

  Option A — simplify the handler to its only reachable path:
  ```go
  if errors.IsForbidden(err) {
      return fmt.Errorf("error: forbidden creating ephemeral container in pod %q — check your RBAC permissions", podName)
  }
  ```

  Option B — retain the `FormatRBACError` call but remove the dead branch, making the full message the unconditional output (anticipating future callers that might skip the pre-flight):
  ```go
  if errors.IsForbidden(err) {
      if rbacMsg := k8s.FormatRBACError(checks); rbacMsg != "" {
          return fmt.Errorf("forbidden creating ephemeral container\n\n%s", rbacMsg)
      }
      return fmt.Errorf("error: forbidden creating ephemeral container in pod %q — check your RBAC permissions", podName)
  }
  ```
  This is the current code as-written; Option B is what is already implemented. It is correct and safe; the only issue is that the `if rbacMsg != ""` guard is permanently false. If the pre-flight guard is ever relaxed (e.g., made optional via a flag), the branch would activate correctly, which is a reasonable defensive argument for leaving it as-is. Documenting this invariant with a short comment would eliminate the confusion.

### Positive

- **Commit discipline:** Each of the three tasks landed in its own atomic commit with a descriptive message that explains the "why" (e.g., "prevents a silent no-op filter"). The diff sizes are minimal and exactly match the plan's stated changes — 9 lines net across three commits with no scope creep.

- **Guard placement in `runNode`:** The `--status requires --show-pods` guard is placed before K8s client construction, meaning invalid flag combinations are rejected without incurring any network I/O. This is the correct insertion point even though the plan's phrasing ("after flag parsing") could have been interpreted as a later position.

- **Consistent error handling for `WriteString`:** The fix at line 275 of `cmd/eks/node.go` brings the summary-line write into parity with the `p.Flush()` check at line 227 and the broader codebase pattern. The error is propagated via `return err` rather than silently discarded, which is correct for a CLI where stdout write failures (e.g., broken pipe) should result in a non-zero exit.

- **Verification artifacts:** All acceptance criteria from the plan are confirmed by the SUMMARY, and independent re-verification (grep counts, build, test, vet) matches the documented results.
