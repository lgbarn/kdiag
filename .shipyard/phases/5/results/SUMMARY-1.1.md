# SUMMARY-1.1: Extract ComputeNodeUtilization

## Status: COMPLETE

## What was done

### Task 1 — Tests (TDD red phase)
Added 7 test cases to `pkg/aws/eni_test.go` under the `// TestComputeNodeUtilization` banner:

| Test | Scenario | Verified |
|---|---|---|
| `TestComputeNodeUtilization_OK` | 2 ENIs, 5 IPs, max=30 → pct=16, OK | ✓ |
| `TestComputeNodeUtilization_Warning` | 21 IPs, max=30 → pct=70, WARNING | ✓ |
| `TestComputeNodeUtilization_Exhausted` | 26 IPs, max=30 → pct=86, EXHAUSTED | ✓ |
| `TestComputeNodeUtilization_PrefixDelegation` | prefixDelegation=true → max=480, pct=5, OK | ✓ |
| `TestComputeNodeUtilization_ENIQueryError` | per-node ENI error → skipped, no terminal error | ✓ |
| `TestComputeNodeUtilization_LimitsError` | GetInstanceTypeLimits error → terminal error, nil utils | ✓ |
| `TestComputeNodeUtilization_EmptyInput` | nodes=nil → empty slices, no error | ✓ |

Two private test helpers added: `m5LargeMock()` and `fixedIPsMock(n)` to reduce boilerplate across similar tests.

### Task 2 — Implementation
Added to `pkg/aws/eni.go`:
- **`NodeInput`** struct: `Name`, `InstanceID`, `InstanceType` — describes a node for evaluation
- **`ENISkippedNode`** struct: `NodeName`, `Reason` — records per-node errors
- **`NodeUtilization`** struct: full utilization snapshot with JSON snake_case tags
- **`ComputeNodeUtilization`** function: batch-fetches limits once, iterates nodes calling `ListNodeENIs`, skips per-node errors, applies 16× multiplier for prefix delegation, classifies EXHAUSTED/WARNING/OK

## Verification results

```
go build ./pkg/aws/...   → PASS
go test ./pkg/aws/...    → ok (0.245s)
go vet ./pkg/aws/...     → PASS (zero warnings)
```

## Deviations from plan
None. Implementation matches spec exactly.

## Commit
`26e6f4b shipyard(phase-5): add ComputeNodeUtilization with NodeInput/ENISkippedNode/NodeUtilization types`
