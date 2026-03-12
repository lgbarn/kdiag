# Simplification Report
**Phase:** 5 — ENI deduplication, error handling fixes, IPv6 support
**Date:** 2026-03-11
**Files analyzed:** 8 (pkg/aws/eni.go, pkg/aws/eni_test.go, pkg/aws/endpoint.go,
pkg/aws/endpoint_test.go, cmd/eks/node.go, cmd/eks/cni.go, cmd/diagnose.go,
cmd/ingress.go, cmd/eks/endpoint.go)
**Findings:** 2 medium, 2 low

---

## High Priority

None. The primary goal of this phase — eliminating three copies of the inline
ENI utilization loop — was executed cleanly. The shared `ComputeNodeUtilization`
function is well-scoped and the callers are correct. No high-priority issues found.

---

## Medium Priority

### `uniqueKeys` is now dead code

- **Type:** Remove
- **Locations:** `cmd/eks/eks.go:179-186`
- **Description:** `uniqueKeys` converts a `map[string]struct{}` to a `[]string`.
  It was called in the pre-phase-5 versions of both `runNode` and `runCNI` to
  extract unique instance types before calling `GetInstanceTypeLimits` directly.
  The refactor to `ComputeNodeUtilization` eliminated both call sites. No other
  caller exists in the codebase.
- **Suggestion:** Delete the `uniqueKeys` function from `cmd/eks/eks.go`. The
  function is 7 lines. Removing it reduces the file and eliminates a maintenance
  surface that will confuse future readers who search for its callers.
- **Impact:** 8 lines removed (including comment), zero callers affected.

---

### `NodeCapacity` duplicates the shape of `NodeUtilization` without a clear reason

- **Type:** Refactor (defer if struct is part of a stable JSON API)
- **Locations:** `cmd/eks/cni.go:46-56`, `pkg/aws/eni.go:131-141`
- **Description:** `NodeCapacity` (the CNI report's per-node struct) and
  `NodeUtilization` (the shared output type) carry almost identical fields:
  `NodeName`, `InstanceType`, `MaxENIs`, `MaxIPsPerENI`, `MaxTotalIPs`,
  `CurrentENIs`, `CurrentIPs`. The mapping loop in `runCNI` (lines 166-182)
  copies each field one-for-one.

  The only structural differences are:
  - `NodeCapacity.Utilization` is `string` (formatted percentage);
    `NodeUtilization.UtilizationPct` is `int`.
  - `NodeCapacity.Exhausted` is `bool`; `NodeUtilization.Status` is `string`.
  - `NodeCapacity` has no `Status` string field — the table renderer derives it
    from `Exhausted` (line 246-249 in `printCNITable`).

  These differences are real but thin. The result is a manual struct-copy loop
  that will silently drift if a field is added to `NodeUtilization` in the future.

- **Suggestion:** Two options, in ascending order of change:
  1. **Minimal (low risk):** Keep both structs but extract the field-copy into a
     named constructor function `nodeCapacityFromUtilization(u awspkg.NodeUtilization) NodeCapacity`
     in `cni.go`. This makes the mapping explicit and testable without changing
     the JSON surface.
  2. **Structural (higher benefit):** Embed `awspkg.NodeUtilization` in `NodeCapacity`
     and store `Exhausted bool` alongside it. Eliminates the copy loop entirely.
     Requires the JSON output shape of `kdiag eks cni` to remain acceptable after
     restructuring — verify against any consumers before applying.

  If `NodeCapacity` is considered a stable external JSON API, defer option 2 and
  apply option 1 only.

- **Impact (option 1):** +5 lines for constructor, -16 lines in `runCNI` loop →
  net -11 lines; drift risk eliminated.

---

## Low Priority

- **`NodeCapacity` drops the WARNING tier.** `cmd/eks/cni.go:167` classifies nodes
  as `Exhausted bool` (EXHAUSTED only), discarding the WARNING tier that
  `ComputeNodeUtilization` returns in `Status`. This is intentional per the
  pre-phase-5 CNI behavior, but it creates an invisible discrepancy: a node at 75%
  utilization is silently treated as OK in the CNI report but WARNING in the node
  report. Consider whether the CNI table should surface WARNING nodes distinctly
  from OK nodes, or at minimum add a comment at `cni.go:167` documenting the
  deliberate omission so future maintainers do not assume the WARNING case is a
  bug. No code removal needed — this is a documentation gap.
  **Location:** `cmd/eks/cni.go:167`

- **`[kdiag] warning: skipped node` log line is a near-duplicate across three call
  sites.** The identical `fmt.Fprintf(os.Stderr, "[kdiag] warning: skipped node %s: %s\n", ...)`
  pattern appears at `cmd/diagnose.go:355`, `cmd/eks/node.go:153`, and
  `cmd/eks/cni.go:161`. The Rule of Three applies, but the pattern is a single
  `fmt.Fprintf` call — extraction into a helper function would save ~0 net lines
  per site after accounting for the call. Flag for awareness only; the cost of
  extraction likely exceeds the benefit unless more skipped-node logging is added
  in future phases.

---

## Summary

- **Duplication found:** 1 instance (skipped-node log line, 3 sites) — below
  extraction threshold given single-line nature
- **Dead code found:** 1 function (`uniqueKeys`, 8 lines) — straightforward removal
- **Complexity hotspots:** 0 functions exceeding thresholds in changed code
- **AI bloat patterns:** 0 instances — error handling is proportionate and
  purposeful; no redundant re-wrapping or defensive type checks observed
- **Unnecessary abstractions:** 0 — `ComputeNodeUtilization` has 3 callers,
  `NodeInput`/`ENISkippedNode`/`NodeUtilization` types are all in active use
- **Estimated cleanup impact:** 8 lines removable immediately (`uniqueKeys`);
  ~11 lines reducible with constructor extraction for `NodeCapacity` mapping

## Recommendation

**Simplification is recommended before the next phase, specifically for `uniqueKeys`.**
This is a mechanical, zero-risk removal (dead function, no callers) that should be
cleaned up rather than accumulated. The `NodeCapacity` mapping finding is worth
addressing if the CNI JSON surface is not considered stable API, but is safe to
defer. All other findings are informational.

The phase 5 refactor itself is well-executed: the extraction of `ComputeNodeUtilization`
is the right abstraction at the right layer, the test coverage for the new function
is thorough (7 test functions covering all status thresholds, prefix delegation,
per-node errors, limits errors, and empty input), and the error propagation fixes
are targeted and correct.
