# Simplification Report
**Phase:** 7 — Isolated Fixes (RBAC duplicate, flag guard, write error)
**Date:** 2026-03-11
**Files analyzed:** 2 (`cmd/shell.go`, `cmd/eks/node.go`)
**Findings:** 1 Medium, 2 Low

---

## Medium Priority

### IsForbidden fallback in `runPodShell` is logically unreachable after pre-flight succeeds

- **Type:** Remove
- **Locations:** `cmd/shell.go:122-128`
- **Description:** The pre-flight RBAC check at `cmd/shell.go:97-103` calls
  `k8s.CheckEphemeralContainerRBAC` and returns early with a formatted permission
  error if any check is denied. If that check passes, the cluster has already
  confirmed the user has the required permissions before `CreateEphemeralContainer`
  is called. The `IsForbidden` handler at line 122 then re-calls `k8s.FormatRBACError`
  on those same (already-confirmed-passing) `checks` — which means `rbacMsg` will
  always be empty at that point, making the enriched `fmt.Errorf` at line 125 dead
  code. The fallback at line 127 is the only branch that can fire, and it provides
  a generic message that doesn't add information beyond the pre-flight output.

  The Phase 7 fix correctly eliminated a duplicate network call (the old `checks2`),
  but the remaining two-branch `IsForbidden` handler is now a complexity artifact:
  the enriched branch (`rbacMsg != ""`) can never execute because pre-flight already
  blocked on that condition.

- **Suggestion:** Collapse `cmd/shell.go:122-128` to a single case:

  ```go
  if errors.IsForbidden(err) {
      return fmt.Errorf("error: forbidden creating ephemeral container in pod %q — check your RBAC permissions", podName)
  }
  ```

  If richer RBAC context on `CreateEphemeralContainer` failure is desired in future,
  it should come from the error itself (e.g., include `err.Error()` in the message),
  not from re-consulting the pre-flight result.

- **Impact:** 4 lines removed, one dead branch eliminated, logic flow easier to follow.

---

## Low Priority

### `warningCount` loop is a second pass over `report.Nodes` when `EXHAUSTED` count is already accumulated inline

- **Type:** Refactor
- **Locations:** `cmd/eks/node.go:267-273`
- **Description:** `ExhaustedNodes` is accumulated during the `utils` loop at line
  159-160 in a single pass. `warningCount` is then computed in a separate loop
  at lines 268-273 over the final (potentially filtered) `report.Nodes`. The two
  loops are structurally parallel and are adjacent, but only the `warningCount` loop
  runs on the post-filter slice. This is intentional (filter may have narrowed the
  set) but asymmetric — `ExhaustedNodes` in the summary may not match the displayed
  nodes if `--status` filtering is active.

  This is low priority because the current asymmetry is a pre-existing design choice
  and not introduced by Phase 7. The Phase 7 fix (error propagation on `WriteString`)
  brought attention to this block but did not change its logic.

- **Suggestion:** Either accumulate `warningCount` in the same `utils` loop as
  `ExhaustedNodes` (if the pre-filter count is what the summary line should reflect),
  or add a comment explaining that the trailing summary reflects the post-filter view.
  No extraction needed; this is a two-line addition or a comment.

- **Impact:** Clarifies intent; prevents future confusion when `--status` filtering
  produces a summary line that contradicts the `ExhaustedNodes` field in the JSON
  report.

### RBAC pre-flight pattern repeated across `cmd/shell.go` and `cmd/capture.go` with minor message variation

- **Type:** Consolidate (note only — Rule of Three not yet met)
- **Locations:** `cmd/shell.go:97-103`, `cmd/capture.go:99-104`
- **Description:** Both `runPodShell` and `runCapture` call
  `k8s.CheckEphemeralContainerRBAC` followed immediately by `k8s.FormatRBACError`
  with an early return. The message strings differ slightly ("insufficient permissions
  to use ephemeral containers" vs. "insufficient permissions"). `pkg/k8s/ephemeral.go`
  already encapsulates this pattern inside `RunInEphemeralContainer` (lines 136-142)
  for commands that use exec-mode ephemeral containers, but `shell.go` and
  `capture.go` are excluded by design (they use attach, not exec).

  Two occurrences do not meet the Rule of Three for extraction. Flagged here so
  a third caller would trigger consolidation — e.g., by adding a
  `k8s.RunRBACPreFlight(ctx, client, namespace)` helper that both call.

- **Impact:** No action needed now. Re-evaluate at three callers.

---

## Summary

- **Duplication found:** 1 instance (dead branch in `IsForbidden` handler, `cmd/shell.go:122-128`)
- **Dead code found:** 1 unreachable branch (the enriched RBAC message path at `cmd/shell.go:124-126`)
- **Complexity hotspots:** 0 functions exceeding thresholds
- **AI bloat patterns:** 0 new instances (the existing verbose `IsVerbose()` logging
  pattern pre-dates Phase 7 and is consistent throughout)
- **Estimated cleanup impact:** ~4 lines removable, 1 dead branch eliminable

## Recommendation

Phase 7 changes are clean, targeted, and correct. The three fixes each address a
real defect with minimal surface area. The medium-priority finding (dead `IsForbidden`
branch) is a small logical artifact left by the duplicate-removal fix — worth cleaning
up before the next phase but not a blocker for shipping. The two low-priority findings
are clarifications, not defects.

**Simplification is recommended but non-blocking.** Address the medium-priority
finding (`cmd/shell.go:122-128`) in the next available task slot.
