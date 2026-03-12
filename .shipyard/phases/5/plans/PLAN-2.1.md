---
phase: phase-5
plan: "2.1"
wave: 2
dependencies: ["1.1"]
must_haves:
  - cmd/eks/node.go runNode replaces its per-node ENI loop with ComputeNodeUtilization
  - cmd/eks/cni.go runCNI replaces its per-node ENI loop with ComputeNodeUtilization (prefixDelegation=cniConfig.PrefixDelegation)
  - cmd/diagnose.go countExhaustedNodes replaced by a direct call to ComputeNodeUtilization (prefixDelegation=false); per-node ENI errors are no longer silently swallowed
  - All three callers convert their EligibleNode slice to []awspkg.NodeInput before calling ComputeNodeUtilization
  - All three callers map returned []awspkg.ENISkippedNode back to their own SkippedNode representation
  - node.go and cni.go verbose stderr warnings for skipped nodes remain (log after receiving []ENISkippedNode)
  - diagnose.go logs skipped nodes to stderr when isVerbose()
  - go build ./... passes; go test ./... passes; no functional behavior change observable in output JSON shape
files_touched:
  - cmd/eks/node.go
  - cmd/eks/cni.go
  - cmd/diagnose.go
tdd: false
---

## Context

Plan 1.1 adds `ComputeNodeUtilization` to `pkg/aws/eni.go`. This plan wires all three
existing callers to use it, deleting the duplicated arithmetic in each. This plan can only
start after Plan 1.1 is merged because it imports the new function.

Plan 1.2 is independent (different files); these two wave-2 changes could be done
concurrently if Plan 1.1 is the only blocker. In practice the developer should complete
Plan 1.1 first, then this plan is the only remaining wave-2 work.

## Caller mapping details

### cmd/eks/node.go

Before (lines 138-196): calls `ListNodeENIs` and `GetInstanceTypeLimits` directly, computes
utilization inline, appends to `report.Nodes` and `report.Skipped`.

After:
1. Remove the per-node loop (lines 138-197) and the preceding `GetInstanceTypeLimits` call
   (lines 130-136).
2. Convert `eligible []EligibleNode` to `[]awspkg.NodeInput`:
   ```go
   nodeInputs := make([]awspkg.NodeInput, 0, len(eligible))
   for _, en := range eligible {
       nodeInputs = append(nodeInputs, awspkg.NodeInput{
           Name:         en.Name,
           InstanceID:   en.InstanceID,
           InstanceType: en.InstanceType,
       })
   }
   ```
3. Call `ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false)`. If the terminal error
   is non-nil, return it wrapped: `fmt.Errorf("failed to compute node utilization: %w", err)`.
4. Iterate `utils []awspkg.NodeUtilization`, building `NodeENIStatus` from each entry. Map
   fields directly (all field names correspond). Increment `report.Summary.ExhaustedNodes`
   when `u.Status == "EXHAUSTED"` (same threshold as before).
5. For each `skipped awspkg.ENISkippedNode`, print the verbose stderr warning and append to
   `report.Skipped` as `SkippedNode{NodeName: s.NodeName, Reason: s.Reason}`.

Output JSON shape must be identical. The `NodeENIStatus` struct is unchanged.

### cmd/eks/cni.go

Before (lines 144-209): same pattern as node.go but with `prefixDelegation` multiplier and
`NodeCapacity` output type.

After:
1. Remove the per-node loop (lines 152-209) and the preceding `GetInstanceTypeLimits` call
   (lines 144-150).
2. Build `[]awspkg.NodeInput` from `eligible` (same conversion as node.go).
3. Call `ComputeNodeUtilization(ctx, ec2Client, nodeInputs, prefixDelegation)`.
4. Iterate `utils`, building `NodeCapacity` from each entry:
   - `Exhausted = u.Status == "EXHAUSTED"` (cni.go only tracks boolean, no WARNING)
   - If exhausted, append `en.Name` to `ipExhausted` — use `u.NodeName`.
5. Map `[]awspkg.ENISkippedNode` to `skipped = append(skipped, SkippedNode{...})` after
   appending verbose stderr warning.

The `NodeCapacity.Exhausted bool` field semantics are unchanged: it is true only at EXHAUSTED,
not at WARNING. This is a deliberate caller-level projection, not a bug.

### cmd/diagnose.go — countExhaustedNodes

Before (lines 326-366): returns `(int, error)`. Calls `GetInstanceTypeLimits` then iterates
nodes calling `ListNodeENIs`, silently continuing on error.

After: replace the entire function body with a call to `ComputeNodeUtilization`:
1. Build `[]awspkg.NodeInput` from `eligible` (from `eks.ClassifyNodes(nodes)`).
2. Call `ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false)`. Terminal error → return
   `0, fmt.Errorf("compute node utilization: %w", err)`.
3. Count EXHAUSTED nodes: `for _, u := range utils { if u.Status == "EXHAUSTED" { exhausted++ } }`.
4. Log skipped nodes to stderr when `IsVerbose()`:
   `fmt.Fprintf(os.Stderr, "[kdiag] warning: skipped node %s: %s\n", s.NodeName, s.Reason)`.
   This fixes the silent-continue deviation documented in RESEARCH.md.
5. Return `exhausted, nil`.

The function signature `(ctx, ec2Client, []corev1.Node) (int, error)` is unchanged — only
the body changes. The `runDiagnose` caller at line 210 requires no changes.

## What does NOT change

- `NodeReport`, `NodeENIStatus`, `NodeCapacity`, `CNIReport`, `SkippedNode` struct definitions
- JSON field names in output
- The `--status` filter logic in node.go (applied after the utilization loop; unchanged)
- The `--show-pods` logic in node.go (appended after; unchanged)
- `ingressSeverity`, `cniSeverity`, and all severity functions in diagnose_types.go

---

```xml
<task id="1" files="cmd/eks/node.go, cmd/eks/cni.go, cmd/diagnose.go" tdd="false">
  <action>
    Refactor all three ENI utilization callers to use ComputeNodeUtilization from pkg/aws/eni.go.

    In cmd/eks/node.go:
    - Delete the GetInstanceTypeLimits call and the per-node ENI loop (lines 130-197).
    - Build []awspkg.NodeInput from eligible using a make+append loop.
    - Call awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false).
    - On terminal error, return wrapped error.
    - Iterate utils to build NodeENIStatus entries; increment ExhaustedNodes counter when
      u.Status == "EXHAUSTED".
    - Iterate skipped to emit verbose stderr warning and append SkippedNode to report.Skipped.

    In cmd/eks/cni.go:
    - Delete the GetInstanceTypeLimits call and the per-node ENI loop (lines 144-209).
    - Build []awspkg.NodeInput from eligible.
    - Call awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, prefixDelegation).
    - On terminal error, return wrapped error.
    - Iterate utils to build NodeCapacity entries; set Exhausted = u.Status == "EXHAUSTED";
      append to ipExhausted when exhausted.
    - Iterate skipped to emit verbose stderr warning and append SkippedNode.

    In cmd/diagnose.go:
    - Replace the body of countExhaustedNodes with:
      build NodeInput slice from eligible (obtained via eks.ClassifyNodes), call
      ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false), count EXHAUSTED nodes,
      log skipped nodes to stderr when IsVerbose(), return count.
    - The function signature (ctx, ec2Client, []corev1.Node) (int, error) stays the same.
  </action>
  <verify>cd /Users/lgbarn/Personal/kdiag && go build ./... && go test ./...</verify>
  <done>go build ./... exits 0. go test ./... exits 0 with all tests passing. Confirm
  the deleted code paths are gone:
    grep -n "GetInstanceTypeLimits\|ListNodeENIs" cmd/eks/node.go cmd/eks/cni.go cmd/diagnose.go
  should produce zero matches (these calls now live only inside pkg/aws/eni.go).</done>
</task>
```
