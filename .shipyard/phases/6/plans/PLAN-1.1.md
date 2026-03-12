# Plan 1.1: Add Concurrency to ComputeNodeUtilization

## Context
`ComputeNodeUtilization` in `pkg/aws/eni.go:148` currently calls `ListNodeENIs` serially for each node. On clusters with 50+ nodes this generates 50+ sequential `DescribeNetworkInterfaces` API calls, risking EC2 API throttling. This plan adds a bounded goroutine pool using a semaphore pattern (buffered channel) with `sync.WaitGroup`. The concurrency parameter is added to the existing function signature — callers pass `10` for production use.

## Dependencies
None (single wave).

## Tasks

### Task 1: Add concurrency parameter and goroutine pool to ComputeNodeUtilization
**Files:** `pkg/aws/eni.go`
**Action:** modify
**Description:**
1. Add `concurrency int` parameter to `ComputeNodeUtilization` signature:
   ```go
   func ComputeNodeUtilization(ctx context.Context, api EC2API, nodes []NodeInput, prefixDelegation bool, concurrency int) ([]NodeUtilization, []ENISkippedNode, error)
   ```
2. Add `"sync"` to imports.
3. After the `GetInstanceTypeLimits` batch call, replace the serial `for _, node := range nodes` loop with a concurrent version:
   - If `concurrency <= 0`, set `concurrency = 1` (serial fallback for tests).
   - Create a `sem := make(chan struct{}, concurrency)` semaphore.
   - Create a `var wg sync.WaitGroup` and `var mu sync.Mutex` for protecting `utils` and `skipped` slices.
   - For each node, `wg.Add(1)` then launch a goroutine that:
     - Acquires semaphore: `sem <- struct{}{}`
     - Defers `func() { <-sem; wg.Done() }()`
     - Calls `ListNodeENIs(ctx, api, node.InstanceID)`
     - On error: `mu.Lock(); skipped = append(...); mu.Unlock(); return`
     - On success: compute utilization (same logic as current), `mu.Lock(); utils = append(...); mu.Unlock()`
   - `wg.Wait()` after launching all goroutines.
4. Update Godoc to document the `concurrency` parameter.
5. Note: result order is non-deterministic with concurrency > 1. Callers already iterate results without depending on order.

**Acceptance Criteria:**
- `ComputeNodeUtilization` accepts a `concurrency int` parameter
- Concurrent execution uses semaphore pattern with `sync.WaitGroup`
- `concurrency <= 0` falls back to serial (1)
- Shared slices protected by `sync.Mutex`
- No new dependencies beyond stdlib `sync`

### Task 2: Update all callers and tests to pass concurrency parameter
**Files:** `cmd/eks/node.go`, `cmd/eks/cni.go`, `cmd/diagnose.go`, `pkg/aws/eni_test.go`
**Action:** modify
**Description:**
1. Update `cmd/eks/node.go:142`: add `10` as the last argument to `ComputeNodeUtilization`.
2. Update `cmd/eks/cni.go:150`: add `10` as the last argument.
3. Update `cmd/diagnose.go:348`: add `10` as the last argument.
4. Update all 7 `TestComputeNodeUtilization_*` test calls in `pkg/aws/eni_test.go` to pass `0` (serial) as the concurrency parameter. This ensures existing tests run deterministically.

**Acceptance Criteria:**
- All 3 callers pass `concurrency=10`
- All 7 existing tests pass `concurrency=0` and continue to pass
- `go build ./...` exits 0
- `go test ./...` exits 0

### Task 3: Add concurrency-specific tests
**Files:** `pkg/aws/eni_test.go`
**Action:** test
**Description:**
Add two new test functions after the existing `TestComputeNodeUtilization_*` tests:

1. `TestComputeNodeUtilization_ConcurrentMultiNode`: Create 5 nodes, each with different IPs. Use `concurrency=3`. Assert all 5 nodes appear in `utils` (order-independent). Verify results are correct by checking total count and that all node names are present.

2. `TestComputeNodeUtilization_ConcurrentPartialError`: Create 3 nodes. Mock `describeNetworkInterfaces` with a closure that inspects `params.Filters[0].Values[0]` (the instance ID) to dispatch: return an error for `"i-002"` (node-2) but succeed for `"i-001"` and `"i-003"`. Use `concurrency=3`. Assert `len(utils) == 2`, `len(skipped) == 1`, skipped node name is `"node-2"`. Verify that one node's failure does not abort the others.

**Acceptance Criteria:**
- Both new tests pass with `concurrency=3`
- Tests verify correct behavior under concurrent execution
- `go test -race ./pkg/aws/...` passes (no data races)

## Verification
```bash
cd /Users/lgbarn/Personal/kdiag
go build ./...
go test ./...
go test -race ./pkg/aws/...
go vet ./...
```
All commands exit 0. No data races detected.
