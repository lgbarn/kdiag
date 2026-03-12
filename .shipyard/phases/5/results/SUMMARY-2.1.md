# SUMMARY-2.1: Wire Callers to Shared ComputeNodeUtilization

**Plan:** PLAN-2.1
**Date:** 2026-03-11
**Branch:** main
**Commit:** 6201560

## What Was Done

### Task 1: Refactor all three ENI utilization callers

Three files were modified to replace their inline ENI utilization logic with calls to `awspkg.ComputeNodeUtilization`.

#### cmd/eks/node.go (`runNode`)

- Removed: `uniqueTypes` map construction, `uniqueKeys` call, `awspkg.GetInstanceTypeLimits` call, per-node `awspkg.ListNodeENIs` loop, and inline utilization/status computation.
- Added: construction of `[]awspkg.NodeInput` from `eligible`, single call to `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false)`.
- Added: a `eligibleByName` map (keyed by node name) to recover `ComputeType` and `Note` fields from `EligibleNode` — these fields are not present in `NodeUtilization` and must be preserved for the `NodeENIStatus` struct.
- `NodeENIStatus.Utilization` is set using `strconv.Itoa(u.UtilizationPct)` matching the prior format.
- Skipped nodes from `ComputeNodeUtilization` are logged via verbose stderr and appended to `report.Skipped`.
- `report.Summary.ExhaustedNodes` is incremented when `u.Status == "EXHAUSTED"`.

#### cmd/eks/cni.go (`runCNI`)

- Removed: `uniqueTypes` map, `uniqueKeys` call, `awspkg.GetInstanceTypeLimits` call, per-node `awspkg.ListNodeENIs` loop, and inline utilization/status computation.
- Added: `[]awspkg.NodeInput` construction, call to `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, prefixDelegation)`.
- `NodeCapacity.Exhausted` is set as `u.Status == "EXHAUSTED"` (boolean, no WARNING tier here).
- Exhausted node names are appended to `ipExhausted`.
- `NodeCapacity.Utilization` uses `strconv.Itoa(u.UtilizationPct)` matching the prior format.
- Skipped nodes from classify step (renamed to `classSkipped`) and from `ComputeNodeUtilization` are merged into a unified `skipped` slice with verbose stderr logging.

#### cmd/diagnose.go (`countExhaustedNodes`)

- Removed: entire inline implementation (unique-types extraction, `GetInstanceTypeLimits`, per-node `ListNodeENIs` with silent-continue on error).
- Added: `[]awspkg.NodeInput` construction, call to `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false)`.
- Terminal error wrapped as `fmt.Errorf("compute node utilization: %w", err)`.
- Skipped nodes now logged to stderr when `IsVerbose()` — this fixes the prior silent-continue deviation.
- EXHAUSTED count computed from returned `utils` slice.
- Function signature unchanged: `(ctx, ec2Client, []corev1.Node) (int, error)`.

## Deviations from Plan

None. The plan was followed exactly. One implementation detail worth noting: `NodeENIStatus` has `ComputeType` and `Note` fields that are not present in `NodeUtilization`. These were preserved by building a `map[string]EligibleNode` lookup keyed by node name before calling `ComputeNodeUtilization`, then reading back `en.ComputeType` and `en.Note` when iterating the results. This approach was required by the plan's constraint that struct definitions must not change.

## Verification Results

| Check | Result |
|---|---|
| `go build ./...` | PASS (no output) |
| `go test ./...` | PASS (all packages) |
| `grep GetInstanceTypeLimits\|ListNodeENIs cmd/eks/node.go cmd/eks/cni.go cmd/diagnose.go` | PASS (zero matches) |
| `go vet ./...` | PASS (zero warnings) |

## Files Modified

- `/Users/lgbarn/Personal/kdiag/cmd/eks/node.go`
- `/Users/lgbarn/Personal/kdiag/cmd/eks/cni.go`
- `/Users/lgbarn/Personal/kdiag/cmd/diagnose.go`

## Net Change

75 insertions, 135 deletions across the three files (net -60 lines). All duplicate ENI utilization logic is now consolidated in `pkg/aws/eni.go:ComputeNodeUtilization`.
