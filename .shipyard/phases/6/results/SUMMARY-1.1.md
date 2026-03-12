# SUMMARY-1.1: Add Concurrency to ComputeNodeUtilization

## Status: Complete

All 3 tasks executed sequentially, verified, and committed. All verification
commands exit 0 with no data races detected.

---

## Task 1: Add concurrency parameter and goroutine pool

**File modified:** `pkg/aws/eni.go`

- Added `"sync"` to imports.
- Updated `ComputeNodeUtilization` signature to accept `concurrency int` as
  the fifth parameter.
- Added `concurrency <= 0` guard (falls back to 1).
- Replaced serial `for _, node := range nodes` loop with a bounded goroutine
  pool: semaphore (`make(chan struct{}, concurrency)`), `sync.WaitGroup`, and
  `sync.Mutex` protecting the shared `utils` and `skipped` slices.
- Loop variable capture (`node := node`) applied for goroutine safety.
- Updated Godoc to document `concurrency` parameter and non-deterministic
  result ordering.

**Commit:** `f312733` — `shipyard(phase-6): add concurrency parameter and goroutine pool to ComputeNodeUtilization`

---

## Task 2: Update callers and existing tests

**Files modified:** `cmd/eks/node.go`, `cmd/eks/cni.go`, `cmd/diagnose.go`, `pkg/aws/eni_test.go`

- `cmd/eks/node.go:142` — added `10` as final argument.
- `cmd/eks/cni.go:150` — added `10` as final argument.
- `cmd/diagnose.go:348` — added `10` as final argument.
- All 7 `TestComputeNodeUtilization_*` test calls updated to pass `0`
  (serial fallback) for deterministic test execution.

**Commit:** `1e86bae` — `shipyard(phase-6): update callers to pass concurrency=10 and tests to pass concurrency=0`

---

## Task 3: Add concurrency-specific tests

**File modified:** `pkg/aws/eni_test.go`

Two new tests added at the end of the file:

1. `TestComputeNodeUtilization_ConcurrentMultiNode` — 5 nodes, `concurrency=3`.
   Verifies all 5 nodes appear in `utils` using order-independent map lookup.

2. `TestComputeNodeUtilization_ConcurrentPartialError` — 3 nodes, `concurrency=3`.
   Mock dispatches via `params.Filters[0].Values[0]` (instance ID): returns
   `node2Err` for `"i-002"`, succeeds for `"i-001"` and `"i-003"`. Asserts
   `len(utils)==2`, `len(skipped)==1`, and `skipped[0].NodeName=="node-2"`.

**Commit:** `9cb27e3` — `shipyard(phase-6): add concurrency-specific tests for ComputeNodeUtilization`

---

## Verification Results

```
go build ./...        exit 0
go test ./...         exit 0 (all packages pass)
go test -race ./pkg/aws/...  exit 0 (no data races)
go vet ./...          exit 0
```

## Deviations

None. Implementation followed the plan exactly.

## Files Changed

- `/Users/lgbarn/Personal/kdiag/pkg/aws/eni.go`
- `/Users/lgbarn/Personal/kdiag/pkg/aws/eni_test.go`
- `/Users/lgbarn/Personal/kdiag/cmd/eks/node.go`
- `/Users/lgbarn/Personal/kdiag/cmd/eks/cni.go`
- `/Users/lgbarn/Personal/kdiag/cmd/diagnose.go`
