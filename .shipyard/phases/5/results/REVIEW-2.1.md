# Review: Plan 2.1

## Verdict: PASS

---

## Stage 1: Spec Compliance

### Task 1: Refactor all three ENI utilization callers to use ComputeNodeUtilization

**Status: PASS**

**Evidence and per-criterion checks:**

**Criterion: All three callers convert `EligibleNode` to `[]awspkg.NodeInput` before calling**

- `cmd/eks/node.go` lines 133-140: `make([]awspkg.NodeInput, 0, len(eligible))` with `append` loop mapping `en.Name`, `en.InstanceID`, `en.InstanceType`. Correct.
- `cmd/eks/cni.go` lines 141-148: identical pattern. Correct.
- `cmd/diagnose.go` lines 339-346: identical pattern inside `countExhaustedNodes`. Correct.

**Criterion: All three callers use `ComputeNodeUtilization` instead of direct `ListNodeENIs`/`GetInstanceTypeLimits`**

- `grep -n "GetInstanceTypeLimits\|ListNodeENIs" cmd/eks/node.go cmd/eks/cni.go cmd/diagnose.go` returns zero matches. Confirmed.
- `node.go` line 142: `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false)`. Correct.
- `cni.go` line 150: `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, prefixDelegation)`. Correct. `prefixDelegation` sourced from `cniConfig.PrefixDelegation` at line 136.
- `diagnose.go` line 348: `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false)`. Correct.

**Criterion: `node.go` maps `NodeUtilization` → `NodeENIStatus`, increments ExhaustedNodes on EXHAUSTED**

- Lines 154-172: loop over `utils`, increments `report.Summary.ExhaustedNodes` when `u.Status == "EXHAUSTED"` (line 155-157). All seven fields of `NodeENIStatus` populated from `NodeUtilization` fields. `Utilization` set via `strconv.Itoa(u.UtilizationPct)` matching prior string format (field tag `json:"utilization_pct"`).
- `ComputeType` and `Note` recovered from `eligibleByName` map (lines 127-130) since `NodeUtilization` does not carry those fields. This is correct and preserves the JSON output shape.

**Criterion: `cni.go` maps `NodeUtilization` → `NodeCapacity`, `Exhausted = u.Status == "EXHAUSTED"`, passes `prefixDelegation`**

- Lines 166-182: `exhausted := u.Status == "EXHAUSTED"` (line 167); exhausted node name appended to `ipExhausted` (line 169). `NodeCapacity.Exhausted` set to `exhausted` (line 180). `Utilization` uses `strconv.Itoa(u.UtilizationPct)` (line 179). All fields correct.
- The WARNING tier from `ComputeNodeUtilization` is intentionally not surfaced as `Exhausted = true`, matching the plan's explicit note that this is a deliberate caller-level projection.

**Criterion: `diagnose.go` counts EXHAUSTED, logs skipped nodes when verbose (fixes silent-continue)**

- Lines 353-357: iterates `skipped`, logs to `os.Stderr` when `IsVerbose()`. Fixes the prior silent-continue deviation.
- Lines 359-364: counts EXHAUSTED nodes from returned `utils` slice.
- Function signature at line 336 is unchanged: `(ctx context.Context, ec2Client awspkg.EC2API, nodes []corev1.Node) (int, error)`. Caller at line 218 unchanged.

**Criterion: `node.go` and `cni.go` verbose stderr warnings for skipped nodes remain**

- `node.go` lines 147-152: iterates `nodeSkipped`, prints `[kdiag] warning: skipped node %s: %s` when `isVerbose()`, appends to `report.Skipped`. Correct.
- `cni.go` lines 159-164: same pattern for `nodeSkipped`; classify-phase skipped nodes (`classSkipped`) are merged into `skipped` at line 157 before the verbose loop. Both skipped populations end up in `report.Skipped`.

**Criterion: JSON output shape unchanged**

- `NodeENIStatus`, `NodeCapacity`, `SkippedNode`, `CNIReport`, `NodeReport` struct definitions untouched (confirmed by grepping `cmd/eks/eks.go` and struct declarations in `node.go`/`cni.go`). No field names or JSON tags altered.

**Verification commands:**

- `go build ./...`: exits 0 (confirmed by running).
- `go test ./...`: exits 0, all packages pass (confirmed by running).
- `grep GetInstanceTypeLimits\|ListNodeENIs cmd/eks/node.go cmd/eks/cni.go cmd/diagnose.go`: zero matches (confirmed by running, grep exit 1 = no matches found).

---

## Stage 2: Code Quality

### Critical

None.

### Minor

- **`node.go` line 144: error wrapping message inconsistency with plan spec.**
  The plan (task action step 3 and spec step 4) specifies the wrapping message as `"failed to compute node utilization: %w"`. The implementation uses `"compute node utilization: %w"` (line 144). The `cni.go` and `diagnose.go` callers also use the shorter form `"compute node utilization: %w"`. All three callers are internally consistent with each other, but they diverge from the plan's stated wrapping string. This is minor because the functional behavior is identical; only the error message text differs. The shorter form (`"compute node utilization: %w"`) is arguably better style since the `%w` chain already implies failure.
  - Remediation: Either update the plan to reflect the implemented message, or decide on a canonical form and apply it uniformly. No functional change needed.

- **`diagnose.go` `countExhaustedNodes` line 337: classify-skipped nodes are silently discarded.**
  `eligible, _ := eks.ClassifyNodes(nodes)` discards the classify-skipped nodes (the blank identifier). In `node.go` and `cni.go`, classify-skipped nodes are added to the report's `Skipped` slice so they appear in JSON output and the trailing summary. `countExhaustedNodes` has no output struct to attach them to (it only returns `(int, error)`), so silently discarding them is structurally forced. However, the plan states that "per-node ENI errors are no longer silently swallowed" and specifies that skipped nodes should be logged when `isVerbose()`. The classify-skipped nodes in `diagnose.go` are still silently discarded — even `IsVerbose()` does not print them. This is a narrow gap in the fix: the ENI-API-phase errors (from `ComputeNodeUtilization`) are now properly logged, but classification-phase errors (Fargate, missing providerID, etc.) from the `ClassifyNodes` call remain silent.
  - Remediation: Capture the second return value and log classify-skipped nodes under the same `IsVerbose()` guard already present at lines 353-357:
    ```go
    eligible, classSkipped := eks.ClassifyNodes(nodes)
    // ... existing nodeInputs build ...
    utils, skipped, err := awspkg.ComputeNodeUtilization(...)
    // ...
    for _, s := range classSkipped {
        if IsVerbose() {
            fmt.Fprintf(os.Stderr, "[kdiag] warning: skipped node %s: %s\n", s.NodeName, s.Reason)
        }
    }
    ```

### Positive

- The `eligibleByName` lookup map in `node.go` (lines 127-130) is a clean solution to preserving `ComputeType` and `Note` fields that `NodeUtilization` intentionally does not carry, without requiring any struct definition changes. The approach has O(1) lookup and adds no extra API calls.
- `cni.go` correctly merges `classSkipped` and `nodeSkipped` into a single `skipped` slice (line 157) before building the report, so the CNI output captures all skip reasons from both the classification phase and the ENI-fetch phase in `CNIReport.Skipped`.
- The `prefixDelegation` parameter is correctly threaded from `cniConfig.PrefixDelegation` (line 136) through to `ComputeNodeUtilization` (line 150), with no accidental hardcoded value.
- All three callers use `fmt.Errorf("...: %w", err)` for terminal errors rather than returning raw errors, preserving stack-unwrappable error chains.
- Net line delta of -60 lines confirms real deduplication, not just indirection.
