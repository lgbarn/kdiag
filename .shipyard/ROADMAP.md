# kdiag — Concerns Cleanup Roadmap

This roadmap addresses 9 actionable concerns identified in the kdiag v0.3.0 codebase. The work is organized into three phases ordered by dependency: shared library foundations first, then concurrent performance work that depends on those foundations, then the remaining isolated fixes that share no file dependencies with phases 1–2.

---

## Phase 5: ENI Deduplication and Error Handling

**Scope:** Eliminate the three copies of ENI utilization logic spread across `cmd/eks/node.go`, `cmd/eks/cni.go`, and `cmd/diagnose.go`. Fix three error-handling bugs where API errors are silently swallowed, producing false-negative diagnostic results. Add IPv6 private-range support to `ClassifyIP`.

**Goals addressed:** 1, 3, 4, 5, 8

**Deliverables:**
- `pkg/aws/eni.go` — `ComputeNodeUtilization` shared function accepting classified nodes, instance-type limits, and ENI info; single `ExhaustedThreshold` constant (85); serial implementation in this phase (replaced in phase 6)
- `pkg/aws/eni_test.go` — tests for `ComputeNodeUtilization`
- `cmd/eks/node.go` — refactored to call shared function; utilization arithmetic removed
- `cmd/eks/cni.go` — refactored to call shared function; prefix-delegation multiplier passed as parameter
- `cmd/diagnose.go` — `countExhaustedNodes` refactored to call shared function
- `pkg/aws/endpoint.go` — `EnrichWithVpcEndpoints` signature changed to `(ctx, api, region, results) ([]EndpointCheckResult, error)`; error returned instead of swallowed at line 101
- `cmd/eks/endpoint.go` — call site updated to handle new error return; if EC2 enrichment fails, log warning and fall back to DNS-only results (preserving `APIEnriched: false`)
- `pkg/aws/endpoint_test.go` — test added: `TestEnrichWithVpcEndpoints_APIError` verifies error propagation
- `cmd/ingress.go` — `findIngressesForPod`: return `([]IngressRuleResult, []IngressTLSResult, error)` instead of `(nil, nil)` on `Services().List()` and `Ingresses().List()` failures; `checkControllerHealth`: distinguish API error from empty pod list — return `"controller API error: <err>"` instead of `"no controller pods found"` when `err != nil`
- `cmd/ingress_test.go` — tests for new error-distinguishing behavior in `checkControllerHealth` and `findIngressesForPod`
- `pkg/aws/endpoint.go` — `ClassifyIP`: add `fc00::/7` to the private-range checks (IPv6 unique local)
- `pkg/aws/endpoint_test.go` — table row added for `fd00::1` → `"private"` and `2001:db8::1` → `"public"`

**Success criteria:**
- `go test ./pkg/aws/... ./cmd/...` passes with all new tests green
- `go build ./...` passes
- `grep -rn "utilPct\|ExhaustedThreshold\|>= 85" cmd/` shows zero occurrences (threshold lives only in `pkg/aws/eni.go`)
- `grep -n "return results" pkg/aws/endpoint.go` shows zero occurrences in the error branch (error is returned)
- `findIngressesForPod` and `checkControllerHealth` in `cmd/ingress.go` have no bare `return nil, nil` on API error paths

**Risk:** Medium. The ENI deduplication requires touching three command files simultaneously. The shared function must preserve each caller's distinct behavior (CNI prefix-delegation multiplier, node command WARNING/EXHAUSTED two-tier, diagnose count-only). Tests for each caller's output must be written before the refactor to act as a regression harness.

---

## Phase 6: Concurrent ENI Queries

**Scope:** Replace the serial N+1 `DescribeNetworkInterfaces` loop in `cmd/eks/node.go` and `cmd/eks/cni.go` with a bounded goroutine pool. This phase depends on phase 5 completing first because the concurrent implementation wraps the shared `ComputeNodeUtilization` function introduced there.

**Goals addressed:** 2

**Deliverables:**
- `pkg/aws/eni.go` — `ListNodeENIsConcurrent(ctx, api, nodes []EligibleNode, concurrency int) (map[string]*NodeENIInfo, error)` function using a semaphore pattern (`make(chan struct{}, concurrency)`) with stdlib `sync.WaitGroup`; errors from individual nodes collected and surfaced (failed nodes logged, not fatal)
- `pkg/aws/eni_test.go` — `TestListNodeENIsConcurrent_*`: success with multiple nodes, one-node failure does not abort others, concurrency limit is respected (verify with a counter protected by `sync/atomic`)
- `cmd/eks/node.go` — `runNode` updated to call `ListNodeENIsConcurrent` with `concurrency=10`; per-node error handling preserves existing skip-and-warn behavior
- `cmd/eks/cni.go` — `runCNI` updated identically; prefix-delegation path unchanged

**Success criteria:**
- `go test ./pkg/aws/... ./cmd/eks/...` passes
- `go build ./...` passes
- `grep -n "ListNodeENIs(" cmd/eks/node.go cmd/eks/cni.go` returns zero hits (all call sites use the concurrent variant)
- No new imports outside stdlib `sync` and existing project packages

**Risk:** Low. The serial loop is straightforward to parallelize with a semaphore pattern. The existing mock (`mockEC2API`) supports concurrent calls without synchronization issues because each invocation is independent.

---

## Phase 7: Isolated Fixes

**Scope:** Three small, independent fixes with no file dependencies on phases 5–6: remove the redundant RBAC pre-flight in the shell ephemeral path, enforce the `--status`/`--show-pods` co-requirement, and handle the discarded write error in `cmd/eks/node.go`. These can be applied in any order within the phase.

**Goals addressed:** 6, 7, 9

**Deliverables:**
- `cmd/shell.go` — `runPodShell`: remove the second call to `CheckEphemeralContainerRBAC` in the `errors.IsForbidden` handler inside the `CreateEphemeralContainer` error block (lines 123–131); the error from the API is already descriptive; replace with a single formatted message referencing the original `checks` result computed at line 97
- `cmd/eks/node.go` — `init()`: add `nodeCmd.MarkFlagsMustBeUsedTogether` call or manual validation at the top of `runNode`: if `statusFilter != ""` and `!showPods`, return `fmt.Errorf("--status requires --show-pods")`
- `cmd/eks/node.go` — `runNode`: replace `_, _ = os.Stdout.WriteString(...)` at the bottom of the function with `_, err = os.Stdout.WriteString(...); if err != nil { return err }` (or use `fmt.Fprint` with error capture consistent with the rest of the file)
- `cmd/eks/node_test.go` (new or existing) — test: `--status EXHAUSTED` without `--show-pods` returns error containing `"--status requires --show-pods"`

**Success criteria:**
- `go test ./cmd/... ./cmd/eks/...` passes
- `go build ./...` passes
- `grep -n "CheckEphemeralContainerRBAC" cmd/shell.go` returns exactly one hit (the initial pre-flight at line 97, not the duplicate in the error handler)
- Running `kdiag eks node --status EXHAUSTED` (without `--show-pods`) produces exit code 1 and a message containing `"--status requires --show-pods"`
- `grep -n "_, _" cmd/eks/node.go` returns zero hits

**Risk:** Low. Each fix is contained to a single function. The RBAC cleanup is a deletion — no logic is added. The flag validation is pure guard-clause addition.

---

## Dependency Order

```
Phase 5  (ENI dedup + error handling + IPv6)
    |
Phase 6  (concurrent ENI pool — uses shared function from phase 5)

Phase 7  (isolated fixes — no dependency on 5 or 6, can start anytime)
```

Phases 5 and 7 can begin concurrently. Phase 6 must wait for phase 5.

---

## Overall Success Criteria

- `go build ./...` passes with zero errors
- `go test ./...` passes with all new tests green and no regressions
- ENI utilization threshold defined in exactly one place (`pkg/aws/eni.go`)
- `EnrichWithVpcEndpoints` errors visible in CLI output
- `findIngressesForPod` and `checkControllerHealth` do not conflate API errors with empty results
- `DescribeNetworkInterfaces` calls are concurrent across both `node` and `cni` subcommands
- `--status` without `--show-pods` produces a clear, actionable error
- IPv6 unique-local addresses classified as private by `ClassifyIP`
