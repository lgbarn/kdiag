# Simplification Report
**Phase:** 6 — Concurrent ENI Utilization
**Date:** 2026-03-12T01:21:30Z
**Files analyzed:** 5 (`pkg/aws/eni.go`, `pkg/aws/eni_test.go`, `cmd/eks/node.go`, `cmd/eks/cni.go`, `cmd/diagnose.go`)
**Findings:** 3 medium, 2 low

---

## High Priority

No high-priority findings. The concurrency pattern is mechanically correct: no data races, no
goroutine leaks, and the mutex guards are properly scoped.

---

## Medium Priority

### Semaphore send before WaitGroup add is a latent ordering hazard

- **Type:** Refactor
- **Locations:** `pkg/aws/eni.go:185-191`
- **Description:** The goroutine sends into the semaphore channel (`sem <- struct{}{}`) as its
  first statement, *after* `wg.Add(1)` has already been called on the outside. This means the
  goroutine immediately blocks in the channel send if the pool is full, holding the goroutine
  open while doing no work. The conventional and safer pattern is to acquire the semaphore
  *before* spawning the goroutine so that the caller controls backpressure, or alternatively to
  acquire it immediately after spawn but before any defer. The current shape is not wrong, but
  the asymmetric placement (send on enter inside goroutine, receive on exit via defer) makes the
  flow harder to audit. More importantly, if the pool is saturated all N goroutines are spawned
  and parked inside the channel send — for a 1000-node cluster this creates 1000 goroutines
  waiting on a channel of depth 10, each consuming a goroutine stack.
- **Suggestion:** Move the semaphore acquire to the call site, before `go func()`:
  ```
  sem <- struct{}{}
  wg.Add(1)
  go func() {
      defer func() { <-sem; wg.Done() }()
      // ... work ...
  }()
  ```
  This bounds goroutine creation to the concurrency limit rather than just bounding active work.
- **Impact:** Reduces peak goroutine count from `len(nodes)` to `concurrency` for large clusters;
  no functional change for small inputs like the current tests.

### Hardcoded concurrency value at every call site

- **Type:** Refactor
- **Locations:** `cmd/eks/node.go:146`, `cmd/eks/cni.go:150`, `cmd/diagnose.go:348`
- **Description:** All three callers pass the literal `10` as the concurrency argument. This
  embeds a tuning decision in three places with no named constant and no documentation
  justifying the value. When a future caller is added, the author must know to pass `10` (or
  some other number) with no guidance. The `concurrency <= 0 → 1` guard inside
  `ComputeNodeUtilization` also exists solely to handle tests that pass `0` — a defensive path
  that exists because the API accepts a magic sentinel rather than having a named default.
- **Suggestion:** Two options, in order of preference:
  1. Define a package-level constant `DefaultConcurrency = 10` in `pkg/aws/eni.go` and use it
     at all three call sites. The `<= 0 → 1` guard can remain as a safety net.
  2. If the concurrency value is never expected to vary at runtime, remove the parameter entirely
     and hardcode `10` inside `ComputeNodeUtilization` with a comment referencing the rationale
     (e.g., "10 concurrent EC2 API calls respects AWS per-account rate limits").
- **Impact:** Removes three separate magic numbers; single point of change if the value needs
  tuning; makes the `0` sentinel in tests explicit (tests would then pass `1` or `DefaultConcurrency`).

---

## Low Priority

### Loop variable re-declaration is unnecessary in Go 1.22+

- **Locations:** `pkg/aws/eni.go:184`
- **Description:** `node := node` is the pre-Go 1.22 idiom to capture a loop variable in a
  goroutine closure. Go 1.22 fixed the loop variable semantics so each iteration gets its own
  variable. This module declares `go 1.25.0` in `go.mod`, so the re-declaration is dead code.
- **Suggestion:** Remove the `node := node` line. No behavioral change; reduces reader confusion
  about why it is there.
- **Impact:** 1 line removed; eliminates a stale pattern that causes readers to question whether
  the loop variable behavior was intentionally worked around.

### `TestComputeNodeUtilization_ConcurrentMultiNode` duplicates the inline ENI mock from `fixedIPsMock`

- **Locations:** `pkg/aws/eni_test.go:431-443` vs `pkg/aws/eni_test.go:216-229`
- **Description:** `TestComputeNodeUtilization_ConcurrentMultiNode` defines its own inline
  `describeNetworkInterfaces` function that is structurally identical to `fixedIPsMock(2)` — same
  device index, same single-ENI response, same 2-IP count. The only difference is the inline
  version does not use the `fixedIPsMock` helper. This is a test-internal duplication (2
  occurrences, below the Rule of Three threshold for mandatory extraction), but is worth noting
  because the two new concurrent tests are the only tests with multi-node inputs and both could
  use the existing helper.
- **Suggestion:** Replace the inline mock in `TestComputeNodeUtilization_ConcurrentMultiNode`
  with `fixedIPsMock(2)`. `TestComputeNodeUtilization_ConcurrentPartialError` cannot use
  `fixedIPsMock` because it needs instance-ID-aware dispatch, so no change there.
- **Impact:** 12 lines removed from the test file; reinforces that `fixedIPsMock` is the
  canonical single-ENI fixture.

---

## Summary

- **Duplication found:** 1 instance (magic `10` across 3 call sites; 1 test mock duplication)
- **Dead code found:** 1 (`node := node` loop capture, Go 1.25 module)
- **Complexity hotspots:** 0 functions exceeding thresholds
- **AI bloat patterns:** 1 (goroutine-per-item spawned before semaphore acquisition; a common
  pattern from generated code that is functionally correct but does not actually bound goroutine
  count)

**Estimated cleanup impact:** ~5 lines removed from production code; ~12 lines removed from
tests; 1 constant added.

---

## Recommendation

Simplification is recommended before the next phase but is not blocking. All findings are
mechanical and low-risk:

1. The semaphore ordering fix (medium) should be addressed before this code handles clusters with
   hundreds of nodes — the current shape creates `len(nodes)` goroutines regardless of the
   concurrency limit.
2. The `DefaultConcurrency` constant (medium) is a one-line change that prevents the magic `10`
   from spreading to future callers.
3. The `node := node` removal (low) and test mock dedup (low) are trivial cleanup.

None of the findings indicate incorrect behavior for the tested input sizes.
