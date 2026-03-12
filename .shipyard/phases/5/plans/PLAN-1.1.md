---
phase: phase-5
plan: "1.1"
wave: 1
dependencies: []
must_haves:
  - NodeUtilization struct defined in pkg/aws/eni.go with NodeName, InstanceType, CurrentENIs, CurrentIPs, MaxENIs, MaxIPsPerENI, MaxTotalIPs, UtilizationPct, Status fields
  - ComputeNodeUtilization exported function in pkg/aws/eni.go accepting ctx, EC2API, []EligibleNode, prefixDelegation bool
  - ComputeNodeUtilization returns ([]NodeUtilization, []SkippedNode, error) — limits batch query error is terminal; per-node ENI errors produce SkippedNode entries
  - Status values are "OK", "WARNING", "EXHAUSTED" using >=70% and >=85% thresholds
  - SkippedNode reuses the existing type from cmd/eks/ — either accept it as a parameter type or define a parallel aws-package type; see file-conflict note below
  - Unit tests cover: prefix-delegation x16 multiplier, WARNING threshold, EXHAUSTED threshold, ENI query failure producing SkippedNode, limits batch failure returning error, zero-node input
files_touched:
  - pkg/aws/eni.go
  - pkg/aws/eni_test.go
tdd: true
---

## Context

Three call sites duplicate ENI utilization arithmetic: `cmd/eks/node.go:138-196`,
`cmd/eks/cni.go:156-209`, and `cmd/diagnose.go:327-366`. The differences are:

| Dimension | node.go | cni.go | diagnose.go |
|---|---|---|---|
| Prefix-delegation x16 | No | Yes | No |
| WARNING (>=70%) | Yes | No | No |
| Per-node ENI error handling | Skip to Skipped list + verbose stderr | Skip to Skipped list + verbose stderr | Silent `continue` (bug) |
| Output granularity | Per-node struct | Per-node struct | Count only |

The shared function must preserve all correct behaviors (WARNING threshold, verbose stderr on
skip, Skipped list) and fix the silent-continue deviation in `diagnose.go`.

## EligibleNode parameter type

The three callers all use `eks.ClassifyNodes` to produce a slice of eligible nodes before
entering the per-node loop. That function returns `[]EligibleNode` (defined in
`cmd/eks/classify.go`). The shared function in `pkg/aws/` cannot import `cmd/eks` (would
create a cycle). The architect's decision: define a lightweight input struct in `pkg/aws/eni.go`
named `NodeInput` with fields `Name string`, `InstanceID string`, `InstanceType string`. Each
caller converts its `[]EligibleNode` slice into `[]NodeInput` before calling
`ComputeNodeUtilization`. This keeps `pkg/aws` free of cmd-layer dependencies.

The `SkippedNode` type is currently defined in `cmd/eks/classify.go`. Because `pkg/aws` cannot
import `cmd/eks`, define a parallel `ENISkippedNode` struct (with `NodeName string` and
`Reason string`) inside `pkg/aws/eni.go`. Each caller maps the returned `[]ENISkippedNode`
into its own `[]SkippedNode` representation.

## NodeUtilization struct design

```
NodeUtilization {
    NodeName       string
    InstanceType   string
    MaxENIs        int32
    MaxIPsPerENI   int32
    CurrentENIs    int
    CurrentIPs     int
    MaxTotalIPs    int
    UtilizationPct int
    Status         string  // "OK" | "WARNING" | "EXHAUSTED"
}
```

Status derivation: if `UtilizationPct >= 85` → "EXHAUSTED"; else if `>= 70` → "WARNING";
else "OK". This is the superset of all three callers. `cni.go` and `diagnose.go` project only
the fields they need from this struct.

## Function signature

```go
// ComputeNodeUtilization queries ENI usage for each node and computes IP utilization.
// prefixDelegation, when true, multiplies the maximum IP capacity by 16 to account
// for VPC CNI prefix delegation mode. A batch failure fetching instance type limits
// is returned as a terminal error. Per-node ENI query failures are non-fatal: the
// node is appended to the returned skipped list and processing continues.
func ComputeNodeUtilization(
    ctx context.Context,
    api EC2API,
    nodes []NodeInput,
    prefixDelegation bool,
) ([]NodeUtilization, []ENISkippedNode, error)
```

## Implementation notes

- Call `GetInstanceTypeLimits` once for all unique instance types (batch call).
- Per-node: call `ListNodeENIs`. On error: append `ENISkippedNode{NodeName, reason}`,
  `continue`. No silent swallowing — the caller decides whether to log to stderr. The
  function itself does not call `fmt.Fprintf(os.Stderr, ...)` because `pkg/aws` has no
  access to `isVerbose()`. Callers are responsible for logging skips (they already do this
  in the existing code).
- Guard against `maxTotalIPs == 0` before dividing (existing code already does this).
- Use `make([]NodeUtilization, 0, len(nodes))` for capacity hint.
- Godoc on `NodeInput`, `NodeUtilization`, `ENISkippedNode`, and `ComputeNodeUtilization`.

## Test cases required (write tests first — TDD)

Use the existing `mockEC2API` in `pkg/aws/ec2iface_mock_test.go`.
Follow the `// -------- TestFunctionName` banner style already established in `eni_test.go`.

| Test name | Setup | Assert |
|---|---|---|
| `TestComputeNodeUtilization_OK` | 1 node, 2 ENIs, 5 IPs used, maxENIs=3, maxIPsPerENI=10 (30 max, 16%) | Status="OK", UtilizationPct=16, no skipped, no error |
| `TestComputeNodeUtilization_Warning` | 1 node, IPs=21, max=30 (70%) | Status="WARNING" |
| `TestComputeNodeUtilization_Exhausted` | 1 node, IPs=26, max=30 (86%) | Status="EXHAUSTED" |
| `TestComputeNodeUtilization_PrefixDelegation` | prefixDelegation=true, maxENIs=3, maxIPsPerENI=10 → max=480; IPs=26 (5%) | Status="OK", MaxTotalIPs=480 |
| `TestComputeNodeUtilization_ENIQueryError` | ListNodeENIs returns error for node-1 | len(util)=0, len(skipped)=1, skipped[0].NodeName="node-1", no terminal error |
| `TestComputeNodeUtilization_LimitsError` | GetInstanceTypeLimits returns error | error non-nil, util=nil |
| `TestComputeNodeUtilization_EmptyInput` | nodes=nil | returns empty slices, no error |

---

```xml
<task id="1" files="pkg/aws/eni_test.go" tdd="true">
  <action>
    Add a new test section below the existing GetInstanceTypeLimits tests following the
    `// -------- TestComputeNodeUtilization` banner style. Write all seven test cases
    listed above using the existing mockEC2API. Each test sets up mock responses for
    describeNetworkInterfaces and describeInstanceTypes, calls ComputeNodeUtilization,
    and asserts the expected NodeUtilization slice, ENISkippedNode slice, and error
    return value. These tests will not compile until Task 2 adds the implementation.
  </action>
  <verify>cd /Users/lgbarn/Personal/kdiag && go test ./pkg/aws/... 2>&1 | grep -E "^(ok|FAIL|---)" | head -20</verify>
  <done>Tests compile (after Task 2). All seven TestComputeNodeUtilization_* cases pass.
  Existing TestListNodeENIs_* and TestGetInstanceTypeLimits_* tests continue to pass.</done>
</task>

<task id="2" files="pkg/aws/eni.go" tdd="true">
  <action>
    Add three new exported types and one exported function to pkg/aws/eni.go, appended
    after GetInstanceTypeLimits.

    1. NodeInput struct: Name, InstanceID, InstanceType string fields. Godoc required.
    2. ENISkippedNode struct: NodeName, Reason string fields. Godoc required.
    3. NodeUtilization struct: NodeName, InstanceType string; MaxENIs, MaxIPsPerENI int32;
       CurrentENIs, CurrentIPs, MaxTotalIPs, UtilizationPct int; Status string. Godoc required.
       JSON struct tags using snake_case, omitempty on optional fields only.
    4. ComputeNodeUtilization function: batch-call GetInstanceTypeLimits for unique instance
       types; iterate nodes calling ListNodeENIs; on per-node error append to skipped and
       continue; compute UtilizationPct = (CurrentIPs * 100) / MaxTotalIPs; apply
       prefixDelegation x16 multiplier to MaxTotalIPs before dividing; derive Status using
       >=85 EXHAUSTED, >=70 WARNING, else OK; guard maxTotalIPs==0; return
       ([]NodeUtilization, []ENISkippedNode, error). Full Godoc comment required.
  </action>
  <verify>cd /Users/lgbarn/Personal/kdiag && go build ./pkg/aws/... && go test ./pkg/aws/...</verify>
  <done>go build exits 0. go test exits 0 with all TestComputeNodeUtilization_* and all
  pre-existing pkg/aws tests passing. No new exported symbols are missing Godoc (run
  `go vet ./pkg/aws/...` and confirm zero warnings).</done>
</task>
```
