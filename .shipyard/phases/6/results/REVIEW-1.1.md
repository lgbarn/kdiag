# REVIEW-1.1: Add Concurrency to ComputeNodeUtilization

**Reviewer:** Claude (claude-sonnet-4-6)
**Date:** 2026-03-11
**Commits reviewed:** f312733, 1e86bae, 9cb27e3

---

## Stage 1: Spec Compliance

**Verdict:** PASS

### Task 1: Add concurrency parameter and goroutine pool to ComputeNodeUtilization

- Status: PASS
- Evidence: `pkg/aws/eni.go:152` — function signature matches the spec exactly: `func ComputeNodeUtilization(ctx context.Context, api EC2API, nodes []NodeInput, prefixDelegation bool, concurrency int) ([]NodeUtilization, []ENISkippedNode, error)`. The `"sync"` import is present at line 6. The `concurrency <= 0` guard is at line 157–159. The semaphore is `make(chan struct{}, concurrency)` at line 181. `var wg sync.WaitGroup` and `var mu sync.Mutex` are declared at lines 179–180. Loop variable capture `node := node` is at line 184. Semaphore acquire is `sem <- struct{}{}` at line 187, with deferred `<-sem; wg.Done()` at lines 189–191. `wg.Wait()` is at line 244.
- Notes: The `limits` map is populated before the goroutine loop and only read (never written) inside goroutines — this is a correct read-only shared access pattern that requires no additional synchronization. Godoc at lines 143–151 documents the `concurrency` parameter and the non-deterministic result ordering, as required.

### Task 2: Update all callers and tests to pass concurrency parameter

- Status: PASS
- Evidence:
  - `cmd/eks/node.go:142` — `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false, 10)` passes `10`.
  - `cmd/eks/cni.go:150` — `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, prefixDelegation, 10)` passes `10`.
  - `cmd/diagnose.go:348` — `awspkg.ComputeNodeUtilization(ctx, ec2Client, nodeInputs, false, 10)` passes `10`.
  - All 7 `TestComputeNodeUtilization_*` tests (lines 257, 295, 317, 339, 367, 393, 408 of `pkg/aws/eni_test.go`) pass `0` as the concurrency argument.
- Notes: `go build ./...` exits 0. `go test ./...` exits 0 with all packages passing.

### Task 3: Add concurrency-specific tests

- Status: PASS
- Evidence:
  - `TestComputeNodeUtilization_ConcurrentMultiNode` at `pkg/aws/eni_test.go:420` — creates 5 `NodeInput` entries, calls with `concurrency=3`, asserts `len(utils)==5` and `len(skipped)==0`, then performs an order-independent map lookup to verify all 5 node names are present.
  - `TestComputeNodeUtilization_ConcurrentPartialError` at `pkg/aws/eni_test.go:468` — creates 3 nodes, mock closure inspects `params.Filters[0].Values[0]` to dispatch: returns `node2Err` for `"i-002"`, succeeds for `"i-001"` and `"i-003"`. Calls with `concurrency=3`. Asserts `len(utils)==2`, `len(skipped)==1`, and `skipped[0].NodeName=="node-2"`.
  - `go test -race -count=5 ./pkg/aws/...` exits 0 with no data races detected (confirmed locally).

---

## Stage 2: Code Quality

### Critical

None.

### Important

- **`TestComputeNodeUtilization_ConcurrentPartialError` index access on `skipped` is only safe due to single-failure design** — `pkg/aws/eni_test.go:507`
  - The test asserts `skipped[0].NodeName` by direct index. This is safe here because exactly one node fails, so `len(skipped)==1` is asserted on line 504 before the index access on line 507. However, the assertion on line 504 uses `t.Fatalf` (which stops the test goroutine), so if it fails the index access is never reached. The pattern is technically correct but the intent would be clearer with an explicit `if len(skipped) >= 1` guard before line 507, or by searching the skipped slice by node name (as the multi-node test does for `utils`). This is a minor consistency issue: the multi-node test uses order-independent map lookup while this test uses direct index access.
  - Remediation: After the `t.Fatalf` guard, add a comment: `// len(skipped)==1 asserted above; index 0 is safe.` OR switch to a name-based search consistent with how `TestComputeNodeUtilization_ConcurrentMultiNode` verifies results order-independently.

- **Context cancellation does not unblock goroutines waiting to acquire the semaphore** — `pkg/aws/eni.go:187`
  - The semaphore acquire `sem <- struct{}{}` is a plain channel send with no `select`. If the caller's `ctx` is cancelled while goroutines are blocked waiting for a semaphore slot (e.g., all 10 slots occupied and the context deadline fires), those goroutines will remain blocked until a slot becomes free — they cannot react to `ctx.Done()`. In the current callers all use `context.WithTimeout`, so cancellation will eventually propagate through `ListNodeENIs` when it calls `DescribeNetworkInterfaces`, causing that call to return an error. The goroutine will then exit, freeing a slot. So in practice the cluster of blocked goroutines will drain correctly, just with some latency. However this is not documented and may surprise future maintainers.
  - Remediation: Either document the behavior explicitly in Godoc (e.g., "goroutines blocked on semaphore acquisition are not cancelled; context cancellation propagates through the API call itself"), or replace the plain send with a select:
    ```go
    select {
    case sem <- struct{}{}:
    case <-ctx.Done():
        mu.Lock()
        skipped = append(skipped, ENISkippedNode{NodeName: node.Name, Reason: ctx.Err().Error()})
        mu.Unlock()
        wg.Done()
        return
    }
    ```
    Note that if the select path is taken, `wg.Done()` must be called before returning because the deferred `wg.Done()` is inside the `defer func() { <-sem; wg.Done() }()` which only fires if the semaphore was acquired. The select approach requires restructuring the defer.

### Suggestions

- **The `skipped` slice is pre-allocated with `make([]ENISkippedNode, 0)` while `utils` uses `make([]NodeUtilization, 0, len(nodes))`** — `pkg/aws/eni.go:176–177`
  - `utils` benefits from a capacity hint, but `skipped` does not. This is a minor inconsistency. In the common case `skipped` is empty, so the missing capacity hint has negligible impact. For symmetry: `skipped := make([]ENISkippedNode, 0)` could become `skipped := make([]ENISkippedNode, 0, 0)` (no change in behavior) or just accept the inconsistency.
  - Remediation: No action required — this is purely cosmetic.

- **`TestComputeNodeUtilization_ConcurrentMultiNode` does not verify computed field values, only node name presence** — `pkg/aws/eni_test.go:420`
  - The test verifies all 5 node names appear in results but does not spot-check any `NodeUtilization` field values (e.g., `UtilizationPct`, `MaxTotalIPs`, `Status`). The serial tests cover field correctness, so this is not a gap in total coverage, but a concurrent test that also spot-checks one node's computed fields would give stronger confidence that the goroutine pool doesn't silently corrupt values.
  - Remediation: Add a check such as `if u.MaxTotalIPs != 30 { t.Errorf(...) }` for any one result entry inside the existing loop.

- **No test exercises `concurrency=1` explicitly as a serial-but-via-pool code path** — `pkg/aws/eni_test.go`
  - The existing 7 tests use `concurrency=0` (which falls back to 1 via the guard) and new tests use `concurrency=3`. A test that passes `concurrency=1` directly would cover the guard-bypass path and confirm the fallback description in Godoc is accurate without depending on the `<=0` guard.
  - Remediation: Add a brief one-node test with `concurrency=1` to explicitly document and exercise the boundary.

---

## Summary

**Verdict:** APPROVE

The implementation is correct and complete. All three tasks were delivered as specified: the goroutine pool uses the semaphore + WaitGroup + Mutex pattern exactly as designed, all three callers pass `concurrency=10`, all seven existing tests pass `concurrency=0`, and both new concurrency tests pass cleanly under the race detector with multiple runs. The build and vet are clean. One Important finding identifies a context-cancellation nuance that is benign in practice but worth documenting; the other Important finding is a minor test-consistency issue. No data-safety or correctness bugs were found.

Critical: 0 | Important: 2 | Suggestions: 3
