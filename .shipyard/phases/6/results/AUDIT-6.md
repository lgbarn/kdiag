# Security Audit Report — Phase 6

## Executive Summary

**Verdict:** PASS
**Risk Level:** Low

Phase 6 adds concurrent goroutine execution to `ComputeNodeUtilization` using stdlib `sync` primitives — no new external dependencies were introduced. The implementation is sound: shared slice access is correctly guarded by a mutex, loop variable capture is handled, and all tests pass cleanly under Go's race detector across 20 runs. One noteworthy behavior — goroutines blocked on semaphore acquisition do not react to context cancellation without a slot becoming free first — is benign in practice because the underlying AWS API calls are context-aware and will drain the pool, but it merits a documentation note so future maintainers are not surprised. There are no exploitable vulnerabilities, no secret exposure, and no data race. The phase is safe to ship.

### What to Do

| Priority | Finding | Location | Effort | Action |
|----------|---------|----------|--------|--------|
| 1 | Semaphore acquisition ignores context cancellation | `pkg/aws/eni.go:187` | Trivial | Add Godoc note clarifying context propagates through the API call, not the sem acquire |
| 2 | Unbounded goroutine spawn proportional to node count | `pkg/aws/eni.go:183–186` | Small | Document the N-goroutine spawn behavior in Godoc; add an upper-bound guard if very large clusters are a design concern |
| 3 | Concurrency hardcoded at 10 in all callers | `cmd/eks/node.go:146`, `cmd/eks/cni.go:150`, `cmd/diagnose.go:348` | Small | Consider exposing as a CLI flag (e.g., `--concurrency`) so operators can tune for their environment |

### Themes
- Context cancellation handling is indirect: the pattern relies on the AWS SDK propagating cancellation through API calls rather than the semaphore select, which is safe today but undocumented.
- Concurrency is operational configuration that is currently compile-time fixed; it may benefit from CLI exposure.

---

## Detailed Findings

### Critical

None.

### Important

**[I1] Goroutines blocked on semaphore acquisition do not observe context cancellation**
- **Location:** `pkg/aws/eni.go:187`
- **Description:** The semaphore acquire `sem <- struct{}{}` is a plain unbuffered-to-bounded channel send with no `select { case ...: case <-ctx.Done(): }` guard. When `concurrency` goroutines are all executing API calls and additional goroutines are waiting to acquire a slot, a cancelled context cannot unblock those waiting goroutines directly. They remain parked until a running goroutine finishes and releases its slot via `<-sem` in the deferred function.
- **Impact:** In practice this is benign: the AWS SDK passes `ctx` to `DescribeNetworkInterfaces`, so the running goroutine will return quickly on cancellation, free its slot, and allow the next waiting goroutine to proceed — which will also return quickly from the context-aware API call. Drain time is bounded by `(N / concurrency) * per-call-overhead-after-cancel`. For a CLI diagnostic tool this is acceptable. However, if this function is ever embedded in a long-running service with strict cancellation semantics, the indirect draining behaviour would become a correctness concern (CWE-821: Improper Synchronization).
- **Remediation:** Add a Godoc comment to `ComputeNodeUtilization` stating: "Context cancellation propagates through the AWS API calls inside each goroutine, not through the semaphore acquisition; all goroutines will drain after cancellation but may take `ceil(N/concurrency)` API round-trips to do so." Alternatively, restructure the semaphore acquire to use a `select` — but note this requires moving `wg.Done()` out of the inner defer to ensure it is called on the cancel path.
- **Evidence:**
  ```go
  // pkg/aws/eni.go:186-191
  go func() {
      sem <- struct{}{}      // no ctx.Done() case here
      defer func() {
          <-sem
          wg.Done()
      }()
  ```

**[I2] Goroutine count is unbounded by concurrency — it equals len(nodes)**
- **Location:** `pkg/aws/eni.go:183–186`
- **Description:** The loop spawns one goroutine per node unconditionally (`go func()`), then each goroutine blocks on `sem <- struct{}{}`. The semaphore bounds concurrent *execution*, not concurrent *goroutine count*. With a 1,000-node cluster all 1,000 goroutines are created and parked immediately. Each parked goroutine costs approximately 2–8 KB of stack. At 1,000 nodes that is 2–8 MB; at 10,000 nodes it is 20–80 MB.
- **Impact:** For a diagnostic CLI querying real-world EKS clusters (typical node counts: 10–500) this is not a practical concern. However, the function has no documented upper bound, and if called against a very large cluster or in a future service context it could cause unexpected memory pressure. This is not an exploitable vulnerability.
- **Remediation:** Document in Godoc that `O(len(nodes))` goroutines are spawned. If very large clusters become a design concern, restructure to a worker-pool pattern where a fixed pool of `concurrency` goroutines consumes from a node channel, keeping goroutine count equal to `concurrency` rather than `len(nodes)`.
- **Evidence:**
  ```go
  // pkg/aws/eni.go:183-186
  for _, node := range nodes {
      node := node
      wg.Add(1)
      go func() {       // spawns len(nodes) goroutines, not min(len(nodes), concurrency)
          sem <- struct{}{}
  ```

### Advisory

- Concurrency value hardcoded at 10 in all three callers (`cmd/eks/node.go:146`, `cmd/eks/cni.go:150`, `cmd/diagnose.go:348`) — expose as a `--concurrency` CLI flag to let operators tune for their network/quota constraints without a code change.
- `limits` map is read concurrently inside goroutines without a mutex (`pkg/aws/eni.go:205`) — this is safe because `limits` is populated at line 171 before any goroutine is spawned and is never written inside the goroutine loop; this should be noted in a comment to prevent future maintainers from adding a write path without adding synchronization.
- No test exercises `concurrency=1` as an explicit path (bypassing the `<=0` guard) — `pkg/aws/eni_test.go` — add a single-node test with `concurrency=1` to pin the boundary behavior.

---

## Cross-Component Analysis

**Authentication and authorization:** No change. `ComputeNodeUtilization` accepts an `EC2API` interface; callers construct the AWS client using the configured profile and credentials chain inherited from previous phases. The concurrent goroutines use the same shared client, which is correct — the AWS SDK for Go v2 is designed to be used concurrently.

**Data flow:** The `limits` map is written once before any goroutines are started (line 171) and only read inside goroutines (line 205). The `utils` and `skipped` slices are written only inside `mu.Lock()` / `mu.Unlock()` guards (lines 195–200, 228–240). There is no unsynchronized shared mutable state. Go's race detector confirms this across 20 test runs.

**Error handling consistency:** Per-node errors are captured into `skipped` under the mutex, consistent with the serial implementation. The terminal error path (`GetInstanceTypeLimits` failure) still returns before any goroutines are launched, so no goroutine coordination is needed for the terminal case. This is correct.

**Context propagation:** All three callers (`runNode`, `runCNI`, `countExhaustedNodes`) obtain `ctx` from the cobra command and pass it through. The concurrent goroutines pass this same context to `ListNodeENIs`, which passes it to `DescribeNetworkInterfaces`. Cancellation is fully propagated to the AWS API boundary.

**No new attack surface:** The only new parameters are `concurrency int` (integer, clamped to 1 if ≤ 0) and the stdlib `sync` package. No new external dependencies, no new network listeners, no new file I/O, no credential handling changes.

---

## Analysis Coverage

| Area | Checked | Notes |
|------|---------|-------|
| Code Security (OWASP) | Yes | No injection, no sensitive data exposure; input is internal K8s API objects |
| Secrets & Credentials | Yes | No secrets in diff; no credentials in test fixtures |
| Dependencies | Yes | No new external dependencies — stdlib `sync` only |
| Infrastructure as Code | N/A | No Terraform, Ansible, or Docker changes |
| Docker/Container | N/A | No Dockerfile changes |
| Configuration | Yes | Concurrency hardcoded at 10; no debug mode or verbose error exposure changes |

---

## Dependency Status

No external dependencies added or changed in Phase 6. The only new import is `"sync"` from the Go standard library.

| Package | Version | Known CVEs | Status |
|---------|---------|-----------|--------|
| `sync` (stdlib) | Go 1.25 | None | OK |

---

## IaC Findings

No infrastructure-as-code changes in this phase.

| Resource | Check | Status |
|----------|-------|--------|
| N/A | N/A | N/A |
